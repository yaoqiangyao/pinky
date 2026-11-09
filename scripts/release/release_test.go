package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	pinky "github.com/DIYgod/pinky"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Version
	}{
		{"2026.1107.1", Version{2026, 1107, 1}},
		{"v2026.1107.12", Version{2026, 1107, 12}},
		{"2027.105.1", Version{2027, 105, 1}},
		{"2028.229.3", Version{2028, 229, 3}}, // a leap day
		{"2026.1231.65535", Version{2026, 1231, 65535}},
	} {
		got, err := ParseVersion(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{
		"", "0.1.0", "1.2.3", "2026.1107", "2026.1107.0", "2026.1107.01", "2026.01107.1",
		"2026.0105.1", "2026.1301.1", "2026.1100.1", "2026.1132.1", "2027.229.1",
		"2026.1107.65536", "2026.1107.1-beta", "26.1107.1", "vv2026.1107.1", "2026.1107.1 ",
	} {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", in, v)
		}
	}
}

func TestVersionStrings(t *testing.T) {
	v := Version{2027, 105, 2}
	if v.String() != "2027.105.2" || v.Tag() != "v2027.105.2" || v.Date() != "2027-01-05" {
		t.Errorf("got %s, %s, %s", v, v.Tag(), v.Date())
	}
}

