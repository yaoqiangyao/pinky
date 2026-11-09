package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/approval"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/history"
	"github.com/DIYgod/pinky/internal/policy"
)

// Options configure a Gateway. Zero durations take the defaults in
// DESIGN.md.
type Options struct {
	// Providers discover and open devices. The gateway never branches on the
	// platform; it only talks to these.
	Providers []driver.Provider
	// DataDir holds the history database and media. Empty keeps history in
	// memory only.
	DataDir string
	// Policy decides actions; nil uses the bundled rules.
	Policy *policy.Engine

	ApprovalTimeout time.Duration // default 60 s, from when the panel opens
	MaxBlock        time.Duration // default 45 s, longest a tool call blocks
	HandoffTimeout  time.Duration // default 15 min
	IdleTimeout     time.Duration // default 10 min without calls ends a lease
	ReconnectGrace  time.Duration // default 60 s a lease survives a dropped device
	ResumeGrace     time.Duration // default 2 min a resuming agent's closed session keeps its leases for its next one
	SettleMax       time.Duration // default 3 s
	PollInterval    time.Duration // default 250 ms, for settling and phone_wait_for
	DiscoverEvery   time.Duration // default 2 s
	// RulesEvery is how often the policy's files are checked for hand
	// edits (policy.Engine.Refresh); default 2 s.
	RulesEvery time.Duration

	// NoScreenshots skips the before and after screenshots of each step.
	NoScreenshots bool
	// TrustNewAgents skips the approval of an agent's first connection. Use
	// it only with fake devices.
	TrustNewAgents bool
	// DefaultScope is what a newly approved agent may do; default everything.
	DefaultScope Scope
	// PIN returns a phone's saved PIN when the person asks to unlock it
	// from the computer, after they confirm (Touch ID); the gateway enters
	// it through driver.Unlocker and forgets it. Nil means unlocking from
	// the computer is not available.
	PIN func(ctx context.Context, deviceID string) (string, error)

	Logger *slog.Logger
}

func (o *Options) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.ApprovalTimeout, 60*time.Second)
	set(&o.MaxBlock, 45*time.Second)
	set(&o.HandoffTimeout, 15*time.Minute)
	set(&o.IdleTimeout, 10*time.Minute)
	set(&o.ReconnectGrace, 60*time.Second)
	set(&o.ResumeGrace, 2*time.Minute)
	set(&o.SettleMax, 3*time.Second)
	set(&o.PollInterval, 250*time.Millisecond)
	set(&o.DiscoverEvery, 2*time.Second)
	set(&o.RulesEvery, 2*time.Second)
	if o.DefaultScope.Mode == "" {
		o.DefaultScope = Scope{Mode: ScopeEverything}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
}

// Gateway holds every device connection and runs the action pipeline. It
// implements API for the client; agents reach it through Sessions.
type Gateway struct {
	opt       Options
	log       *slog.Logger
	policy    *policy.Engine
	store     *history.Store // nil without DataDir
	approvals *approval.Center
	rec       *recorder

	mu          sync.Mutex
	devices     map[string]*device
	sessions    map[string]*Session
	sessionSeq  uint64       // the last Session.seq
	resumeStops []resumeStop // recent Stops of resuming agents (resume.go)
	ended       []*task      // recently ended tasks, newest last
	agents      map[string]*agentState
	apSteps     map[string]stepRef // approval id -> the step it holds

	subMu sync.Mutex
	subs  map[chan Event]struct{}

	// hubMu guards the video streams (streams.go). It is never held while
	// taking g.mu.
	hubMu      sync.Mutex
	hubs       map[string]*hub
	hubsClosed bool

	// Guarded by mu.
	recordOn   bool                      // record the screen of new tasks
	background map[string]bool           // the person's choice per phone: agents on a background display, or not
	removed    map[string]bool           // phones the person removed, kept off the list while listed over the network (remove.go)
	known      map[string]bool           // phones that are the person's, which providers may open over Wi-Fi (wifi.go)
	names      map[string]history.Device // what the history store calls the phones Pinky has seen (wifi.go)
	wifiOn     bool                      // the providers use phones over Wi-Fi (SetWiFi)
	keepDays   int                       // history retention; 0 keeps everything
	prune      chan struct{}             // asks the retention loop to run now

	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	shots      running // history screenshots being taken or confirmed
	videos     running // task recordings, until their files are closed
	videoStops running // recordings being finished
	closed     bool
}

