package android

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// Reconnecting paired phones over Wi-Fi (Config.ReconnectWiFi). The adb
// server connects by itself to the phones it paired with when their
// connect service shows up on the network, but only while its own
// discovery works. After the computer's network changes it sees nothing
// until it restarts, so a phone whose connection dropped (it slept, or
// left the network for a while) stays away. List then looks for the paired
// phones that are not connected with the driver's own search, and connects
// to them by address, as the server would.

// reconnectSearch is how long a pass searches the network: a dozing phone
// may take five seconds to answer.
var reconnectSearch = 6 * time.Second

const (
	reconnectSoon  = 5 * time.Second // the wait after a phone came or went, or one was connected
	reconnectFirst = 10 * time.Second
	reconnectMax   = 2 * time.Minute // the longest wait while a paired phone stays away
	reconnectIdle  = time.Minute     // the wait while every paired phone is connected

	// A phone that refused a connection (it no longer trusts this
	// computer, say) is tried again at the same address after these waits,
	// tripled each time. The adb server (platform-tools 37) can crash when
	// a TLS handshake fails, taking every other tool's connections down, so
	// a phone it quit on is left alone for the longest wait at once.
	refusedFirst = 5 * time.Minute
	refusedMax   = 6 * time.Hour
)

// connectSuffix ends the serials of the server's own Wi-Fi connections,
// "adb-<serial>-<suffix>._adb-tls-connect._tcp".
const connectSuffix = "." + adb.ServiceConnect

type reconnector struct {
	p   *Provider
	now func() time.Time

	mu      sync.Mutex
	running bool
	next    time.Time         // no pass starts before then
	wait    time.Duration     // the wait after a pass that left a phone away
	online  string            // the serials in use at the last List
	byAddr  map[string]string // the addresses the driver connected to, with their guids
	noted   map[string]string // the last thing logged about a guid
	refused map[string]refusal
}

// refusal is a phone's last failed connection, by guid.
type refusal struct {
	addr  string
	count int
	until time.Time // no new try at addr before then
}

func newReconnector(p *Provider) *reconnector {
	return &reconnector{p: p, now: time.Now, byAddr: map[string]string{}, noted: map[string]string{},
		refused: map[string]refusal{}}
}

// kick starts a pass in the background when one is due. A phone coming or
// going makes the next pass come soon.
func (r *reconnector) kick(entries []adb.DeviceEntry) {
	now := r.now()
	online := inUseSerials(entries)
	r.mu.Lock()
	if online != r.online {
		r.online = online
		r.wait = 0
		if soon := now.Add(reconnectSoon); r.next.After(soon) {
			r.next = soon
		}
	}
	if r.running || now.Before(r.next) {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	go r.pass(entries)
}

func (r *reconnector) pass(entries []adb.DeviceEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), reconnectSearch+20*time.Second)
	defer cancel()
	away, connected := r.reconnect(ctx, entries)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
	now := r.now()
	switch {
	case connected:
		r.wait = 0
		r.next = now.Add(reconnectSoon)
	case away:
		r.wait = min(max(2*r.wait, reconnectFirst), reconnectMax)
		r.next = now.Add(r.wait)
	default:
		r.wait = 0
		r.next = now.Add(reconnectIdle)
	}
}

// reconnect connects the paired phones that are on the network but not
// connected. It reports whether a paired phone is still away, and whether
// it connected one.
func (r *reconnector) reconnect(ctx context.Context, entries []adb.DeviceEntry) (away, connected bool) {
	p := r.p
	guids, err := p.cfg.KnownHosts()
	if err != nil {
		r.note("", "android: reading the paired phones: "+err.Error())
		return false, false
	}
	want := r.missing(guids, entries)
	// Phones the person removed from Pinky stay away until they add them
	// back (Forget).
	want = slices.DeleteFunc(want, func(g string) bool { return p.forgot(guidSerial(g)) })
	want = slices.DeleteFunc(want, func(g string) bool { return r.onWiFi(ctx, g, entries) })
	if len(want) == 0 {
		return false, false
	}
	// A phone the server's discovery lists, the server connects by itself.
	listed, _ := p.cfg.Client.MDNSServices(ctx)
	want = slices.DeleteFunc(want, func(g string) bool { return serviceAddr(listed, g) != "" })
	if len(want) == 0 {
		return false, false
	}
	b, err := p.browse(adb.ServiceConnect)
	if err != nil {
		return true, false
	}
	defer b.Close()
	found := r.search(ctx, b, want)
	if err := b.Err(); err != nil {
		r.note("", "android: searching the network for paired phones: "+err.Error())
	}
	for _, g := range want {
		addr := found[g]
		if addr == "" {
			away = true
			continue
		}
		if serialInUse(entries, addr) {
			r.remember(addr, g) // connected by address already, by someone else
			continue
		}
		if r.holdBack(g, addr) {
			away = true
			continue
		}
		r.note(g, "android: "+g+" is on the network at "+addr+", but the adb server has not connected it (its discovery stops working after the computer's network changes); connecting")
		if err := p.cfg.Client.Connect(ctx, addr); err != nil {
			r.refuse(ctx, g, addr, err)
			away = true
			continue
		}
		r.remember(addr, g)
		connected = true
	}
	return away, connected
}