func TestCompare(t *testing.T) {
	ordered := []string{"2026.930.3", "2026.1001.1", "2026.1107.2", "2026.1107.9", "2026.1107.10", "2026.1231.1", "2027.105.1"}
	for i := range ordered {
		for j := range ordered {
			a, _ := ParseVersion(ordered[i])
			b, _ := ParseVersion(ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestNext(t *testing.T) {
	// 16:30 UTC on 6 November is half past midnight on 7 November in
	// Asia/Shanghai.
	now := time.Date(2026, 11, 6, 16, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		tags []string
		want string
	}{
		{nil, "2026.1107.1"},
		{[]string{"v2026.1106.4", "v2026.1105.1"}, "2026.1107.1"},
		{[]string{"v2026.1107.1"}, "2026.1107.2"},
		{[]string{"v2026.1107.2", "v2026.1107.10", "v2026.1107.9"}, "2026.1107.11"},
		{[]string{"2026.1107.5", "v2026.1107.x", "release-2026.1107.7", "v0.1.0", "v2026.1107.1"}, "2026.1107.2"},
	} {
		got, err := Next(now, tc.tags)
		if err != nil || got.String() != tc.want {
			t.Errorf("Next(%v) = %v, %v; want %s", tc.tags, got, err, tc.want)
		}
	}
	if got, err := Next(time.Date(2027, 1, 5, 3, 0, 0, 0, time.UTC), []string{"v2026.1231.3"}); err != nil || got.String() != "2027.105.1" {
		t.Errorf("Next on 5 January = %v, %v; want 2027.105.1", got, err)
	}
	if _, err := Next(now, []string{"v2026.1108.1"}); err == nil {
		t.Error("Next with a tag of tomorrow: no error")
	}
}

const (
	changelogHead   = "# Changelog\n\nWhat changed in each release.\n\n"
	translationHead = "# 更新日志\n\n"
)

func TestStamp(t *testing.T) {
	v := Version{2026, 1107, 2}
	none := func(string) bool { return false }
	released := "## 2026.1107.1 (2026-11-07)\n\n- First.\n"

	got, notes, err := Stamp(changelogHead+"## Unreleased\n\n- Second.\n- Also.\n\n"+released, v, none)
	want := changelogHead + "## 2026.1107.2 (2026-11-07)\n\n- Second.\n- Also.\n\n" + released
	if err != nil || got != want || notes != "- Second.\n- Also." {
		t.Errorf("Stamp(Unreleased) = %q, %q, %v", got, notes, err)
	}

	// A version heading without a tag yet (another release took it, or
	// its tag was never pushed) gets the next version.
	got, _, err = Stamp(changelogHead+"## 2026.1106.3 (2026-11-06)\n\n- Late.\n\n"+released, v, func(s string) bool { return s == "2026.1107.1" })
	if err != nil || !strings.Contains(got, "## 2026.1107.2 (2026-11-07)\n\n- Late.") || strings.Contains(got, "2026.1106.3") {
		t.Errorf("Stamp(untagged version) = %q, %v", got, err)
	}

	// Windows line breaks stay.
	got, _, err = Stamp("## unreleased\r\n\r\n- Crlf.\r\n", v, none)
	if err != nil || got != "## 2026.1107.2 (2026-11-07)\r\n\r\n- Crlf.\r\n" {
		t.Errorf("Stamp(CRLF) = %q, %v", got, err)
	}

	for name, text := range map[string]string{
		"empty":            changelogHead,
		"released":         changelogHead + released,
		"no notes":         changelogHead + "## Unreleased\n\n" + released,
		"unreleased below": changelogHead + "## Unreleased\n\n- A.\n\n## 2026.1106.1 (2026-11-06)\n\n- B.\n\n## Unreleased\n\n- C.\n",
		"duplicate":        changelogHead + "## Unreleased\n\n- A.\n\n## 2026.1107.2 (2026-11-07)\n\n- B.\n",
		"not a version":    changelogHead + "## Next\n\n- A.\n",
	} {
		tagged := func(s string) bool { return s == "2026.1107.1" }
		if got, _, err := Stamp(text, v, tagged); err == nil {
			t.Errorf("Stamp(%s) = %q, want an error", name, got)
		}
	}
	if _, _, err := Stamp(changelogHead+released, v, func(string) bool { return true }); !errors.Is(err, errNoRelease) {
		t.Errorf("Stamp(nothing new) = %v, want errNoRelease", err)
	}
}

func TestStampTranslation(t *testing.T) {
	v := Version{2026, 1107, 2}
	none := func(string) bool { return false }
	const file = "CHANGELOG.zh-CN.md"
	head := "# 更新日志\n\n"
	released := "## 2026.1107.1 (2026-11-07)\n\n- 第一条。\n"
	got, notes, err := StampTranslation(file, head+"## Unreleased\n\n- 第二条。\n- 还有。\n\n"+released, v, "- Second.\n- Also.", none)
	if err != nil || got != head+"## 2026.1107.2 (2026-11-07)\n\n- 第二条。\n- 还有。\n\n"+released || notes != "- 第二条。\n- 还有。" {
		t.Errorf("StampTranslation = %q, %q, %v", got, notes, err)
	}
	for name, text := range map[string]string{
		"no Unreleased":  head + released,
		"no notes":       head + "## Unreleased\n\n" + released,
		"a note missing": head + "## Unreleased\n\n- 第二条。\n\n" + released,
		"a note more":    head + "## Unreleased\n\n- 第二条。\n- 还有。\n- 多的。\n\n" + released,
	} {
		tagged := func(s string) bool { return s == "2026.1107.1" }
		_, _, err := StampTranslation(file, text, v, "- Second.\n- Also.", tagged)
		if err == nil || !strings.Contains(err.Error(), file) {
			t.Errorf("StampTranslation(%s) = %v, want an error naming %s", name, err, file)
		}
	}
}

// TestChangelogTranslations: each translation of the repository's
// changelog has CHANGELOG.md's headings, in its order, and a bullet for
// each of CHANGELOG.md's under each (a version it lacks shows in English).
func TestChangelogTranslations(t *testing.T) {
	en, err := os.ReadFile(filepath.Join("..", "..", changelogFile))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]section{}
	var order []string
	enLines := splitLines(string(en))
	for _, s := range sections(string(en)) {
		want[s.version] = s
		order = append(order, s.version)
	}
	for _, tr := range translations {
		text, err := os.ReadFile(filepath.Join("..", "..", tr.file))
		if err != nil {
			t.Fatal(err)
		}
		// The app shows it in its catalog's language.
		if _, err := os.Stat(filepath.Join("..", "..", "cmd", "pinky", "locales", tr.lang+".json")); err != nil {
			t.Errorf("%s is in %s, which the app's catalogs lack: %v", tr.file, tr.lang, err)
		}
		if pinky.ChangelogTranslations[tr.lang] != string(text) {
			t.Errorf("the app does not embed %s as its changelog in %s", tr.file, tr.lang)
		}
		lines := splitLines(string(text))
		last := -1
		for _, s := range sections(string(text)) {
			w, ok := want[s.version]
			if !ok {
				t.Errorf("%s has a section %q that %s lacks", tr.file, lines[s.line], changelogFile)
				continue
			}
			if lines[s.line] != enLines[w.line] {
				t.Errorf("%s's heading %q is %q in %s", tr.file, lines[s.line], enLines[w.line], changelogFile)
			}
			if i := slices.Index(order, s.version); i < last {
				t.Errorf("%s has %s out of %s's order", tr.file, s.version, changelogFile)
			} else {
				last = i
			}
			if got, n := bullets(s.notes), bullets(w.notes); got != n {
				t.Errorf("%s has %d notes for %s, %s %d", tr.file, got, s.version, changelogFile, n)
			}
		}
	}
}

func TestNotes(t *testing.T) {
	text := changelogHead + "## Unreleased\n\n- Next.\n\n## 2026.1107.2 (2026-11-07)\n\n- Two.\n\n  ```sh\n  ## not a heading\n  ```\n\n### Details\n\nMore.\n\n## v2026.1107.1 (2026-11-07)\n\n- One.\n"
	for version, want := range map[string]string{
		"2026.1107.2":  "- Two.\n\n  ```sh\n  ## not a heading\n  ```\n\n### Details\n\nMore.",
		"v2026.1107.1": "- One.",
		"Unreleased":   "- Next.",
	} {
		if got, err := Notes(text, version); err != nil || got != want {
			t.Errorf("Notes(%s) = %q, %v; want %q", version, got, err, want)
		}
	}
	if _, err := Notes(text, "2026.1106.1"); err == nil {
		t.Error("Notes of a version without a section: no error")
	}
	if _, err := Notes("## 2026.1107.1 (2026-11-07)\n\n", "2026.1107.1"); err == nil {
		t.Error("Notes of an empty section: no error")
	}
}

func TestStampConfig(t *testing.T) {
	in := "{\n  \"name\": \"Pinky\",\n  \"version\": \"0.1.0\",\n  \"macos\": {\n    \"minimumSystemVersion\": \"13.0\"\n  }\n}\n"
	got, err := StampConfig(in, "2026.1107.1")
	if want := strings.Replace(in, "0.1.0", "2026.1107.1", 1); err != nil || got != want {
		t.Errorf("StampConfig = %q, %v; want %q", got, err, want)
	}
	if _, err := StampConfig(`{"name": "Pinky"}`, "2026.1107.1"); err == nil {
		t.Error("StampConfig without a version: no error")
	}
}

// writeArchives writes stand-ins for a release's archives in dir.
func writeArchives(t *testing.T, dir, version string) {
	t.Helper()
	for _, a := range archives {
		if err := os.WriteFile(filepath.Join(dir, archiveName(a.platform, version)), []byte(a.platform), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManifestAndCask(t *testing.T) {
	dir := t.TempDir()
	writeArchives(t, dir, "2026.1107.1")
	if err := os.WriteFile(filepath.Join(dir, "pinky-2026.1107.1-arm64.dmg"), []byte("dmg"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 7, 8, 9, 10, 0, time.UTC)
	m, _, err := BuildManifest(dir, "2026.1107.1", "DIYgod/pinky", "- One.", now, false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2026.1107.1" || m.Tag != "v2026.1107.1" || m.Date != "2026-11-07T08:09:10Z" || m.Notes != "- One." || len(m.Assets) != 4 {
		t.Errorf("manifest = %+v", m)
	}
	// SHA-256 of "darwin-arm64".
	arm := m.Assets["darwin-arm64"]
	if arm.URL != "https://github.com/DIYgod/pinky/releases/download/v2026.1107.1/pinky-2026.1107.1-darwin-arm64.zip" ||
		arm.Size != int64(len("darwin-arm64")) || arm.SHA256 != "1a349b12b50ad5b43740e0952adc33c7805ce06f091074be977624d09ed9d432" {
		t.Errorf("darwin-arm64 = %+v", arm)
	}

	// The JSON is the updater's contract.
	path := filepath.Join(dir, "latest.json")
	if err := writeJSON(path, m); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "tag", "date", "notes", "assets"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("latest.json has no %q: %s", key, data)
		}
	}
	asset := raw["assets"].(map[string]any)["windows-amd64"].(map[string]any)
	for _, key := range []string{"url", "sha256", "size"} {
		if _, ok := asset[key]; !ok {
			t.Errorf("an asset of latest.json has no %q: %s", key, data)
		}
	}

	if err := WriteChecksums(dir); err != nil {
		t.Fatal(err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, checksumsName))
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 6 || !strings.HasSuffix(lines[0], "  latest.json") || !strings.HasSuffix(lines[1], "  pinky-2026.1107.1-arm64.dmg") {
		t.Errorf("SHA256SUMS = %s", sums)
	}

	cask, err := Cask(m, "DIYgod/pinky")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`version "2026.1107.1"`,
		`sha256 arm:   "` + m.Assets["darwin-arm64"].SHA256 + `",`,
		`intel: "` + m.Assets["darwin-amd64"].SHA256 + `"`,
		`url "https://github.com/DIYgod/pinky/releases/download/v#{version}/pinky-#{version}-darwin-#{arch}.zip"`,
		"auto_updates true",
		// Ventura or later, as the Go toolchain builds Pinky (macOS 13);
		// Homebrew 7 deprecates the ">= :ventura" form.
		"depends_on macos: :ventura\n",
		`app "Pinky.app"`,
		`command_wrapper "pinkyctl",`,
	} {
		if !strings.Contains(cask, want) {
			t.Errorf("the cask lacks %s:\n%s", want, cask)
		}
	}
	if got := caskVersion(cask); got != "2026.1107.1" {
		t.Errorf("caskVersion = %q", got)
	}
	if _, err := Cask(m, "DIYgod/elsewhere"); err == nil {
		t.Error("Cask for another repository than the manifest's URLs: no error")
	}

	os.Remove(filepath.Join(dir, archiveName("windows-amd64", "2026.1107.1")))
	if _, _, err := BuildManifest(dir, "2026.1107.1", "DIYgod/pinky", "- One.", now, false); err == nil {
		t.Error("BuildManifest without the Windows installer: no error")
	}
	m, missing, err := BuildManifest(dir, "2026.1107.1", "DIYgod/pinky", "- One.", now, true)
	if _, ok := m.Assets["windows-amd64"]; err != nil || ok || len(m.Assets) != 3 || len(missing) != 1 || missing[0] != "pinky-setup-2026.1107.1-amd64.exe" {
		t.Errorf("partial BuildManifest without the Windows installer = %+v, %v, %v", m, missing, err)
	}
}

func TestZipDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"Pinky.exe": "exe", "sub/pinky-companion.apk": "apk"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "app.zip")
	if err := ZipDir(out, dir); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			got[f.Name] = "dir"
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		got[f.Name] = string(b)
	}
	if len(got) != 3 || got["Pinky.exe"] != "exe" || got["sub/"] != "dir" || got["sub/pinky-companion.apk"] != "apk" {
		t.Errorf("zip holds %v", got)
	}
}

// gitRepo makes a checkout on main with a remote, origin, both in
// temporary directories, and makes it the working directory.
func gitRepo(t *testing.T) (clone, remote string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	// Neither the person's nor the system's git configuration (signing,
	// hooks) applies.
	base := t.TempDir()
	global := filepath.Join(base, "gitconfig")
	write(t, global, "")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Release Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "release@example.com")
	}
	remote, clone = filepath.Join(base, "remote.git"), filepath.Join(base, "clone")
	run := func(dir string, args ...string) {
		t.Helper()
		if _, err := git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	run(base, "init", "--quiet", "--bare", "--initial-branch=main", remote)
	run(base, "init", "--quiet", "--initial-branch=main", clone)
	run(clone, "remote", "add", "origin", remote)
	write(t, filepath.Join(clone, changelogFile), changelogHead+"## 2026.1106.1 (2026-11-06)\n\n- Old.\n")
	for _, tr := range translations {
		write(t, filepath.Join(clone, tr.file), translationHead+"## 2026.1106.1 (2026-11-06)\n\n- 旧的。\n")
	}
	run(clone, "add", ".")
	run(clone, "commit", "--quiet", "-m", "feat: first")
	run(clone, "tag", "--annotate", "-m", "old", "v2026.1106.1")
	run(clone, "push", "--quiet", "origin", "main", "v2026.1106.1")
	t.Chdir(clone)
	return clone, remote
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTag(t *testing.T) {
	clone, remote := gitRepo(t)
	want, err := Next(time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing under Unreleased: nothing to release.
	if err := runTag([]string{"-push"}); !errors.Is(err, errNoRelease) {
		t.Fatalf("tag without notes = %v, want errNoRelease", err)
	}

	// Notes without their translation are not released.
	zh := translations[0].file
	notes := "## Unreleased\n\n- New.\n\n"
	write(t, filepath.Join(clone, changelogFile), changelogHead+notes+"## 2026.1106.1 (2026-11-06)\n\n- Old.\n")
	headBefore, _ := git(clone, "rev-parse", "HEAD")
	if err := runTag([]string{"-push"}); err == nil || !strings.Contains(err.Error(), zh) {
		t.Fatalf("tag without the notes in %s = %v", zh, err)
	}
	if head, _ := git(clone, "rev-parse", "HEAD"); head != headBefore {
		t.Fatal("tag without a translation committed")
	}

	// Notes left uncommitted are part of the release commit.
	write(t, filepath.Join(clone, zh), translationHead+"## Unreleased\n\n- 新的。\n\n## 2026.1106.1 (2026-11-06)\n\n- 旧的。\n")
	if err := runTag([]string{"-push", "-trailer", "Co-Authored-By: Someone <someone@example.com>"}); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(filepath.Join(clone, changelogFile))
	if !strings.Contains(string(text), "## "+want.String()+" ("+want.Date()+")\n\n- New.") {
		t.Errorf("CHANGELOG.md after tag:\n%s", text)
	}
	zhText, _ := os.ReadFile(filepath.Join(clone, zh))
	if !strings.Contains(string(zhText), "## "+want.String()+" ("+want.Date()+")\n\n- 新的。") {
		t.Errorf("%s after tag:\n%s", zh, zhText)
	}
	if status, _ := git(clone, "status", "--porcelain"); status != "" {
		t.Errorf("after tag: git status %q", status)
	}
	msg, _ := git(clone, "log", "-1", "--format=%B")
	if !strings.HasPrefix(msg, "chore(release): "+want.String()) || !strings.Contains(msg, "Co-Authored-By: Someone <someone@example.com>") {
		t.Errorf("release commit message = %q", msg)
	}
	tags, _ := remoteTags(clone, "origin")
	if !strings.Contains(strings.Join(tags, " "), want.Tag()) {
		t.Errorf("origin's tags = %v, want %s", tags, want.Tag())
	}
	head, _ := git(clone, "rev-parse", "HEAD")
	if remoteHead, _ := git(remote, "rev-parse", "main"); remoteHead != head {
		t.Errorf("origin's main = %s, want %s", remoteHead, head)
	}
	tagMsg, _ := git(clone, "tag", "--list", "--format=%(contents)", want.Tag())
	if !strings.Contains(tagMsg, "- New.") {
		t.Errorf("tag message = %q", tagMsg)
	}

	// Other changes are not released by the way.
	write(t, filepath.Join(clone, changelogFile), string(text)+"\n")
	write(t, filepath.Join(clone, "go.mod"), "module example\n")
	if _, err := git(clone, "add", "go.mod"); err != nil {
		t.Fatal(err)
	}
	if err := runTag(nil); err == nil || !strings.Contains(err.Error(), "go.mod") {
		t.Errorf("tag with go.mod staged = %v", err)
	}
	if _, err := git(clone, "rm", "--quiet", "--cached", "go.mod"); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(clone, "go.mod"))

	// A push that fails leaves the checkout as it was.
	if runtime.GOOS == "windows" {
		return
	}
	if _, err := git(clone, "checkout", "--quiet", changelogFile); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(clone, changelogFile), strings.Replace(string(text), "# Changelog\n\nWhat changed in each release.\n\n", changelogHead+"## Unreleased\n\n- Newer.\n\n", 1))
	write(t, filepath.Join(clone, zh), strings.Replace(string(zhText), translationHead, translationHead+"## Unreleased\n\n- 更新的。\n\n", 1))
	before, _ := os.ReadFile(filepath.Join(clone, changelogFile))
	zhBefore, _ := os.ReadFile(filepath.Join(clone, zh))
	headBefore, _ = git(clone, "rev-parse", "HEAD")
	write(t, filepath.Join(remote, "hooks", "pre-receive"), "#!/bin/sh\necho no pushes today >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(remote, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runTag([]string{"-push"}); err == nil {
		t.Fatal("tag with a refused push: no error")
	}
	after, _ := os.ReadFile(filepath.Join(clone, changelogFile))
	zhAfter, _ := os.ReadFile(filepath.Join(clone, zh))
	headAfter, _ := git(clone, "rev-parse", "HEAD")
	localTagList, _ := localTags(clone)
	status, _ := git(clone, "status", "--porcelain")
	if string(after) != string(before) || string(zhAfter) != string(zhBefore) || headAfter != headBefore || len(localTagList) != 2 || !modified(status, changelogFile, zh) {
		t.Errorf("after a refused push: CHANGELOG.md changed %v, %s changed %v, HEAD %s (was %s), tags %v, status %q", string(after) != string(before), zh, string(zhAfter) != string(zhBefore), headAfter, headBefore, localTagList, status)
	}
}

