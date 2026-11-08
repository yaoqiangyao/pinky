// Package telemetry is Pinky's usage statistics, crash reports and
// feedback. docs/TELEMETRY.md lists everything they hold.
//
// Usage statistics are counters with fixed names, such as
// "tools.phone_tap" or "phones.android.wifi", sent with a random install
// id, Pinky's version and the computer's system, at most once a day. They
// never hold what is on a phone's screen, what is typed, app names, phone
// identifiers, PINs, file names or anything else about the person. They
// are counted and sent only while the setting is on (the default), and
// never while DO_NOT_TRACK is set.
//
// Crash reports come from the Go runtime, which writes a crash to a file
// in the data folder (runtime/debug.SetCrashOutput); the next launch sends
// them under the same setting. Feedback goes to GitHub as an issue that the
// person reads and submits themselves (IssueURL).
//
// Nothing here may stop Pinky: errors are logged and dropped.
package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Endpoint is the base URL of the backend that receives reports, the
// Worker in worker/; the paths in backend.go follow it. Empty sends
// nothing. A build may set it with
// -ldflags "-X github.com/DIYgod/pinky/internal/telemetry.Endpoint=…".
// Development builds never use it (endpoint).
// This build sends nothing: Pinky by flyfox keeps no usage statistics or
// crash reports.
var Endpoint = ""

// EndpointEnv overrides Endpoint, for a staging backend or a test. It also
// lets a development build send, which it otherwise never does.
const EndpointEnv = "PINKY_TELEMETRY_URL"

// DevVersion is the version of a build without -X main.version.
const DevVersion = "0.0.0-dev"

// DoNotTrack reports whether the DO_NOT_TRACK environment variable asks
// programs not to collect usage statistics (consoledonottrack.com).
func DoNotTrack() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DO_NOT_TRACK"))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// How often usage statistics go out, and how the client paces itself.
const (
	sendEvery    = 24 * time.Hour
	persistEvery = 5 * time.Minute
	maxKeys      = 400
	maxBackoff   = 24 * time.Hour
)

// Options configure Open.
type Options struct {
	// Dir is Pinky's data folder. The client keeps its state in
	// telemetry.json there, and crash reports in crashes/.
	Dir string
	// Version is Pinky's version.
	Version string
	// Process is "app" or "daemon".
	Process string
	// Backend receives reports. Nil posts them to Endpoint (or
	// EndpointEnv) over HTTP; with neither, or in a development build
	// without EndpointEnv, nothing is sent.
	Backend Backend
	// Crashes is this process's crash capture (CaptureCrashes); what it
	// found of earlier runs is counted and sent.
	Crashes *Crashes
	// Usage is the setting to start with (SetUsage): usage statistics and
	// crash reports.
	Usage bool
	Log   *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Tick is how often Run looks at what is due; zero is five minutes.
	Tick time.Duration
}

// Client counts usage, keeps it on disk and sends it when due; it also
// sends crash reports. Its methods are safe from any goroutine, and a nil
// *Client does nothing.
type Client struct {
	opt     Options
	backend Backend // nil: nothing is sent
	log     *slog.Logger
	now     func() time.Time
	path    string

	mu      sync.Mutex
	st      state
	usage   bool // Settings › Privacy › Share usage statistics and crash reports
	dirty   bool
	sending bool
	closed  bool
	wake    chan struct{}
	// crashRetry is when crash reports that failed to go out may be tried
	// again.
	crashRetry time.Time
	decided    map[string]time.Time // needs the person answered (markDecided)
	changed    func()               // tests: called after each persist
}

// state is telemetry.json.
type state struct {
	InstallID string `json:"install_id"`
	// Salt keys the hashes that count each phone once a period; the
	// hashes never leave the computer.
	Salt        string    `json:"salt"`
	LastSent    time.Time `json:"last_sent,omitzero"`
	PeriodStart time.Time `json:"period_start,omitzero"`
	// Counts add up; Max keeps the highest value seen; Phones holds, per
	// bucket such as "android.usb", the hashed ids of the phones seen.
	Counts map[string]int64    `json:"counts,omitempty"`
	Max    map[string]int64    `json:"max,omitempty"`
	Phones map[string][]string `json:"phones,omitempty"`
	// Counted are the crash reports already counted (crashes.<process>).
	Counted []string `json:"counted_crashes,omitempty"`
	// Failures and NextTry back off after failed sends.
	Failures int       `json:"failures,omitempty"`
	NextTry  time.Time `json:"next_try,omitzero"`
}

