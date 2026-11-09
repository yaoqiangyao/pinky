package gateway

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// testPolicy is the bundled policy with the demo apps' categories and
// "Ask before payments, messages and deletions" (preset 1). The gateway's
// tests check that sends, deletions, calls, links and settings changes ask,
// which the default preset (payments only) lets through.
func testPolicy(t testing.TB) *policy.Engine {
	t.Helper()
	pol := policy.Default()
	if err := pol.SetPreset(policy.PresetCommits); err != nil {
		t.Fatal(err)
	}
	for cat, e := range demo.PolicyApps() {
		pol.AddApps(cat, e)
	}
	return pol
}

func newTestGateway(t *testing.T, opts ...func(*Options)) (*Gateway, *demo.Device) {
	t.Helper()
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	pol := testPolicy(t)
	o := Options{
		Providers: []driver.Provider{demo.NewProvider(driver.Android, dev)}, Policy: pol, TrustNewAgents: true,
		ApprovalTimeout: 3 * time.Second, MaxBlock: 2 * time.Second, SettleMax: 200 * time.Millisecond,
		PollInterval: 5 * time.Millisecond, DiscoverEvery: 20 * time.Millisecond, NoScreenshots: true,
	}
	for _, f := range opts {
		f(&o)
	}
	g, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g, dev
}

var agent = AgentInfo{Name: "Claude Code", Client: "claude-code 2.1"}

func connect(t *testing.T, g *Gateway, task string) (*Session, *Result) {
	t.Helper()
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	res := s.Connect(context.Background(), ConnectRequest{Task: task})
	if res.IsError() || res.Status != StatusSettled {
		t.Fatalf("connect: %+v", res)
	}
	return s, res
}

// ref finds an element's ref by label in a result's screen.
func ref(t *testing.T, res *Result, label string) int {
	t.Helper()
	if res.Screen == nil {
		t.Fatalf("no screen in %+v", res)
	}
	for _, e := range res.Screen.Elements {
		if e.Label == label {
			return e.Ref
		}
	}
	t.Fatalf("%q not on screen:\n%s", label, res.Screen.Render())
	return 0
}

func tap(s *Session, r int) *Result {
	return s.Act(context.Background(), Call{}, Action{Tool: "tap", Ref: r})
}

func mustStatus(t *testing.T, res *Result, want Status) {
	t.Helper()
	if res.IsError() || res.Status != want {
		t.Fatalf("want %s, got status %q error %q: %s", want, res.Status, res.Error, res.Message)
	}
}

