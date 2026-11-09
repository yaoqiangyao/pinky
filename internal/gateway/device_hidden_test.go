//go:build device

package gateway

// Checks, on a person's phone, that a blocked app's notification or card is
// hidden from agents on the screen the person leaves it on: the notification
// shade, Notification Center, recent apps, the app switcher, or an app with
// a banner over it. The person makes something with a marker text that only
// the app under test shows (a reminder or a shell notification "Pinky 7731",
// 7731 typed into the Calculator) and sets the screen; the test blocks that
// app for the run only, reads the screen as an agent would, with a
// screenshot, and says whether the marker reached the agent anywhere.
//
// It sends no input and keeps nothing: no history, the screenshot only in
// memory. Its output holds no screen text: counts, yes or no answers, and,
// for an element that still shows the marker, its role, resource id,
// ancestry and place, which say how the screen is built, not what it says.
//
//	go test -c -tags device -o /tmp/pinky-gw/gateway.test ./internal/gateway
//	scripts/devlock.sh <id> env PINKY_DEVICE=<id> PINKY_BLOCK=com.apple.reminders PINKY_MARKER=7731 \
//	  PINKY_WAIT=90 /tmp/pinky-gw/gateway.test -test.run TestDeviceBlockedHidden -test.v
//
// PINKY_BLOCK lists the app ids (or names) to block, separated by commas;
// PINKY_WAIT, in seconds, reads until the marker or a hidden part shows
// (for a banner, which comes and goes), 0 by default.

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android"
	"github.com/DIYgod/pinky/internal/driver/ios"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

func TestDeviceBlockedHidden(t *testing.T) {
	id, marker := os.Getenv("PINKY_DEVICE"), strings.TrimSpace(os.Getenv("PINKY_MARKER"))
	block := strings.Split(os.Getenv("PINKY_BLOCK"), ",")
	if id == "" || marker == "" || strings.TrimSpace(block[0]) == "" {
		t.Skip("set PINKY_DEVICE, PINKY_BLOCK and PINKY_MARKER, and run through scripts/devlock.sh")
	}
	wait, _ := strconv.Atoi(os.Getenv("PINKY_WAIT"))
	pol := policy.Default() // in memory: the blocks last for the run
	for _, b := range block {
		if b = strings.TrimSpace(b); b == "" {
			continue
		}
		app := screen.App{Name: b}
		if strings.Contains(b, ".") && !strings.Contains(b, " ") {
			app = screen.App{ID: b}
		}
		if err := pol.SetAppBlocked(app, true); err != nil {
			t.Fatal(err)
		}
	}
	var inner driver.Provider
	if isIOSID(id) {
		inner = ios.NewProvider()
	} else {
		inner = android.NewProvider(android.Config{})
	}
	// Only the gateway's warnings: its log may name apps.
	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	g, err := New(Options{Providers: []driver.Provider{&oneDevice{Provider: inner, id: id}}, TrustNewAgents: true,
		Policy: pol, Logger: log, NoScreenshots: true})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for deadline := time.Now().Add(60 * time.Second); !deviceOnline(g, id); time.Sleep(500 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not come online", id)
		}
	}
	// The phone's own screen, where the shade and recent apps are.
	if !isIOSID(id) {
		if err := g.SetBackground(id, false); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	s := g.OpenSession(AgentInfo{Name: "Pinky blocked-app check"})
	defer s.Close()
	res := s.Connect(ctx, ConnectRequest{Call: Call{Device: id}, Task: "Check that a blocked app is hidden", ReadOnly: true})
	if res.Status == StatusLocked || screenOf(res).Locked {
		t.Skip("the phone is locked; left as it is")
	}
	if res.IsError() {
		t.Fatalf("connect: %s %s", res.Error, res.Message)
	}

	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
	m := norm(marker)
	leaks := func(res *Result) (els []screen.Element, events int, hidden, unplaced int) {
		for _, e := range screenOf(res).Elements {
			if e.Hidden {
				hidden++
				if e.Bounds.Empty() {
					unplaced++
				}
			}
			if strings.Contains(norm(e.Label), m) {
				els = append(els, e)
			}
		}
		for _, ev := range screenOf(res).Events {
			if strings.Contains(norm(ev.Text), m) {
				events++
			}
		}
		return
	}
	// Wait for the banner, if asked.
	for deadline := time.Now().Add(time.Duration(wait) * time.Second); time.Now().Before(deadline); {
		r := s.Observe(ctx, ObserveRequest{Call: Call{Device: id}})
		els, evs, hidden, _ := leaks(r)
		if len(els) > 0 || evs > 0 || hidden > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	res = s.Observe(ctx, ObserveRequest{Call: Call{Device: id}, Screenshot: true})
	if res.IsError() {
		t.Fatalf("observe: %s", res.Error)
	}
	sc := screenOf(res)
	front := "another app"
	if pol.IsSystem(sc.App) || sc.App.ID == "" {
		front = sc.App.ID
	}
	if _, blocked := pol.Blocked(sc.App); blocked {
		front = "the blocked app itself (nothing to check: its screen is hidden whole)"
	}
	els, evs, hidden, unplaced := leaks(res)
	t.Logf("screen: %s, %d elements, %d hidden (%d without a place)", front, len(sc.Elements), hidden, unplaced)
	t.Logf("marker in the element list: %v (%d elements); in events: %v", len(els) > 0, len(els), evs > 0)
	for _, e := range els {
		area := 0.0
		if sc.Width > 0 && sc.Height > 0 {
			area = 100 * float64(e.Bounds.W*e.Bounds.H) / float64(sc.Width*sc.Height)
		}
		t.Logf("  showing the marker: %s %s id=%q path=%q at %+v (%.1f%% of the screen)", e.Source, e.Role, e.ID, e.Path, e.Bounds, area)
	}
	pictured := false
	switch {
	case res.Screenshot == nil:
		t.Logf("screenshot: withheld (%s)", res.Message)
	default:
		t.Logf("screenshot: given, covered boxes said: %v", strings.Contains(res.Message, "blocked apps are covered"))
		lines, err := readPictureHere(res.Screenshot)
		switch {
		case err != nil:
			t.Logf("this computer cannot read the screenshot's text: %v", err)
		default:
			for _, l := range lines {
				if strings.Contains(norm(l.Text), m) {
					pictured = true
				}
			}
			t.Logf("marker readable in the screenshot: %v", pictured)
		}
	}
	res.Screenshot = nil
	switch {
	case len(els) > 0 || evs > 0 || pictured:
		t.Errorf("FAIL: the marker reached the agent")
	case hidden == 0:
		t.Errorf("NOT CHECKED: nothing was hidden and the marker is nowhere: is the blocked app's notification or card on the screen?")
	default:
		t.Logf("PASS: hidden, and the marker reached the agent nowhere")
	}
}