// Open loads the client's state from opt.Dir. It always returns a usable
// client: a state it cannot read starts over. Open it once the gateway's
// lock is held, so one process at a time owns telemetry.json.
func Open(opt Options) *Client {
	if opt.Log == nil {
		opt.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Tick <= 0 {
		opt.Tick = persistEvery
	}
	if opt.Process == "" {
		opt.Process = "app"
	}
	c := &Client{opt: opt, log: opt.Log, now: opt.Now, wake: make(chan struct{}, 1), usage: opt.Usage}
	c.backend = opt.Backend
	if c.backend == nil {
		if url := endpoint(opt.Version); url != "" {
			c.backend = &HTTP{URL: url, Version: opt.Version}
		}
	}
	if opt.Dir != "" {
		c.path = filepath.Join(opt.Dir, "telemetry.json")
		if err := os.MkdirAll(opt.Dir, 0o700); err != nil {
			c.log.Warn("telemetry: data folder", "err", err)
		}
		c.load()
	}
	if c.st.InstallID == "" {
		c.st.InstallID = randomHex(16)
		c.dirty = true
	}
	if c.st.Salt == "" {
		c.st.Salt = randomHex(16)
		c.dirty = true
	}
	if !c.usage && c.keysLocked() > 0 {
		// Turned off while another run had counts: forget them.
		c.st.Counts, c.st.Max, c.st.Phones, c.st.PeriodStart = nil, nil, nil, time.Time{}
		c.dirty = true
	}
	c.countCrashes(opt.Crashes)
	return c
}

// endpoint is where an HTTP backend posts, or "" for none.
func endpoint(version string) string {
	if v := strings.TrimSpace(os.Getenv(EndpointEnv)); v != "" {
		if v == "-" || v == "off" {
			return ""
		}
		return strings.TrimRight(v, "/")
	}
	if version == "" || version == DevVersion {
		return "" // a development build: the developer's own runs are not usage
	}
	return strings.TrimRight(Endpoint, "/")
}

func (c *Client) load() {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			c.log.Warn("telemetry: state unreadable; starting over", "err", err)
		}
		return
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		c.log.Warn("telemetry: state unreadable; starting over", "err", err)
		return
	}
	c.st = st
}

// persist writes the state when it changed.
func (c *Client) persist() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.dirty || c.path == "" {
		c.mu.Unlock()
		return
	}
	data, err := json.MarshalIndent(c.st, "", "  ")
	c.dirty = false
	changed := c.changed
	c.mu.Unlock()
	if err == nil {
		err = writeFile(c.path, append(data, '\n'))
	}
	if err != nil {
		c.log.Warn("telemetry: save state", "err", err)
	}
	if changed != nil {
		changed()
	}
}

// writeFile replaces path atomically, readable only by the user.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on supported systems; a time-based id
		// still keeps installs apart.
		sum := sha256.Sum256([]byte(time.Now().String()))
		return hex.EncodeToString(sum[:n])
	}
	return hex.EncodeToString(b)
}

// --- settings ---

// SetUsage turns usage statistics and crash reports on or off, from
// Settings › Privacy. Turning them off forgets what was counted and not yet
// sent; crash reports stay on this computer.
func (c *Client) SetUsage(on bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	was := c.usage
	c.usage = on
	if was && !on {
		c.st.Counts, c.st.Max, c.st.Phones = nil, nil, nil
		c.st.PeriodStart = time.Time{}
		c.dirty = true
	}
	c.mu.Unlock()
	if was && !on {
		c.persist()
	}
	c.poke()
}

// Usage reports whether usage statistics are counted: the setting is on
// and DO_NOT_TRACK is not set.
func (c *Client) Usage() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage && !DoNotTrack()
}

