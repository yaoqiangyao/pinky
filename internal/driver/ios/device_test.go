//go:build device

// Tests against a real iPhone with iOS 27. Run them while holding the
// device's lock, for example:
//
//	PINKY_DEVICE=<udid> scripts/devlock.sh <udid> go test -tags device -count=1 ./internal/driver/ios/
//
// They use only the home screen, Settings (reading), Calculator and Clock,
// pace their input, and leave the phone on the home screen.
package ios

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

var shared struct {
	once sync.Once
	d    *Device
	err  error
}

// testDevice opens one session for all tests: opening one per test would
// start and stop the device's media stream again and again.
func testDevice(t *testing.T) *Device {
	t.Helper()
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	shared.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shared.d, shared.err = Open(ctx, driver.Info{ID: id, Platform: driver.IOS})
	})
	if shared.err != nil {
		t.Fatal(shared.err)
	}
	return shared.d
}

func TestMain(m *testing.M) {
	code := m.Run()
	if d := shared.d; d != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		d.Key(ctx, driver.KeyHome)
		cancel()
		time.Sleep(time.Second)
		d.Close()
	}
	os.Exit(code)
}

func ctxFor(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func pause() { time.Sleep(400 * time.Millisecond) }

// home presses Home and waits for SpringBoard to finish with it: a launch
// sent while the press is still being handled is undone by it.
func home(ctx context.Context, d *Device) {
	d.Key(ctx, driver.KeyHome)
	time.Sleep(1200 * time.Millisecond)
}

// TestDeviceInspect checks what the provider reports for the test device,
// without listing (and so probing) other phones.
func TestDeviceInspect(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	entry, err := findEntry(id, false)
	if err != nil {
		t.Fatal(err)
	}
	info := NewProvider().inspect(ctxFor(t, 30*time.Second), entry)
	t.Logf("%+v", info)
	if info.State != driver.StateOnline || info.Name == "" || info.Model == "" || major(info.OSVersion) < minIOS || info.Connection != "usb" {
		t.Fatalf("not ready: %+v", info)
	}
}

func TestDeviceStream(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 30*time.Second)
	s, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Codec() != driver.HEVC {
		t.Fatalf("codec %s", s.Codec())
	}
	w, h := s.Size()
	if w < 500 || h < 1000 {
		t.Fatalf("size %dx%d", w, h)
	}
	var frames, keys int
	first := true
	deadline := time.After(4 * time.Second)
loop:
	for {
		select {
		case p, ok := <-s.Packets():
			if !ok {
				t.Fatalf("stream ended: %v", s.Err())
			}
			if first {
				if !p.Config {
					t.Fatal("the first packet is not the codec configuration")
				}
				first = false
				continue
			}
			if frames == 0 && !p.KeyFrame {
				t.Fatal("the first frame is not a keyframe")
			}
			if p.KeyFrame {
				keys++
			}
			frames++
		case <-deadline:
			break loop
		}
	}
	st, _ := d.StreamStats()
	t.Logf("%dx%d, %d frames in about 4 s (%d keyframes); %+v", w, h, frames, keys, st)
	if frames < 30 {
		t.Fatalf("only %d frames", frames)
	}
}

// changed waits up to max for a video frame clearly larger than the idle
// ones, which means the screen moved.
func changed(d *Device, since time.Time, max time.Duration) bool {
	d.mu.Lock()
	m := d.m
	d.mu.Unlock()
	time.Sleep(max)
	base := 0
	for _, f := range m.framesSince(since.Add(-time.Second)) {
		if f.at.After(since) {
			break
		}
		if base == 0 || f.size < base {
			base = f.size
		}
	}
	limit := 4 * base
	if limit < 4096 {
		limit = 4096
	}
	for _, f := range m.framesSince(since) {
		if f.size > limit {
			return true
		}
	}
	return false
}

func TestDeviceHomeAndObserve(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{OCR: true})
	if err != nil {
		t.Fatal(err)
	}
	screen.NewRefs().Assign(s)
	t.Logf("%+v\n%s", st, s.Render())
	if s.App.ID != springBoard || s.Width < 500 {
		t.Fatalf("header %s", s.Header())
	}
	if st.Elements < 5 || st.Placed == 0 {
		t.Fatalf("elements %d, placed %d", st.Elements, st.Placed)
	}
}

