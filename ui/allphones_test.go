package ui

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// manyPhones are the phones of fakeapi.ManyPhones, in the order the
// fake lists them.
var manyPhones = []string{"Pixel 9", "iPhone 17", "Galaxy S25", "Pixel 8a", "iPhone 16e", "OnePlus 13", "iPhone 15 Pro", "Moto G"}

// tileRects returns the box of each phone's tile in All phones.
func tileRects(t *testing.T, tt *mg.Tester) []mg.Rect {
	t.Helper()
	var out []mg.Rect
	for _, name := range manyPhones {
		r, ok := tt.Find(locales.PhoneShow(name))
		if !ok {
			t.Fatalf("no tile for %s: %q", name, tt.Texts())
		}
		out = append(out, r)
	}
	return out
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.5 }

// TestAllPhonesGrid: the tiles of All phones sit in columns, the last
// row's from the first column on, and the columns are centered in the
// room between the sidebar and the right panel, at any window width. The
// window is tall enough to show them all: Find gives what shows.
func TestAllPhonesGrid(t *testing.T) {
	ta := newTestApp(t, fakeapi.ManyPhones)
	ta.sync()
	ta.selectPhone(allPhones)
	for _, w := range []int{960, 1280, 1500, 1920} {
		tt := mg.NewTester(ta.view, w, 4000)
		tt.Frame()
		rs := tileRects(t, tt)
		cols := 0
		for _, r := range rs {
			if near(r.Y, rs[0].Y) {
				cols++
			}
		}
		want := map[int]int{960: 1, 1280: 3, 1500: 4, 1920: 6}[w]
		if cols != want {
			t.Errorf("width %d: %d columns, want %d", w, cols, want)
		}
		for i, r := range rs {
			if above := rs[i%cols]; !near(r.X, above.X) || !near(r.W, above.W) {
				t.Errorf("width %d: %s's tile at x %v (%v wide), not under %s's at %v (%v wide)",
					w, manyPhones[i], r.X, r.W, manyPhones[i%cols], above.X, above.W)
			}
			if row := i / cols; !near(r.Y, rs[row*cols].Y) {
				t.Errorf("width %d: %s's tile at y %v, not in the row of %s at %v", w, manyPhones[i], r.Y, manyPhones[row*cols], rs[row*cols].Y)
			}
			if r.W < tileMinWidth-2 || r.W > tileMaxWidth {
				t.Errorf("width %d: %s's tile is %v wide", w, manyPhones[i], r.W)
			}
		}
		centered(t, w, rs[0], rs[cols-1])
	}

	// Phones that all fit in a row are centered in it, as wide as tiles
	// get.
	ta.api.Play(fakeapi.ThreePhones)
	ta.sync()
	ta.selectPhone(allPhones)
	tt := mg.NewTester(ta.view, 1700, 820)
	tt.Frame()
	var rs []mg.Rect
	for _, name := range manyPhones[:3] {
		r, _ := tt.Find(locales.PhoneShow(name))
		if !near(r.Y, 83) || !near(r.W, tileMaxWidth-2) {
			t.Errorf("three phones: %s's tile at %v", name, r)
		}
		rs = append(rs, r)
	}
	centered(t, 1700, rs[0], rs[2])
}

// centered checks that the tiles from first to last, a row, are centered
// between the sidebar and the right panel of a window w wide.
func centered(t *testing.T, w int, first, last mg.Rect) {
	t.Helper()
	left := first.X - sidebarWidth
	right := float32(w) - rightPanelWidth - (last.X + last.W)
	if math.Abs(float64(left-right)) > 1 {
		t.Errorf("width %d: %v to the left of the tiles, %v to the right", w, left, right)
	}
}

// TestAllPhonesScrolls: with more phones than the window shows, All
// phones scrolls under its header, by the wheel or a touchpad, and the
// tiles out of view can be reached with the pointer and with Tab.
func TestAllPhonesScrolls(t *testing.T) {
	ta := newTestApp(t, fakeapi.ManyPhones)
	ta.sync()
	ta.selectPhone(allPhones)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	line := locales.PhoneConnected(6) + " · " + locales.PhoneOfflineCount(2)
	head, ok := tt.Find(line)
	if !ok {
		t.Fatalf("no header: %q", tt.Texts())
	}
	last := locales.PhoneShow("Moto G")
	r, _ := tt.Find(last)
	if r.Y+r.H <= 820 {
		t.Fatalf("Moto G's tile shows at once (%v): the test needs more phones", r)
	}
	// Find gives what shows of an element: all of a tile, as the first's.
	first, _ := tt.Find(locales.PhoneShow("Pixel 9"))
	whole := func(r mg.Rect) bool { return near(r.W, first.W) && near(r.H, first.H) }
	tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 2000)
	tt.Frame()
	r, _ = tt.Find(last)
	if !whole(r) {
		t.Fatalf("Moto G's tile after scrolling to the end: %v, a tile is %v", r, first)
	}
	if h, _ := tt.Find(line); h != head {
		t.Errorf("the header moved with the tiles: %v, was %v", h, head)
	}
	if err := tt.Click(last); err != nil {
		t.Fatal(err)
	}
	if ta.sel != "moto-g" {
		t.Errorf("clicking Moto G's tile selected %q", ta.sel)
	}

	// Tab reaches the last tile, and brings it into view.
	ta.selectPhone(allPhones)
	tt = mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	for i := 0; i < 80 && !tt.Focused(last); i++ {
		tt.Key(0, mg.KeyTab)
	}
	if !tt.Focused(last) {
		t.Fatal("Tab never reached Moto G's tile")
	}
	tt.Frame()
	if r, _ = tt.Find(last); !whole(r) {
		t.Errorf("Moto G's tile has the focus out of view: %v", r)
	}
}

