package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/tailnet"
)

// Phones reached over the network without Wireless debugging. A phone's
// adb can listen on the network at a fixed port (adb tcpip, set up over
// the cable or another connection), on every network the phone joins, for
// the computers it trusts, until it restarts. Pinky uses that for two
// things, which share this code:
//
//   - Wi-Fi for phones without Wireless debugging (Android 10, wifi.go):
//     the provider connects at the phone's Wi-Fi address, and again
//     whenever the phone is back on the network, finding a new address
//     with mDNS (adbd announces _adb._tcp while it listens).
//   - Phones away from the computer's network (tailnet.go): the provider
//     connects at the phone's Tailscale address. Wireless debugging needs
//     the phone on Wi-Fi, turns itself off when the phone leaves the
//     network it was turned on for (Android 11 to 16), takes a new port
//     each time it comes on, and is found with mDNS, which does not cross
//     a VPN; Tailscale encrypts the way there.
//
// The driver keeps such phones and their addresses in Config.AwayFile and
// connects to each address that has no connection yet, also while the
// phone has another one (a cable, Wireless debugging at home), so that
// unplugging it or leaving home leaves it connected. List names a phone
// kept here that has no connection at all as offline, and says why.

// AwayPort is where a phone's adb listens for Pinky on the network: adb's
// own port for `adb tcpip`.
const AwayPort = 5555

// netPhone is a phone whose adb Pinky had listen on the network, and the
// addresses Pinky reaches it at.
type netPhone struct {
	ID   string `json:"id"`             // the phone's id, its hardware serial
	Name string `json:"name,omitempty"` // for the logs and while it is offline
	Port int    `json:"port,omitempty"`
	// LAN is its address on the computer's network, for a phone moved to
	// Wi-Fi (SetPhoneWiFi); "" when Pinky does not reach it there this way.
	LAN string `json:"lan,omitempty"`
	// Node and Tailnet are its Tailscale node id and address, for a phone
	// used away from home (SetAway); "" when it is not.
	Node    string `json:"node,omitempty"`
	Tailnet string `json:"tailnet,omitempty"`
}

func (n netPhone) port() int {
	if n.Port <= 0 {
		return AwayPort
	}
	return n.Port
}

// lanSerial and tailSerial are the adb serials of its connections on the
// computer's network and through Tailscale, "" when it has no such
// address.
func (n netPhone) lanSerial() string  { return joinAddr(n.LAN, n.port()) }
func (n netPhone) tailSerial() string { return joinAddr(n.Tailnet, n.port()) }

func joinAddr(host string, port int) string {
	if host == "" {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// netStore keeps the phones reached over the network in a file, or in
// memory when it has no path.
type netStore struct {
	path string
	logf func(string, ...any)

	mu     sync.Mutex
	loaded bool
	phones []netPhone
}

type netFile struct {
	Phones []netPhone `json:"phones"`
}

func (s *netStore) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logf("android: reading the phones Pinky reaches over the network: %v", err)
		}
		return
	}
	var f netFile
	if err := json.Unmarshal(b, &f); err != nil {
		s.logf("android: reading %s: %v", s.path, err)
		return
	}
	for _, n := range f.Phones {
		if n.ID != "" && (n.LAN != "" || n.Tailnet != "") {
			s.phones = append(s.phones, n)
		}
	}
}

func (s *netStore) list() []netPhone {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return slices.Clone(s.phones)
}

func (s *netStore) get(id string) (netPhone, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	for _, n := range s.phones {
		if n.ID == id {
			return n, true
		}
	}
	return netPhone{}, false
}

// update changes what is kept about a phone (fn gets the zero netPhone
// with its id when none is) and saves it; a phone left with no address is
// forgotten.
func (s *netStore) update(id string, fn func(n *netPhone)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	i := slices.IndexFunc(s.phones, func(n netPhone) bool { return n.ID == id })
	n := netPhone{ID: id}
	if i >= 0 {
		n = s.phones[i]
	}
	old := n
	fn(&n)
	n.ID = id
	switch {
	case n == old && i >= 0:
		return nil
	case n.LAN == "" && n.Tailnet == "":
		if i < 0 {
			return nil
		}
		s.phones = slices.Delete(s.phones, i, i+1)
	case i >= 0:
		s.phones[i] = n
	default:
		s.phones = append(s.phones, n)
	}
	return s.saveLocked()
}

