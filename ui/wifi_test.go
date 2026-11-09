package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// foundApp is a client with an iPad that Pinky found on Wi-Fi.
func foundApp(t *testing.T, hold bool) (*testApp, string) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: hold})
	id := ta.api.FindOnWiFi()
	ta.waitFor(t, "the found iPad", func() bool { return ta.device(id) != nil })
	return ta, id
}

// TestFoundOnWiFiPage: the page of an iPad found on Wi-Fi says what it is
// and why Pinky waits; Use sets it up, and the steps follow it.
func TestFoundOnWiFiPage(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.sel = id
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	mustHave(t, tt, "Found Sam's iPad on Wi-Fi", "iPad Pro 11-inch (3rd generation) · iOS 27.0.1", locales.FoundDetail(),
		locales.FoundIgnoreNote(), "Found on Wi-Fi") // the sidebar's status line
	if hasText(tt, "See what's wrong") || hasText(tt, "Show the steps") {
		t.Errorf("a found phone's page offers the setup steps: %q", tt.Texts())
	}
	if err := tt.Click("Use Sam's iPad"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the iPad being prepared", func() bool {
		d := ta.device(id)
		return d != nil && d.Info.Setup == driver.SetupPreparing
	})
	tt.Frame()
	if hasText(tt, "Found Sam's iPad on Wi-Fi") {
		t.Error("the page still asks after Use")
	}
	ta.api.AdvanceIPhone()
	ta.waitFor(t, "the iPad online", func() bool { d := ta.device(id); return d != nil && d.Info.State == driver.StateOnline })
}

// TestFoundOnWiFiIgnore: Ignore takes the iPad off the list and says how
// to use it after all.
func TestFoundOnWiFiIgnore(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	mustHave(t, tt, "Sam's iPad", "Found on Wi-Fi")
	if err := tt.Click("Ignore Sam's iPad"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the iPad gone", func() bool { return ta.device(id) == nil && !ta.foundBusy(id) })
	if got := ta.api.Removals(); !slices.Equal(got, []fakeapi.Removal{{Device: id}}) {
		t.Errorf("removals %+v", got)
	}
	if !slices.Contains(ta.toasts, "Ignoring Sam's iPad. To use it after all, plug it in.") {
		t.Errorf("toasts %q", ta.toasts)
	}
}

// TestSettingsWiFiSwitch: Settings › Phones turns iPhones over Wi-Fi off and
// on again, in the gateway too.
func TestSettingsWiFiSwitch(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	mustHave(t, tt, "Use iPhones and iPads over Wi-Fi", locales.SettingsPhonesWiFiDesc())
	if !ta.settings.IOSWiFi {
		t.Fatal("iPhones over Wi-Fi are off by default")
	}
	if err := tt.Click("Use iPhones and iPads over Wi-Fi"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "Wi-Fi off in the gateway", func() bool { return !ta.api.WiFi() && ta.device(id) == nil })
	if ta.settings.IOSWiFi {
		t.Error("the setting stayed on")
	}
	tt.Frame()
	if err := tt.Click("Use iPhones and iPads over Wi-Fi"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "Wi-Fi on again", func() bool { return ta.api.WiFi() })
}

// TestAddIPhoneFound: Add phone › iPhone lists the phones found on Wi-Fi
// above the steps, which say which of them need the cable; once the
// person chooses Use, the steps follow the phone.
func TestAddIPhoneFound(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "iphone"
	tt := mg.NewTester(ta.view, 1280, 980)
	tt.Frame()
	mustHave(t, tt, locales.AddPhoneIPhoneFoundTitle(), locales.AddPhoneIPhoneFoundDetail(), "Sam's iPad",
		"Needs the cable", locales.AddPhoneIPhoneStepDevModeWiFi(), "Also works over Wi-Fi, more slowly", locales.AddPhoneIPhoneStepPlugWiFi(),
		"Looking for your iPhone…") // the found iPad is not followed by the steps
	if err := tt.Click("Use Sam's iPad"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the steps following the iPad", func() bool { tt.Frame(); return hasText(tt, "Preparing the iPhone…") })
	if hasText(tt, locales.AddPhoneIPhoneFoundDetail()) {
		t.Error("the found box stayed after Use")
	}
	_ = id

	// With Wi-Fi off, the steps say nothing about Wi-Fi.
	ta.settings.IOSWiFi = false
	tt.Frame()
	if hasText(tt, "Needs the cable") || hasText(tt, "Also works over Wi-Fi") {
		t.Errorf("Wi-Fi hints with Wi-Fi off: %q", tt.Texts())
	}
}

// TestTrustOverWiFi: an iPhone on Wi-Fi that no longer trusts the computer
// is told to plug it in, on its page and in Add phone.
func TestTrustOverWiFi(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateUnauthorized, Setup: driver.SetupTrust, Detail: "no longer trusted"})
	ta.waitFor(t, "the iPad waiting for Trust", func() bool { d := ta.device(id); return d != nil && d.Info.Setup == driver.SetupTrust })
	ta.sel = id
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	want := locales.AddPhoneIPhoneTrustWiFi("Sam's iPad")
	mustHave(t, tt, want)
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "iphone"
	tt.Frame()
	mustHave(t, tt, want)
}

