package telemetry

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// The logs diagnostics end with, in the data folder, and how much of each.
var diagnosticLogs = []string{"pinky.log", "daemon.log"}

const (
	logTailBytes = 96 << 10
	logTailLines = 300
)

// Diagnostics is what the person may attach to feedback: a summary
// (Pinky's version and system, the settings, the last crash, the phones'
// platforms, models and systems) and the end of Pinky's logs. Phone ids
// and names, the home folder, the user name, e-mail and IP addresses, and
// log fields that may name apps, tasks or files are taken out (Redactor).
// The person reads it before it goes anywhere.
type Diagnostics struct {
	Summary string
	Logs    []LogTail
}

// LogTail is the end of one log, oldest line first.
type LogTail struct {
	Name  string
	Lines []string
}

// String is the whole text.
func (d Diagnostics) String() string { return d.Text(-1) }

// Text is the summary with at most the last maxLines lines of each log; a
// negative maxLines keeps them all.
func (d Diagnostics) Text(maxLines int) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(d.Summary, "\n"))
	b.WriteByte('\n')
	for _, l := range d.Logs {
		lines := l.Lines
		if maxLines >= 0 && len(lines) > maxLines {
			lines = lines[len(lines)-maxLines:]
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s, the last lines:\n", l.Name)
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteByte('\n')
	}
	out := b.String()
	if len(out) > maxDiagnostics {
		out = "… (cut)\n" + out[len(out)-maxDiagnostics+10:]
	}
	return out
}

// Diagnostics reads the diagnostics now: extra lines from the caller
// (settings, the video decoder) go in the summary.
func (c *Client) Diagnostics(phones []driver.Info, extra ...string) Diagnostics {
	red := NewRedactor()
	for _, p := range phones {
		red.Phone(p.ID, p.Name, p.Model)
	}
	var d Diagnostics
	if c != nil && c.opt.Dir != "" {
		for _, name := range diagnosticLogs {
			tail, err := readTail(filepath.Join(c.opt.Dir, name), logTailBytes, logTailLines)
			if err != nil || strings.TrimSpace(tail) == "" {
				continue
			}
			red.Learn(tail)
			d.Logs = append(d.Logs, LogTail{Name: name, Lines: strings.Split(tail, "\n")})
		}
		for i := range d.Logs {
			for j, line := range d.Logs[i].Lines {
				d.Logs[i].Lines[j] = red.String(line)
			}
		}
	}

	var b strings.Builder
	sys := System()
	version := DevVersion
	if c != nil && c.opt.Version != "" {
		version = c.opt.Version
	}
	fmt.Fprintf(&b, "Pinky %s · %s %s · %s · %s\n", version, OSName(sys.OS), sys.OSVersion, sys.Arch, sys.GoVersion)
	if c != nil {
		stats := "off"
		switch {
		case DoNotTrack():
			stats = "off (DO_NOT_TRACK)"
		case c.Usage():
			stats = "on"
		}
		fmt.Fprintf(&b, "Usage statistics and crash reports: %s\n", stats)
		if crashes := c.Crashes(); len(crashes) > 0 {
			r := crashes[0]
			fmt.Fprintf(&b, "Last crash: %s (%s, Pinky %s): %s\n", r.Crashed.UTC().Format(time.DateTime), r.Process, r.Version, r.Reason)
		}
	}
	for _, line := range extra {
		if line = strings.TrimSpace(line); line != "" {
			b.WriteString(red.String(line))
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\nPhones (%d):\n", len(phones))
	if len(phones) == 0 {
		b.WriteString("  none connected\n")
	}
	for _, p := range phones {
		parts := []string{strings.TrimSpace(string(p.Platform) + " " + p.OSVersion)}
		for _, s := range []string{p.Model, p.Connection, p.Backend, string(p.State), wifiLine(p.WiFi)} {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		b.WriteString("  " + red.String(strings.Join(parts, " · ")) + "\n")
	}
	d.Summary = b.String()
	return d
}

// wifiLine says what Pinky knows of an iPhone's Wi-Fi connection
// (driver.Info.WiFi), "" for other phones: "wifi connections turned_on,
// not seen on wifi".
func wifiLine(w driver.WiFiConnection) string {
	if w.Connections == "" {
		return ""
	}
	s := "wifi connections " + w.Connections
	if w.Seen {
		s += ", seen on wifi"
	} else {
		s += ", not seen on wifi"
	}
	if w.Detail != "" {
		s += " (" + w.Detail + ")"
	}
	return s
}

// readTail returns the last lines of a file, at most max bytes and lines,
// starting at a line.
func readTail(path string, max int64, lines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := int64(0)
	if info.Size() > max {
		start = info.Size() - max
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return "", err
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	all := strings.Split(strings.TrimRight(strings.ToValidUTF8(string(data), "?"), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}
