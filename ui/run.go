package ui

import (
	"context"
	"errors"
	"log"
	"os"
	"runtime"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/secret"
	"github.com/DIYgod/pinky/internal/telemetry"
	"github.com/DIYgod/pinky/internal/update"
	"github.com/DIYgod/pinky/internal/video"
)

// Options configure the client.
type Options struct {
	// Version shows in Settings › Advanced and the About panel.
	Version string
	// Theme forces "light" or "dark"; empty follows the system.
	Theme string
	// Shortcut is the global Stop all shortcut, in MyGo's accelerator
	// syntax. Empty is Option-Command-Period on macOS and
	// Ctrl+Alt+Period elsewhere; "-" registers none.
	Shortcut string
	// NoTray leaves out the menu bar or notification area icon.
	NoTray bool
	// Menus are extra menus for the menu bar, after the client's own; the
	// demo adds its Demo menu.
	Menus []*mygo.MenuItem
	// OnReady runs on the main thread once the main window exists.
	OnReady func(main *mygo.Window)
	// Settings are the Safety, Privacy and Advanced choices to start with;
	// nil is the defaults. gateway.API has no settings methods yet, so the
	// client keeps them and reports each change through OnSettings, with
	// the settings as they were before it: pass on only what differs.
	Settings   *Settings
	OnSettings func(old, s Settings)
	// GatewaySettings reads again what the gateway keeps of Settings ›
	// Safety (Preset, HideCodes, ReadOnly and BlockedApps), which hand
	// edits of rules.yaml and apps.yaml change while Pinky runs. since is
	// the version of the policy it returned last, -1 at first; ok is false
	// while the policy is still at it. The client calls it off the main
	// thread every two seconds while Settings shows, and shows what it
	// returns; nil keeps what Settings started with.
	GatewaySettings func(since int) (s Settings, version int, ok bool)
	// RulesFile is the policy rules file that Settings › Advanced opens.
	RulesFile string
	// Agents lists the agents for Settings › Agents; nil shows those seen
	// in tasks.
	Agents []AgentInfo
	// StartPage opens "activity", "settings" or "add-phone" at start
	// instead of the phones.
	StartPage string
	// Hidden starts with only the menu bar icon and no window, as when an
	// agent launches Pinky through `pinky mcp`. The window opens by itself
	// when the first Needs you item arrives, and from the menu bar icon.
	Hidden bool
	// Secrets keeps the phones' PINs for unlocking from this computer; nil
	// is the system keychain (secret.System). The client only saves and
	// removes them: the gateway reads one, after Touch ID, to unlock.
	Secrets secret.Store
	// AgentToken is the token agents present, which Settings › Agents shows
	// for remote agents as their bearer token; empty leaves it out.
	AgentToken string
	// Telemetry counts usage and gathers feedback's diagnostics
	// (docs/TELEMETRY.md); nil leaves out Send Feedback and the Privacy
	// switch for usage statistics and crash reports.
	Telemetry *telemetry.Client
	// Updates finds and installs new versions of Pinky; nil leaves updates
	// out (tests, the demo). Changelog is CHANGELOG.md, for What's new, and
	// ChangelogTranslations are its translations by the catalogs' language
	// tags ("zh-Hans": CHANGELOG.zh-CN.md), which What's new shows in their
	// language, with CHANGELOG.md's notes of a version they lack.
	Updates               *update.Updater
	Changelog             string
	ChangelogTranslations map[string]string
	// AgentSetup finds the agents on this computer and adds Pinky to them
	// when the person clicks Connect in Settings › Agents, or in the
	// sidebar's card while no agent can reach Pinky; nil shows only the
	// commands to add an agent by hand.
	AgentSetup AgentSetup
}

