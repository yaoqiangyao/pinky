package ui

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

func TestIPhoneStep(t *testing.T) {
	dev := func(state driver.State, setup string) *gateway.DeviceView {
		return &gateway.DeviceView{Info: driver.Info{Platform: driver.IOS, State: state, Setup: setup}}
	}
	for _, c := range []struct {
		d    *gateway.DeviceView
		host driver.HostCheck
		want int
	}{
		{nil, driver.HostCheck{Missing: "apple_devices"}, iphoneHost},
		{nil, driver.HostCheck{Missing: "usbmuxd_stopped"}, iphonePlug}, // installed: it starts with the phone
		{nil, driver.HostCheck{}, iphonePlug},
		{dev(driver.StateOffline, ""), driver.HostCheck{}, iphonePlug},
		{dev(driver.StateUnauthorized, driver.SetupTrust), driver.HostCheck{}, iphoneTrust},
		{dev(driver.StateNeedsSetup, driver.SetupDeveloperMode), driver.HostCheck{}, iphoneDevMode},
		{dev(driver.StateNeedsSetup, driver.SetupPreparing), driver.HostCheck{}, iphonePrepare},
		{dev(driver.StateNeedsSetup, driver.SetupNoDiskImage), driver.HostCheck{}, iphonePrepare},
		{dev(driver.StateNeedsSetup, ""), driver.HostCheck{}, iphonePrepare},
		{dev(driver.StateOnline, ""), driver.HostCheck{}, iphoneReady},
	} {
		if got := iphoneStep(c.d, c.host); got != c.want {
			t.Errorf("iphoneStep(%+v, %+v) = %d, want %d", c.d, c.host, got, c.want)
		}
	}
}

// addIPhoneApp opens Add phone › iPhone on a fake that holds a new iPhone
// at each step until the test moves it on.
func addIPhoneApp(t *testing.T) (*testApp, *mg.Tester) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.sync()
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "iphone"
	ta.add.snapshot(ta.app)
	return ta, mg.NewTester(ta.view, 1280, 820)
}

// TestAddIPhoneSteps walks a new iPhone through Add phone: each step says
// what to do on the phone, and the next one shows once the phone gets there.
func TestAddIPhoneSteps(t *testing.T) {
	ta, tt := addIPhoneApp(t)
	tt.Frame()
	for _, want := range []string{"Connect your iPhone", "Plug in the iPhone", "Looking for your iPhone…", "Trust this computer", "Turn on Developer Mode", "Prepare the iPhone"} {
		if !hasText(tt, want) {
			t.Fatalf("before the phone: no %q in %q", want, tt.Texts())
		}
	}
	ta.api.ConnectIPhone()
	for _, step := range []struct{ what, text string }{
		{"the Trust step", "Waiting for the iPhone to trust this computer…"},
		{"the Developer Mode step", "Settings › Privacy & Security › Developer Mode"},
		{"the preparation step", "Preparing the iPhone…"},
		{"the phone ready", "iPhone 16 is ready"},
	} {
		ta.waitFor(t, step.what, func() bool { tt.Frame(); return hasText(tt, step.text) })
		if step.text != "Waiting for the iPhone to trust this computer…" && !hasText(tt, "Found iPhone 16 · iOS 27.1") {
			t.Errorf("%s: the found phone is not named in %q", step.what, tt.Texts())
		}
		ta.api.AdvanceIPhone()
	}
	if _, ok := tt.Find("Done"); !ok {
		t.Fatal("no Done button once the phone is ready")
	}
}

