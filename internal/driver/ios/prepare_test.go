package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
	"howett.net/plist"
)

// writeDiskImage makes a developer disk image folder as Xcode installs it:
// iOS_DDI/version.plist and Restore with a manifest that has a personalized
// identity and, when version is set, a cryptex one.
func writeDiskImage(t *testing.T, dir, build, version string, skip ...string) string {
	t.Helper()
	restore := filepath.Join(dir, "iOS_DDI", "Restore")
	if err := os.MkdirAll(filepath.Join(restore, "Firmware"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, v any) {
		for _, s := range skip {
			if s == name {
				return
			}
		}
		p := filepath.Join(restore, name)
		var b []byte
		switch v := v.(type) {
		case string:
			b = []byte(v)
		default:
			var err error
			if b, err = plist.Marshal(v, plist.XMLFormat); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("../version.plist", map[string]any{"ProductBuildVersion": build})
	entry := func(path string) map[string]any {
		return map[string]any{"Digest": []byte{1}, "Info": map[string]any{"Path": path, "Personalize": true}}
	}
	identities := []any{map[string]any{
		"ApBoardID": "0x0C", "ApChipID": "0x8010",
		"Manifest": map[string]any{
			"PersonalizedDMG":    entry("b.dmg"),
			"LoadableTrustCache": entry("Firmware/b.dmg.trustcache"),
		},
	}}
	write("b.dmg", "image")
	write("Firmware/b.dmg.trustcache", "trust")
	if version != "" {
		identities = append(identities, map[string]any{
			"Cryptex1,ChipID": "0xFF10", "Cryptex1,Type": "0x0",
			"Manifest": map[string]any{
				"Cryptex1,GenericDmg":        entry("a.dmg"),
				"Cryptex1,CryptexInfoPlist":  entry("Firmware/a.dmg.cryptex_info"),
				"Cryptex1,GenericTrustCache": entry("Firmware/a.dmg.trustcache"),
			},
		})
		write("a.dmg", "cryptex")
		write("Firmware/a.dmg.trustcache", "trust")
		write("Firmware/a.dmg.cryptex_info", map[string]any{"CFBundleIdentifier": ddiCryptex, "CFBundleVersion": version})
	}
	write("BuildManifest.plist", map[string]any{"BuildIdentities": identities})
	return filepath.Join(dir, "iOS_DDI")
}

func TestReadDiskImage(t *testing.T) {
	dir := writeDiskImage(t, t.TempDir(), "27B52", "27.2.52.0")
	img, err := readDiskImage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if img.Dir != filepath.Join(dir, "Restore") || img.Build != "27B52" || img.Version != "27.2.52.0" {
		t.Fatalf("readDiskImage = %+v", img)
	}
	// The Restore folder itself works too.
	if img2, err := readDiskImage(filepath.Join(dir, "Restore")); err != nil || img2 != img {
		t.Fatalf("readDiskImage(Restore) = %+v, %v", img2, err)
	}
	// An image without a cryptex identity (Xcode 15) has no version.
	old := writeDiskImage(t, t.TempDir(), "15F31d", "")
	if img, err := readDiskImage(old); err != nil || img.Version != "" || img.Build != "15F31d" {
		t.Fatalf("an Xcode 15 image = %+v, %v", img, err)
	}
	// A folder missing a file the manifest names is not used.
	half := writeDiskImage(t, t.TempDir(), "27B52", "27.2.52.0", "a.dmg")
	if _, err := readDiskImage(half); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("a half-copied image: %v", err)
	}
	if _, err := readDiskImage(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an empty folder: %v", err)
	}
}

func TestFindDiskImage(t *testing.T) {
	own := t.TempDir()
	writeDiskImage(t, own, "99A1", "99.0.1.0")
	t.Setenv(ddiEnv, "")
	img, err := findDiskImage(own)
	if err != nil {
		t.Fatal(err)
	}
	// Pinky's folder wins over Xcode's (on a Mac with Xcode) by being newer.
	if img.Source != sourcePinky || img.Version != "99.0.1.0" {
		t.Fatalf("findDiskImage = %+v", img)
	}
	// PINKY_DDI is the only folder looked in, and a wrong one is reported.
	env := writeDiskImage(t, t.TempDir(), "26A1", "26.0.1.0")
	t.Setenv(ddiEnv, env)
	if img, err := findDiskImage(own); err != nil || img.Source != sourceEnv || img.Version != "26.0.1.0" {
		t.Fatalf("with PINKY_DDI: %+v, %v", img, err)
	}
	t.Setenv(ddiEnv, filepath.Join(env, "nope"))
	if _, err := findDiskImage(own); err == nil || !strings.Contains(err.Error(), ddiEnv) {
		t.Fatalf("with a wrong PINKY_DDI: %v", err)
	}
}

func TestCompareVersionsAndBuilds(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"27.2.52.0", "27.2.52.0", 0},
		{"27.2.52.0", "27.10.1.0", -1},
		{"28.0", "27.9.9.9", 1},
		{"", "17.0", -1},
		{"17.0", "", 1},
		{"27.2", "27.2.0.0", 0},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"27B52", "27B52", 0},
		{"27B52", "27A324", 1},
		{"17E5179g", "27B52", -1},
		{"27C10", "27B99", 1},
		{"27B100", "27B99", 1},
	} {
		if got := compareBuilds(c.a, c.b); got != c.want {
			t.Errorf("compareBuilds(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestClassifyMountError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want mountProblem
	}{
		{errors.New("MountImage: mount command failed: MountImage: device responded with error: DeviceLocked"), problemLocked},
		{errors.New("MountImage: device responded with error: DeveloperModeIsNotEnabled"), problemDevMode},
		{errors.New(`MountImage: failed to get Cryptex1 ticket from Apple: requestTicket: failed to send request: Post "https://gs.apple.com/TSS/controller?action=2": dial tcp: lookup gs.apple.com: no such host`), problemNetwork},
		{errors.New(`MountImage: failed to get Cryptex1 ticket from Apple: requestTicket: failed to send request: Post "https://gs.apple.com/TSS/controller?action=2": tls: failed to verify certificate: x509: certificate signed by unknown authority`), problemOther},
		{errors.New(`MountImage: failed to get signature from Apple: unexpected status in response 94: "This device isn't eligible for the requested build."`), problemRefused},
		{errors.New(appleRefused), problemRefused},
		{errors.New("MountImage: findCryptexIdentity: no Cryptex1 identity for chip 0xff10"), problemNotFor},
		{errors.Join(errors.New("as a cryptex: x"), errNewerImageMounted), problemNewer},
		{errNoDiskImage, problemNoImage},
		{fmt.Errorf("mount: %w", errPhoneLocked), problemLocked},
		{errors.New("something else"), problemOther},
	} {
		if got := classifyMountError(c.err); got != c.want {
			t.Errorf("classifyMountError(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// fakePrep is a preparer whose mounts the test runs and ends.
type fakePrep struct {
	mu      sync.Mutex
	now     time.Time
	mounts  int
	started chan struct{}
	results chan error
	done    chan string
}

func newFakePrep(t *testing.T, pr *preparer) *fakePrep {
	f := &fakePrep{now: time.Unix(1000, 0), started: make(chan struct{}, 10), results: make(chan error), done: make(chan string, 10)}
	pr.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	pr.findFn = func(string) (diskImage, error) {
		return diskImage{Dir: "/x", Build: "27B52", Version: "27.2.52.0"}, nil
	}
	pr.mountFn = func(context.Context, goios.DeviceEntry, diskImage) (mountResult, error) {
		f.mu.Lock()
		f.mounts++
		f.mu.Unlock()
		f.started <- struct{}{}
		select {
		case err := <-f.results:
			return mountResult{How: mountCryptex}, err
		case <-time.After(5 * time.Second):
			t.Error("the test never ended the mount")
			return mountResult{}, errors.New("test timeout")
		}
	}
	return f
}

func (f *fakePrep) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *fakePrep) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mounts
}

func TestPreparerMounts(t *testing.T) {
	var pr preparer
	f := newFakePrep(t, &pr)
	e := entry("phone", 1, "USB")
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	done := func() { f.done <- "done" }
	state := func() mountJob { return pr.mountState(e, "", ok, done, nil) }

	if j := state(); !j.running {
		t.Fatalf("first look: %+v, want a mount running", j)
	}
	<-f.started
	if j := state(); !j.running || f.count() > 1 {
		t.Fatalf("while it runs: %+v, %d mounts; want the same one", j, f.count())
	}
	// Locked: tried again a few seconds later.
	f.results <- errors.New("MountImage: device responded with error: DeviceLocked")
	<-f.done
	if j := state(); j.running || classifyMountError(j.err) != problemLocked {
		t.Fatalf("after a locked phone: %+v", j)
	}
	f.advance(4 * time.Second)
	if j := state(); !j.running {
		t.Fatalf("4 s later: %+v; want a second try", j)
	}
	<-f.started
	if f.count() != 2 {
		t.Fatalf("4 s later: %d mounts; want a second try", f.count())
	}
	f.results <- nil
	<-f.done
	if j := state(); j.running || j.err != nil {
		t.Fatalf("after mounting: %+v", j)
	}
	f.advance(10 * time.Second)
	if j := state(); j.running || f.count() != 2 {
		t.Fatalf("10 s later: %+v, %d mounts; a phone just mounted is not mounted again", j, f.count())
	}
	// Missing services again after a while (another tool took the image
	// off): mounted again.
	f.advance(time.Minute)
	if j := state(); !j.running {
		t.Fatalf("a minute later: %+v; want a new mount", j)
	}
	<-f.started
	f.results <- nil
	<-f.done
	// The phone restarted: a new attach mounts again at once.
	e = entry("phone", 2, "USB")
	if j := state(); !j.running {
		t.Fatalf("after a restart: %+v", j)
	}
	<-f.started
	if f.count() != 4 {
		t.Fatalf("after a restart: %d mounts, want 4", f.count())
	}
	f.results <- nil
	<-f.done
}

// TestPreparerLogs writes a line when a mount starts and when it ends: the
// way it went and how long it took, or why it failed and when it is tried
// again.
func TestPreparerLogs(t *testing.T) {
	var pr preparer
	f := newFakePrep(t, &pr)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	pr.mountState(entry("phone", 1, "USB"), "", ok, func() { f.done <- "done" }, log)
	<-f.started
	f.results <- nil
	<-f.done
	// The next attach, whose mount times out.
	pr.mountState(entry("phone", 2, "USB"), "", ok, func() { f.done <- "done" }, log)
	<-f.started
	f.results <- errors.New("mounting the developer disk image: context deadline exceeded")
	<-f.done
	out := buf.String()
	for _, want := range []string{
		`level=INFO msg="iphone: mounting the developer disk image" device=phone image="the image 27B52" dir=/x`,
		`level=INFO msg="iphone: developer disk image ready" device=phone way=cryptex kept=false replaced="" took=`,
		`level=WARN msg="iphone: could not mount the developer disk image" device=phone way=cryptex took=`,
		`err="mounting the developer disk image: context deadline exceeded" retry_in=30s`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %s:\n%s", want, out)
		}
	}
}

// appleRefused is how a mount fails when Apple's signing server refuses
// the image: what the cryptex package returns for gs.apple.com's answer
// (TestCryptexMountRefusedByApple), as users of iOS 27.3 saw it with an
// image Apple no longer signs.
const appleRefused = `MountImage: failed to get Cryptex1 ticket from Apple: unexpected status in response 94: "This device isn't eligible for the requested build."`

// TestPreparerRefusedImage does not try an image Apple's signing server
// refused again, but tries at once when the computer has another image or
// the phone reconnects.
func TestPreparerRefusedImage(t *testing.T) {
	var pr preparer
	f := newFakePrep(t, &pr)
	old := diskImage{Source: sourceXcode, Dir: "/Library/Developer/DeveloperDiskImages/iOS_DDI/Restore", Build: "17B48", Version: "17.2.48.0"}
	img, finds := old, 0
	pr.findFn = func(string) (diskImage, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		finds++
		if img.Dir == "" {
			return diskImage{}, errNoDiskImage
		}
		return img, nil
	}
	setImage := func(i diskImage) {
		f.mu.Lock()
		img = i
		f.mu.Unlock()
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	e := entry("phone", 1, "USB")
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	state := func() mountJob { return pr.mountState(e, "", ok, func() { f.done <- "done" }, log) }
	refuse := func() {
		t.Helper()
		<-f.started
		f.results <- errors.New(appleRefused)
		<-f.done
	}

	state()
	refuse()
	for _, wait := range []time.Duration{0, time.Second, 10 * time.Minute, 24 * time.Hour} {
		f.advance(wait)
		if j := state(); j.running || !imageRefused(j.err) || f.count() != 1 {
			t.Fatalf("%v after the refusal: %+v, %d mounts; the same image is not tried again", wait, j, f.count())
		}
	}
	if finds < 5 {
		t.Errorf("looked for an image %d times; each look checks whether it changed", finds)
	}
	if out := buf.String(); !strings.Contains(out, `msg="iphone: Apple's signing server refused the developer disk image" device=phone image="Xcode's image 17B48" version=17.2.48.0`) ||
		!strings.Contains(out, `retry="once another image is on this computer"`) {
		t.Errorf("the log says not which image or when it is tried again:\n%s", out)
	}

	info := diskImageState(driver.Info{OSVersion: "27.3"}, state(), "")
	want := &driver.DiskImage{Source: driver.DiskImageFromXcode, Build: "17B48", Version: "17.2.48.0", Dir: old.Dir}
	if info.Setup != driver.SetupDiskImageRefused || info.DiskImage == nil || *info.DiskImage != *want {
		t.Fatalf("refused: %+v, want %s with %+v", info, driver.SetupDiskImageRefused, want)
	}
	for _, says := range []string{"Xcode's image 17B48, version 17.2.48.0, in " + old.Dir, `status 94: "This device isn't eligible for the requested build."`, "Install or update Xcode from the App Store"} {
		if !strings.Contains(info.Detail, says) {
			t.Errorf("%q does not say %q", info.Detail, says)
		}
	}

	// Xcode was updated: its newer image is tried at once.
	setImage(diskImage{Source: sourceXcode, Dir: old.Dir, Build: "27B52", Version: "27.2.52.0"})
	if j := state(); !j.running {
		t.Fatalf("with a newer image: %+v; want a new mount", j)
	}
	refuse()
	// It went: the job ends with no image, which says where to get one.
	setImage(diskImage{})
	if j := state(); !j.running {
		t.Fatalf("without an image: %+v; want a new look", j)
	}
	<-f.done
	if j := state(); !errors.Is(j.err, errNoDiskImage) {
		t.Fatalf("without an image: %+v", j)
	}
	// The same image again after the phone reconnects: one more try.
	setImage(old)
	f.advance(time.Minute)
	state()
	refuse()
	n := f.count()
	e = entry("phone", 2, "USB")
	if j := state(); !j.running {
		t.Fatalf("after a reconnect: %+v; want a new mount", j)
	}
	refuse()
	if f.count() != n+1 {
		t.Fatalf("%d mounts, want %d", f.count(), n+1)
	}
}

// writeMirrorImage puts an image in Pinky's folder as the download from
// the third-party mirror leaves it.
func writeMirrorImage(t *testing.T, own, build, version string) {
	t.Helper()
	if err := os.Rename(writeDiskImage(t, t.TempDir(), build, version), filepath.Join(own, mirrorFolder)); err != nil {
		t.Fatal(err)
	}
}

// TestFindDiskImageAvoiding passes over images Apple refused while there is
// another: an official copy gives way to the mirror's download. The copy
// is in Pinky's folder, which every system looks in; Xcode's, only on a
// Mac, is passed over the same way.
func TestFindDiskImageAvoiding(t *testing.T) {
	defer func(d string) { xcodeDDI = d }(xcodeDDI)
	t.Setenv(ddiEnv, "")
	xcodeDDI = filepath.Join(t.TempDir(), "none")
	own := t.TempDir()
	writeDiskImage(t, own, "17B48", "17.2.48.0")
	refused := map[string]bool{}
	pick := func(what string, source, build string) {
		t.Helper()
		img, err := findDiskImageAvoiding(own, func(img diskImage) bool { return refused[img.Build] })
		if err != nil || img.Source != source || img.Build != build {
			t.Errorf("%s: %+v, %v; want %s's %s", what, img, err, source, build)
		}
	}
	pick("the official copy alone", sourcePinky, "17B48")
	refused["17B48"] = true
	pick("the official copy refused, alone", sourcePinky, "17B48")
	writeMirrorImage(t, own, "27A5228h", "27.1.5228.8")
	pick("the official copy refused, the mirror's downloaded", sourceMirror, "27A5228h")
	if img, err := findDiskImage(own); err != nil || img.Source != sourcePinky {
		t.Errorf("without refusals the official copy still wins: %+v, %v", img, err)
	}
	refused["27A5228h"] = true
	pick("both refused", sourceMirror, "27A5228h")
	writeDiskImage(t, own, "27B52", "27.2.52.0")
	pick("a newer official copy", sourcePinky, "27B52")
}

// imageMounter mounts with images it is told Apple refuses, by build, and
// keeps the builds it was asked to mount.
type imageMounter struct {
	mu      sync.Mutex
	refuse  map[string]bool
	mounted []string
	done    chan struct{}
}

func (m *imageMounter) prep(pr *preparer) {
	m.done = make(chan struct{}, 10)
	pr.mountFn = func(_ context.Context, _ goios.DeviceEntry, img diskImage) (mountResult, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.mounted = append(m.mounted, img.Build)
		if m.refuse[img.Build] {
			return mountResult{How: mountCryptex}, errors.New(appleRefused)
		}
		return mountResult{How: mountCryptex}, nil
	}
}

func (m *imageMounter) mounts() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.mounted, " ")
}

// TestPreparerMirrorAfterRefusal mounts the mirror's download in the same
// job when Apple refuses the official copy (here in Pinky's folder; Xcode's
// goes the same way), keeps using it after a restart, and reports the
// mirror's refusal without trying again when Apple refuses that one too.
func TestPreparerMirrorAfterRefusal(t *testing.T) {
	defer func(d string) { xcodeDDI = d }(xcodeDDI)
	t.Setenv(ddiEnv, "")
	xcodeDDI = filepath.Join(t.TempDir(), "none")
	own := t.TempDir()
	writeDiskImage(t, own, "17B48", "17.2.48.0")
	writeMirrorImage(t, own, "27A5228h", "27.1.5228.8")
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	e := entry("phone", 1, "USB")
	run := func(pr *preparer, m *imageMounter) mountJob {
		t.Helper()
		pr.mountState(e, own, ok, func() { m.done <- struct{}{} }, nil)
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
			t.Fatal("the mount did not end")
		}
		return pr.mountState(e, own, ok, nil, nil)
	}

	var pr preparer
	m := &imageMounter{refuse: map[string]bool{"17B48": true}}
	m.prep(&pr)
	if j := run(&pr, m); j.err != nil || j.img.Source != sourceMirror || m.mounts() != "17B48 27A5228h" {
		t.Fatalf("the official copy refused: %+v after mounting %q; want the mirror's in the same job", j, m.mounts())
	}
	b, err := os.ReadFile(filepath.Join(own, refusedFile))
	if err != nil || !strings.Contains(string(b), `"17B48 17.2.48.0"`) {
		t.Fatalf("the refusal is not kept: %s, %v", b, err)
	}

	// After a restart, the refused copy is not tried again.
	var again preparer
	m = &imageMounter{}
	m.prep(&again)
	if j := run(&again, m); j.err != nil || m.mounts() != "27A5228h" {
		t.Fatalf("after a restart: %+v after mounting %q; want the mirror's alone", j, m.mounts())
	}

	// Apple refuses the mirror's too: reported once, not tried again.
	var later preparer
	f := newFakePrep(t, &later)
	later.findFn = nil
	m = &imageMounter{refuse: map[string]bool{"17B48": true, "27A5228h": true}}
	m.prep(&later)
	j := run(&later, m)
	if !imageRefused(j.err) || j.img.Source != sourceMirror || m.mounts() != "27A5228h" {
		t.Fatalf("every image refused: %+v after mounting %q; want the mirror's refusal", j, m.mounts())
	}
	info := diskImageState(driver.Info{OSVersion: "27.3"}, j, own)
	if info.Setup != driver.SetupDiskImageRefused || info.DiskImage == nil || info.DiskImage.Source != driver.DiskImageFromMirror {
		t.Fatalf("every image refused: %+v", info)
	}
	for _, wait := range []time.Duration{time.Minute, time.Hour} {
		f.advance(wait)
		if j := later.mountState(e, own, ok, nil, nil); j.running || m.mounts() != "27A5228h" {
			t.Fatalf("%v later: %+v after mounting %q; nothing is tried again", wait, j, m.mounts())
		}
	}
	// A signature clears the refusal.
	m.mu.Lock()
	m.refuse = map[string]bool{}
	m.mu.Unlock()
	e = entry("phone", 2, "USB")
	if j := run(&later, m); j.err != nil || m.mounts() != "27A5228h 27A5228h" {
		t.Fatalf("after a reconnect, signed: %+v after mounting %q", j, m.mounts())
	}
	if later.isRefused(own, diskImage{Build: "27A5228h", Version: "27.1.5228.8"}) {
		t.Error("the mirror's image is still marked refused after Apple signed it")
	}
}