// Settings are what the Settings screen changes.
type Settings struct {
	Preset             int // 1: payments, messages and deletions; 2: payments only (the default, recommended); 3: every action
	BlockedApps        []BlockedApp
	ReadOnly           bool
	UnlockFromComputer bool
	HideCodes          bool
	KeepDays           int
	Record             bool
	DeveloperMode      bool
	// RemoteAgents serves MCP over Streamable HTTP at RemoteAddr, for agents
	// that cannot start `pinky mcp`; each request needs the agent token.
	RemoteAgents bool
	RemoteAddr   string
	// UsageStats shares anonymous usage statistics and crash reports
	// (docs/TELEMETRY.md).
	UsageStats bool
	// AutoUpdate installs new versions of Pinky, and of the companion app
	// on Android phones, by themselves while no one uses them.
	AutoUpdate bool
	// KeepAwakeAway keeps the computer from idle sleep while a phone is
	// connected through Tailscale, away from its network.
	KeepAwakeAway bool
	// IOSWiFi uses iPhones and iPads over Wi-Fi when this computer reaches
	// them there (gateway.SetWiFi); off, only on a cable.
	IOSWiFi bool
}

// DefaultRemoteAddr is where Pinky serves MCP over HTTP: this computer
// only, on port 7465 (PINK on a phone keypad).
const DefaultRemoteAddr = "127.0.0.1:7465"

// BlockedApp is an app agents may not open. ID is its package name or
// bundle id when Pinky knows it, so that unblocking takes the app out of
// every entry that blocks it, not only its name.
type BlockedApp struct {
	Name, Kind string
	ID         string
}

// AgentInfo is a row of Settings › Agents.
type AgentInfo struct {
	Name      string
	Scope     string // "Can use Dinely, Chatter and Calendar"
	Connected bool
}

// DefaultSettings are the design's defaults.
func DefaultSettings() Settings {
	return Settings{
		Preset: 2,
		BlockedApps: []BlockedApp{
			{Name: "Northbank", Kind: "Banking"}, {Name: "Vaultly", Kind: "Passwords"}, {Name: "Coinpouch", Kind: "Wallet"},
		},
		UnlockFromComputer: true, HideCodes: true, KeepDays: 30, Record: true, RemoteAddr: DefaultRemoteAddr,
		UsageStats:    usageStatsByDefault,
		AutoUpdate:    true,
		KeepAwakeAway: true,
		IOSWiFi:       true,
	}
}

type page int

const (
	pagePhones page = iota
	pageActivity
	pageSettings
	pageAddPhone
)

// allPhones is the sidebar row that shows every phone.
const allPhones = "\x00all"

