package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/ui/demogw"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// stepsProvider lists the phones a test sets, as their driver would while
// they wait for a step, and opens none.
type stepsProvider struct {
	platform driver.Platform
	mu       sync.Mutex
	infos    []driver.Info
}

func (p *stepsProvider) Platform() driver.Platform { return p.platform }

func (p *stepsProvider) List(context.Context) ([]driver.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.infos), nil
}

func (p *stepsProvider) Open(context.Context, string) (driver.Device, error) {
	return nil, driver.ErrOffline
}

// list lists infos from the next discovery on; none unplugs every phone.
func (p *stepsProvider) list(infos ...driver.Info) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.infos = infos
}

// newStepsApp is the client on the real gateway, with phones from ps.
func newStepsApp(t *testing.T, ps ...driver.Provider) *gwApp {
	t.Helper()
	g, err := gateway.New(gateway.Options{Providers: ps, Policy: policy.Default(), TrustNewAgents: true,
		DiscoverEvery: 20 * time.Millisecond, NoScreenshots: true})
	if err != nil {
		t.Fatal(err)
	}
	ga := &gwApp{w: &demogw.World{Gateway: g}}
	ga.app = newApp(g, Options{NoTray: true, Shortcut: "-"})
	testHooks(t, ga.app)
	ga.app.videoErr = errors.New("no video in tests")
	ga.app.postHook = func(fn func()) {
		ga.mu.Lock()
		ga.q = append(ga.q, fn)
		ga.mu.Unlock()
	}
	t.Cleanup(func() {
		ga.app.end()
		g.Close()
	})
	return ga
}

