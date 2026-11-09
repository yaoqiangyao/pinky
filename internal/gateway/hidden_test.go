package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// recordedScreen reads a tree the companion app sent on our Android 16
// emulator (testdata/README.md) as the driver turns it into a screen.
func recordedScreen(t *testing.T, name string) *screen.Screen {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "android16-emulator-"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var tr companion.Tree
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	return tr.Screen(companion.ScreenOptions{})
}

// blockNames blocks the given display names (any case).
func blockNames(blocked ...string) func(string) bool {
	return func(n string) bool {
		for _, b := range blocked {
			if strings.EqualFold(n, b) {
				return true
			}
		}
		return false
	}
}

func hiddenOf(s *screen.Screen) []screen.Element {
	var out []screen.Element
	for _, e := range s.Elements {
		if e.Hidden {
			out = append(out, e)
		}
	}
	return out
}

func labels(s *screen.Screen) string {
	var b strings.Builder
	for _, e := range s.Elements {
		b.WriteString(e.Line())
		b.WriteByte('\n')
	}
	return b.String()
}

// The shade with Shell's notification expanded: its header names the app.
// The row and its Collapse button give way to one hidden element in the
// row's place; the other notification and the shade's buttons stay.
func TestHideNotificationRowByName(t *testing.T) {
	s := recordedScreen(t, "shade-expanded")
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Shell")}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	text := labels(s)
	for _, gone := range []string{"Card statement ready", "1,234.56", `"Collapse"`} {
		if strings.Contains(text, gone) {
			t.Fatalf("%q still on the screen:\n%s", gone, text)
		}
	}
	for _, kept := range []string{"Serial console enabled", `"Expand"`, `"Silent"`, `"Clear all notifications."`, "Flashlight"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("%q was hidden:\n%s", kept, text)
		}
	}
	h := hiddenOf(s)
	if len(h) != 1 || h[0].Label != hiddenNotification || h[0].Bounds != (screen.Rect{X: 42, Y: 570, W: 996, H: 307}) {
		t.Fatalf("hidden element: %+v", h)
	}
	if !s.Elements[0].Hidden {
		t.Fatalf("the hidden element is not where the row was:\n%s", text)
	}
}

// Collapsed, the row starts with its title: only what the phone's list of
// notifications says of Shell's notification finds it.
func TestHideCollapsedNotificationRowByText(t *testing.T) {
	s := recordedScreen(t, "shade-collapsed")
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Shell")}); n != 0 {
		t.Fatalf("hid a row by name alone:\n%s", labels(s))
	}
	texts := noteTexts("Card statement ready", "Your balance is 1,234.56, due 12 Nov")
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Shell"), texts: texts}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	if text := labels(s); strings.Contains(text, "1,234.56") || !strings.Contains(text, "Serial console enabled") {
		t.Fatalf("wrong row hidden:\n%s", text)
	}
}

