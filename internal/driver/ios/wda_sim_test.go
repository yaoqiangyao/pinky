//go:build wdasim

// Tests of the WebDriverAgent path against WDA running on the iOS
// Simulator, which speaks the same HTTP as on a phone. Build WDA for a
// simulator and run it, then:
//
//	PINKY_WDA_URL=http://127.0.0.1:8100 PINKY_MJPEG_URL=http://127.0.0.1:9100 \
//	  go test -tags wdasim -count=1 -v -run TestWDASim ./internal/driver/ios/
//
// docs/notes/ios.md has the commands. They use Settings (reading only) and
// the home screen.
package ios

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

func simDevice(t *testing.T) *WDADevice {
	t.Helper()
	base := os.Getenv("PINKY_WDA_URL")
	if base == "" {
		t.Skip("PINKY_WDA_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d, err := OpenWDAURL(ctx, base, os.Getenv("PINKY_MJPEG_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func findLabel(s *screen.Screen, label string) (screen.Element, bool) {
	for _, e := range s.Elements {
		if e.Label == label {
			return e, true
		}
	}
	return screen.Element{}, false
}

// closeSearch finds the button that leaves Settings' search: Cancel up to
// iOS 18, a close button from iOS 26.
func closeSearch(s *screen.Screen) (screen.Element, bool) {
	if e, ok := findLabel(s, "Cancel"); ok {
		return e, true
	}
	return findLabel(s, "close")
}

func simWait(t *testing.T, d *WDADevice, what string, ok func(*screen.Screen) bool) *screen.Screen {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var s *screen.Screen
	for time.Now().Before(deadline) {
		var err error
		s, err = d.Observe(simCtx(t, 30*time.Second), driver.ObserveOptions{})
		if err == nil && ok(s) {
			return s
		}
		time.Sleep(500 * time.Millisecond)
	}
	var labels []string
	if s != nil {
		for _, e := range s.Elements {
			labels = append(labels, e.Role+":"+e.Label)
		}
	}
	t.Fatalf("%s: not seen; last screen: %v", what, labels)
	return nil
}

func simCtx(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestWDASim(t *testing.T) {
	d := simDevice(t)
	info := d.Info()
	t.Logf("info %+v, screen %dx%d at %.0fx", info, d.width, d.height, d.scale)
	if d.width < 600 || d.height < 1200 || info.Backend != backendWDA {
		t.Fatalf("screen %dx%d backend %q", d.width, d.height, info.Backend)
	}

	t.Run("home screen", func(t *testing.T) {
		if err := d.Key(simCtx(t, 20*time.Second), driver.KeyHome); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		s := simWait(t, d, "home screen", func(s *screen.Screen) bool {
			_, ok := findLabel(s, "Safari")
			return ok && s.App.ID == springBoard
		})
		t.Logf("observe %v: %d elements, app %+v", time.Since(start), len(s.Elements), s.App)
		e, ok := findLabel(s, "Safari")
		if !ok || e.Bounds.Empty() || e.Role != screen.RoleButton {
			t.Fatalf("Safari icon: %+v %v", e, ok)
		}
	})

	t.Run("settings", func(t *testing.T) {
		if err := d.Launch(simCtx(t, 30*time.Second), "com.apple.Preferences"); err != nil {
			t.Fatal(err)
		}
		s := simWait(t, d, "Settings", func(s *screen.Screen) bool {
			if c, ok := closeSearch(s); ok { // left in search by an earlier run
				d.Tap(simCtx(t, 20*time.Second), c.Bounds.Center(), driver.TapOptions{})
				return false
			}
			if _, ok := findLabel(s, "About"); ok { // left on the General page
				d.Key(simCtx(t, 20*time.Second), driver.KeyBack)
				return false
			}
			_, ok := findLabel(s, "General")
			if !ok && s.App.ID == "com.apple.Preferences" { // scrolled down by an earlier run
				d.Swipe(simCtx(t, 20*time.Second), screen.Point{X: d.width / 2, Y: d.height / 3}, screen.Point{X: d.width / 2, Y: d.height * 4 / 5}, 300*time.Millisecond)
			}
			return ok && s.App.ID == "com.apple.Preferences"
		})
		gen, _ := findLabel(s, "General")
		t.Logf("General at %+v role %s", gen.Bounds, gen.Role)
		time.Sleep(400 * time.Millisecond)
		if err := d.Tap(simCtx(t, 20*time.Second), gen.Bounds.Center(), driver.TapOptions{}); err != nil {
			t.Fatal(err)
		}
		simWait(t, d, "General page", func(s *screen.Screen) bool { _, ok := findLabel(s, "About"); return ok })
		time.Sleep(400 * time.Millisecond)
		if err := d.Key(simCtx(t, 20*time.Second), driver.KeyBack); err != nil {
			t.Fatal(err)
		}
		simWait(t, d, "back on Settings", func(s *screen.Screen) bool {
			_, about := findLabel(s, "About")
			_, gen := findLabel(s, "General")
			return gen && !about
		})
	})

	t.Run("type", func(t *testing.T) {
		s := simWait(t, d, "search field", func(s *screen.Screen) bool {
			for _, e := range s.Elements {
				if e.Role == screen.RoleInput {
					return true
				}
			}
			return false
		})
		var field screen.Element
		for _, e := range s.Elements {
			if e.Role == screen.RoleInput {
				field = e
				break
			}
		}
		if err := d.Tap(simCtx(t, 20*time.Second), field.Bounds.Center(), driver.TapOptions{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(800 * time.Millisecond)
		for _, text := range []string{"Wallpaper", "蓝牙 é ✓"} {
			start := time.Now()
			if err := d.Type(simCtx(t, 30*time.Second), text, driver.TypeOptions{}); err != nil {
				t.Fatal(err)
			}
			t.Logf("typed %q in %v", text, time.Since(start))
			simWait(t, d, "field shows "+text, func(s *screen.Screen) bool {
				for _, e := range s.Elements {
					if e.Role == screen.RoleInput && strings.Contains(e.Label, text) {
						return true
					}
				}
				return false
			})
		}
		if err := d.Type(simCtx(t, 30*time.Second), "", driver.TypeOptions{}); err != nil {
			t.Fatal(err)
		}
		cancel, ok := closeSearch(simWait(t, d, "close search", func(s *screen.Screen) bool { _, ok := closeSearch(s); return ok }))
		if ok {
			d.Tap(simCtx(t, 20*time.Second), cancel.Bounds.Center(), driver.TapOptions{})
		}
	})

	t.Run("screenshot", func(t *testing.T) {
		img, err := d.Screenshot(simCtx(t, 30*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != d.width || b.Dy() != d.height {
			t.Fatalf("screenshot %v, screen %dx%d", b, d.width, d.height)
		}
	})

	t.Run("stream", func(t *testing.T) {
		if os.Getenv("PINKY_MJPEG_URL") == "" {
			t.Skip("PINKY_MJPEG_URL not set")
		}
		start := time.Now()
		s, err := d.Stream(simCtx(t, 30*time.Second), driver.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		w, h := s.Size()
		t.Logf("stream %dx%d %s, first frame after %v", w, h, s.Codec(), time.Since(start))
		// Move something so frames keep coming: swipe the list.
		go func() {
			time.Sleep(300 * time.Millisecond)
			d.Swipe(context.Background(), screen.Point{X: d.width / 2, Y: d.height * 3 / 4}, screen.Point{X: d.width / 2, Y: d.height / 3}, 400*time.Millisecond)
		}()
		var buf bytes.Buffer
		var n, keys, configs int
		timeout := time.After(3 * time.Second)
	read:
		for {
			select {
			case p, ok := <-s.Packets():
				if !ok {
					break read
				}
				n++
				if p.KeyFrame {
					keys++
				}
				if p.Config {
					configs++
				}
				buf.Write(p.Data)
			case <-timeout:
				break read
			}
		}
		t.Logf("%d packets, %d key frames, %d configs, %d bytes", n, keys, configs, buf.Len())
		if n < 3 || keys < 1 || configs < 1 {
			t.Fatalf("too few packets: %d (%d key, %d config)", n, keys, configs)
		}
		if probe, err := exec.LookPath("ffprobe"); err == nil {
			f := filepath.Join(t.TempDir(), "s.h264")
			os.WriteFile(f, buf.Bytes(), 0o600)
			out, err := exec.Command(probe, "-v", "error", "-count_frames", "-select_streams", "v:0",
				"-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "csv=p=0", f).CombinedOutput()
			t.Logf("ffprobe: %s", strings.TrimSpace(string(out)))
			if err != nil || !strings.HasPrefix(string(out), "h264,") {
				t.Fatalf("ffprobe: %v %s", err, out)
			}
		}
	})

	t.Run("settle", func(t *testing.T) {
		start := time.Now()
		ok, err := d.WaitSettled(simCtx(t, 10*time.Second), 3*time.Second)
		t.Logf("still screen: settled %v in %v", ok, time.Since(start))
		if err != nil || !ok {
			t.Fatalf("still screen: %v %v", ok, err)
		}
		if err := d.Swipe(simCtx(t, 20*time.Second), screen.Point{X: d.width / 2, Y: d.height * 3 / 4}, screen.Point{X: d.width / 2, Y: d.height / 3}, 200*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		ok, err = d.WaitSettled(simCtx(t, 10*time.Second), 3*time.Second)
		t.Logf("after a fling: settled %v in %v", ok, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("mirror touch", func(t *testing.T) {
		vw, vh := d.videoSize()
		if vw == 0 {
			t.Skip("no stream yet")
		}
		ctx := simCtx(t, 20*time.Second)
		p0 := screen.Point{X: vw / 2, Y: vh * 3 / 4}
		if err := d.Touch(ctx, driver.TouchDown, p0); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 10; i++ {
			time.Sleep(20 * time.Millisecond)
			d.Touch(ctx, driver.TouchMove, screen.Point{X: vw / 2, Y: p0.Y - i*vh/30})
		}
		if err := d.Touch(ctx, driver.TouchUp, screen.Point{X: vw / 2, Y: p0.Y - vh/3}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("locked", func(t *testing.T) {
		locked, err := d.Locked(simCtx(t, 10*time.Second))
		if err != nil || locked {
			t.Fatalf("locked %v, %v", locked, err)
		}
		d.Key(simCtx(t, 20*time.Second), driver.KeyHome)
	})
}

// TestWDASimOrientations turns the Simulator through each orientation in
// Safari, which turns on iPhones and iPads: the screen's size follows it,
// every element lies on the screen, a landscape screen has elements past
// the portrait width, and the live picture comes the right way up.
//
//	PINKY_WDA_URL=… PINKY_MJPEG_URL=… go test -tags wdasim -count=1 -v -run TestWDASimOrientations ./internal/driver/ios/
func TestWDASimOrientations(t *testing.T) {
	d := simDevice(t)
	setOrientation := func(o string) {
		t.Helper()
		if err := d.c.do(simCtx(t, 20*time.Second), d.c.hc, http.MethodPost, "/orientation", map[string]any{"orientation": o}, nil); err != nil {
			t.Fatalf("orientation %s: %v", o, err)
		}
		time.Sleep(1500 * time.Millisecond)
	}
	t.Cleanup(func() { setOrientation("PORTRAIT") })
	if err := d.Launch(simCtx(t, 30*time.Second), "com.apple.mobilesafari"); err != nil {
		t.Fatal(err)
	}
	short := min(d.width, d.height)
	for _, o := range []string{"LANDSCAPE", "UIA_DEVICE_ORIENTATION_LANDSCAPERIGHT", "PORTRAIT"} {
		t.Run(o, func(t *testing.T) {
			setOrientation(o)
			landscape := o != "PORTRAIT"
			s := simWait(t, d, "Safari "+o, func(s *screen.Screen) bool {
				return s.App.ID == "com.apple.mobilesafari" && (s.Width > s.Height) == landscape
			})
			if s.Orientation != orientationOf(s.Width, s.Height) {
				t.Fatalf("%dx%d named %s", s.Width, s.Height, s.Orientation)
			}
			far := 0
			for _, e := range s.Elements {
				b := e.Bounds
				if b.Empty() {
					continue
				}
				if b.X < 0 || b.Y < 0 || b.X+b.W > s.Width+2 || b.Y+b.H > s.Height+2 {
					t.Errorf("%q at %+v, off a %dx%d screen", e.Label, b, s.Width, s.Height)
				}
				far = max(far, b.X+b.W)
			}
			if landscape && far <= short {
				t.Errorf("no element past the portrait width %d (furthest edge %d)", short, far)
			}
			if os.Getenv("PINKY_MJPEG_URL") == "" {
				return
			}
			st, err := d.Stream(simCtx(t, 20*time.Second), driver.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if w, h := st.Size(); (w > h) != landscape {
				t.Errorf("the picture is %dx%d in %s", w, h, o)
			}
		})
	}
}

// TestWDASimUpsideDown turns an iPad Simulator upside down in Settings:
// XCTest's screenshot and picture come as the panel is, so Pinky turns
// them; OCR then finds General's text where the tree puts the element,
// and the picture's frames carry the half turn. iPhones never turn upside
// down, so the test skips on them.
func TestWDASimUpsideDown(t *testing.T) {
	d := simDevice(t)
	set := func(o string) error {
		err := d.c.do(simCtx(t, 20*time.Second), d.c.hc, http.MethodPost, "/orientation", map[string]any{"orientation": o}, nil)
		time.Sleep(1500 * time.Millisecond)
		return err
	}
	if err := d.Launch(simCtx(t, 30*time.Second), "com.apple.Preferences"); err != nil {
		t.Fatal(err)
	}
	if err := set("UIA_DEVICE_ORIENTATION_PORTRAIT_UPSIDEDOWN"); err != nil {
		t.Skipf("no upside down here (an iPhone): %v", err)
	}
	t.Cleanup(func() { set("PORTRAIT") })
	if q := d.interfaceTurn(simCtx(t, 10*time.Second)); q != 2 {
		t.Skipf("the interface did not turn upside down (turn %d)", q)
	}
	s := simWait(t, d, "Settings upside down", func(s *screen.Screen) bool {
		_, ok := findLabel(s, "General")
		return ok && s.App.ID == "com.apple.Preferences"
	})
	if s.Width > s.Height {
		t.Fatalf("upside down read as %dx%d", s.Width, s.Height)
	}
	var gen screen.Element
	for _, e := range s.Elements {
		if strings.HasPrefix(e.Label, "General") && e.Role == screen.RoleButton {
			gen = e
			break
		}
	}
	img, err := d.Screenshot(simCtx(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	lines, err := recognizeText(b.Bytes(), ocrOptions{Languages: ocrLanguages})
	if err != nil {
		t.Skipf("no OCR here: %v", err)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	found := false
	for _, l := range lines {
		r := ocrRect(l, w, h)
		c := r.Center()
		if strings.TrimSpace(l.Text) == "General" && c.X >= gen.Bounds.X && c.X <= gen.Bounds.X+gen.Bounds.W &&
			c.Y >= gen.Bounds.Y-20 && c.Y <= gen.Bounds.Y+gen.Bounds.H+20 {
			found = true
		}
	}
	if !found {
		t.Fatalf("OCR did not find General at the tree's %+v: the screenshot is not upright", gen.Bounds)
	}
	if os.Getenv("PINKY_MJPEG_URL") == "" {
		return
	}
	st, err := d.Stream(simCtx(t, 20*time.Second), driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if q, ok := st.(*frameStream).lastTurn(); !ok || q != 2 {
		t.Fatalf("the picture's frames turned %d (%v), want a half turn", q, ok)
	}
}