// TestAddIPhoneStuck shows why the preparation step is stuck, in words for
// the case of a computer without the developer disk image.
func TestAddIPhoneStuck(t *testing.T) {
	ta, tt := addIPhoneApp(t)
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
	id := ta.add.iphone(ta.app).Info.ID
	defer func(os string) { guideOS = os }(guideOS)

	guideOS = "darwin"
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage, Detail: "Pinky needs Apple's developer disk image."})
	ta.waitFor(t, "the missing image", func() bool { tt.Frame(); return hasText(tt, "This computer has no developer disk image yet") })
	if !hasText(tt, "Install Xcode from the App Store and open it once") {
		t.Errorf("no word of Xcode in %q", tt.Texts())
	}
	if _, ok := tt.Find("Get Xcode in the App Store"); !ok {
		t.Error("no App Store link")
	}

	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDiskImage, Detail: "Pinky could not reach Apple's signing server (gs.apple.com)."})
	ta.waitFor(t, "the failed mount", func() bool { tt.Frame(); return hasText(tt, "Pinky could not finish this step.") })
	if !hasText(tt, "gs.apple.com") || !hasText(tt, "Pinky tries again by itself.") {
		t.Errorf("the reason or the retry note is missing: %q", tt.Texts())
	}

	// Apple's signing server refused Xcode's image: no retry with it, the
	// image named, and Xcode to update.
	refused := driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDiskImageRefused,
		Detail:    "Apple's signing server would not sign the developer disk image Pinky found for this iPhone.",
		DiskImage: &driver.DiskImage{Source: driver.DiskImageFromXcode, Build: "17B48", Version: "17.2.48.0", Dir: "/Library/Developer/DeveloperDiskImages/iOS_DDI/Restore"}}
	ta.api.SetIPhone(id, refused)
	ta.waitFor(t, "the refused image", func() bool { tt.Frame(); return hasText(tt, "Apple won't sign this developer disk image") })
	for _, want := range []string{"Xcode installed on this Mac: build 17B48, version 17.2.48.0", "Install or update Xcode from the App Store", "as soon as another image is on this computer"} {
		if !hasText(tt, want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if hasText(tt, "Pinky tries again by itself.") || hasText(tt, "Pinky could not finish this step.") {
		t.Errorf("a refused image is not one to retry: %q", tt.Texts())
	}
	if _, ok := tt.Find("Get Xcode in the App Store"); !ok {
		t.Error("no App Store link for Xcode's image")
	}
	// The other way: the third-party mirror's image, on a click.
	if !hasText(tt, "Or download it from a third-party mirror") {
		t.Errorf("no mirror offered after Xcode's image was refused: %q", tt.Texts())
	}
	if _, ok := tt.Find("Download (32 MB)"); !ok {
		t.Errorf("no Download button: %q", tt.Texts())
	}
	refused.DiskImage = &driver.DiskImage{Source: driver.DiskImageFromMirror, Build: "27A5228h", Version: "27.1.5228.8"}
	ta.api.SetIPhone(id, refused)
	ta.waitFor(t, "the refused mirror image", func() bool { tt.Frame(); return hasText(tt, "downloaded from the third-party mirror: build 27A5228h") })
	if !hasText(tt, "Install or update Xcode") || !hasText(tt, "update Pinky once a version with a newer image is out") {
		t.Errorf("no word of a newer Xcode or Pinky: %q", tt.Texts())
	}
	if hasText(tt, "Or download it from a third-party mirror") {
		t.Errorf("the mirror offered again after Apple refused its image: %q", tt.Texts())
	}
	refused.DiskImage = &driver.DiskImage{Source: driver.DiskImageFromFolder, Build: "17B48", Dir: "/Users/me/Pinky/DeveloperDiskImages/iOS_DDI/Restore"}
	ta.api.SetIPhone(id, refused)
	ta.waitFor(t, "the refused image in Pinky's folder", func() bool {
		tt.Frame()
		return hasText(tt, "in /Users/me/Pinky/DeveloperDiskImages/iOS_DDI/Restore: build 17B48, version –")
	})
	if _, ok := tt.Find("Get Xcode in the App Store"); ok {
		t.Errorf("Xcode offered for an image the person put in Pinky's folder: %q", tt.Texts())
	}
	// Windows and Linux have no Xcode: the mirror's image is the other way.
	if !hasText(tt, "Or download it from a third-party mirror") {
		t.Errorf("no mirror offered after the image in Pinky's folder was refused: %q", tt.Texts())
	}
	// PINKY_DDI's image is the only one Pinky uses while it is set: no
	// download would be used.
	refused.DiskImage = &driver.DiskImage{Source: driver.DiskImageFromEnv, Build: "17B48", Version: "17.2.48.0", Dir: "/opt/ddi/Restore"}
	ta.api.SetIPhone(id, refused)
	ta.waitFor(t, "the refused PINKY_DDI image", func() bool { tt.Frame(); return hasText(tt, "in /opt/ddi/Restore: build 17B48") })
	if hasText(tt, "Or download it from a third-party mirror") {
		t.Errorf("the mirror offered for PINKY_DDI's image: %q", tt.Texts())
	}

	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUnlock})
	ta.waitFor(t, "the unlock step", func() bool { tt.Frame(); return hasText(tt, "Unlock the iPhone so Pinky can finish.") })

	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUpdate, OSVersion: "16.7"})
	ta.waitFor(t, "an old iOS", func() bool { tt.Frame(); return hasText(tt, "needs a newer iOS") })
}