// TestRefusedDetail says how to get an image Apple signs, by where the
// refused one came from.
func TestRefusedDetail(t *testing.T) {
	err := errors.New(appleRefused)
	for _, c := range []struct {
		img  diskImage
		says string
	}{
		{diskImage{Source: sourceXcode, Build: "17B48", Version: "17.2.48.0", Dir: "/x"}, "Install or update Xcode from the App Store and open it once"},
		{diskImage{Source: sourceMirror, Build: "27A5228h", Version: "27.1.5228.8", Dir: "/m"}, "update Pinky when a version with a newer image is out"},
		{diskImage{Source: sourcePinky, Build: "17B48", Dir: "/p"}, "Replace it with the iOS_DDI folder"},
		{diskImage{Source: sourceEnv, Build: "17B48", Dir: "/e"}, "Replace it with the iOS_DDI folder"},
	} {
		d := refusedDetail(c.img, err)
		for _, says := range []string{c.says, imageName(c.img), "in " + c.img.Dir, "status 94"} {
			if !strings.Contains(d, says) {
				t.Errorf("%s image: %q does not say %q", c.img.Source, d, says)
			}
		}
	}
}

func TestPreparerServicesMissing(t *testing.T) {
	defer func(d time.Duration) { serviceWait = d }(serviceWait)
	serviceWait = 200 * time.Millisecond
	var pr preparer
	f := newFakePrep(t, &pr)
	e := entry("phone", 1, "USB")
	missing := func(context.Context, goios.DeviceEntry) (bool, error) { return false, nil }
	pr.mountState(e, "", missing, func() { f.done <- "done" }, nil)
	<-f.started
	f.results <- nil
	select {
	case <-f.done:
	case <-time.After(15 * time.Second):
		t.Fatal("the mount did not end")
	}
	j := pr.mountState(e, "", missing, nil, nil)
	if j.running || !errors.Is(j.err, errServicesMissing) {
		t.Fatalf("services missing after a mount: %+v", j)
	}
	info := diskImageState(driver.Info{OSVersion: "28.0"}, j, "")
	if info.Setup != driver.SetupDiskImage || !strings.Contains(info.Detail, "too old for iOS 28.0") {
		t.Fatalf("an image older than the phone: %+v", info)
	}
	info = diskImageState(driver.Info{OSVersion: "27.0.1"}, j, "")
	if !strings.Contains(info.Detail, "Restart the iPhone") {
		t.Fatalf("services missing with a current image: %+v", info)
	}
}