var _ API = (*Gateway)(nil)

// New starts a gateway: it opens the history store, discovers devices once
// and keeps discovering in the background until Close.
func New(o Options) (*Gateway, error) {
	o.defaults()
	g := &Gateway{
		opt:        o,
		log:        o.Logger,
		policy:     o.Policy,
		devices:    map[string]*device{},
		sessions:   map[string]*Session{},
		agents:     map[string]*agentState{},
		apSteps:    map[string]stepRef{},
		subs:       map[chan Event]struct{}{},
		hubs:       map[string]*hub{},
		background: map[string]bool{},
		removed:    map[string]bool{},
		known:      map[string]bool{},
		names:      map[string]history.Device{},
		prune:      make(chan struct{}, 1),
	}
	if g.policy == nil {
		g.policy = policy.Default()
	}
	if o.DataDir != "" {
		st, err := history.Open(filepath.Clean(o.DataDir))
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		g.store = st
		if bg, err := st.BackgroundDevices(context.Background()); err == nil {
			g.background = bg
		} else {
			g.log.Warn("history: background displays", "err", err)
		}
		// How the person chose to connect each phone, before the first
		// discovery opens it.
		if ways, err := st.DeviceConnections(context.Background()); err == nil {
			g.applyConnections(ways)
		} else {
			g.log.Warn("history: connections", "err", err)
		}
		g.loadRemoved()
		g.loadKnown()
	}
	g.approvals = approval.New(approval.Options{
		Timeout:  o.ApprovalTimeout,
		OnChange: func() { g.emit(Event{Kind: EventNeeds}); g.emit(Event{Kind: EventDevices}) },
		OnExpire: g.approvalExpired,
	})
	g.rec = newRecorder(g.log)
	g.ctx, g.cancel = context.WithCancel(context.Background())

	ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
	g.discover(ctx)
	cancel()

	g.wg.Add(4)
	go g.discoverLoop()
	go g.idleLoop()
	go g.retentionLoop()
	go g.rulesLoop()
	return g, nil
}

// Close ends every lease, stops the loops and closes devices and history.
func (g *Gateway) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	var sessions []*Session
	for _, s := range g.sessions {
		sessions = append(sessions, s)
	}
	g.mu.Unlock()
	for _, s := range sessions {
		s.end("gateway stopped", false)
	}
	g.mu.Lock()
	g.endKeptLocked("gateway stopped")
	g.mu.Unlock()
	g.approvals.Close()
	g.cancel()
	// Recordings close their files, which needs the history queue.
	g.videos.wait()
	g.stopAllHubs()
	g.wg.Wait()
	g.shots.wait()
	g.rec.close()
	g.mu.Lock()
	var devs []driver.Device
	for _, d := range g.devices {
		if d.dev != nil {
			devs = append(devs, d.dev)
			d.dev = nil
		}
	}
	g.mu.Unlock()
	for _, dev := range devs {
		dev.Close()
	}
	if g.store != nil {
		return g.store.Close()
	}
	return nil
}

// Policy returns the policy engine, for settings screens.
func (g *Gateway) Policy() *policy.Engine { return g.policy }

// Flush waits until the history screenshots being taken, the recordings of
// tasks that ended and the history writes queued so far are done.
func (g *Gateway) Flush() {
	g.shots.wait()
	g.videoStops.wait()
	g.rec.flush()
}

func (g *Gateway) discoverLoop() {
	defer g.wg.Done()
	t := time.NewTicker(g.opt.DiscoverEvery)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(g.ctx, 30*time.Second)
		g.discover(ctx)
		g.pollLocks(ctx)
		cancel()
		g.expireOffline()
	}
}

func (g *Gateway) idleLoop() {
	defer g.wg.Done()
	every := g.opt.IdleTimeout / 20
	if every < 10*time.Millisecond {
		every = 10 * time.Millisecond
	}
	if every > 5*time.Second {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
		}
		g.expireIdle()
	}
}

// Refresh runs one discovery pass now.
func (g *Gateway) Refresh(ctx context.Context) { g.discover(ctx) }

