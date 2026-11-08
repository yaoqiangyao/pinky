package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2026.1107.1", "2026.1107.1", 0},
		{"2026.1107.1", "2026.1107.2", -1},
		{"2026.1107.10", "2026.1107.9", 1}, // numbers, not text
		{"2026.1107", "2026.1107.0", 0},
		{"2026.1107.1", "2026.1107", 1},
		{"2026.1201.1", "2026.1107.3", 1},
		{"2027.101.1", "2026.1231.9", 1},
		{"v2026.1107.1", "2026.1107.1", 0},
		{"0.1.0", "2026.1107.1", -1},
		{"0.0.0-dev", "0.1.0", -1},
		{"0.0.0-dev", "garbage", 0},
		{"1.2.3", "", 1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestIsReleaseAndNewer(t *testing.T) {
	for v, want := range map[string]bool{
		"2026.1107.1": true, "0.1.0": true, "v1": true,
		DevVersion: false, "0.0.0": false, "": false, "1..2": false, "1.2.x": false, "1.2-beta": false, " 1.2 ": true,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v", v, got)
		}
	}
	if !Newer("2026.1107.2", "2026.1107.1") || Newer("2026.1107.1", "2026.1107.1") || Newer("2026.1107.0", "2026.1107.1") {
		t.Error("Newer compares releases wrong")
	}
	if Newer("2026.1107.2", DevVersion) {
		t.Error("a development build sees an update")
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

const feedJSON = `{
  "version": "2026.1107.2",
  "tag": "v2026.1107.2",
  "date": "2026-11-07T10:00:00Z",
  "notes": "- Faster mirroring\n- Pinky updates itself",
  "assets": {
    "darwin-arm64": {"url": "https://github.com/DIYgod/pinky/releases/download/v2026.1107.2/pinky-2026.1107.2-darwin-arm64.zip", "sha256": "%s", "size": 42},
    "darwin-amd64": {"url": "http://example.com/Pinky.zip", "sha256": "%s", "size": 42},
    "windows-amd64": {"url": "https://example.com/Pinky-Setup.exe", "sha256": "abc", "size": 42},
    "linux-amd64": {"url": "https://example.com/pinky.tar.gz", "sha256": "%s", "size": 0}
  }
}`

func TestParseFeed(t *testing.T) {
	s := strings.Repeat("ab", 32)
	f, err := ParseFeed([]byte(fmt.Sprintf(feedJSON, strings.ToUpper(s), s, s)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "2026.1107.2" || f.Tag != "v2026.1107.2" || !strings.Contains(f.Notes, "Faster") {
		t.Errorf("feed %+v", f)
	}
	if !f.Date.Equal(time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("date %v", f.Date)
	}
	a, ok := f.Asset("darwin-arm64")
	if !ok || a.SHA256 != s || a.Size != 42 {
		t.Errorf("darwin-arm64: %+v %v", a, ok)
	}
	// Plain HTTP to another host, a bad checksum and a zero size are left out.
	for _, p := range []string{"darwin-amd64", "windows-amd64", "linux-amd64"} {
		if _, ok := f.Asset(p); ok {
			t.Errorf("%s should be left out", p)
		}
	}
	for _, bad := range []string{
		`{"version": "0.0.0-dev", "assets": {}}`,
		`{"version": "next", "assets": {}}`,
		`{"version": 3}`,
		`not json`,
	} {
		if _, err := ParseFeed([]byte(bad)); err == nil {
			t.Errorf("ParseFeed(%s) did not fail", bad)
		}
	}
}

func TestFeedURL(t *testing.T) {
	t.Setenv(FeedEnv, "")
	if got := FeedURL(); got != "https://github.com/yaoqiangyao/pinky/releases/latest/download/latest.json" {
		t.Errorf("FeedURL = %s", got)
	}
	if got := ReleasePage("v2026.1107.2"); got != "https://github.com/yaoqiangyao/pinky/releases/tag/v2026.1107.2" {
		t.Errorf("ReleasePage = %s", got)
	}
	t.Setenv(FeedEnv, "http://127.0.0.1:9/latest.json")
	if FeedURL() != "http://127.0.0.1:9/latest.json" || ReleasePage("") != "" {
		t.Error("the environment does not override the feed")
	}
}

// feedServer serves latest.json and one asset.
func feedServer(t *testing.T, version string, asset []byte) (*httptest.Server, *int) {
	t.Helper()
	hits := new(int)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if !strings.HasPrefix(r.UserAgent(), "Pinky/") {
			t.Errorf("User-Agent %q", r.UserAgent())
		}
		fmt.Fprintf(w, `{"version": %q, "tag": "v%s", "notes": "- New", "assets": {%q: {"url": %q, "sha256": %q, "size": %d}}}`,
			version, version, Platform(), srv.URL+"/download/v"+version+"/Pinky.zip", sum(asset), len(asset))
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) { w.Write(asset) })
	mux.HandleFunc("/redirect/latest.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/latest.json", http.StatusFound)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, hits
}

func TestCheck(t *testing.T) {
	asset := []byte("a zip of Pinky.app")
	srv, hits := feedServer(t, "2026.1107.2", asset)
	u := &Updater{Version: "2026.1107.1", Feed: srv.URL + "/redirect/latest.json"}
	r, err := u.Check(context.Background())
	if err != nil || r == nil {
		t.Fatalf("Check = %v, %v", r, err)
	}
	if r.Version != "2026.1107.2" || r.Asset.Size != int64(len(asset)) {
		t.Errorf("release %+v", r)
	}
	if st := u.State(); st.Latest != "2026.1107.2" || st.Checked.IsZero() {
		t.Errorf("state %+v", st)
	}

	u.Version = "2026.1107.2"
	if r, err := u.Check(context.Background()); r != nil || err != nil {
		t.Errorf("the same version: %v, %v", r, err)
	}
	u.Version = "2026.1108.1"
	if r, err := u.Check(context.Background()); r != nil || err != nil {
		t.Errorf("a newer running version: %v, %v", r, err)
	}
	before := *hits
	u.Version = DevVersion
	if _, err := u.Check(context.Background()); !errors.Is(err, ErrDevelopment) {
		t.Errorf("a development build: %v", err)
	}
	if *hits != before {
		t.Error("a development build asked the feed")
	}
	u.Version, u.Platform = "2026.1107.1", "plan9-mips"
	if _, err := u.Check(context.Background()); !errors.Is(err, ErrNoAsset) {
		t.Errorf("no asset for this computer: %v", err)
	}
}

func TestCheckFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing.json":
			http.NotFound(w, r)
		case "/huge.json":
			w.Write([]byte(`{"version": "9.9.9", "notes": "`))
			w.Write([]byte(strings.Repeat("x", maxFeedSize)))
		case "/https.json":
			http.Redirect(w, r, "http://example.com/latest.json", http.StatusFound)
		}
	}))
	defer srv.Close()
	for _, path := range []string{"/missing.json", "/huge.json", "/https.json"} {
		u := &Updater{Version: "1.0.0", Feed: srv.URL + path}
		if _, err := u.Check(context.Background()); err == nil {
			t.Errorf("%s: no error", path)
		}
	}
	u := &Updater{Version: "1.0.0", Feed: "http://example.com/latest.json"}
	if _, err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("plain HTTP to another host: %v", err)
	}
}

