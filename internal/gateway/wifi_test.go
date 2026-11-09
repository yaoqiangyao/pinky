package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// airProvider lists demo iPhones over a cable or Wi-Fi as conn says, and
// behaves as driver.WiFiFinder asks: a phone on Wi-Fi only is listed with
// SetupConfirmWiFi and not opened until UseOverWiFi, none is listed over
// Wi-Fi while it is off, and a forgotten one not at all.
type airProvider struct {
	*demo.Provider

	mu        sync.Mutex
	on        bool
	use       map[string]bool
	forgot    map[string]bool
	conn      map[string]string   // by id; "usb" when unset
	opens     map[string][]string // the connection of each Open, by id
	sessions  map[string][]*overDevice
	openDelay time.Duration // Open takes this long
	hold      *listHold     // the next List waits for it (holdList)
}

func newAirProvider(devs ...*demo.Device) *airProvider {
	return &airProvider{Provider: demo.NewProvider(driver.IOS, devs...), use: map[string]bool{}, forgot: map[string]bool{},
		conn: map[string]string{}, opens: map[string][]string{}, sessions: map[string][]*overDevice{}}
}

func (p *airProvider) connOf(id string) string {
	if c := p.conn[id]; c != "" {
		return c
	}
	return "usb"
}

func (p *airProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.Provider.List(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []driver.Info
	for _, info := range infos {
		info.Connection = p.connOf(info.ID)
		if info.Connection == "wifi" {
			if !p.on || p.forgot[info.ID] || info.State == driver.StateOffline {
				continue // usbmuxd lists a phone on the network only while it is there
			}
			if !p.use[info.ID] {
				info.State, info.Setup, info.Detail = driver.StateNeedsSetup, driver.SetupConfirmWiFi, "Found on Wi-Fi."
			}
		}
		out = append(out, info)
	}
	if h := p.hold; h != nil {
		p.hold = nil
		p.mu.Unlock()
		close(h.held)
		<-h.release
		p.mu.Lock()
	}
	return out, err
}

// listHold makes one List take its listing, say so (held), and wait for
// release before it returns it, as a listing slower than the passes after
// it does.
type listHold struct{ held, release chan struct{} }

// holdList holds the next List until the returned function is called,
// which the test's cleanup calls too.
func (p *airProvider) holdList(t *testing.T) (held <-chan struct{}, release func()) {
	h := &listHold{held: make(chan struct{}), release: make(chan struct{})}
	p.mu.Lock()
	p.hold = h
	p.mu.Unlock()
	release = sync.OnceFunc(func() { close(h.release) })
	t.Cleanup(release)
	return h.held, release
}

func (p *airProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	c, used, delay := p.connOf(id), p.use[id], p.openDelay
	p.mu.Unlock()
	if c == "wifi" && !used {
		return nil, errors.New("found on Wi-Fi; not used yet")
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	dev, err := p.Provider.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	od := &overDevice{Device: dev, conn: c}
	p.mu.Lock()
	p.opens[id] = append(p.opens[id], c)
	p.sessions[id] = append(p.sessions[id], od)
	p.mu.Unlock()
	return od, nil
}

func (p *airProvider) SetWiFi(on bool) {
	p.mu.Lock()
	p.on = on
	p.mu.Unlock()
}

func (p *airProvider) UseOverWiFi(id string) {
	p.mu.Lock()
	p.use[id] = true
	p.mu.Unlock()
}

func (p *airProvider) Forget(id string) {
	p.mu.Lock()
	p.forgot[id] = true
	delete(p.use, id)
	p.mu.Unlock()
}

func (p *airProvider) Remember(id string) {
	p.mu.Lock()
	delete(p.forgot, id)
	p.mu.Unlock()
}

func (p *airProvider) Disconnect(context.Context, string) error { return nil }

func (p *airProvider) set(id, conn string) {
	p.mu.Lock()
	p.conn[id] = conn
	p.mu.Unlock()
}

func (p *airProvider) state(id string) (used, forgot bool, opens []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.use[id], p.forgot[id], slices.Clone(p.opens[id])
}

// overDevice is a session over one connection, which dies with it.
type overDevice struct {
	driver.Device
	conn   string
	dead   atomic.Bool
	frames atomic.Bool // the last read with OCR was offered frames of the live video
}

func (d *overDevice) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	if d.dead.Load() {
		return nil, fmt.Errorf("the tunnel stopped: %w", driver.ErrOffline)
	}
	if opt.OCR {
		d.frames.Store(opt.Frame != nil)
	}
	return d.Device.Observe(ctx, opt)
}

// pull kills the sessions of a phone, as pulling its cable does, before
// usbmuxd stops listing it there.
func (p *airProvider) pull(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.sessions[id] {
		d.dead.Store(true)
	}
}

func (d *overDevice) Info() driver.Info {
	info := d.Device.Info()
	info.Connection = d.conn
	return info
}

func viewOf(g *Gateway, id string) (DeviceView, bool) {
	for _, v := range g.Devices() {
		if v.Info.ID == id {
			return v, true
		}
	}
	return DeviceView{}, false
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestWiFiFoundPhone: a phone found on Wi-Fi that Pinky has not used waits
// for the person, unseen by agents; Use opens it, and is remembered.
func TestWiFiFoundPhone(t *testing.T) {
	dir := t.TempDir()
	newProvider := func() *airProvider {
		p := newAirProvider(demo.NewDevice("ipad", "Mum's iPad", driver.IOS), demo.NewDevice("iphone", "iPhone", driver.IOS))
		p.set("ipad", "wifi")
		return p
	}
	p := newProvider()
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DataDir = dir })
	if _, ok := viewOf(g, "ipad"); ok {
		t.Fatal("a phone on Wi-Fi was listed with Wi-Fi off")
	}
	g.SetWiFi(true)
	waitFor(t, "the found iPad", func() bool { v, ok := viewOf(g, "ipad"); return ok && v.StatusLine == "Found on Wi-Fi" })
	v, _ := viewOf(g, "ipad")
	if v.Status != DeviceNeedsSetup || v.Info.Setup != driver.SetupConfirmWiFi {
		t.Fatalf("found iPad = %+v", v)
	}
	if used, _, opens := p.state("ipad"); used || len(opens) != 0 {
		t.Fatalf("the found iPad was used (%v) or opened (%v)", used, opens)
	}
	// The iPhone on the cable is the person's, and the provider is told.
	if used, _, _ := p.state("iphone"); !used {
		t.Fatal("a phone on a cable was not given to the provider as the person's")
	}

	// Agents do not see it.
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	res := s.Connect(context.Background(), ConnectRequest{Task: "look", Device: "Mum's iPad"})
	if _, list, _ := strings.Cut(res.Message, "Connected phones:"); !res.IsError() || strings.Contains(list, "iPad") {
		t.Fatalf("an agent reached or saw the found iPad: %+v", res)
	}
	if res := s.Connect(context.Background(), ConnectRequest{Task: "look", Device: "ipad"}); !res.IsError() {
		t.Fatalf("an agent reached the found iPad by id: %+v", res)
	}

	if err := g.UseFoundPhone("nobody"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("UseFoundPhone of an unknown phone: %v", err)
	}
	if err := g.UseFoundPhone("ipad"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the iPad online", func() bool { v, _ := viewOf(g, "ipad"); return v.Status == DeviceIdle })
	if _, _, opens := p.state("ipad"); !slices.Equal(opens, []string{"wifi"}) {
		t.Fatalf("opens %v", opens)
	}
	g.Flush()
	g.Close()

	// Remembered: after a restart the provider knows both before it lists.
	p2 := newProvider()
	newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p2}; o.DataDir = dir })
	for _, id := range []string{"ipad", "iphone"} {
		if used, _, _ := p2.state(id); !used {
			t.Fatalf("after a restart %s is not the person's", id)
		}
	}
}