func (g *Gateway) discover(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range g.opt.Providers {
		infos, err := p.List(ctx)
		if err != nil {
			g.log.Warn("list devices", "platform", p.Platform(), "err", err)
			continue
		}
		seen := map[string]bool{}
		for _, info := range infos {
			seen[info.ID] = true
			if d := g.updateDevice(p, info); d != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					g.open(ctx, d, p)
				}()
			}
		}
		g.mu.Lock()
		var gone []*device
		var left []string
		now := time.Now()
		for id, d := range g.devices {
			if d.provider != p || seen[id] {
				continue
			}
			if d.unlistedAt.IsZero() {
				d.unlistedAt = now
			}
			switch {
			case d.online:
				gone = append(gone, d)
			case foundLocked(d) && !g.known[id] && d.lease == nil:
				// Found on Wi-Fi and never used: nothing to keep once it
				// is gone (wifi.go).
				delete(g.devices, id)
				left = append(left, id)
			case !d.opened && !d.opening && d.lease == nil && !g.known[id] && now.Sub(d.unlistedAt) >= unopenedGrace:
				// Never opened, and gone: it never became one of the
				// person's phones (an iPhone unplugged before it trusted
				// the computer, the entry Windows lists for a phone
				// without a USB driver once the driver is installed). One
				// that is the person's (wifi.go), such as an iPhone that
				// stayed locked on Wi-Fi, stays.
				delete(g.devices, id)
				left = append(left, id)
				g.log.Info("device forgotten", "device", id, "why", "never opened, and no longer listed")
			case unlisted(d):
				left = append(left, id)
			}
		}
		g.mu.Unlock()
		for _, d := range gone {
			g.setOffline(d, "the phone disconnected")
		}
		for _, id := range left {
			g.emit(Event{Kind: EventDevices, DeviceID: id})
		}
	}
	wg.Wait()
}

// unopenedGrace is how long a phone the gateway never opened stays listed,
// offline, after its provider stopped listing it, before it is forgotten:
// long enough for a phone that restarts while it is set up (Developer
// Mode) to come back as itself. A variable for tests.
var unopenedGrace = 2 * time.Minute

// unlisted marks a phone its provider no longer lists, and that the
// gateway has not opened, as offline: it went away while it waited for a
// step, or after it was listed offline, so the step and the reason no
// longer hold. It reports whether that changed the phone. The caller holds
// g.mu.
func unlisted(d *device) bool {
	if d.opening || d.info.State == driver.StateOnline {
		return false // an online one that went away is shown offline (Devices)
	}
	was := d.info
	d.info.State, d.info.Setup, d.info.Detail = driver.StateOffline, "", ""
	if waitsForStep(was.State) {
		// A lease kept while it waited has the grace of any phone that
		// drops from now (expireOffline).
		d.offlineSince = time.Now()
	}
	return d.info != was
}

// updateDevice records what the provider says and returns the device when it
// should be opened.
func (g *Gateway) updateDevice(p driver.Provider, info driver.Info) *device {
	g.mu.Lock()
	if g.removed[info.ID] {
		if !cabled(info) {
			// The person removed it; the provider lists it over the
			// network by itself.
			g.mu.Unlock()
			return nil
		}
		// Plugged in: the person adds it back.
		g.addedBackLocked(info.ID)
	}
	d, ok := g.devices[info.ID]
	if !ok {
		d = newDevice(p, info)
		g.devices[info.ID] = d
	}
	old := d.info
	if info.Name == "" {
		// Listed without a name, as an iPhone usbmuxd lost (wifi.go): the
		// name it had.
		if rec, ok := g.names[info.ID]; ok && rec.Name != "" {
			info.Name = rec.Name
			if info.Model == "" {
				info.Model = rec.Model
			}
			if info.OSVersion == "" {
				info.OSVersion = rec.OSVersion
			}
		} else if old.Name != "" {
			info.Name, info.Model, info.OSVersion = old.Name, old.Model, old.OSVersion
		}
	}
	d.info = info
	d.nameV.Store(info.Name)
	d.provider = p
	d.unlistedAt = time.Time{}
	if waitsForStep(old.State) && info.State == driver.StateOffline {
		// It no longer waits: a lease kept while it did has the grace of
		// any phone that drops from now (expireOffline).
		d.offlineSince = time.Now()
	}
	if cabled(info) && info.State != driver.StateUnauthorized {
		g.knowLocked(d) // a phone on a cable that trusts the computer is the person's (wifi.go)
	}
	// The phone moved between its cable and Wi-Fi: its session goes with
	// the old connection, and it opens again over the new one below, while
	// calls wait for it (wifi.go).
	from := d.over
	moved := info.State == driver.StateOnline && d.dev != nil && from != "" && info.Connection != "" && info.Connection != from
	if moved {
		d.moving = time.Now().Add(moveWait)
	}
	g.mu.Unlock()
	if moved {
		g.log.Info("device moving", "device", d.id, "from", from, "to", info.Connection)
		g.setOffline(d, movingWhy(info.Connection))
	}
	g.mu.Lock()
	open := info.State == driver.StateOnline && d.dev == nil && !d.opening && !d.removed
	if open {
		d.opening, d.openingAt = true, time.Now()
		if time.Now().Before(d.moving) {
			// Back on its other connection: calls wait while it opens.
			d.moving = time.Now().Add(moveWait)
		}
	}
	offline := info.State != driver.StateOnline && d.online
	g.mu.Unlock()
	if !ok || old != info {
		g.emit(Event{Kind: EventDevices, DeviceID: info.ID})
	}
	if offline {
		g.setOffline(d, info.Detail)
	}
	if open {
		return d
	}
	return nil
}

