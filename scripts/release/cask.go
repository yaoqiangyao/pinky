package main

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// The Homebrew cask of Pinky, Casks/pinky.rb in the tap: the macOS zips of
// the release, which the app is to update itself from (auto_updates), and
// the pinkyctl command for terminals and agents (pinkyctl mcp). The command
// is a wrapper that runs the app's executable by its own path
// (command_wrapper) rather than a symbolic link to it, so that the app
// finds its bundle as it does when it is opened. It is not named pinky:
// coreutils has a pinky command, which Homebrew's coreutils links into the
// same folder, so a cask's pinky was skipped there, and a coreutils
// installed after it could not link.
var caskTemplate = template.Must(template.New("cask").Parse(`cask "pinky" do
  arch arm: "arm64", intel: "amd64"

  version "{{.Version}}"
  sha256 arm:   "{{.ARM}}",
         intel: "{{.Intel}}"

  url "https://github.com/{{.Repo}}/releases/download/v#{version}/pinky-#{version}-darwin-#{arch}.zip"
  name "Pinky"
  desc "Let AI agents use your phone, with you in the loop"
  homepage "https://github.com/{{.Repo}}"

  livecheck do
    url :url
    strategy :github_latest
  end

  auto_updates true
  depends_on macos: :ventura

  app "Pinky.app"
  command_wrapper "pinkyctl",
                  executable: "#{appdir}/Pinky.app/Contents/MacOS/Pinky"

  uninstall quit: "app.pinky.desktop"

  zap trash: [
    "~/Library/Application Support/Pinky",
    "~/Library/Caches/app.pinky.desktop",
    "~/Library/HTTPStorages/app.pinky.desktop",
    "~/Library/Logs/Pinky",
    "~/Library/Preferences/app.pinky.desktop.plist",
    "~/Library/Saved Application State/app.pinky.desktop.savedState",
  ]

  caveats <<~EOS
    Agents start Pinky with: pinkyctl mcp
      claude mcp add pinky -- pinkyctl mcp
    The command was named pinky before, a name coreutils has too: change
    agents that run "pinky mcp" to run "pinkyctl mcp".
  EOS
end
`))

// caskFile is the cask's name among a release's files, from which the
// tap's workflow takes it (scripts/release/homebrew-tap).
const caskFile = "pinky.rb"

var (
	repoRe   = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Cask returns the cask of the release that m describes, published in repo.
func Cask(m *Manifest, repo string) (string, error) {
	if !repoRe.MatchString(repo) {
		return "", fmt.Errorf("%q is not a GitHub repository (owner/name)", repo)
	}
	v, err := ParseVersion(m.Version)
	if err != nil {
		return "", err
	}
	data := struct{ Version, ARM, Intel, Repo string }{Version: v.String(), Repo: repo}
	for _, p := range []struct {
		platform string
		sum      *string
	}{{"darwin-arm64", &data.ARM}, {"darwin-amd64", &data.Intel}} {
		a, ok := m.Assets[p.platform]
		if !ok {
			return "", fmt.Errorf("the manifest has no %s archive", p.platform)
		}
		// The cask builds the URL from the version, as Homebrew's tools
		// expect: it has to be the manifest's.
		if want := downloadURL(repo, v.Tag(), archiveName(p.platform, v.String())); a.URL != want {
			return "", fmt.Errorf("the manifest's %s archive is %s, not %s, which the cask would download", p.platform, a.URL, want)
		}
		if !sha256Re.MatchString(a.SHA256) {
			return "", fmt.Errorf("the manifest's %s archive has no SHA-256", p.platform)
		}
		*p.sum = a.SHA256
	}
	var b strings.Builder
	if err := caskTemplate.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// caskVersionRe finds the version of a cask.
var caskVersionRe = regexp.MustCompile(`(?m)^\s*version\s+"([^"]+)"`)

// caskVersion returns the version a cask installs, or "" when it names none.
func caskVersion(text string) string {
	if m := caskVersionRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}