func TestDiskImageState(t *testing.T) {
	img := diskImage{Build: "27B52", Version: "27.2.52.0"}
	for _, c := range []struct {
		job   mountJob
		setup string
		says  string
	}{
		{mountJob{running: true}, driver.SetupPreparing, "preparing"},
		{mountJob{img: img, err: errors.New("device responded with error: DeviceLocked")}, driver.SetupUnlock, "Unlock the iPhone"},
		{mountJob{err: errNoDiskImage}, driver.SetupNoDiskImage, "comes with Xcode"},
		{mountJob{img: img, err: errors.New("requestTicket: failed to send request: dial tcp: i/o timeout")}, driver.SetupDiskImage, "gs.apple.com"},
		{mountJob{img: img, err: errors.New(`unexpected status in response 94: "This device isn't eligible"`)}, driver.SetupDiskImageRefused, "would not sign"},
		{mountJob{img: img, err: errors.New("findCryptexIdentity: no Cryptex1 identity")}, driver.SetupDiskImage, "Xcode 27B52"},
		{mountJob{img: img, err: errors.New("as a cryptex: boom\nas a personalized image: bang")}, driver.SetupDiskImage, "could not mount the developer disk image: as a personalized image: bang"},
	} {
		info := diskImageState(driver.Info{OSVersion: "27.0.1"}, c.job, "/data/DeveloperDiskImages")
		if info.State != driver.StateNeedsSetup || info.Setup != c.setup || !strings.Contains(info.Detail, c.says) {
			t.Errorf("job %+v: %+v, want %s saying %q", c.job, info, c.setup, c.says)
		}
	}
}