// TestWiFiScreenshots draws a phone found on Wi-Fi (its page, Settings ›
// Phones and Add phone › iPhone), in each of the client's languages, into
// $PINKY_SCREENSHOTS:
//
//	PINKY_SCREENSHOTS=dir go test ./ui -run TestWiFiScreenshots
func TestWiFiScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	dark := os.Getenv("PINKY_SCREENSHOTS_DARK") != ""
	for _, lang := range testLanguages() {
		short := langShort(lang)
		inLanguage(t, lang)
		ta, id := foundApp(t, true)
		shoot := func(name string, w, h int) {
			t.Helper()
			tt := mg.NewTester(ta.view, w, h)
			inLanguage(t, lang, tt)
			tt.SetScale(2)
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(dir, name+"-"+short+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
		ta.sel = id
		shoot("wifi-found-page", 1280, 820)
		ta.page, ta.set.sec = pageSettings, secPhones
		shoot("wifi-settings", 1280, 900)
		ta.page = pageAddPhone
		ta.add.reset(ta.app)
		ta.add.step = "iphone"
		shoot("wifi-add-iphone", 1280, 1100)
		ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUnlock, Detail: "Unlock the iPhone."})
		ta.waitFor(t, "the iPad locked", func() bool { d := ta.device(id); return d != nil && lockedOnWiFi(*d) })
		ta.page, ta.sel = pagePhones, id
		shoot("wifi-locked-page", 1280, 820)
		ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline})
		ta.api.SetConnecting(id, "Moving to Wi-Fi")
		ta.waitFor(t, "the iPad moving", func() bool { d := ta.device(id); return d != nil && d.Status == gateway.DeviceConnecting })
		shoot("wifi-moving-page", 1280, 820)
		ta.api.SetConnecting(id, "")
		ta.api.LoseOnWiFi(id)
		ta.waitFor(t, "the iPad lost", func() bool { d := ta.device(id); return d != nil && lostOnWiFi(*d) })
		defer func(os string) { guideOS = os }(guideOS)
		guideOS = "darwin"
		shoot("wifi-lost-page", 1280, 820)
		ta.found.asking, ta.found.askFor = true, id
		shoot("wifi-lost-dialog", 1280, 820)
		ta.found.asking, ta.found.askFor = false, ""
	}
}

