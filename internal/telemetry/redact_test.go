package telemetry

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

func TestRedactor(t *testing.T) {
	home, _ := os.UserHomeDir()
	r := NewRedactor()
	r.Phone("3e8be48e", "Mi 10 Pro", "Mi 10 Pro")
	r.Phone("00008110-001E30C9262A801E", "Ada’s iPhone", "iPhone 13 mini")
	in := strings.Join([]string{
		`time=2026-11-07T15:21:25.380+08:00 level=INFO msg="device online" device=3e8be48e name="Mi 10 Pro"`,
		`level=INFO msg="device online" device=00008110-001E30C9262A801E name="Ada’s iPhone"`,
		`level=INFO msg="gateway ready" socket="` + filepath.Join(home, "Library", "Pinky", "pinky.sock") + `"`,
		`level=INFO msg="app refused the background display" device=3e8be48e app="Dinely Bank" err="no"`,
		`level=INFO msg="lease idle" device=3e8be48e task="Pay Alice for dinner"`,
		`connect adb-3e8be48e-kIY2HE._adb-tls-connect._tcp at 192.168.1.23:41233 and [fe80::1c2b:3a4d:5e6f:7a8b]`,
		`remote agents on addr=127.0.0.1:7465 url=http://192.168.1.23:7465/mcp; token 0123456789abcdef0123456789abcdef0123456789abcdef`,
		`mail someone.else+pinky@example.co.uk about Ada’s iPhone; mac 3c:22:fb:01:02:03`,
		`uuid 123e4567-e89b-12d3-a456-426614174000`,
	}, "\n")
	out := r.String(in)
	for _, gone := range []string{"3e8be48e", "001E30C9262A801E", "Ada’s iPhone", home, "Dinely Bank", "Pay Alice",
		"kIY2HE", "192.168.1.23", "fe80::1c2b", "0123456789abcdef0123", "someone.else", "3c:22:fb", "123e4567"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q is still in:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"device=<phone-1>", "device=<phone-2>", `name=<redacted>`, "~", "127.0.0.1",
		"<phone-2 name>", "<email>", "<adb-wifi-phone>", "level=INFO", "gateway ready"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q is missing from:\n%s", kept, out)
		}
	}
	if u, err := user.Current(); err == nil && len(u.Username) >= 3 && strings.Contains(out, u.Username) {
		t.Errorf("the user name %q is still in:\n%s", u.Username, out)
	}
}

func TestRedactorKeepsPinkysModule(t *testing.T) {
	r := &Redactor{phones: map[string]string{}}
	r.add("DIYgod", "<user>", true)
	out := r.String("github.com/DIYgod/pinky/internal/gateway.(*Gateway).Decide\nby DIYgod")
	if out != "github.com/DIYgod/pinky/internal/gateway.(*Gateway).Decide\nby <user>" {
		t.Fatalf("got %q", out)
	}
}

func TestRedactorWholeWords(t *testing.T) {
	r := &Redactor{phones: map[string]string{}}
	r.add("dev", "<user>", true)
	r.Phone("ab12", "Pixel", "Pixel 9")
	out := r.String("device dev online; ab12 and xab12y; Pixel and Pixels")
	if out != "device <user> online; <phone-1> and xab12y; <phone-1 name> and Pixels" {
		t.Fatalf("got %q", out)
	}
}

func TestRedactorLearnsPhonesFromLogs(t *testing.T) {
	r := NewRedactor()
	log := `level=INFO msg="device online" device=FA6C20303860 name=Pixel` + "\n" + `level=WARN msg=x device=emulator-5554 err="emulator-5554 offline"`
	r.Learn(log)
	out := r.String(log)
	if strings.Contains(out, "FA6C20303860") || strings.Contains(out, "emulator-5554") {
		t.Fatalf("phone ids left:\n%s", out)
	}
}

func TestDiagnostics(t *testing.T) {
	dir := t.TempDir()
	var log strings.Builder
	for i := 0; i < logTailLines+50; i++ {
		log.WriteString(`level=INFO msg="device online" device=3e8be48e name="Ada’s Mi"` + "\n")
	}
	log.WriteString(`level=WARN msg="list devices" platform=android err="adb server not running"` + "\n")
	os.WriteFile(filepath.Join(dir, "pinky.log"), []byte(log.String()), 0o600)
	os.WriteFile(filepath.Join(dir, "daemon.log"), []byte("\n\n"), 0o600)

	t.Setenv("DO_NOT_TRACK", "")
	c := Open(Options{Dir: dir, Version: "1.2.3", Usage: true})
	phones := []driver.Info{
		{ID: "3e8be48e", Platform: driver.Android, Name: "Ada’s Mi", Model: "Mi 10 Pro", OSVersion: "17", Connection: "wifi", Backend: "scrcpy", State: driver.StateOnline},
		{ID: "00008110-001E30C9262A801E", Platform: driver.IOS, Name: "Ada’s iPhone", Model: "iPhone 13 mini", OSVersion: "27.1", Connection: "usb", State: driver.StateOnline,
			WiFi: driver.WiFiConnection{Connections: driver.WiFiConnectionsFailed, Detail: "no answer within 8s"}},
	}
	d := c.Diagnostics(phones, "Video: FFmpeg 9.0.2", "Settings: preset 1").String()
	for _, want := range []string{"Pinky 1.2.3 · " + OSName(System().OS), "Usage statistics and crash reports: on", "Phones (2):", "android 17 · Mi 10 Pro · wifi · scrcpy · online",
		"ios 27.1 · iPhone 13 mini · usb · online · wifi connections failed, not seen on wifi (no answer within 8s)", "Video: FFmpeg 9.0.2", "pinky.log, the last lines:", "adb server not running"} {
		if !strings.Contains(d, want) {
			t.Errorf("%q is missing:\n%s", want, d)
		}
	}
	for _, gone := range []string{"3e8be48e", "Ada’s", "001E30C9262A801E", "daemon.log"} {
		if strings.Contains(d, gone) {
			t.Errorf("%q should not be there:\n%s", gone, d)
		}
	}
	if n := strings.Count(d, `msg="device online"`); n > logTailLines {
		t.Errorf("%d log lines, at most %d", n, logTailLines)
	}
	if empty := (*Client)(nil).Diagnostics(nil).String(); !strings.Contains(empty, "none connected") {
		t.Errorf("no phones:\n%s", empty)
	}
}
