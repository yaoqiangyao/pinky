package android

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

const (
	guidPixel = "adb-R5CN20ABC-x1y2z3"
	guidOther = "adb-EMULATOR36X4X9X0-nW9LNN"
)

func newReconnectFake(t *testing.T) (*fakeADB, *Provider) {
	f := startFakeADB(t)
	cfg := f.config()
	cfg.ReconnectWiFi = true
	cfg.KnownHosts = func() ([]string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.Clone(f.known), nil
	}
	old := reconnectSearch
	reconnectSearch = 300 * time.Millisecond
	t.Cleanup(func() { reconnectSearch = old })
	return f, NewProvider(cfg)
}

func (f *fakeADB) counts() (browses int, connects []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.browses, slices.Clone(f.connects)
}

func entries(t *testing.T, p *Provider) []adb.DeviceEntry {
	t.Helper()
	e, err := p.cfg.Client.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestReconnectServerBlind: a paired phone is on the network, but the adb
// server's discovery lists nothing, so it will not connect it. The driver
// connects it by address, once.
func TestReconnectServerBlind(t *testing.T) {
	f, p := newReconnectFake(t)
	ctx := context.Background()
	f.set(func(f *fakeADB) {
		f.known = []string{guidPixel, guidOther}
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
	})
	away, connected := p.wifi.reconnect(ctx, entries(t, p))
	if !connected || !away {
		t.Errorf("reconnect = away %v, connected %v", away, connected)
	}
	browses, connects := f.counts()
	if browses != 1 || len(connects) != 1 || connects[0] != "192.168.1.42:41000" {
		t.Fatalf("browses %d, connects %q", browses, connects)
	}
	// Connected now: the next pass looks only for the other phone.
	away, connected = p.wifi.reconnect(ctx, entries(t, p))
	if connected || !away {
		t.Errorf("second pass = away %v, connected %v", away, connected)
	}
	if browses, connects := f.counts(); browses != 2 || len(connects) != 1 {
		t.Errorf("second pass: browses %d, connects %q", browses, connects)
	}
	// With the other one gone from the paired phones, nothing is left to do.
	f.set(func(f *fakeADB) { f.known = []string{guidPixel} })
	if away, connected := p.wifi.reconnect(ctx, entries(t, p)); away || connected {
		t.Errorf("third pass = away %v, connected %v", away, connected)
	}
	if browses, _ := f.counts(); browses != 2 {
		t.Errorf("searched with every phone connected: %d", browses)
	}
}

// TestReconnectPhoneOnWiFiAlready: a paired phone that is connected on the
// computer's network at an address the reconnector does not know (adb
// tcpip's port, another tool's, a Pinky that ran before, its IPv6 address)
// gets no other connection; on a cable or through Tailscale only, it gets
// one, as the server's discovery would give it.
func TestReconnectPhoneOnWiFiAlready(t *testing.T) {
	const props = "Pixel 9\n17\n37\nPixel\nR5CN20ABC\n"
	for _, tc := range []struct {
		name, serial, extra string
		connects            bool
	}{
		{"adb tcpip's port", "192.168.1.42:5555", "", false},
		{"its IPv6 address", "[2408:8207:1234::5]:41000", "", false},
		{"a cable", "R5CN20ABC", "usb:1-1 ", true},
		{"Tailscale", "100.101.102.103:5555", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p := newReconnectFake(t)
			f.set(func(f *fakeADB) {
				f.known = []string{guidPixel}
				f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
				f.devices = []string{tc.serial + "\tdevice " + tc.extra + "product:x model:Pixel_9 device:x transport_id:3"}
				f.props = map[string]string{tc.serial: props}
			})
			p.wifi.reconnect(context.Background(), entries(t, p))
			if _, connects := f.counts(); (len(connects) == 1) != tc.connects || len(connects) > 1 {
				t.Errorf("connects %q, want a connection: %v", connects, tc.connects)
			}
		})
	}
}

// TestReconnectHiddenSerial: a connection the reconnector made for a
// paired phone whose shell reads no serial is listed under the serial in
// the phone's guid.
func TestReconnectHiddenSerial(t *testing.T) {
	f, p := newReconnectFake(t)
	f.set(func(f *fakeADB) {
		f.known = []string{guidPixel}
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
		f.props = map[string]string{"192.168.1.42:41000": "Pixel 9\n17\n37\nPixel\n\npinky-boot=\npinky-aid=9774d56d682e549c\n"}
	})
	if _, connected := p.wifi.reconnect(context.Background(), entries(t, p)); !connected {
		t.Fatal("not connected")
	}
	infos, err := p.List(context.Background())
	if err != nil || len(infos) != 1 || infos[0].ID != "R5CN20ABC" {
		t.Fatalf("listed %+v %v, want the phone as R5CN20ABC", infos, err)
	}
}

