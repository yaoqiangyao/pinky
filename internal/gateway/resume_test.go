package gateway

import (
	"context"
	"strings"
	"testing"
	"time"
)

// grok is a remote agent whose client opens an MCP session for every
// call: each of its calls comes in a session of its own.
var grok = AgentInfo{Name: "Grok Bot (remote)", Client: "grok-bot 1", Resume: true}

// call opens a new session of a, as such a client does.
func call(t *testing.T, g *Gateway, a AgentInfo) *Session {
	t.Helper()
	s := g.OpenSession(a)
	t.Cleanup(s.CloseNow)
	return s
}

func TestResumeContinuesLease(t *testing.T) {
	ctx := context.Background()
	g, dev := newTestGateway(t)
	first := call(t, g, grok)
	mustStatus(t, first.Connect(ctx, ConnectRequest{Device: "Pixel 9", Task: "Reorder lunch"}), StatusSettled)
	taskID := g.Devices()[0].TaskID

	// The next call, in a new session, continues the task: by device, or
	// without one.
	second := call(t, g, grok)
	res := second.Act(ctx, Call{Device: "Pixel 9"}, Action{Tool: "launch", App: "Dinely"})
	mustStatus(t, res, StatusSettled)
	third := call(t, g, grok)
	mustStatus(t, tap(third, ref(t, res, "Orders")), StatusSettled)
	if d := g.Devices()[0]; d.TaskID != taskID || !strings.HasPrefix(d.StatusLine, "Grok Bot (remote) · Reorder lunch") {
		t.Fatalf("device %+v, want the same task %s", d, taskID)
	}
	if dev.Current() != "orders" {
		t.Fatalf("screen %s", dev.Current())
	}

	// An older session never takes it back; it is told the phone is in use.
	if res := first.Act(ctx, Call{Device: "Pixel 9"}, Action{Tool: "key", Key: "back"}); res.Error != ErrInvalidArgument {
		t.Fatalf("older session: %+v", res)
	}
	if res := first.Connect(ctx, ConnectRequest{Device: "Pixel 9", Task: "Reorder lunch"}); res.Error != ErrLeaseHeld {
		t.Fatalf("older session's connect: %+v", res)
	}

	// Other agents, and the same name on this computer, never take it.
	for _, a := range []AgentInfo{{Name: "Codex (remote)", Resume: true}, {Name: "Grok Bot (remote)"}} {
		other := call(t, g, a)
		if res := other.Connect(ctx, ConnectRequest{Device: "Pixel 9", Task: "Mine now"}); res.Error != ErrLeaseHeld {
			t.Fatalf("%s: %+v", a.Name, res)
		}
		if res := other.Act(ctx, Call{Device: "Pixel 9"}, Action{Tool: "key", Key: "back"}); res.Error != ErrInvalidArgument {
			t.Fatalf("%s acting: %+v", a.Name, res)
		}
	}

	// The same task again from a new session keeps going; release frees it.
	fourth := call(t, g, grok)
	mustStatus(t, fourth.Connect(ctx, ConnectRequest{Task: "Reorder lunch"}), StatusSettled)
	if d := g.Devices()[0]; d.TaskID != taskID {
		t.Fatalf("connect again started task %s, want %s", d.TaskID, taskID)
	}
	fifth := call(t, g, grok)
	if res := fifth.Release(ctx, ReleaseRequest{Summary: "Ordered"}); res.Message != "Released Pixel 9." {
		t.Fatalf("release: %+v", res)
	}
	if d := g.Devices()[0]; d.Status != DeviceIdle {
		t.Fatalf("after release: %+v", d)
	}
}