// TestAddIPhoneDriver asks Windows for Apple's iPhone driver first.
func TestAddIPhoneDriver(t *testing.T) {
	defer func(os string) { guideOS = os }(guideOS)
	guideOS = "windows"
	ta, tt := addIPhoneApp(t)
	ta.api.SetHostCheck(driver.HostCheck{Missing: "apple_devices", Detail: "install Apple Devices"})
	ta.waitFor(t, "the driver step", func() bool { tt.Frame(); return hasText(tt, "Waiting for it to be installed…") })
	if !hasText(tt, "Install Apple's iPhone driver") || !hasText(tt, "Microsoft Store") {
		t.Fatalf("no driver step in %q", tt.Texts())
	}
	ta.api.SetHostCheck(driver.HostCheck{})
	ta.add.hostAt = ta.add.hostAt.AddDate(0, 0, -1) // ask again now
	ta.waitFor(t, "the plug-in step", func() bool { tt.Frame(); return hasText(tt, "Looking for your iPhone…") })
}

// TestAddIPhoneScreenshots draws each step in light and dark, and in
// Chinese, into $PINKY_SCREENSHOTS, for the notes.
func TestAddIPhoneScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	ta, tt := addIPhoneApp(t)
	shoot := func(name string) {
		t.Helper()
		for _, dark := range []bool{false, true} {
			st := mg.NewTester(ta.view, 1280, 820)
			st.SetScale(2)
			st.SetDark(dark)
			st.Frame()
			st.Frame()
			file := name + "-light.png"
			if dark {
				file = name + "-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, file))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, st.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
	tt.Frame()
	shoot("iphone-plug")
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
	id := ta.add.iphone(ta.app).Info.ID
	for _, st := range []struct {
		name string
		info driver.Info
	}{
		{"iphone-trust", driver.Info{State: driver.StateUnauthorized, Setup: driver.SetupTrust}},
		{"iphone-devmode", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode}},
		{"iphone-preparing", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupPreparing}},
		{"iphone-noimage", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage, Detail: "Pinky needs Apple's developer disk image, which comes with Xcode."}},
		{"iphone-failed", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDiskImage, Detail: "Pinky could not reach Apple's signing server (gs.apple.com), which signs the developer disk image for each iPhone. Check the internet connection."}},
		{"iphone-refused", refusedXcodeImage},
		{"iphone-ready", driver.Info{State: driver.StateOnline}},
	} {
		ta.api.SetIPhone(id, st.info)
		ta.waitFor(t, st.name, func() bool { tt.Frame(); d := ta.add.iphone(ta.app); return d != nil && d.Info.Setup == st.info.Setup })
		shoot(st.name)
	}
	inLanguage(t, "zh-Hans", tt)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode})
	ta.waitFor(t, "Developer Mode in Chinese", func() bool { tt.Frame(); return hasText(tt, "开发者模式") })
	shoot("iphone-devmode-zh")
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage})
	ta.waitFor(t, "the missing image in Chinese", func() bool { tt.Frame(); return hasText(tt, "开发者磁盘映像") })
	shoot("iphone-noimage-zh")
	ta.api.SetIPhone(id, refusedXcodeImage)
	ta.waitFor(t, "the refused image in Chinese", func() bool { tt.Frame(); return hasText(tt, "Apple 不再为这个开发者磁盘映像签名") })
	shoot("iphone-refused-zh")
}

