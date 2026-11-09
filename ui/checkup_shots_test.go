package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// TestCheckupScreenshots draws Add phone's Android steps for every brand,
// and checkups, in each of the client's languages, into
// $PINKY_SCREENSHOTS:
//
//	PINKY_SCREENSHOTS=dir go test ./ui -run TestCheckupScreenshots
func TestCheckupScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	dark := os.Getenv("PINKY_SCREENSHOTS_DARK") != ""
	for _, lang := range testLanguages() {
		short := langShort(lang)
		shoot := func(name string, view func(*mg.Context), w, h int) {
			t.Helper()
			tt := mg.NewTester(view, w, h)
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
		inLanguage(t, lang)

		// Add phone › Android, for each brand, before the phone shows up.
		ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.NoPhones, Speed: 50, HoldSetup: true})
		ta.sync()
		ta.page = pageAddPhone
		ta.add.reset(ta.app)
		ta.add.step = "android"
		for _, b := range brands() {
			ta.droid.brand = b.id
			shoot("add-android-"+b.id, func(c *mg.Context) { ta.addPhonePage(c) }, 820, 1500)
		}

		// A Redmi waits for Allow, then comes online and gets its checkup.
		ta.droid.brand = ""
		cu := fakeapi.XiaomiCheckup()
		id := ta.api.ConnectPhoneAs("Redmi K80", &cu)
		ta.waitFor(t, "the phone", func() bool { return ta.device(id) != nil })
		shoot("add-android-allow", func(c *mg.Context) { ta.addPhonePage(c) }, 820, 1100)
		ta.api.FinishSetup()
		tt := mg.NewTester(func(c *mg.Context) { ta.addPhonePage(c) }, 820, 1400)
		inLanguage(t, lang, tt)
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
		shoot("add-android-checkup", func(c *mg.Context) { ta.addPhonePage(c) }, 820, 1400)

		// The checkup page of the Redmi, and of a phone that is fine.
		ta = newTestApp(t, fakeapi.Checkup)
		ta.sync()
		ta.openCheckup(*ta.device("redmi-k80"))
		tt = mg.NewTester(ta.view, 1280, 1100)
		inLanguage(t, lang, tt)
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("redmi-k80"); return r != nil && r.have && !r.running })
		shoot("checkup-xiaomi", ta.view, 1280, 1100)
		ta = newTestApp(t, fakeapi.OnePhone)
		ta.sync()
		ta.openCheckup(*ta.device("pixel-9"))
		tt = mg.NewTester(ta.view, 1280, 900)
		inLanguage(t, lang, tt)
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("pixel-9"); return r != nil && r.have && !r.running })
		shoot("checkup-pixel", ta.view, 1280, 900)
		// An OPPO phone that refuses input, on Android 12 over Wi-Fi.
		ta = newTestApp(t, fakeapi.OnePhone)
		ta.sync()
		oppo := fakeapi.XiaomiCheckup()
		oppo.Family, oppo.System = "oppo", "ColorOS 12.1"
		for i, c := range oppo.Checks {
			if c.ID == driver.CheckBackground {
				oppo.Checks[i] = driver.Check{ID: driver.CheckBackground, Status: driver.CheckWarn, Fix: driver.FixUnlockedOnly}
			}
		}
		ta.api.SetCheckup("pixel-9", oppo)
		ta.openCheckup(*ta.device("pixel-9"))
		tt = mg.NewTester(ta.view, 1280, 1000)
		inLanguage(t, lang, tt)
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("pixel-9"); return r != nil && r.have && !r.running })
		shoot("checkup-oppo", ta.view, 1280, 1000)
		// A phone that went offline.
		ta = newTestApp(t, fakeapi.ThreePhones)
		ta.sync()
		ta.openCheckup(*ta.device("galaxy-s25"))
		tt = mg.NewTester(ta.view, 1280, 820)
		inLanguage(t, lang, tt)
		ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("galaxy-s25"); return r != nil && r.have && !r.running })
		shoot("checkup-offline", ta.view, 1280, 820)
	}
}