func (g *Gateway) open(ctx context.Context, d *device, p driver.Provider) {
	g.mu.Lock()
	closing := d.closing
	g.mu.Unlock()
	if closing != nil {
		// The last session still stops (its video stream, its tunnel).
		t := time.NewTimer(closeWait)
		select {
		case <-closing:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}
	g.mu.Lock()
	listed := d.info.Connection // what it opens over (connectionOf)
	g.mu.Unlock()
	dev, err := p.Open(ctx, d.id)
	g.mu.Lock()
	d.opening = false
	if err != nil || g.closed || d.removed {
		g.wakeLocked(d) // calls waiting for the open (connectWait) go on
		g.mu.Unlock()
		if err == nil {
			dev.Close()
		} else {
			g.log.Warn("open device", "device", d.id, "err", err)
		}
		return
	}
	d.dev = dev
	d.over = connectionOf(listed, dev)
	d.online = true
	d.opened = true
	d.offlineSince = time.Time{}
	d.moving = time.Time{}
	d.workSent = false
	g.noteWorkLocked(d)
	info := d.info
	if d.lease != nil {
		// A task that connected while the phone waited for a step has not
		// recorded yet (firstScreen); for others this does nothing.
		g.startVideoLocked(d.lease)
	}
	g.wakeLocked(d)
	g.mu.Unlock()
	g.log.Info("device online", "device", d.id, "name", info.Name, "connection", d.over)
	g.emit(Event{Kind: EventDevices, DeviceID: d.id})
	g.readyNow(d)
	if w, ok := dev.(driver.PersonWatcher); ok {
		go g.watchPerson(d.id, w.PersonInput())
	}
	if g.store != nil {
		g.rec.do(func(ctx context.Context) error {
			return g.store.SaveDevice(ctx, history.Device{ID: info.ID, Platform: string(info.Platform), Name: info.Name,
				Model: info.Model, OSVersion: info.OSVersion, Backend: info.Backend, LastSeen: time.Now()})
		})
	}
}

// watchPerson takes over for the person whenever they use the phone itself,
// until the device closes its channel.
func (g *Gateway) watchPerson(id string, ch <-chan struct{}) {
	for {
		select {
		case <-g.ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			g.PersonTouched(id)
		}
	}
}

// setOffline marks a device offline. Its lease survives for ReconnectGrace.
func (g *Gateway) setOffline(d *device, why string) {
	g.mu.Lock()
	if !d.online && d.dev == nil {
		g.mu.Unlock()
		return
	}
	dev := d.dev
	d.dev = nil
	d.online = false
	d.offlineSince = time.Now()
	if why != "" {
		d.offlineWhy = why
	} else if d.over == "usb" && g.wifiOn && g.known[d.id] && time.Now().After(d.moving) {
		// The session dropped: on a phone the person uses over Wi-Fi too,
		// most likely its cable was pulled, and it may be back on Wi-Fi in
		// a moment (wifi.go).
		if _, ok := d.provider.(driver.WiFiFinder); ok {
			d.moving = time.Now().Add(dropWait)
		}
	}
	var closed chan struct{}
	if dev != nil {
		closed = make(chan struct{})
		d.closing = closed
	}
	// Anything may happen on the phone while it is away.
	g.inputLocked(d)
	g.pauseVideoLocked(d)
	var bg driver.Device
	if l := d.lease; l != nil && l.bg != nil {
		// The background display went with the phone; the next call opens
		// another (background.go).
		bg, l.bg = l.bg, nil
	}
	g.wakeLocked(d)
	g.mu.Unlock()
	g.stopHubs(d.id)
	if bg != nil {
		go bg.Close()
	}
	if dev != nil {
		go func() {
			dev.Close()
			close(closed)
		}()
	}
	g.log.Info("device offline", "device", d.id, "why", why)
	g.emit(Event{Kind: EventDevices, DeviceID: d.id})
	g.emit(Event{Kind: EventTasks, DeviceID: d.id})
}

func (g *Gateway) expireOffline() {
	now := time.Now()
	g.mu.Lock()
	var ls []*lease
	for _, d := range g.devices {
		// A lease on a phone that waits for a step waits with it: the
		// person is setting it up (device_needs_setup).
		if !d.online && d.lease != nil && !waitsForStep(d.info.State) && now.Sub(d.offlineSince) > g.opt.ReconnectGrace {
			ls = append(ls, d.lease)
		}
	}
	g.mu.Unlock()
	for _, l := range ls {
		g.endLease(l, OutcomeOffline, "")
	}
}

func (g *Gateway) expireIdle() {
	now := time.Now()
	g.mu.Lock()
	var ls []*lease
	for _, s := range g.sessions {
		for _, l := range s.leases {
			if l.inflight == 0 && now.Sub(l.lastUsed) > g.opt.IdleTimeout {
				ls = append(ls, l)
			}
		}
	}
	g.mu.Unlock()
	for _, l := range ls {
		g.log.Info("lease idle", "device", l.dev.id, "task", l.task.title)
		g.endLease(l, OutcomeExpired, "")
	}
}

// idleLockEvery is how often an idle phone's lock state is read, so the
// client can show a locked phone, and offer to save its PIN for unlocking
// from the computer, before an agent needs it. A variable for tests.
var idleLockEvery = 6 * time.Second

// pollLocks asks lock-reporting drivers about devices that have an agent,
// and about idle ones every idleLockEvery. A locked device whose driver
// cannot report locks is read instead, so the unlock card goes away when
// the person unlocks.
func (g *Gateway) pollLocks(ctx context.Context) {
	g.mu.Lock()
	var report, read []*device
	now := time.Now()
	for _, d := range g.devices {
		if d.dev == nil || !d.online {
			continue
		}
		if d.lease == nil {
			if _, ok := d.dev.(driver.LockReporter); ok && now.Sub(d.lockReadAt) >= idleLockEvery {
				d.lockReadAt = now
				report = append(report, d)
			}
			continue
		}
		if _, ok := d.dev.(driver.LockReporter); ok {
			report = append(report, d)
		} else if d.locked && d.lease.bg == nil {
			read = append(read, d)
		}
	}
	g.mu.Unlock()
	for _, d := range report {
		g.checkLock(ctx, d)
	}
	for _, d := range read {
		g.observe(ctx, d, false)
	}
}

// checkLock reads the lock state from a LockReporter, if the driver has one.
func (g *Gateway) checkLock(ctx context.Context, d *device) {
	dev := g.driverOf(d)
	lr, ok := dev.(driver.LockReporter)
	if !ok {
		return
	}
	locked, err := lr.Locked(ctx)
	if err != nil {
		if errors.Is(err, driver.ErrOffline) {
			g.setOffline(d, "")
		}
		return
	}
	g.setLocked(d, locked)
}

// setLocked records the lock state and updates Needs. An unlock seen on
// the phone does not end a handoff by itself: the person may unlock to do
// what the agent asked, so they hand back explicitly. Unlock from the
// computer does end the handoffs opened for the lock (see Unlock).
func (g *Gateway) setLocked(d *device, locked bool) {
	g.mu.Lock()
	if d.locked == locked {
		g.mu.Unlock()
		return
	}
	d.locked = locked
	if locked && (d.lease == nil || d.lease.bg == nil) {
		g.pauseVideoLocked(d)
	}
	g.wakeLocked(d)
	g.mu.Unlock()
	g.emit(Event{Kind: EventDevices, DeviceID: d.id})
	g.emit(Event{Kind: EventNeeds, DeviceID: d.id})
}

// driverOf returns the phone's own device: its main screen, lock, files
// and the person's mirror.
func (g *Gateway) driverOf(d *device) driver.Device {
	g.mu.Lock()
	defer g.mu.Unlock()
	return d.dev
}

// screenOf returns the device the agent's reads and actions go to: the
// lease's background display when it works on one, else the phone.
func (g *Gateway) screenOf(d *device) (dev driver.Device, background bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.screenOfLocked(d)
}

func (g *Gateway) screenOfLocked(d *device) (driver.Device, bool) {
	if l := d.lease; l != nil && l.bg != nil {
		return l.bg, true
	}
	return d.dev, false
}

// noteWorkLocked tells the phone's driver whether an agent works on it, a
// task running while the person is not in control, when that changed
// (driver.WorkWatcher). Android turns Pinky Companion's accessibility
// service on only while one does.
func (g *Gateway) noteWorkLocked(d *device) {
	ww, ok := d.dev.(driver.WorkWatcher)
	if !ok {
		return
	}
	working := d.lease != nil && !d.person
	if d.workSent && d.working == working {
		return
	}
	d.working, d.workSent = working, true
	ww.AgentWorking(working)
}

// wakeLocked wakes every call waiting on the device's state.
func (g *Gateway) wakeLocked(d *device) {
	close(d.wake)
	d.wake = make(chan struct{})
}

// emit sends a change notification to subscribers without blocking. It
// takes only subMu, so it may run under any other lock.
func (g *Gateway) emit(ev Event) {
	g.subMu.Lock()
	defer g.subMu.Unlock()
	for ch := range g.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe implements API.
func (g *Gateway) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, 64)
	g.subMu.Lock()
	g.subs[ch] = struct{}{}
	g.subMu.Unlock()
	go func() {
		<-ctx.Done()
		g.subMu.Lock()
		delete(g.subs, ch)
		close(ch)
		g.subMu.Unlock()
	}()
	return ch
}