func waitNeed(t *testing.T, g *Gateway) NeedView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if needs := g.Needs(); len(needs) > 0 {
			return needs[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no need appeared")
	return NeedView{}
}

// toCart walks the demo phone to the Dinely cart and returns that result.
func toCart(t *testing.T, s *Session) *Result {
	t.Helper()
	ctx := context.Background()
	res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"})
	mustStatus(t, res, StatusSettled)
	res = tap(s, ref(t, res, "Orders"))
	mustStatus(t, res, StatusSettled)
	res = tap(s, ref(t, res, "Reorder · Chicken rice bowl"))
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(strings.Join(res.Diff, "\n"), `toast: "Added to cart"`) {
		t.Fatalf("diff misses the toast: %q", res.Diff)
	}
	res = tap(s, ref(t, res, "Cart · 1"))
	mustStatus(t, res, StatusSettled)
	return res
}

func TestApprovalAllow(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.DataDir = t.TempDir(); o.NoScreenshots = false })
	s, _ := connect(t, g, "Reorder last week's lunch")
	cart := toCart(t, s)
	place := ref(t, cart, "Place order · $12.80")
	go func() {
		n := waitNeed(t, g)
		if n.Kind != NeedApproval || n.Rule != "Payments" || n.Screenshot == nil || n.Target.Empty() {
			t.Errorf("need: %+v", n)
		}
		if devs := g.Devices(); devs[0].Status != DeviceNeedsYou {
			t.Errorf("device status %s", devs[0].Status)
		}
		if err := g.Decide(n.ID, AllowOnce, "go ahead"); err != nil {
			t.Error(err)
		}
	}()
	res := s.Act(context.Background(), Call{Intent: "Pay $12.80 for 1 chicken rice bowl"}, Action{Tool: "tap", Ref: place})
	mustStatus(t, res, StatusSettled)
	if res.Note != "go ahead" || !strings.Contains(res.Message, "allowed this once") {
		t.Fatalf("result: %+v", res)
	}
	if dev.Current() != "done" {
		t.Fatalf("order not placed: screen %s", dev.Current())
	}
	if len(g.Needs()) != 0 {
		t.Fatal("need still listed")
	}
	// The toast and notification the order posted come with the result.
	if r := res.Screen.Render(); !strings.Contains(r, `toast: "Order placed"`) || !strings.Contains(r, "Your order is on its way") {
		t.Fatalf("events missing:\n%s", r)
	}

	// History has the task, the approval step finished in place, and media.
	// The after picture is kept once a read confirms it: here the one the
	// gateway makes when the task ends.
	s.Release(context.Background(), ReleaseRequest{})
	g.Flush()
	tasks, err := g.History(context.Background(), HistoryQuery{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("history: %v %v", tasks, err)
	}
	var payStep *StepView
	for i, st := range tasks[0].Steps {
		if st.Tool == "phone_tap" && strings.Contains(st.Text, "Place order") {
			payStep = &tasks[0].Steps[i]
		}
	}
	if payStep == nil || payStep.Status != "settled" || payStep.Before == "" || payStep.After == "" {
		t.Fatalf("pay step: %+v", payStep)
	}
	if !strings.Contains(payStep.Args, "Pay $12.80") {
		t.Fatalf("intent missing from args: %s", payStep.Args)
	}
	for _, id := range []string{payStep.Before, payStep.After} {
		p, err := g.MediaPath(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApprovalDeny(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	go func() {
		n := waitNeed(t, g)
		g.Decide(n.ID, Deny, "too expensive")
	}()
	res := tap(s, ref(t, cart, "Place order · $12.80"))
	mustStatus(t, res, StatusDenied)
	if res.Note != "too expensive" || res.Rule != "Payments" {
		t.Fatalf("result: %+v", res)
	}
	if dev.Current() != "cart" {
		t.Fatalf("order placed despite deny: %s", dev.Current())
	}
}

func TestApprovalTimeout(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.ApprovalTimeout = 80 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	res := tap(s, ref(t, cart, "Place order · $12.80"))
	mustStatus(t, res, StatusDenied)
	if !strings.Contains(res.Message, "Nobody answered") || res.Note == "" {
		t.Fatalf("timeout result: %+v", res)
	}
	if dev.Current() != "cart" {
		t.Fatal("acted after a timeout")
	}
}

func TestApprovalPendingThenRepeat(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.MaxBlock = 40 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	place := ref(t, cart, "Place order · $12.80")
	res := tap(s, place)
	mustStatus(t, res, StatusPending)
	id := res.ApprovalID
	if id == "" || res.Rule != "Payments" {
		t.Fatalf("pending result: %+v", res)
	}

	// The device takes no other action while the approval is open, but
	// reads still work.
	note := ref(t, cart, "Note for the restaurant")
	busy := s.Act(context.Background(), Call{}, Action{Tool: "type", Ref: note, Text: "no cilantro"})
	if busy.Status != StatusPending || busy.ApprovalID != id {
		t.Fatalf("busy device: %+v", busy)
	}
	if obs := s.Observe(context.Background(), ObserveRequest{}); obs.Status != StatusSettled {
		t.Fatalf("observe during approval: %+v", obs)
	}

	// The person decides while no call is open; the decision is kept.
	if err := g.Decide(waitNeed(t, g).ID, AllowOnce, ""); err != nil {
		t.Fatal(err)
	}
	// Other arguments with the id are refused.
	bad := s.Act(context.Background(), Call{ApprovalID: id}, Action{Tool: "tap", Ref: note})
	if bad.Error != ErrInvalidArgument {
		t.Fatalf("mismatched args: %+v", bad)
	}
	res = s.Act(context.Background(), Call{ApprovalID: id}, Action{Tool: "tap", Ref: place})
	mustStatus(t, res, StatusSettled)
	if dev.Current() != "done" {
		t.Fatal("approved action did not run")
	}
	// Single use.
	again := s.Act(context.Background(), Call{ApprovalID: id}, Action{Tool: "tap", Ref: place})
	if again.Error != ErrInvalidArgument {
		t.Fatalf("reused approval id: %+v", again)
	}
}

func TestApprovalStale(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.MaxBlock = 40 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	place := ref(t, cart, "Place order · $12.80")
	res := tap(s, place)
	mustStatus(t, res, StatusPending)
	// While the approval waits, the cart changes; the button stays the same.
	dev.AddToCart()
	if err := g.Decide(waitNeed(t, g).ID, AllowOnce, ""); err != nil {
		t.Fatal(err)
	}
	res = s.Act(context.Background(), Call{ApprovalID: res.ApprovalID}, Action{Tool: "tap", Ref: place})
	mustStatus(t, res, StatusStale)
	if dev.Current() != "cart" || dev.Cart() != 2 {
		t.Fatal("stale approval acted")
	}
	if !strings.Contains(strings.Join(res.Diff, "\n"), "2 × Chicken rice bowl") {
		t.Fatalf("stale result should show what changed: %q", res.Diff)
	}

	// The target moving away is stale too.
	res = tap(s, place)
	mustStatus(t, res, StatusPending)
	dev.Show("orders")
	g.Decide(waitNeed(t, g).ID, AllowOnce, "")
	mustStatus(t, s.Act(context.Background(), Call{ApprovalID: res.ApprovalID}, Action{Tool: "tap", Ref: place}), StatusStale)
	if dev.Current() != "orders" {
		t.Fatal("stale approval acted")
	}
}

func TestTakeOverAndHandoff(t *testing.T) {
	g, _ := newTestGateway(t)
	s, first := connect(t, g, "Lunch")
	home := ref(t, first, "Dinely")
	if err := g.TakeOver("pixel"); err != nil {
		t.Fatal(err)
	}
	if v := g.Devices()[0]; v.Status != DeviceInControl {
		t.Fatalf("device status %s", v.Status)
	}
	res := tap(s, home)
	mustStatus(t, res, StatusTookOver)
	if res.Screen == nil {
		t.Fatal("user_took_over without the current screen")
	}
	mustStatus(t, tap(s, home), StatusPaused)

	done := make(chan *Result, 1)
	go func() { done <- s.Handoff(context.Background(), HandoffRequest{Reason: "Sign in to Dinely, please"}) }()
	n := waitNeed(t, g)
	if n.Kind != NeedHandoff || n.Title != "Sign in to Dinely, please" {
		t.Fatalf("handoff need: %+v", n)
	}
	if err := g.HandBack("pixel", "signed in"); err != nil {
		t.Fatal(err)
	}
	res = <-done
	mustStatus(t, res, StatusSettled)
	if res.Note != "signed in" || !res.Full {
		t.Fatalf("handoff result: %+v", res)
	}
	mustStatus(t, tap(s, home), StatusSettled)

	// Person input on the mirror takes over too.
	if err := g.Touch("pixel", driver.TouchDown, screen.Point{X: 10, Y: 10}); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, tap(s, ref(t, s.Observe(context.Background(), ObserveRequest{}), "Search dishes")), StatusTookOver)
	// Handing back before the agent calls phone_handoff: the call returns at once.
	g.HandBack("pixel", "done here")
	res = s.Handoff(context.Background(), HandoffRequest{Reason: "waiting"})
	mustStatus(t, res, StatusSettled)
	if res.Note != "done here" {
		t.Fatalf("early hand back note: %+v", res)
	}
}

func TestTakeOverDuringApproval(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	go func() {
		n := waitNeed(t, g)
		g.Decide(n.ID, TakeOver, "")
	}()
	res := tap(s, ref(t, cart, "Place order · $12.80"))
	mustStatus(t, res, StatusTookOver)
	if dev.Current() != "cart" || len(g.Needs()) != 0 {
		t.Fatal("take over acted or left the panel open")
	}
}

func TestStop(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	go func() {
		waitNeed(t, g)
		g.Stop("pixel")
	}()
	res := tap(s, ref(t, cart, "Place order · $12.80"))
	mustStatus(t, res, StatusStopped)
	if dev.Current() != "cart" || len(g.Needs()) != 0 {
		t.Fatal("stop acted or left the panel open")
	}
	mustStatus(t, s.Observe(context.Background(), ObserveRequest{}), StatusStopped)
	mustStatus(t, s.Connect(context.Background(), ConnectRequest{Task: "again"}), StatusStopped)
	if tasks := g.Tasks(); len(tasks) == 0 || tasks[0].State != TaskStopped {
		t.Fatalf("tasks after stop: %+v", tasks)
	}
	// Another session may connect.
	connect(t, g, "Fresh start")

	// Stop all ends every lease and cancels a slow action in flight.
	s3 := g.OpenSession(AgentInfo{Name: "Codex"})
	defer s3.Close()
	dev.SetSlow(time.Second)
	defer dev.SetSlow(0)
	g.Stop("pixel")
	mustStatus(t, s3.Connect(context.Background(), ConnectRequest{Task: "slow"}), StatusSettled)
	// Only once s3 holds the phone: a test goroutine that ran late must
	// not get to Connect after Stop all.
	go func() {
		time.Sleep(50 * time.Millisecond)
		g.StopAll()
	}()
	start := time.Now()
	res = s3.Act(context.Background(), Call{}, Action{Tool: "key", Key: "home"})
	mustStatus(t, res, StatusStopped)
	if time.Since(start) > 800*time.Millisecond {
		t.Fatal("stop all did not cancel the action in flight")
	}
}

func TestLeaseHeld(t *testing.T) {
	g, _ := newTestGateway(t)
	connect(t, g, "Reorder lunch")
	other := g.OpenSession(AgentInfo{Name: "Codex"})
	defer other.Close()
	res := other.Connect(context.Background(), ConnectRequest{Task: "Something else"})
	if res.Error != ErrLeaseHeld || !strings.Contains(res.Holder, "Reorder lunch") {
		t.Fatalf("lease_held: %+v", res)
	}
}

func TestSessionCloseReleases(t *testing.T) {
	g, _ := newTestGateway(t, func(o *Options) { o.MaxBlock = 20 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	mustStatus(t, tap(s, ref(t, cart, "Place order · $12.80")), StatusPending)
	s.Close()
	if len(g.Needs()) != 0 {
		t.Fatal("approval not withdrawn on close")
	}
	connect(t, g, "Next agent")
}

func TestCancelWithdrawsApproval(t *testing.T) {
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitNeed(t, g)
		cancel()
	}()
	s.Act(ctx, Call{}, Action{Tool: "tap", Ref: ref(t, cart, "Place order · $12.80")})
	if len(g.Needs()) != 0 {
		t.Fatal("cancelled call left the panel open")
	}
}

func TestReadOnly(t *testing.T) {
	g, dev := newTestGateway(t)
	g.SetReadOnly("pixel", true)
	s, _ := connect(t, g, "Look around")
	cart := toCart(t, s)
	res := tap(s, ref(t, cart, "Place order · $12.80"))
	mustStatus(t, res, StatusBlocked)
	if res.Rule != policy.RuleReadOnly {
		t.Fatalf("rule %q", res.Rule)
	}
	res = s.Act(context.Background(), Call{}, Action{Tool: "type", Ref: ref(t, cart, "Note for the restaurant"), Text: "hi"})
	mustStatus(t, res, StatusBlocked)
	dev.Show("dinely")
	obs := s.Observe(context.Background(), ObserveRequest{})
	res = s.Act(context.Background(), Call{}, Action{Tool: "type", Ref: ref(t, obs, "Search dishes"), Text: "rice"})
	mustStatus(t, res, StatusSettled)
}

func TestBlockedApp(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Look around")
	apps := s.Apps(context.Background(), AppsRequest{})
	for _, a := range apps.Apps {
		if strings.Contains(a.Name, "Wallet") {
			t.Fatalf("blocked app listed: %+v", apps.Apps)
		}
	}
	// Opening it asks the person (unblock_test.go has the rest); they keep
	// it blocked, and the next launch is refused at once.
	go func() {
		n := waitNeed(t, g)
		if !n.Unblock || n.Rule != "Finance apps" {
			t.Errorf("need: %+v", n)
		}
		g.Decide(n.ID, Deny, "")
	}()
	res := s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "com.google.android.apps.walletnfcrel"})
	mustStatus(t, res, StatusDenied)
	if res.Rule != "Finance apps" {
		t.Fatalf("rule %q", res.Rule)
	}
	res = s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "com.google.android.apps.walletnfcrel"})
	mustStatus(t, res, StatusBlocked)
	if res.Rule != "Finance apps" || !strings.Contains(res.Message, "kept it blocked for this task") {
		t.Fatalf("second launch: %+v", res)
	}
	// The person opens it; the agent sees nothing of it.
	dev.Show("wallet")
	dev.Notify("com.google.android.apps.walletnfcrel", "You paid $25.00")
	dev.Notify("com.google.android.apps.messaging", "Your code is 482913")
	obs := s.Observe(context.Background(), ObserveRequest{Screenshot: true})
	text := obs.Screen.Render()
	if strings.Contains(text, "4242") || strings.Contains(text, "$25") || len(obs.Screen.Elements) != 0 || obs.Screenshot != nil {
		t.Fatalf("blocked app leaked:\n%s", text)
	}
	if strings.Contains(text, "482913") || !strings.Contains(text, "••••••") {
		t.Fatalf("code not masked:\n%s", text)
	}
	res = s.Act(context.Background(), Call{}, Action{Tool: "tap", Point: &screen.Point{X: 500, Y: 900}})
	mustStatus(t, res, StatusBlocked)
	if len(dev.Inputs()) != 0 {
		t.Fatalf("input reached a blocked app: %+v", dev.Inputs())
	}
	mustStatus(t, s.Act(context.Background(), Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
}

func TestCredentialFields(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Sign in")
	dev.Show("account")
	obs := s.Observe(context.Background(), ObserveRequest{})
	for _, label := range []string{"Password", "Verification code"} {
		res := s.Act(context.Background(), Call{}, Action{Tool: "type", Ref: ref(t, obs, label), Text: "hunter2"})
		mustStatus(t, res, StatusBlocked)
		if res.Rule != policy.RuleCredentials || !strings.Contains(res.Message, "phone_handoff") {
			t.Fatalf("%s: %+v", label, res)
		}
	}
	for _, in := range dev.Inputs() {
		if in.Kind == "type" {
			t.Fatalf("typed into a credential field: %+v", in)
		}
	}
	mustStatus(t, s.Act(context.Background(), Call{}, Action{Tool: "type", Ref: ref(t, obs, "Email"), Text: "a@b.c"}), StatusSettled)
}

func TestSendMessageAsks(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Reply to Sam")
	dev.Show("chats")
	obs := s.Observe(context.Background(), ObserveRequest{})
	res := tap(s, ref(t, obs, "Sam"))
	mustStatus(t, res, StatusSettled)
	input := ref(t, res, "Message")
	go func() {
		n := waitNeed(t, g)
		if n.Rule != "Messages" || !strings.Contains(n.Title, "Saturday at 3 works") {
			t.Errorf("need %+v", n)
		}
		g.Decide(n.ID, AllowOnce, "")
	}()
	res = s.Act(context.Background(), Call{}, Action{Tool: "type", Ref: input, Text: "Saturday at 3 works", Submit: true})
	mustStatus(t, res, StatusSettled)
	if chat := dev.Chat(); chat[len(chat)-1] != "Me: Saturday at 3 works" {
		t.Fatalf("chat: %v", chat)
	}
}

func TestBatchStopsAtApproval(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.MaxBlock = 20 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	res := s.Batch(context.Background(), BatchRequest{Steps: []Action{
		{Tool: "type", Ref: ref(t, cart, "Note for the restaurant"), Text: "no cilantro"},
		{Tool: "tap", Ref: ref(t, cart, "Place order · $12.80")},
		{Tool: "key", Key: "home"},
	}})
	if len(res.Steps) != 2 || res.Steps[0].Status != StatusSettled || res.Status != StatusPending || res.ApprovalID == "" {
		t.Fatalf("batch: %+v", res)
	}
	if dev.Current() != "cart" {
		t.Fatal("batch ran past the approval")
	}
}

// waitUntil polls cond until it holds. Tests wait for what they expect to
// happen rather than for a fixed time, which a stalled goroutine could
// outlast; the deadline only ends a test that would fail anyway.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestOfflineAndReconnect(t *testing.T) {
	// The phone comes back well within the grace period, however slowly
	// the test runs.
	g, dev := newTestGateway(t, func(o *Options) { o.ReconnectGrace = time.Minute })
	s, first := connect(t, g, "Lunch")
	dinely := ref(t, first, "Dinely")
	dev.SetOffline(true)
	res := tap(s, dinely)
	if res.Status != StatusUncertain && res.Error != ErrDeviceOffline {
		t.Fatalf("drop: %+v", res)
	}
	if res = tap(s, dinely); res.Error != ErrDeviceOffline {
		t.Fatalf("after drop: %+v", res)
	}
	dev.SetOffline(false)
	waitUntil(t, "the phone to come back", func() bool { return g.Devices()[0].Status != DeviceOffline })
	mustStatus(t, s.Observe(context.Background(), ObserveRequest{}), StatusSettled)
}