func TestDiskImageMissingForgetsWhenDone(t *testing.T) {
	p := NewProvider()
	f := newFakePrep(t, &p.prep)
	e := entry("phone", 1, "USB")
	p.known["phone"] = known{key: keyOf(e), info: driver.Info{ID: "phone", State: driver.StateNeedsSetup, Setup: driver.SetupPreparing}}
	p.ddi["phone"] = ddiCheck{}
	p.ddi["phone|display"] = ddiCheck{}
	p.ddi["phone2"] = ddiCheck{}
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	info := p.diskImageMissing(e, driver.Info{ID: "phone"}, ok)
	if info.Setup != driver.SetupPreparing {
		t.Fatalf("diskImageMissing = %+v", info)
	}
	<-f.started
	f.results <- nil
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		_, k := p.known["phone"]
		_, d1 := p.ddi["phone"]
		_, d2 := p.ddi["phone|display"]
		_, other := p.ddi["phone2"]
		p.mu.Unlock()
		if !k && !d1 && !d2 {
			if !other {
				t.Fatal("another phone's check was forgotten too")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the provider still remembers the phone after its mount ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRevealDevModeOncePerAttach(t *testing.T) {
	p := NewProvider()
	var mu sync.Mutex
	reveals := 0
	p.prep.revealFn = func(goios.DeviceEntry) error {
		mu.Lock()
		reveals++
		mu.Unlock()
		return nil
	}
	count := func() int {
		time.Sleep(20 * time.Millisecond) // reveal runs in the background
		mu.Lock()
		defer mu.Unlock()
		return reveals
	}
	e := entry("phone", 1, "USB")
	info := p.devModeOff(e, driver.Info{})
	if info.State != driver.StateNeedsSetup || info.Setup != driver.SetupDeveloperMode || !strings.Contains(info.Detail, "Privacy & Security > Developer Mode") {
		t.Fatalf("devModeOff = %+v", info)
	}
	p.devModeOff(e, driver.Info{})
	if n := count(); n != 1 {
		t.Fatalf("%d reveals for one attach, want 1", n)
	}
	p.devModeOff(entry("phone", 2, "USB"), driver.Info{})
	if n := count(); n != 2 {
		t.Fatalf("%d reveals after a reconnect, want 2", n)
	}
	// A reveal that failed is sent again at the next look.
	p.prep.revealFn = func(goios.DeviceEntry) error {
		mu.Lock()
		reveals++
		mu.Unlock()
		return errors.New("amfi: connection refused")
	}
	e3 := entry("phone", 3, "USB")
	p.devModeOff(e3, driver.Info{})
	count()
	p.devModeOff(e3, driver.Info{})
	if n := count(); n != 4 {
		t.Fatalf("%d reveals after a failed one, want 4", n)
	}
}

func TestAskTrustThrottles(t *testing.T) {
	var pr preparer
	now := time.Unix(1000, 0)
	pr.now = func() time.Time { return now }
	asks := 0
	pr.pairFn = func(goios.DeviceEntry) error {
		asks++
		return errors.New("Please accept the PairingDialog on the device and run pairing again!")
	}
	e := entry("phone", 1, "USB")
	pr.askTrust(e, nil)
	pr.askTrust(e, nil)
	if asks != 1 {
		t.Fatalf("%d pairing requests at once, want 1", asks)
	}
	now = now.Add(5 * time.Second)
	pr.askTrust(e, nil)
	if asks != 2 {
		t.Fatalf("%d pairing requests 5 s later, want 2", asks)
	}
}

func TestTrustState(t *testing.T) {
	info := trustState(driver.Info{}, errors.New("lockdown: InvalidHostID"))
	if info.State != driver.StateUnauthorized || info.Setup != driver.SetupTrust || !strings.Contains(info.Detail, "tap Trust") {
		t.Fatalf("trustState = %+v", info)
	}
	if info := trustState(driver.Info{}, errors.New("Lockdown error: UserDeniedPairing")); !strings.Contains(info.Detail, "plug it in again") {
		t.Fatalf("after Don't Trust: %+v", info)
	}
}

// TestCoreDeviceTooOldState: an iPhone below iOS 27 set to connect without
// the helper waits for another connection, a step the client can word.
func TestCoreDeviceTooOldState(t *testing.T) {
	info := coreDeviceTooOldState(driver.Info{OSVersion: "18.6"})
	if info.State != driver.StateNeedsSetup || info.Setup != driver.SetupConnection ||
		!strings.Contains(info.Detail, "iOS 18.6") || !strings.Contains(info.Detail, "Settings > Phones") {
		t.Fatalf("coreDeviceTooOldState = %+v", info)
	}
}

func TestRecheckFor(t *testing.T) {
	for _, c := range []struct {
		info driver.Info
		want time.Duration
	}{
		{driver.Info{State: driver.StateUnauthorized}, 2 * time.Second},
		{driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupPreparing}, 3 * time.Second},
		{driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUnlock}, 3 * time.Second},
		{driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode}, recheckOffline},
		{driver.Info{State: driver.StateOffline}, recheckOffline},
	} {
		if got := recheckFor(c.info); got != c.want {
			t.Errorf("recheckFor(%+v) = %v, want %v", c.info, got, c.want)
		}
	}
}

