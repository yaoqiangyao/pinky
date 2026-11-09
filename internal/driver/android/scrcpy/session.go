package scrcpy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// ErrClosed is returned by a session that has ended.
var ErrClosed = errors.New("scrcpy: session closed")

// ErrAppNotFound is returned by StartApp when the server finds no app, or
// more than one, for the name.
var ErrAppNotFound = errors.New("app not found")

// Session is one running server with its sockets.
type Session struct {
	client *adb.Client
	serial string
	scid   uint32
	opt    Options
	jar    string

	shell   net.Conn
	video   net.Conn
	videoR  *bufio.Reader
	control net.Conn

	// DeviceName is the phone's model as the server reports it.
	DeviceName string
	// Codec is the video codec id (CodecH264 and so on); 0 without video.
	Codec uint32

	logs    logHub
	exited  chan struct{} // the server process's output ended
	writeMu sync.Mutex
	appMu   sync.Mutex // one StartApp at a time, since it reads the log

	mu       sync.Mutex
	seq      uint64
	acks     map[uint64]chan struct{}
	clipWait []chan string

	closeOnce sync.Once
	done      chan struct{}
	err       error

	jarOnce sync.Once
	jarDone chan struct{} // closed once the jar is removed after the end
}

// Start pushes the bundled server to the phone, runs it with opt and
// connects its sockets. ctx bounds the setup only.
func Start(ctx context.Context, c *adb.Client, serial string, opt Options) (*Session, error) {
	if opt.NoVideo && opt.NoControl {
		return nil, errors.New("scrcpy: a session needs video or control")
	}
	scid, err := randomSCID()
	if err != nil {
		return nil, err
	}
	s := &Session{
		client: c, serial: serial, scid: scid, opt: opt,
		jar:     fmt.Sprintf("/data/local/tmp/pinky-scrcpy-%08x.jar", scid),
		exited:  make(chan struct{}),
		done:    make(chan struct{}),
		jarDone: make(chan struct{}),
		acks:    map[uint64]chan struct{}{},
	}
	if err := c.Push(ctx, serial, bytes.NewReader(serverJar), s.jar, 0o644, time.Now()); err != nil {
		return nil, fmt.Errorf("scrcpy: push server: %w", err)
	}
	cmd := "CLASSPATH=" + s.jar + " app_process / com.genymobile.scrcpy.Server " + adb.Command(opt.Args(scid)...)
	s.shell, err = c.Dial(ctx, serial, "shell:"+cmd)
	if err != nil {
		s.removeJar()
		return nil, fmt.Errorf("scrcpy: start server: %w", err)
	}
	go s.readLogs()

	if err := s.connect(ctx); err != nil {
		s.Close()
		// The server deletes its jar once it runs; this covers the case
		// where it never got that far.
		s.removeJar()
		return nil, err
	}
	if s.control != nil {
		go s.readDeviceMessages()
	}
	return s, nil
}

func randomSCID() (uint32, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]) & 0x7fffffff, nil
}

func (s *Session) removeJar() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.client.Shell(ctx, s.serial, "rm -f "+s.jar)
}

// connect opens the video and control sockets in the order the server
// accepts them and reads the stream headers.
func (s *Session) connect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var first net.Conn
	if !s.opt.NoVideo {
		conn, err := s.dialSocket(ctx, true)
		if err != nil {
			return err
		}
		s.video = conn
		s.videoR = bufio.NewReaderSize(conn, 256<<10)
		first = conn
	}
	if !s.opt.NoControl {
		conn, err := s.dialSocket(ctx, first == nil)
		if err != nil {
			return err
		}
		s.control = conn
		if first == nil {
			first = conn
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = first.SetReadDeadline(time.Unix(1, 0)) })
	defer stop()
	var r io.Reader = first
	if first == s.video {
		r = s.videoR
	}
	name, err := readDeviceName(r)
	if err != nil {
		return s.startError("read device name", err)
	}
	s.DeviceName = name
	if s.video != nil {
		s.Codec, err = readCodec(s.videoR)
		if err != nil {
			return s.startError("read codec", err)
		}
	}
	if !stop() {
		return s.startError("connect", ctx.Err())
	}
	_ = first.SetReadDeadline(time.Time{})
	return nil
}