// slowOpen is a provider whose Open takes long once armed, as an iPhone
// over Wi-Fi or a slow Windows runner does.
type slowOpen struct {
	driver.Provider
	mu    sync.Mutex
	delay time.Duration
}

func (p *slowOpen) Open(ctx context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	d := p.delay
	p.mu.Unlock()
	select {
	case <-time.After(d):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return p.Provider.Open(ctx, id)
}

// TestCallsWaitWhileConnecting: a phone that dropped is back and Pinky is
// opening it (Connecting): an agent's call waits for the open instead of
// getting device_offline.
func TestCallsWaitWhileConnecting(t *testing.T) {
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	p := &slowOpen{Provider: demo.NewProvider(driver.Android, dev)}
	g, _ := newTestGateway(t, func(o *Options) {
		o.Providers = []driver.Provider{p}
		o.ReconnectGrace = time.Minute
	})
	s, _ := connect(t, g, "Lunch")
	dev.SetOffline(true)
	waitUntil(t, "the phone to drop", func() bool { return g.Devices()[0].Status == DeviceOffline })
	p.mu.Lock()
	p.delay = 400 * time.Millisecond
	p.mu.Unlock()
	dev.SetOffline(false)
	waitUntil(t, "Pinky to open it", func() bool { return g.Devices()[0].Status == DeviceConnecting })
	start := time.Now()
	mustStatus(t, s.Observe(context.Background(), ObserveRequest{}), StatusSettled)
	if st := g.Devices()[0].Status; st == DeviceOffline || st == DeviceConnecting {
		t.Fatalf("status %q after the read", st)
	}
	t.Logf("the read waited %v for the open", time.Since(start).Round(time.Millisecond))
}

// TestOfflinePastGrace ends the lease of a phone that stays away past the
// grace period; the phone coming back does not bring the lease back.
func TestOfflinePastGrace(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.ReconnectGrace = 50 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	dev.SetOffline(true)
	waitUntil(t, "the lease to end", func() bool { ts := g.Tasks(); return len(ts) == 1 && ts[0].State == TaskFailed })
	dev.SetOffline(false)
	waitUntil(t, "the phone to come back", func() bool { return g.Devices()[0].Status != DeviceOffline })
	if res := s.Observe(context.Background(), ObserveRequest{}); res.Error != ErrInvalidArgument {
		t.Fatalf("lease survived past grace: %+v", res)
	}
}

func TestIdleExpiry(t *testing.T) {
	g, _ := newTestGateway(t, func(o *Options) { o.IdleTimeout = 60 * time.Millisecond })
	s, _ := connect(t, g, "Lunch")
	waitUntil(t, "the idle lease to end", func() bool { ts := g.Tasks(); return len(ts) == 1 && ts[0].State == TaskDone })
	if res := s.Observe(context.Background(), ObserveRequest{}); res.Error != ErrInvalidArgument {
		t.Fatalf("lease survived idling: %+v", res)
	}
	g.Flush()
}

func TestNewAgentNeedsApproval(t *testing.T) {
	g, _ := newTestGateway(t, func(o *Options) { o.TrustNewAgents = false; o.MaxBlock = 30 * time.Millisecond })
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(context.Background(), ConnectRequest{Task: "Lunch"})
	mustStatus(t, res, StatusPending)
	n := waitNeed(t, g)
	if n.Rule != "New agent" || !strings.Contains(n.Title, "Claude Code") {
		t.Fatalf("need %+v", n)
	}
	g.Decide(n.ID, AllowOnce, "")
	mustStatus(t, s.Connect(context.Background(), ConnectRequest{Call: Call{ApprovalID: res.ApprovalID}, Task: "Lunch"}), StatusSettled)
	// Approved agents connect straight away next time.
	s2 := g.OpenSession(agent)
	defer s2.Close()
	s.Close()
	mustStatus(t, s2.Connect(context.Background(), ConnectRequest{Task: "Dinner"}), StatusSettled)
}

func TestLockedPhone(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) { o.MaxBlock = 50 * time.Millisecond })
	s, first := connect(t, g, "Lunch")
	dinely := ref(t, first, "Dinely")
	dev.SetLocked(true)
	res := tap(s, dinely)
	mustStatus(t, res, StatusLocked)
	var unlock *NeedView
	for _, n := range g.Needs() {
		if n.Kind == NeedUnlock {
			unlock = &n
		}
	}
	if unlock == nil {
		t.Fatalf("no unlock card: %+v", g.Needs())
	}
	if err := g.Unlock("pixel"); err != ErrNoUnlock {
		t.Fatalf("unlock without a hook: %v", err)
	}
	res = s.Handoff(context.Background(), HandoffRequest{Reason: "Please unlock the phone"})
	mustStatus(t, res, StatusPending)
	// Unlocking on the phone does not hand back by itself: the person may be
	// doing what the agent asked.
	dev.SetLocked(false)
	time.Sleep(100 * time.Millisecond)
	res = s.Handoff(context.Background(), HandoffRequest{Call: Call{ApprovalID: res.ApprovalID}, Reason: "Please unlock the phone"})
	mustStatus(t, res, StatusPending)
	g.HandBack("pixel", "unlocked it")
	res = s.Handoff(context.Background(), HandoffRequest{Call: Call{ApprovalID: res.ApprovalID}, Reason: "Please unlock the phone"})
	mustStatus(t, res, StatusSettled)
	if res.Note != "unlocked it" {
		t.Fatalf("hand back note: %+v", res)
	}
}

