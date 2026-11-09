package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
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

// stillMirror has the phone's mirror show img, as its stream would: tests
// have no video.
func stillMirror(ta *testApp, id string, img image.Image) *mirror {
	b := img.Bounds()
	m := &mirror{a: ta.app, id: id, state: "playing", bitmap: mg.NewBitmap(img),
		picW: b.Dx(), picH: b.Dy(), used: ta.buildSeq, retry: time.Now().Add(time.Hour)}
	ta.mirrors[id] = m
	return m
}

// phoneBoxes are where the Phone screen draws the phone and its banner.
type phoneBoxes struct {
	fig, screen mg.Rect // the phone with its bezel, and its screen
	text, hand  mg.Rect // the banner's words and Hand back
	keys        mg.Rect // the Home key under the phone
	banner      bool
}

// findPhone finds the Phone screen's parts for the phone named name.
func findPhone(t *testing.T, tt *mg.Tester, name string) phoneBoxes {
	t.Helper()
	var b phoneBoxes
	var ok bool
	if b.fig, ok = tt.Find(locales.PhoneScreenLabel(name)); !ok {
		t.Fatalf("no phone: %q", tt.Texts())
	}
	if b.screen, ok = tt.Find(locales.MirrorScreenLabel(name)); !ok {
		t.Fatalf("no mirror: %q", tt.Texts())
	}
	if b.keys, ok = tt.Find(locales.PhoneKeyHome()); !ok {
		t.Fatalf("no Home key: %q", tt.Texts())
	}
	b.hand, b.banner = tt.Find(locales.PhoneHandBack())
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, locales.PhoneBannerInControl()) || strings.HasPrefix(s, locales.PhoneBannerYourTurn()) {
			b.text, _ = tt.Find(s)
		}
	}
	if b.banner && b.text.H == 0 {
		t.Fatalf("Hand back without the banner's words: %q", tt.Texts())
	}
	return b
}

// clickScreen clicks the mirror at fx, fy of its width and height and
// returns where the touch went down and came up on the phone.
func clickScreen(t *testing.T, ta *testApp, tt *mg.Tester, scr mg.Rect, fx, fy float32) (down, up screen.Point) {
	t.Helper()
	n := len(ta.api.Inputs())
	tt.ClickAt(scr.X+fx*scr.W, scr.Y+fy*scr.H)
	var got []fakeapi.Input
	ta.waitFor(t, "the touch", func() bool { got = ta.api.Inputs()[n:]; return len(got) >= 2 })
	if got[0].Kind != "touch" || got[0].Action != driver.TouchDown || got[1].Action != driver.TouchUp {
		t.Fatalf("a click at %.2f, %.2f sent %+v", fx, fy, got)
	}
	return got[0].Point, got[1].Point
}

// nearPoint reports whether p is within a pixel of x, y.
func nearPoint(p screen.Point, x, y int) bool {
	return p.X >= x-1 && p.X <= x+1 && p.Y >= y-1 && p.Y <= y+1
}

