// These tests stop daemons with SIGTERM and check Unix file modes, which
// Windows lacks; CI's smoke run (scripts/smoke.sh) covers the daemon there.

//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/gateway"
)

// These tests build the pinky binary and run `pinky daemon --fake` and
// `pinky mcp` as real processes, the way an agent would.

func buildPinky(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets and signals")
	}
	bin := filepath.Join(t.TempDir(), "pinky")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// pinkyHome returns a short PINKY_HOME: socket paths must fit in 104 bytes.
func pinkyHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "pinky-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return home
}

func env(home string) []string { return append(os.Environ(), api.HomeEnv+"="+home) }

func dirsFor(t *testing.T, home string) api.Dirs {
	t.Setenv(api.HomeEnv, home)
	d, err := api.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func waitSocket(t *testing.T, d api.Dirs) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := api.Dial(d); err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the gateway socket never came up")
}

type mcpClient struct {
	cs *sdk.ClientSession
}

func startMCP(t *testing.T, bin, home, clientName string, args ...string) *mcpClient {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"mcp"}, args...)...)
	cmd.Env = env(home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cl := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: "e2e"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := cl.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to pinky mcp: %v\n%s", err, stderr.String())
	}
	t.Cleanup(func() { cs.Close() })
	return &mcpClient{cs: cs}
}

