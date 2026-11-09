package ui

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/video"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// orientAPI is the fake of three phones with the streams a test picks:
// each phone's picture at a size, marked with a turn (driver.Packet.Turn),
// and iPhone 17 shown as an iPad when ipad is set.
type orientAPI struct {
	*fakeapi.API
	ipad  bool
	specs map[string]orientSpec
	open  []driver.Stream
}

// orientSpec is a phone's picture as it sends it: w×h, turned turn
// quarter turns from upright.
type orientSpec struct{ w, h, turn int }

func (o *orientAPI) Devices() []gateway.DeviceView {
	ds := o.API.Devices()
	if o.ipad {
		for i := range ds {
			if ds[i].Info.ID == "iphone-17" {
				ds[i].Info.Model = "iPad Pro 11-inch (M5)"
			}
		}
	}
	return ds
}

func (o *orientAPI) Stream(ctx context.Context, id string, _ driver.StreamOptions) (driver.Stream, error) {
	spec, ok := o.specs[id]
	if !ok {
		return nil, fmt.Errorf("no stream of %s: %w", id, driver.ErrUnsupported)
	}
	st, err := video.StartTestStream(ctx, video.TestStreamOptions{Codec: driver.H264, Width: spec.w, Height: spec.h, FPS: 10, Source: "testsrc2"})
	if err != nil {
		return nil, err
	}
	o.open = append(o.open, st)
	return turned(st, spec.turn), nil
}

