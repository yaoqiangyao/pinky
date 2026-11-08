//go:build e2e && darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/update"
)

// TestUpdateEndToEnd updates a real Pinky.app against a local feed: it
// builds two versions of the e2e app (MyGo's automation socket), signs
// them with the Developer ID in PINKY_E2E_SIGN (Pinky's team, 492J8Q67PF,
// not notarized), installs the older one in a temporary Applications
// folder and serves the newer one's zip. The older one, started in the
// menu bar, finds the update, installs it by itself as nothing happens,
// and the newer one starts in its place, drops the old bundle and shows
// What's new. Everything stays in a temporary folder; nothing touches
// /Applications or a running Pinky.
//
//	PINKY_E2E_SIGN="Developer ID Application: NATURAL SELECTION LABS PTE. LTD. (492J8Q67PF)" \
//	  go test -tags e2e -run TestUpdateEndToEnd -v -timeout 10m ./cmd/pinky/
func TestUpdateEndToEnd(t *testing.T) {
	sign := os.Getenv("PINKY_E2E_SIGN")
	if sign == "" {
		t.Skip("PINKY_E2E_SIGN (a Developer ID of Pinky's team) not set")
	}
	tmp, err := os.MkdirTemp("/tmp", "pku-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	home := filepath.Join(tmp, "h")
	apps := filepath.Join(tmp, "Applications")
	installed := filepath.Join(apps, "Pinky.app")
	const oldV, newV = "2026.1107.1", "2026.1107.2"

	buildBundle(t, installed, oldV, sign)
	newApp := filepath.Join(tmp, "new", "Pinky.app")
	buildBundle(t, newApp, newV, sign)
	zip := filepath.Join(tmp, "srv", "Pinky-"+newV+"-arm64.zip")
	os.MkdirAll(filepath.Dir(zip), 0o755)
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", newApp, zip).CombinedOutput(); err != nil {
		t.Fatalf("zip: %v: %s", err, out)
	}
	data, err := os.ReadFile(zip)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			fmt.Fprintf(w, `{"version": %q, "tag": "v%s", "date": %q, "notes": "- Updates itself", "assets": {%q: {"url": %q, "sha256": %q, "size": %d}}}`,
				newV, newV, time.Now().UTC().Format(time.RFC3339), update.Platform(), srv.URL+"/download/"+filepath.Base(zip), hex.EncodeToString(sum[:]), len(data))
		case "/download/" + filepath.Base(zip):
			w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	sock := filepath.Join(tmp, "ui.sock")
	t.Setenv(api.HomeEnv, home)
	dirs, err := api.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(installed, "Contents", "MacOS", "Pinky"), "--fake", "--background")
	cmd.Env = append(os.Environ(), api.HomeEnv+"="+home, update.FeedEnv+"="+srv.URL+"/latest.json", "MYGO_AUTOMATION="+sock)
	logf, _ := os.Create(filepath.Join(tmp, "old.log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		quitPinky(dirs)
		cmd.Process.Kill()
		if t.Failed() {
			for _, name := range []string{"old.log", filepath.Join("h", "data", "pinky.log"), filepath.Join("h", "data", "update.log")} {
				if b, err := os.ReadFile(filepath.Join(tmp, name)); err == nil {
					t.Logf("%s:\n%s", name, tail(string(b), 4000))
				}
			}
		}
	})
	waitFor(t, "the old version's gateway", 30*time.Second, func() bool { return gatewayVersion(dirs) == oldV })

	// Nothing happens in Pinky: within the first minute it finds the
	// update and installs it, and exits for the new version.
	select {
	case <-exited:
	case <-time.After(3 * time.Minute):
		t.Fatal("the old version did not update itself within three minutes")
	}
	waitFor(t, "the new version's gateway", 60*time.Second, func() bool { return gatewayVersion(dirs) == newV })
	if v := plistVersion(t, installed); v != newV {
		t.Errorf("installed %s", v)
	}
	waitFor(t, "the old bundle to go", 30*time.Second, func() bool {
		left, _ := filepath.Glob(filepath.Join(apps, ".Pinky.app.update-*"))
		return len(left) == 0
	})
	st := update.LoadState(filepath.Join(dirs.Config, "update.json"))
	if st.LastRun != newV || st.NotesFrom != oldV {
		t.Errorf("update.json: %+v", st)
	}

	// The new version's window has What's new.
	c := dialAutomation(t, sock)
	defer c.conn.Close()
	labels := waitLabels(t, c, 30*time.Second, newsTexts(newV))
	t.Logf("the new version shows: %s", strings.Join(labels[:min(len(labels), 12)], " · "))
}

// buildBundle makes a Pinky.app of version at app, as mygo build does, with
// the e2e build of cmd/pinky, and signs it.
func buildBundle(t *testing.T, app, version, sign string) {
	t.Helper()
	exe := filepath.Join(app, "Contents", "MacOS", "Pinky")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	ldflags := strings.Join([]string{
		"-X main.version=" + version,
		"-X github.com/egoist/mygo.production=1",
		"-X github.com/egoist/mygo.packageName=Pinky",
		"-X github.com/egoist/mygo.packageVersion=" + version,
		"-X github.com/egoist/mygo.packageIdentifier=com.flyfox.pinky",
	}, " ")
	b := exec.Command("go", "build", "-tags", "e2e", "-ldflags", ldflags, "-o", exe, ".")
	b.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, out)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Pinky</string>
<key>CFBundleDisplayName</key><string>Pinky</string>
<key>CFBundleIdentifier</key><string>com.flyfox.pinky</string>
<key>CFBundleExecutable</key><string>Pinky</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
<key>CFBundleVersion</key><string>` + version + `</string>
<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>NSPrincipalClass</key><string>NSApplication</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/codesign", "--force", "--deep", "--timestamp=none", "--sign", sign, app).CombinedOutput(); err != nil {
		t.Fatalf("sign %s: %v\n%s", version, err, out)
	}
}

func plistVersion(t *testing.T, app string) string {
	t.Helper()
	out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print CFBundleShortVersionString", filepath.Join(app, "Contents", "Info.plist")).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
