package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// helperApp is a test app with an iPhone plugged in, and the address the
// how-to buttons open.
func helperApp(t *testing.T) (*testApp, string, *string) {
	t.Helper()
	opened := new(string)
	saved := openURL
	openURL = func(_ *mg.Context, u string, done func(error)) { *opened = u; done(nil) }
	t.Cleanup(func() { openURL = saved })
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the iPhone", func() bool { return len(ta.devices) > 1 })
	return ta, ta.iphoneView(t).Info.ID, opened
}

// TestIPhoneHelperReminder: two days before the helper's signature runs
// out, an iPhone's page says when the helper stops working, with how to
// install it again, and so does its checkup. Days before, neither
// reminds; the checkup notes the day.
func TestIPhoneHelperReminder(t *testing.T) {
	ta, id, opened := helperApp(t)
	exp := time.Now().Add(30 * time.Hour)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline, HelperExpires: exp})
	ta.api.SetCheckup(id, driver.Checkup{System: "iOS 18.6", Checks: []driver.Check{
		{ID: driver.CheckHelper, Status: driver.CheckWarn, Fix: driver.FixInstallHelper, Detail: "WebDriverAgent answers; its provisioning profile runs out soon"},
		{ID: driver.CheckScreen, Status: driver.CheckPass},
	}})
	ta.sync()
	d := ta.iphoneView(t)
	soon := locales.CheckHelperSoon(d.Info.Name, exp.Local())
	ta.selectPhone(id)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if !hasText(tt, soon) {
		t.Fatalf("the phone's page lacks the reminder %q: %q", soon, tt.Texts())
	}
	if err := tt.Click(locales.CheckHelperHow()); err != nil || *opened != helperAgainDocs {
		t.Fatalf("how to install it again: %v, opened %q", err, *opened)
	}
	ta.openCheckup(d)
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
	if !hasText(tt, soon) || !hasText(tt, locales.CheckHelperHow()) {
		t.Errorf("the checkup lacks the reminder: %q", tt.Texts())
	}

	// Five days left: no reminder.
	exp = time.Now().Add(5 * 24 * time.Hour)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline, HelperExpires: exp})
	ta.api.SetCheckup(id, driver.Checkup{Checks: []driver.Check{{ID: driver.CheckHelper, Status: driver.CheckPass}}})
	ta.sync()
	ta.chk.showing = ""
	tt.Frame()
	tt.Frame()
	if hasText(tt, locales.CheckHelperSoon(d.Info.Name, exp.Local())) || hasText(tt, locales.CheckHelperHow()) {
		t.Errorf("a reminder five days before: %q", tt.Texts())
	}
	ta.openCheckup(ta.iphoneView(t))
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
	if !hasText(tt, locales.CheckHelperPass()) || !hasText(tt, locales.CheckHelperUntil(exp.Local())) {
		t.Errorf("the checkup lacks the day it runs out: %q", tt.Texts())
	}
}

// TestIPhoneHelperReminderLayout: the reminder takes its own room above
// the phone, wrapping in a narrow window, and the phone and its keys
// shrink for it rather than run off the window.
func TestIPhoneHelperReminderLayout(t *testing.T) {
	ta, id, _ := helperApp(t)
	exp := time.Now().Add(20 * time.Hour)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline, HelperExpires: exp})
	ta.sync()
	d := ta.iphoneView(t)
	ta.selectPhone(id)
	for _, lang := range testLanguages() {
		for _, size := range [][2]int{{1280, 820}, {900, 600}} {
			tt := mg.NewTester(ta.view, size[0], size[1])
			inLanguage(t, lang, tt)
			tt.Frame()
			tt.Frame()
			soon, _ := tt.Find(locales.CheckHelperSoon(d.Info.Name, exp.Local()))
			phone, ok := tt.Find(locales.PhoneScreenLabel(d.Info.Name))
			home, okHome := tt.Find(locales.PhoneKeyHome())
			if soon.H == 0 || !ok || !okHome {
				t.Fatalf("%s %v: reminder %v, phone %v %v, Home %v %v: %q", lang, size, soon, phone, ok, home, okHome, tt.Texts())
			}
			if soon.Y+soon.H+14 > phone.Y+0.5 || home.Y+home.H > float32(size[1])-16+0.5 {
				t.Errorf("%s %v: the reminder %v runs into the phone %v, or Home %v off the window", lang, size, soon, phone, home)
			}
		}
	}
}