// refusedXcodeImage is an iPhone whose mount Apple's signing server refused,
// with Xcode 26.0.1's image.
var refusedXcodeImage = driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDiskImageRefused,
	Detail:    "Apple's signing server would not sign the developer disk image Pinky found for this iPhone.",
	DiskImage: &driver.DiskImage{Source: driver.DiskImageFromXcode, Build: "17B48", Version: "17.2.48.0", Dir: "/Library/Developer/DeveloperDiskImages/iOS_DDI/Restore"}}

// TestAddIPhoneAlreadyReady says that an iPhone connected and ready before
// Add phone opened is ready, rather than waiting for one to be plugged in,
// and follows another iPhone plugged in then.
func TestAddIPhoneAlreadyReady(t *testing.T) {
	ta, tt := addIPhoneApp(t)
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
	id := ta.add.iphone(ta.app).Info.ID
	ta.api.SetIPhone(id, driver.Info{State: driver.StateOnline})
	ta.waitFor(t, "the phone online", func() bool {
		tt.Frame()
		for _, d := range ta.devices {
			if d.Info.ID == id {
				return d.Info.State == driver.StateOnline
			}
		}
		return false
	})
	// Add phone › iPhone opens again, with that iPhone known.
	ta.add.reset(ta.app)
	ta.add.step = "iphone"
	ta.waitFor(t, "the ready iPhone", func() bool { tt.Frame(); return hasText(tt, "iPhone 16 is ready") })
	if !hasText(tt, "To add another iPhone, plug it in.") {
		t.Errorf("no word of adding another iPhone: %q", tt.Texts())
	}
	if hasText(tt, "Looking for your iPhone") {
		t.Errorf("still looking for an iPhone: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.AddPhoneOlderIPhone()); !ok {
		t.Errorf("no way to an older iPhone: %q", tt.Texts())
	}

	ta.api.ConnectIPhone()
	ta.waitFor(t, "the new iPhone", func() bool {
		tt.Frame()
		d := ta.add.iphone(ta.app)
		return d != nil && d.Info.ID != id
	})
	tt.Frame()
	if hasText(tt, "To add another iPhone") {
		t.Errorf("the steps still show the ready iPhone: %q", tt.Texts())
	}
}

// TestAddIPhoneShortWindow reaches the mirror offer's button in a window too
// short to show it, as people do: the card scrolls under a mouse wheel, and
// Tab brings the button it reaches into view.
func TestAddIPhoneShortWindow(t *testing.T) {
	defer func(os string) { guideOS = os }(guideOS)
	guideOS = "linux"
	const label = "Download (32 MB)"
	shows := func(tt *mg.Tester) bool {
		r, _ := tt.Find(label)
		return r.H > 0 && r.Y >= 0 && r.Y+r.H <= 640
	}
	open := func() (*testApp, *mg.Tester) {
		ta, _ := addIPhoneApp(t)
		tt := mg.NewTester(ta.view, 1280, 640)
		ta.api.ConnectIPhone()
		ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
		ta.api.SetIPhone(ta.add.iphone(ta.app).Info.ID, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage})
		ta.waitFor(t, "the offer", func() bool { tt.Frame(); return hasText(tt, "Or download it from a third-party mirror") })
		if shows(tt) {
			r, _ := tt.Find(label)
			t.Fatalf("the button shows at %v without scrolling: the window is not short enough for this test", r)
		}
		return ta, tt
	}

	// A mouse wheel over the card, to its end.
	ta, tt := open()
	for range 20 {
		tt.Wheel(640, 300, 0, 120)
		tt.Advance(400 * time.Millisecond)
		tt.Frame()
	}
	if !shows(tt) {
		r, _ := tt.Find(label)
		t.Fatalf("the wheel did not bring the button into view: %v", r)
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the download, after the wheel", func() bool { tt.Frame(); return ta.add.dl.seq != 0 })

	// The keyboard alone.
	ta, tt = open()
	for i := 0; i < 60 && !tt.Focused(label); i++ {
		tt.Key(0, mg.KeyTab)
		tt.Frame()
	}
	if !tt.Focused(label) || !shows(tt) {
		r, _ := tt.Find(label)
		t.Fatalf("Tab did not bring the button into view: focused %v, at %v", tt.Focused(label), r)
	}
	tt.Key(0, mg.KeyEnter)
	ta.waitFor(t, "the download, from the keyboard", func() bool { tt.Frame(); return ta.add.dl.seq != 0 })
}

// TestAddIPhoneDownload offers the developer disk image from the mirror
// when the computer has none, says where it comes from, downloads it on a
// click and moves on; a failed or changed download says so and points back
// to the official ways.
func TestAddIPhoneDownload(t *testing.T) {
	// The steps above the offer differ by system: an App Store link on a
	// Mac, a longer text on how to copy the image elsewhere. Fonts differ
	// too, and on Linux and Windows the longer text pushes the offer's
	// button below the window, where the card scrolls; the shorter window
	// puts it there on every system.
	for _, c := range []struct {
		goos   string
		height int
	}{{"darwin", 820}, {"linux", 640}} {
		t.Run(c.goos, func(t *testing.T) {
			defer func(os string) { guideOS = os }(guideOS)
			guideOS = c.goos
			testAddIPhoneDownload(t, c.height)
		})
	}
}

func testAddIPhoneDownload(t *testing.T, height int) {
	ta, _ := addIPhoneApp(t)
	tt := mg.NewTester(ta.view, 1280, height)
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
	id := ta.add.iphone(ta.app).Info.ID
	noImage := driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage, Detail: "Pinky needs Apple's developer disk image."}

	// A download that fails, then one whose files are not the expected ones.
	for _, c := range []struct {
		err  error
		says string
	}{
		{errors.New("downloading Image.dmg: connection reset"), "The download did not work."},
		{errors.Join(errors.New("Image.dmg"), driver.ErrDiskImageMismatch), "The mirror's files are not the ones Pinky expects"},
	} {
		ta.api.SetIPhone(id, noImage)
		ta.api.SetDownloadError(c.err)
		ta.waitFor(t, "the offer", func() bool { tt.Frame(); return hasText(tt, "Or download it from a third-party mirror") })
		for _, want := range []string{"github.com/doronz88/DeveloperDiskImage", "not Apple", "Xcode 27.0 beta 4 (27A5228h)"} {
			if !hasText(tt, want) {
				t.Fatalf("the offer does not say %q: %q", want, tt.Texts())
			}
		}
		label := "Download (32 MB)"
		if ta.add.dl.err != nil {
			label = "Try again"
		}
		if err := clickInView(tt, label); err != nil {
			t.Fatalf("%s: %v (%q)", label, err, tt.Texts())
		}
		ta.waitFor(t, "the failure", func() bool { tt.Frame(); return hasText(tt, c.says) })
		if !hasText(tt, "use an official copy instead") {
			t.Errorf("a failed download does not point to the official ways: %q", tt.Texts())
		}
	}

	// One that works: the phone is prepared next.
	ta.api.SetDownloadError(nil)
	if err := clickInView(tt, "Try again"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the progress", func() bool { tt.Frame(); return hasText(tt, "Downloading…") })
	ta.waitFor(t, "the phone being prepared", func() bool { tt.Frame(); return hasText(tt, "Preparing the iPhone…") })
}

// clickInView clicks the element labelled s, first scrolling the Add phone
// card while the element lies below the window or below the card's edge,
// which clips it to nothing, as a person scrolls the card.
func clickInView(tt *mg.Tester, s string) error {
	for range 8 {
		r, ok := tt.Find(s)
		if !ok {
			break
		}
		if r.W > 0 && r.H > 0 {
			if err := tt.Click(s); err == nil || !strings.Contains(err.Error(), "outside the window") {
				return err
			}
		}
		// The card's middle: inside it in any window the tests use.
		tt.Scroll(r.X+r.W/2, 300, 0, 200)
		tt.Frame()
	}
	return tt.Click(s)
}