func TestHostCheck(t *testing.T) {
	err := errors.New("dial unix /var/run/usbmuxd: connect: no such file or directory")
	for _, c := range []struct {
		goos      string
		installed bool
		want      string
	}{
		{"windows", false, "apple_devices"},
		{"linux", false, "usbmuxd"},
		{"linux", true, "usbmuxd_stopped"},
		{"darwin", false, "usbmuxd"},
	} {
		if got := hostCheck(c.goos, c.installed, err); got.Missing != c.want || got.Detail == "" {
			t.Errorf("hostCheck(%s, %v) = %+v, want %s", c.goos, c.installed, got, c.want)
		}
	}
}

func TestListWithoutUsbmuxd(t *testing.T) {
	p := NewProvider()
	p.inspectFn = func(_ context.Context, e goios.DeviceEntry) driver.Info {
		return driver.Info{ID: e.Properties.SerialNumber, State: driver.StateOnline}
	}
	down := errors.New("dial unix /var/run/usbmuxd: connect: no such file or directory")
	var answer error
	phones := []goios.DeviceEntry{entry("phone", 1, "USB")}
	p.listFn = func() (goios.DeviceList, error) {
		if answer != nil {
			return goios.DeviceList{}, answer
		}
		return goios.DeviceList{DeviceList: phones}, nil
	}
	ctx := context.Background()
	// Never answered: no phones and no error, and CheckHost says why.
	answer = down
	if got, err := p.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("without usbmuxd: %v, %v", got, err)
	}
	if c := p.CheckHost(); c.Missing == "" {
		t.Fatalf("CheckHost without usbmuxd = %+v", c)
	}
	// It answered with a phone; a hiccup is an error twice (the gateway
	// keeps the phone), then the phone is gone.
	answer = nil
	if got, err := p.List(ctx); err != nil || len(got) != 1 {
		t.Fatalf("with usbmuxd: %v, %v", got, err)
	}
	if c := p.CheckHost(); c.Missing != "" {
		t.Fatalf("CheckHost with usbmuxd = %+v", c)
	}
	answer = down
	for i := 0; i < 2; i++ {
		if _, err := p.List(ctx); err == nil {
			t.Fatalf("failure %d after phones: no error", i+1)
		}
	}
	if got, err := p.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("third failure: %v, %v; want no phones", got, err)
	}
}