// TestIPhoneHelperReminderGivesWay: while the person is in control, the
// banner takes the reminder's place above the phone; once they hand the
// phone back, the reminder is back. Neither covers the phone.
func TestIPhoneHelperReminderGivesWay(t *testing.T) {
	ta, id, _ := helperApp(t)
	exp := time.Now().Add(20 * time.Hour)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline, HelperExpires: exp})
	ta.sync()
	d := ta.iphoneView(t)
	soon := locales.CheckHelperSoon(d.Info.Name, exp.Local())
	img := image.NewRGBA(image.Rect(0, 0, 1170, 2532))
	stillMirror(ta, id, img)
	ta.selectPhone(id)
	const w, h = 1280, 820
	tt := mg.NewTester(ta.view, w, h)
	tt.Frame()
	reminderFits := func(what string) {
		t.Helper()
		r, ok := tt.Find(soon) // the reminder's box, labeled with its words
		b := findPhone(t, tt, d.Info.Name)
		if !ok || b.banner || r.Y < 58+16-0.5 || r.Y+r.H+14 > b.fig.Y+0.5 || b.keys.Y+b.keys.H > h-16+0.5 {
			t.Fatalf("%s: the reminder %v (shown %v), the phone %v, Home %v, Hand back %v", what, r, ok, b.fig, b.keys, b.banner)
		}
	}
	reminderFits("before")

	ta.takeOver(id)
	ta.waitFor(t, "the person in control", func() bool { return ta.iphoneView(t).Status == gateway.DeviceInControl })
	tt.Frame()
	tt.Frame()
	b := findPhone(t, tt, d.Info.Name)
	if hasText(tt, soon) {
		t.Fatalf("the reminder shows with the banner: %q", tt.Texts())
	}
	checkBanner(t, b, w, h)

	ta.handBack(id)
	ta.waitFor(t, "the phone handed back", func() bool { return ta.iphoneView(t).Status != gateway.DeviceInControl })
	tt.Frame()
	tt.Frame()
	reminderFits("after Hand back")
}