// Rows the driver did not merge into one element are gathered by place.
func TestHideNotificationRowInPieces(t *testing.T) {
	row := "FrameLayout#legacy_window_root/ScrollView#notification_stack_scroller/FrameLayout#expandableNotificationRow/FrameLayout#expanded"
	s := &screen.Screen{Width: 1080, Height: 2400, App: screen.App{ID: "com.android.systemui"}, Elements: []screen.Element{
		{Role: screen.RoleText, Label: "Northbank", ID: "android:id/app_name_text", Path: row, Bounds: screen.Rect{X: 179, Y: 622, W: 200, H: 43}},
		{Role: screen.RoleButton, Label: "Collapse", ID: "android:id/expand_button", Path: row, Bounds: screen.Rect{X: 891, Y: 570, W: 147, H: 147}},
		{Role: screen.RoleText, Label: "Card charged", ID: "android:id/title", Path: row, Bounds: screen.Rect{X: 179, Y: 701, W: 817, H: 57}},
		{Role: screen.RoleText, Label: "€12.00 at Café Lumen", ID: "android:id/text", Path: row, Bounds: screen.Rect{X: 179, Y: 774, W: 817, H: 51}},
		{Role: screen.RoleText, Label: "Chatter", ID: "android:id/app_name_text", Path: row, Bounds: screen.Rect{X: 179, Y: 1100, W: 200, H: 43}},
		{Role: screen.RoleText, Label: "Sam: Saturday?", ID: "android:id/title", Path: row, Bounds: screen.Rect{X: 179, Y: 1160, W: 817, H: 57}},
		{Role: screen.RoleText, Label: "an OCR line in the row", Source: screen.SourceOCR, Bounds: screen.Rect{X: 300, Y: 640, W: 300, H: 30}},
	}}
	if n := hideParts(s, hideHints{blockedName: blockNames("Northbank")}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	text := labels(s)
	if strings.Contains(text, "Card charged") || strings.Contains(text, "Café") || strings.Contains(text, "OCR line") || !strings.Contains(text, "Sam: Saturday?") {
		t.Fatalf("wrong elements hidden:\n%s", text)
	}
	if h := hiddenOf(s); len(h) != 1 || h[0].Bounds != (screen.Rect{X: 179, Y: 570, W: 859, H: 255}) {
		t.Fatalf("hidden element: %+v", h)
	}
}

// Recent apps: the card of a blocked app goes, with what OCR read of its
// snapshot; the other card stays.
func TestHideRecentsCard(t *testing.T) {
	s := recordedScreen(t, "recents")
	s.Elements = append(s.Elements, screen.Element{Role: screen.RoleText, Label: "Balance 4,321.09", Source: screen.SourceOCR,
		Bounds: screen.Rect{X: 300, Y: 900, W: 400, H: 50}})
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Settings")}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	text := labels(s)
	if strings.Contains(text, `"Settings"`) || strings.Contains(text, "4,321.09") || !strings.Contains(text, `"Chrome"`) || !strings.Contains(text, "scrollable") {
		t.Fatalf("wrong elements hidden:\n%s", text)
	}
	if h := hiddenOf(s); len(h) != 1 || h[0].Label != hiddenCard || h[0].Bounds != (screen.Rect{X: 173, Y: 375, W: 734, H: 1631}) {
		t.Fatalf("hidden element: %+v", h)
	}
	// In an app's own screen, a large element named like a blocked app is
	// the app's content.
	s = recordedScreen(t, "recents")
	s.App = screen.App{ID: "com.example.notes"}
	if n := hideParts(s, hideHints{blockedName: blockNames("Settings")}); n != 0 {
		t.Fatalf("hid an app's content:\n%s", labels(s))
	}
}

// A blocked app's name in recent apps whose card has no size Pinky can
// trust is hidden without a place, which withholds screenshots.
func TestHideRecentsNameWithoutCard(t *testing.T) {
	s := &screen.Screen{Width: 1080, Height: 2400, App: screen.App{ID: "com.example.launcher"}, Elements: []screen.Element{
		{Role: screen.RoleText, Label: "Northbank", ID: "com.example.launcher:id/task_title", Path: "FrameLayout#recents_view/FrameLayout#task_view",
			Bounds: screen.Rect{X: 200, Y: 300, W: 300, H: 50}},
		{Role: screen.RoleText, Label: "Chatter", ID: "com.example.launcher:id/task_title", Path: "FrameLayout#recents_view/FrameLayout#task_view",
			Bounds: screen.Rect{X: 700, Y: 300, W: 300, H: 50}},
		// A tablet's taskbar names apps too, and is not recent apps.
		{Role: screen.RoleButton, Label: "Northbank", ID: "com.example.launcher:id/taskbar_icon", Path: "FrameLayout#taskbar_view",
			Bounds: screen.Rect{X: 500, Y: 2300, W: 80, H: 80}},
	}}
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Northbank")}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	if h := hiddenOf(s); len(h) != 1 || !h[0].Bounds.Empty() {
		t.Fatalf("hidden element: %+v", h)
	}
	if text := labels(s); !strings.Contains(text, `button "Northbank"`) || !strings.Contains(text, `"Chatter"`) {
		t.Fatalf("the taskbar's icon or the other card was hidden:\n%s", text)
	}
	img := image.NewRGBA(image.Rect(0, 0, 540, 1200))
	if _, _, err := coverHidden(img, s); !errors.Is(err, errHiddenShot) {
		t.Fatalf("a hidden card without a place: %v", err)
	}
}