// CanSend reports whether this build has a backend to send to.
func (c *Client) CanSend() bool { return c != nil && c.backend != nil }

// InstallID is the random id usage statistics carry.
func (c *Client) InstallID() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st.InstallID
}

// --- counting ---

// Count adds one to a counter.
func (c *Client) Count(name string) { c.Add(name, 1) }

// Add adds n to a counter. Names are dotted words (Key builds them);
// anything else in a name becomes "_".
func (c *Client) Add(name string, n int64) {
	if c == nil || n == 0 {
		return
	}
	name = cleanName(name)
	if name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.usage || c.closed {
		return
	}
	if c.st.Counts == nil {
		c.st.Counts = map[string]int64{}
	}
	if _, ok := c.st.Counts[name]; !ok && c.keysLocked() >= maxKeys {
		return
	}
	c.st.Counts[name] += n
	c.startPeriodLocked()
	c.dirty = true
}

// Max keeps the highest value a gauge reached this period, such as the
// most phones connected at once.
func (c *Client) Max(name string, v int64) {
	if c == nil {
		return
	}
	name = cleanName(name)
	if name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.usage || c.closed {
		return
	}
	if c.st.Max == nil {
		c.st.Max = map[string]int64{}
	}
	old, ok := c.st.Max[name]
	if !ok && c.keysLocked() >= maxKeys {
		return
	}
	if !ok || v > old {
		c.st.Max[name] = v
		c.startPeriodLocked()
		c.dirty = true
	}
}

// SeePhone counts a phone once a period in a bucket such as
// "android.wifi". The id is kept only as a salted hash, on this computer;
// the report carries how many phones each bucket saw.
func (c *Client) SeePhone(bucket, id string) {
	if c == nil || id == "" {
		return
	}
	bucket = cleanName(bucket)
	if bucket == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.usage || c.closed {
		return
	}
	sum := sha256.Sum256([]byte(c.st.Salt + "\x00" + id))
	h := hex.EncodeToString(sum[:6])
	if c.st.Phones == nil {
		c.st.Phones = map[string][]string{}
	}
	set, ok := c.st.Phones[bucket]
	if !ok && c.keysLocked() >= maxKeys {
		return
	}
	for _, x := range set {
		if x == h {
			return
		}
	}
	if len(set) >= 50 {
		return // a bucket counts up to 50 phones
	}
	c.st.Phones[bucket] = append(set, h)
	c.startPeriodLocked()
	c.dirty = true
}

func (c *Client) keysLocked() int { return len(c.st.Counts) + len(c.st.Max) + len(c.st.Phones) }

func (c *Client) startPeriodLocked() {
	if c.st.PeriodStart.IsZero() {
		c.st.PeriodStart = c.now()
	}
}

// Key joins the parts of a counter name, each in lower case with
// anything but letters and digits as "_": Key("runs", "Claude Code") is
// "runs.claude_code".
func Key(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = cleanPart(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ".")
}

func cleanName(name string) string {
	return Key(strings.Split(name, ".")...)
}