// TestPhonesThatNeedSetup: on the real gateway, phones their drivers list
// as waiting for a step (an Android phone to allow USB debugging, an
// iPhone to trust the computer, then Developer Mode) need setup; they are
// not offline. The sidebar, the tiles and the menu bar flyout say so, and
// each phone's page says the step in the person's words, as its checkup
// does, with See what's wrong and, for the iPhone, its steps in Add
// phone. Unplugged, they are offline and say no step. In each of the
// client's languages.
func TestPhonesThatNeedSetup(t *testing.T) {
	droid, ios := &stepsProvider{platform: driver.Android}, &stepsProvider{platform: driver.IOS}
	pixel := driver.Info{ID: "px-8a", Platform: driver.Android, Name: "Pixel 8a", Model: "Pixel 8a", OSVersion: "16",
		Connection: "usb", Backend: "scrcpy", State: driver.StateUnauthorized,
		Detail: "Unlock the phone and allow USB debugging for this computer."}
	iphone := driver.Info{ID: "00008140-0016", Platform: driver.IOS, Name: "iPhone 16", Model: "iPhone 16", OSVersion: "27.1",
		Connection: "usb", Backend: "coredevice", State: driver.StateUnauthorized, Setup: driver.SetupTrust,
		Detail: "Unlock the iPhone and tap Trust on the \"Trust This Computer?\" prompt, then enter its passcode."}
	droid.list(pixel)
	ios.list(iphone)
	ga := newStepsApp(t, droid, ios)
	is := func(id string, st gateway.DeviceStatus) bool { return ga.device(id).Status == st }
	ga.waitFor(t, "both phones", func() bool {
		return is(pixel.ID, gateway.DeviceNeedsSetup) && is(iphone.ID, gateway.DeviceNeedsSetup)
	})

	const w, h = 1280, 820
	for _, lang := range testLanguages() {
		tt := mg.NewTester(ga.view, w, h)
		inLanguage(t, lang, tt)
		status := locales.GwStatusNeedsSetup()
		page := func(name string, want, not []string) {
			t.Helper()
			tt.Frame()
			tt.Frame()
			for _, s := range want {
				if !hasText(tt, s) {
					t.Errorf("%s: %s's page lacks %q: %q", lang, name, s, tt.Texts())
				}
			}
			for _, s := range not {
				if hasText(tt, s) {
					t.Errorf("%s: %s's page shows %q: %q", lang, name, s, tt.Texts())
				}
			}
			for _, d := range []driver.Info{pixel, iphone} {
				if _, ok := tt.Find(locales.SidebarPhoneRow(d.Name, status)); !ok {
					t.Errorf("%s: the sidebar does not say %s needs setup: %q", lang, d.Name, tt.Texts())
				}
			}
		}

		ga.chk.showing = ""
		ga.selectPhone(pixel.ID)
		page("Pixel 8a", []string{locales.PhoneNeedsSetup(pixel.Name), locales.CheckAuthorizedAndroidFail(), locales.PhoneCheckupFix()},
			[]string{locales.PhoneOffline(pixel.Name), locales.PhoneReconnect(), locales.PhoneSetUpIPhone(), pixel.Detail})
		ga.selectPhone(iphone.ID)
		page("iPhone 16", []string{locales.PhoneNeedsSetup(iphone.Name), locales.AddPhoneIPhoneStepTrustDetail(),
			locales.PhoneCheckupFix(), locales.PhoneSetUpIPhone()},
			[]string{locales.PhoneOffline(iphone.Name), locales.PhoneReconnect(), iphone.Detail})

		// All phones: both tiles say it, and neither counts as offline.
		ga.sel = allPhones
		tt.Frame()
		tiles := 0
		for _, s := range tt.Texts() {
			if s == status {
				tiles++
			}
		}
		if tiles < 4 || !hasText(tt, locales.PhoneConnected(2)) || hasText(tt, locales.PhoneOfflineCount(1)) {
			t.Errorf("%s: All phones shows %d × %q: %q", lang, tiles, status, tt.Texts())
		}

		// The menu bar flyout: a click on a phone shows its page.
		ft := mg.NewTester(ga.flyoutView, 320, ga.flyoutHeight())
		inLanguage(t, lang, ft)
		ft.Frame()
		for _, d := range []driver.Info{pixel, iphone} {
			if _, ok := ft.Find(locales.FlyoutPhoneLabel(d.Name, status)); !ok {
				t.Errorf("%s: the flyout does not say %s needs setup: %q", lang, d.Name, ft.Texts())
			}
		}
		if err := ft.Click(locales.FlyoutPhoneLabel(pixel.Name, status)); err != nil || ga.sel != pixel.ID || ga.page != pagePhones {
			t.Errorf("%s: the flyout's Pixel 8a: %v, showing %q on page %v", lang, err, ga.sel, ga.page)
		}
	}

	// See what's wrong: the checkup says the same step.
	tt := mg.NewTester(ga.view, w, h)
	ga.selectPhone(pixel.ID)
	tt.Frame()
	if err := tt.Click(locales.PhoneCheckupFix()); err != nil {
		t.Fatal(err)
	}
	ga.waitFor(t, "the checkup", func() bool { tt.Frame(); r := ga.checkupOf(pixel.ID); return r != nil && r.have && !r.running })
	tt.Frame()
	if ga.chk.showing != pixel.ID || !hasText(tt, locales.CheckAuthorizedAndroidFail()) {
		t.Errorf("the checkup of the Pixel: %q", tt.Texts())
	}
	ga.chk.showing = ""

	// Show the steps: Add phone › iPhone follows the iPhone.
	ga.selectPhone(iphone.ID)
	tt.Frame()
	if err := tt.Click(locales.PhoneSetUpIPhone()); err != nil || ga.page != pageAddPhone || ga.add.focus != iphone.ID {
		t.Errorf("Show the steps: %v, page %v, following %q", err, ga.page, ga.add.focus)
	}

	// The iPhone trusts the computer and waits for Developer Mode.
	iphone.State, iphone.Setup = driver.StateNeedsSetup, driver.SetupDeveloperMode
	iphone.Detail = "Turn on Developer Mode: on the iPhone, open Settings > Privacy & Security > Developer Mode, turn it on and tap Restart."
	ios.list(iphone)
	ga.waitFor(t, "Developer Mode", func() bool { return ga.device(iphone.ID).Info.Setup == driver.SetupDeveloperMode })
	ga.selectPhone(iphone.ID)
	tt.Frame()
	if !hasText(tt, locales.AddPhoneIPhoneStepDevModeDetail()) || hasText(tt, iphone.Detail) || hasText(tt, locales.AddPhoneIPhoneStepTrustDetail()) {
		t.Errorf("waiting for Developer Mode: %q", tt.Texts())
	}

	// Unplugged while they wait: offline, without the step.
	droid.list()
	ios.list()
	ga.waitFor(t, "both phones offline", func() bool {
		return is(pixel.ID, gateway.DeviceOffline) && is(iphone.ID, gateway.DeviceOffline)
	})
	tt.Frame()
	tt.Frame()
	if !hasText(tt, locales.PhoneOffline(iphone.Name)) || !hasText(tt, locales.PhoneNotConnected()) ||
		hasText(tt, locales.PhoneNeedsSetup(iphone.Name)) || hasText(tt, locales.AddPhoneIPhoneStepDevModeDetail()) {
		t.Errorf("the unplugged iPhone's page: %q", tt.Texts())
	}
	for _, d := range []driver.Info{pixel, iphone} {
		if _, ok := tt.Find(locales.SidebarPhoneRow(d.Name, locales.GwStatusOffline())); !ok || hasText(tt, locales.GwStatusNeedsSetup()) {
			t.Errorf("the sidebar for the unplugged %s: %q", d.Name, tt.Texts())
		}
	}
}

