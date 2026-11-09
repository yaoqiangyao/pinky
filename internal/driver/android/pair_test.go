package android

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/android/mdns"
)

// fakeADB is an adb server with a phone that pairs over Wi-Fi. It is also
// the driver's own network search (Browser), which can see what the
// server's discovery does not.
type fakeADB struct {
	t  *testing.T
	ln net.Listener

	mu            sync.Mutex
	services      []string // the server's mDNS services, "instance\ttype\taddr"
	noMDNS        bool     // the server has no mDNS discovery (host:mdns:* fail)
	code          string   // the code or password the phone accepts
	guid          string   // reported by pair; "" for an old server
	devices       []string // host:devices-l lines
	connects      []string
	disconnects   []string // host:disconnect arguments, with the port the server adds
	pairs         int
	autoSerial    string            // a serial the server adds by itself after pairing
	props         map[string]string // getprop output by serial; none fails the shell
	connectOK     map[string]bool   // addresses host:connect reaches; nil: all
	quitOnConnect bool              // the server crashes on host:connect, as platform-tools 37 can
	slowDevices   time.Duration     // how long host:devices-l takes, as on a busy computer
	connectsOff   bool              // a phone host:connect reaches stays offline

	known     []string // the phones the server paired with (adb_known_hosts.pb)
	own       []string // what the driver's search sees, as services
	ownErr    error    // what the driver's search reports
	browseErr error    // Browse fails
	browses   int
	closes    int

	// run answers the other services on a phone's connection (shell
	// commands, tcpip:, usb:), by serial; false: unsupported.
	run func(serial, service string) (string, bool)
	// unreachable addresses time out on host:connect, as a phone that
	// left the network does.
	unreachable map[string]bool
	probes      []string // addresses the network code probed
}

func newFakeADB(t *testing.T) (*fakeADB, *Provider) {
	f := startFakeADB(t)
	return f, NewProvider(f.config())
}

func startFakeADB(t *testing.T) *fakeADB {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeADB{t: t, ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	return f
}

func (f *fakeADB) config() Config {
	return Config{Client: &adb.Client{Addr: f.ln.Addr().String()}, Browser: f, Logf: f.t.Logf}
}

func (f *fakeADB) set(fn func(f *fakeADB)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func readRequest(r *bufio.Reader) (string, bool) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return "", false
	}
	size, _ := strconv.ParseUint(string(n[:]), 16, 32)
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", false
	}
	return string(buf), true
}

func (f *fakeADB) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	req, ok := readRequest(r)
	if !ok {
		return
	}
	reply := func(s string) { fmt.Fprintf(c, "OKAY%04x%s", len(s), s) }
	fail := func(msg string) { fmt.Fprintf(c, "FAIL%04x%s", len(msg), msg) }
	if serial, ok := strings.CutPrefix(req, "host:transport:"); ok {
		f.mu.Lock()
		out, has := f.props[serial]
		run := f.run
		f.mu.Unlock()
		if !has {
			fail("device '" + serial + "' not found")
			return
		}
		io.WriteString(c, "OKAY")
		next, ok := readRequest(r)
		switch {
		case ok && strings.HasPrefix(next, "shell:getprop ro.product.model"):
			io.WriteString(c, "OKAY"+out)
		case ok && run != nil:
			if out, ok := run(serial, next); ok {
				io.WriteString(c, "OKAY"+out)
				return
			}
			fail("unsupported")
		default:
			fail("unsupported")
		}
		return
	}
	if req == "host:devices-l" {
		f.mu.Lock()
		slow := f.slowDevices
		f.mu.Unlock()
		time.Sleep(slow)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case req == "host:mdns:check":
		if f.noMDNS {
			// As a real server started with ADB_MDNS=0.
			reply("ERROR: mdns discovery disabled")
			return
		}
		reply("mdns daemon version [adb discovery 0.0.0]")
	case req == "host:mdns:services":
		if f.noMDNS {
			fail("mdns daemon unavailable")
			return
		}
		reply(strings.Join(f.services, "\n") + "\n")
	case strings.HasPrefix(req, "host:pair:"):
		code, addr, _ := strings.Cut(strings.TrimPrefix(req, "host:pair:"), ":")
		f.pairs++
		// As the real server: OKAY, then a second status once it is over.
		if code != f.code {
			msg := "Failed: Wrong password or connection was dropped."
			fmt.Fprintf(c, "OKAYFAIL%04x%s", len(msg), msg)
			return
		}
		io.WriteString(c, "OKAY")
		if f.autoSerial != "" {
			f.devices = append(f.devices, f.autoSerial+"\tdevice product:x model:Pixel_9 device:x transport_id:7")
		}
		if f.guid == "" {
			reply("Successfully paired to " + addr)
			return
		}
		reply("Successfully paired to " + addr + " [guid=" + f.guid + "]")
	case strings.HasPrefix(req, "host:connect:"):
		addr := strings.TrimPrefix(req, "host:connect:")
		f.connects = append(f.connects, addr)
		if f.quitOnConnect {
			f.ln.Close()
			return
		}
		if f.unreachable[addr] {
			reply("failed to connect to '" + addr + "': Operation timed out")
			return
		}
		if f.connectOK != nil && !f.connectOK[addr] {
			reply("failed to connect to '" + addr + "': Connection refused")
			return
		}
		if f.connectsOff {
			f.devices = append(f.devices, addr+"\toffline transport_id:8")
		} else {
			f.devices = append(f.devices, addr+"\tdevice product:x model:Pixel_9 device:x transport_id:8")
		}
		reply("connected to " + addr)
	case strings.HasPrefix(req, "host:disconnect:"):
		// As platform-tools 37: the argument is an address, with port 5555
		// when it has none, so a name is never found.
		addr := strings.TrimPrefix(req, "host:disconnect:")
		if !strings.Contains(addr, ":") {
			addr += ":5555"
		}
		f.disconnects = append(f.disconnects, addr)
		for i, line := range f.devices {
			if strings.HasPrefix(line, addr+"\t") {
				f.devices = append(f.devices[:i:i], f.devices[i+1:]...)
				reply("disconnected " + addr)
				return
			}
		}
		fail("no such device '" + addr + "'")
	case req == "host:devices-l":
		reply(strings.Join(f.devices, "\n") + "\n")
	default:
		fail("unsupported")
	}
}