func TestUnlockFromComputer(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.pin = "4711"
	var asked []string
	g := newExtraGateway(t, x, func(o *Options) {
		o.PIN = func(ctx context.Context, id string) (string, error) {
			asked = append(asked, id)
			return "4711", nil
		}
	})
	s, _ := connect(t, g, "Lunch")
	x.SetLocked(true)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
	done := make(chan *Result, 1)
	go func() { done <- s.Handoff(ctx, HandoffRequest{Reason: "Please unlock the phone"}) }()
	waitNeedKind(t, g, NeedHandoff)
	if err := g.Decide("unlock:pixel", AllowOnce, ""); err != nil {
		t.Fatal(err)
	}
	res := <-done
	mustStatus(t, res, StatusSettled)
	if res.Note != "The phone was unlocked." {
		t.Fatalf("unlock handoff: %+v", res)
	}
	if len(asked) != 1 || asked[0] != "pixel" || len(x.entered) != 1 {
		t.Fatalf("PIN asked for %v, entered %d times", asked, len(x.entered))
	}
	// The task's history says so, never with the PIN.
	found := false
	for _, st := range taskSteps(g, s) {
		if strings.Contains(st.Args+st.Text, "4711") {
			t.Fatalf("the PIN is in history: %+v", st)
		}
		found = found || st.Text == "Unlocked from the computer"
	}
	if !found {
		t.Fatalf("no unlock step: %+v", taskSteps(g, s))
	}
}

