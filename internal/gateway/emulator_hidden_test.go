//go:build device

package gateway

// Blocked apps' notifications and recent-apps cards on our emulator, never
// on a phone: the shell posts a notification that stands in for a bank's
// (blocked for this run), and Settings stands in for a banking app in
// recent apps.
//
//	go test -c -tags device -o /tmp/pinky-gw/gateway.test ./internal/gateway
//	scripts/devlock.sh emulator-5560 env PINKY_EMULATOR=emulator-5560 PINKY_TEST_PIN=1234 /tmp/pinky-gw/gateway.test -test.run TestEmulatorHidesBlocked -test.v

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

func TestEmulatorHidesBlockedNotificationsAndCards(t *testing.T) {
	id := emulatorID(t)
	ctx := context.Background()
	pol := policy.Default()
	for _, app := range []string{"com.android.shell", "com.android.settings", "com.google.android.settings.intelligence"} {
		if err := pol.SetAppBlocked(screen.App{ID: app}, true); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	prov := &oneDevice{Provider: android.NewProvider(android.Config{}), id: id}
	g, err := New(Options{Providers: []driver.Provider{prov}, TrustNewAgents: true, Policy: pol, Logger: log,
		NoScreenshots: true, PIN: testPIN})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for deadline := time.Now().Add(60 * time.Second); !deviceOnline(g, id); time.Sleep(500 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not come online", id)
		}
	}
	adbShell(t, id, "input", "keyevent", "KEYCODE_WAKEUP")
	if err := g.SetBackground(id, false); err != nil {
		t.Fatal(err)
	}
	unlockForTest(t, g, id)
	adbShell(t, id, "input", "keyevent", "KEYCODE_HOME")
	// Posting with the same tag again replaces it; the shell cannot
	// cancel it, so it stays in the emulator's shade.
	adbShell(t, id, "cmd notification post -S bigtext -t 'Card statement ready' pinky-hidden 'Your balance is 1,234.56, due 12 Nov'")
	time.Sleep(time.Second)

	s := g.OpenSession(agent)
	defer s.Close()
	if res := s.Connect(ctx, ConnectRequest{Task: "Check the notifications", Device: id}); res.IsError() {
		t.Fatalf("connect: %+v", res)
	}
	adbShell(t, id, "cmd", "statusbar", "expand-notifications")
	time.Sleep(1500 * time.Millisecond)
	check := func(what string, wantHidden string, gone ...string) *Result {
		t.Helper()
		res := s.Observe(ctx, ObserveRequest{Screenshot: true})
		text := res.Screen.Render()
		t.Logf("%s:\n%s\n%s", what, text, res.Message)
		for _, g := range gone {
			if strings.Contains(text, g) {
				t.Fatalf("%s: %q reached the agent", what, g)
			}
		}
		var hidden *screen.Element
		for i, e := range res.Screen.Elements {
			if e.Label == wantHidden {
				hidden = &res.Screen.Elements[i]
			}
		}
		if hidden == nil {
			t.Fatalf("%s: nothing hidden", what)
		}
		if res.Screenshot == nil {
			t.Fatalf("%s: no screenshot: %s", what, res.Message)
		}
		c := hidden.Bounds.Center()
		b := res.Screenshot.Bounds()
		x, y := c.X*b.Dx()/res.Screen.Width, c.Y*b.Dy()/res.Screen.Height
		if !covered(res.Screenshot.At(b.Min.X+x, b.Min.Y+y)) {
			t.Fatalf("%s: not covered at %d,%d: %v", what, x, y, res.Screenshot.At(x, y))
		}
		return res
	}
	res := check("the shade, expanded", hiddenNotification, "Card statement ready", "1,234.56", `"Collapse"`)
	// Collapsed, the row no longer names Shell. Its Collapse button is
	// hidden with it: the test taps it at its place, top right.
	for _, e := range res.Screen.Elements {
		if e.Hidden {
			b := e.Bounds
			adbShell(t, id, "input", "tap", strconv.Itoa(b.X+b.W-74), strconv.Itoa(b.Y+73))
			time.Sleep(1500 * time.Millisecond)
			break
		}
	}
	check("the shade, collapsed", hiddenNotification, "Card statement ready", "1,234.56")
	adbShell(t, id, "cmd", "statusbar", "collapse")

	// Recent apps with Settings in them.
	adbShell(t, id, "am", "start", "-W", "-n", "com.android.settings/.Settings")
	adbShell(t, id, "input", "keyevent", "KEYCODE_APP_SWITCH")
	time.Sleep(1500 * time.Millisecond)
	check("recent apps", hiddenCard, "Search Settings", "Network & internet")
	adbShell(t, id, "input", "keyevent", "KEYCODE_HOME")
}