// TestReconnectLeavesItToTheServer: phones the server's discovery lists, or
// that it connected itself, are its own; the driver neither searches nor
// connects.
func TestReconnectLeavesItToTheServer(t *testing.T) {
	f, p := newReconnectFake(t)
	ctx := context.Background()
	f.set(func(f *fakeADB) {
		f.known = []string{guidPixel, guidOther}
		f.services = []string{guidPixel + "\t_adb-tls-connect._tcp.\t192.168.1.42:41000"}
		f.devices = []string{guidOther + "._adb-tls-connect._tcp\tdevice product:x model:sdk device:x transport_id:3"}
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
	})
	if away, connected := p.wifi.reconnect(ctx, entries(t, p)); away || connected {
		t.Errorf("reconnect = away %v, connected %v", away, connected)
	}
	if browses, connects := f.counts(); browses != 0 || len(connects) != 0 {
		t.Errorf("browses %d, connects %q", browses, connects)
	}
	// An offline connection of the server's does not count.
	f.set(func(f *fakeADB) {
		f.services = nil
		f.devices = []string{guidOther + "._adb-tls-connect._tcp\toffline transport_id:3", guidPixel + "._adb-tls-connect._tcp\tdevice transport_id:4"}
		f.own = []string{guidOther + "\t_adb-tls-connect._tcp\t192.168.1.7:43210"}
	})
	if _, connected := p.wifi.reconnect(ctx, entries(t, p)); !connected {
		t.Error("the phone whose connection is offline was not reconnected")
	}
	if _, connects := f.counts(); len(connects) != 1 || connects[0] != "192.168.1.7:43210" {
		t.Errorf("connects %q", connects)
	}
}

// TestReconnectRefused: a phone that refuses the connection is tried
// again at that address only after a long wait, growing each time, and at
// once at a new address.
func TestReconnectRefused(t *testing.T) {
	f, p := newReconnectFake(t)
	ctx := context.Background()
	now := time.Unix(1_000_000, 0)
	p.wifi.now = func() time.Time { return now }
	f.set(func(f *fakeADB) {
		f.known = []string{guidPixel}
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
		f.connectOK = map[string]bool{}
	})
	tries := func() int { _, c := f.counts(); return len(c) }
	p.wifi.reconnect(ctx, nil)
	if tries() != 1 {
		t.Fatalf("tries = %d", tries())
	}
	now = now.Add(4 * time.Minute)
	if away, _ := p.wifi.reconnect(ctx, nil); !away || tries() != 1 {
		t.Errorf("tried again after 4 minutes: %d", tries())
	}
	now = now.Add(2 * time.Minute)
	p.wifi.reconnect(ctx, nil)
	if tries() != 2 {
		t.Errorf("not tried again after 6 minutes: %d", tries())
	}
	now = now.Add(10 * time.Minute) // the second wait is 15 minutes
	p.wifi.reconnect(ctx, nil)
	if tries() != 2 {
		t.Errorf("tried again 10 minutes after the second refusal: %d", tries())
	}
	// Wireless debugging was turned off and on: a new port.
	f.set(func(f *fakeADB) {
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:42000"}
		f.connectOK["192.168.1.42:42000"] = true
	})
	if _, connected := p.wifi.reconnect(ctx, nil); !connected || tries() != 3 {
		t.Errorf("the new address: connected %v, tries %d", connected, tries())
	}
	p.wifi.mu.Lock()
	defer p.wifi.mu.Unlock()
	if len(p.wifi.refused) != 0 {
		t.Errorf("refusals kept after the connection: %v", p.wifi.refused)
	}
}

// TestReconnectServerQuits: when the adb server quits on a connection (it
// can crash on a failed TLS handshake), the phone is left alone for hours.
func TestReconnectServerQuits(t *testing.T) {
	f, p := newReconnectFake(t)
	now := time.Unix(1_000_000, 0)
	p.wifi.now = func() time.Time { return now }
	f.set(func(f *fakeADB) {
		f.known = []string{guidPixel}
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
		f.quitOnConnect = true
	})
	if away, connected := p.wifi.reconnect(context.Background(), nil); !away || connected {
		t.Errorf("reconnect = away %v, connected %v", away, connected)
	}
	p.wifi.mu.Lock()
	defer p.wifi.mu.Unlock()
	if r := p.wifi.refused[guidPixel]; r.until.Sub(now) != refusedMax {
		t.Errorf("refusal = %+v", r)
	}
}