func TestWrongPINIsNotRetried(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.pin = "4711"
	g := newExtraGateway(t, x, func(o *Options) {
		o.PIN = func(context.Context, string) (string, error) { return "0000", nil }
	})
	s, _ := connect(t, g, "Lunch")
	x.SetLocked(true)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
	if err := g.Unlock("pixel"); !errors.Is(err, ErrWrongPIN) || !errors.Is(err, driver.ErrLocked) {
		t.Fatalf("wrong PIN: %v", err)
	}
	if len(x.entered) != 1 {
		t.Fatalf("entered the PIN %d times, want once", len(x.entered))
	}
	var failed bool
	for _, st := range taskSteps(g, s) {
		failed = failed || strings.HasPrefix(st.Text, "Unlock from the computer failed")
	}
	if !failed {
		t.Fatalf("no failed unlock step: %+v", taskSteps(g, s))
	}
	// Cancelled at the prompt: nothing is entered.
	g2 := newExtraGateway(t, newExtraDev("pixel", "Pixel 9"), func(o *Options) {
		o.PIN = func(context.Context, string) (string, error) { return "", errors.New("cancelled") }
	})
	if err := g2.Unlock("pixel"); err == nil || err.Error() != "cancelled" {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestWaitForAndDiff(t *testing.T) {
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	go func() {
		time.Sleep(30 * time.Millisecond)
		dev.Show("done")
	}()
	res := s.WaitFor(context.Background(), WaitForRequest{Text: "order placed", TimeoutMS: 2000})
	mustStatus(t, res, StatusSettled)
	if res.Matched == nil || !*res.Matched {
		t.Fatalf("wait_for: %+v", res)
	}
	res = s.WaitFor(context.Background(), WaitForRequest{Text: "never there", TimeoutMS: 50})
	if res.Matched == nil || *res.Matched {
		t.Fatalf("wait_for timeout: %+v", res)
	}
	// An action in the same app returns a diff, not the whole screen.
	dev.Show("orders")
	obs := s.Observe(context.Background(), ObserveRequest{})
	res = tap(s, ref(t, obs, "Reorder · Beef noodles"))
	mustStatus(t, res, StatusSettled)
	if res.Full || len(res.Diff) == 0 {
		t.Fatalf("want a diff: %+v", res)
	}
	joined := strings.Join(res.Diff, "\n")
	if !strings.Contains(joined, `+ [`) || !strings.Contains(joined, `"Cart · 1"`) {
		t.Fatalf("diff:\n%s", joined)
	}
}

type settlingDevice struct {
	*demo.Device
	calls int
}

func (d *settlingDevice) WaitSettled(ctx context.Context, max time.Duration) (bool, error) {
	d.calls++
	return true, nil
}

type settlingProvider struct{ *demo.Provider }

func (p settlingProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	dev, err := p.Provider.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	return &settlingDevice{Device: demo.Of(dev)}, nil
}

func TestSettlerIsUsed(t *testing.T) {
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	g, err := New(Options{Providers: []driver.Provider{settlingProvider{demo.NewProvider(driver.Android, dev)}},
		TrustNewAgents: true, NoScreenshots: true, PollInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s, first := connect(t, g, "Lunch")
	start := time.Now()
	mustStatus(t, tap(s, ref(t, first, "Dinely")), StatusSettled)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("polled instead of using the Settler")
	}
	g.mu.Lock()
	sd := g.devices["pixel"].dev.(*settlingDevice)
	g.mu.Unlock()
	if sd.calls != 1 {
		t.Fatalf("WaitSettled called %d times", sd.calls)
	}
}

func TestScopeWatchOnly(t *testing.T) {
	g, _ := newTestGateway(t)
	g.SetAgentScope(agent.Name, Scope{Mode: ScopeWatchOnly})
	s, first := connect(t, g, "Watch")
	res := tap(s, ref(t, first, "Dinely"))
	if res.Error != ErrScopeDenied {
		t.Fatalf("watch-only tap: %+v", res)
	}
	mustStatus(t, s.Observe(context.Background(), ObserveRequest{}), StatusSettled)
}

func TestTaskAppList(t *testing.T) {
	g, _ := newTestGateway(t)
	s := g.OpenSession(agent)
	defer s.Close()
	mustStatus(t, s.Connect(context.Background(), ConnectRequest{Task: "Lunch", Apps: []string{"Dinely"}}), StatusSettled)
	mustStatus(t, s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	res := s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "Chatter"})
	if res.Error != ErrScopeDenied {
		t.Fatalf("launch outside the task's apps: %+v", res)
	}
}

func TestUnclaimedApprovalExpires(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) {
		o.ApprovalTimeout = 60 * time.Millisecond
		o.MaxBlock = 10 * time.Millisecond
	})
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	place := ref(t, cart, "Place order · $12.80")
	res := tap(s, place)
	mustStatus(t, res, StatusPending)
	first := res.ApprovalID
	// Nobody answers and the agent does not come back: the timeout denies
	// it, and once the decision expires the step is closed.
	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks := g.Tasks()
		last := tasks[0].Steps[len(tasks[0].Steps)-1]
		if last.Status == string(StatusDenied) && tasks[0].State == TaskWorking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("step not closed: %+v state %s", last, tasks[0].State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A late call with the old id asks again.
	res = s.Act(context.Background(), Call{ApprovalID: first}, Action{Tool: "tap", Ref: place})
	mustStatus(t, res, StatusPending)
	if res.ApprovalID == first || dev.Current() != "cart" {
		t.Fatalf("late call: %+v", res)
	}
}

func TestPersonTouchedPausesAgent(t *testing.T) {
	g, _ := newTestGateway(t)
	s, first := connect(t, g, "Lunch")
	if err := g.PersonTouched("pixel"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, tap(s, ref(t, first, "Dinely")), StatusTookOver)
	if err := g.PersonTouched("nope"); err != ErrUnknownDevice {
		t.Fatalf("unknown device: %v", err)
	}
}

func TestEvents(t *testing.T) {
	g, _ := newTestGateway(t, func(o *Options) { o.MaxBlock = 20 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := g.Subscribe(ctx)
	seen := map[EventKind]bool{}
	s, _ := connect(t, g, "Lunch")
	cart := toCart(t, s)
	mustStatus(t, tap(s, ref(t, cart, "Place order · $12.80")), StatusPending)
	deadline := time.After(2 * time.Second)
	for !(seen[EventDevices] && seen[EventTasks] && seen[EventNeeds]) {
		select {
		case ev := <-events:
			seen[ev.Kind] = true
		case <-deadline:
			t.Fatalf("events seen: %v", seen)
		}
	}
	cancel()
	for range events {
	}
}

func TestWipeHistory(t *testing.T) {
	g, _ := newTestGateway(t, func(o *Options) { o.DataDir = t.TempDir(); o.NoScreenshots = false })
	old, _ := connect(t, g, "Old task")
	mustStatus(t, old.Act(context.Background(), Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	g.Flush() // the screenshot is taken before the next input, as agents think
	old.Release(context.Background(), ReleaseRequest{})
	s, _ := connect(t, g, "Running task")
	res := s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "Chatter"})
	mustStatus(t, res, StatusSettled)
	g.Flush()
	tasks, _ := g.History(context.Background(), HistoryQuery{})
	if len(tasks) != 2 {
		t.Fatalf("before wipe: %d tasks", len(tasks))
	}
	media := ""
	for _, task := range tasks {
		for _, st := range task.Steps {
			if st.After != "" {
				media = st.After
			}
		}
	}
	if media == "" {
		t.Fatal("no step has a screenshot")
	}
	if err := g.WipeHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.MediaPath(context.Background(), media); err == nil {
		t.Fatal("media survived the wipe")
	}
	tasks, _ = g.History(context.Background(), HistoryQuery{})
	if len(tasks) != 1 || tasks[0].Title != "Running task" {
		t.Fatalf("after wipe: %+v", tasks)
	}
	// The running task keeps recording.
	mustStatus(t, s.Act(context.Background(), Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	tasks, _ = g.History(context.Background(), HistoryQuery{Search: "running"})
	if len(tasks) != 1 || len(tasks[0].Steps) == 0 {
		t.Fatalf("running task after wipe: %+v", tasks)
	}
}

func TestPersonInputOnPhoneTakesOver(t *testing.T) {
	g, dev := newTestGateway(t)
	s, first := connect(t, g, "Lunch")
	home := ref(t, first, "Dinely")
	dev.PersonTouch()
	deadline := time.Now().Add(2 * time.Second)
	for g.Devices()[0].Status != DeviceInControl {
		if time.Now().After(deadline) {
			t.Fatalf("device status %s after a touch on the phone", g.Devices()[0].Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mustStatus(t, tap(s, home), StatusTookOver)
}

func TestGatewayBlockedApps(t *testing.T) {
	g, _ := newTestGateway(t)
	got := g.BlockedApps(context.Background())
	if len(got) != 1 || got[0].ID != "com.google.android.apps.walletnfcrel" || got[0].Category != policy.CatFinance {
		t.Fatalf("blocked apps on the demo phone: %+v", got)
	}
	if err := g.SetAppBlocked(screen.App{ID: "com.dinely.app", Name: "Dinely"}, true); err != nil {
		t.Fatal(err)
	}
	s, _ := connect(t, g, "Lunch")
	go func() {
		n := waitNeed(t, g)
		if !n.Unblock || n.Rule != policy.RuleBlocked {
			t.Errorf("need: %+v", n)
		}
		g.Decide(n.ID, Deny, "")
	}()
	if res := s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "Dinely"}); res.Status != StatusDenied || res.Rule != policy.RuleBlocked {
		t.Fatalf("launch a blocked app: %+v", res)
	}
	if got := g.BlockedApps(context.Background()); len(got) != 2 {
		t.Fatalf("after blocking Dinely: %+v", got)
	}
}

func TestAgentViewDropsStatusBarOCR(t *testing.T) {
	g, _ := newTestGateway(t)
	raw := &screen.Screen{Width: 1125, Height: 2436, App: screen.App{Name: "Calculator", ID: "com.apple.calculator"}, Elements: []screen.Element{
		{Source: screen.SourceOCR, Label: "5:05", Bounds: screen.Rect{X: 120, Y: 70, W: 90, H: 36}},
		{Source: screen.SourceOCR, Label: "100", Bounds: screen.Rect{X: 990, Y: 72, W: 60, H: 34}},
		{Source: screen.SourceOCR, Label: "5:06", Bounds: screen.Rect{X: 120, Y: 62, W: 90, H: 54}}, // reaches below 4.5%
		{Source: screen.SourceOCR, Label: "6x7", Bounds: screen.Rect{X: 950, Y: 640, W: 100, H: 44}},
		{Role: screen.RoleButton, Label: "Equals", Bounds: screen.Rect{X: 840, Y: 2100, W: 230, H: 230}},
	}}
	view, _ := g.agentView(nil, raw)
	var labels []string
	for _, e := range view.Elements {
		labels = append(labels, e.Label)
	}
	if strings.Join(labels, ",") != "6x7,Equals" {
		t.Fatalf("agent sees %q", labels)
	}
	if len(raw.Elements) != 5 {
		t.Fatal("the raw screen was changed")
	}
}

// TestIdlePhoneLockShows reads an idle phone's lock state, so the client
// can show it and offer to unlock from the computer before an agent needs
// it.
func TestIdlePhoneLockShows(t *testing.T) {
	saved := idleLockEvery
	idleLockEvery = 10 * time.Millisecond
	t.Cleanup(func() { idleLockEvery = saved })
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x)
	status := func() DeviceStatus { return g.Devices()[0].Status }
	x.SetLocked(true)
	for deadline := time.Now().Add(3 * time.Second); status() != DeviceLocked; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("an idle locked phone shows %q", status())
		}
	}
	x.SetLocked(false)
	for deadline := time.Now().Add(3 * time.Second); status() != DeviceIdle; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("an idle unlocked phone shows %q", status())
		}
	}
}

// TestDefaultAsksBeforePaymentsOnly runs the bundled policy as it ships:
// a message goes out without asking, a payment still asks.
func TestDefaultAsksBeforePaymentsOnly(t *testing.T) {
	g, dev := newTestGateway(t, func(o *Options) {
		pol := policy.Default()
		for cat, e := range demo.PolicyApps() {
			pol.AddApps(cat, e)
		}
		o.Policy, o.MaxBlock = pol, 50*time.Millisecond
	})
	if p := g.Policy().Preset(); p != policy.PresetPayments {
		t.Fatalf("default preset %d", p)
	}
	ctx := context.Background()
	s, _ := connect(t, g, "Chat")
	dev.Show("chats")
	res := tap(s, ref(t, s.Observe(ctx, ObserveRequest{}), "Sam"))
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "type", Ref: ref(t, res, "Message"), Text: "hi", Submit: true}), StatusSettled)
	if chat := dev.Chat(); chat[len(chat)-1] != "Me: hi" || len(g.Needs()) != 0 {
		t.Fatalf("the message asked or did not go: %v, needs %+v", chat, g.Needs())
	}
	cart := toCart(t, s)
	res = tap(s, ref(t, cart, "Place order · $12.80"))
	mustStatus(t, res, StatusPending)
	if n := g.Needs(); len(n) != 1 || n[0].Rule != "Payments" || dev.Current() != "cart" {
		t.Fatalf("the payment did not ask: needs %+v, screen %s", n, dev.Current())
	}
}