// app is the client's state. Everything in it is read and written on the
// main thread, by the views and by functions posted with post.
type app struct {
	api gateway.API
	opt Options
	ctx context.Context
	end context.CancelFunc

	main   *mygo.Window
	panel  *mygo.Window
	flyout *mygo.Window
	tray   *mygo.Tray

	devices []gateway.DeviceView
	tasks   []gateway.TaskView
	needs   []gateway.NeedView
	unlocks map[string]bool // phones with an open unlock need, by id
	loaded  bool

	page      page
	panelPage page   // the page syncPanel last saw, to sync again when it changes
	sel       string // the selected phone, or allPhones
	selShown  string // the selection the sidebar last scrolled into view

	mirrors map[string]*mirror
	shots   map[string]*mg.Bitmap // approval screenshots by need ID
	// An approval's screenshot shown larger: in viewer, a window beside
	// the floating panel, for the need viewing; over the main window for
	// the need shotOpen.
	viewer   *mygo.Window
	viewing  string
	shotOpen string
	panelMax int // the floating panel's height at most, on its display

	noting   map[string]bool   // needs whose Deny with a note is open
	notes    map[string]string // the notes
	details  map[string]bool   // tasks whose technical details show
	answered []answer          // recent answers the approval panel shows
	panelIdx int               // the approval the floating panel shows
	notified map[string]bool   // needs a notification went out for
	seenNeed map[string]bool

	settings Settings
	// reported is what the next change reports as the settings before it:
	// as OnSettings last reported them, with what GatewaySettings read
	// since. changes counts the changes, to drop a read they overtook.
	reported Settings
	changes  int
	act      activityState
	set      settingsState
	add      addState
	droid    droidState      // Add phone's Android steps
	chk      checkupState    // the phones' checkups
	more     map[string]bool // the More phone controls popover, per device
	rm       removeState     // the Remove this phone dialog (removephone.go)
	found    foundState      // phones found on Wi-Fi: Use and Ignore on their way (wifi.go)
	toasts   []string

	// control is the banner above each phone's mirror as it shows, which
	// changes between the person's gestures on the mirror (phone.go).
	control map[string]controlText

	quitting  bool
	focused   bool // the main window has the keyboard
	shortcut  string
	videoErr  error
	buildSeq  int
	updating  atomic.Bool
	callMu    sync.Mutex
	callQueue map[string]chan func()
	setq      settingsQueue // settings on their way to the gateway
	postHook  func(func())  // tests run posted functions themselves
	shownOnce bool          // the main window has shown (Options.Hidden)

	mediaCache map[string]*mediaEntry // step screenshots by media id
	previews   map[string]bool        // steps whose screenshot shows in a task card
	agents     agentsState
	always     alwaysState       // Settings › Safety › Always allowed
	apps       appsState         // Settings › Safety › Add app's apps on the phones
	rules      rulesState        // Settings › Safety › Rules
	files      fileProblemsState // rules files edited by hand that cannot be used
	gwSet      gwSettingsState   // what the gateway keeps of the settings, read again after hand edits

	postMu  sync.Mutex
	postWin *mygo.Window // the main window, once ready made it; post waits for it
	early   []func()     // posts made before that

	pin       pinState       // PINs for unlocking from this computer
	t         telState       // usage statistics, crash reports and feedback
	comp      companionState // the Android companion app, per phone
	bg        bgState        // background displays, per phone
	conn      connState      // iPhones: how Pinky connects, per phone
	rec       recState       // task recordings in Activity
	remote    remoteState    // Settings › Agents › Remote agents
	setup     setupState     // Settings › Agents › Connect an agent, and the sidebar's card
	upd       updateState    // new versions of Pinky and the companion app
	tn        tailnetState   // phones away from this computer's network, through Tailscale
	phoneWiFi phoneWiFiState // phones without Wireless debugging moved to Wi-Fi (wifiswitch.go)

	awake   func(on bool) // keeps the computer awake while agent tasks run; tests replace it
	awakeOn bool
	// awayAwake keeps it awake while a phone is away (tailnet.go); tests
	// replace it.
	awayAwake   func(on bool)
	awayAwakeOn bool
	exportTo    func() (string, error) // where exports go; tests replace it
	reveal      func(path string)      // shows an exported file; tests replace it
}

// Run starts the client and returns when it quits. It owns the main
// thread, as MyGo's App.Run does.
func Run(api gateway.API, opt Options) error {
	a := newApp(api, opt)
	if !mygo.App.IsPackaged() {
		// A packaged app keeps the name its bundle was built with.
		mygo.App.SetName("Pinky")
	}
	if opt.Version != "" {
		mygo.App.SetVersion(opt.Version)
	}
	switch opt.Theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	}
	if opt.Hidden && runtime.GOOS == "darwin" {
		// No Dock icon until the window first shows.
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWindowAllClosed(func() {}) // closing the window keeps the gateway in the menu bar
	mygo.App.OnActivate(func(bool) { a.showMain() })
	mygo.App.OnNotificationClick(func(id string) { a.openNeed(id) })
	mygo.App.WhenReady(a.ready)
	err := mygo.App.Run()
	a.shutdown()
	return err
}