func cleanPart(p string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(strings.TrimSpace(p)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
		} else if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// --- reports ---

// Report is the usage report that would go out now: what Settings ›
// Privacy shows as what is shared.
func (c *Client) Report() Usage {
	if c == nil {
		return Usage{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reportLocked()
}

func (c *Client) reportLocked() Usage {
	sys := System()
	u := Usage{InstallID: c.st.InstallID, Version: c.opt.Version, OS: sys.OS, OSVersion: sys.OSVersion, Arch: sys.Arch,
		PeriodStart: c.st.PeriodStart, PeriodEnd: c.now(), Counts: map[string]int64{}}
	if u.PeriodStart.IsZero() {
		u.PeriodStart = u.PeriodEnd
	}
	for k, v := range c.st.Counts {
		u.Counts[k] = v
	}
	for k, v := range c.st.Max {
		u.Counts[k] = v
	}
	for k, set := range c.st.Phones {
		u.Counts["phones."+k] = int64(len(set))
	}
	return u
}

// due reports whether a usage report should go out now.
func (c *Client) dueLocked(now time.Time) bool {
	switch {
	case c.backend == nil, !c.usage, c.closed, c.sending, DoNotTrack():
		return false
	case c.keysLocked() == 0, c.st.PeriodStart.IsZero():
		return false
	case now.Sub(c.st.PeriodStart) < sendEvery:
		return false
	case !c.st.NextTry.IsZero() && now.Before(c.st.NextTry):
		return false
	}
	return true
}

// Run persists the counts and sends what is due until ctx ends: crash
// reports at once, usage statistics once a day.
func (c *Client) Run(ctx context.Context) {
	if c == nil {
		return
	}
	t := time.NewTicker(c.opt.Tick)
	defer t.Stop()
	for {
		c.sendCrashes(ctx)
		c.sendUsage(ctx)
		c.persist()
		select {
		case <-ctx.Done():
			c.persist()
			return
		case <-t.C:
		case <-c.wake:
		}
	}
}

// poke makes Run look at what is due now.
func (c *Client) poke() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// sendUsage sends the usage report when it is due. What was counted while
// it was on its way stays for the next one.
func (c *Client) sendUsage(ctx context.Context) {
	c.mu.Lock()
	now := c.now()
	if !c.dueLocked(now) {
		c.mu.Unlock()
		return
	}
	rep := c.reportLocked()
	c.sending = true
	c.mu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := c.backend.Usage(sctx, rep)
	cancel()

	c.mu.Lock()
	c.sending = false
	now = c.now()
	if err != nil && !Permanent(err) {
		c.st.Failures++
		c.st.NextTry = now.Add(backoff(c.st.Failures))
		c.dirty = true
		c.mu.Unlock()
		c.log.Info("telemetry: usage statistics not sent; trying later", "err", err)
		return
	}
	if err != nil {
		c.log.Warn("telemetry: usage statistics refused; dropped", "err", err)
	}
	for k, v := range rep.Counts {
		if strings.HasPrefix(k, "phones.") {
			continue
		}
		if _, ok := c.st.Counts[k]; ok {
			if c.st.Counts[k] -= v; c.st.Counts[k] <= 0 {
				delete(c.st.Counts, k)
			}
		}
	}
	c.st.Max, c.st.Phones = nil, nil
	c.st.PeriodStart = time.Time{}
	if len(c.st.Counts) > 0 {
		c.st.PeriodStart = now
	}
	c.st.LastSent, c.st.Failures, c.st.NextTry = now, 0, time.Time{}
	c.dirty = true
	c.mu.Unlock()
}

// backoff is how long to wait after the nth failed send in a row: an hour,
// doubling, up to a day.
func backoff(n int) time.Duration {
	d := time.Hour
	for i := 1; i < n && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

// Close saves what was counted. Counting stops.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.persist()
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// ErrNoBackend is returned when this build has nowhere to send to.
var ErrNoBackend = errors.New("this build of Pinky has no address to send reports to")

// --- the package's client ---

var std atomic.Pointer[Client]

// SetDefault makes c the client that Count and Add use.
func SetDefault(c *Client) { std.Store(c) }

// Default is the client SetDefault set, or nil.
func Default() *Client { return std.Load() }

// Count adds one to a counter of the default client.
func Count(name string) { std.Load().Count(name) }

// Add adds n to a counter of the default client.
func Add(name string, n int64) { std.Load().Add(name, n) }

// System is the computer's system, as reports name it.
type SystemInfo struct {
	OS        string `json:"os"`         // "macos", "windows", "linux"
	OSVersion string `json:"os_version"` // "27.2", "10.0.26100", "ubuntu 24.04"
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
}

var (
	sysOnce sync.Once
	sysInfo SystemInfo
)

// OSName is how people write a system's name: "macOS" for "macos".
func OSName(os string) string {
	switch os {
	case "macos", "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	}
	return os
}

// System returns the computer's system, read once.
func System() SystemInfo {
	sysOnce.Do(func() {
		name := runtime.GOOS
		if name == "darwin" {
			name = "macos"
		}
		sysInfo = SystemInfo{OS: name, OSVersion: osVersion(), Arch: runtime.GOARCH, GoVersion: runtime.Version()}
	})
	return sysInfo
}