func (s *netStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(netFile{Phones: s.phones}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// --- the phone's side ---

// phoneAddrs returns the phone's IPv4 addresses on Wi-Fi and its
// Tailscale addresses, from `ip -o addr show`.
func (p *Provider) phoneAddrs(ctx context.Context, serial string) (wifi netip.Addr, tail []netip.Addr, err error) {
	out, err := p.cfg.Client.Shell(ctx, serial, "ip -o addr show 2>/dev/null")
	if err != nil {
		return netip.Addr{}, nil, wrapErr(err)
	}
	wifi, tail = parsePhoneAddrs(string(out))
	return wifi, tail, nil
}

// parsePhoneAddrs reads `ip -o addr show`:
//
//	30: wlan0    inet 192.168.5.137/24 brd 192.168.5.255 scope global wlan0\       valid_lft 6974sec ...
//	31: tun0    inet 100.101.2.2/32 scope global tun0\       valid_lft forever preferred_lft forever
//
// It returns the first IPv4 address of a wlan interface, and the
// Tailscale addresses, IPv4 first. Carriers give phones addresses in
// Tailscale's 100.64.0.0/10 too, so only a VPN's interface (tun*) counts,
// or one with an address in Tailscale's own IPv6 range.
func parsePhoneAddrs(out string) (wifi netip.Addr, tail []netip.Addr) {
	type iface struct {
		addrs []netip.Addr
		ours  bool
	}
	var order []string
	ifs := map[string]*iface{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || (f[2] != "inet" && f[2] != "inet6") {
			continue
		}
		pfx, err := netip.ParsePrefix(f[3])
		if err != nil {
			continue
		}
		name, a := f[1], pfx.Addr().Unmap()
		if strings.HasPrefix(name, "wlan") && a.Is4() && !wifi.IsValid() {
			wifi = a
		}
		if !tailnet.Contains(a) {
			continue
		}
		in := ifs[name]
		if in == nil {
			in = &iface{}
			ifs[name] = in
			order = append(order, name)
		}
		in.addrs = append(in.addrs, a)
		if strings.HasPrefix(name, "tun") || a.Is6() {
			in.ours = true
		}
	}
	var v4, v6 []netip.Addr
	for _, name := range order {
		if !ifs[name].ours {
			continue
		}
		for _, a := range ifs[name].addrs {
			if a.Is4() {
				v4 = append(v4, a)
			} else {
				v6 = append(v6, a)
			}
		}
	}
	return wifi, append(v4, v6...)
}

// listenPort returns the port the phone's adb listens on the network at
// (adb tcpip), 0 when it does not.
func (p *Provider) listenPort(ctx context.Context, serial string) int {
	out, err := p.cfg.Client.Shell(ctx, serial, "getprop service.adb.tcp.port")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return max(n, 0)
}

// netConns are a phone's online connections, by kind.
type netConns struct {
	usb   string // by cable
	local string // over Wi-Fi on the computer's network: Wireless debugging, or adb tcpip at its address
	tail  string // through Tailscale
}

// any returns a connection to run a command over: the cable first.
func (c netConns) any() string {
	for _, s := range []string{c.usb, c.local, c.tail} {
		if s != "" {
			return s
		}
	}
	return ""
}

// conns returns the phone's online connections.
func (p *Provider) conns(ctx context.Context, entries []adb.DeviceEntry, id string) netConns {
	var c netConns
	for _, e := range entries {
		if e.State != adb.StateDevice {
			continue
		}
		pr, err := p.deviceProps(ctx, e)
		if err != nil || (e.Serial != id && p.phoneID(e, pr) != id) {
			continue
		}
		switch {
		case viaTailnet(e.Serial):
			c.tail = e.Serial
		case !e.Wireless():
			c.usb = e.Serial
		case c.local == "" || isAddr(e.Serial):
			// The connection at an address before Wireless debugging's.
			c.local = e.Serial
		}
	}
	return c
}

// listen has the phone's adb listen on the network at AwayPort, over a
// connection it has, unless it does already.
func (p *Provider) listen(ctx context.Context, id, serial string) error {
	if p.listenPort(ctx, serial) == AwayPort {
		return nil
	}
	p.cfg.logf("android: %s: having adb listen on the network at port %d", id, AwayPort)
	if err := p.cfg.Client.TCPIP(ctx, serial, AwayPort); err != nil {
		return fmt.Errorf("android: %s: adb tcpip: %w", id, wrapErr(err))
	}
	return nil
}

// stopListening has the phone's adb stop listening on the network, over
// the first connection it has, and drops its connections at addresses
// Pinky kept.
func (p *Provider) stopListening(ctx context.Context, id string, n netPhone) error {
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return wrapErr(err)
	}
	c := p.conns(ctx, entries, id)
	var errs []error
	if s := c.any(); s != "" {
		p.cfg.logf("android: %s: adb stops listening on the network", id)
		if err := p.cfg.Client.USB(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("android: %s: adb usb: %w", id, wrapErr(err)))
		}
	}
	for _, s := range []string{n.lanSerial(), n.tailSerial()} {
		if s == "" {
			continue
		}
		if err := p.cfg.Client.Disconnect(ctx, s); err != nil && !errors.Is(err, adb.ErrDeviceNotFound) {
			p.cfg.logf("android: disconnecting %s: %v", s, err)
		}
	}
	return errors.Join(errs...)
}