// Browse implements Browser.
func (f *fakeADB) Browse(types ...string) (Browse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.browseErr != nil {
		return nil, f.browseErr
	}
	f.browses++
	return &fakeBrowse{f: f, types: types}, nil
}

type fakeBrowse struct {
	f     *fakeADB
	types []string
}

func (b *fakeBrowse) Services() []adb.MDNSService {
	b.f.mu.Lock()
	defer b.f.mu.Unlock()
	var out []adb.MDNSService
	for _, s := range adb.ParseMDNSServices(strings.Join(b.f.own, "\n")) {
		for _, t := range b.types {
			if s.Type == t {
				out = append(out, s)
			}
		}
	}
	return out
}

func (b *fakeBrowse) Err() error {
	b.f.mu.Lock()
	defer b.f.mu.Unlock()
	return b.f.ownErr
}

func (b *fakeBrowse) Close() error {
	b.f.mu.Lock()
	b.f.closes++
	b.f.mu.Unlock()
	return nil
}

func TestQRPairing(t *testing.T) {
	f, p := newFakeADB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg, err := p.StartPairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	qr := pg.QR()
	name, pass, ok := parseQR(qr)
	if !ok || !strings.HasPrefix(name, "pinky-") || len(pass) < 12 {
		t.Fatalf("QR() = %q", qr)
	}
	f.set(func(f *fakeADB) {
		f.code = pass
		f.guid = "adb-R5CN20ABC-x1y2z3"
	})
	// The phone shows up a moment after the person scans the code.
	go func() {
		time.Sleep(700 * time.Millisecond)
		f.set(func(f *fakeADB) {
			f.services = []string{
				"pinky-other\t_adb-tls-pairing._tcp.\t192.168.1.9:30000",
				name + "\t_adb-tls-pairing._tcp.\t192.168.1.42:37123",
				"adb-R5CN20ABC-x1y2z3\t_adb-tls-connect._tcp.\t192.168.1.42:41000",
			}
		})
	}()
	info, err := pg.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "192.168.1.42:41000" || info.Connection != "wifi" || info.State != driver.StateOnline {
		t.Errorf("Wait = %+v", info)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connects) != 1 || f.connects[0] != "192.168.1.42:41000" {
		t.Errorf("connects = %q", f.connects)
	}
}