// TestLockedOnWiFiPage: an iPad locked on Wi-Fi says to unlock it, and why
// Pinky waits.
func TestLockedOnWiFiPage(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUnlock, Detail: "Unlock the iPhone."})
	ta.waitFor(t, "the iPad locked", func() bool { d := ta.device(id); return d != nil && lockedOnWiFi(*d) })
	ta.sel = id
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	mustHave(t, tt, "Sam's iPad is locked", locales.PhoneLockedWiFiDetail(), "Locked on Wi-Fi")
	if hasText(tt, "is offline") {
		t.Errorf("the page calls it offline: %q", tt.Texts())
	}
}

// TestConnectingPage: a phone Pinky is opening, or moving to Wi-Fi, says so
// on its page, in its header and in Settings, and offers no Take over.
func TestConnectingPage(t *testing.T) {
	ta, id := foundApp(t, true)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline})
	ta.api.SetConnecting(id, "Moving to Wi-Fi")
	ta.waitFor(t, "the iPad moving", func() bool { d := ta.device(id); return d != nil && d.Status == gateway.DeviceConnecting })
	ta.sel = id
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	mustHave(t, tt, "Moving Sam's iPad to Wi-Fi…", "iPad · Moving to Wi-Fi")
	if hasText(tt, "is offline") || hasText(tt, "Take over") {
		t.Errorf("a moving phone reads as offline or can be taken over: %q", tt.Texts())
	}
	ta.api.SetConnecting(id, "Connecting")
	ta.waitFor(t, "the iPad connecting", func() bool { d := ta.device(id); return d != nil && d.StatusLine == "Connecting" })
	tt.Frame()
	mustHave(t, tt, "Connecting to Sam's iPad…")
	ta.page, ta.set.sec = pageSettings, secPhones
	tt.Frame()
	mustHave(t, tt, "Connecting…")
}

// TestLostOnWiFiPage: an iPhone the device service lost on Wi-Fi says so,
// and its Restart button asks first, then restarts the service with the
// reason macOS shows, and brings the phone back. Declined, nothing is said.
func TestLostOnWiFiPage(t *testing.T) {
	defer func(os string) { guideOS = os }(guideOS)
	guideOS = "darwin"
	ta, id := foundApp(t, true)
	ta.api.UseFoundPhone(id)
	ta.api.LoseOnWiFi(id)
	ta.waitFor(t, "the iPad lost", func() bool { d := ta.device(id); return d != nil && lostOnWiFi(*d) })
	ta.sel = id
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	mustHave(t, tt, "This Mac lost Sam's iPad on Wi-Fi", locales.LostDetail("Sam's iPad"), locales.LostRestartNote(), "sudo killall usbmuxd", "Lost on Wi-Fi")
	if err := tt.Click("Restart device service…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Restart the device service?", locales.LostConfirmMessage("Sam's iPad"))
	if len(ta.api.Restarts()) != 0 {
		t.Fatal("restarted before the person confirmed")
	}
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the restart", func() bool { return len(ta.api.Restarts()) == 1 && !ta.found.restarting })
	if got := ta.api.Restarts(); got[0] != locales.LostReason() {
		t.Errorf("reason %q", got[0])
	}
	if !slices.Contains(ta.toasts, "Restarted the device service. Sam's iPad shows up again in a few seconds.") {
		t.Errorf("toasts %q", ta.toasts)
	}
	ta.waitFor(t, "the iPad back", func() bool { d := ta.device(id); return d != nil && d.Info.State == driver.StateOnline })

	// Declined in macOS's request: no notice.
	ta.api.LoseOnWiFi(id)
	ta.api.SetRestartError(fmt.Errorf("restarting the device service: %w", driver.ErrCanceled))
	ta.waitFor(t, "lost again", func() bool { d := ta.device(id); return d != nil && lostOnWiFi(*d) })
	ta.toasts = nil
	tt.Frame()
	if err := tt.Click("Restart device service…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the declined restart", func() bool { return len(ta.api.Restarts()) == 2 && !ta.found.restarting })
	for _, toast := range ta.toasts {
		if strings.Contains(toast, "device service") {
			t.Errorf("a notice for a declined restart: %q", toast)
		}
	}
}
