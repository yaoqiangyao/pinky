package agents

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// An MCP bundle (.mcpb) installs an MCP server in Claude Desktop with a
// double-click: a zip with manifest.json at its root
// (https://github.com/modelcontextprotocol/mcpb). Pinky's does not carry
// Pinky, which is an app of its own that agents share: it starts the Pinky
// installed on the computer. On macOS a small launcher does, looking in
// Applications, ~/Applications and for the cask's pinkyctl; on Windows the
// bundle names the installer's Pinky.exe.

// BundleTool is a tool the bundle's manifest lists for Claude Desktop to
// show.
type BundleTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// BundleOptions say what goes in a bundle.
type BundleOptions struct {
	Version string
	Icon    []byte // PNG
	Tools   []BundleTool
	// Command, when set, is the Pinky the bundle starts: a bundle made for
	// this computer (GOOS) alone. Empty is the release's bundle, which
	// finds Pinky where its installers put it, on macOS and Windows.
	Command string
	GOOS    string
}

// launcher finds the installed Pinky and starts its MCP server; $PINKY,
// which the person may set in the extension's settings in Claude Desktop
// (or a bundle made for this computer sets), comes first.
const launcher = `#!/bin/sh
# Starts the Pinky installed on this computer as an MCP server for Claude Desktop.
for p in "$PINKY" /Applications/Pinky.app/Contents/MacOS/Pinky "$HOME/Applications/Pinky.app/Contents/MacOS/Pinky"; do
	case $p in /*) [ -x "$p" ] && exec "$p" mcp ;; esac
done
for p in /opt/homebrew/bin/pinkyctl /usr/local/bin/pinkyctl /usr/bin/pinkyctl "$HOME/.local/bin/pinkyctl"; do
	[ -x "$p" ] && exec "$p" mcp
done
echo "Pinky is not installed. Get it from https://github.com/yaoqiangyao/pinky/releases/latest" >&2
exit 1
`

// manifest is the bundle's manifest.json.
func manifest(o BundleOptions) object {
	m := object{
		{"manifest_version", "0.3"},
		{"name", "pinky"},
		{"display_name", "Pinky"},
		{"version", o.Version},
		{"description", "Let Claude use your iPhone or Android phone, with you watching and approving the risky steps."},
		{"long_description", "Pinky lets Claude operate your real iPhone or Android phone, while you watch on your computer, approve risky steps such as payments, take over and review what happened. " +
			"This extension starts the Pinky app installed on your computer (https://github.com/yaoqiangyao/pinky/releases/latest); install Pinky first. " +
			"The first time Claude connects, Pinky asks you to allow it."},
		{"author", object{{"name", "DIYgod"}, {"url", "https://github.com/DIYgod"}}},
		{"homepage", "https://github.com/yaoqiangyao/pinky"},
		{"documentation", "https://github.com/yaoqiangyao/pinky/blob/main/docs/mcp.md"},
		{"support", "https://github.com/yaoqiangyao/pinky/issues"},
		{"repository", object{{"type", "git"}, {"url", "https://github.com/yaoqiangyao/pinky"}}},
		{"license", "Apache-2.0"},
		{"icon", "icon.png"},
		{"server", object{
			{"type", "binary"},
			{"entry_point", "server/pinky.sh"},
			{"mcp_config", mcpConfig(o)},
		}},
	}
	if o.Command == "" {
		// The release's bundle: the person may point it at Pinky elsewhere.
		m = append(m, field{"user_config", object{
			{"pinky", object{
				{"type", "file"},
				{"title", "Pinky"},
				{"description", "Only when Pinky is not in Applications: Pinky's program, Pinky.app/Contents/MacOS/Pinky."},
				{"required", false},
			}},
		}})
	}
	return append(m,
		field{"tools", o.Tools},
		field{"keywords", []string{"phone", "android", "iphone", "mobile", "automation"}},
		field{"compatibility", object{{"platforms", platforms(o)}}},
	)
}

// mcpConfig is how Claude Desktop starts the bundle's server.
func mcpConfig(o BundleOptions) object {
	launch := object{{"command", "/bin/sh"}, {"args", []string{"${__dirname}/server/pinky.sh"}}}
	switch {
	case o.Command == "":
		return append(launch,
			field{"env", object{{"PINKY", "${user_config.pinky}"}}},
			field{"platform_overrides", object{
				{"win32", object{{"command", `${HOME}\AppData\Local\Programs\Pinky\Pinky.exe`}, {"args", []string{"mcp"}}}},
			}})
	case o.GOOS == "windows":
		return object{{"command", o.Command}, {"args", []string{"mcp"}}}
	}
	return append(launch, field{"env", object{{"PINKY", o.Command}}})
}

// platforms are the systems a bundle runs on, as MCPB names them.
func platforms(o BundleOptions) []string {
	switch {
	case o.Command == "":
		return []string{"darwin", "win32"}
	case o.GOOS == "windows":
		return []string{"win32"}
	case o.GOOS == "linux":
		return []string{"linux"}
	}
	return []string{"darwin"}
}

// WriteBundle writes Pinky's MCP bundle for Claude Desktop.
func WriteBundle(w io.Writer, o BundleOptions) error {
	compact, err := marshal(manifest(o))
	if err != nil {
		return err
	}
	var m bytes.Buffer
	if err := json.Indent(&m, compact, "", "  "); err != nil {
		return err
	}
	z := zip.NewWriter(w)
	// A fixed time keeps the same bundle byte for byte.
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range []struct {
		name string
		data []byte
		mode fs.FileMode
	}{
		{"manifest.json", append(m.Bytes(), '\n'), 0o644},
		{"icon.png", o.Icon, 0o644},
		{"server/pinky.sh", []byte(launcher), 0o755},
	} {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: mod}
		h.SetMode(f.mode)
		fw, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := fw.Write(f.data); err != nil {
			return err
		}
	}
	return z.Close()
}

// bundleApp is an app that installs MCP bundles: Claude Desktop.
type bundleApp struct {
	macID string // its bundle id, which macOS opens the bundle with
}

// openBundle writes Pinky's MCP bundle for this computer, which starts
// this Pinky, into Pinky's folder, and opens it in the app, which asks the
// person to install it.
func (s *Setup) openBundle(ctx context.Context, app *bundleApp) (string, error) {
	if s.Dir == "" {
		return "", errors.New("Pinky has no folder for its extension")
	}
	o := s.Bundle
	o.Command, o.GOOS = s.Server.Command, s.Env.GOOS
	if o.Version == "" {
		o.Version = "0.0.0"
	}
	var b bytes.Buffer
	if err := WriteBundle(&b, o); err != nil {
		return "", err
	}
	path := filepath.Join(s.Dir, "Pinky.mcpb")
	if err := writeFile(path, b.Bytes()); err != nil {
		return "", err
	}
	name, args := "xdg-open", []string{path}
	switch s.Env.GOOS {
	case "darwin":
		name, args = "/usr/bin/open", []string{"-b", app.macID, path}
	case "windows":
		name, args = "rundll32.exe", []string{"url.dll,FileProtocolHandler", path}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if out, err := s.Env.Run(ctx, name, args...); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return path, fmt.Errorf("Pinky could not open its extension in the app (%s); drag %s into the app's Settings › Extensions", msg, path)
	}
	return path, nil
}