func TestDeviceSwipeAndTouch(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	si := d.screen()
	left := screen.Point{X: si.Width * 8 / 10, Y: si.Height / 2}
	right := screen.Point{X: si.Width * 2 / 10, Y: si.Height / 2}

	at := time.Now()
	if err := d.Swipe(ctx, left, right, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !changed(d, at, time.Second) {
		t.Error("the screen did not move after a swipe")
	}
	pause()

	// The same gesture back, as raw touches. Their points are in video pixels,
	// which on iPhone equal screen pixels inside the screen.
	at = time.Now()
	if err := d.Touch(ctx, driver.TouchDown, right); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		time.Sleep(15 * time.Millisecond)
		p := screen.Point{X: right.X + (left.X-right.X)*i/20, Y: right.Y}
		if err := d.Touch(ctx, driver.TouchMove, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Touch(ctx, driver.TouchUp, left); err != nil {
		t.Fatal(err)
	}
	if !changed(d, at, time.Second) {
		t.Error("the screen did not move after raw touches")
	}
	pause()
	home(ctx, d)
}

func TestDeviceApps(t *testing.T) {
	d := testDevice(t)
	apps, err := d.Apps(ctxFor(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, a := range apps {
		ids[a.ID] = true
	}
	for _, want := range []string{"com.apple.Preferences", "com.apple.calculator", "com.apple.mobiletimer"} {
		if !ids[want] {
			t.Errorf("%s missing from %d apps", want, len(apps))
		}
	}
}

func TestDeviceLaunchSettings(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Launch(ctx, "Settings"); err != nil { // by display name
		t.Fatal(err)
	}
	settled, err := d.WaitSettled(ctx, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("settled=%v %+v\n%s", settled, st, s.Render())
	if s.App.ID != "com.apple.Preferences" {
		t.Fatalf("app %q", s.App.ID)
	}
	if len(s.Elements) == 0 {
		t.Fatal("no elements")
	}
	pause()
	home(ctx, d)
}

func TestDeviceCalculatorTap(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Launch(ctx, "com.apple.calculator"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("\n%s", s.Render())
	if s.App.ID != "com.apple.calculator" {
		t.Fatalf("app %q", s.App.ID)
	}
	var key screen.Element
	for _, e := range s.Elements {
		if (e.Label == "7" || e.Label == "8" || e.Label == "4") && !e.Bounds.Empty() {
			key = e
			break
		}
	}
	if key.Bounds.Empty() {
		t.Skip("no digit key placed by OCR on this screen")
	}
	pause()
	at := time.Now()
	if err := d.Tap(ctx, key.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	if !changed(d, at, time.Second) {
		t.Errorf("tapping %q changed nothing", key.Label)
	}
	pause()
	home(ctx, d)
}

func TestDeviceScreenshot(t *testing.T) {
	d := testDevice(t)
	img, err := d.Screenshot(ctxFor(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	si := d.screen()
	if b := img.Bounds(); b.Dx() != si.Width || b.Dy() != si.Height {
		t.Fatalf("screenshot %v, screen %dx%d", b, si.Width, si.Height)
	}
}

func TestDeviceLocked(t *testing.T) {
	d := testDevice(t)
	locked, err := d.Locked(ctxFor(t, 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("the test phone reports locked; stop and check it")
	}
}

func TestDeviceKeys(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 10*time.Second)
	if err := d.Key(ctx, driver.KeyRecents); err == nil || !strings.Contains(err.Error(), driver.ErrUnsupported.Error()) {
		t.Fatalf("recents: %v", err)
	}
}

// TestDeviceSwipeLatency reports how long a swipe takes to show up in the
// video.
func TestDeviceSwipeLatency(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	lat, err := d.SwipeLatency(ctx, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("touch-down to changed frame: %v", lat)
	if len(lat) < 2 {
		t.Fatalf("only %d of 4 swipes showed up in the video", len(lat))
	}
}

// TestDeviceType types into Settings' search field, the one text field the
// safety rules allow, and clears it again.
// TestDeviceType types into Settings' search field through the pasteboard:
// English while the phone's keyboard may be Pinyin, Chinese, emoji, with
// Append, and checks that the pasteboard holds what it held before each
// call, for an empty pasteboard, one with several items and
// representations, and one the phone does not let anyone read (cleared).
func TestDeviceType(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 3*time.Minute)
	pb := d.pasteboard()
	if pb == nil {
		t.Skip("no pasteboard service")
	}
	if err := d.Launch(ctx, "com.apple.Preferences"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var field screen.Element
	for _, e := range s.Elements {
		if e.Role == screen.RoleInput && e.Label == "Search" && !e.Bounds.Empty() {
			field = e
		}
	}
	if s.App.ID != "com.apple.Preferences" || field.Bounds.Empty() {
		t.Skip("Settings' search field is not on screen")
	}
	defer home(ctx, d)
	defer leaveSearch(ctx, t, d)
	// The pasteboard is set up by the test; at the end it is left empty.
	defer pb.set(context.Background(), []any{})
	if err := d.Tap(ctx, field.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	saveShot(ctx, t, d, "type-focused")

	person := []any{
		map[string]any{
			"types": []any{"public.utf8-plain-text", "public.html"},
			"data": map[string]any{
				"public.utf8-plain-text": map[string]any{"data": []byte("Pinky test clipboard")},
				"public.html":            map[string]any{"data": []byte("<b>Pinky</b> test clipboard")},
			},
		},
		map[string]any{
			"types": []any{"public.utf8-plain-text"},
			"data":  map[string]any{"public.utf8-plain-text": map[string]any{"data": []byte("second item")}},
		},
	}
	unreadable := []any{map[string]any{
		"types": []any{"public.utf8-plain-text", "org.nspasteboard.ConcealedType"},
		"data": map[string]any{
			"public.utf8-plain-text":         map[string]any{"data": []byte("not a password")},
			"org.nspasteboard.ConcealedType": map[string]any{"data": []byte{}},
		},
	}}
	steps := []struct {
		name   string
		board  []any // the pasteboard before the call
		text   string
		append bool
		want   string // the field afterwards
		saved  string
		after  string // the pasteboard afterwards: "same" or "empty"
	}{
		{"English, empty pasteboard", []any{}, "hello", false, "hello", "empty", "empty"},
		{"Chinese replaces it", person, "你好", false, "你好", "full", "same"},
		{"emoji appended", person, "👋🏽", true, "你好👋🏽", "full", "same"},
		// iOS's smart insert adds a space between two pasted words; Type
		// takes it off.
		{"English word", person, "Wall", false, "Wall", "full", "same"},
		{"word appended to a word", person, "paper", true, "Wallpaper", "full", "same"},
		{"unreadable pasteboard", unreadable, "clock", false, "clock", "unreadable", "empty"},
	}
	for _, step := range steps {
		if _, err := pb.set(ctx, step.board); err != nil {
			t.Fatalf("%s: set up the pasteboard: %v", step.name, err)
		}
		before, berr := pb.pull(ctx, pbAllResolved)
		if err := d.Type(ctx, step.text, driver.TypeOptions{Append: step.append}); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		st := d.LastTypeStats()
		t.Logf("%s: %s, saved %s (%v), pasted in %v (confirmed %v), %s in %v, %v in all",
			step.name, st.Method, st.Saved, st.Save.Round(time.Millisecond), st.Paste.Round(time.Millisecond),
			st.Confirmed, st.Restore, st.Restored.Round(time.Millisecond), st.Total.Round(time.Millisecond))
		if st.Method != "paste" || st.Saved != step.saved || !st.Confirmed {
			t.Errorf("%s: method %s, saved %s, confirmed %v; want paste, %s, true", step.name, st.Method, st.Saved, st.Confirmed, step.saved)
		}
		after, aerr := pb.pull(ctx, pbAllResolved)
		switch step.after {
		case "same":
			if berr != nil || aerr != nil || pbFingerprint(before) != pbFingerprint(after) {
				t.Errorf("%s: the pasteboard changed (errors %v, %v)", step.name, berr, aerr)
			}
		case "empty":
			if aerr != nil || len(after.Items) != 0 {
				t.Errorf("%s: the pasteboard is not empty afterwards (%v)", step.name, aerr)
			}
		}
		time.Sleep(time.Second)
		s, err := d.Observe(ctx, driver.ObserveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		saveShot(ctx, t, d, "type-"+strings.ReplaceAll(step.name, " ", "-"))
		if !fieldReads(s, step.want) {
			t.Errorf("%s: the field does not read %q:\n%s", step.name, step.want, s.Render())
		}
		pause()
	}
}

// saveShot writes a screenshot under /tmp/pinky-ios when PINKY_SHOTS is
// set, for looking at by hand (never committed; delete them afterwards).
func saveShot(ctx context.Context, t *testing.T, d *Device, name string) {
	if os.Getenv("PINKY_SHOTS") == "" {
		return
	}
	img, err := d.Screenshot(ctx)
	if err != nil {
		t.Log(err)
		return
	}
	os.MkdirAll("/tmp/pinky-ios", 0o700)
	f, err := os.Create("/tmp/pinky-ios/" + name + ".png")
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
}

// TestDeviceTypeKeys types with keys, the fallback for phones without the
// pasteboard service, in Settings' search: with a Pinyin keyboard active
// it switches to the Latin one for the text and back afterwards.
func TestDeviceTypeKeys(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 2*time.Minute)
	if err := d.Launch(ctx, "com.apple.Preferences"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var field screen.Element
	for _, e := range s.Elements {
		if e.Role == screen.RoleInput && e.Label == "Search" && !e.Bounds.Empty() {
			field = e
		}
	}
	if s.App.ID != "com.apple.Preferences" || field.Bounds.Empty() {
		t.Skip("Settings' search field is not on screen")
	}
	defer home(ctx, d)
	defer leaveSearch(ctx, t, d)
	if err := d.Tap(ctx, field.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	rax, err := d.raxNow()
	if err != nil {
		t.Skip("remoteAXService is not connected")
	}
	noop := func(string, ...any) {}
	d.inputMu.Lock()
	latinBefore, err := d.latinKeyboard(ctx, rax, noop)
	d.inputMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	strokes, _ := asciiStrokes("clock")
	var st TypeStats
	d.inputMu.Lock()
	err = d.typeKeys(ctx, strokes, &st)
	d.inputMu.Unlock()
	t.Logf("Latin keyboard active before: %v; typed in %v; %s", latinBefore, time.Since(start).Round(time.Millisecond), strings.Join(st.Trace, "; "))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	s, err = d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A Latin keyboard capitalizes the first letter of a field (the
	// hardware keyboard's Auto-Capitalization setting).
	if !fieldReads(s, "clock") && !fieldReads(s, "Clock") {
		t.Errorf("the field does not read \"clock\":\n%s", s.Render())
	}
	d.inputMu.Lock()
	d.pressKeys(ctx, []keyStroke{{usage: keyA, mod: keyLeftGUI}, {usage: keyBackspace}})
	latinAfter, err := d.latinKeyboard(ctx, rax, noop)
	d.inputMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if latinAfter != latinBefore {
		t.Errorf("the keyboard was not switched back (Latin before %v, after %v)", latinBefore, latinAfter)
	}
}

// TestDeviceWake checks what can be checked without the screen going off
// (the side button is off limits and the test phone does not lock by
// itself): the screen reads as on, and Wake then leaves the app in front.
func TestDeviceWake(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Launch(ctx, "com.apple.calculator"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	start := time.Now()
	on, err := d.ScreenOn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("screen on: %v, read in %v", on, time.Since(start).Round(time.Millisecond))
	if !on {
		t.Fatal("the screen reads as off while the test drives it")
	}
	if err := d.Wake(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.App.ID != "com.apple.calculator" {
		t.Errorf("Wake left %q in front; it must not press Home on a screen that is on", s.App.ID)
	}
	locked, err := d.Locked(ctx)
	if err != nil || locked {
		t.Errorf("locked %v, %v; want unlocked", locked, err)
	}
}

// fieldReads reports whether an input on screen shows text.
func fieldReads(s *screen.Screen, text string) bool {
	for _, e := range s.Elements {
		if e.Role == screen.RoleInput && (e.Label == text || strings.HasSuffix(e.Label, ", "+text)) {
			return true
		}
	}
	return false
}

// pbFingerprint summarizes a pasteboard's items, types and bytes.
func pbFingerprint(s *pbSnapshot) string {
	if s == nil {
		return "nil"
	}
	h := sha256.New()
	for _, it := range s.Items {
		fmt.Fprintf(h, "item %d\n", len(it.Types))
		for _, ty := range it.Types {
			fmt.Fprintf(h, "%s %x\n", ty, it.Data[ty].Data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// leaveSearch closes Settings' search through the close or Cancel button
// Observe finds.
func leaveSearch(ctx context.Context, t *testing.T, d *Device) {
	d.Key(ctx, driver.KeyEscape)
	time.Sleep(time.Second)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Log(err)
		return
	}
	for _, e := range s.Elements {
		if e.Role == screen.RoleButton && (e.Label == "close" || e.Label == "Cancel") && !e.Bounds.Empty() {
			d.Tap(ctx, e.Bounds.Center(), driver.TapOptions{})
			time.Sleep(time.Second)
			return
		}
	}
}

// TestDeviceObserveFrames taps a Calculator key where Observe places it and
// reads the display back, then clears it.
func TestDeviceObserveFrames(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	defer home(ctx, d)
	if err := d.Launch(ctx, "com.apple.calculator"); err != nil {
		t.Fatal(err)
	}
	observe := func(what string) *screen.Screen {
		t.Helper()
		settled, err := d.WaitSettled(ctx, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: settled %v, source %s, %d elements (%d with bounds), walk %v, total %v",
			what, settled, st.Source, st.Elements, st.Placed, st.Walk.Round(time.Millisecond), st.Total.Round(time.Millisecond))
		if !strings.HasPrefix(st.Source, "remoteax") {
			t.Fatalf("Observe fell back to %s: %v", st.Source, st.RAXError)
		}
		return s
	}
	find := func(s *screen.Screen, labels ...string) screen.Element {
		for _, e := range s.Elements {
			for _, l := range labels {
				if e.Label == l && !e.Bounds.Empty() {
					return e
				}
			}
		}
		t.Fatalf("no %q on screen:\n%s", labels, s.Render())
		return screen.Element{}
	}
	s := observe("launch")
	for _, e := range s.Elements {
		if e.Bounds.Empty() {
			t.Errorf("element without bounds: %s", e.Line())
		}
	}
	if err := d.Tap(ctx, find(s, "7").Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	s = observe("after 7")
	shows := false
	for _, e := range s.Elements {
		if e.ID == "StandardInputView" && e.Label == "7" {
			shows = true
			t.Logf("the display: %s", e.Line())
		}
	}
	if !shows {
		t.Errorf("the display does not show 7:\n%s", s.Render())
	}
	if err := d.Tap(ctx, find(s, "Clear", "All Clear").Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	observe("after clear")
}

// TestDeviceObserveTiming measures Observe the way an agent uses it: act,
// wait for the screen to settle, observe.
func TestDeviceObserveTiming(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 2*time.Minute)
	defer home(ctx, d)
	for _, app := range []string{"com.apple.Preferences", "com.apple.calculator"} {
		for i := 0; i < 3; i++ {
			home(ctx, d)
			if err := d.Launch(ctx, app); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			settled := false
			if os.Getenv("PINKY_NO_SETTLE") == "" {
				var err error
				settled, err = d.WaitSettled(ctx, 3*time.Second)
				if err != nil {
					t.Fatal(err)
				}
			}
			waited := time.Since(start)
			s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s #%d: settle %v (%v), observe %v via %s, %d elements, %d with bounds",
				app, i+1, waited.Round(time.Millisecond), settled, st.Total.Round(time.Millisecond), st.Source, len(s.Elements), st.Placed)
			if st.Total > 700*time.Millisecond {
				t.Logf("   %s", strings.Join(st.Trace, "; "))
			}
			// Observe again with no input in between: a walk.
			time.Sleep(pauseBetween)
			_, st, err = d.ObserveWithStats(ctx, driver.ObserveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s #%d again: observe %v via %s", app, i+1, st.Total.Round(time.Millisecond), st.Source)
		}
	}
}

const pauseBetween = 400 * time.Millisecond
