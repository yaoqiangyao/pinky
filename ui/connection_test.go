package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

func TestConnectionChoice(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 1400)
	ta.waitFor(t, "the iPhone's connections", func() bool { tt.Frame(); _, ok := ta.conn.got["iphone-17"]; return ok })
	tt.Frame()
	for _, want := range []string{locales.ConnectionTitle(), locales.ConnectionAutoNow("CoreDevice"), locales.ConnectionWDA(), locales.ConnectionNotInstalled()} {
		if !hasText(tt, want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	// Only iPhones offer a choice.
	if _, ok := ta.conn.got["pixel-9"]; ok {
		t.Error("an Android phone has connections")
	}
	if err := tt.Click(locales.ConnectionWDA()); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the helper chosen", func() bool {
		got, err := ta.api.Connections(ta.ctx, "iphone-17")
		return err == nil && got.Chosen == "wda" && !ta.conn.busy["iphone-17"]
	})
	ta.waitFor(t, "the iPhone needing the helper", func() bool {
		for _, d := range ta.devices {
			if d.Info.ID == "iphone-17" {
				return d.Info.State == driver.StateNeedsSetup
			}
		}
		return false
	})
	if !hasText(tt, locales.ConnectionReconnecting("iPhone 17")) && !containsToast(ta.app, locales.ConnectionReconnecting("iPhone 17")) {
		t.Errorf("no word about reconnecting: %q", ta.toasts)
	}
	// Back to Automatic.
	ta.conn.at["iphone-17"] = ta.conn.at["iphone-17"].AddDate(-1, 0, 0)
	ta.waitFor(t, "the choice read again", func() bool { tt.Frame(); return ta.conn.got["iphone-17"].Chosen == "wda" })
	if err := tt.Click(locales.ConnectionAuto()); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "Automatic chosen", func() bool {
		got, _ := ta.api.Connections(ta.ctx, "iphone-17")
		return got.Chosen == driver.ConnectionAuto && !ta.conn.busy["iphone-17"]
	})
}

func containsToast(a *app, s string) bool {
	for _, x := range a.toasts {
		if x == s {
			return true
		}
	}
	return false
}

// TestConnectionScreenshot writes Settings › Phones with the iPhone's
// connection choice, in each of the client's languages, into
// $PINKY_SCREENSHOTS.
func TestConnectionScreenshot(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	for _, lang := range testLanguages() {
		ta := newTestApp(t, fakeapi.ThreePhones)
		ta.sync()
		ta.page, ta.set.sec = pageSettings, secPhones
		tt := mg.NewTester(ta.view, 1280, 1500)
		inLanguage(t, lang, tt)
		tt.SetScale(2)
		ta.waitFor(t, "the iPhone's connections", func() bool { tt.Frame(); _, ok := ta.conn.got["iphone-17"]; return ok })
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, "connection-"+lang+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