// TestIPhonePasscodeStep: while iOS asks for its passcode before the
// WebDriverAgent helper may control the iPhone, the phone's page, Add
// phone › iPhone and the checkup say so in the person's language, not in
// the driver's English, and not as a step Pinky could not finish.
func TestIPhonePasscodeStep(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the iPhone", func() bool { return len(ta.devices) > 1 })
	id := ta.iphoneView(t).Info.ID
	const detail = "Enter the passcode on the iPhone: iOS asks for it now and then before the WebDriverAgent helper can control the phone."
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupPasscode, Detail: detail})
	ta.sync()
	for _, lang := range testLanguages() {
		tt := mg.NewTester(ta.view, 1280, 820)
		inLanguage(t, lang, tt)
		want := locales.AddPhoneIPhoneStepPasscodeDetail()
		english := func(where string) {
			t.Helper()
			if lang != "en" && hasText(tt, detail) {
				t.Errorf("%s: %s shows the driver's English: %q", lang, where, tt.Texts())
			}
		}

		ta.chk.showing = ""
		ta.selectPhone(id)
		tt.Frame()
		tt.Frame()
		if !hasText(tt, locales.PhoneNeedsSetup(ta.iphoneView(t).Info.Name)) || !hasText(tt, want) {
			t.Errorf("%s: the phone's page: %q", lang, tt.Texts())
		}
		english("the phone's page")

		ta.openCheckup(ta.iphoneView(t))
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
		if !hasText(tt, locales.AddPhoneIPhoneStepPasscode()) || !hasText(tt, want) {
			t.Errorf("%s: the checkup: %q", lang, tt.Texts())
		}
		english("the checkup")
		ta.chk.showing = ""

		ta.page = pageAddPhone
		ta.add.reset(ta.app)
		ta.add.step, ta.add.focus = "iphone", id
		tt.Frame()
		tt.Frame()
		if !hasText(tt, want) || hasText(tt, locales.AddPhoneIPhoneStuck()) || hasText(tt, locales.AddPhoneIPhoneRetrying()) {
			t.Errorf("%s: Add phone › iPhone: %q", lang, tt.Texts())
		}
		english("Add phone")
	}
}

// shows reports whether the frame has an element showing or labeled s
// exactly, such as a button.
func shows(tt *mg.Tester, s string) bool {
	_, ok := tt.Find(s)
	return ok
}