// A call that is still running keeps its session's lease: the newer
// session waits for it, then carries on with its approval.
func TestResumeWaitsForRunningCall(t *testing.T) {
	ctx := context.Background()
	g, dev := newTestGateway(t, func(o *Options) { o.MaxBlock = 300 * time.Millisecond })
	first := call(t, g, grok)
	mustStatus(t, first.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
	cart := toCart(t, first)
	place := ref(t, cart, "Place order · $12.80")

	done := make(chan *Result, 1)
	go func() { done <- tap(first, place) }()
	waitNeedKind(t, g, NeedApproval)
	second := call(t, g, grok)
	if res := second.Act(ctx, Call{Device: "pixel"}, Action{Tool: "tap", Ref: place}); res.Error != ErrLeaseHeld || !strings.Contains(res.Message, "still busy") {
		t.Fatalf("while the first call runs: %+v", res)
	}
	pending := <-done
	if pending.Status != StatusPending || pending.ApprovalID == "" {
		t.Fatalf("first call: %+v", pending)
	}

	// The next session repeats the call with the approval id, which moved
	// with the lease.
	third := call(t, g, grok)
	go func() { done <- third.Act(ctx, Call{ApprovalID: pending.ApprovalID}, Action{Tool: "tap", Ref: place}) }()
	time.Sleep(50 * time.Millisecond)
	if err := g.Decide(pending.ApprovalID, AllowOnce, "ok"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, <-done, StatusSettled)
	if dev.Current() != "done" {
		t.Fatalf("screen %s", dev.Current())
	}
}

// A session that closes leaves its lease for a while to the agent's next
// session, then it ends.
func TestResumeAfterClose(t *testing.T) {
	ctx := context.Background()
	g, _ := newTestGateway(t, func(o *Options) { o.ResumeGrace = 200 * time.Millisecond })
	first := call(t, g, grok)
	mustStatus(t, first.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
	first.Close()
	if d := g.Devices()[0]; d.Status == DeviceIdle {
		t.Fatal("the lease ended with the session")
	}
	second := call(t, g, grok)
	mustStatus(t, second.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	time.Sleep(300 * time.Millisecond)
	if d := g.Devices()[0]; d.Status == DeviceIdle {
		t.Fatal("the continued lease ended with the grace of the closed session")
	}
	second.Close()
	deadline := time.Now().Add(2 * time.Second)
	for g.Devices()[0].Status != DeviceIdle {
		if time.Now().After(deadline) {
			t.Fatal("the kept lease never ended")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Agents on this computer end their leases with the session.
	local := g.OpenSession(agent)
	mustStatus(t, local.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
	local.Close()
	if d := g.Devices()[0]; d.Status != DeviceIdle {
		t.Fatalf("a local session's lease outlived it: %+v", d)
	}

	// Ending kept leases ends them at once.
	third := call(t, g, grok)
	mustStatus(t, third.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
	third.Close()
	g.EndKeptLeases()
	if d := g.Devices()[0]; d.Status != DeviceIdle {
		t.Fatalf("kept lease after EndKeptLeases: %+v", d)
	}
}

// The person's Stop holds for the agent's newer sessions.
func TestResumeStopHolds(t *testing.T) {
	ctx := context.Background()
	g, _ := newTestGateway(t)
	earlier := call(t, g, grok) // a session that started before the stopped one
	first := call(t, g, grok)
	mustStatus(t, first.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
	if err := g.Stop("pixel"); err != nil {
		t.Fatal(err)
	}
	first.Close()
	next := call(t, g, grok)
	if res := next.Connect(ctx, ConnectRequest{Task: "Lunch, again"}); res.Status != StatusStopped || !strings.Contains(res.Message, "10 min") {
		t.Fatalf("connect after Stop: %+v", res)
	}
	if res := next.Act(ctx, Call{Device: "pixel"}, Action{Tool: "key", Key: "home"}); res.Status != StatusStopped {
		t.Fatalf("act after Stop: %+v", res)
	}
	if res := next.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}); res.Status != StatusStopped {
		t.Fatalf("act without device after Stop: %+v", res)
	}
	// Not for other agents, nor for a session that started before it.
	mustStatus(t, call(t, g, AgentInfo{Name: "Codex (remote)", Resume: true}).Connect(ctx, ConnectRequest{Task: "Other"}), StatusSettled)
	if res := earlier.Connect(ctx, ConnectRequest{Task: "Lunch"}); res.Error != ErrLeaseHeld {
		t.Fatalf("an earlier session: %+v", res)
	}

	// Stop all too.
	g2, _ := newTestGateway(t)
	s := call(t, g2, grok)
	mustStatus(t, s.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
	if err := g2.StopAll(); err != nil {
		t.Fatal(err)
	}
	if res := call(t, g2, grok).Connect(ctx, ConnectRequest{Task: "Lunch"}); res.Status != StatusStopped {
		t.Fatalf("connect after Stop all: %+v", res)
	}
}

// The first connection's approval carries over to the agent's next
// session, which repeats the call.
func TestResumeNewAgentApproval(t *testing.T) {
	ctx := context.Background()
	g, _ := newTestGateway(t, func(o *Options) { o.TrustNewAgents = false; o.MaxBlock = 200 * time.Millisecond })
	res := call(t, g, grok).Connect(ctx, ConnectRequest{Task: "Lunch"})
	if res.Status != StatusPending || res.Rule != "New agent" {
		t.Fatalf("first connection: %+v", res)
	}
	done := make(chan *Result, 1)
	go func() {
		done <- call(t, g, grok).Connect(ctx, ConnectRequest{Task: "Lunch", Call: Call{ApprovalID: res.ApprovalID}})
	}()
	time.Sleep(50 * time.Millisecond)
	if n := g.Needs(); len(n) != 1 {
		t.Fatalf("needs %+v, want the one card", n)
	}
	if err := g.Decide(res.ApprovalID, AllowAlways, ""); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, <-done, StatusSettled)
}
