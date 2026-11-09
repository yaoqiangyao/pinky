package ui

import (
	"fmt"
	"image"
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
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// drain runs what the views posted, without reading the fake's views
// again: the tests here set the needs themselves.
func (ta *testApp) drain() {
	ta.mu.Lock()
	q := ta.q
	ta.q = nil
	ta.mu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// shotCase is an approval with a screenshot of the fake checkout, or
// without one.
type shotCase struct {
	name      string
	w, h      int     // the phone's screen in points
	scale     float32 // pixels a point
	marks     func(target screen.Rect, iw, ih int) shotMarks
	noShot    bool
	app, rule string
	title     string
}

func tapMarks(t screen.Rect, _, _ int) shotMarks { return shotMarks{target: t, point: t.Center()} }

var shotCases = []shotCase{
	{name: "phone", w: 393, h: 852, scale: 3, marks: tapMarks},
	{name: "landscape", w: 852, h: 393, scale: 3, marks: tapMarks},
	{name: "tablet", w: 820, h: 1180, scale: 2, marks: tapMarks},
	{name: "tablet-landscape", w: 1180, h: 820, scale: 2, marks: tapMarks},
	{name: "swipe", w: 393, h: 852, scale: 3, title: "Swipe up?", marks: func(_ screen.Rect, iw, ih int) shotMarks {
		return shotMarks{target: screen.Rect{W: iw, H: ih}, point: screen.Point{X: iw / 2, Y: ih * 7 / 10}, to: screen.Point{X: iw / 2, Y: ih * 3 / 10}}
	}},
	{name: "point", w: 393, h: 852, scale: 3, title: "Tap at 590,1500?", marks: func(_ screen.Rect, iw, ih int) shotMarks {
		return shotMarks{target: screen.Rect{X: 566, Y: 1476, W: 48, H: 48}, point: screen.Point{X: 590, Y: 1500}}
	}},
	{name: "no-target", w: 393, h: 852, scale: 3, rule: "Agent request", title: "Place the lunch order?", marks: func(screen.Rect, int, int) shotMarks {
		return shotMarks{}
	}},
	{name: "locked", noShot: true, title: "Tap “Unlock”?"},
	{name: "blocked", noShot: true, app: "Northbank", title: "Open Messages?"},
}

// need makes the case's approval, in Chinese words where zh is set, with
// a title and detail of long ones.
func (sc shotCase) need(t *testing.T, id string, zh, long bool) gateway.NeedView {
	t.Helper()
	now := time.Now()
	n := gateway.NeedView{
		ID: id, Kind: gateway.NeedApproval, DeviceID: "pixel-9", DeviceName: "Pixel 9", App: "Dinely", Agent: "Claude Code",
		TaskID: "task-" + id, Title: "Pay: “Place order · $12.80”?", Detail: "Claude Code · Pay $12.80 for lunch", Rule: "Payments",
		Created: now, Deadline: now.Add(60 * time.Second), AlwaysAllow: true, AllowTask: true,
	}
	if zh {
		n.Title, n.Detail = "Pay: “提交订单 · ¥12.80”?", "Claude Code · 给午饭付 ¥12.80"
	}
	if sc.title != "" {
		n.Title = sc.title
	}
	if sc.app != "" {
		n.App = sc.app
	}
	if sc.rule != "" {
		n.Rule, n.AlwaysAllow, n.AllowTask = sc.rule, false, false
	}
	if long {
		n.Title = "Tap “Place order · $12.80 — chicken rice bowl, no cilantro, delivered to 88 Market St, Apt 5”?"
		n.Detail = "Claude Code · Reorder last week's lunch from Golden Wok and pay for it with the saved Visa card, then wait for the courier"
	}
	if sc.noShot {
		n.Target = screen.Rect{X: 54, Y: 2292, W: 1071, H: 162}
		n.Point = n.Target.Center()
		return n
	}
	img, target := fakeCheckout(t, sc.w, sc.h, sc.scale, zh)
	b := img.Bounds()
	m := sc.marks(target, b.Dx(), b.Dy())
	n.Screenshot, n.Target, n.Point, n.To = img, m.target, m.point, m.to
	return n
}

// shotApp is a client with no phones of the fake's, and the needs given.
func shotApp(t *testing.T, needs ...gateway.NeedView) *testApp {
	t.Helper()
	ta := newTestApp(t, fakeapi.NoPhones)
	ta.loaded = true
	ta.devices = []gateway.DeviceView{{Info: deviceInfo("pixel-9", "Pixel 9"), Status: gateway.DeviceNeedsYou}}
	ta.setNeeds(needs)
	return ta
}

// within reports whether r lies inside a w × h window.
func within(r mg.Rect, w, h float32) bool {
	return r.W > 0 && r.H > 0 && r.X >= 0 && r.Y >= 0 && r.X+r.W <= w+0.5 && r.Y+r.H <= h+0.5
}

// TestShotLayout checks how the figure splits its box and what the zoom
// shows, for a phone and a tablet, upright and on their side.
func TestShotLayout(t *testing.T) {
	box := mg.Rect{X: 16, Y: 100, W: 368, H: 196}
	for _, c := range []struct {
		name    string
		iw, ih  int
		target  screen.Rect
		sideBy  bool
		maxText float32 // DIPs a point, about, at most
	}{
		{"phone", 1179, 2556, screen.Rect{X: 54, Y: 2292, W: 1071, H: 162}, true, 1},
		{"phone button", 1179, 2556, screen.Rect{X: 900, Y: 140, W: 160, H: 90}, true, 1},
		{"landscape", 2556, 1179, screen.Rect{X: 1314, Y: 838, W: 1158, H: 162}, false, 1},
		{"tablet", 1640, 2360, screen.Rect{X: 36, Y: 2184, W: 1568, H: 108}, true, 1},
		{"tablet landscape", 2360, 1640, screen.Rect{X: 1200, Y: 1400, W: 1100, H: 108}, false, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := box
			if !c.sideBy {
				r.H = figureHeight(r.W, panelFigure, panelWide, panelLens, c.iw, c.ih, true)
			}
			whole, lens := shotLayout(r, c.iw, c.ih, true)
			a := float32(c.iw) / float32(c.ih)
			if d := whole.W/whole.H - a; d > 0.01 || d < -0.01 {
				t.Errorf("the whole screen is %v, not of the screen's shape %v", whole, a)
			}
			if c.sideBy != (lens.X > whole.X+whole.W) {
				t.Errorf("whole %v, lens %v: side by side should be %v", whole, lens, c.sideBy)
			}
			for _, x := range []mg.Rect{whole, lens} {
				if x.X < r.X-0.01 || x.Y < r.Y-0.01 || x.X+x.W > r.X+r.W+0.01 || x.Y+x.H > r.Y+r.H+0.01 {
					t.Errorf("%v is out of the figure %v", x, r)
				}
			}
			if lens.W < 140 || lens.H < 80 {
				t.Errorf("the zoom is too small: %v", lens)
			}
			m := shotMarks{target: c.target, point: c.target.Center()}
			src, s := zoomOn(c.iw, c.ih, m, lens.W, lens.H)
			if src.X < 0 || src.Y < 0 || src.X+src.W > float32(c.iw)+0.5 || src.Y+src.H > float32(c.ih)+0.5 {
				t.Errorf("the zoom shows %v, out of the %d×%d screenshot", src, c.iw, c.ih)
			}
			if pt := s * pxPerPt(c.iw, c.ih); pt > c.maxText+0.001 || pt < 0.5 {
				t.Errorf("the zoom shows a point as %.2f DIPs", pt)
			}
			p := c.target.Center()
			if !(float32(p.X) > src.X && float32(p.X) < src.X+src.W && float32(p.Y) > src.Y && float32(p.Y) < src.Y+src.H) {
				t.Errorf("the zoom %v misses where the finger goes, %v", src, p)
			}
		})
	}
}

// TestApprovalPanelFits draws the floating panel for each kind of
// screenshot, in each of the client's languages, with short and long words:
// the countdown, the buttons and More options stay in the window, and the
// screenshot keeps a useful size.
func TestApprovalPanelFits(t *testing.T) {
	for _, lang := range testLanguages() {
		for _, sc := range shotCases {
			for _, long := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/long=%v", lang, sc.name, long)
				t.Run(name, func(t *testing.T) {
					inLanguage(t, lang)
					n := sc.need(t, "ap-1", lang == "zh-Hans", long)
					ta := shotApp(t, n)
					if sc.name == "locked" {
						ta.devices[0].Status = gateway.DeviceLocked
					}
					h := panelHeight(n)
					tt := mg.NewTester(ta.panelView, panelW, h)
					inLanguage(t, lang, tt)
					tt.Frame()
					W, H := float32(panelW), float32(h)
					wants := []string{locales.NeedsDeny(), locales.NeedsAllow(), locales.NeedsMoreOptions()}
					for _, s := range wants {
						r, ok := tt.Find(s)
						if !ok || !within(r, W, H) {
							t.Errorf("%q at %v (found %v) is not in the %v×%v panel: %q", s, r, ok, W, H, tt.Texts())
						}
					}
					countdown := ""
					around := strings.SplitN(locales.PanelDeniesIn("\x00"), "\x00", 2)
					for _, s := range tt.Texts() {
						if len(around) == 2 && strings.HasPrefix(s, around[0]) && strings.HasSuffix(s, around[1]) {
							countdown = s
						}
					}
					if r, ok := tt.Find(countdown); countdown == "" || !ok || !within(r, W, H) {
						t.Errorf("no countdown in the panel: %q", tt.Texts())
					}
					fig, ok := tt.Find(locales.PanelShotLabel("Pixel 9"))
					switch {
					case sc.noShot && ok:
						t.Errorf("a figure without a screenshot: %v", fig)
					case !sc.noShot && (!ok || !within(fig, W, H) || fig.H < 150):
						t.Errorf("the screenshot is at %v (found %v) in the %v×%v panel", fig, ok, W, H)
					}
					switch sc.name {
					case "locked":
						if !hasText(tt, locales.ShotMissingLocked("Pixel 9")) {
							t.Errorf("no word of the lock: %q", tt.Texts())
						}
					case "blocked":
						if !hasText(tt, locales.ShotMissingBlocked("Northbank")) {
							t.Errorf("no word of the blocked app: %q", tt.Texts())
						}
					}
				})
			}
		}
	}
}