// TestIPhoneHelperRanOut: once the helper's signature ran out, the iPhone
// needs setup, and its page, its checkup and Add phone say why in words,
// with how to install it again, not the driver's English or the steps of
// preparing it.
func TestIPhoneHelperRanOut(t *testing.T) {
	ta, id, opened := helperApp(t)
	exp := time.Now().Add(-3 * time.Hour)
	const detail = "The WebDriverAgent helper's provisioning profile ran out"
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupInstallHelper, Detail: detail, HelperExpires: exp})
	ta.sync()
	d := ta.iphoneView(t)
	ranOut := locales.CheckHelperExpired(d.Info.Name, exp.Local())
	ta.selectPhone(id)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if !hasText(tt, ranOut) || hasText(tt, detail) || hasText(tt, locales.PhoneSetUpIPhone()) {
		t.Fatalf("the phone's page: %q; want %q, without the driver's words or the steps", tt.Texts(), ranOut)
	}
	if err := tt.Click(locales.CheckHelperHow()); err != nil || *opened != helperAgainDocs {
		t.Fatalf("how to install it again: %v, opened %q", err, *opened)
	}
	// Listed, not opened: it needs setup, in the sidebar too.
	if _, ok := tt.Find(locales.SidebarPhoneRow(d.Info.Name, locales.GwStatusNeedsSetup())); !ok ||
		d.Status != gateway.DeviceNeedsSetup || hasText(tt, locales.PhoneOffline(d.Info.Name)) {
		t.Fatalf("listed as %q: %q", d.Status, tt.Texts())
	}

	*opened = ""
	ta.openCheckup(d)
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
	for _, want := range []string{locales.CheckHelperTitle(), ranOut} {
		if !hasText(tt, want) {
			t.Errorf("the checkup lacks %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(locales.CheckHelperHow()); err != nil || *opened != helperAgainDocs {
		t.Errorf("the checkup's how to install it again: %v, opened %q", err, *opened)
	}

	ta.chk.showing = ""
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step, ta.add.focus = "iphone", id
	tt.Frame()
	tt.Frame()
	if !hasText(tt, ranOut) || hasText(tt, locales.AddPhoneIPhoneRetrying()) || !hasText(tt, locales.CheckHelperHow()) {
		t.Errorf("Add phone › iPhone: %q", tt.Texts())
	}

	// Chosen in Settings › Phones without the helper installed.
	ta.page = pagePhones
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupInstallHelper, Detail: "The WebDriverAgent helper is not installed on this iPhone."})
	ta.sync()
	ta.selectPhone(id)
	tt.Frame()
	if !hasText(tt, locales.CheckHelperMissing()) {
		t.Errorf("without the helper: %q", tt.Texts())
	}
	*opened = ""
	if err := tt.Click(locales.CheckHelperHow()); err != nil || *opened != helperDocs {
		t.Errorf("how to install it: %v, opened %q", err, *opened)
	}
}

// TestIPhoneHelperScreenshots draws an iPhone's page with the reminder,
// its checkup, and its page once the helper ran out, into
// $PINKY_SCREENSHOTS, in each of the client's languages:
//
//	PINKY_SCREENSHOTS=dir go test ./ui -run TestIPhoneHelperScreenshots
func TestIPhoneHelperScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	dark := os.Getenv("PINKY_SCREENSHOTS_DARK") != ""
	ta, id, _ := helperApp(t)
	exp := time.Now().Add(30 * time.Hour)
	shoot := func(name string, lang string, size ...int) {
		t.Helper()
		w, h := 1280, 820
		if len(size) == 2 {
			w, h = size[0], size[1]
		}
		st := mg.NewTester(ta.view, w, h)
		inLanguage(t, lang, st)
		st.SetScale(2)
		st.SetDark(dark)
		st.Frame()
		st.Frame()
		f, err := os.Create(filepath.Join(dir, name+"-"+lang+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, st.Image()); err != nil {
			t.Fatal(err)
		}
	}
	for _, lang := range testLanguages() {
		inLanguage(t, lang)
		ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline, HelperExpires: exp})
		ta.api.SetCheckup(id, driver.Checkup{System: "iOS 18.6", Checks: []driver.Check{
			{ID: driver.CheckDeveloperMode, Status: driver.CheckPass},
			{ID: driver.CheckDiskImage, Status: driver.CheckPass},
			{ID: driver.CheckHelper, Status: driver.CheckWarn, Fix: driver.FixInstallHelper},
			{ID: driver.CheckScreen, Status: driver.CheckPass},
			{ID: driver.CheckInput, Status: driver.CheckPass},
		}})
		ta.sync()
		ta.selectPhone(id)
		ta.chk.showing = ""
		shoot("helper-reminder", lang)
		shoot("helper-reminder-narrow", lang, 900, 600)
		tt := mg.NewTester(ta.view, 1280, 820)
		ta.openCheckup(ta.iphoneView(t))
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
		shoot("helper-checkup", lang)
		ta.chk.showing = ""
		ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupInstallHelper, HelperExpires: exp.Add(-48 * time.Hour),
			Detail: "The WebDriverAgent helper's provisioning profile ran out"})
		ta.sync()
		shoot("helper-ran-out", lang)
	}
}