// TestWiFiOlderListing: a discovery pass that listed a phone found on
// Wi-Fi just before the person chose Use, and is slow to apply it, does not
// undo the open of the pass after the choice: the phone opens once, and
// stays online.
func TestWiFiOlderListing(t *testing.T) {
	p := newAirProvider(demo.NewDevice("ipad", "Mum's iPad", driver.IOS))
	p.set("ipad", "wifi")
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	waitFor(t, "the found iPad", func() bool { v, ok := viewOf(g, "ipad"); return ok && v.StatusLine == "Found on Wi-Fi" })

	held, release := p.holdList(t)
	select {
	case <-held: // a pass listed the iPad as found
	case <-time.After(5 * time.Second):
		t.Fatal("no discovery listed the phones")
	}
	if err := g.UseFoundPhone("ipad"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // long enough for a pass to open it
	release()
	waitFor(t, "the iPad online", func() bool { v, _ := viewOf(g, "ipad"); return v.Status == DeviceIdle })
	time.Sleep(100 * time.Millisecond) // a few passes more (DiscoverEvery 20 ms)
	if _, _, opens := p.state("ipad"); !slices.Equal(opens, []string{"wifi"}) {
		t.Fatalf("opens %v", opens)
	}
	if v, _ := viewOf(g, "ipad"); v.Status != DeviceIdle {
		t.Fatalf("iPad = %+v", v)
	}
}

// TestWiFiIgnoreFoundPhone: Ignore keeps a found phone off the list while
// it is on Wi-Fi; plugged in, it is back, and the person's.
func TestWiFiIgnoreFoundPhone(t *testing.T) {
	p := newAirProvider(demo.NewDevice("ipad", "Mum's iPad", driver.IOS))
	p.set("ipad", "wifi")
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DataDir = t.TempDir() })
	g.SetWiFi(true)
	waitFor(t, "the found iPad", func() bool { _, ok := viewOf(g, "ipad"); return ok })
	if err := g.IgnoreFoundPhone(context.Background(), "ipad"); err != nil {
		t.Fatal(err)
	}
	if _, ok := viewOf(g, "ipad"); ok {
		t.Fatal("an ignored phone stayed on the list")
	}
	if _, forgot, _ := p.state("ipad"); !forgot {
		t.Fatal("the provider was not told to leave the ignored phone alone")
	}
	p.set("ipad", "usb")
	waitFor(t, "the iPad plugged in", func() bool { v, ok := viewOf(g, "ipad"); return ok && v.Status == DeviceIdle })
	if used, forgot, _ := p.state("ipad"); !used || forgot {
		t.Fatalf("plugged in: used %v, forgot %v", used, forgot)
	}
}