func newApp(api gateway.API, opt Options) *app {
	ctx, end := context.WithCancel(context.Background())
	a := &app{
		api: api, opt: opt, ctx: ctx, end: end,
		sel:     "",
		mirrors: map[string]*mirror{}, shots: map[string]*mg.Bitmap{},
		noting: map[string]bool{}, notes: map[string]string{}, details: map[string]bool{},
		notified: map[string]bool{}, seenNeed: map[string]bool{}, unlocks: map[string]bool{}, more: map[string]bool{},
		callQueue:  map[string]chan func(){},
		mediaCache: map[string]*mediaEntry{}, previews: map[string]bool{},
		awake: keepAwake, awayAwake: keepAwakeAway, exportTo: exportFolder, reveal: revealFile,
	}
	a.pin = pinState{saved: map[string]bool{}, asked: map[string]bool{}, unlocking: map[string]bool{}, later: map[string]bool{},
		wrong: map[string]bool{}}
	a.comp = companionState{state: map[string]driver.CompanionState{}, checked: map[string]time.Time{},
		loading: map[string]bool{}, installing: map[string]time.Time{}, err: map[string]string{}, later: map[string]bool{}}
	a.bg = bgState{on: map[string]bool{}, busy: map[string]bool{}, phone: map[string]string{}}
	a.rec = recState{views: map[string]*recEntry{}}
	a.control = map[string]controlText{}
	if opt.Settings != nil {
		a.settings = opt.Settings.clone()
	} else {
		a.settings = DefaultSettings()
	}
	a.reported = a.settings.clone()
	a.gwSet.version = -1
	a.act.init()
	switch opt.StartPage {
	case "activity":
		a.page = pageActivity
	case "settings":
		a.page = pageSettings
	case "add-phone":
		a.page = pageAddPhone
	}
	return a
}

func (a *app) ready() {
	keepRunning()
	// Load FFmpeg now, so a missing one is reported at start rather than
	// as the first mirror opens.
	if err := video.Load(); err != nil {
		a.videoErr = err
		log.Printf("pinky: %v", err)
	}
	// Read the views before the window shows, so its first frame has the
	// phones rather than an empty sidebar; watch keeps them current.
	a.devices, a.tasks = a.api.Devices(), a.api.Tasks()
	a.setNeeds(a.api.Needs())
	a.setBackgrounds(a.readBackground(a.devices))
	a.loaded = true
	a.applySettings()
	a.afterRefresh(true)

	a.main = mygo.NewWindow(mygo.WindowOptions{
		Title: "Pinky", Width: 1280, Height: 820, MinWidth: 960, MinHeight: 620,
		StateKey: "main", TitleBarStyle: mygo.TitleBarHidden, Hidden: a.opt.Hidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 17},
		BackgroundColor:      "light-dark(#f7f7f8, #212125)",
		Content:              mg.View(a.view),
	})
	a.postMu.Lock()
	a.postWin = a.main
	early := a.early
	a.early = nil
	a.postMu.Unlock()
	for _, fn := range early {
		a.post(fn)
	}
	if runtime.GOOS != "darwin" {
		a.main.SetIcon(iconPNG())
	}
	a.main.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.main.Hide()
		}
	})
	// Hidden or minimized, the mirrors stop streaming; shown again, the
	// view builds anew and opens them.
	a.main.OnHide(a.closeMirrors)
	a.main.OnMinimize(a.closeMirrors)
	a.main.OnShow(a.main.Invalidate)
	a.main.OnRestore(a.main.Invalidate)
	a.main.OnFocus(func() { a.focused = true; a.syncPanel() })
	a.main.OnBlur(func() { a.focused = false; a.syncPanel() })
	a.focused = a.main.IsFocused()

	a.newPanel()
	if !a.opt.NoTray {
		a.newTray()
	}
	a.registerShortcut()
	a.setMenu()
	a.updateTray()
	// Windows draw anew in another language by themselves; the menus and
	// the tray's tooltip keep the labels they were given until set again.
	mygo.App.OnLocaleChanged(func(string) {
		a.setMenu()
		a.updateTray()
		a.invalidate()
	})
	// Started hidden while something already waits (an agent's first
	// connection, say): open for it now.
	if len(a.needs) > 0 {
		a.openForNeed(a.needs[0])
	}
	if a.opt.OnReady != nil {
		a.opt.OnReady(a.main)
	}
	go a.watch()
	a.startUpdates()
}

func (a *app) shutdown() {
	a.end()
	a.setAwake(false)
	a.setAwayAwake(false)
	for id, m := range a.mirrors {
		m.close()
		delete(a.mirrors, id)
	}
	mygo.GlobalShortcut.UnregisterAll()
}