// TestQRPairingServerBlind is the bug this search fixes: the adb server's
// discovery has stopped and lists nothing, while the phone's services are
// on the network. Wait pairs with the address the driver found, connects
// at once (the server will not), and the phone keeps its hardware serial
// as its id.
func TestQRPairingServerBlind(t *testing.T) {
	f, p := newFakeADB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg, err := p.StartPairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, pass, _ := parseQR(pg.QR())
	rep := pg.(driver.PairingReporter)
	if pr := rep.Progress(); pr.Found || pr.Paired || pr.NotAllowed || pr.SearchErr != "" {
		t.Errorf("progress before the scan = %+v", pr)
	}
	f.set(func(f *fakeADB) {
		f.code, f.guid = pass, "adb-R5CN20ABC-x1y2z3"
		f.props = map[string]string{"192.168.1.42:41000": "Pixel 9\n16\n36\nPixel 9\nR5CN20ABC\n"}
	})
	go func() {
		time.Sleep(300 * time.Millisecond)
		f.set(func(f *fakeADB) {
			f.own = []string{
				name + "\t_adb-tls-pairing._tcp\t192.168.1.42:37123",
				"adb-R5CN20ABC-x1y2z3\t_adb-tls-connect._tcp\t192.168.1.42:41000",
			}
		})
	}()
	start := time.Now()
	info, err := pg.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "R5CN20ABC" || info.Name != "Pixel 9" || info.Connection != "wifi" || info.State != driver.StateOnline {
		t.Errorf("Wait = %+v", info)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
	if pr := rep.Progress(); !pr.Found || !pr.Paired {
		t.Errorf("progress after = %+v", pr)
	}
	pg.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connects) != 1 || f.connects[0] != "192.168.1.42:41000" {
		t.Errorf("connects = %q", f.connects)
	}
	if f.browses != 1 || f.closes != 1 {
		t.Errorf("browses %d, closes %d", f.browses, f.closes)
	}
}