// TestWiFiCableMoves: a phone whose cable is pulled moves to Wi-Fi as the
// same phone and its task goes on: a call made while it moves waits for it.
// Plugged in again, it moves back to the cable.
func TestWiFiCableMoves(t *testing.T) {
	p := newAirProvider(demo.NewDevice("iphone", "iPhone", driver.IOS))
	p.openDelay = 150 * time.Millisecond
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	s, _ := connect(t, g, "Check the weather")
	ctx := context.Background()

	p.set("iphone", "wifi") // the cable is pulled
	waitFor(t, "the move", func() bool {
		v, _ := viewOf(g, "iphone")
		return v.Status == DeviceConnecting && v.StatusLine == "Moving to Wi-Fi" && v.Info.State == driver.StateOffline
	})
	res := s.Observe(ctx, ObserveRequest{})
	if res.IsError() || res.Screen == nil {
		t.Fatalf("a call during the move: %+v", res)
	}
	if v, _ := viewOf(g, "iphone"); v.Info.Connection != "wifi" || v.TaskID == "" {
		t.Fatalf("after the move: %+v", v)
	}

	p.set("iphone", "usb") // plugged in again
	waitFor(t, "moving to the cable", func() bool {
		v, _ := viewOf(g, "iphone")
		return v.Status == DeviceConnecting && v.StatusLine == "Moving to the cable"
	})
	waitFor(t, "back on the cable", func() bool {
		_, _, opens := p.state("iphone")
		v, _ := viewOf(g, "iphone")
		return len(opens) == 3 && v.Status != DeviceOffline
	})
	if res := s.Observe(ctx, ObserveRequest{}); res.IsError() {
		t.Fatalf("after moving back: %+v", res)
	}
	if _, _, opens := p.state("iphone"); !slices.Equal(opens, []string{"usb", "wifi", "usb"}) {
		t.Fatalf("opens %v", opens)
	}

	// Wi-Fi turned off: the phone on Wi-Fi goes offline.
	p.set("iphone", "wifi")
	waitFor(t, "on Wi-Fi again", func() bool {
		v, _ := viewOf(g, "iphone")
		return v.Info.Connection == "wifi" && v.Status != DeviceOffline
	})
	g.SetWiFi(false)
	waitFor(t, "offline with Wi-Fi off", func() bool { v, _ := viewOf(g, "iphone"); return v.Status == DeviceOffline })
}