// post runs fn on the main thread and redraws every window.
func (a *app) post(fn func()) {
	if a.postHook != nil {
		a.postHook(fn) // tests, which have no window
		return
	}
	// A goroutine may finish before the main window exists (a stream that
	// fails at once, opened by the window's first frame): keep its update
	// until ready has the window.
	a.postMu.Lock()
	w := a.postWin
	if w == nil {
		a.early = append(a.early, fn)
		a.postMu.Unlock()
		return
	}
	a.postMu.Unlock()
	w.Update(func() {
		fn()
		a.invalidate()
	})
}

// invalidate redraws the windows other than the main one, which Update
// redraws itself.
func (a *app) invalidate() {
	for _, w := range []*mygo.Window{a.panel, a.flyout, a.viewer} {
		if w != nil && !w.IsDestroyed() {
			w.Invalidate()
		}
	}
}

// watch reads the gateway's views and follows its events, a few at a
// time, until the client quits.
func (a *app) watch() {
	events := a.api.Subscribe(a.ctx)
	a.refresh(true, true, true)
	for {
		var ev gateway.Event
		var ok bool
		select {
		case ev, ok = <-events:
		case <-a.ctx.Done():
			return
		}
		if !ok {
			return
		}
		devs, tasks, needs := ev.Kind == gateway.EventDevices, ev.Kind == gateway.EventTasks, ev.Kind == gateway.EventNeeds
		// Events come in bursts: take what else arrives within a frame.
		timeout := time.After(16 * time.Millisecond)
	drain:
		for {
			select {
			case ev, ok = <-events:
				if !ok {
					break drain
				}
				switch ev.Kind {
				case gateway.EventDevices:
					devs = true
				case gateway.EventTasks:
					tasks = true
				case gateway.EventNeeds:
					needs = true
				}
			case <-timeout:
				break drain
			}
		}
		a.refresh(devs, tasks, needs)
	}
}

func (a *app) refresh(devs, tasks, needs bool) {
	var d []gateway.DeviceView
	var t []gateway.TaskView
	var n []gateway.NeedView
	var bg map[string]bool
	if devs {
		d = a.api.Devices()
		bg = a.readBackground(d)
	}
	if tasks {
		t = a.api.Tasks()
	}
	if needs {
		n = a.api.Needs()
	}
	a.post(func() {
		if devs {
			a.devices = d
			a.setBackgrounds(bg)
			for id := range a.pin.wrong {
				if dv := a.device(id); dv != nil && dv.Status != gateway.DeviceLocked && dv.Status != gateway.DeviceNeedsYou {
					delete(a.pin.wrong, id) // unlocked on the phone: the person may try again
				}
			}
		}
		if tasks {
			a.tasks = t
		}
		if needs {
			a.setNeeds(n)
		}
		first := !a.loaded
		a.loaded = true
		a.afterRefresh(first)
	})
}

// afterRefresh keeps the selection valid and opens Add phone on a first
// run without phones.
func (a *app) afterRefresh(first bool) {
	if first && len(a.devices) == 0 && a.page == pagePhones {
		a.page = pageAddPhone
	}
	if a.sel == allPhones && len(a.devices) < 2 {
		a.sel = ""
	}
	if a.sel != allPhones && a.device(a.sel) == nil {
		a.sel = ""
		if len(a.devices) > 0 {
			a.sel = a.devices[0].Info.ID
		}
	}
	a.loadPINs()
	a.setAwake(a.anyRunning())
	a.setAwayAwake(a.settings.KeepAwakeAway && a.anyAway())
	a.updateTray()
	a.syncPanel()
}

// setBackgrounds takes what readBackground read.
func (a *app) setBackgrounds(bg map[string]bool) {
	for id, on := range bg {
		if !a.bg.busy[id] {
			a.bg.on[id] = on
		}
	}
}

// anyRunning reports whether an agent task runs on any phone.
func (a *app) anyRunning() bool {
	for _, t := range a.tasks {
		if running(t.State) {
			return true
		}
	}
	return false
}