// TestSidebarScrolls: in a short window, the sidebar's phones scroll
// between the brand and Activity, Settings and Stop all, which stay in
// view.
func TestSidebarScrolls(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.ManyPhones, Speed: 50, HoldSetup: true})
	for i := range 4 {
		ta.api.ConnectPhoneAs(fmt.Sprintf("Phone %d", i+1), nil)
	}
	ta.sync()
	if len(ta.devices) != 12 {
		t.Fatalf("%d phones", len(ta.devices))
	}
	tt := mg.NewTester(ta.view, 960, 620)
	tt.Frame()
	for _, s := range []string{locales.SidebarAddPhone(), locales.SidebarActivity(), locales.SidebarSettings(), locales.SidebarStopAll()} {
		if r, ok := tt.Find(s); !ok || r.H == 0 || r.Y+r.H > 620 {
			t.Errorf("%s at %v, out of the window", s, r)
		}
	}
	d := ta.devices[len(ta.devices)-1]
	row := locales.SidebarPhoneRow(d.Info.Name, statusLine(d))
	first, _ := tt.Find(locales.SidebarPhoneRow(ta.devices[0].Info.Name, statusLine(ta.devices[0])))
	if r, _ := tt.Find(row); near(r.H, first.H) {
		t.Fatalf("%s's row shows at once (%v): the test needs more phones", d.Info.Name, r)
	}
	tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 2000)
	tt.Frame()
	if r, _ := tt.Find(row); !near(r.H, first.H) {
		t.Fatalf("%s's row after scrolling the sidebar: %v, a row is %v", d.Info.Name, r, first)
	}
	if err := tt.Click(row); err != nil {
		t.Fatal(err)
	}
	if ta.sel != d.Info.ID {
		t.Errorf("clicking %s's row selected %q", d.Info.Name, ta.sel)
	}
	if r, ok := tt.Find(locales.SidebarStopAll()); !ok || r.H == 0 || r.Y+r.H > 620 {
		t.Errorf("Stop all at %v after scrolling, out of the window", r)
	}
}

// TestAllPhonesLivePictures: the tiles' live pictures play on while All
// phones scrolls. A tile below the window has its stream open, and shows
// its picture once scrolled into view, without the stream opening again.
func TestAllPhonesLivePictures(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	ta := newTestApp(t, fakeapi.ManyPhones)
	ta.sync()
	if err := ta.api.StopAll(); err != nil { // no step outlined on the tiles
		t.Fatal(err)
	}
	ta.waitFor(t, "the tasks to stop", func() bool { return !ta.anyRunning() })
	online := 0
	for _, d := range ta.devices {
		if d.Info.State == driver.StateOnline {
			online++
		}
	}
	bars := video.TestStreamOptions{Codec: driver.H264, Width: 540, Height: 1200, FPS: 10, Source: "pal100bars"}
	sa := &streamAPI{API: ta.api}
	for range online {
		sa.next = append(sa.next, bars)
	}
	ta.app.api = sa
	ta.app.videoErr = nil
	t.Cleanup(ta.closeMirrors)
	ta.selectPhone(allPhones)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()

	// The first row plays; iPhone 15 Pro's tile, in the third, does not
	// show, but its stream is open.
	const far = "iphone-15-pro"
	ta.frameUntil(t, tt, "the first row's pictures", func() bool {
		for _, id := range []string{"pixel-9", "iphone-17"} {
			if m := ta.mirrors[id]; m == nil || m.bitmap == nil {
				return false
			}
		}
		m := ta.mirrors[far]
		return m != nil && m.state == "playing"
	})
	if r, _ := tt.Find(locales.PhoneShow("iPhone 15 Pro")); r.H > 0 {
		t.Fatalf("iPhone 15 Pro's tile shows at once: %v", r)
	}
	m := ta.mirrors[far]
	gen := m.gen
	sa.mu.Lock()
	opened := len(sa.open)
	sa.mu.Unlock()

	first, _ := tt.Find(locales.PhoneShow("Pixel 9"))
	tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 2000)
	ta.frameUntil(t, tt, "iPhone 15 Pro's picture", func() bool { return m.bitmap != nil })
	tile, _ := tt.Find(locales.PhoneShow("iPhone 15 Pro"))
	if !near(tile.H, first.H) {
		t.Fatalf("iPhone 15 Pro's tile after scrolling: %v", tile)
	}
	// The picture, as Contain fits it in the screen: the mini phone is
	// 168 by 352 at the top of the tile's padding, its screen 5 inside.
	screen := mg.Rect{X: tile.X + (tile.W-168)/2 + 5, Y: tile.Y + 14 + 5, W: 158, H: 342}
	pw := screen.H * float32(bars.Width) / float32(bars.Height)
	pic := image.Rect(int(screen.X+(screen.W-pw)/2), int(screen.Y), int(screen.X+(screen.W+pw)/2), int(screen.Y+screen.H))
	tt.Frame()
	if off := barsOff(tt.Image(), pic, barColors(t, bars.Source)); off > 16 {
		t.Errorf("iPhone 15 Pro's tile shows its bars %d levels off", off)
	}
	sa.mu.Lock()
	again := len(sa.open) - opened
	sa.mu.Unlock()
	if ta.mirrors[far] != m || m.gen != gen || again != 0 {
		t.Errorf("scrolling opened streams again: %d more, the mirror's generation %d, was %d", again, m.gen, gen)
	}
}