// upWait is how long connectWhenUp tries.
var upWait = 20 * time.Second

// connectWhenUp connects to a phone whose adb is restarting to listen on
// the network, trying for up to upWait, and waits for it to come online.
func (p *Provider) connectWhenUp(ctx context.Context, serial string) error {
	ctx, cancel := context.WithTimeout(ctx, upWait)
	defer cancel()
	for {
		err := p.cfg.Client.Connect(ctx, serial)
		if err == nil {
			_, err = p.waitConnected(ctx, serial)
			return err
		}
		if sleepCtx(ctx, time.Second) != nil {
			return connectErr(serial, err)
		}
	}
}

// waitConnected waits for the connection with this serial to come online,
// and returns the phone. A phone that asks the person to allow this
// computer is returned as it is (unauthorized) once it has asked for a
// while: the person may be away from it.
func (p *Provider) waitConnected(ctx context.Context, serial string) (driver.Info, error) {
	var asking time.Time
	for {
		entries, err := p.cfg.Client.Devices(ctx)
		if err != nil {
			return driver.Info{}, wrapErr(err)
		}
		for _, e := range entries {
			if e.Serial != serial {
				continue
			}
			switch e.State {
			case adb.StateDevice:
				return p.infoFor(ctx, serial)
			case adb.StateUnauthorized:
				if asking.IsZero() {
					asking = time.Now()
				}
				if time.Since(asking) > 10*time.Second {
					return infoFromEntry(e), nil
				}
			}
		}
		if err := sleepCtx(ctx, pairPoll); err != nil {
			return driver.Info{}, fmt.Errorf("android: adb connected to %s, but the phone did not come online: %w", serial, driver.ErrOffline)
		}
	}
}

// connectErr words a failed connection at an address: refused means the
// phone is there but its adb does not listen at that port.
func connectErr(serial string, err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "refused") {
		return fmt.Errorf("android: %s: %v: %w", serial, err, driver.ErrNotListening)
	}
	return wrapErr(err)
}

// serialHost is the host of a connection made by address, "" for any
// other.
func serialHost(serial string) string {
	h, _, err := net.SplitHostPort(serial)
	if err != nil {
		return ""
	}
	return h
}

// viaTailnet reports whether a connection goes through Tailscale: it was
// made by address, to a Tailscale address.
func viaTailnet(serial string) bool {
	a, err := netip.ParseAddr(serialHost(serial))
	return err == nil && tailnet.Contains(a)
}

// legacyInstance reports whether an mDNS instance of adb tcpip's service
// (_adb._tcp) is the phone's: "adb-<serial>", or on Android 11 and later
// the Wireless debugging guid "adb-<serial>-<suffix>".
func legacyInstance(instance, id string) bool {
	return id != "" && (instance == "adb-"+id || guidSerial(instance) == id)
}

// --- offline phones ---