// modified tells whether git status --porcelain says that files, and
// nothing else, are changed and not staged.
func modified(status string, files ...string) bool {
	lines := strings.Split(strings.TrimSpace(status), "\n")
	if len(lines) != len(files) {
		return false
	}
	for i, l := range lines {
		if strings.TrimSpace(l) != "M "+files[i] {
			return false
		}
	}
	return true
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"2026.1107.2", "2026.1107.1"}, true},
		{[]string{"2026.1107.2", "v2026.1107.10"}, false},
		{[]string{"2026.1107.2", "2026.1107.2"}, false},
		{[]string{"2026.1107.2", ""}, true},
		{[]string{"2026.1107.2"}, true},
	} {
		err := runNewer(tc.args)
		if (err == nil) != tc.ok {
			t.Errorf("newer %v = %v, want ok %v", tc.args, err, tc.ok)
		}
	}
}

func TestManifestCommand(t *testing.T) {
	dir := t.TempDir()
	writeArchives(t, dir, "2026.1107.1")
	changelog := filepath.Join(t.TempDir(), changelogFile)
	write(t, changelog, changelogHead+"## 2026.1107.1 (2026-11-07)\n\n- One.\n")
	if err := runManifest([]string{"-dir", dir, "-version", "2026.1107.1", "-changelog", changelog}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Notes != "- One." || m.LocalizedNotes != nil || !strings.HasPrefix(m.Assets["linux-amd64"].URL, "https://github.com/yaoqiangyao/pinky/releases/download/v2026.1107.1/") {
		t.Errorf("latest.json without a translation = %+v", m)
	}
	if page := PageNotes(m); page != "- One." {
		t.Errorf("the release page without a translation:\n%s", page)
	}
	// The translations next to the changelog give the notes in their
	// languages.
	write(t, filepath.Join(filepath.Dir(changelog), translations[0].file), translationHead+"## 2026.1107.1 (2026-11-07)\n\n- 一。\n")
	if err := runManifest([]string{"-dir", dir, "-version", "2026.1107.1", "-changelog", changelog}); err != nil {
		t.Fatal(err)
	}
	if m, err = readManifest(filepath.Join(dir, "latest.json")); err != nil {
		t.Fatal(err)
	}
	if m.Notes != "- One." || len(m.LocalizedNotes) != 1 || m.LocalizedNotes["zh-Hans"] != "- 一。" {
		t.Errorf("latest.json = %+v", m)
	}
	// The release page has the English notes, then the Chinese ones.
	if page := PageNotes(m); page != "- One.\n\n---\n\n### 中文\n\n- 一。" {
		t.Errorf("the release page:\n%s", page)
	}
	cask, err := os.ReadFile(filepath.Join(dir, caskFile))
	if err != nil || !strings.Contains(string(cask), `homepage "https://github.com/yaoqiangyao/pinky"`) {
		t.Errorf("%s = %s, %v", caskFile, cask, err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, checksumsName))
	if !strings.Contains(string(sums), "  latest.json\n") || !strings.Contains(string(sums), "  "+caskFile+"\n") {
		t.Errorf("SHA256SUMS = %s", sums)
	}
}

// This fork's releases carry the Windows installer alone: the manifest
// leaves out the other platforms (-partial) and writes no cask.
func TestManifestWindowsOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, archiveName("windows-amd64", "2026.1107.1")), []byte("exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	changelog := filepath.Join(t.TempDir(), changelogFile)
	write(t, changelog, changelogHead+"## 2026.1107.1 (2026-11-07)\n\n- One.\n")
	if err := runManifest([]string{"-dir", dir, "-version", "2026.1107.1", "-changelog", changelog, "-partial"}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Assets["windows-amd64"]; !ok || len(m.Assets) != 1 {
		t.Errorf("latest.json's assets = %+v; want windows-amd64 alone", m.Assets)
	}
	if _, err := os.Stat(filepath.Join(dir, caskFile)); !os.IsNotExist(err) {
		t.Errorf("%s was written for a release without the Mac archives: %v", caskFile, err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, checksumsName))
	if !strings.Contains(string(sums), "  latest.json\n") {
		t.Errorf("SHA256SUMS = %s", sums)
	}
}
