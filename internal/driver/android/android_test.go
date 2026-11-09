package android

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
	"github.com/DIYgod/pinky/internal/screen"
)

// Outputs below are hand-written in the shape each Android version prints.

func TestParseKeyguard(t *testing.T) {
	android17 := "    KeyguardServiceDelegate\n      showing=true\n      occluded=false\n      simSecure=false\n"
	android10 := "    KeyguardServiceDelegate\n      showing=false\n      showingAndNotOccluded=true\n      KeyguardStateMonitor\n        mIsShowing=false\n"
	older := "    mShowingLockscreen=true mShowingDream=false\n"
	for name, c := range map[string]struct {
		out           string
		showing, have bool
	}{
		"android 17": {android17, true, true},
		"android 10": {android10, false, true},
		"older":      {older, true, true},
		"nothing":    {"mDreamingLockscreen=false\n", false, false},
	} {
		showing, ok := parseKeyguard(c.out)
		if showing != c.showing || ok != c.have {
			t.Errorf("%s: showing=%v ok=%v", name, showing, ok)
		}
	}
}

func TestParseMisc(t *testing.T) {
	if pkg := parseFocusedApp("  mFocusedApp=ActivityRecord{250279450 u0 app.lawnchair.play/app.lawnchair.LawnchairLauncher t348}"); pkg != "app.lawnchair.play" {
		t.Errorf("focused app = %q", pkg)
	}
	if pkg := parseFocusedApp("  mFocusedApp=AppWindowToken{7d88c2 token=Token{abab10d ActivityRecord{10b6fa4 u0 com.google.android.apps.nexuslauncher/.NexusLauncherActivity t163}}}"); pkg != "com.google.android.apps.nexuslauncher" {
		t.Errorf("focused app (Android 10) = %q", pkg)
	}
	for out, want := range map[string]bool{"  mWakefulness=Awake\n": true, "  mWakefulness=Dozing\n": false, "  mWakefulness=Asleep\n": false} {
		if awake, ok := parseAwake(out); !ok || awake != want {
			t.Errorf("parseAwake(%q) = %v, %v", out, awake, ok)
		}
	}
	w, h, ok := parseWMSize("Physical size: 1080x2340\n")
	if !ok || w != 1080 || h != 2340 {
		t.Errorf("wm size = %dx%d %v", w, h, ok)
	}
	w, h, _ = parseWMSize("Physical size: 1080x2340\nOverride size: 720x1560\n")
	if w != 720 || h != 1560 {
		t.Errorf("override = %dx%d", w, h)
	}
	if w, h := rotate(1080, 2340, 1); w != 2340 || h != 1080 {
		t.Errorf("rotate = %dx%d", w, h)
	}
	if w, h := orient(1080, 2340, 1920, 886); w != 2340 || h != 1080 {
		t.Errorf("orient landscape = %dx%d", w, h)
	}
	if w, h := orient(2340, 1080, 540, 1170); w != 1080 || h != 2340 {
		t.Errorf("orient portrait = %dx%d", w, h)
	}
	sec := splitSections("showing=false\n--ime--\nmInputShown=true\n")
	if sec[""] != "showing=false\n" || sec["ime"] != "mInputShown=true\n\n" {
		t.Errorf("sections = %q", sec)
	}
}

func TestInfoFromEntry(t *testing.T) {
	info := infoFromEntry(adb.DeviceEntry{Serial: "3e8be48e", State: "device", USB: "1-1", Model: "Mi_10_Pro"})
	if info.State != driver.StateOnline || info.Name != "Mi 10 Pro" || info.Connection != "usb" || info.Backend != "scrcpy" {
		t.Errorf("online = %+v", info)
	}
	info = infoFromEntry(adb.DeviceEntry{Serial: "192.168.1.2:5555", State: "unauthorized"})
	if info.State != driver.StateUnauthorized || info.Connection != "wifi" || info.Detail == "" {
		t.Errorf("unauthorized = %+v", info)
	}
	info = infoFromEntry(adb.DeviceEntry{Serial: "x", State: "no permissions", Detail: "missing udev rules"})
	if info.State != driver.StateNeedsSetup || info.Setup != setupUSBPermission {
		t.Errorf("no permissions = %+v", info)
	}
	if err := wrapErr(&adb.Error{Msg: "device 'x' not found"}); !errors.Is(err, driver.ErrOffline) {
		t.Errorf("wrapErr = %v", err)
	}
}