// dialSocket connects to the server's abstract socket, retrying until the
// server listens. The first socket carries a dummy byte that proves the
// server accepted it.
func (s *Session) dialSocket(ctx context.Context, dummy bool) (net.Conn, error) {
	name := "localabstract:" + socketName(s.scid)
	var lastErr error
	for {
		conn, err := s.client.Dial(ctx, s.serial, name)
		if err == nil && dummy {
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var b [1]byte
			_, err = io.ReadFull(conn, b[:])
			_ = conn.SetReadDeadline(time.Time{})
			if err != nil {
				conn.Close()
			}
		}
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if errors.Is(err, adb.ErrDeviceNotFound) || errors.Is(err, adb.ErrDeviceOffline) || errors.Is(err, adb.ErrServerNotRunning) {
			return nil, err
		}
		select {
		case <-s.exited:
			return nil, s.startError("server exited", lastErr)
		case <-ctx.Done():
			return nil, s.startError("connect", fmt.Errorf("%w (last error: %v)", ctx.Err(), lastErr))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// startError wraps a setup error with the server's last log lines, which
// usually say what went wrong.
func (s *Session) startError(what string, err error) error {
	// Give the log reader a moment to catch the server's last words.
	select {
	case <-s.exited:
	case <-time.After(300 * time.Millisecond):
	}
	if logs := s.logs.recent(); len(logs) > 0 {
		err = fmt.Errorf("scrcpy: %s: %w; server log: %s", what, err, strings.Join(logs, " | "))
	} else {
		err = fmt.Errorf("scrcpy: %s: %w", what, err)
	}
	if why := s.VideoFailure(); why != "" {
		return &EncoderError{Reason: why, Err: err}
	}
	return err
}

// EncoderError is a start that failed because the server's video encoder
// did, before the server sent the stream's header: a phone with no encoder
// for the codec asked for (a TV without H.265) fails at "read codec".
type EncoderError struct {
	Reason string // the server's log line that says so
	Err    error
}

func (e *EncoderError) Error() string { return e.Err.Error() }
func (e *EncoderError) Unwrap() error { return e.Err }

// readLogs collects the server's output lines until the process ends.
func (s *Session) readLogs() {
	defer close(s.exited)
	sc := bufio.NewScanner(s.shell)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		s.logs.add(strings.TrimRight(sc.Text(), "\r"))
	}
	s.logs.add("") // wake waiters
}

// Logs returns the server's most recent log lines.
func (s *Session) Logs() []string { return s.logs.recent() }

// SubscribeLogs returns the server's log lines from now on, until cancel
// is called. A slow reader misses lines rather than blocking the server's
// output.
func (s *Session) SubscribeLogs() (lines <-chan string, cancel func()) { return s.logs.subscribe() }

// ReadPacket reads the next video packet. Only one goroutine may read.
func (s *Session) ReadPacket() (Packet, error) {
	if s.videoR == nil {
		return Packet{}, errors.New("scrcpy: session has no video")
	}
	p, err := ReadPacket(s.videoR)
	if err != nil {
		s.fail(fmt.Errorf("scrcpy: video stream: %w", err))
		return Packet{}, s.Err()
	}
	return p, nil
}

func (s *Session) readDeviceMessages() {
	r := bufio.NewReader(s.control)
	for {
		m, err := ReadDeviceMessage(r)
		if err != nil {
			s.fail(fmt.Errorf("scrcpy: control channel: %w", err))
			return
		}
		s.mu.Lock()
		switch m.Type {
		case DeviceClipboard:
			for _, ch := range s.clipWait {
				ch <- m.Text
			}
			s.clipWait = nil
		case DeviceAckClipboard:
			if ch, ok := s.acks[m.Sequence]; ok {
				close(ch)
				delete(s.acks, m.Sequence)
			}
		}
		s.mu.Unlock()
	}
}

// Send writes one encoded control message.
func (s *Session) Send(msg []byte) error {
	if s.control == nil {
		return errors.New("scrcpy: session has no control channel")
	}
	select {
	case <-s.done:
		return s.Err()
	default:
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.control.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := s.control.Write(msg); err != nil {
		s.fail(fmt.Errorf("scrcpy: control channel: %w", err))
		return s.Err()
	}
	return nil
}

// Key presses and releases a key.
func (s *Session) Key(keycode, meta int32) error {
	if err := s.Send(InjectKeycode(ActionDown, keycode, 0, meta)); err != nil {
		return err
	}
	return s.Send(InjectKeycode(ActionUp, keycode, 0, meta))
}

// Text types text with the virtual keyboard's key map. Characters the map
// lacks are skipped by the server.
func (s *Session) Text(text string) error {
	for _, part := range SplitText(text, MaxInjectText) {
		msg, err := InjectText(part)
		if err != nil {
			return err
		}
		if err := s.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

// SetClipboard sets the clipboard, presses PASTE when paste is set, and
// waits until the server confirms.
func (s *Session) SetClipboard(ctx context.Context, text string, paste bool) error {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	ack := make(chan struct{})
	s.acks[seq] = ack
	s.mu.Unlock()
	msg, err := SetClipboard(seq, text, paste)
	if err == nil {
		err = s.Send(msg)
	}
	if err != nil {
		s.mu.Lock()
		delete(s.acks, seq)
		s.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-ack:
		return nil
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.acks, seq)
		s.mu.Unlock()
		return fmt.Errorf("scrcpy: set clipboard: %w", ctx.Err())
	}
}

// Clipboard reads the clipboard text. The server sends nothing for an
// empty or non-text clipboard, so a missing answer reads as "".
func (s *Session) Clipboard(ctx context.Context) (string, error) {
	ch := make(chan string, 1)
	s.mu.Lock()
	s.clipWait = append(s.clipWait, ch)
	s.mu.Unlock()
	remove := func() {
		s.mu.Lock()
		for i, c := range s.clipWait {
			if c == ch {
				s.clipWait = append(s.clipWait[:i], s.clipWait[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
	if err := s.Send(GetClipboard(CopyKeyNone)); err != nil {
		remove()
		return "", err
	}
	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	select {
	case text := <-ch:
		return text, nil
	case <-t.C:
		remove()
		return "", nil
	case <-s.done:
		return "", s.Err()
	case <-ctx.Done():
		remove()
		return "", ctx.Err()
	}
}

// StartedApp is what the server reported when it started an app.
type StartedApp struct {
	Name, Package string
	Display       int
}

var startedRE = regexp.MustCompile(`Starting app "(.*)" \[([^\]]+)\] on display (-?\d+)`)

// StartApp starts an app on the session's display and waits until the
// server reports the result. Name is a package, or "?" and a display name;
// a leading "+" force-stops the app first. A lookup by name lists every
// app, which takes a few seconds.
func (s *Session) StartApp(ctx context.Context, name string) (StartedApp, error) {
	msg, err := StartApp(name)
	if err != nil {
		return StartedApp{}, err
	}
	s.appMu.Lock()
	defer s.appMu.Unlock()
	lines, cancel := s.logs.subscribe()
	defer cancel()
	if err := s.Send(msg); err != nil {
		return StartedApp{}, err
	}
	ctx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	for {
		select {
		case line := <-lines:
			if m := startedRE.FindStringSubmatch(line); m != nil {
				d, _ := strconv.Atoi(m[3])
				return StartedApp{Name: m[1], Package: m[2], Display: d}, nil
			}
			switch {
			case strings.Contains(line, "No app found"), strings.Contains(line, "No unique app found"):
				return StartedApp{}, fmt.Errorf("%w: %s", ErrAppNotFound, logMessage(line))
			case strings.Contains(line, "start app"), strings.Contains(line, "Could not start"):
				if strings.Contains(line, "ERROR") || strings.Contains(line, "WARN") {
					return StartedApp{}, fmt.Errorf("scrcpy: %s", logMessage(line))
				}
			}
		case <-s.done:
			return StartedApp{}, s.Err()
		case <-ctx.Done():
			return StartedApp{}, fmt.Errorf("scrcpy: start app %q: %w", name, ctx.Err())
		}
	}
}

// WaitLog returns the first server log line, recent or new, for which
// match is true.
func (s *Session) WaitLog(ctx context.Context, match func(line string) bool) (string, error) {
	lines, cancel := s.logs.subscribe()
	defer cancel()
	for _, l := range s.logs.recent() {
		if match(l) {
			return l, nil
		}
	}
	for {
		select {
		case l := <-lines:
			if l != "" && match(l) {
				return l, nil
			}
		case <-s.done:
			return "", s.Err()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

var newDisplayRE = regexp.MustCompile(`New display: .*\(id=(-?\d+)\)`)

// DisplayID waits for the id of the virtual display the server created
// for Options.Virtual.
func (s *Session) DisplayID(ctx context.Context) (int, error) {
	line, err := s.WaitLog(ctx, func(l string) bool { return newDisplayRE.MatchString(l) })
	if err != nil {
		return 0, err
	}
	id, _ := strconv.Atoi(newDisplayRE.FindStringSubmatch(line)[1])
	return id, nil
}

// logMessage strips the "[server] LEVEL: " prefix.
func logMessage(line string) string {
	if i := strings.Index(line, ": "); i >= 0 && strings.HasPrefix(line, "[server]") {
		return line[i+2:]
	}
	return line
}

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// encoderErrorRE matches the server's log lines for a video encoder that
// fails, as the Android 10 emulator's does (so does its screenrecord).
var encoderErrorRE = regexp.MustCompile(`Capture/encoding error|Exception on thread Thread\[video|Encoder failed|Could not open video stream|MediaCodec\.`)

// VideoFailure returns the first server log line that says the video
// capture or encoder failed, or "" when there is none.
func (s *Session) VideoFailure() string {
	for _, l := range s.logs.recent() {
		if encoderErrorRE.MatchString(l) {
			return logMessage(l)
		}
	}
	return ""
}

// Err says why the session ended, or is nil while it runs.
func (s *Session) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

func (s *Session) fail(err error) {
	s.closeOnce.Do(func() {
		s.err = err
		close(s.done)
		s.closeConns()
		// The server deletes its jar when it starts, but not always when
		// it crashes early (a failing encoder on Android 10): make sure
		// nothing stays on the phone.
		go s.cleanJar()
	})
}

// cleanJar removes the server's jar once.
func (s *Session) cleanJar() {
	s.jarOnce.Do(func() {
		s.removeJar()
		close(s.jarDone)
	})
}

func (s *Session) closeConns() {
	for _, c := range []net.Conn{s.control, s.video, s.shell} {
		if c != nil {
			c.Close()
		}
	}
}

// Close stops the session; the server exits when its sockets close. It
// waits up to 3 s for the server's jar to be gone from the phone.
func (s *Session) Close() error {
	s.fail(ErrClosed)
	select {
	case <-s.jarDone:
	case <-time.After(3 * time.Second):
	}
	return nil
}

// Serial returns the device serial.
func (s *Session) Serial() string { return s.serial }

// logHub keeps recent log lines and fans new ones out to subscribers.
type logHub struct {
	mu    sync.Mutex
	lines []string
	subs  map[chan string]struct{}
}

const logKeep = 40

func (h *logHub) add(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if line != "" {
		h.lines = append(h.lines, line)
		if len(h.lines) > logKeep {
			h.lines = h.lines[len(h.lines)-logKeep:]
		}
	}
	for ch := range h.subs {
		select {
		case ch <- line:
		default: // a slow subscriber misses lines rather than blocking the reader
		}
	}
}

func (h *logHub) recent() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

func (h *logHub) subscribe() (<-chan string, func()) {
	ch := make(chan string, 64)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan string]struct{}{}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}