// TestAgentWaitsForSetupStep: on the real gateway, an agent connects to a
// phone that waits for a step and asks the person to do it. Its task card
// says the phone needs setup, without Take over; the handoff card says the
// agent goes on once the phone is ready, with Decline and Done but no Take
// over. Done before the step tells the agent the phone still waits.
func TestAgentWaitsForSetupStep(t *testing.T) {
	droid := &stepsProvider{platform: driver.Android}
	pixel := driver.Info{ID: "px-8a", Platform: driver.Android, Name: "Pixel 8a", Model: "Pixel 8a", OSVersion: "16",
		Connection: "usb", Backend: "scrcpy", State: driver.StateUnauthorized,
		Detail: "Unlock the phone and allow USB debugging for this computer."}
	droid.list(pixel)
	ga := newStepsApp(t, droid)
	ga.waitFor(t, "the phone", func() bool { return ga.device(pixel.ID).Status == gateway.DeviceNeedsSetup })
	ctx := context.Background()
	s := ga.w.Gateway.OpenSession(gateway.AgentInfo{Name: "Claude Code"})
	defer s.Close()
	if res := s.Connect(ctx, gateway.ConnectRequest{Call: gateway.Call{Device: pixel.ID}, Task: "Reorder lunch"}); res.Status != gateway.StatusNeedsSetup {
		t.Fatalf("connecting: %+v", res)
	}
	ga.waitFor(t, "the task", func() bool { return ga.taskOf(pixel.ID) != nil })
	ga.selectPhone(pixel.ID)
	for _, lang := range testLanguages() {
		tt := mg.NewTester(ga.view, 1280, 820)
		inLanguage(t, lang, tt)
		tt.Frame()
		if !hasText(tt, locales.TasksStatusNeedsSetup()) || !hasText(tt, locales.TasksStop()) || shows(tt, locales.PhoneTakeOver()) ||
			hasText(tt, locales.TasksStatusWorking()) {
			t.Errorf("%s: the task card: %q", lang, tt.Texts())
		}
	}

	done := make(chan *gateway.Result, 1)
	go func() {
		done <- s.Handoff(ctx, gateway.HandoffRequest{Reason: "Allow USB debugging on the Pixel"})
	}()
	ga.waitFor(t, "the handoff", func() bool { return len(ga.needs) > 0 })
	var tt *mg.Tester
	for _, lang := range append(testLanguages()[1:], "en") {
		tt = mg.NewTester(ga.view, 1280, 820)
		inLanguage(t, lang, tt)
		tt.Frame()
		if !hasText(tt, locales.NeedsHandoffSetup("Claude Code")) || !shows(tt, locales.NeedsDecline()) || !shows(tt, locales.NeedsDone()) ||
			shows(tt, locales.PhoneTakeOver()) {
			t.Errorf("%s: the handoff card: %q", lang, tt.Texts())
		}
	}
	if err := tt.Click(locales.NeedsDone()); err != nil {
		t.Fatal(err)
	}
	ga.sync()
	select {
	case r := <-done:
		if r.Status != gateway.StatusNeedsSetup || !strings.Contains(r.Message, "still waits") {
			t.Errorf("Done before the step: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Done did not answer the handoff")
	}
}

// TestIPhoneConnectionStep: an iPhone below iOS 27 set to connect through
// CoreDevice says, in the person's language, that it needs another
// connection, on its page, in its checkup and in Add phone, each with a
// way to Settings › Phones, where the connection is chosen; not the
// driver's English, the steps of Add phone, or a step Pinky retries.
func TestIPhoneConnectionStep(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the iPhone", func() bool { return len(ta.devices) > 1 })
	id := ta.iphoneView(t).Info.ID
	const detail = "Connecting without the helper needs iOS 27 or later; this iPhone runs iOS 18.6. Choose Automatic or WebDriverAgent in Settings > Phones."
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupConnection, Detail: detail, OSVersion: "18.6"})
	ta.sync()
	for _, lang := range testLanguages() {
		tt := mg.NewTester(ta.view, 1280, 820)
		inLanguage(t, lang, tt)
		want := locales.ConnectionChosenNeedsIOS27("18.6")
		check := func(where string, ok bool) {
			t.Helper()
			if !ok || hasText(tt, detail) {
				t.Errorf("%s: %s: %q", lang, where, tt.Texts())
			}
		}
		toSettings := func(where string) {
			t.Helper()
			if err := tt.Click(locales.ConnectionChoose()); err != nil || ta.page != pageSettings || ta.set.sec != secPhones {
				t.Errorf("%s: %s, Choose the connection: %v, page %v, section %v", lang, where, err, ta.page, ta.set.sec)
			}
		}

		ta.chk.showing = ""
		ta.selectPhone(id)
		tt.Frame()
		tt.Frame()
		check("the phone's page", hasText(tt, locales.PhoneNeedsSetup(ta.iphoneView(t).Info.Name)) && hasText(tt, want) &&
			!shows(tt, locales.PhoneSetUpIPhone()))
		toSettings("the phone's page")

		ta.openCheckup(ta.iphoneView(t))
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
		check("the checkup", hasText(tt, locales.ConnectionTitle()) && hasText(tt, want))
		toSettings("the checkup")
		ta.chk.showing = ""

		ta.page = pageAddPhone
		ta.add.reset(ta.app)
		ta.add.step, ta.add.focus = "iphone", id
		tt.Frame()
		tt.Frame()
		check("Add phone › iPhone", hasText(tt, want) && !hasText(tt, locales.AddPhoneIPhoneStuck()) && !hasText(tt, locales.AddPhoneIPhoneRetrying()))
		toSettings("Add phone › iPhone")
	}
}