func TestFirstPart(t *testing.T) {
	cases := map[string]string{
		"Shell, •, now, Card statement ready, Your balance is 1,234.56":    "Shell",
		"Shell · • · now · Alerted · Your balance is 1,234.56, due 12 Nov": "Shell",
		"招商银行，交易提醒":                                                        "招商银行",
		"Settings":                                                         "",
		"Chase, 2m ago, Payment received":                                  "Chase",
	}
	for in, want := range cases {
		if got := firstPart(in); got != want {
			t.Errorf("firstPart(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNoteTexts(t *testing.T) {
	got := noteTexts("OK", "到账", "  Card   Statement Ready ", "A notification whose text runs on for much longer than any row shows it")
	want := []string{"到账", "card statement ready", "a notification whose text runs on for mu"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("noteTexts = %q", got)
	}
}

// listerPhone is a phone that lists its apps and notifications.
type listerPhone struct {
	driver.Device
	apps  []driver.App
	notes []driver.Notification
}

func (p *listerPhone) Apps(context.Context) ([]driver.App, error) { return p.apps, nil }
func (p *listerPhone) Notifications(context.Context) ([]driver.Notification, error) {
	return p.notes, nil
}

// hideBlocked takes the blocked apps from the phone's apps (an app blocked
// by its id is found by its name) and their texts from its notifications.
func TestHideBlockedWithThePhonesLists(t *testing.T) {
	g, _ := newTestGateway(t)
	d := &device{}
	phone := &listerPhone{
		apps: []driver.App{{ID: "com.android.shell", Name: "Shell"}, {ID: "android", Name: "Android System"}},
		notes: []driver.Notification{
			{App: "com.android.shell", Texts: []string{"Card statement ready", "Your balance is 1,234.56, due 12 Nov"}},
			{App: "android", Texts: []string{"Serial console enabled"}},
		},
	}
	s := recordedScreen(t, "shade-collapsed")
	g.hideBlocked(context.Background(), d, phone, s)
	if h := hiddenOf(s); len(h) != 0 {
		t.Fatalf("hidden while Shell is not blocked:\n%s", labels(s))
	}
	g.policy.AddApps(policy.CatBlocked, policy.CategoryEntries{IDs: []string{"com.android.shell"}})
	s = recordedScreen(t, "shade-collapsed")
	name := g.hideBlocked(context.Background(), d, phone, s)
	if h := hiddenOf(s); len(h) != 1 || strings.Contains(labels(s), "1,234.56") {
		t.Fatalf("Shell's row not hidden:\n%s", labels(s))
	}
	if name == nil || name("com.android.shell") != "Shell" {
		t.Fatal("no names for the read's events")
	}
	s = recordedScreen(t, "shade-expanded")
	g.hideBlocked(context.Background(), d, phone, s)
	if h := hiddenOf(s); len(h) != 1 {
		t.Fatalf("Shell's expanded row not hidden:\n%s", labels(s))
	}
	// By name alone: blocked by its id, Shell is found by its name.
	phone.notes = nil
	d.hide.notesAt = time.Time{}
	s = recordedScreen(t, "shade-expanded")
	g.hideBlocked(context.Background(), d, phone, s)
	if h := hiddenOf(s); len(h) != 1 {
		t.Fatalf("Shell's expanded row not hidden by its name:\n%s", labels(s))
	}
}

// Through an agent's task on a fake Pixel: the pulled-down shade over the
// home screen shows Google Wallet's notification (blocked by default) and
// one from Chatter. The agent reads that one is hidden, its screenshot has
// a gray box there, and a headerless row is found by the notification
// event the agent was not shown.
func TestShadeHidesBlockedNotifications(t *testing.T) {
	g, dev := newTestGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Check my notifications")
	stack := "system:com.android.systemui>FrameLayout#legacy_window_root>ScrollView#notification_stack_scroller"
	rowID := "com.android.systemui:id/expandableNotificationRow"
	wallet := screen.Rect{X: 42, Y: 570, W: 996, H: 307}
	chat := screen.Rect{X: 42, Y: 1055, W: 996, H: 218}
	dev.Show("home")
	dev.SetOverlay([]screen.Element{
		{Role: screen.RoleButton, Label: "Google Wallet, •, now, Card charged, €12.00 at Café Lumen", ID: rowID, Path: stack, Bounds: wallet},
		{Role: screen.RoleButton, Label: "Collapse", ID: "android:id/expand_button", Path: stack + ">FrameLayout#expandableNotificationRow",
			Bounds: screen.Rect{X: 891, Y: 570, W: 147, H: 147}},
		{Role: screen.RoleButton, Label: "Chatter, •, now, Sam, Are we still on for Saturday?", ID: rowID, Path: stack, Bounds: chat},
	})
	res := s.Observe(ctx, ObserveRequest{Screenshot: true})
	mustStatus(t, res, StatusSettled)
	text := res.Screen.Render()
	if strings.Contains(text, "Café") || !strings.Contains(text, hiddenNotification) || !strings.Contains(text, "Are we still on") {
		t.Fatalf("shade as the agent reads it:\n%s", text)
	}
	if res.Screenshot == nil {
		t.Fatalf("no screenshot: %s", res.Message)
	}
	if !covered(res.Screenshot.At(540, 723)) || covered(res.Screenshot.At(540, 1164)) {
		t.Fatal("the wallet's row is not covered, or the chat's is")
	}
	if !strings.Contains(res.Message, "Notifications and cards of blocked apps are covered in the screenshot.") {
		t.Fatalf("message: %q", res.Message)
	}

	// Collapsed, the row does not name the app; the notification the
	// agent was not shown does.
	dev.Notify("com.google.android.apps.walletnfcrel", "Card charged: €48.20 at Bäckerei Sonne")
	dev.SetOverlay([]screen.Element{
		{Role: screen.RoleButton, Label: "Card charged, •, now, €48.20 at Bäckerei Sonne", ID: rowID, Path: stack, Bounds: wallet},
	})
	res = s.Observe(ctx, ObserveRequest{})
	text = res.Screen.Render()
	if strings.Contains(text, "Bäckerei") || !strings.Contains(text, hiddenNotification) {
		t.Fatalf("collapsed row as the agent reads it:\n%s", text)
	}
	for _, ev := range res.Screen.Events {
		if strings.Contains(ev.Text, "Bäckerei") {
			t.Fatalf("the blocked app's notification reached the agent: %+v", ev)
		}
	}
}

// A heads-up notification over an app is hidden there too, and the app's
// screen stays as it is.
func TestHeadsUpHiddenOverAnApp(t *testing.T) {
	g, dev := newTestGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Order lunch")
	dev.Show("dinely")
	dev.SetOverlay([]screen.Element{
		{Role: screen.RoleButton, Label: "Google Wallet, •, now, Card charged, €12.00", ID: "com.android.systemui:id/expandableNotificationRow",
			Path: "system:com.android.systemui>FrameLayout", Bounds: screen.Rect{X: 42, Y: 60, W: 996, H: 220}},
	})
	res := s.Observe(ctx, ObserveRequest{})
	text := res.Screen.Render()
	if strings.Contains(text, "€12.00") || !strings.Contains(text, hiddenNotification) || !strings.Contains(text, "Dinely") {
		t.Fatalf("heads-up as the agent reads it:\n%s", text)
	}
}

// History's pictures of the shade have the blocked app's row covered too.
func TestHistoryShotsCoverHiddenRows(t *testing.T) {
	g, hd, _ := shotGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Open Chatter")
	hd.Device.SetOverlay([]screen.Element{
		{Role: screen.RoleButton, Label: "Google Wallet, •, now, Card charged, €12.00", ID: "com.android.systemui:id/expandableNotificationRow",
			Path: "system:com.android.systemui>FrameLayout", Bounds: screen.Rect{X: 42, Y: 60, W: 996, H: 220}},
	})
	g.Flush()
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Chatter"}), StatusSettled)
	g.Flush()
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled) // confirms the picture
	steps := toolSteps(t, g, "Open Chatter", "phone_launch")
	if len(steps) != 1 || steps[0].After == "" {
		t.Fatalf("steps: %+v", steps)
	}
	img := mediaImage(t, g, steps[0].After)
	if x, y := at(img, screen.Point{X: 540, Y: 170}); !covered(img.At(x, y)) {
		t.Fatalf("the blocked app's row is not covered in history: %v", img.At(x, y))
	}
	if x, y := at(img, screen.Point{X: 540, Y: 1500}); covered(img.At(x, y)) {
		t.Fatal("the app's screen is covered in history")
	}
}

// An app switcher labelled as a whole still has its cards looked at.
func TestHideCardInsideALabelledContainer(t *testing.T) {
	s := &screen.Screen{Width: 1170, Height: 2532, App: screen.App{ID: "com.apple.springboard", Name: "SpringBoard"}, Elements: []screen.Element{
		{Role: screen.RoleOther, Label: "App Switcher", Bounds: screen.Rect{X: 0, Y: 0, W: 1170, H: 2532}},
		{Role: screen.RoleButton, Label: "Northbank", Bounds: screen.Rect{X: 100, Y: 400, W: 700, H: 1500}},
		{Role: screen.RoleButton, Label: "Chatter", Bounds: screen.Rect{X: 850, Y: 400, W: 300, H: 1500}},
		{Role: screen.RoleText, Label: "Available balance 4,321.09", Source: screen.SourceOCR, Bounds: screen.Rect{X: 150, Y: 800, W: 500, H: 60}},
	}}
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Northbank")}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	if text := labels(s); strings.Contains(text, "4,321.09") || !strings.Contains(text, "App Switcher") || !strings.Contains(text, "Chatter") {
		t.Fatalf("wrong elements hidden:\n%s", text)
	}
}

// An iPhone's notification cell starts with its app's name.
func TestHideNotificationCellOnSpringBoard(t *testing.T) {
	s := &screen.Screen{Width: 1170, Height: 2532, App: screen.App{ID: "com.apple.springboard", Name: "SpringBoard"}, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "Northbank, 2m ago, Card charged, €12.00 at Café Lumen", Bounds: screen.Rect{X: 30, Y: 600, W: 1110, H: 230}},
		{Role: screen.RoleButton, Label: "Chatter, now, Sam, Saturday?", Bounds: screen.Rect{X: 30, Y: 850, W: 1110, H: 230}},
		{Role: screen.RoleButton, Label: "Northbank", Bounds: screen.Rect{X: 60, Y: 2100, W: 180, H: 180}},
	}}
	if n := hideParts(s, hideHints{system: true, blockedName: blockNames("Northbank")}); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	if text := labels(s); strings.Contains(text, "Café") || !strings.Contains(text, "Saturday?") || !strings.Contains(text, `button "Northbank"`) {
		t.Fatalf("wrong elements hidden (the home screen's icon stays):\n%s", text)
	}
}