// TestControlBannerAboveTheScreen: the "You're in control" and "Your turn"
// banners sit above the phone, which shrinks to leave them room, and every
// part of its screen takes a click: the top included, which the banner
// used to cover (its top 7% in the window's default size, more in the
// smallest), where apps put their back buttons and menus. In each of the
// client's languages, in the default, smallest, a tall and a wide window.
func TestControlBannerAboveTheScreen(t *testing.T) {
	for _, lang := range testLanguages() {
		for _, sc := range []fakeapi.Scenario{fakeapi.Takeover, fakeapi.Handoff} {
			for _, size := range [][2]int{{1280, 820}, {960, 620}, {1100, 1300}, {1900, 820}} {
				t.Run(fmt.Sprintf("%s/%s/%dx%d", lang, sc, size[0], size[1]), func(t *testing.T) {
					inLanguage(t, lang)
					ta := newTestAppTimeout(t, sc, 10*time.Minute)
					ta.waitFor(t, "the phone in the person's hands", func() bool {
						if sc == fakeapi.Handoff {
							_, ok := ta.handoffFor("pixel-9")
							return ok
						}
						return len(ta.devices) > 0 && ta.devices[0].Status == gateway.DeviceInControl
					})
					img, _ := fakeCheckout(t, 360, 800, 3, lang != "en")
					m := stillMirror(ta, "pixel-9", img)
					w, h := float32(size[0]), float32(size[1])
					tt := mg.NewTester(ta.view, size[0], size[1])
					inLanguage(t, lang, tt)
					tt.Frame()
					b := findPhone(t, tt, "Pixel 9")
					if !b.banner {
						t.Fatalf("no banner: %q", tt.Texts())
					}
					checkBanner(t, b, w, h)

					// Clicks along the top of the screen and down its
					// middle reach the phone where they land on its
					// picture.
					for _, fy := range []float32{0.01, 0.035, 0.065, 0.5, 0.97} {
						for _, fx := range []float32{0.04, 0.5, 0.96} {
							scr := b.screen
							down, up := clickScreen(t, ta, tt, scr, fx, fy)
							x, y := int(fx*float32(m.picW)), int(fy*float32(m.picH))
							if !nearPoint(down, x, y) || down != up {
								t.Errorf("a click at %.2f, %.2f of the screen touched %v to %v, want %d, %d", fx, fy, down, up, x, y)
							}
							// A handoff turns into the person's control at
							// the first touch, with a banner of its own.
							ta.sync()
							tt.Frame()
							b = findPhone(t, tt, "Pixel 9")
							checkBanner(t, b, w, h)
						}
					}
				})
			}
		}
	}
}

// checkBanner checks the banner is above the phone and the phone and its
// keys fit the window: the banner's words and Hand back above the phone's
// bezel, the keys under it, all within the middle's padding.
func checkBanner(t *testing.T, b phoneBoxes, w, h float32) {
	t.Helper()
	const eps = 0.5
	if !b.banner {
		t.Fatal("no banner")
	}
	top := min(b.text.Y, b.hand.Y) - 11 // the banner's padding and border
	bottom := max(b.text.Y+b.text.H, b.hand.Y+b.hand.H) + 11
	if bottom+14 > b.fig.Y+eps {
		t.Errorf("the banner (down to %.1f) reaches the phone (from %.1f)", bottom, b.fig.Y)
	}
	if b.screen.Y < b.fig.Y || b.screen.Y+b.screen.H > b.fig.Y+b.fig.H {
		t.Errorf("the screen %v is not in the phone %v", b.screen, b.fig)
	}
	if top < 58+16-eps || b.keys.Y+b.keys.H > h-16+eps {
		t.Errorf("the banner from %.1f and the keys down to %.1f overflow the window's %.0f", top, b.keys.Y+b.keys.H, h)
	}
	if b.fig.Y+b.fig.H+14 > b.keys.Y+eps {
		t.Errorf("the phone (down to %.1f) reaches the keys (from %.1f)", b.fig.Y+b.fig.H, b.keys.Y)
	}
	if b.hand.X+b.hand.W > w-320-16+eps {
		t.Errorf("Hand back %v reaches into the right panel of a window %.0f wide", b.hand, w)
	}
}

// TestControlBannerHeight: the banner is as tall as its words, which wrap
// within it at any width, up to three lines; longer words end with an
// ellipsis rather than push the phone down.
func TestControlBannerHeight(t *testing.T) {
	long := "open Settings, then Passwords, find the Dinely account and copy its one-time code into the sign-in page, then wait for the order to load"
	// A line of the banner's words, in this system's font.
	lt := mg.NewTester(func(c *mg.Context) { mg.RichText(c, mg.Span{Text: "Ag", Size: 14}) }, 200, 100)
	lt.Frame()
	line, ok := lt.Find("Ag")
	if !ok || line.H == 0 {
		t.Fatal("no line to measure")
	}
	for _, lang := range testLanguages() {
		for _, reason := range []string{"Sign in to Dinely, please", long} {
			inLanguage(t, lang)
			ta := newTestAppTimeout(t, fakeapi.Handoff, 10*time.Minute)
			ta.waitFor(t, "the handoff", func() bool { _, ok := ta.handoffFor("pixel-9"); return ok })
			for i := range ta.needs {
				if ta.needs[i].Kind == gateway.NeedHandoff {
					ta.needs[i].Title = reason
				}
			}
			img, _ := fakeCheckout(t, 360, 800, 3, lang != "en")
			stillMirror(ta, "pixel-9", img)
			for w := 960; w <= 1500; w += 9 {
				tt := mg.NewTester(ta.view, w, 820)
				inLanguage(t, lang, tt)
				tt.Frame()
				b := findPhone(t, tt, "Pixel 9")
				checkBanner(t, b, float32(w), 820)
				// The banner's height, from Hand back in its middle and
				// the phone 14 below it.
				mid := b.hand.Y + b.hand.H/2
				bh := 2 * (b.fig.Y - 14 - mid)
				top := mid - bh/2
				if b.text.Y < top+11-0.5 || b.text.Y+b.text.H > top+bh-11+0.5 {
					t.Errorf("%s, %d wide: the words %v spill out of the banner %.1f to %.1f", lang, w, b.text, top, top+bh)
				}
				if lines := b.text.H / line.H; lines > controlLines+0.5 {
					t.Errorf("%s, %d wide: the words take %.1f lines", lang, w, lines)
				}
			}
		}
	}
}