func TestMountWay(t *testing.T) {
	cryptex := diskImage{Version: "27.2.52.0"}
	old := diskImage{} // Xcode 15: no cryptex
	for _, c := range []struct {
		major    int
		img      diskImage
		cryptexd bool
		want     string
		err      error
	}{
		{27, cryptex, true, mountCryptex, nil},
		{28, cryptex, true, mountCryptex, nil},
		{27, old, true, "", errNoCryptex},
		{27, cryptex, false, "", nil}, // an error, not the other way
		{26, cryptex, true, mountPersonalized, nil},
		{18, old, false, mountPersonalized, nil},
	} {
		got, err := mountWay(c.major, c.img, c.cryptexd)
		if got != c.want || (c.err != nil && !errors.Is(err, c.err)) || (c.want == "" && err == nil) {
			t.Errorf("mountWay(%d, %+v, %v) = %q, %v; want %q", c.major, c.img, c.cryptexd, got, err, c.want)
		}
	}
	if classifyMountError(errNoCryptex) != problemNotFor {
		t.Error("an image without a cryptex should read as not for this iPhone")
	}
}

// TestMountStopsWhenThePhoneLocks: a locked phone is not mounted at all,
// and a phone that locks during the mount stops it within a lock watch,
// both as errPhoneLocked, which asks the person to unlock it.
func TestMountStopsWhenThePhoneLocks(t *testing.T) {
	defer func(d time.Duration) { lockWatch = d }(lockWatch)
	lockWatch = 20 * time.Millisecond
	e := entry("phone", 1, "USB")
	var mu sync.Mutex
	locked := true
	state := func(context.Context, goios.DeviceEntry) (phoneState, error) {
		mu.Lock()
		defer mu.Unlock()
		return phoneState{major: 27, locked: locked}, nil
	}
	mounts := 0
	hang := func(ctx context.Context, _ diskImage, major int) (mountResult, error) {
		mounts++
		if major != 27 {
			t.Errorf("mounted for iOS %d", major)
		}
		<-ctx.Done() // go-ios waiting for a phone that refuses
		return mountResult{}, ctx.Err()
	}
	if _, err := mountOnWith(context.Background(), e, diskImage{}, state, hang); !errors.Is(err, errPhoneLocked) || mounts != 0 {
		t.Fatalf("a locked phone: %v after %d mounts; want errPhoneLocked and no mount", err, mounts)
	}
	mu.Lock()
	locked = false
	mu.Unlock()
	go func() {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		locked = true
		mu.Unlock()
	}()
	start := time.Now()
	_, err := mountOnWith(context.Background(), e, diskImage{}, state, hang)
	if !errors.Is(err, errPhoneLocked) || mounts != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("a phone that locked during the mount: %v after %v; want errPhoneLocked within a watch", err, time.Since(start))
	}
	// An unlocked phone mounts as usual.
	mu.Lock()
	locked = false
	mu.Unlock()
	ok := func(context.Context, diskImage, int) (mountResult, error) { return mountResult{How: mountCryptex}, nil }
	if r, err := mountOnWith(context.Background(), e, diskImage{}, state, ok); err != nil || r.How != mountCryptex {
		t.Fatalf("an unlocked phone: %+v, %v", r, err)
	}
}
