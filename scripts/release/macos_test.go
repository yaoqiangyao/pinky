package main

import (
	"debug/macho"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// macOSNames are Homebrew's names for the versions of macOS that a cask's
// depends_on takes.
var macOSNames = map[int]string{
	12: "monterey",
	13: "ventura",
	14: "sonoma",
	15: "sequoia",
	26: "tahoe",
}

// TestMacOSMinimum checks that what Pinky says about its oldest macOS
// matches what the Go linker builds. The linker writes its own minimum into
// every Mac binary (LC_BUILD_VERSION's minos, 13.0 in Go 1.27), and macOS
// refuses to run the binary on an older system whatever Info.plist says.
// So macos.minimumSystemVersion in mygo.json, which becomes Info.plist's
// LSMinimumSystemVersion, must name that version, and the cask's
// depends_on macos must ask for it. Pinky once said 12 while Go built it
// for 13; a Go upgrade that raises the minimum fails here instead.
// scripts/macapp.sh checks the packaged app's own binaries the same way.
func TestMacOSMinimum(t *testing.T) {
	root, err := sourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var built string
	for _, arch := range []string{"arm64", "amd64"} {
		exe := filepath.Join(dir, "main-"+arch)
		cmd := exec.Command("go", "build", "-o", exe, "main.go")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build for darwin/%s: %v\n%s", arch, err, out)
		}
		v, err := machoMinOS(exe)
		if err != nil {
			t.Fatalf("darwin/%s: %v", arch, err)
		}
		if built != "" && v != built {
			t.Fatalf("Go builds darwin/arm64 for macOS %s and darwin/amd64 for macOS %s", built, v)
		}
		built = v
	}

	for _, name := range []string{"cmd/pinky/mygo.json", "cmd/pinky-ui-demo/mygo.json"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			MacOS struct {
				MinimumSystemVersion string `json:"minimumSystemVersion"`
			} `json:"macos"`
		}
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := config.MacOS.MinimumSystemVersion; !sameVersion(got, built) {
			t.Errorf("%s: macos.minimumSystemVersion is %q, but Go builds Mac binaries for macOS %s and later; make them agree, and change the README's and the cask's macOS too", name, got, built)
		}
	}

	m := regexp.MustCompile(`depends_on macos: :([a-z]+)\n`).FindStringSubmatch(caskTemplate.Tree.Root.String())
	if m == nil {
		t.Fatal("the cask has no depends_on macos: :<name>")
	}
	major, _, _ := strings.Cut(built, ".")
	n, _ := strconv.Atoi(major)
	want, ok := macOSNames[n]
	if !ok {
		t.Fatalf("Go builds for macOS %s, whose name in Homebrew this test does not know; add it to macOSNames", built)
	}
	if m[1] != want {
		t.Errorf("the cask depends on macos: :%s, but Go builds for macOS %s (:%s)", m[1], built, want)
	}
}

// machoMinOS returns the minimum macOS that a Mach-O binary asks for: the
// minos of its LC_BUILD_VERSION, as major.minor or major.minor.patch.
func machoMinOS(path string) (string, error) {
	f, err := macho.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	const lcBuildVersion = 0x32
	for _, l := range f.Loads {
		raw := l.Raw()
		if len(raw) < 16 || f.ByteOrder.Uint32(raw) != lcBuildVersion {
			continue
		}
		v := f.ByteOrder.Uint32(raw[12:])
		s := fmt.Sprintf("%d.%d", v>>16, v>>8&0xff)
		if v&0xff != 0 {
			s += fmt.Sprintf(".%d", v&0xff)
		}
		return s, nil
	}
	return "", fmt.Errorf("%s has no LC_BUILD_VERSION", path)
}

// sameVersion reports whether a and b name the same version, so that "13"
// matches "13.0" and "13.0.0".
func sameVersion(a, b string) bool {
	parts := func(s string) [3]int {
		var p [3]int
		for i, f := range strings.SplitN(s, ".", 3) {
			n, err := strconv.Atoi(f)
			if err != nil {
				return [3]int{-1}
			}
			p[i] = n
		}
		return p
	}
	pa := parts(a)
	return pa[0] >= 0 && pa == parts(b)
}

func TestSameVersion(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"13.0", "13.0", true},
		{"13", "13.0", true},
		{"13.0.0", "13.0", true},
		{"12.0", "13.0", false},
		{"13.1", "13.0", false},
		{"", "13.0", false},
		{"thirteen", "13.0", false},
	} {
		if got := sameVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("sameVersion(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