// An iPhone's banner over an app comes as one element with the app that
// posted it: a blocked app's is hidden by its id, though its name says
// nothing; another app's stays. A blocked app's banner without a place is
// hidden without one.
func TestHideBannerByItsApp(t *testing.T) {
	s := &screen.Screen{Width: 1125, Height: 2436, App: screen.App{ID: "com.apple.mobilenotes", Name: "Notes"}, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "Ledgerly, Card charged, €12.00", ID: screen.NotificationID("com.example.ledgerly"),
			Path: "com.apple.springboard", Bounds: screen.Rect{X: 24, Y: 30, W: 1077, H: 240}},
		{Role: screen.RoleText, Label: "now", Source: screen.SourceOCR, Bounds: screen.Rect{X: 950, Y: 60, W: 80, H: 40}},
		{Role: screen.RoleText, Label: "Shopping list", Bounds: screen.Rect{X: 40, Y: 400, W: 600, H: 60}},
	}}
	h := hideHints{blockedName: blockNames(), blockedApp: func(id string) bool { return id == "com.example.ledgerly" }}
	if n := hideParts(s, h); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	if text := labels(s); strings.Contains(text, "€12.00") || strings.Contains(text, `"now"`) || !strings.Contains(text, "Shopping list") {
		t.Fatalf("wrong elements hidden:\n%s", text)
	}
	if h := hiddenOf(s); len(h) != 1 || h[0].Bounds != (screen.Rect{X: 24, Y: 30, W: 1077, H: 240}) {
		t.Fatalf("hidden element: %+v", h)
	}

	s = &screen.Screen{Width: 1125, Height: 2436, App: screen.App{ID: "com.apple.mobilenotes"}, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "Ledgerly, Card charged", ID: screen.NotificationID("com.example.ledgerly")},
		{Role: screen.RoleButton, Label: "Messages, Anna, Lunch?", ID: screen.NotificationID("com.apple.MobileSMS")},
	}}
	if n := hideParts(s, h); n != 1 {
		t.Fatalf("hid %d parts:\n%s", n, labels(s))
	}
	if hd := hiddenOf(s); len(hd) != 1 || !hd[0].Bounds.Empty() || !strings.Contains(labels(s), "Lunch?") {
		t.Fatalf("banners without a place:\n%s", labels(s))
	}
	if _, _, err := coverHidden(image.NewRGBA(image.Rect(0, 0, 375, 812)), s); !errors.Is(err, errHiddenShot) {
		t.Fatalf("a blocked banner without a place: %v", err)
	}
}