// TestApprovalShotOpens clicks the floating panel's screenshot: a larger
// view opens, follows the approval the panel shows, closes with Esc and
// when its approval is answered.
func TestApprovalShotOpens(t *testing.T) {
	sc := shotCases[0]
	n1, n2 := sc.need(t, "ap-1", false, false), sc.need(t, "ap-2", false, false)
	n2.Created = n1.Created.Add(time.Second)
	ta := shotApp(t, n1, n2)
	tt := mg.NewTester(ta.panelView, panelW, panelHeight(n1))
	tt.Frame()
	if err := tt.Click(locales.PanelShotLabel("Pixel 9")); err != nil {
		t.Fatal(err)
	}
	ta.drain()
	if ta.viewing != n1.ID {
		t.Fatalf("viewing %q after the click, want %q", ta.viewing, n1.ID)
	}
	w, h := 520, 900
	vt := mg.NewTester(ta.viewerView, w, h)
	vt.Frame()
	big, ok := vt.Find(locales.PanelShotLabel("Pixel 9"))
	if !ok || big.H < 600 || !within(big, float32(w), float32(h)) {
		t.Errorf("the large screenshot is at %v (found %v) in %d×%d", big, ok, w, h)
	}
	if !hasText(vt, "Pay: “Place order · $12.80”?") || !hasText(vt, "Pixel 9 · Dinely") {
		t.Errorf("the large view lacks the approval's words: %q", vt.Texts())
	}

	// The panel turns to the next approval: the large view follows.
	if err := tt.Click(locales.PanelNext()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ta.drain()
	if ta.viewing != n2.ID {
		t.Errorf("viewing %q after Next, want %q", ta.viewing, n2.ID)
	}

	// Esc in the large view closes it.
	vt.Key(0, mg.KeyEscape)
	ta.drain()
	if ta.viewing != "" {
		t.Errorf("viewing %q after Esc", ta.viewing)
	}

	// An answered approval closes its large view.
	ta.openShot(n2)
	ta.setNeeds([]gateway.NeedView{n1})
	if ta.viewing != "" {
		t.Errorf("viewing %q after its approval went", ta.viewing)
	}
}

// TestApprovalShotOnCard shows the screenshot on a Needs you card when the
// window's middle does not show that phone, and larger over the window on
// a click.
func TestApprovalShotOnCard(t *testing.T) {
	n := shotCases[0].need(t, "ap-1", false, false)
	ta := shotApp(t, n)
	ta.devices = append(ta.devices, gateway.DeviceView{Info: deviceInfo("iphone", "iPhone 15"), Status: gateway.DeviceIdle})
	label := locales.PanelShotLabel("Pixel 9")

	// The phone shows in the middle, its mirror playing: no screenshot.
	ta.sel = "pixel-9"
	ta.mirrors["pixel-9"] = &mirror{a: ta.app, id: "pixel-9", state: "playing", used: ta.buildSeq, retry: time.Now().Add(time.Hour)}
	tt := mg.NewTester(ta.rightPanel, 320, 820)
	tt.Frame()
	if _, ok := tt.Find(label); ok {
		t.Error("the card shows the screenshot next to the phone's own screen")
	}

	// Another phone is selected: the card shows it.
	ta.sel = "iphone"
	tt.Frame()
	fig, ok := tt.Find(label)
	if !ok || fig.H < 140 || fig.W < 250 {
		t.Fatalf("the card's screenshot is at %v (found %v)", fig, ok)
	}
	if r, ok := tt.Find(locales.NeedsAllow()); !ok || r.Y < fig.Y+fig.H {
		t.Errorf("Allow at %v is not under the screenshot %v", r, fig)
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	if ta.shotOpen != n.ID {
		t.Fatalf("shotOpen %q after the click", ta.shotOpen)
	}
	wt := mg.NewTester(ta.view, 1280, 820)
	wt.Frame()
	wt.Frame()
	big, ok := wt.Find(locales.ShotLabel("Pixel 9"))
	if !ok || big.H < 500 {
		t.Errorf("the large view is at %v (found %v)", big, ok)
	}
	if err := wt.Click(locales.ShotClose()); err != nil {
		t.Fatal(err)
	}
	wt.Frame()
	if ta.shotOpen != "" {
		t.Errorf("shotOpen %q after Close", ta.shotOpen)
	}
}

func deviceInfo(id, name string) driver.Info {
	return driver.Info{ID: id, Name: name, Platform: driver.Android, State: driver.StateOnline}
}

// TestApprovalShotScreenshots draws the approval card with its screenshot
// into $PINKY_SCREENSHOTS, for the notes: the floating panel for each kind
// of screenshot in each of the client's languages, light and dark, its
// answer, the large view, and a Needs you card. The phone screens are drawn
// by the test, not taken from a phone.
func TestApprovalShotScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the approval card's screenshots")
	}
	write := func(name string, img image.Image) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	shoot := func(name, lang string, dark bool, view func(*mg.Context), w, h int) {
		t.Helper()
		tt := mg.NewTester(view, w, h)
		inLanguage(t, lang, tt)
		tt.SetScale(2)
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame()
		write(name, tt.Image())
	}
	for _, lang := range testLanguages() {
		zh := lang == "zh-Hans"
		suffix := "-" + langShort(lang)
		for _, sc := range shotCases {
			inLanguage(t, lang)
			n := sc.need(t, "ap-1", zh, false)
			ta := shotApp(t, n)
			if sc.name == "locked" {
				ta.devices[0].Status = gateway.DeviceLocked
			}
			shoot("panel-"+sc.name+suffix, lang, false, ta.panelView, panelW, panelHeight(n))
			if sc.name == "phone" {
				shoot("panel-"+sc.name+suffix+"-dark", lang, true, ta.panelView, panelW, panelHeight(n))
				ta.openShot(n)
				shoot("viewer-"+sc.name+suffix, lang, false, ta.viewerView, 480, 920)
				ta.closeShot()
				ta.remember(n, gateway.AllowOnce, "")
				shoot("panel-answered"+suffix, lang, false, ta.panelView, panelW, panelHeight(n))
				ta.answered = nil
				ta.devices = append(ta.devices, gateway.DeviceView{Info: deviceInfo("iphone", "iPhone 15"), Status: gateway.DeviceIdle})
				ta.sel = "iphone"
				shoot("card"+suffix, lang, false, ta.rightPanel, 320, 640)
				ta.shotOpen = n.ID
				shoot("window-large"+suffix, lang, false, ta.view, 1280, 820)
				ta.shotOpen = ""
			}
		}
	}
}
