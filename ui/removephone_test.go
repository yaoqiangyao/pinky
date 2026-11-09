package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/secret"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// mustHave fails the test when the frame lacks one of texts.
func mustHave(t *testing.T, tt *mg.Tester, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if !hasText(tt, s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
}

// TestRemovePhoneFromMenu: the ⋯ menu under the mirror removes the Pixel,
// which an agent is using over a cable: the dialog says the task stops,
// that the phone comes back while plugged in and that its history stays,
// and once removed the phone and its saved PIN are gone.
func TestRemovePhoneFromMenu(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.waitFor(t, "the lunch task", func() bool { return ta.taskOf("pixel-9") != nil })
	store := ta.opt.Secrets.(*secret.Memory)
	if err := store.Save(secret.PINAccount("pixel-9"), "1234"); err != nil {
		t.Fatal(err)
	}
	ta.pin.saved["pixel-9"] = true
	ta.sel = "pixel-9"
	tt := mg.NewTester(ta.view, 1280, 820)
	if err := tt.Click("More phone controls"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Remove this phone…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Remove Pixel 9?", "It leaves Pinky's list, and agents can no longer use it.",
		"Its saved PIN and its settings in Pinky are deleted.", "Claude Code's task on it stops: Reorder last week's lunch.",
		"Also delete its history: tasks, screenshots and recordings", "Its history stays in Activity.",
		"It's connected with a cable, so it shows up again right away", "The phone keeps trusting this computer")
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the removal", func() bool { return ta.device("pixel-9") == nil && !ta.rm.open })
	if got := ta.api.Removals(); !slices.Equal(got, []fakeapi.Removal{{Device: "pixel-9"}}) {
		t.Errorf("removals %+v", got)
	}
	if store.Has(secret.PINAccount("pixel-9")) || ta.pin.saved["pixel-9"] {
		t.Error("the Pixel's PIN stayed")
	}
	tt.Frame()
	if hasText(tt, "Remove Pixel 9?") {
		t.Error("the dialog stayed open")
	}
	if ta.sel == "pixel-9" {
		t.Error("the removed phone stays selected")
	}
}

// TestRemovePhoneHistory: checking the box deletes the phone's history
// too.
func TestRemovePhoneHistory(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.sync()
	ta.sel = "galaxy-s25"
	tt := mg.NewTester(ta.view, 1280, 820)
	if err := tt.Click("Remove this phone"); err != nil { // offline: a link on its page
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Remove Galaxy S25?", "To add it back later, plug it in, or pair it again over Wi-Fi in Add phone.")
	if hasText(tt, "task on it stops") || hasText(tt, "connected with a cable") {
		t.Errorf("notes that do not apply to an offline phone: %q", tt.Texts())
	}
	if err := tt.Click("Also delete its history: tasks, screenshots and recordings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if hasText(tt, "Its history stays in Activity.") {
		t.Error("the note says the history stays while the box is checked")
	}
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the removal", func() bool { return len(ta.api.Removals()) == 1 && !ta.rm.open })
	if got := ta.api.Removals(); got[0] != (fakeapi.Removal{Device: "galaxy-s25", History: true}) {
		t.Errorf("removals %+v", got)
	}
}

// TestRemovePhoneCancel closes the dialog and keeps the phone.
func TestRemovePhoneCancel(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	if err := tt.Click("Remove Galaxy S25"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Remove Galaxy S25?")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ta.rm.open || hasText(tt, "Remove Galaxy S25?") || len(ta.api.Removals()) != 0 {
		t.Errorf("Cancel: open %v, removals %+v", ta.rm.open, ta.api.Removals())
	}
}

// TestRemovePhoneNotes: over Wi-Fi, an Android phone is disconnected and
// stays away until paired again; an iPhone stays away until plugged in.
func TestRemovePhoneNotes(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.api.SetTransport("pixel-9", "wifi", true)
	ta.sync()
	for _, c := range []struct{ id, want string }{
		{"pixel-9", "Pinky disconnects from it and won't connect to it again by itself. To add it back, pair it again in Add phone, or plug it in."},
		{"iphone-17", "Pinky stops using it over Wi-Fi. To add it back, plug it in."},
	} {
		ta.askRemove(*ta.device(c.id))
		tt := mg.NewTester(ta.view, 1280, 820)
		mustHave(t, tt, c.want, "The phone keeps trusting this computer")
		if hasText(tt, "connected with a cable") {
			t.Errorf("%s: the cable note shows for a phone on Wi-Fi", c.id)
		}
		ta.rm.open = false
	}
	ta.api.SetTransport("iphone-17", "usb", false)
	ta.sync()
	ta.askRemove(*ta.device("iphone-17"))
	tt := mg.NewTester(ta.view, 1280, 820)
	mustHave(t, tt, "To add it back later, plug it in again.")
}

// TestRemovePhoneAllPhones: a tile's ⋯ button and its right-click menu in
// All phones, and a phone's row in the sidebar, open the dialog.
func TestRemovePhoneAllPhones(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.sync()
	ta.sel = allPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	r, ok := tt.Find("Show Galaxy S25")
	if !ok {
		t.Fatalf("no Galaxy tile: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("More actions for Galaxy S25"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Remove this phone…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Remove Galaxy S25?")
	ta.rm.open = false
	tt.Frame()

	if err := tt.RightClick("Show iPhone 17"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Contains(menu, "Remove this phone…") {
		t.Fatalf("the tile's menu: %q", menu)
	}
	if err := tt.ChooseMenuItem("Remove this phone…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Remove iPhone 17?")
	ta.rm.open = false
	tt.Frame()

	if err := tt.RightClick("Galaxy S25, Offline"); err != nil { // its row in the sidebar
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove this phone…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustHave(t, tt, "Remove Galaxy S25?")
}

// TestRemovePhoneChinese: the dialog in Simplified Chinese.
func TestRemovePhoneChinese(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.waitFor(t, "the lunch task", func() bool { return ta.taskOf("pixel-9") != nil })
	tt := mg.NewTester(ta.view, 1280, 820)
	inLanguage(t, "zh-Hans", tt)
	ta.askRemove(*ta.device("pixel-9"))
	tt.Frame()
	mustHave(t, tt, "移除 Pixel 9？", "它会从 Pinky 的手机列表里消失", "同时删除它的记录：任务、截图和录屏",
		"它用数据线连着", "手机仍然信任这台电脑", "取消", "移除")
}

// TestRemovePhoneScreenshots draws the Remove phone dialog into
// $PINKY_SCREENSHOTS, for the notes: a phone on a cable with a task, one
// offline, one over Wi-Fi, in each of the client's languages, light and
// dark, and the entry points (the ⋯ menu, an offline phone's page, a tile in
// All phones, Settings › Phones). The fake's phones show no picture.
func TestRemovePhoneScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the Remove phone dialog's screenshots")
	}
	shoot := func(name, lang string, dark bool, ta *testApp, prep func(tt *mg.Tester)) {
		t.Helper()
		tt := mg.NewTester(ta.view, 1280, 820)
		inLanguage(t, lang, tt)
		tt.SetScale(2)
		tt.SetDark(dark)
		tt.Frame()
		if prep != nil {
			prep(tt)
		}
		tt.Frame()
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	for _, lang := range testLanguages() {
		suffix := "-" + langShort(lang)
		inLanguage(t, lang)
		ta := newTestApp(t, fakeapi.ThreePhones)
		ta.waitFor(t, "the lunch task", func() bool { return ta.taskOf("pixel-9") != nil })
		ta.sel = "pixel-9"
		ta.askRemove(*ta.device("pixel-9"))
		shoot("remove-cable"+suffix, lang, false, ta, nil)
		shoot("remove-cable"+suffix+"-dark", lang, true, ta, nil)
		ta.rm.open = false
		ta.more["pixel-9"] = true
		shoot("remove-menu"+suffix, lang, false, ta, nil)
		ta.more["pixel-9"] = false
		ta.sel = "galaxy-s25"
		shoot("remove-offline-page"+suffix, lang, false, ta, nil)
		ta.askRemove(*ta.device("galaxy-s25"))
		ta.rm.history = true
		shoot("remove-offline"+suffix, lang, false, ta, nil)
		ta.rm = removeState{}
		ta.api.SetTransport("pixel-9", "wifi", true)
		ta.sync()
		ta.askRemove(*ta.device("pixel-9"))
		shoot("remove-wifi"+suffix, lang, false, ta, nil)
		ta.rm = removeState{}
		ta.sel = allPhones
		shoot("remove-tile"+suffix, lang, false, ta, func(tt *mg.Tester) {
			if r, ok := tt.Find(locales.PhoneShow("Galaxy S25")); ok {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Frame()
				tt.Advance(300 * time.Millisecond) // the ⋯ fades in
			}
		})
		ta.page, ta.set.sec = pageSettings, secPhones
		shoot("remove-settings"+suffix, lang, false, ta, nil)
	}
}

// TestRemovePhoneForgetsClientState: what the client keeps about a removed
// phone goes with it, such as the connection choice Settings › Phones read,
// so a phone that comes back at once reads it again.
func TestRemovePhoneForgetsClientState(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the iPhone's connections", func() bool {
		tt.Frame()
		_, ok := ta.conn.got["iphone-17"]
		return ok
	})
	ta.bg.on["iphone-17"], ta.pin.asked["iphone-17"], ta.more["tile:iphone-17"] = true, true, true
	ta.chk.runs, ta.chk.family = map[string]*checkupRun{"iphone-17": {have: true}}, map[string]string{"iphone-17": "ios"}
	ta.chk.showing = "iphone-17"
	ta.askRemove(*ta.device("iphone-17"))
	ta.removePhone()
	ta.waitFor(t, "the removal", func() bool { return !ta.rm.busy && ta.device("iphone-17") == nil })
	if _, ok := ta.conn.got["iphone-17"]; ok {
		t.Error("the iPhone's connection choice stayed")
	}
	if _, ok := ta.conn.at["iphone-17"]; ok {
		t.Error("the time it was read stayed")
	}
	if ta.bg.on["iphone-17"] || ta.pin.asked["iphone-17"] || ta.more["tile:iphone-17"] {
		t.Error("other state about the iPhone stayed")
	}
	if ta.checkupOf("iphone-17") != nil || ta.chk.family["iphone-17"] != "" || ta.chk.showing != "" {
		t.Error("the iPhone's checkup stayed, and would show again should it come back")
	}
}