// TestControlBannerWaitsForTheFinger: a touch on the mirror takes the
// phone over, and the banner that says so waits for the finger to lift:
// the mirror stays where it is under the finger, and the touch ends where
// it started.
func TestControlBannerWaitsForTheFinger(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.waitFor(t, "the payment approval", func() bool { _, ok := ta.approval(); return ok })
	img, _ := fakeCheckout(t, 360, 800, 3, false)
	m := stillMirror(ta, "pixel-9", img)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	before := findPhone(t, tt, "Pixel 9")
	if before.banner {
		t.Fatalf("a banner while the agent works: %q", tt.Texts())
	}
	x, y := before.screen.X+before.screen.W/2, before.screen.Y+before.screen.H*0.3
	tt.Press(x, y)
	ta.waitFor(t, "the takeover", func() bool { return ta.devices[0].Status == gateway.DeviceInControl })
	tt.Frame()
	held := findPhone(t, tt, "Pixel 9")
	if held.banner || held.screen != before.screen {
		t.Errorf("the finger is down, yet the banner shows (%v) or the mirror moved from %v to %v", held.banner, before.screen, held.screen)
	}
	tt.Release(x, y)
	var got []fakeapi.Input
	ta.waitFor(t, "the touch", func() bool { got = ta.api.Inputs(); return len(got) >= 2 })
	if got[0].Action != driver.TouchDown || got[1].Action != driver.TouchUp || got[0].Point != got[1].Point {
		t.Errorf("the touch moved: %+v", got)
	}
	tt.Frame()
	after := findPhone(t, tt, "Pixel 9")
	if !after.banner || after.screen.H >= before.screen.H {
		t.Errorf("after the finger lifted: banner %v, the screen %v (was %v)", after.banner, after.screen, before.screen)
	}
	checkBanner(t, after, 1280, 820)
	// The mirror the click focused keeps the keyboard as the banner
	// comes: what the person types goes to the phone.
	if !tt.Focused(locales.MirrorScreenLabel("Pixel 9")) {
		t.Error("the mirror lost the keyboard to the banner")
	}
	tt.Type("hi")
	ta.waitFor(t, "the text", func() bool { got = ta.api.Inputs(); return len(got) >= 3 })
	if got[2].Kind != "text" || got[2].Text != "hi" {
		t.Errorf("typing sent %+v", got[2:])
	}

	// A wheel or touchpad gesture holds the banner too, until it ends; and
	// Hand back takes it away once the gesture is over.
	if err := tt.Click(locales.PhoneHandBack()); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the hand back", func() bool { return ta.devices[0].Status != gateway.DeviceInControl })
	m.scrolling = true
	tt.Frame()
	if b := findPhone(t, tt, "Pixel 9"); !b.banner {
		t.Error("the banner left during a scroll")
	}
	m.scrolling = false
	tt.Frame()
	if b := findPhone(t, tt, "Pixel 9"); b.banner || b.screen != before.screen {
		t.Errorf("after the scroll: banner %v, the screen %v (was %v)", b.banner, b.screen, before.screen)
	}
}