// darkBox returns the box of the dark pixels in r: the phone's bezel and
// screen on a light page.
func darkBox(img *image.RGBA, r image.Rectangle) image.Rectangle {
	box := image.Rectangle{}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if int(c.R)+int(c.G)+int(c.B) < 3*70 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// TestBezelFollowsOrientation turns an iPhone, an iPad and an Android
// phone's landscape app through their orientations (GitHub issue #16): on
// the phone's page and in All phones the bezel takes the picture's shape
// the right way up. With $PINKY_SCREENSHOTS set it saves both pages per
// case, to look at.
func TestBezelFollowsOrientation(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	dir := os.Getenv("PINKY_SCREENSHOTS")
	// Pictures as the phones send them, small for speed: an iPhone's
	// display is portrait whatever way it turns, an iPad Pro's landscape
	// (driver.Packet.Turn turns them upright), and an Android phone's
	// picture turns itself.
	iphone := func(turn int) orientSpec { return orientSpec{360, 780, turn} }
	ipad := func(turn int) orientSpec { return orientSpec{600, 420, turn} }
	cases := []struct {
		name  string
		ipad  bool
		id    string // the phone turned
		phone string // its name
		spec  orientSpec
		wide  bool // the upright picture is landscape
	}{
		{"iphone-portrait", false, "iphone-17", "iPhone 17", iphone(0), false},
		{"iphone-landscape-left", false, "iphone-17", "iPhone 17", iphone(3), true},
		{"iphone-landscape-right", false, "iphone-17", "iPhone 17", iphone(1), true},
		{"iphone-upside-down", false, "iphone-17", "iPhone 17", iphone(2), false},
		{"ipad-portrait", true, "iphone-17", "iPhone 17", ipad(1), false},
		{"ipad-landscape", true, "iphone-17", "iPhone 17", ipad(0), true},
		{"ipad-upside-down", true, "iphone-17", "iPhone 17", ipad(3), false},
		{"ipad-landscape-other", true, "iphone-17", "iPhone 17", ipad(2), true},
		{"android-landscape-app", false, "pixel-9", "Pixel 9", orientSpec{780, 360, 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ta := newTestApp(t, fakeapi.ThreePhones)
			specs := map[string]orientSpec{"pixel-9": {360, 780, 0}, "iphone-17": iphone(0)}
			specs[tc.id] = tc.spec
			oa := &orientAPI{API: ta.api, ipad: tc.ipad, specs: specs}
			ta.app.api = oa
			ta.app.videoErr = nil
			t.Cleanup(func() {
				ta.closeMirrors()
				for _, s := range oa.open {
					s.Close()
				}
			})
			// The test app reads the fake's phones itself: make iPhone 17
			// an iPad after each read.
			sync := func() {
				ta.sync()
				ta.app.devices = oa.Devices()
			}
			sync()
			if err := ta.api.StopAll(); err != nil {
				t.Fatal(err)
			}
			ta.waitFor(t, "the tasks to stop", func() bool { return !ta.anyRunning() })
			upW, upH := tc.spec.w, tc.spec.h
			if tc.spec.turn%2 == 1 {
				upW, upH = upH, upW
			}
			want := float64(upW) / float64(upH)
			shoot := func(name string, tt *mg.Tester) {
				if dir == "" {
					return
				}
				f, err := os.Create(filepath.Join(dir, "bezel-"+tc.name+"-"+name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				png.Encode(f, tt.Image())
			}
			playing := func(tt *mg.Tester) func() bool {
				return func() bool {
					tt.Frame()
					m := ta.mirrors[tc.id]
					return m != nil && m.state == "playing" && m.picW == upW && m.picH == upH
				}
			}

			// The phone's page.
			ta.selectPhone(tc.id)
			tt := mg.NewTester(ta.view, 1440, 900)
			ta.waitFor(t, "the picture on the phone's page", playing(tt))
			sync()
			tt.Frame()
			tt.Frame()
			r, ok := tt.Find(locales.MirrorScreenLabel(tc.phone))
			if !ok {
				t.Fatalf("no mirror: %q", tt.Texts())
			}
			if got := float64(r.W) / float64(r.H); got < want*0.95 || got > want*1.05 {
				t.Errorf("phone's page: the screen is %.0fx%.0f, of aspect %.2f; want %.2f", r.W, r.H, got, want)
			}
			if (r.W > r.H) != tc.wide {
				t.Errorf("phone's page: the screen is %.0fx%.0f", r.W, r.H)
			}
			if tc.ipad && !hasText(tt, "iPad · Wi-Fi") {
				t.Errorf("the phone's page does not say iPad: %q", tt.Texts())
			}
			shoot("phone", tt)

			// All phones: the tile's phone, found as the dark box in the
			// tile's slot.
			ta.selectPhone(allPhones)
			tg := mg.NewTester(ta.view, 1440, 900)
			ta.waitFor(t, "the picture in All phones", playing(tg))
			sync()
			tg.Frame()
			tg.Frame()
			tr, ok := tg.Find(locales.PhoneShow(tc.phone))
			if !ok {
				t.Fatalf("no tile: %q", tg.Texts())
			}
			slot := image.Rect(int(tr.X)+14, int(tr.Y)+14, int(tr.X+tr.W)-14, int(tr.Y)+14+tileSlotHeight)
			box := darkBox(tg.Image(), slot)
			sw, sh := float64(box.Dx()-10), float64(box.Dy()-10)
			if box.Empty() || sh <= 0 {
				t.Fatalf("All phones: no phone in the tile's slot %v", slot)
			}
			if got := sw / sh; got < want*0.9 || got > want*1.1 {
				t.Errorf("All phones: the phone is %v, its screen of aspect %.2f; want %.2f", box, got, want)
			}
			shoot("all", tg)
		})
	}
}

func TestFitBezel(t *testing.T) {
	// A portrait phone takes the height, a turned one the width.
	if pw, ph, sw, sh := fitBezel(0.46, 204, 352, 5); ph != 352 || sh != 342 || pw > 204 || sw != 342*0.46 {
		t.Errorf("portrait: %v×%v, screen %v×%v", pw, ph, sw, sh)
	}
	if pw, ph, sw, sh := fitBezel(2.16, 204, 352, 5); pw != 204 || sw != 194 || ph >= 352 || sh != 194/2.16 {
		t.Errorf("landscape: %v×%v, screen %v×%v", pw, ph, sw, sh)
	}
	// The corners round with the short side: the same turned, and less on
	// an iPad.
	o1, i1 := bezelRadius(bezelIPhone, 282, 614, 9)
	o2, i2 := bezelRadius(bezelIPhone, 614, 282, 9)
	if o1 != o2 || i1 != i2 || i1 < 40 || i1 > 44 || o1 != i1+8 {
		t.Errorf("iPhone radius %v/%v, turned %v/%v", o1, i1, o2, i2)
	}
	if _, inner := bezelRadius(bezelIPad, 420, 600, 9); inner >= i1/2 {
		t.Errorf("an iPad's corners are as round as an iPhone's: %v", inner)
	}
	if bezelIPad.defaultAspect() < 0.6 || bezelIPhone.defaultAspect() > 0.5 {
		t.Error("default shapes")
	}
}