// deviceByRef finds a device by id, name or a unique id prefix.
func (g *Gateway) deviceByRef(ref string) (*device, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d, ok := g.devices[ref]; ok {
		return d, nil
	}
	var match []*device
	for _, d := range g.devices {
		if equalFold(d.name(), ref) {
			match = append(match, d)
		}
	}
	if len(match) == 0 {
		for _, d := range g.devices {
			if len(ref) >= 4 && hasPrefixFold(d.id, ref) {
				match = append(match, d)
			}
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return nil, fmt.Errorf("no phone matches %q", ref)
	}
	return nil, fmt.Errorf("%d phones match %q; use the device id", len(match), ref)
}

// sortedDevices returns devices in a stable order: online first, then by name.
func (g *Gateway) sortedDevicesLocked() []*device {
	out := make([]*device, 0, len(g.devices))
	for _, d := range g.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].online != out[j].online {
			return out[i].online
		}
		if out[i].name() != out[j].name() {
			return out[i].name() < out[j].name()
		}
		return out[i].id < out[j].id
	})
	return out
}

// recorder writes history in the background, in order, so a slow disk never
// slows a step down.
type recorder struct {
	log   *slog.Logger
	mu    sync.Mutex
	cond  *sync.Cond
	queue []func(context.Context) error
	busy  bool
	done  bool
	idle  chan struct{}
}

func newRecorder(log *slog.Logger) *recorder {
	r := &recorder{log: log, idle: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	go r.run()
	return r
}

func (r *recorder) do(f func(context.Context) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.queue = append(r.queue, f)
	r.cond.Broadcast()
}

func (r *recorder) run() {
	defer close(r.idle)
	for {
		r.mu.Lock()
		for len(r.queue) == 0 && !r.done {
			r.cond.Wait()
		}
		if len(r.queue) == 0 && r.done {
			r.mu.Unlock()
			return
		}
		f := r.queue[0]
		r.queue = r.queue[1:]
		r.busy = true
		r.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := f(ctx); err != nil {
			r.log.Warn("history write", "err", err)
		}
		cancel()
		r.mu.Lock()
		r.busy = false
		r.cond.Broadcast()
		r.mu.Unlock()
	}
}

func (r *recorder) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for (len(r.queue) > 0 || r.busy) && !r.done {
		r.cond.Wait()
	}
}

func (r *recorder) close() {
	r.mu.Lock()
	r.done = true
	r.cond.Broadcast()
	r.mu.Unlock()
	<-r.idle
}