func TestMatchApp(t *testing.T) {
	apps := []driver.App{
		{ID: "com.android.settings", Name: "\u8bbe\u7f6e", System: true},
		{ID: "com.android.calculator2", Name: "Calculator", System: true},
		{ID: "com.android.deskclock", Name: "Clock", System: true},
		{ID: "com.example.cal", Name: "Calendar"},
		{ID: "com.example.clockwork", Name: "Clockwork"},
	}
	for q, want := range map[string]string{
		"com.android.settings": "com.android.settings",
		"\u8bbe\u7f6e":         "com.android.settings",
		"calculator":           "com.android.calculator2",
		"CLOCK":                "com.android.deskclock", // exact beats prefix
		"calcu":                "com.android.calculator2",
		"work":                 "com.example.clockwork",
	} {
		got, err := matchApp(apps, q)
		if err != nil || got != want {
			t.Errorf("matchApp(%q) = %q, %v; want %q", q, got, err, want)
		}
	}
	if _, err := matchApp(apps, "cal"); !errors.Is(err, errAmbiguous) {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if _, err := matchApp(apps, "nothing"); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if !looksLikePackage("com.example.app") || looksLikePackage("Calculator") || looksLikePackage("a b.c") {
		t.Error("looksLikePackage")
	}
}

func TestKeyTypingFor(t *testing.T) {
	gboard := "com.google.android.inputmethod.latin/com.android.inputmethod.latin.LatinIME"
	for _, c := range []struct {
		ime, locale string
		keys        bool
	}{
		{"com.android.inputmethod.latin/.LatinIME", "zh-Hans-CN", true}, // AOSP keyboard: letters commit directly
		{gboard, "en-US", true},
		{gboard, "zh-Hans-CN", false}, // Gboard on a Chinese phone may be in pinyin mode
		{gboard, "ja_JP", false},
		{"com.sohu.inputmethod.sogou/.SogouIME", "en-US", false},
		{"com.example.ime/.Ime", "", true},
		{"", "ko-KR", false},
	} {
		if got := keyTypingFor(c.ime, c.locale); got != c.keys {
			t.Errorf("keyTypingFor(%q, %q) = %v", c.ime, c.locale, got)
		}
	}
}

func TestParseIMEWindowVisible(t *testing.T) {
	if parseIMEWindowVisible("    mHasSurface=false isReadyForDisplay()=false\n    isOnScreen=false\n    isVisible=false\n") {
		t.Error("hidden keyboard reported as visible")
	}
	if !parseIMEWindowVisible("    isOnScreen=true\n    isVisible=true\n") {
		t.Error("visible keyboard not seen")
	}
}

func TestPlainASCII(t *testing.T) {
	if !plainASCII("hello, World! 123 ~") {
		t.Error("printable ASCII rejected")
	}
	for _, s := range []string{"line\nbreak", "tab\there", "caf\u00e9", "\u4f60\u597d"} {
		if plainASCII(s) {
			t.Errorf("%q should be pasted", s)
		}
	}
}

func TestSettleTracker(t *testing.T) {
	var tr settleTracker
	t0 := time.Unix(1000, 0)
	tr.resize(t0, 1080, 2340)
	if tr.small != defaultSettleBytes(1080, 2340) || !tr.lastChange().IsZero() {
		t.Fatalf("after resize: small=%d last=%v", tr.small, tr.lastChange())
	}
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	at := func(n int) time.Time { return t0.Add(ms(n)) }
	pts := ms(5000)
	step := func(arrive, gap, size int, key bool) bool {
		pts += ms(gap)
		return tr.packet(at(arrive), pts, size, false, key)
	}
	if step(0, 0, 40000, true) {
		t.Error("a key frame counted as a change")
	}
	if step(100, 100, 30000, false) {
		t.Error("a refinement 100 ms after the previous frame counted as a change")
	}
	if !step(111, 11, 20000, false) {
		t.Error("an animation frame did not count")
	}
	if tr.lastChange() != at(111) {
		t.Errorf("last change = %v", tr.lastChange())
	}
	if step(122, 11, 100, false) {
		t.Error("a tiny packet counted as a change")
	}
	if !step(2000, 1878, 5000, false) {
		t.Error("a new frame after silence did not count")
	}
	if tr.packet(at(2001), 0, 30, true, false) {
		t.Error("configuration counted")
	}
}

func TestWaitQuiet(t *testing.T) {
	ctx := context.Background()
	long := time.Now().Add(-time.Hour)
	still := quietCond{last: func() time.Time { return long }, quiet: 50 * time.Millisecond}
	busy := quietCond{last: time.Now, quiet: 50 * time.Millisecond}
	start := time.Now()
	ok, err := waitQuiet(ctx, start, time.Second, nil, still)
	if err != nil || !ok {
		t.Fatalf("still screen: %v %v", ok, err)
	}
	if d := time.Since(start); d < 50*time.Millisecond || d > 500*time.Millisecond {
		t.Errorf("still screen took %v", d)
	}
	// A screen that keeps changing hits the limit.
	ok, err = waitQuiet(ctx, time.Now(), 150*time.Millisecond, nil, busy)
	if err != nil || ok {
		t.Fatalf("busy screen: %v %v", ok, err)
	}
	// Every condition must hold: still video with a busy tree is not settled.
	ok, err = waitQuiet(ctx, time.Now(), 150*time.Millisecond, nil, still, busy)
	if err != nil || ok {
		t.Fatalf("still video, busy tree: %v %v", ok, err)
	}
	// Each condition has its own quiet period.
	lastTree := time.Now()
	tree := quietCond{last: func() time.Time { return lastTree }, quiet: 120 * time.Millisecond}
	start = time.Now()
	ok, err = waitQuiet(ctx, start, time.Second, nil, still, tree)
	if err != nil || !ok || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("tree quiet period: %v %v after %v", ok, err, time.Since(start))
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := waitQuiet(cctx, time.Now(), time.Second, nil, busy); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	done := make(chan struct{})
	close(done)
	if _, err := waitQuiet(ctx, time.Now(), time.Second, done, busy); err != errSessionEnded {
		t.Errorf("ended session: %v", err)
	}
}

func testLive(vw, vh int, screenSize func(int, int) (int, int)) *live {
	l := &live{cfg: &Config{}, video: true, sized: make(chan struct{}), done: make(chan struct{}), screenSize: screenSize, vw: vw, vh: vh}
	close(l.sized)
	return l
}

func TestPosition(t *testing.T) {
	ctx := context.Background()
	native := testLive(1080, 2340, func(vw, vh int) (int, int) { return orient(1080, 2340, vw, vh) })
	p, err := native.position(ctx, screen.Point{X: 540, Y: 1170})
	if err != nil || p != (scrcpy.Position{X: 540, Y: 1170, Width: 1080, Height: 2340}) {
		t.Errorf("native = %+v, %v", p, err)
	}
	// A scaled stream maps screen pixels onto the video frame.
	scaled := testLive(512, 1104, func(vw, vh int) (int, int) { return orient(1080, 2340, vw, vh) })
	p, _ = scaled.position(ctx, screen.Point{X: 1080, Y: 2340})
	if p != (scrcpy.Position{X: 511, Y: 1103, Width: 512, Height: 1104}) {
		t.Errorf("scaled corner = %+v", p)
	}
	p, _ = scaled.position(ctx, screen.Point{X: 540, Y: 1170})
	if p.X != 256 || p.Y != 552 {
		t.Errorf("scaled center = %+v", p)
	}
	// A background display whose video the server scaled down for its
	// encoder takes points in display pixels too.
	bg := &Background{w: 1080, h: 2400}
	small := testLive(230, 512, bg.screenSize)
	p, _ = small.position(ctx, screen.Point{X: 540, Y: 1200})
	if p != (scrcpy.Position{X: 115, Y: 256, Width: 230, Height: 512}) {
		t.Errorf("scaled background display = %+v", p)
	}
	// Without video, points are display pixels of the current rotation.
	nov := &live{cfg: &Config{}, done: make(chan struct{}), displaySize: func(context.Context) (int, int, error) { return 1920, 1080, nil }}
	p, _ = nov.position(ctx, screen.Point{X: 1900, Y: 1000})
	if p != (scrcpy.Position{X: 1900, Y: 1000, Width: 1920, Height: 1080}) {
		t.Errorf("without video = %+v", p)
	}
	// Landscape: the video is wide, so the screen is too.
	land := testLive(2340, 1080, func(vw, vh int) (int, int) { return orient(1080, 2340, vw, vh) })
	p, _ = land.position(ctx, screen.Point{X: 2000, Y: 100})
	if p.X != 2000 || p.Y != 100 || p.Width != 2340 {
		t.Errorf("landscape = %+v", p)
	}
}

func TestStreamPush(t *testing.T) {
	l := testLive(100, 100, nil)
	st := &stream{l: l, ch: make(chan driver.Packet, 4), waitStart: true}
	frame := scrcpy.Packet{Data: []byte{1}, PTS: time.Second}
	st.push(frame)
	if len(st.ch) != 0 {
		t.Fatal("a frame before the configuration was delivered")
	}
	st.push(scrcpy.Packet{Data: []byte{0}, Config: true})
	st.push(scrcpy.Packet{Data: []byte{2}, KeyFrame: true})
	st.push(frame)
	if len(st.ch) != 3 {
		t.Fatalf("delivered %d packets", len(st.ch))
	}
	if p := <-st.ch; !p.Config {
		t.Error("the stream did not start with the configuration")
	}
	st.end(errors.New("gone"))
	if _, ok := <-st.ch; !ok {
		t.Error("buffered packets were lost on end")
	}
	st.push(frame) // after end: ignored, no panic
	if st.Err() == nil {
		t.Error("no error after end")
	}
}

func TestRefusedText(t *testing.T) {
	for code, want := range map[string]bool{
		"not_found": true, "not_editable": true, "disabled": true, "unsupported": true, "action_failed": true,
		"timeout": false, "internal": false, "bad_request": false,
	} {
		err := fmt.Errorf("wrapped: %w", &companion.Error{Code: code})
		if got := refusedText(err); got != want {
			t.Errorf("refusedText(%s) = %v", code, got)
		}
	}
	if refusedText(companion.ErrNotRunning) || refusedText(context.DeadlineExceeded) || refusedText(nil) {
		t.Error("connection errors may have written the text")
	}
}

func TestFindCompanionAPK(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PINKY_COMPANION_APK", "")
	if _, err := FindCompanionAPK(); !errors.Is(err, errNoCompanionAPK) {
		t.Fatalf("nothing there: %v", err)
	}
	build := filepath.Join(dir, "android", "companion", "app", "build", "outputs", "apk", "debug")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	debug := filepath.Join(build, "app-debug.apk")
	os.WriteFile(debug, []byte("apk"), 0o644)
	if p, err := FindCompanionAPK(); err != nil || !sameFile(p, debug) {
		t.Fatalf("debug build: %q, %v", p, err)
	}
	env := filepath.Join(dir, "chosen.apk")
	os.WriteFile(env, []byte("apk"), 0o644)
	t.Setenv("PINKY_COMPANION_APK", env)
	if p, err := FindCompanionAPK(); err != nil || !sameFile(p, env) {
		t.Fatalf("$PINKY_COMPANION_APK: %q, %v", p, err)
	}
	cands := companionAPKCandidates()
	if len(cands) != 4 || !strings.HasSuffix(cands[1], "pinky-companion.apk") ||
		!strings.HasSuffix(cands[2], filepath.Join("..", "Resources", "pinky-companion.apk")) && !strings.Contains(cands[2], "Resources") {
		t.Errorf("candidates = %q", cands)
	}
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

func TestEmptyWithKeyboard(t *testing.T) {
	app := screen.App{ID: "com.google.android.settings.intelligence"}
	if !emptyWithKeyboard(&screen.Screen{App: app, Keyboard: true}) {
		t.Error("an empty tree with the keyboard up was accepted")
	}
	for _, s := range []*screen.Screen{
		{App: app, Keyboard: false},
		{App: app, Keyboard: true, Locked: true},
		{App: app, Keyboard: true, Elements: []screen.Element{{Role: "input"}}},
	} {
		if emptyWithKeyboard(s) {
			t.Errorf("flagged %+v", s)
		}
	}
}

func TestParseRootTasks(t *testing.T) {
	android16 := `RootTask id=1 bounds=[0,0][1080,2400] displayId=0 userId=0
 configuration={1.0 310mcc260mnc [en_US] ldltr sw411dp w411dp h914dp 420dpi}
  taskId=6: com.google.android.apps.nexuslauncher/com.google.android.apps.nexuslauncher.NexusLauncherActivity bounds=[0,0][1080,2400] userId=0 visible=true
RootTask id=122 bounds=[0,0][1080,2400] displayId=1038 userId=0
 configuration={1.0}
  taskId=122: com.google.android.deskclock/com.android.deskclock.DeskClock bounds=[0,0][1080,2400] userId=0 visible=true
`
	got := parseRootTasks(android16)
	if len(got) != 2 || got[0].id != 1 || got[0].display != 0 || len(got[0].tasks) != 1 || got[0].tasks[0] != 6 ||
		got[1].id != 122 || got[1].display != 1038 || got[1].tasks[0] != 122 {
		t.Errorf("Android 16: %+v", got)
	}
	android10 := "Stack id=60 bounds=[0,0][1080,1920] displayId=0 userId=0\n configuration={1.0}\n  taskId=66: app.pinky.companion/app.pinky.companion.MainActivity bounds=[0,0][1080,1920]\n  taskId=67: app.pinky.companion/x.Y bounds=[0,0][1080,1920]\n"
	got = parseRootTasks(android10)
	if len(got) != 1 || got[0].id != 60 || len(got[0].tasks) != 2 || got[0].tasks[1] != 67 {
		t.Errorf("Android 10: %+v", got)
	}
}

// TestMatchAppByPackage finds apps by English names on a phone in Chinese,
// through their packages.
func TestMatchAppByPackage(t *testing.T) {
	apps := []driver.App{
		{ID: "com.google.android.calculator", Name: "计算器", System: true},
		{ID: "com.google.android.deskclock", Name: "时钟", System: true},
		{ID: "com.android.settings", Name: "设置", System: true},
		{ID: "com.google.android.calendar", Name: "日历", System: true},
	}
	for q, want := range map[string]string{
		"Calculator": "com.google.android.calculator",
		"Clock":      "com.google.android.deskclock",
		"Settings":   "com.android.settings",
		"计算器":        "com.google.android.calculator",
	} {
		got, err := matchApp(apps, q)
		if err != nil || got != want {
			t.Errorf("matchApp(%q) = %q, %v; want %q", q, got, err, want)
		}
	}
	if _, err := matchApp(apps, "Maps"); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("an app the phone lacks: %v", err)
	}
}

// TestOnePhoneOverUSBAndWiFi lists a phone connected both ways once, under
// its hardware serial, by USB while that works.
func TestOnePhoneOverUSBAndWiFi(t *testing.T) {
	usb := adb.DeviceEntry{Serial: "3e8be48e", USB: "1048576X", State: adb.StateDevice}
	wifi := adb.DeviceEntry{Serial: "adb-3e8be48e-kIY2HE._adb-tls-connect._tcp", State: adb.StateDevice}
	pr := deviceProps{HardwareSerial: "3e8be48e"}
	if got := phoneID(usb, pr); got != "3e8be48e" {
		t.Errorf("USB id %q", got)
	}
	if got := phoneID(wifi, pr); got != "3e8be48e" {
		t.Errorf("Wi-Fi id %q, want the hardware serial", got)
	}
	if got := phoneID(wifi, deviceProps{}); got != "3e8be48e" {
		t.Errorf("Wi-Fi id without a hardware serial %q, want the serial in the name", got)
	}
	addr := adb.DeviceEntry{Serial: "192.168.1.5:41235", State: adb.StateDevice}
	if got := phoneID(addr, deviceProps{}); got != addr.Serial {
		t.Errorf("an address's id without a hardware serial %q", got)
	}
	emu := adb.DeviceEntry{Serial: "emulator-5560", State: adb.StateDevice}
	if got := phoneID(emu, deviceProps{HardwareSerial: "EMULATOR36X1X11X0"}); got != "emulator-5560" {
		t.Errorf("an emulator's id %q", got)
	}
	on := driver.StateOnline
	out := mergeTransports([]driver.Info{
		{ID: "3e8be48e", Connection: "wifi", State: on},
		{ID: "3e8be48e", Connection: "usb", State: on},
		{ID: "emulator-5560", Connection: "usb", State: on},
	})
	if len(out) != 2 || out[0].ID != "3e8be48e" || out[0].Connection != "usb" {
		t.Errorf("merged %+v", out)
	}
	out = mergeTransports([]driver.Info{
		{ID: "3e8be48e", Connection: "usb", State: driver.StateUnauthorized},
		{ID: "3e8be48e", Connection: "wifi", State: on},
	})
	if len(out) != 1 || out[0].Connection != "wifi" {
		t.Errorf("USB not usable, Wi-Fi online: %+v", out)
	}
}

// TestPhoneHidingItsSerial: a phone whose shell reads neither ro.serialno
// nor ro.boot.serialno, with several Wi-Fi connections at addresses (a new
// port each time Wireless debugging restarts), is listed once: under the
// serial that a connection naming it has with the same android_id, else
// under its android_id. Each connection was a phone of its own before: a
// Galaxy S10+ was listed some 40 times, all online.
func TestPhoneHidingItsSerial(t *testing.T) {
	const hidden = "SM-G975F\n12\n31\nGalaxy S10+\n\npinky-boot=\npinky-aid=9774d56d682e549c\n"
	addrs := []string{"192.168.1.5:37001", "192.168.1.5:41235", "192.168.1.5:43567"}
	line := func(serial, extra string) string {
		return serial + "\tdevice " + extra + "product:beyond2 model:SM_G975F device:beyond2 transport_id:1"
	}
	for _, tc := range []struct {
		name   string
		named  []string // connections that name the serial: a cable's, or the discovery's
		props  string
		wantID string
	}{
		{"addresses only", nil, hidden, "android-9774d56d682e549c"},
		{"with the discovery's connection", []string{"adb-R58M30ABCDE-kIY2HE._adb-tls-connect._tcp"}, hidden, "R58M30ABCDE"},
		{"with a cable", []string{"R58M30ABCDE usb:1-1 "}, hidden, "R58M30ABCDE"},
		{"ro.boot.serialno only", nil, "SM-G975F\n12\n31\nGalaxy S10+\n\npinky-boot=R58M30ABCDE\n", "R58M30ABCDE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p := newFakeADB(t)
			f.set(func(f *fakeADB) {
				f.props = map[string]string{}
				for _, n := range tc.named {
					serial, extra, _ := strings.Cut(n, " ")
					f.devices = append(f.devices, line(serial, extra))
					f.props[serial] = tc.props
				}
				for _, a := range addrs {
					f.devices = append(f.devices, line(a, ""))
					f.props[a] = tc.props
				}
			})
			infos, err := p.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(infos) != 1 || infos[0].ID != tc.wantID || infos[0].Name != "Galaxy S10+" || infos[0].State != driver.StateOnline {
				t.Fatalf("listed %+v, want one online Galaxy S10+ with id %s", infos, tc.wantID)
			}
		})
	}
	// Once seen with a connection that names it, the phone keeps that
	// serial at its addresses alone too.
	f, p := newFakeADB(t)
	f.set(func(f *fakeADB) {
		f.props = map[string]string{"adb-R58M30ABCDE-kIY2HE._adb-tls-connect._tcp": hidden, addrs[0]: hidden}
		f.devices = []string{line("adb-R58M30ABCDE-kIY2HE._adb-tls-connect._tcp", ""), line(addrs[0], "")}
	})
	if _, err := p.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeADB) { f.devices = f.devices[1:] })
	if infos, err := p.List(context.Background()); err != nil || len(infos) != 1 || infos[0].ID != "R58M30ABCDE" {
		t.Fatalf("at its address alone: %+v %v", infos, err)
	}
}

func TestResetWait(t *testing.T) {
	l := &live{cfg: &Config{}, video: true, sized: make(chan struct{}), done: make(chan struct{})}
	t0 := time.Now()
	if w := l.resetWait(t0); w != 0 {
		t.Fatalf("no reset sent: waited %v", w)
	}
	if !l.askReset(t0) {
		t.Fatal("the first reset was not sent")
	}
	// A second ask waits for the first one's answer instead of going out.
	if l.askReset(t0.Add(time.Second)) {
		t.Error("a second reset went out while the first waited")
	}
	if w := l.resetWait(t0.Add(3 * time.Second)); w != 3*time.Second {
		t.Errorf("waited %v, want the 3s since the first ask", w)
	}
	// The session header answers it (run clears resetAt).
	l.resetAt = time.Time{}
	if w := l.resetWait(t0.Add(time.Hour)); w != 0 {
		t.Errorf("answered: waited %v", w)
	}
	if !l.askReset(t0.Add(4 * time.Second)) {
		t.Fatal("a reset after the answer was not sent")
	}
	if w := l.resetWait(t0.Add(4*time.Second + resetStall)); w < resetStall {
		t.Errorf("unanswered: waited %v, want at least %v", w, resetStall)
	}
}