// setAwake keeps the computer from sleeping while agent tasks run, and
// lets it sleep again once they end.
func (a *app) setAwake(on bool) {
	if on == a.awakeOn || a.awake == nil {
		return
	}
	a.awakeOn = on
	a.awake(on)
}

// applySettings passes Record and Keep activity to a gateway that takes
// them, at start; Settings passes each change the same way.
func (a *app) applySettings() {
	a.applyRecording(a.settings.Record)
	a.applyRetention(a.settings.KeepDays)
}

// applyRecording, applyRetention and applyReadOnly pass a setting the
// gateway keeps to a gateway that takes it. The gateway may stop
// recordings or delete old tasks then, so the change goes through
// settingsQueue, off the main thread.
func (a *app) applyRecording(on bool) {
	if rs, ok := a.api.(recordingSetter); ok {
		a.setq.push("record", func() { rs.SetRecording(on) })
	}
}

func (a *app) applyRetention(days int) {
	if rs, ok := a.api.(retentionSetter); ok {
		a.setq.push("keep", func() { rs.SetRetention(days) })
	}
}

func (a *app) applyReadOnly(on bool) {
	if ro, ok := a.api.(readOnlySetter); ok {
		a.setq.push("read-only", func() { _ = ro.SetReadOnly("", on) }) // every phone
	}
}

// settingsQueue passes settings to the gateway off the main thread, one
// change at a time and in the order they were made, so two quick changes
// of a switch cannot reach the gateway the other way round. A change that
// has not started when its setting changes again is dropped: the last
// change wins, and a slow gateway is not asked for the states in between.
type settingsQueue struct {
	mu      sync.Mutex
	waiting []settingChange // oldest first, at most one per setting
	done    chan struct{}   // closed when the goroutine applying them stops; nil while none runs
}

type settingChange struct {
	key   string
	apply func()
}

// push queues apply as the latest change of the setting key.
func (q *settingsQueue) push(key string, apply func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiting = slices.DeleteFunc(q.waiting, func(c settingChange) bool { return c.key == key })
	q.waiting = append(q.waiting, settingChange{key: key, apply: apply})
	if q.done == nil {
		q.done = make(chan struct{})
		go q.run(q.done)
	}
}

func (q *settingsQueue) run(done chan struct{}) {
	for {
		q.mu.Lock()
		if len(q.waiting) == 0 {
			q.done = nil
			q.mu.Unlock()
			close(done)
			return
		}
		c := q.waiting[0]
		q.waiting = slices.Delete(q.waiting, 0, 1)
		q.mu.Unlock()
		c.apply()
	}
}