func (c *mcpClient) call(t *testing.T, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, ct := range res.Content {
		if tc, ok := ct.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

var refRe = regexp.MustCompile(`\[(\d+)\] \w+ "([^"]*)"`)

func refOf(t *testing.T, text, label string) int {
	t.Helper()
	for _, m := range refRe.FindAllStringSubmatch(text, -1) {
		if m[2] == label {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	t.Fatalf("%q not in:\n%s", label, text)
	return 0
}

func run(t *testing.T, bin, home string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env(home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pinky %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestEndToEnd(t *testing.T) {
	bin := buildPinky(t)
	home := pinkyHome(t)
	d := dirsFor(t, home)

	daemon := exec.Command(bin, "daemon", "--fake")
	daemon.Env = env(home)
	var logs bytes.Buffer
	daemon.Stderr = &logs
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			daemon.Process.Kill()
		}
		if t.Failed() {
			t.Logf("daemon log:\n%s", logs.String())
		}
	}()
	waitSocket(t, d)

	// One gateway per user.
	second := exec.Command(bin, "daemon", "--fake")
	second.Env = env(home)
	if out, err := second.CombinedOutput(); err == nil || !strings.Contains(string(out), "already running") {
		t.Fatalf("second daemon: %v\n%s", err, out)
	}

	out := run(t, bin, home, "devices")
	if !strings.Contains(out, "Pixel 9 (fake)") || !strings.Contains(out, "iPhone 17 (fake)") {
		t.Fatalf("devices:\n%s", out)
	}

	agent := startMCP(t, bin, home, "claude-code")
	tools, err := agent.cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 14 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	text, isErr := agent.call(t, "phone_connect", map[string]any{"device": "Pixel 9 (fake)", "task": "Reorder last week's lunch"})
	if isErr || !strings.HasPrefix(text, "settled · Pixel 9 (fake)") {
		t.Fatalf("connect:\n%s", text)
	}
	text, _ = agent.call(t, "phone_launch", map[string]any{"app": "Dinely"})
	text, _ = agent.call(t, "phone_tap", map[string]any{"ref": refOf(t, text, "Orders")})
	text, _ = agent.call(t, "phone_tap", map[string]any{"ref": refOf(t, text, "Reorder · Chicken rice bowl")})
	text, _ = agent.call(t, "phone_tap", map[string]any{"ref": refOf(t, text, "Cart · 1")})
	place := refOf(t, text, "Place order · $12.80")

	// The tap waits for the person; a control client answers, which a
	// gateway with fake phones allows.
	type result struct {
		text  string
		isErr bool
	}
	done := make(chan result, 1)
	go func() {
		text, isErr := agent.call(t, "phone_tap", map[string]any{"ref": place, "intent": "Pay $12.80 for 1 chicken rice bowl"})
		done <- result{text, isErr}
	}()
	ctl, err := api.DialControl(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	var needs []api.NeedInfo
	for i := 0; i < 200 && len(needs) == 0; i++ {
		if err := ctl.Call("needs", nil, &needs); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(needs) != 1 || needs[0].Rule != "Payments" || !needs[0].HasScreenshot {
		t.Fatalf("needs: %+v", needs)
	}
	if out := run(t, bin, home, "needs"); !strings.Contains(out, needs[0].ID) {
		t.Fatalf("pinky needs:\n%s", out)
	}
	run(t, bin, home, "decide", needs[0].ID, "allow", "enjoy")
	r := <-done
	if r.isErr || !strings.HasPrefix(r.text, "settled") || !strings.Contains(r.text, "Order placed") || !strings.Contains(r.text, `note: "enjoy"`) {
		t.Fatalf("approved tap:\n%s", r.text)
	}

	// Credentials are refused.
	text, _ = agent.call(t, "phone_tap", map[string]any{"ref": refOf(t, r.text, "Back to home")})
	text, _ = agent.call(t, "phone_tap", map[string]any{"ref": refOf(t, text, "Account")})
	text, _ = agent.call(t, "phone_type", map[string]any{"ref": refOf(t, text, "Password"), "text": "hunter2"})
	if !strings.HasPrefix(text, "blocked_by_policy") || !strings.Contains(text, "rule: credentials") {
		t.Fatalf("password:\n%s", text)
	}

	// A second agent finds the phone held.
	other := startMCP(t, bin, home, "codex")
	text, isErr = other.call(t, "phone_connect", map[string]any{"device": "fake-pixel-9", "task": "Something else"})
	if !isErr || !strings.Contains(text, "lease_held") || !strings.Contains(text, "Reorder last week's lunch") {
		t.Fatalf("lease_held:\n%s", text)
	}
	text, isErr = other.call(t, "phone_connect", map[string]any{"device": "fake-iphone-17", "task": "On the iPhone"})
	if isErr || !strings.Contains(text, "iPhone 17 (fake)") {
		t.Fatalf("connect to the iPhone:\n%s", text)
	}
	if text, _ = other.call(t, "phone_key", map[string]any{"key": "recents"}); !strings.Contains(text, "unsupported") {
		t.Fatalf("recents on iPhone:\n%s", text)
	}

	text, _ = agent.call(t, "phone_release", map[string]any{"outcome": "done", "summary": "Ordered the chicken rice bowl."})
	if !strings.HasPrefix(text, "settled") {
		t.Fatalf("release:\n%s", text)
	}

	// The task is in history, with its steps.
	var hist []gateway.TaskView
	if err := ctl.Call("history", api.HistoryParams{Search: "lunch"}, &hist); err != nil || len(hist) != 1 {
		t.Fatalf("history: %+v %v", hist, err)
	}
	if hist[0].State != gateway.TaskDone || len(hist[0].Steps) < 6 {
		t.Fatalf("history task: %+v", hist[0])
	}

	// Closing the agent's stdin ends its session and frees the iPhone.
	other.cs.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var devs []gateway.DeviceView
		ctl.Call("devices", nil, &devs)
		busy := false
		for _, dv := range devs {
			busy = busy || dv.TaskID != ""
		}
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leases not released after the agent left: %+v", devs)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// SIGTERM stops the gateway cleanly and removes the socket.
	daemon.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("daemon exit: %v\n%s", err, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
	exited <- nil
	if _, err := os.Stat(d.Socket()); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestShimStartsDaemon(t *testing.T) {
	bin := buildPinky(t)
	home := pinkyHome(t)
	d := dirsFor(t, home)
	defer func() {
		// Stop the daemon the shim started.
		if b, err := os.ReadFile(d.LockFile()); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				syscall.Kill(pid, syscall.SIGTERM)
				time.Sleep(200 * time.Millisecond)
			}
		}
	}()
	agent := startMCP(t, bin, home, "claude-code", "--fake")
	text, isErr := agent.call(t, "phone_connect", map[string]any{"device": "fake-pixel-9", "task": "Started by the shim"})
	if isErr || !strings.HasPrefix(text, "settled") {
		t.Fatalf("connect:\n%s", text)
	}
	if st, err := os.Stat(d.TokenFile()); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("agent token: %v %v", st, err)
	}
}

// bearerTransport adds the agent token to every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// `pinky daemon --http` serves the same tools over Streamable HTTP, with
// the token `pinky token` prints.
func TestDaemonServesHTTP(t *testing.T) {
	bin := buildPinky(t)
	home := pinkyHome(t)
	d := dirsFor(t, home)
	daemon := exec.Command(bin, "daemon", "--fake", "--http", "127.0.0.1:0")
	daemon.Env = env(home)
	var logs syncBuffer
	daemon.Stderr = &logs
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			daemon.Process.Kill()
		}
		if t.Failed() {
			t.Logf("daemon log:\n%s", logs.String())
		}
	}()
	waitSocket(t, d)
	urlRe := regexp.MustCompile(`MCP over HTTP at (http://\S+)`)
	var url string
	for deadline := time.Now().Add(10 * time.Second); url == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if m := urlRe.FindStringSubmatch(logs.String()); m != nil {
			url = m[1]
		}
	}
	if url == "" {
		t.Fatal("the daemon did not say where it serves HTTP")
	}
	token := strings.TrimSpace(run(t, bin, home, "token"))
	if len(token) < 32 {
		t.Fatalf("token %q", token)
	}
	tr := &sdk.StreamableClientTransport{Endpoint: url, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearerTransport{token}}}
	cl := sdk.NewClient(&sdk.Implementation{Name: "codex", Version: "e2e"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := cl.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	c := &mcpClient{cs: cs}
	text, isErr := c.call(t, "phone_connect", map[string]any{"device": "Pixel 9 (fake)", "task": "Check the time"})
	if isErr || !strings.HasPrefix(text, "settled · Pixel 9 (fake)") {
		t.Fatalf("connect over HTTP:\n%s", text)
	}

	// The agent's files are on its own machine: it hands them to Pinky, in
	// the call or through a link it sends the file to, and pushes them,
	// which the person allows.
	text, isErr = c.call(t, "phone_files", map[string]any{"action": "upload", "paths": []string{"hello.txt"},
		"content": base64.StdEncoding.EncodeToString([]byte("hi"))})
	if isErr || !strings.Contains(text, "Saved hello.txt (2 B)") {
		t.Fatalf("upload in the call:\n%s", text)
	}
	text, isErr = c.call(t, "phone_files", map[string]any{"action": "upload", "paths": []string{"Pictures/cat.png"}})
	m := regexp.MustCompile(`curl --upload-file <your file> '([^']+)'`).FindStringSubmatch(text)
	if isErr || m == nil {
		t.Fatalf("upload link:\n%s", text)
	}
	img := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 50_000)
	req, _ := http.NewRequest(http.MethodPut, m[1], bytes.NewReader(img))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var answer bytes.Buffer
	answer.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(answer.String(), "Saved Pictures/cat.png (200 KB)") {
		t.Fatalf("PUT to the link: %d\n%s", resp.StatusCode, answer.String())
	}
	pushed := make(chan string, 1)
	go func() {
		text, _ := c.call(t, "phone_files", map[string]any{"action": "push", "intent": "Send the cat picture"})
		pushed <- text
	}()
	ctl, err := api.DialControl(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	var needs []api.NeedInfo
	for i := 0; i < 200 && len(needs) == 0; i++ {
		if err := ctl.Call("needs", nil, &needs); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(needs) != 1 || needs[0].Rule != "Files" || needs[0].Title != "Copy 2 files to Pixel 9 (fake)?" {
		t.Fatalf("needs: %+v", needs)
	}
	run(t, bin, home, "decide", needs[0].ID, "allow")
	if text := <-pushed; !strings.HasPrefix(text, "settled") || !strings.Contains(text, "Copied 2 files to the phone") {
		t.Fatalf("push:\n%s", text)
	}
	text, _ = c.call(t, "phone_files", map[string]any{})
	if !strings.Contains(text, "hello.txt · 2 B · ") || !strings.Contains(text, "Pictures/cat.png · 200 KB · ") || strings.Count(text, " · both") != 2 {
		t.Fatalf("list after the push:\n%s", text)
	}

	if text, _ := c.call(t, "phone_release", map[string]any{}); !strings.HasPrefix(text, "settled") {
		t.Fatalf("release:\n%s", text)
	}

	// A client that sends fixed headers only, as Grok Bot does (GitHub
	// DIYgod/pinky#10): no request after initialize carries the session
	// id, and its GET stream has none either. It still works through a
	// task on the iPhone.
	idless := func(msg string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(msg))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "GrokBot/1.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return resp.StatusCode, b.String()
	}
	if code, body := idless(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"grok","version":"1"}}}`); code != http.StatusOK {
		t.Fatalf("initialize: %d %s", code, body)
	}
	if code, _ := idless(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted {
		t.Fatalf("initialized: %d", code)
	}
	get, _ := http.NewRequest(http.MethodGet, url, nil)
	get.Header.Set("Accept", "text/event-stream")
	get.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(get); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET without a session: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	for i, call := range []struct{ args, want string }{
		{`{"name":"phone_connect","arguments":{"device":"iPhone 17 (fake)","task":"Open a folder"}}`, `settled · iPhone 17 (fake)`},
		{`{"name":"phone_launch","arguments":{"app":"Dinely"}}`, `settled · iPhone 17 (fake)`},
		{`{"name":"phone_observe","arguments":{}}`, `settled · iPhone 17 (fake)`},
		{`{"name":"phone_release","arguments":{"summary":"Done"}}`, `Released iPhone 17 (fake)`},
	} {
		code, body := idless(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, i+2, call.args))
		if code != http.StatusOK || !strings.Contains(body, call.want) || strings.Contains(body, `"isError":true`) {
			t.Fatalf("call %d without a session id: %d\n%s", i, code, body)
		}
	}

	// Without the token: refused.
	resp, err = http.Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", resp.StatusCode)
	}
}

// syncBuffer is a bytes.Buffer safe for a process writing while the test
// reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