// TestQRPairingWithoutServerMDNS pairs with a server that has no mDNS at
// all, by the driver's own search.
func TestQRPairingWithoutServerMDNS(t *testing.T) {
	f, p := newFakeADB(t)
	f.set(func(f *fakeADB) { f.noMDNS = true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg, err := p.StartPairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	name, pass, _ := parseQR(pg.QR())
	f.set(func(f *fakeADB) {
		f.code = pass // an old server: no guid
		f.own = []string{
			name + "\t_adb-tls-pairing._tcp\t192.168.1.42:37123",
			"adb-R5CN20ABC-x1y2z3\t_adb-tls-connect._tcp\t192.168.1.42:41000",
		}
	})
	info, err := pg.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "192.168.1.42:41000" {
		t.Errorf("Wait = %+v", info)
	}
}

// TestQRPairingCannotSearch: with no discovery in the server and no search
// of its own, the driver cannot pair by QR code.
func TestQRPairingCannotSearch(t *testing.T) {
	f, p := newFakeADB(t)
	f.set(func(f *fakeADB) {
		f.noMDNS = true
		f.browseErr = errors.New("no sockets")
	})
	if _, err := p.StartPairing(context.Background()); !errors.Is(err, driver.ErrUnsupported) {
		t.Errorf("StartPairing = %v", err)
	}
	// Without a server at all.
	f.ln.Close()
	if _, err := p.StartPairing(context.Background()); !errors.Is(err, driver.ErrUnsupported) {
		t.Errorf("StartPairing without a server = %v", err)
	}
}

// TestPairingProgressNotAllowed reports a search the system blocks (macOS
// Local Network privacy) until the phone shows up.
func TestPairingProgressNotAllowed(t *testing.T) {
	f, p := newFakeADB(t)
	f.set(func(f *fakeADB) {
		f.ownErr = fmt.Errorf("%w: the questions never came back", mdns.ErrNotAllowed)
	})
	pg, err := p.StartPairing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	pr := pg.(driver.PairingReporter).Progress()
	if !pr.NotAllowed || !strings.Contains(pr.SearchErr, "not allowed") {
		t.Errorf("progress = %+v", pr)
	}
	f.set(func(f *fakeADB) { f.ownErr = errors.New("mdns: no network interface to search") })
	if pr := pg.(driver.PairingReporter).Progress(); pr.NotAllowed || pr.SearchErr == "" {
		t.Errorf("progress without a network = %+v", pr)
	}
}

func parseQR(qr string) (name, pass string, ok bool) {
	rest, ok := strings.CutPrefix(qr, "WIFI:T:ADB;")
	if !ok || !strings.HasSuffix(rest, ";;") {
		return "", "", false
	}
	for _, field := range strings.Split(strings.TrimSuffix(rest, ";;"), ";") {
		k, v, _ := strings.Cut(field, ":")
		switch k {
		case "S":
			name = v
		case "P":
			pass = v
		}
	}
	return name, pass, name != "" && pass != ""
}

func TestQRPairingClose(t *testing.T) {
	f, p := newFakeADB(t)
	pg, err := p.StartPairing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		pg.Close()
	}()
	if _, err := pg.Wait(context.Background()); err == nil {
		t.Error("Wait after Close returned no error")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closes != 1 {
		t.Errorf("the search was closed %d times", f.closes)
	}
}

func TestPairWithCode(t *testing.T) {
	f, p := newFakeADB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.set(func(f *fakeADB) {
		f.code = "482913"
		f.guid = "adb-R5CN20ABC-x1y2z3"
		// A current adb server connects by itself after pairing.
		f.autoSerial = "adb-R5CN20ABC-x1y2z3._adb-tls-connect._tcp"
	})
	if _, err := p.PairWithCode(ctx, "192.168.1.42:37123", "12345"); err == nil {
		t.Error("a five-digit code was accepted")
	}
	if _, err := p.PairWithCode(ctx, "192.168.1.42", "482913"); err == nil {
		t.Error("an address without a port was accepted")
	}
	if _, err := p.PairWithCode(ctx, "192.168.1.42:99999", "482913"); err == nil {
		t.Error("a port out of range was accepted")
	}
	if _, err := p.PairWithCode(ctx, "192.168.1.42:37123", "000000"); !errors.Is(err, adb.ErrPairing) || !errors.Is(err, driver.ErrPairRejected) ||
		!strings.Contains(err.Error(), "Wrong password or connection was dropped") {
		t.Errorf("wrong code: %v", err)
	}
	info, err := p.PairWithCode(ctx, " 192.168.1.42:37123 ", "482913")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "adb-R5CN20ABC-x1y2z3._adb-tls-connect._tcp" || info.Connection != "wifi" {
		t.Errorf("PairWithCode = %+v", info)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connects) != 0 {
		t.Errorf("connected by hand although the server had: %q", f.connects)
	}
}

// TestPairError says why adb could not pair from its words and how long
// it took, as adb 37 answers (checked with refused, closed, TLS and silent
// addresses): a refused connection, the phone ending the pairing, an
// address that does not answer, and a server without host:pair.
func TestPairError(t *testing.T) {
	for _, c := range []struct {
		msg  string
		took time.Duration
		want error
	}{
		{"Unable to start pairing client.", 0, driver.ErrPairNotListening},
		{"Wrong password or connection was dropped.", 40 * time.Millisecond, driver.ErrPairRejected},
		{"Wrong password or connection was dropped.", 5040 * time.Millisecond, driver.ErrPairUnreachable},
		{"unknown host service", 0, driver.ErrPairOldTool},
		{"Successfully paired but server returned unknown response=", 0, nil},
	} {
		err := pairError(fmt.Errorf("%w: %s", adb.ErrPairing, c.msg), "192.168.1.42:37123", c.took)
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%q after %v: %v, want %v", c.msg, c.took, err, c.want)
		}
		for _, other := range []error{driver.ErrPairNotListening, driver.ErrPairRejected, driver.ErrPairUnreachable, driver.ErrPairOldTool} {
			if other != c.want && errors.Is(err, other) {
				t.Errorf("%q after %v: %v is also %v", c.msg, c.took, err, other)
			}
		}
		if !errors.Is(err, adb.ErrPairing) || !strings.Contains(err.Error(), c.msg) || !strings.Contains(err.Error(), "192.168.1.42:37123") {
			t.Errorf("%q: %v lost adb's words or the address", c.msg, err)
		}
	}
	if err := pairError(context.DeadlineExceeded, "192.168.1.42:37123", time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a deadline: %v", err)
	}
}

// TestPairWithCodeServerBlind connects by hand to the connect service the
// driver's own search found, when the server's discovery sees nothing.
func TestPairWithCodeServerBlind(t *testing.T) {
	f, p := newFakeADB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.set(func(f *fakeADB) {
		f.code, f.guid = "482913", "adb-R5CN20ABC-x1y2z3"
		f.own = []string{"adb-R5CN20ABC-x1y2z3\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
	})
	info, err := p.PairWithCode(ctx, "192.168.1.42:37123", "482913")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "192.168.1.42:41000" {
		t.Errorf("PairWithCode = %+v", info)
	}
}

// TestPairWithCodeNotFound: the phone paired, but its connect service shows
// up nowhere; the error says so, and ConnectWiFi then connects with the
// address the person reads off the phone.
func TestPairWithCodeNotFound(t *testing.T) {
	f, p := newFakeADB(t)
	old := connectTimeout
	connectTimeout = 1500 * time.Millisecond
	defer func() { connectTimeout = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.set(func(f *fakeADB) { f.code, f.guid = "482913", "adb-R5CN20ABC-x1y2z3" })
	_, err := p.PairWithCode(ctx, "192.168.1.42:37123", "482913")
	if !errors.Is(err, driver.ErrPairedNotFound) {
		t.Fatalf("PairWithCode = %v", err)
	}
	if _, err := p.ConnectWiFi(ctx, "the phone"); err == nil {
		t.Error("ConnectWiFi took a bad address")
	}
	f.set(func(f *fakeADB) { f.connectOK = map[string]bool{"192.168.1.42:41000": true} })
	if _, err := p.ConnectWiFi(ctx, "192.168.1.42:5555"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("ConnectWiFi to a closed port = %v", err)
	}
	info, err := p.ConnectWiFi(ctx, " 192.168.1.42:41000 ")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "192.168.1.42:41000" || info.State != driver.StateOnline {
		t.Errorf("ConnectWiFi = %+v", info)
	}
}

// TestPairWaitEndsInRequest: when the wait for a paired or connected phone
// runs out during a request to a slow adb server, rather than between two,
// the phone is still not found, or not online, as when it runs out between
// them; not the request's timeout.
func TestPairWaitEndsInRequest(t *testing.T) {
	f, p := newFakeADB(t)
	old := connectTimeout
	connectTimeout = time.Second
	defer func() { connectTimeout = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Each listing takes 400 ms: the second starts at about 0.9 s and is
	// cut at 1 s.
	f.set(func(f *fakeADB) {
		f.code, f.guid, f.slowDevices = "482913", "adb-R5CN20ABC-x1y2z3", 400*time.Millisecond
	})
	if _, err := p.PairWithCode(ctx, "192.168.1.42:37123", "482913"); !errors.Is(err, driver.ErrPairedNotFound) {
		t.Errorf("PairWithCode = %v", err)
	}
	// Connected, but never online.
	f.set(func(f *fakeADB) { f.connectsOff = true })
	if _, err := p.ConnectWiFi(ctx, "192.168.1.42:41000"); !errors.Is(err, driver.ErrOffline) {
		t.Errorf("ConnectWiFi = %v", err)
	}
	// The person leaving still ends it as such.
	gone, stop := context.WithCancel(ctx)
	stop()
	if _, err := p.ConnectWiFi(gone, "192.168.1.42:41000"); errors.Is(err, driver.ErrOffline) {
		t.Errorf("ConnectWiFi after the person left = %v", err)
	}
}

// lateCtx is a context whose deadline has passed while Err does not say
// so yet, as a timeout's timer lags the connection deadline set from it.
type lateCtx struct{ context.Context }

func (lateCtx) Deadline() (time.Time, bool) { return time.Now().Add(-time.Millisecond), true }

// TestWaitRanOut tells a wait's own timeout from the caller ending it.
func TestWaitRanOut(t *testing.T) {
	bg := context.Background()
	over, stop := context.WithTimeout(bg, -time.Second)
	defer stop()
	gone, cancel := context.WithCancel(bg)
	cancel()
	goneWait, stop2 := context.WithTimeout(gone, -time.Second)
	defer stop2()
	waiting, stop3 := context.WithTimeout(bg, time.Hour)
	defer stop3()
	for _, c := range []struct {
		name         string
		parent, wait context.Context
		want         bool
	}{
		{"its timeout", bg, over, true},
		{"its deadline, before Err says so", bg, lateCtx{bg}, true},
		{"still waiting", bg, waiting, false},
		{"the caller left", gone, goneWait, false},
		{"the caller's deadline", over, over, false},
	} {
		if got := waitRanOut(c.parent, c.wait); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestConnectAddr(t *testing.T) {
	svcs := []adb.MDNSService{
		{Instance: "adb-A-1", Type: adb.ServiceConnect, Addr: "192.168.1.5:40000"},
		{Instance: "adb-B-2", Type: adb.ServiceConnect, Addr: "192.168.1.42:41000"},
		{Instance: "pinky-x", Type: adb.ServicePairing, Addr: "192.168.1.42:37123"},
	}
	if got := connectAddr(svcs, "adb-B-2", "10.0.0.1"); got != "192.168.1.42:41000" {
		t.Errorf("by guid = %q", got)
	}
	if got := connectAddr(svcs, "", "192.168.1.42"); got != "192.168.1.42:41000" {
		t.Errorf("by host = %q", got)
	}
	if got := connectAddr(svcs, "adb-C-3", "192.168.1.42"); got != "" {
		t.Errorf("unknown guid = %q", got)
	}
}
