package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// iphoneView is the fake iPhone ConnectIPhone plugged in.
func (ta *testApp) iphoneView(t *testing.T) gateway.DeviceView {
	t.Helper()
	for _, d := range ta.devices {
		if d.Info.Platform == driver.IOS && d.Info.State != driver.StateOffline && d.Info.ID != "iphone-17" {
			return d
		}
	}
	t.Fatal("no new iPhone")
	return gateway.DeviceView{}
}

// TestCheckupOfIPhone: an online iPhone's items in words, and the image it
// could not mount again after a restart pointing to the steps.
func TestCheckupOfIPhone(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the iPhone", func() bool { return len(ta.devices) > 1 })
	d := ta.iphoneView(t)
	ta.api.SetIPhone(d.Info.ID, driver.Info{State: driver.StateOnline})
	ta.api.SetCheckup(d.Info.ID, driver.Checkup{System: "iOS 27.1", Checks: []driver.Check{
		{ID: driver.CheckDeveloperMode, Status: driver.CheckPass},
		{ID: driver.CheckDiskImage, Status: driver.CheckWarn, Fix: driver.SetupNoDiskImage, Detail: "no image on this computer"},
		{ID: driver.CheckScreen, Status: driver.CheckPass},
		{ID: driver.CheckInput, Status: driver.CheckPass},
	}})
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.openCheckup(ta.iphoneView(t))
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(d.Info.ID); return r != nil && r.have && !r.running })
	for _, want := range []string{"Developer Mode", locales.CheckDeveloperModePass(), "Developer disk image", locales.CheckDiskImageNoCopy(),
		locales.CheckInputIPhonePass(), "System: iOS 27.1", locales.CheckTrustedPass()} {
		if !hasText(tt, want) {
			t.Errorf("the checkup lacks %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(locales.PhoneSetUpIPhone()); err != nil {
		t.Fatalf("no way to the steps: %v (%q)", err, tt.Texts())
	}
	tt.Frame()
	if ta.page != pageAddPhone || ta.add.step != "iphone" || ta.add.focus != d.Info.ID {
		t.Errorf("the steps did not open for the phone: page %v, step %q, focus %q", ta.page, ta.add.step, ta.add.focus)
	}
}

// TestCheckupOfIPhoneWaitingForAStep: an iPhone that is not online gets
// its step (Info.Setup) as the authorized item, in words.
func TestCheckupOfIPhoneWaitingForAStep(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the iPhone", func() bool { return len(ta.devices) > 1 })
	d := ta.iphoneView(t)
	ta.api.SetIPhone(d.Info.ID, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode, Detail: "Turn on Developer Mode"})
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.openCheckup(ta.iphoneView(t))
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(d.Info.ID); return r != nil && r.have && !r.running })
	for _, want := range []string{"Developer Mode", "Settings › Privacy & Security › Developer Mode", locales.PhoneSetUpIPhone()} {
		if !hasText(tt, want) {
			t.Errorf("the checkup lacks %q: %q", want, tt.Texts())
		}
	}
}

func TestIPhoneSetupWords(t *testing.T) {
	d := gateway.DeviceView{Info: driver.Info{Platform: driver.IOS, Name: "iPhone 8", OSVersion: "16.7"}}
	for _, step := range []string{driver.SetupTrust, driver.SetupDeveloperMode, driver.SetupUnlock, driver.SetupPreparing,
		driver.SetupNoDiskImage, driver.SetupDiskImage, driver.SetupDiskImageRefused, driver.SetupUpdate, driver.SetupInstallHelper} {
		if title, text, _ := setupWords(d, step); title == "" || text == "" {
			t.Errorf("no words for %q", step)
		}
	}
	if title, _, _ := setupWords(d, "something new"); title != "" {
		t.Error("an unknown step should fall back to the driver's words")
	}
}

// TestCheckupIPhoneScreenshots draws an iPhone's checkup, online with an
// item to look at, into $PINKY_SCREENSHOTS, in English and Chinese.
func TestCheckupIPhoneScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the iPhone", func() bool { return len(ta.devices) > 1 })
	d := ta.iphoneView(t)
	ta.api.SetIPhone(d.Info.ID, driver.Info{State: driver.StateOnline})
	ta.api.SetCheckup(d.Info.ID, driver.Checkup{System: "iOS 27.1", Checks: []driver.Check{
		{ID: driver.CheckDeveloperMode, Status: driver.CheckPass},
		{ID: driver.CheckDiskImage, Status: driver.CheckWarn, Fix: driver.SetupNoDiskImage},
		{ID: driver.CheckScreen, Status: driver.CheckPass},
		{ID: driver.CheckInput, Status: driver.CheckPass},
	}})
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.openCheckup(ta.iphoneView(t))
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(d.Info.ID); return r != nil && r.have && !r.running })
	shoot := func(name string) {
		st := mg.NewTester(ta.view, 1280, 820)
		st.SetScale(2)
		st.Frame()
		st.Frame()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, st.Image()); err != nil {
			t.Fatal(err)
		}
	}
	shoot("iphone-checkup.png")
	inLanguage(t, "zh-Hans", tt)
	shoot("iphone-checkup-zh.png")
}

// TestCheckupRefusedImage says which image Apple refused and, for Xcode's
// and one in Pinky's folder, that the steps also offer the third-party
// mirror's.
func TestCheckupRefusedImage(t *testing.T) {
	var a app
	ck := driver.Check{ID: driver.CheckAuthorized, Status: driver.CheckFail, Fix: driver.SetupDiskImageRefused}
	d := gateway.DeviceView{Info: driver.Info{Platform: driver.IOS, State: driver.StateNeedsSetup, Setup: driver.SetupDiskImageRefused,
		DiskImage: &driver.DiskImage{Source: driver.DiskImageFromXcode, Build: "17B48", Version: "17.2.48.0"}}}
	w, ok := a.iphoneCheckWords(d, ck)
	if !ok || w.title != locales.AddPhoneIPhoneRefusedTitle() || !strings.Contains(w.text, "17B48") || w.note != locales.CheckDiskImageRefusedNote() || w.detail {
		t.Errorf("Xcode's image refused: %+v", w)
	}
	d.Info.DiskImage = &driver.DiskImage{Source: driver.DiskImageFromFolder, Build: "17B48", Version: "17.2.48.0", Dir: `C:\Users\me\AppData\Local\Pinky\DeveloperDiskImages\iOS_DDI\Restore`}
	w, _ = a.iphoneCheckWords(d, ck)
	if w.note != locales.CheckDiskImageRefusedNote() || !strings.Contains(w.text, `Pinky\DeveloperDiskImages`) {
		t.Errorf("an image in Pinky's folder refused: %+v", w)
	}
	for _, source := range []string{driver.DiskImageFromMirror, driver.DiskImageFromEnv} {
		d.Info.DiskImage = &driver.DiskImage{Source: source, Build: "27A5228h", Version: "27.1.5228.8"}
		w, _ = a.iphoneCheckWords(d, ck)
		if w.note != locales.AddPhoneIPhoneRefusedRetry() || !strings.Contains(w.text, "27A5228h") {
			t.Errorf("the %s image refused: %+v", source, w)
		}
	}
}