// TestWiFiFoundPhoneGone: a found phone that goes away leaves the list, as
// it was never the person's; one they chose to use stays, offline.
func TestWiFiFoundPhoneGone(t *testing.T) {
	ipad, ipad2 := demo.NewDevice("ipad", "Mum's iPad", driver.IOS), demo.NewDevice("ipad2", "My iPad", driver.IOS)
	p := newAirProvider(ipad, ipad2)
	p.set("ipad", "wifi")
	p.set("ipad2", "wifi")
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	waitFor(t, "both iPads", func() bool { _, a := viewOf(g, "ipad"); _, b := viewOf(g, "ipad2"); return a && b })
	ipad.SetOffline(true)
	waitFor(t, "the found iPad gone", func() bool { _, ok := viewOf(g, "ipad"); return !ok })

	if err := g.UseFoundPhone("ipad2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "my iPad online", func() bool { v, _ := viewOf(g, "ipad2"); return v.Status == DeviceIdle })
	ipad2.SetOffline(true)
	waitFor(t, "my iPad offline", func() bool { v, ok := viewOf(g, "ipad2"); return ok && v.Status == DeviceOffline })
}

// TestWiFiCablePulledMidRead: the cable is pulled while the agent reads the
// screen, before usbmuxd stops listing the phone on it. The read that hits
// the dead session reads again once the phone is back on Wi-Fi.
func TestWiFiCablePulledMidRead(t *testing.T) {
	p := newAirProvider(demo.NewDevice("iphone", "iPhone", driver.IOS))
	p.openDelay = 100 * time.Millisecond
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DiscoverEvery = time.Hour })
	g.SetWiFi(true)
	s, _ := connect(t, g, "Check the weather")
	p.pull("iphone")
	// usbmuxd lists it on Wi-Fi now; a discovery pass sees that a moment
	// later (whichever runs first: SetWiFi's or this one).
	p.set("iphone", "wifi")
	go func() {
		time.Sleep(150 * time.Millisecond)
		g.Refresh(context.Background())
	}()
	res := s.Observe(context.Background(), ObserveRequest{})
	if res.IsError() || res.Screen == nil {
		t.Fatalf("the read across the pull: %+v", res)
	}
	if v, _ := viewOf(g, "iphone"); v.Info.Connection != "wifi" {
		t.Fatalf("after the pull: %+v", v)
	}
}

// TestWiFiLockedPhone: a phone locked on Wi-Fi, which the provider lists
// offline until it is unlocked, reads as such, and agents are told to ask
// for an unlock.
func TestWiFiLockedPhone(t *testing.T) {
	ipad := demo.NewDevice("ipad", "My iPad", driver.IOS)
	p := &lockedProvider{airProvider: newAirProvider(ipad)}
	p.set("ipad", "wifi")
	p.UseOverWiFi("ipad")
	p.locked.Store(true)
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	waitFor(t, "the locked iPad", func() bool { v, _ := viewOf(g, "ipad"); return v.StatusLine == "Locked on Wi-Fi" })
	if v, _ := viewOf(g, "ipad"); v.Status != DeviceOffline {
		t.Fatalf("at rest, the locked iPad = %+v", v)
	}
	// An agent leases it as a phone that waits for a step, and goes on
	// once it is unlocked.
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	res := s.Connect(context.Background(), ConnectRequest{Task: "look", Device: "ipad"})
	if res.Status != StatusNeedsSetup || res.Setup != driver.SetupUnlock {
		t.Fatalf("connect to the locked iPad: %+v", res)
	}
	if v, _ := viewOf(g, "ipad"); v.Status != DeviceNeedsSetup {
		t.Fatalf("with an agent waiting, the locked iPad = %+v", v)
	}
	p.locked.Store(false)
	waitFor(t, "the iPad unlocked", func() bool { v, _ := viewOf(g, "ipad"); return v.Status == DeviceWorking })
	if res := s.Observe(context.Background(), ObserveRequest{}); res.IsError() || res.Screen == nil {
		t.Fatalf("a read once unlocked: %+v", res)
	}
}

// lockedProvider lists its phones on Wi-Fi locked while locked is set, as
// the iOS provider lists a phone locked on Wi-Fi.
type lockedProvider struct {
	*airProvider
	locked atomic.Bool
}

func (p *lockedProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.airProvider.List(ctx)
	for i, info := range infos {
		if info.Connection == "wifi" && info.State == driver.StateOnline && p.locked.Load() {
			infos[i].State, infos[i].Setup, infos[i].Detail = driver.StateNeedsSetup, driver.SetupUnlock, "Unlock the iPhone."
		}
	}
	return infos, err
}

// restartingProvider lists a phone that usbmuxd lost, without a name as the
// iOS provider does after Pinky restarted, and records restarts.
type restartingProvider struct {
	*airProvider
	lost     atomic.Bool
	restarts atomic.Int32
	reason   atomic.Value
}

func (p *restartingProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.airProvider.List(ctx)
	if p.lost.Load() {
		return []driver.Info{{ID: "iphone", Platform: driver.IOS, Connection: "wifi", State: driver.StateNeedsSetup,
			Setup: driver.SetupDeviceService, Detail: "This Mac's device service lost the iPhone on Wi-Fi."}}, err
	}
	return infos, err
}