// TestSidebarFollowsSelection: choosing a phone whose row is out of the
// sidebar's view, as by its tile in All phones, scrolls the row into view.
func TestSidebarFollowsSelection(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.ManyPhones, Speed: 50, HoldSetup: true})
	for i := range 4 {
		ta.api.ConnectPhoneAs(fmt.Sprintf("Phone %d", i+1), nil)
	}
	ta.sync()
	ta.selectPhone(allPhones)
	tt := mg.NewTester(ta.view, 960, 620)
	tt.Frame()
	d := ta.devices[len(ta.devices)-1]
	row := locales.SidebarPhoneRow(d.Info.Name, statusLine(d))
	full, _ := tt.Find(locales.SidebarPhoneRow(ta.devices[0].Info.Name, statusLine(ta.devices[0])))
	if r, _ := tt.Find(row); near(r.H, full.H) {
		t.Fatalf("%s's row shows at once: %v", d.Info.Name, r)
	}
	tile := locales.PhoneShow(d.Info.Name)
	first, _ := tt.Find(locales.PhoneShow(ta.devices[0].Info.Name))
	tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 1e5)
	tt.Frame()
	if err := tt.Click(tile); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if ta.sel != d.Info.ID {
		t.Fatalf("clicking %s's tile selected %q", d.Info.Name, ta.sel)
	}
	if r, _ := tt.Find(row); !near(r.H, full.H) {
		t.Errorf("%s's row once it is chosen: %v, a row is %v", d.Info.Name, r, full)
	}
	// Scrolled away by hand, it stays where the person put it.
	tt.Scroll(full.X+full.W/2, 300, 0, -1e5)
	tt.Frame()
	tt.Frame()
	if r, _ := tt.Find(row); near(r.H, full.H) {
		t.Errorf("%s's row came back into view: %v", d.Info.Name, r)
	}
}

// TestAllPhonesScreenshots draws All phones with eight phones into
// $PINKY_SCREENSHOTS, for the notes: at the top and scrolled to the end, in
// each of the client's languages, light and dark, and the sidebar of twelve
// phones in the smallest window, scrolled to its end. The fake's phones show
// no picture.
func TestAllPhonesScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write All phones' screenshots")
	}
	shoot := func(name string, tt *mg.Tester) {
		t.Helper()
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
		ta := newTestApp(t, fakeapi.ManyPhones)
		ta.waitFor(t, "the lunch task", func() bool { return ta.taskOf("pixel-9") != nil })
		ta.selectPhone(allPhones)
		for _, dark := range []bool{false, true} {
			if dark && lang != "en" {
				continue
			}
			theme := ""
			if dark {
				theme = "-dark"
			}
			tt := mg.NewTester(ta.view, 1280, 820)
			inLanguage(t, lang, tt)
			tt.SetScale(2)
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			shoot("all-phones"+suffix+theme, tt)
			tt.Scroll(500, 400, 0, 2000)
			tt.Frame()
			tt.Frame()
			shoot("all-phones-end"+suffix+theme, tt)
		}
	}
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.ManyPhones, Speed: 50, HoldSetup: true})
	for i := range 4 {
		ta.api.ConnectPhoneAs(fmt.Sprintf("Phone %d", i+1), nil)
	}
	ta.sync()
	ta.selectPhone(allPhones)
	tt := mg.NewTester(ta.view, 960, 620)
	inLanguage(t, "en", tt)
	tt.SetScale(2)
	tt.Frame()
	tt.Scroll(100, 300, 0, 2000)
	tt.Move(100, 30) // off the rows
	tt.Frame()
	tt.Frame()
	shoot("sidebar-many-phones-en", tt)
}