// TestReconnectNoPairedPhones: a computer that never paired a phone over
// Wi-Fi does not search the network.
func TestReconnectNoPairedPhones(t *testing.T) {
	f, p := newReconnectFake(t)
	if away, connected := p.wifi.reconnect(context.Background(), nil); away || connected {
		t.Errorf("reconnect = away %v, connected %v", away, connected)
	}
	if browses, _ := f.counts(); browses != 0 {
		t.Errorf("searched %d times", browses)
	}
}

// TestReconnectSchedule: passes back off while a paired phone stays away,
// up to two minutes, and come soon again once a phone comes or goes.
func TestReconnectSchedule(t *testing.T) {
	f, p := newReconnectFake(t)
	f.set(func(f *fakeADB) { f.known = []string{guidPixel} })
	r := p.wifi
	var mu sync.Mutex
	now := time.Unix(1_000_000, 0)
	r.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	settle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			r.mu.Lock()
			running := r.running
			r.mu.Unlock()
			if !running {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the pass did not finish")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waits := func() time.Duration {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.next.Sub(now)
	}
	var got []time.Duration
	for range 6 {
		r.kick(nil)
		settle()
		got = append(got, waits())
		advance(waits())
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 2 * time.Minute, 2 * time.Minute}
	if !slices.Equal(got, want) {
		t.Errorf("waits = %v, want %v", got, want)
	}
	if browses, _ := f.counts(); browses != 6 {
		t.Errorf("browses = %d", browses)
	}
	// A phone came: the next pass is due in five seconds.
	r.kick(nil)
	settle()
	advance(time.Second)
	r.kick([]adb.DeviceEntry{{Serial: "emulator-5554", State: adb.StateDevice}})
	if w := waits(); w != 5*time.Second {
		t.Errorf("after a change the next pass is in %v", w)
	}
	// And the phone on the network is connected at that pass.
	f.set(func(f *fakeADB) { f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"} })
	advance(5 * time.Second)
	r.kick([]adb.DeviceEntry{{Serial: "emulator-5554", State: adb.StateDevice}})
	settle()
	if _, connects := f.counts(); len(connects) != 1 {
		t.Errorf("connects = %q", connects)
	}
}

// TestListReconnects: List starts the passes by itself.
func TestListReconnects(t *testing.T) {
	f, p := newReconnectFake(t)
	f.set(func(f *fakeADB) {
		f.known = []string{guidPixel}
		f.own = []string{guidPixel + "\t_adb-tls-connect._tcp\t192.168.1.42:41000"}
	})
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		infos, err := p.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(infos) == 1 && infos[0].ID == "192.168.1.42:41000" && infos[0].Connection == "wifi" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("List = %+v", infos)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Without ReconnectWiFi, List leaves the network alone.
	cfg := f.config()
	p2 := NewProvider(cfg)
	f.set(func(f *fakeADB) { f.devices, f.browses = nil, 0 })
	if _, err := p2.List(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if browses, _ := f.counts(); browses != 0 {
		t.Errorf("searched without ReconnectWiFi: %d", browses)
	}
}

func TestParseKnownHosts(t *testing.T) {
	// The Mac's own file: an emulator, then the Mi twice.
	raw := "\x0a\x1d\x0a\x1badb-EMULATOR36X4X9X0-nW9LNN" +
		"\x0a\x15\x0a\x13adb-3e8be48e-kIY2HE" +
		"\x0a\x15\x0a\x13adb-3e8be48e-kIY2HE"
	got, err := parseKnownHosts([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "adb-EMULATOR36X4X9X0-nW9LNN,adb-3e8be48e-kIY2HE" {
		t.Errorf("guids = %q", got)
	}
	// Unknown fields of every wire type are skipped.
	extra := "\x08\x96\x01" + "\x11" + "12345678" + "\x1d" + "1234" + "\x0a\x09\x10\x01\x0a\x05adb-x"
	if got, err := parseKnownHosts([]byte(extra)); err != nil || len(got) != 1 || got[0] != "adb-x" {
		t.Errorf("with other fields: %q, %v", got, err)
	}
	if _, err := parseKnownHosts([]byte("\x0a\x40short")); err == nil {
		t.Error("a cut-off file parsed")
	}
}

func TestReadKnownHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, err := readKnownHosts(); err != nil || len(got) != 0 {
		t.Errorf("without the file: %q, %v", got, err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".android"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".android", "adb_known_hosts.pb"), []byte("\x0a\x15\x0a\x13adb-3e8be48e-kIY2HE"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got, err := readKnownHosts(); err != nil || len(got) != 1 || got[0] != "adb-3e8be48e-kIY2HE" {
		t.Errorf("readKnownHosts = %q, %v", got, err)
	}
}