func (p *restartingProvider) RestartDeviceService(_ context.Context, reason string) error {
	p.restarts.Add(1)
	p.reason.Store(reason)
	p.lost.Store(false)
	return nil
}

// TestWiFiLostPhone: a phone the device service lost reads as such, with
// the name Pinky saved, and restarting the service brings it back.
func TestWiFiLostPhone(t *testing.T) {
	dir := t.TempDir()
	iphone := demo.NewDevice("iphone", "DIYgod's iPhone", driver.IOS)
	p := &restartingProvider{airProvider: newAirProvider(iphone)}
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DataDir = dir })
	waitFor(t, "the iPhone online", func() bool { v, _ := viewOf(g, "iphone"); return v.Status == DeviceIdle })
	g.Flush()
	g.Close()

	// Pinky starts again while usbmuxd has lost the phone.
	p2 := &restartingProvider{airProvider: newAirProvider(iphone)}
	p2.lost.Store(true)
	g2, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p2}; o.DataDir = dir })
	g2.SetWiFi(true)
	v, _ := viewOf(g2, "iphone")
	if v.Status != DeviceNeedsSetup || v.StatusLine != "Lost on Wi-Fi" || v.Info.Name != "DIYgod's iPhone" {
		t.Fatalf("the lost iPhone = %+v", v)
	}
	if err := g2.RestartDeviceService(context.Background(), "nobody", ""); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("restart for an unknown phone: %v", err)
	}
	if err := g2.RestartDeviceService(context.Background(), "iphone", "Pinky restarts the device service."); err != nil {
		t.Fatal(err)
	}
	if p2.restarts.Load() != 1 || p2.reason.Load() != "Pinky restarts the device service." {
		t.Fatalf("restarts %d, reason %v", p2.restarts.Load(), p2.reason.Load())
	}
	waitFor(t, "the iPhone back", func() bool { v, _ := viewOf(g2, "iphone"); return v.Status == DeviceIdle })
}

// TestObserveOffersFrames: a read with OCR offers the driver pictures from
// the live video, which an iPhone over Wi-Fi reads text from.
func TestObserveOffersFrames(t *testing.T) {
	p := newAirProvider(demo.NewDevice("iphone", "iPhone", driver.IOS))
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	s, _ := connect(t, g, "Read the screen")
	if res := s.Observe(context.Background(), ObserveRequest{}); res.IsError() {
		t.Fatal(res.Message)
	}
	p.mu.Lock()
	od := p.sessions["iphone"][len(p.sessions["iphone"])-1]
	p.mu.Unlock()
	if !od.frames.Load() {
		t.Fatal("phone_observe offered the driver no frames of the live video")
	}
}

// openCounter counts a provider's opens, by phone.
type openCounter struct {
	*netProvider
	mu    sync.Mutex
	opens map[string]int
}

func (c *openCounter) Open(ctx context.Context, id string) (driver.Device, error) {
	c.mu.Lock()
	c.opens[id]++
	c.mu.Unlock()
	return c.netProvider.Open(ctx, id)
}

func (c *openCounter) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens[id]
}

// TestWiFiListedConnection: a phone whose sessions name another connection
// than its provider lists (the Mi, listed on Wi-Fi, whose demo sessions say
// "usb") opens once, not again at every discovery, and the listing changing
// is still a move. While the gateway took the session's word for it, the Mi
// of TestRemoveDevice reopened every 20 ms, and now and then the read after
// an action found it offline (main's CI on Windows, 2026-11-08).
func TestWiFiListedConnection(t *testing.T) {
	p, _, _ := newTwoPhones()
	c := &openCounter{netProvider: p, opens: map[string]int{}}
	g, err := New(Options{Providers: []driver.Provider{c}, Policy: testPolicy(t), TrustNewAgents: true,
		DiscoverEvery: 20 * time.Millisecond, NoScreenshots: true})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	waitOpen(t, g, "mi")
	time.Sleep(300 * time.Millisecond) // some fifteen discoveries
	if n := c.count("mi"); n != 1 {
		t.Fatalf("the Mi opened %d times, want once", n)
	}

	p.mu.Lock()
	p.conn["mi"] = "usb"
	p.mu.Unlock()
	waitFor(t, "the Mi to open over its cable", func() bool { return c.count("mi") == 2 })
	waitOpen(t, g, "mi")
	time.Sleep(300 * time.Millisecond)
	if n := c.count("mi"); n != 2 {
		t.Fatalf("the Mi opened %d times once it moved, want twice", n)
	}
}