func TestDownload(t *testing.T) {
	asset := []byte(strings.Repeat("pinky", 50000))
	srv, _ := feedServer(t, "2026.1107.2", asset)
	dir := t.TempDir()
	a := Asset{URL: srv.URL + "/download/v2026.1107.2/Pinky.zip", SHA256: sum(asset), Size: int64(len(asset))}
	var last, calls int64
	file, err := Download(context.Background(), NewClient(), a, dir, "1.0.0", func(done, total int64) {
		calls++
		if done < last || total != a.Size {
			t.Errorf("progress %d/%d after %d", done, total, last)
		}
		last = done
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(file) != "Pinky.zip" || last != a.Size || calls == 0 {
		t.Errorf("file %s, progress %d (%d calls)", file, last, calls)
	}
	if err := VerifyFile(file, a); err != nil {
		t.Errorf("VerifyFile: %v", err)
	}

	bad := a
	bad.SHA256 = sum([]byte("something else"))
	bad.URL = srv.URL + "/download/v2026.1107.2/Other.zip"
	if _, err := Download(context.Background(), NewClient(), bad, dir, "1.0.0", nil); !errors.Is(err, ErrChecksum) {
		t.Errorf("a wrong checksum: %v", err)
	}
	short := a
	short.Size--
	short.URL = srv.URL + "/download/v2026.1107.2/Short.zip"
	if _, err := Download(context.Background(), NewClient(), short, dir, "1.0.0", nil); err == nil {
		t.Error("a wrong size passed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("failed downloads left files: %v", names)
	}
}

func TestAssetName(t *testing.T) {
	for in, want := range map[string]string{
		"https://x/releases/download/v1/Pinky-1-arm64.zip": "Pinky-1-arm64.zip",
		"https://x/":                    "pinky-update",
		"https://x/.hidden":             "pinky-update",
		"https://x/a%2F..%2F..%2Fevil":  "evil",
		"https://x/dl?name=../../x.zip": "dl",
	} {
		if got := assetName(in); got != want {
			t.Errorf("assetName(%q) = %q, want %q", in, got, want)
		}
	}
}

const changelog = `# Changelog

All notable changes.

## Unreleased

- Not out yet

## 2026.1201.1 (2026-12-01)

- Pinky updates the companion app by itself

## 2026.1107.2 (2026-11-07)

- Faster mirroring
- Fixed: **Mi 10 Pro** unlocks

## [2026.1107.1] - 2026-11-07

- First release
`

func TestChangelog(t *testing.T) {
	es := ParseChangelog(changelog)
	if len(es) != 3 {
		t.Fatalf("entries: %+v", es)
	}
	if es[0].Version != "2026.1201.1" || es[0].Date.Format("2006-01-02") != "2026-12-01" || es[0].Body != "- Pinky updates the companion app by itself" {
		t.Errorf("first: %+v", es[0])
	}
	if es[2].Version != "2026.1107.1" || es[2].Date.IsZero() {
		t.Errorf("Keep a Changelog heading: %+v", es[2])
	}
	got := Since(es, "2026.1107.1", "2026.1201.1")
	if len(got) != 2 || got[0].Version != "2026.1201.1" || got[1].Version != "2026.1107.2" {
		t.Errorf("Since: %+v", got)
	}
	if got := Since(es, "2026.1107.2", "2026.1107.2"); len(got) != 0 {
		t.Errorf("nothing new: %+v", got)
	}
	if got := Since(es, "", "2026.1107.2"); len(got) != 1 || got[0].Version != "2026.1107.2" {
		t.Errorf("only this version: %+v", got)
	}
	md := Markdown(Since(es, "2026.1107.1", "2026.1107.2"), nil)
	if md != "### 2026.1107.2 · November 7, 2026\n\n- Faster mirroring\n- Fixed: **Mi 10 Pro** unlocks" {
		t.Errorf("Markdown:\n%s", md)
	}
}

func TestTranslate(t *testing.T) {
	es := ParseChangelog(changelog)
	zh := ParseChangelog("# 更新日志\n\n## 2026.1201.1 (2026-12-01)\n\n- Pinky 会自己更新手机上的 companion 应用\n\n## 2026.1107.2 (2026-11-07)\n\n")
	got := Translate(Since(es, "2026.1107.1", "2026.1201.1"), zh)
	if len(got) != 2 || got[0].Body != "- Pinky 会自己更新手机上的 companion 应用" || got[1].Body != es[1].Body || !got[0].Date.Equal(es[0].Date) {
		t.Errorf("Translate: %+v", got)
	}
	if es[0].Body != "- Pinky updates the companion app by itself" {
		t.Errorf("Translate changed the entries: %+v", es[0])
	}
	md := Markdown(got[:1], func(d time.Time) string { return d.Format("2006 年 1 月 2 日") })
	if md != "### 2026.1201.1 · 2026 年 12 月 1 日\n\n- Pinky 会自己更新手机上的 companion 应用" {
		t.Errorf("Markdown:\n%s", md)
	}
}

func TestNotesIn(t *testing.T) {
	s := strings.Repeat("ab", 32)
	data := strings.Replace(fmt.Sprintf(feedJSON, s, s, s), `"assets"`, `"localized_notes": {"zh-Hans": "- 镜像更快了", "fr": " "},
  "assets"`, 1)
	f, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	for lang, want := range map[string]string{
		"zh-Hans":    "- 镜像更快了",
		"zh-Hans-CN": "- 镜像更快了",
		"en":         f.Notes,
		"":           f.Notes,
		"zh-Hant":    f.Notes, // another script
		"fr":         f.Notes, // empty notes
	} {
		if got := f.NotesIn(lang); got != want {
			t.Errorf("NotesIn(%q) = %q, want %q", lang, got, want)
		}
	}
	// A feed of a release without translations, or of an older one.
	f.LocalizedNotes = nil
	if got := f.NotesIn("zh-Hans"); got != f.Notes {
		t.Errorf("NotesIn without translations = %q", got)
	}
}

func TestStateLaunched(t *testing.T) {
	var s State
	s.Launched("2026.1107.1")
	if s.LastRun != "2026.1107.1" || s.NotesFrom != "" {
		t.Errorf("a first run shows notes: %+v", s)
	}
	s.Launched("2026.1107.2")
	if s.NotesFrom != "2026.1107.1" {
		t.Errorf("an update: %+v", s)
	}
	s.Launched("2026.1201.1") // not dismissed yet: notes keep the older start
	if s.NotesFrom != "2026.1107.1" || s.LastRun != "2026.1201.1" {
		t.Errorf("a second update: %+v", s)
	}
	s.NotesFrom = ""
	s.Launched(DevVersion)
	if s.LastRun != "2026.1201.1" {
		t.Errorf("a development build changed the state: %+v", s)
	}
	s.Failed = "2026.1201.2"
	s.Launched("2026.1107.2") // went back
	if s.NotesFrom != "" || s.LastRun != "2026.1107.2" || s.Failed != "2026.1201.2" {
		t.Errorf("going back: %+v", s)
	}
	s.Launched("2026.1201.2") // installed by hand after all
	if s.Failed != "" {
		t.Errorf("a failed version that runs now: %+v", s)
	}

	path := filepath.Join(t.TempDir(), "sub", "update.json")
	s.Changes = []Change{{Kind: "companion", Device: "Mi 10 Pro", From: "0.1.0 (68)", To: "0.1.0 (75)", At: time.Now()}}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(path); got.LastRun != s.LastRun || len(got.Changes) != 1 || got.Changes[0].Device != "Mi 10 Pro" {
		t.Errorf("saved %+v, loaded %+v", s, got)
	}
	os.WriteFile(path, []byte("{broken"), 0o600)
	if got := LoadState(path); got.LastRun != "" {
		t.Errorf("a broken file: %+v", got)
	}
}

func TestSnoozed(t *testing.T) {
	now := time.Now()
	s := State{Later: "2026.1107.2", LaterUntil: now.Add(time.Hour)}
	if !s.Snoozed("2026.1107.2", now) || s.Snoozed("2026.1107.3", now) || s.Snoozed("2026.1107.2", now.Add(2*time.Hour)) {
		t.Error("Snoozed")
	}
}

func TestIdle(t *testing.T) {
	now := time.Now()
	long := now.Add(-time.Hour)
	for _, c := range []struct {
		name string
		a    Activity
		want bool
	}{
		{"nothing", Activity{}, true},
		{"long quiet", Activity{LastInput: long, LastTask: long}, true},
		{"a task", Activity{Tasks: 1, LastInput: long}, false},
		{"an approval", Activity{Needs: 1}, false},
		{"unlocking", Activity{Busy: []string{"unlocking Pixel 9"}}, false},
		{"the person just clicked", Activity{LastInput: now.Add(-time.Minute)}, false},
		{"an agent just finished", Activity{LastTask: now.Add(-2 * time.Minute)}, false},
		{"just past quiet", Activity{LastInput: now.Add(-QuietFor - time.Second)}, true},
	} {
		got, why := Idle(now, c.a)
		if got != c.want || (!got && why == "") {
			t.Errorf("%s: Idle = %v (%q)", c.name, got, why)
		}
	}
}

func TestDownloadStalls(t *testing.T) {
	defer func(d time.Duration) { StallTimeout = d }(StallTimeout)
	StallTimeout = 300 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("only the start"))
		w.(http.Flusher).Flush()
		select { // then nothing, as a proxy whose far side hangs
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	a := Asset{URL: srv.URL + "/Pinky.zip", SHA256: sum([]byte("x")), Size: 1000}
	start := time.Now()
	_, err := Download(context.Background(), NewClient(), a, t.TempDir(), "1.0.0", nil)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("Download = %v, want ErrStalled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to notice", d)
	}
}

// BundleID names Pinky's entry in Settings › Apps, which the installer
// makes from cmd/pinky/mygo.json's identifier: they must agree, or a
// Windows update cannot find the installed Pinky.
func TestBundleIDIsTheAppsIdentifier(t *testing.T) {
	b, err := os.ReadFile("../../cmd/pinky/mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var app struct{ Identifier string }
	if err := json.Unmarshal(b, &app); err != nil {
		t.Fatal(err)
	}
	if app.Identifier != BundleID {
		t.Errorf("mygo.json's identifier is %q, BundleID %q", app.Identifier, BundleID)
	}
}