// netInfos names the connections at a kept address that are not online
// after their phone, and adds the kept phones that have no connection at
// all as offline, with why, so that the person sees them and what to do.
func (p *Provider) netInfos(infos []driver.Info) []driver.Info {
	phones := p.net.list()
	if len(phones) == 0 {
		return infos
	}
	online := map[string]bool{}
	for _, in := range infos {
		if in.State == driver.StateOnline {
			online[in.ID] = true
		}
	}
	for _, n := range phones {
		if p.forgot(n.ID) || online[n.ID] {
			continue
		}
		setup, detail := p.link.why(n)
		found := false
		for i := range infos {
			if in := &infos[i]; in.ID == n.lanSerial() || in.ID == n.tailSerial() {
				in.ID, in.Name, in.Model = n.ID, nonEmpty(n.Name, n.ID), nonEmpty(n.Name, n.ID)
				in.State, in.Setup, in.Detail = driver.StateOffline, setup, detail
				found = true
			}
		}
		if found {
			continue
		}
		info := driver.Info{ID: n.ID, Platform: driver.Android, Name: nonEmpty(n.Name, n.ID), Model: nonEmpty(n.Name, n.ID),
			Connection: "wifi", Backend: "scrcpy", State: driver.StateOffline, Setup: setup, Detail: detail}
		if n.LAN == "" {
			info.Via = driver.ViaTailscale
		}
		infos = append(infos, info)
	}
	return infos
}

// --- reconnecting ---

// Reconnecting the phones kept here. List starts a pass now and then; a
// pass connects each kept address that has no connection: the address on
// the computer's network (or a new one mDNS finds), and the Tailscale
// address while Tailscale says the phone is online.

const (
	netSoon  = 3 * time.Second  // after a phone came or went, or one was set up
	netAway  = 20 * time.Second // while a kept address has no connection
	netIdle  = time.Minute      // while every one has
	netFirst = 10 * time.Second // the wait after a failed connection, doubled each time
	netMax   = 2 * time.Minute
)

// netLook is how long a pass looks for a phone's adb on the network when
// its address does not answer: a dozing phone may take five seconds to
// answer mDNS.
var netLook = 6 * time.Second

// probeWait is how long a pass waits for a kept address to take a TCP
// connection before it asks the adb server to connect there: the server
// waits for the system's own timeout (75 s on macOS) at an address
// nothing answers, and answers nothing else meanwhile.
var probeWait = 3 * time.Second

// probe checks that something takes TCP connections at an address. adbd
// takes the connection and closes it once it sees nothing come, without
// a prompt on the phone. It returns an error wrapping
// driver.ErrNotListening when the phone answered but its adb does not
// listen there.
func probe(ctx context.Context, serial string) error {
	d := net.Dialer{Timeout: probeWait}
	c, err := d.DialContext(ctx, "tcp", serial)
	if err != nil {
		return connectErr(serial, err)
	}
	return c.Close()
}

type netLink struct {
	p     *Provider
	now   func() time.Time
	probe func(ctx context.Context, serial string) error // tests replace it

	mu      sync.Mutex
	running bool
	next    time.Time
	online  string
	tries   map[string]netTry // failed connections, by serial
	noted   map[string]string
}

// netTry is the last failed connection at an address.
type netTry struct {
	count   int
	until   time.Time // no new try before then
	refused bool      // the phone answered, but its adb does not listen
}

func newNetLink(p *Provider) *netLink {
	return &netLink{p: p, now: time.Now, probe: probe, tries: map[string]netTry{}, noted: map[string]string{}}
}

// soon has the next pass come in a few seconds.
func (t *netLink) soon() {
	t.mu.Lock()
	if n := t.now().Add(netSoon); t.next.After(n) {
		t.next = n
	}
	t.mu.Unlock()
}