// On a fake iPhone, Wallet's banner (blocked by default) over Settings:
// the agent reads it as hidden, the screenshot covers it, and a banner of
// another app stays readable, its code masked.
func TestIPhoneBannerHidden(t *testing.T) {
	dev := demo.NewDevice("iphone", "iPhone 17", driver.IOS)
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{demo.NewProvider(driver.IOS, dev)} })
	ctx := context.Background()
	s, _ := connect(t, g, "Change a setting")
	dev.Show("settings")
	dev.SetOverlay([]screen.Element{
		{Role: screen.RoleButton, Label: "Wallet, Apple Card, $12.00 at Café Lumen", ID: screen.NotificationID("com.apple.Passbook"),
			Path: "com.apple.springboard", Bounds: screen.Rect{X: 24, Y: 30, W: 1032, H: 240}},
		{Role: screen.RoleButton, Label: "Messages, Anna, Your code is 482913", ID: screen.NotificationID("com.apple.MobileSMS"),
			Path: "com.apple.springboard", Bounds: screen.Rect{X: 24, Y: 290, W: 1032, H: 240}},
	})
	res := s.Observe(ctx, ObserveRequest{Screenshot: true})
	mustStatus(t, res, StatusSettled)
	text := res.Screen.Render()
	if strings.Contains(text, "Café") || !strings.Contains(text, hiddenNotification) || !strings.Contains(text, "Your code is ••••••") {
		t.Fatalf("banners as the agent reads them:\n%s", text)
	}
	if res.Screenshot == nil {
		t.Fatalf("no screenshot: %s", res.Message)
	}
	b := res.Screenshot.Bounds()
	at := func(x, y int) (int, int) { return x * b.Dx() / 1080, y * b.Dy() / 2340 }
	if x, y := at(540, 150); !covered(res.Screenshot.At(x, y)) {
		t.Fatal("Wallet's banner is not covered")
	}
	if x, y := at(540, 1500); covered(res.Screenshot.At(x, y)) {
		t.Fatal("Settings is covered")
	}
}
