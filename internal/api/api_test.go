package api

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/mcp"
)

// shortDirs returns dirs under /tmp (the temporary folder on Windows,
// which has no /tmp): Unix socket paths must stay short.
func shortDirs(t *testing.T) Dirs {
	t.Helper()
	tmp := "/tmp"
	if runtime.GOOS == "windows" {
		tmp = os.TempDir()
	}
	base, err := os.MkdirTemp(tmp, "pk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return Dirs{Config: filepath.Join(base, "c"), Data: filepath.Join(base, "d"), Run: filepath.Join(base, "r")}
}

func startServer(t *testing.T, d Dirs, trusted bool) (*gateway.Gateway, *Listener) {
	t.Helper()
	ln, err := Listen(d)
	if err != nil {
		t.Fatal(err)
	}
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	g, err := gateway.New(gateway.Options{Providers: []driver.Provider{demo.NewProvider(driver.Android, dev)},
		TrustNewAgents: true, NoScreenshots: true, PollInterval: 5 * time.Millisecond, MaxBlock: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := EnsureToken(d.TokenFile())
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Gateway: g, MCP: mcp.New(g, "test"), Token: tok, Trusted: trusted, Version: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-done
		ln.Close()
		g.Close()
	})
	return g, ln
}

func TestSingleGatewayAndPermissions(t *testing.T) {
	d := shortDirs(t)
	startServer(t, d, false)
	if _, err := Listen(d); !errors.As(err, new(*RunningError)) {
		t.Fatalf("second gateway: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for path, want := range map[string]os.FileMode{d.Run: 0o700, d.Socket(): 0o600, d.TokenFile(): 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s: mode %v, want %v", path, got, want)
		}
	}
}