// wait returns once the changes queued so far have been applied.
func (q *settingsQueue) wait() {
	q.mu.Lock()
	done := q.done
	q.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (a *app) setNeeds(n []gateway.NeedView) {
	sort.SliceStable(n, func(i, j int) bool { return n[i].Created.Before(n[j].Created) })
	// A handoff on a locked phone shows as one card, with the unlock
	// button in it, rather than as a handoff and an unlock card.
	a.unlocks = map[string]bool{}
	handoff := map[string]bool{}
	for _, x := range n {
		switch x.Kind {
		case gateway.NeedUnlock:
			a.unlocks[x.DeviceID] = true
		case gateway.NeedHandoff:
			handoff[x.DeviceID] = true
		}
	}
	shown := n[:0:0]
	for _, x := range n {
		if x.Kind != gateway.NeedUnlock || !handoff[x.DeviceID] {
			shown = append(shown, x)
		}
	}
	n = shown
	// Settings › Agents reads the agents again when a first connection is
	// asked or answered.
	asks := 0
	for _, x := range n {
		if newAgentNeed(x) {
			asks++
		}
	}
	if asks != a.agents.asks {
		a.agents.asks, a.agents.lastLoad = asks, time.Time{}
	}
	a.needs = n
	live := map[string]bool{}
	for _, x := range n {
		live[x.ID] = true
		if !a.seenNeed[x.ID] {
			a.seenNeed[x.ID] = true
			a.notify(x)
			a.openForNeed(x)
		}
	}
	for id := range a.shots {
		if !live[id] {
			delete(a.shots, id)
		}
	}
	if a.viewing != "" && !live[a.viewing] {
		a.closeShot()
	}
	for id := range a.noting {
		if !live[id] {
			delete(a.noting, id)
			delete(a.notes, id)
		}
	}
	if a.panelIdx >= len(a.approvals()) {
		a.panelIdx = max(0, len(a.approvals())-1)
	}
}

func (a *app) device(id string) *gateway.DeviceView {
	for i := range a.devices {
		if a.devices[i].Info.ID == id {
			return &a.devices[i]
		}
	}
	return nil
}

func (a *app) taskOf(deviceID string) *gateway.TaskView {
	// The running task, or the one that ended last.
	var best *gateway.TaskView
	for i := range a.tasks {
		t := &a.tasks[i]
		if t.DeviceID != deviceID {
			continue
		}
		if running(t.State) {
			return t
		}
		if best == nil || t.Started.After(best.Started) {
			best = t
		}
	}
	return best
}

// approvals are the needs the floating panel shows: approvals and
// handoffs, everything but unlocks.
func (a *app) approvals() []gateway.NeedView {
	var out []gateway.NeedView
	for _, n := range a.needs {
		if n.Kind != gateway.NeedUnlock {
			out = append(out, n)
		}
	}
	return out
}

func (a *app) needsOf(deviceID string) []gateway.NeedView {
	var out []gateway.NeedView
	for _, n := range a.needs {
		if n.DeviceID == deviceID {
			out = append(out, n)
		}
	}
	return out
}

// call runs a gateway call off the main thread, one at a time per
// device so the person's input keeps its order, and shows its error.
func (a *app) call(deviceID, what string, fn func() error) {
	a.callMu.Lock()
	q, ok := a.callQueue[deviceID]
	if !ok {
		q = make(chan func(), 256)
		a.callQueue[deviceID] = q
		go func() {
			for f := range q {
				f()
			}
		}()
	}
	a.callMu.Unlock()
	job := func() {
		if err := fn(); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("pinky: %s: %v", what, err)
			a.post(func() { a.toasts = append(a.toasts, locales.ToastCouldNot(what, err.Error())) })
		}
	}
	select {
	case q <- job:
	default:
		// The gateway stopped answering; drop rather than block the UI.
	}
}

// --- actions ---

// selectPhone shows a phone, or the Phones page as it was for a need
// about no phone (an agent's first connection).
func (a *app) selectPhone(id string) {
	a.page = pagePhones
	if id != "" {
		a.sel = id
	}
}

func (a *app) decide(n gateway.NeedView, d gateway.Decision, note string) {
	a.remember(n, d, note)
	a.call(n.DeviceID, locales.CallAnswer(), func() error {
		err := a.api.Decide(n.ID, d, note)
		if d == gateway.AllowAlways {
			a.post(a.always.stale) // Settings › Safety lists it now
		}
		return err
	})
	if d == gateway.TakeOver {
		a.selectPhone(n.DeviceID)
		a.showMain()
	}
}

func (a *app) takeOver(id string) {
	a.call(id, locales.CallTakeOver(), func() error { return a.api.TakeOver(id) })
	a.selectPhone(id)
}

func (a *app) handBack(id string) {
	a.call(id, locales.CallHandBack(), func() error { return a.api.HandBack(id, "") })
}

func (a *app) stop(id string) {
	a.call(id, locales.CallStop(), func() error { return a.api.Stop(id) })
}

// stopAll is the emergency stop, from the sidebar, the flyout and the
// global shortcut.
func (a *app) stopAll() {
	a.call("", locales.CallStopAll(), a.api.StopAll)
}

func (a *app) showMain() {
	if a.main == nil {
		return
	}
	a.regularApp()
	if a.main.IsMinimized() {
		a.main.Restore()
	}
	a.main.Show()
	a.main.Focus()
}

