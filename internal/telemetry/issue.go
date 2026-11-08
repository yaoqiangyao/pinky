package telemetry

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Feedback goes to GitHub as an issue: Pinky opens a new issue with the
// person's words filled in, and they read it and submit it with their own
// GitHub account. The issue is public.
const (
	// IssueRepo is the repository that takes feedback.
	IssueRepo = "yaoqiangyao/pinky"
	// IssueTemplate is its issue form, .github/ISSUE_TEMPLATE/feedback.yml;
	// the query parameters below fill its fields by id.
	IssueTemplate = "feedback.yml"
	// maxIssueURL keeps a new issue's address within what GitHub takes.
	maxIssueURL = 7500
)

// Feedback is what the person wrote, for IssueURL.
type Feedback struct {
	Title   string
	Message string
	// Version is the line that names Pinky's version and the system
	// (VersionLine); it goes in whether or not diagnostics do.
	Version string
	// Diagnostics are attached when not nil.
	Diagnostics *Diagnostics
}

// VersionLine names Pinky's version and the computer's system, such as
// "Pinky 0.2.0 · macOS 27.2 · arm64".
func (c *Client) VersionLine() string {
	version := DevVersion
	if c != nil && c.opt.Version != "" {
		version = c.opt.Version
	}
	sys := System()
	return strings.TrimSpace(fmt.Sprintf("Pinky %s · %s %s · %s", version, OSName(sys.OS), sys.OSVersion, sys.Arch))
}

// IssueURL returns the address of a new GitHub issue with the feedback
// filled in. The title is the message's first line when empty. What does
// not fit in an address is left out, oldest log lines first, then the
// diagnostics, then the end of the message; cut reports that something
// was, so the person can paste the rest (Feedback's text, from Copy).
func IssueURL(f Feedback) (u string, cut bool) {
	message := strings.TrimSpace(f.Message)
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = firstLine(message, 80)
	}
	build := func(message, diag string) string {
		q := url.Values{}
		q.Set("template", IssueTemplate)
		q.Set("title", title)
		q.Set("message", message)
		if f.Version != "" {
			q.Set("version", f.Version)
		}
		if diag != "" {
			q.Set("diagnostics", diag)
		}
		return "https://github.com/" + IssueRepo + "/issues/new?" + q.Encode()
	}
	if d := f.Diagnostics; d != nil {
		most := 0
		for _, l := range d.Logs {
			most = max(most, len(l.Lines))
		}
		for _, n := range []int{-1, 200, 120, 80, 60, 40, 30, 20, 15, 10, 5, 0} {
			if u := build(message, d.Text(n)); len(u) <= maxIssueURL {
				return u, n >= 0 && n < most
			}
		}
	}
	if u := build(message, ""); len(u) <= maxIssueURL {
		return u, f.Diagnostics != nil
	}
	// A message too long for an address: as much as fits.
	runes := []rune(message)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if len(build(string(runes[:mid])+" …", "")) <= maxIssueURL {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return build(string(runes[:lo])+" …", ""), true
}

// Text is the feedback as text, for Copy: the message, the version line
// and the whole diagnostics.
func (f Feedback) Text() string {
	var b strings.Builder
	if t := strings.TrimSpace(f.Title); t != "" {
		b.WriteString(t + "\n\n")
	}
	b.WriteString(strings.TrimSpace(f.Message))
	if f.Version != "" {
		b.WriteString("\n\n" + f.Version)
	}
	if f.Diagnostics != nil {
		b.WriteString("\n\n---\n" + f.Diagnostics.String())
	}
	return b.String()
}

// firstLine is a text's first line, at most n characters.
func firstLine(s string, n int) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > n {
		line = string([]rune(line)[:n-1]) + "…"
	}
	return line
}