// kick starts a pass in the background when one is due.
func (t *netLink) kick(entries []adb.DeviceEntry) {
	phones := t.p.net.list()
	if len(phones) == 0 {
		return
	}
	now := t.now()
	online := inUseSerials(entries)
	t.mu.Lock()
	if online != t.online {
		t.online = online
		if n := now.Add(netSoon); t.next.After(n) {
			t.next = n
		}
	}
	if t.running || now.Before(t.next) {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.mu.Unlock()
	go t.pass(phones, entries)
}

func (t *netLink) pass(phones []netPhone, entries []adb.DeviceEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	missing := t.reconnect(ctx, phones, entries)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = false
	wait := netIdle
	if missing {
		wait = netAway
	}
	t.next = t.now().Add(wait)
}

// reconnect connects the kept addresses that have no connection. It
// reports whether one is still without.
func (t *netLink) reconnect(ctx context.Context, phones []netPhone, entries []adb.DeviceEntry) (missing bool) {
	p := t.p
	var st *tailnet.Status
	for _, n := range phones {
		if p.forgot(n.ID) {
			continue
		}
		if n.LAN != "" && !serialInUse(entries, n.lanSerial()) && !t.lan(ctx, n, entries) {
			missing = true
		}
		if n.Tailnet == "" || serialInUse(entries, n.tailSerial()) {
			continue
		}
		if st == nil {
			s, err := p.tailnetStatus(ctx)
			if err != nil {
				t.note("", "android: reading Tailscale's status: "+err.Error())
			}
			st = &s
		}
		if !st.Running() {
			t.note("", "android: Tailscale is not running on this computer ("+string(st.State)+"); the phones set up for away from home wait for it")
			missing = true
			continue
		}
		t.note("", "")
		if !t.tail(ctx, n, *st, entries) {
			missing = true
		}
	}
	return missing
}

// lan connects a phone at its address on the computer's network. A phone
// connected by cable says its Wi-Fi address; one that does not answer at
// its address may have another, which mDNS finds while its adb listens.
func (t *netLink) lan(ctx context.Context, n netPhone, entries []adb.DeviceEntry) bool {
	p := t.p
	if c := p.conns(ctx, entries, n.ID); c.usb != "" {
		if wifi, _, err := p.phoneAddrs(ctx, c.usb); err == nil && wifi.IsValid() && wifi.String() != n.LAN {
			n = t.moved(n, wifi.String())
			if serialInUse(entries, n.lanSerial()) {
				return true
			}
		}
	}
	serial := n.lanSerial()
	if t.holdBack(serial) {
		return false
	}
	err := t.connect(ctx, serial)
	if err == nil {
		t.ok(n.ID, serial, "android: connected to "+n.ID+" over Wi-Fi at "+serial)
		return true
	}
	// The phone may have another address now (the old one answering for
	// another device, or nothing).
	if addr := t.look(n); addr != "" && serialHost(addr) != n.LAN {
		if err2 := t.connect(ctx, addr); err2 == nil {
			n = t.moved(n, serialHost(addr))
			t.ok(n.ID, n.lanSerial(), "android: connected to "+n.ID+" over Wi-Fi at its new address "+n.lanSerial())
			return true
		}
	}
	t.fail(n.ID, serial, err, "over Wi-Fi")
	return false
}

// connect has the adb server connect at an address once a probe found
// something listening there.
func (t *netLink) connect(ctx context.Context, serial string) error {
	if err := t.probe(ctx, serial); err != nil {
		return err
	}
	if err := t.p.cfg.Client.Connect(ctx, serial); err != nil {
		return connectErr(serial, err)
	}
	return nil
}

// moved keeps a phone's new address on the computer's network.
func (t *netLink) moved(n netPhone, addr string) netPhone {
	t.p.cfg.logf("android: %s's Wi-Fi address changed from %s to %s", n.ID, n.LAN, addr)
	if err := t.p.net.update(n.ID, func(m *netPhone) {
		if m.LAN != "" {
			m.LAN = addr
		}
	}); err != nil {
		t.p.cfg.logf("android: keeping %s's new address: %v", n.ID, err)
	}
	n.LAN = addr
	return n
}

// look searches the network for the phone's adb (_adb._tcp) for a moment,
// and returns where it listens.
func (t *netLink) look(n netPhone) string {
	b, err := t.p.browse(adb.ServiceLegacy)
	if err != nil {
		return ""
	}
	defer b.Close()
	deadline := time.Now().Add(netLook)
	for time.Now().Before(deadline) {
		for _, s := range b.Services() {
			if s.Type == adb.ServiceLegacy && legacyInstance(s.Instance, n.ID) && s.Addr != "" {
				return s.Addr
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return ""
}

// tail connects a phone at its Tailscale address while Tailscale says it
// is online, keeping a new address Tailscale gave it.
func (t *netLink) tail(ctx context.Context, n netPhone, st tailnet.Status, entries []adb.DeviceEntry) bool {
	p := t.p
	d, ok := st.Device(n.Node)
	if !ok {
		d, ok = st.Device(n.Tailnet)
	}
	if ok && d.IPv4().IsValid() && d.IPv4().String() != n.Tailnet {
		// Tailscale gave the phone a new address (it signed in again).
		p.cfg.logf("android: %s's Tailscale address changed from %s to %s", n.ID, n.Tailnet, d.IPv4())
		n.Tailnet = d.IPv4().String()
		if err := p.net.update(n.ID, func(m *netPhone) {
			if m.Tailnet != "" {
				m.Tailnet = n.Tailnet
			}
		}); err != nil {
			p.cfg.logf("android: keeping %s's new address: %v", n.ID, err)
		}
		if serialInUse(entries, n.tailSerial()) {
			return true
		}
	}
	if ok && !d.Online {
		t.note(n.ID+"/tail", "android: "+n.ID+" is set up for away from home, but Tailscale says it is offline (its Tailscale is off, or it has no network)")
		return false
	}
	serial := n.tailSerial()
	if t.holdBack(serial) {
		return false
	}
	if err := t.connect(ctx, serial); err != nil {
		t.fail(n.ID, serial, err, "through Tailscale")
		return false
	}
	t.ok(n.ID+"/tail", serial, "android: connected to "+n.ID+" through Tailscale at "+serial)
	return true
}

// forgive drops what is known of failed connections at an address, as
// the person just set it up again.
func (t *netLink) forgive(serial string) {
	t.mu.Lock()
	delete(t.tries, serial)
	t.mu.Unlock()
}

func (t *netLink) holdBack(serial string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.tries[serial]
	return ok && t.now().Before(f.until)
}

func (t *netLink) ok(key, serial, msg string) {
	t.mu.Lock()
	delete(t.tries, serial)
	t.mu.Unlock()
	t.note(key, msg)
}

// fail notes a failed connection and when to try that address again.
func (t *netLink) fail(id, serial string, err error, how string) {
	refused := errors.Is(err, driver.ErrNotListening)
	t.mu.Lock()
	f := t.tries[serial]
	f.count++
	wait := netFirst
	for i := 1; i < f.count && wait < netMax; i++ {
		wait *= 2
	}
	wait = min(wait, netMax)
	f.until, f.refused = t.now().Add(wait), refused
	t.tries[serial] = f
	t.mu.Unlock()
	why := err.Error()
	if refused {
		why = "its adb does not listen on the network (the phone restarted, or someone turned it off)"
	}
	t.note(id+"/"+serial, fmt.Sprintf("android: connecting to %s %s at %s: %s; trying again in %v", id, how, serial, why, wait))
}

// refused reports whether the phone's adb refused the last connection at
// one of its kept addresses, and none answered since.
func (t *netLink) refused(n netPhone) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range []string{n.lanSerial(), n.tailSerial()} {
		if f, ok := t.tries[s]; ok && f.refused {
			return true
		}
	}
	return false
}

// why says why a kept phone has no connection, as Info.Setup and Detail.
func (t *netLink) why(n netPhone) (setup, detail string) {
	if t.refused(n) {
		return driver.SetupNetworkOff, "Its adb stopped listening on the network, as it does when the phone restarts: plug it in once and turn its Wi-Fi connection on again"
	}
	if n.LAN == "" {
		return driver.SetupNetworkAway, "Not reachable through Tailscale now; Pinky connects again by itself once Tailscale says the phone is online"
	}
	return driver.SetupNetworkAway, "Not on this network now; Pinky connects again by itself once the phone is back"
}

// note logs a message under a key when it differs from the last one; ""
// only clears the last one.
func (t *netLink) note(key, msg string) {
	t.mu.Lock()
	same := t.noted[key] == msg
	t.noted[key] = msg
	t.mu.Unlock()
	if !same && msg != "" {
		t.p.cfg.logf("%s", msg)
	}
}