// TestServeFreesTheLock: once Serve returns, the socket and the lock are
// free, so the same process can start a gateway again at once (TestQuit
// does). Accept fails while the listener is still closing, so Serve must
// wait for Close to finish.
func TestServeFreesTheLock(t *testing.T) {
	d := shortDirs(t)
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	g, err := gateway.New(gateway.Options{Providers: []driver.Provider{demo.NewProvider(driver.Android, dev)}, NoScreenshots: true})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for i := range 20 {
		ln, err := Listen(d)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		srv := &Server{Gateway: g, MCP: mcp.New(g, "test"), Version: "test"}
		done := make(chan error, 1)
		go func() { done <- srv.Serve(ctx, ln) }()
		// One client first, so Serve is blocked in Accept when it stops.
		ctl, err := DialControl(d)
		if err != nil {
			t.Fatal(err)
		}
		ctl.Close()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestMCPOverSocket(t *testing.T) {
	d := shortDirs(t)
	startServer(t, d, false)

	// A wrong token is refused.
	c, err := Dial(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Greet(c, Hello{Role: RoleMCP, Token: "nope"}); err == nil || !strings.Contains(err.Error(), "bad agent token") {
		t.Fatalf("wrong token: %v", err)
	}
	c.Close()

	c, err = Dial(d)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := EnsureToken(d.TokenFile())
	br, _, err := Greet(c, Hello{Role: RoleMCP, Token: tok})
	if err != nil {
		t.Fatal(err)
	}
	cl := sdk.NewClient(&sdk.Implementation{Name: "claude-code", Version: "1"}, nil)
	cs, err := cl.Connect(context.Background(), &sdk.IOTransport{Reader: io.NopCloser(br), Writer: c}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "phone_connect", Arguments: map[string]any{"task": "Socket test"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if res.IsError || !strings.HasPrefix(text, "settled · Pixel 9") {
		t.Fatalf("connect over the socket:\n%s", text)
	}
}

func TestControl(t *testing.T) {
	d := shortDirs(t)
	g, _ := startServer(t, d, false)
	ctl, err := DialControl(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	var devs []gateway.DeviceView
	if err := ctl.Call("devices", nil, &devs); err != nil || len(devs) != 1 || devs[0].Info.Name != "Pixel 9" {
		t.Fatalf("devices: %+v %v", devs, err)
	}
	s := g.OpenSession(gateway.AgentInfo{Name: "Claude Code"})
	defer s.Close()
	s.Connect(context.Background(), gateway.ConnectRequest{Task: "Help"})
	go s.Handoff(context.Background(), gateway.HandoffRequest{Reason: "Please sign in"})
	var needs []NeedInfo
	deadline := time.Now().Add(2 * time.Second)
	for len(needs) == 0 && time.Now().Before(deadline) {
		if err := ctl.Call("needs", nil, &needs); err != nil {
			t.Fatal(err)
		}
	}
	if len(needs) != 1 || needs[0].Kind != gateway.NeedHandoff {
		t.Fatalf("needs: %+v", needs)
	}
	// An untrusted socket client cannot answer.
	err = ctl.Call("decide", DecideParams{Need: needs[0].ID, Decision: gateway.AllowOnce}, nil)
	var re *RPCError
	if !errors.As(err, &re) || re.Code != CodeNotTrusted {
		t.Fatalf("decide over an untrusted socket: %v", err)
	}
	if err := ctl.Call("hand_back", DeviceParams{Device: "pixel"}, nil); err == nil {
		t.Fatal("hand_back over an untrusted socket")
	}
	// Stopping is always allowed.
	if err := ctl.Call("stop", DeviceParams{Device: "pixel"}, nil); err != nil {
		t.Fatal(err)
	}
	var tasks []gateway.TaskView
	if err := ctl.Call("tasks", nil, &tasks); err != nil || len(tasks) != 1 || tasks[0].State != gateway.TaskStopped {
		t.Fatalf("tasks: %+v %v", tasks, err)
	}
	if err := ctl.Call("fly", nil, nil); err == nil {
		t.Fatal("unknown method accepted")
	}
}

func TestTrustedControl(t *testing.T) {
	d := shortDirs(t)
	g, _ := startServer(t, d, true)
	ctl, err := DialControl(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	if !ctl.Reply.Trusted {
		t.Fatal("hello does not say trusted")
	}
	s := g.OpenSession(gateway.AgentInfo{Name: "Claude Code"})
	defer s.Close()
	s.Connect(context.Background(), gateway.ConnectRequest{Task: "Help"})
	done := make(chan *gateway.Result, 1)
	go func() { done <- s.Handoff(context.Background(), gateway.HandoffRequest{Reason: "Please sign in"}) }()
	var needs []NeedInfo
	for i := 0; i < 500 && len(needs) == 0; i++ {
		ctl.Call("needs", nil, &needs)
		time.Sleep(2 * time.Millisecond)
	}
	if len(needs) == 0 {
		t.Fatal("no need")
	}
	if err := ctl.Call("decide", DecideParams{Need: needs[0].ID, Decision: gateway.AllowOnce, Note: "done"}, nil); err != nil {
		t.Fatal(err)
	}
	// The call blocks MaxBlock at most (50 ms here), then says
	// approval_pending, and the agent repeats it with the approval_id: a
	// slow computer answers the person's decision to the repeat (once on
	// Windows CI).
	res := <-done
	if res.Status == gateway.StatusPending {
		res = s.Handoff(context.Background(), gateway.HandoffRequest{Call: gateway.Call{ApprovalID: res.ApprovalID}, Reason: "Please sign in"})
	}
	if res.Status != gateway.StatusSettled || res.Note != "done" {
		t.Fatalf("handoff: %+v", res)
	}
}

// TestTrustedControlLateDecision: the person hands control back after the
// agent's call stopped waiting. The agent's next call, with the approval_id
// or without, gets the answer, and the person is not asked again.
func TestTrustedControlLateDecision(t *testing.T) {
	d := shortDirs(t)
	g, _ := startServer(t, d, true)
	ctl, err := DialControl(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	s := g.OpenSession(gateway.AgentInfo{Name: "Claude Code"})
	defer s.Close()
	s.Connect(context.Background(), gateway.ConnectRequest{Task: "Help"})
	for _, withID := range []bool{true, false} {
		res := s.Handoff(context.Background(), gateway.HandoffRequest{Reason: "Please sign in"})
		if res.Status != gateway.StatusPending || res.ApprovalID == "" {
			t.Fatalf("first call: %+v", res)
		}
		var needs []NeedInfo
		if err := ctl.Call("needs", nil, &needs); err != nil || len(needs) != 1 {
			t.Fatalf("needs %+v, %v", needs, err)
		}
		if err := ctl.Call("decide", DecideParams{Need: needs[0].ID, Decision: gateway.AllowOnce, Note: "done"}, nil); err != nil {
			t.Fatal(err)
		}
		again := gateway.HandoffRequest{Reason: "Please sign in"}
		if withID {
			again.ApprovalID = res.ApprovalID
		}
		if res := s.Handoff(context.Background(), again); res.Status != gateway.StatusSettled || res.Note != "done" {
			t.Fatalf("repeat (approval_id %v): %+v", withID, res)
		}
		if err := ctl.Call("needs", nil, &needs); err != nil || len(needs) != 0 {
			t.Fatalf("asked again: %+v, %v", needs, err)
		}
	}
}

func TestEnsureToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "agent-token")
	a, err := EnsureToken(path)
	if err != nil || len(a) != 64 {
		t.Fatalf("token %q %v", a, err)
	}
	b, _ := EnsureToken(path)
	if a != b {
		t.Fatal("token changed")
	}
	if runtime.GOOS != "windows" {
		os.Chmod(path, 0o644)
		EnsureToken(path)
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("loose token mode not fixed: %v", st.Mode().Perm())
		}
	}
}

func TestDefaultDirsHome(t *testing.T) {
	t.Setenv(HomeEnv, "/tmp/pinky-x")
	d, err := DefaultDirs()
	home, _ := filepath.Abs("/tmp/pinky-x") // D:\tmp\pinky-x on Windows
	if err != nil || d.Socket() != filepath.Join(home, "run", "pinky.sock") || d.Rules() != filepath.Join(home, "config", "rules.yaml") {
		t.Fatalf("%+v %v", d, err)
	}
}

// TestQuit ends the process hosting the gateway on request, which Windows
// needs (it has no signal for that), and refuses where nothing can quit.
func TestQuit(t *testing.T) {
	d := shortDirs(t)
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	g, err := gateway.New(gateway.Options{Providers: []driver.Provider{demo.NewProvider(driver.Android, dev)}, NoScreenshots: true})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	serve := func(quit func()) (context.CancelFunc, chan error) {
		ln, err := Listen(d)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		srv := &Server{Gateway: g, MCP: mcp.New(g, "test"), Version: "test"}
		if quit != nil {
			srv.Quit = func() { quit(); cancel() }
		}
		done := make(chan error, 1)
		go func() { done <- srv.Serve(ctx, ln); ln.Close() }()
		return cancel, done
	}
	call := func() error {
		ctl, err := DialControl(d)
		if err != nil {
			t.Fatal(err)
		}
		defer ctl.Close()
		return ctl.Call("quit", nil, nil)
	}

	cancel, done := serve(nil)
	if err := call(); err == nil {
		t.Error("quit worked without Quit")
	}
	cancel()
	<-done

	quitted := make(chan struct{})
	_, done = serve(func() { close(quitted) })
	if err := call(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server still serves after quit")
	}
	<-quitted
}