// TestLockGivesWayUnderThePointer: a locked phone's mirror is dimmed with
// a pill naming the lock, which give way while the pointer is on the
// mirror, for the person to unlock it by hand there, and come back when
// it leaves. A click goes to the phone either way.
func TestLockGivesWayUnderThePointer(t *testing.T) {
	ta := newTestApp(t, fakeapi.Locked)
	ta.waitFor(t, "the lock", func() bool { return len(ta.devices) > 0 && ta.isLocked(ta.devices[0]) })
	white := image.NewRGBA(image.Rect(0, 0, 1080, 2400))
	draw.Draw(white, white.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	stillMirror(ta, "pixel-9", white)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	b := findPhone(t, tt, "Pixel 9")
	pill, ok := tt.Find(locales.MirrorLocked("Pixel 9"))
	if !ok {
		t.Fatalf("no lock pill: %q", tt.Texts())
	}
	// Two points on the screen: one away from the pill, one on it, in its
	// padding left of the lock icon.
	away := image.Pt(int(b.screen.X+b.screen.W/2), int(b.screen.Y+b.screen.H*0.2))
	under := image.Pt(int(pill.X-8-16-7), int(pill.Y+pill.H/2))
	light := func(p image.Point) uint8 {
		c := tt.Image().RGBAAt(p.X, p.Y)
		return min(c.R, c.G, c.B)
	}
	if light(away) > 160 || light(under) > 160 {
		t.Errorf("the locked screen is not dimmed: %d away from the pill, %d on it", light(away), light(under))
	}
	tt.Move(b.screen.X+b.screen.W/2, b.screen.Y+b.screen.H*0.6)
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if light(away) < 240 || light(under) < 240 {
		t.Errorf("with the pointer on the mirror, the lock screen does not show as it is: %d away from the pill, %d under it", light(away), light(under))
	}
	if _, up := clickScreen(t, ta, tt, b.screen, 0.5, 0.6); !nearPoint(up, 540, 1440) {
		t.Errorf("a click on the locked screen touched %v, want 540, 1440", up)
	}
	tt.Move(110, 400) // the sidebar
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if light(away) > 160 {
		t.Errorf("the pointer left, and the lock did not come back: %d", light(away))
	}
}

// fakePinPad draws a phone's lock screen asking for its PIN, w × h points
// at scale pixels a point, in Chinese when zh is set: what the person sees
// on the mirror after swiping up on a locked phone. Nothing here comes
// from a real phone.
func fakePinPad(t *testing.T, w, h int, scale float32, zh bool) image.Image {
	t.Helper()
	title, emergency := "Enter PIN", "Emergency"
	if zh {
		title, emergency = "输入 PIN 码", "紧急呼叫"
	}
	white, dim := hex("#ffffff"), mg.RGBA(255, 255, 255, 0.16)
	key := func(c *mg.Context, s string) {
		k := mg.Box(c).Size(72, 72).Radius(36).Center()
		if s != "" {
			k.Background(dim)
		}
		k.Children(func() { mg.Text(c, s).FontSize(30).TextColor(white) })
	}
	view := func(c *mg.Context) {
		mg.Column(c).Fill().Background(hex("#1d2740")).AlignItems(mg.Center).Children(func() {
			mg.Row(c).Height(48).Padding(0, 28).AlignItems(mg.Center).FillWidth().Children(func() {
				mg.Text(c, "9:41").FontSize(16).FontWeight(650).TextColor(white)
			})
			mg.Icon(c, icLock).FontSize(24).TextColor(white).Margin(56, 0, 0, 0)
			mg.Text(c, title).FontSize(20).TextColor(white).Margin(16, 0, 0, 0)
			mg.Row(c).Gap(16).Margin(22, 0, 0, 0).Children(func() {
				for i := 0; i < 4; i++ {
					d := mg.Box(c).Size(14, 14).Radius(7).Border(1.5, white)
					if i < 2 {
						d.Background(white)
					}
				}
			})
			mg.Spacer(c)
			mg.Column(c).Gap(18).Children(func() {
				for _, row := range [][]string{{"1", "2", "3"}, {"4", "5", "6"}, {"7", "8", "9"}, {"", "0", ""}} {
					mg.Row(c).Gap(30).Children(func() {
						for _, s := range row {
							key(c, s)
						}
					})
				}
			})
			mg.Text(c, emergency).FontSize(16).TextColor(white).Margin(30, 0, 44, 0)
		})
	}
	tt := mg.NewTester(view, w, h)
	tt.SetScale(scale)
	tt.Frame()
	return tt.Image()
}

// TestControlBannerScreenshots draws the banner above the mirror and the
// lock giving way under the pointer into $PINKY_SCREENSHOTS, in light and
// dark, in each of the client's languages: mirror-bar-<lang>-<theme>.png has
// the Phone screen's middle in four states side by side (in control; a
// handoff's "Your turn"; locked; locked with the pointer on the mirror), and
// takeover-<theme>.png the whole window in control, in English. The mirrors
// show pages drawn in the test.
func TestControlBannerScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	const scale = 2
	write := func(name string, img image.Image) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	theme := func(dark bool) string {
		if dark {
			return "dark"
		}
		return "light"
	}
	for _, lang := range testLanguages() {
		inLanguage(t, lang)
		zh := lang == "zh-Hans"
		checkout, _ := fakeCheckout(t, 360, 800, 3, zh)
		pinPad := fakePinPad(t, 360, 800, 3, zh)
		// render draws the window with the phone showing img; hover puts
		// the pointer on the mirror first.
		render := func(ta *testApp, img image.Image, dark, hover bool) *image.RGBA {
			t.Helper()
			stillMirror(ta, "pixel-9", img)
			tt := mg.NewTester(ta.view, 1280, 820)
			inLanguage(t, lang, tt)
			tt.SetScale(scale)
			tt.SetDark(dark)
			tt.Frame()
			if hover {
				b := findPhone(t, tt, "Pixel 9")
				tt.Move(b.screen.X+b.screen.W/2, b.screen.Y+b.screen.H*0.6)
				tt.Advance(300 * time.Millisecond)
				tt.Frame()
			}
			return tt.Image()
		}
		takeover := newTestAppTimeout(t, fakeapi.Takeover, 10*time.Minute)
		takeover.waitFor(t, "the takeover", func() bool {
			return len(takeover.devices) > 0 && takeover.devices[0].Status == gateway.DeviceInControl
		})
		handoff := newTestAppTimeout(t, fakeapi.Handoff, 10*time.Minute)
		handoff.waitFor(t, "the handoff", func() bool { _, ok := handoff.handoffFor("pixel-9"); return ok })
		if zh {
			for i := range handoff.needs {
				if handoff.needs[i].Kind == gateway.NeedHandoff {
					handoff.needs[i].Title = "请先登录 Dinely。" // "Sign in to Dinely first, please."
				}
			}
		}
		locked := newTestApp(t, fakeapi.Locked)
		locked.waitFor(t, "the lock", func() bool { return len(locked.devices) > 0 && locked.isLocked(locked.devices[0]) })

		for _, dark := range []bool{false, true} {
			shots := []*image.RGBA{
				render(takeover, checkout, dark, false),
				render(handoff, checkout, dark, false),
				render(locked, pinPad, dark, false),
				render(locked, pinPad, dark, true),
			}
			if lang == "en" {
				write("takeover-"+theme(dark)+".png", shots[0])
			}
			// The middle of the Phone screen, around the phone and its
			// banner, below the header.
			crop := image.Rect(340*scale, 58*scale, 840*scale, 820*scale)
			gap := 24 * scale
			out := image.NewRGBA(image.Rect(0, 0, len(shots)*crop.Dx()+(len(shots)-1)*gap, crop.Dy()))
			bg := lightPalette.bg
			if dark {
				bg = darkPalette.bg
			}
			draw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{bg.R, bg.G, bg.B, 255}), image.Point{}, draw.Src)
			for i, s := range shots {
				at := image.Pt(i*(crop.Dx()+gap), 0)
				draw.Draw(out, crop.Sub(crop.Min).Add(at), s, crop.Min, draw.Src)
			}
			short := langShort(lang)
			write("mirror-bar-"+short+"-"+theme(dark)+".png", out)
		}
	}
}