// openForNeed opens the window of an app started hidden when something
// first needs the person, without taking the keyboard from the app in use.
func (a *app) openForNeed(n gateway.NeedView) {
	if !a.opt.Hidden || a.main == nil || a.main.IsVisible() || a.shownOnce {
		return
	}
	a.selectPhone(n.DeviceID)
	a.regularApp()
	a.main.ShowInactive()
}

// regularApp gives a hidden-started app its Dock icon once a window shows.
func (a *app) regularApp() {
	if a.opt.Hidden && !a.shownOnce && runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	a.shownOnce = true
}

// openNeed brings the main window up at the phone a need is about.
func (a *app) openNeed(id string) {
	for _, n := range a.needs {
		if n.ID == id {
			a.selectPhone(n.DeviceID)
		}
	}
	a.showMain()
	if a.main != nil {
		a.main.Invalidate()
	}
}

// notify sends a system notification for a new need while the main
// window is not in front.
func (a *app) notify(n gateway.NeedView) {
	if a.main == nil || a.notified[n.ID] || a.focused || !mygo.NotificationsSupported() {
		return
	}
	a.notified[n.ID] = true
	title := needTitle(n)
	sub := n.DeviceName
	if n.App != "" {
		sub += " · " + n.App
	}
	nt := mygo.NewNotification(mygo.NotificationOptions{ID: n.ID, Group: "needs", Title: title, Subtitle: sub, Body: needDetail(n)})
	nt.OnClick(func() {
		if n.Kind != gateway.NeedUnlock {
			a.showPanel(true)
		} else {
			a.openNeed(n.ID)
		}
		nt.Close()
	})
	go func() {
		if err := nt.Show(); err != nil && !errors.Is(err, mygo.ErrNotificationsDenied) {
			log.Printf("pinky: notification: %v", err)
		}
	}()
}

func (a *app) registerShortcut() {
	acc := a.opt.Shortcut
	if acc == "-" {
		return
	}
	if acc == "" {
		acc = "CmdOrCtrl+Alt+."
	}
	if err := mygo.GlobalShortcut.Register(acc, a.stopAll); err != nil {
		log.Printf("pinky: global shortcut %s: %v", acc, err)
		return
	}
	a.shortcut = acc
}

// shortcutLabel is how the sidebar and the flyout write the shortcut.
func shortcutLabel() string {
	if runtime.GOOS == "darwin" {
		return "⌥⌘."
	}
	return "Ctrl+Alt+."
}

func (a *app) setMenu() {
	items := []*mygo.MenuItem{}
	if runtime.GOOS == "darwin" {
		items = append(items, &mygo.MenuItem{Role: mygo.RoleAppMenu})
	}
	items = append(items,
		&mygo.MenuItem{Role: mygo.RoleEditMenu},
		&mygo.MenuItem{Label: locales.MenuPhones(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuActivity(), Accelerator: "CmdOrCtrl+1", Click: func(*mygo.MenuItem, *mygo.Window) { a.goPage(pageActivity) }},
			{Label: locales.MenuSettings(), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.goPage(pageSettings) }},
			{Label: locales.MenuAddPhone(), Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.goPage(pageAddPhone) }},
			mygo.Separator(),
			{Label: locales.MenuStopAll(), Click: func(*mygo.MenuItem, *mygo.Window) { a.stopAll() }},
		}},
	)
	items = append(items, a.opt.Menus...)
	if debugMenu != nil {
		items = append(items, debugMenu(a))
	}
	items = append(items, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	if help := a.helpMenu(); help != nil {
		items = append(items, help)
	}
	mygo.App.SetMenu(mygo.NewMenu(items))
}

func (a *app) goPage(p page) {
	a.page = p
	a.showMain()
	if a.main != nil {
		a.main.Invalidate()
	}
}

// debugMenu adds a Debug menu in test builds (tag e2e).
var debugMenu func(a *app) *mygo.MenuItem

// logf is for errors nobody needs to act on.
func logf(format string, args ...any) {
	if os.Getenv("PINKY_UI_DEBUG") != "" {
		log.Printf(format, args...)
	}
}