// onWiFi reports whether a paired phone is connected on the computer's
// network already in a way that missing does not see: at an address that
// neither the server's discovery nor this driver connected, such as adb
// tcpip's port 5555, another tool's connection, one that Pinky made before
// it restarted, or the phone's other address (IPv6 rather than IPv4). One
// more connection would be the same phone again. A cable's and Tailscale's
// do not count: the server's discovery would connect the phone at home all
// the same.
func (r *reconnector) onWiFi(ctx context.Context, guid string, entries []adb.DeviceEntry) bool {
	serial := guidSerial(guid)
	if serial == "" {
		return false
	}
	for _, e := range entries {
		if e.State != adb.StateDevice || !e.Wireless() || viaTailnet(e.Serial) {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		pr, err := r.p.deviceProps(pctx, e)
		cancel()
		if err == nil && r.p.phoneID(e, pr) == serial {
			return true
		}
	}
	return false
}

// guidAt returns the guid of the paired phone this driver connected at an
// address, "" for an address it did not connect.
func (r *reconnector) guidAt(addr string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byAddr[addr]
}

// holdBack reports whether a phone refused a connection at this address
// too recently to try again.
func (r *reconnector) holdBack(guid, addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.refused[guid]
	return ok && f.addr == addr && r.now().Before(f.until)
}

// refuse notes a failed connection and when to try that address again.
func (r *reconnector) refuse(ctx context.Context, guid, addr string, err error) {
	_, verr := r.p.cfg.Client.Version(ctx)
	quit := errors.Is(verr, adb.ErrServerNotRunning)
	r.mu.Lock()
	f := r.refused[guid]
	if f.addr != addr {
		f = refusal{addr: addr}
	}
	f.count++
	wait := refusedFirst
	for i := 1; i < f.count && wait < refusedMax; i++ {
		wait *= 3
	}
	if quit || wait > refusedMax {
		wait = refusedMax
	}
	f.until = r.now().Add(wait)
	r.refused[guid] = f
	r.mu.Unlock()
	if quit {
		r.p.cfg.logf("android: the adb server quit while connecting to %s at %s (%v); Pinky leaves that phone alone for %v", guid, addr, err, wait)
		return
	}
	r.p.cfg.logf("android: connecting to %s at %s: %v; trying again in %v", guid, addr, err, wait)
}

// missing returns the paired phones' guids that have no connection in use:
// none named after them by the server, and none at an address the driver
// connected them at.
func (r *reconnector) missing(guids []string, entries []adb.DeviceEntry) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	present := map[string]bool{}
	for _, e := range entries {
		if !inUse(e.State) {
			continue
		}
		if g, ok := strings.CutSuffix(e.Serial, connectSuffix); ok {
			present[g] = true
		}
		if g, ok := r.byAddr[e.Serial]; ok {
			present[g] = true
		}
	}
	var out []string
	for _, g := range guids {
		if !present[g] && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// search waits up to reconnectSearch for the phones' connect services and
// returns their addresses by guid.
func (r *reconnector) search(ctx context.Context, b Browse, want []string) map[string]string {
	found := map[string]string{}
	deadline := time.Now().Add(reconnectSearch)
	for {
		svcs := b.Services()
		for _, g := range want {
			if a := serviceAddr(svcs, g); a != "" {
				found[g] = a
			}
		}
		if len(found) == len(want) || !time.Now().Before(deadline) {
			return found
		}
		if sleepCtx(ctx, 200*time.Millisecond) != nil {
			return found
		}
	}
}

func (r *reconnector) remember(addr, guid string) {
	r.mu.Lock()
	r.byAddr[addr] = guid
	delete(r.refused, guid)
	r.mu.Unlock()
}

// drop forgets a connection by address once it is gone.
func (r *reconnector) drop(addr string) {
	r.mu.Lock()
	delete(r.byAddr, addr)
	r.mu.Unlock()
}

// note logs a message about a guid ("" for the search) when it differs
// from the last one.
func (r *reconnector) note(guid, msg string) {
	r.mu.Lock()
	same := r.noted[guid] == msg
	r.noted[guid] = msg
	r.mu.Unlock()
	if !same {
		r.p.cfg.logf("%s", msg)
	}
}

// serviceAddr returns the address of a phone's connect service.
func serviceAddr(svcs []adb.MDNSService, guid string) string {
	for _, s := range svcs {
		if s.Type == adb.ServiceConnect && s.Instance == guid {
			return s.Addr
		}
	}
	return ""
}

// inUse reports whether a connection in this state is there or on its way.
func inUse(state string) bool {
	return state == adb.StateDevice || state == adb.StateConnecting || state == adb.StateAuthorizing
}

func serialInUse(entries []adb.DeviceEntry, serial string) bool {
	for _, e := range entries {
		if e.Serial == serial && inUse(e.State) {
			return true
		}
	}
	return false
}

func inUseSerials(entries []adb.DeviceEntry) string {
	var s []string
	for _, e := range entries {
		if inUse(e.State) {
			s = append(s, e.Serial)
		}
	}
	sort.Strings(s)
	return strings.Join(s, " ")
}
