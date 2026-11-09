package approval

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var bind = Binding{Session: "s1", Device: "d1", Lease: "l1", Tool: "phone_tap", Args: "ref=15"}

func TestDecideWhileWaiting(t *testing.T) {
	var changes atomic.Int32
	c := New(Options{Timeout: time.Second, OnChange: func() { changes.Add(1) }})
	a := c.Open(Request{Kind: KindAction, Binding: bind, Rule: "Payments"})
	go func() {
		time.Sleep(20 * time.Millisecond)
		if err := c.Decide(a.ID, AllowOnce, "ok"); err != nil {
			t.Error(err)
		}
	}()
	out, err := c.Wait(context.Background(), a.ID, time.Second)
	if err != nil || out.State != Used || out.Decision != AllowOnce || out.Note != "ok" {
		t.Fatalf("wait: %+v %v", out, err)
	}
	if _, err := c.Wait(context.Background(), a.ID, time.Second); !errors.Is(err, ErrUsed) {
		t.Fatalf("second use: %v", err)
	}
	if changes.Load() < 2 {
		t.Fatalf("OnChange ran %d times", changes.Load())
	}
}

func TestPendingThenDecisionKept(t *testing.T) {
	c := New(Options{Timeout: time.Second})
	a := c.Open(Request{Kind: KindAction, Binding: bind})
	out, err := c.Wait(context.Background(), a.ID, 20*time.Millisecond)
	if err != nil || out.State != Open {
		t.Fatalf("want pending, got %+v %v", out, err)
	}
	if busy, ok := c.Busy("d1"); !ok || busy.ID != a.ID {
		t.Fatal("device not busy while the approval is open")
	}
	if err := c.Decide(a.ID, Deny, "not now"); err != nil {
		t.Fatal(err)
	}
	if err := c.Decide(a.ID, AllowOnce, ""); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("second decision: %v", err)
	}
	if _, ok := c.Busy("d1"); !ok {
		t.Fatal("a decision no call picked up should still hold the device")
	}
	// No call is waiting; the decision is kept for the next one.
	time.Sleep(30 * time.Millisecond)
	out, err = c.Wait(context.Background(), a.ID, time.Second)
	if err != nil || out.Decision != Deny || out.Note != "not now" {
		t.Fatalf("kept decision: %+v %v", out, err)
	}
	if _, ok := c.Busy("d1"); ok {
		t.Fatal("device still busy after the decision was used")
	}
}

func TestTimeoutDenies(t *testing.T) {
	c := New(Options{Timeout: 40 * time.Millisecond})
	a := c.Open(Request{Kind: KindAction, Binding: bind})
	out, err := c.Wait(context.Background(), a.ID, time.Second)
	if err != nil || out.Decision != Deny || !out.TimedOut {
		t.Fatalf("timeout: %+v %v", out, err)
	}
	if out.Note == "" {
		t.Fatal("timeout without a note")
	}
}

func TestDecisionExpires(t *testing.T) {
	expired := make(chan Info, 1)
	// A timeout far beyond the wait below: what expires it is Keep.
	c := New(Options{Timeout: time.Minute, Keep: 30 * time.Millisecond, OnExpire: func(i Info) { expired <- i }})
	a := c.Open(Request{Kind: KindAction, Binding: bind})
	if err := c.Decide(a.ID, AllowOnce, ""); err != nil {
		t.Fatal(err)
	}
	// Not a fixed sleep: after a long stall of the test, its Wait could
	// run before the expiry's timer does.
	select {
	case i := <-expired:
		if i.ID != a.ID {
			t.Fatalf("expired %s, not %s", i.ID, a.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the decision did not expire")
	}
	if _, err := c.Wait(context.Background(), a.ID, time.Second); !errors.Is(err, ErrExpired) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestBindingAndWithdraw(t *testing.T) {
	c := New(Options{Timeout: time.Second})
	a := c.Open(Request{Kind: KindAction, Binding: bind})
	if err := c.Check(a.ID, bind); err != nil {
		t.Fatal(err)
	}
	other := bind
	other.Args = "ref=16"
	if err := c.Check(a.ID, other); !errors.Is(err, ErrMismatch) {
		t.Fatalf("other args: %v", err)
	}
	if err := c.Check("ap_nope", bind); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown id: %v", err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		c.Withdraw(a.ID, "stopped")
	}()
	out, err := c.Wait(context.Background(), a.ID, time.Second)
	if err != nil || out.State != Withdrawn || out.Reason != "stopped" {
		t.Fatalf("withdraw: %+v %v", out, err)
	}
	if len(c.List(true)) != 0 {
		t.Fatal("withdrawn approval still listed")
	}
}

func TestWaitCancelled(t *testing.T) {
	c := New(Options{Timeout: time.Second})
	a := c.Open(Request{Kind: KindAction, Binding: bind})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Wait(ctx, a.ID, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	if len(c.List(false)) != 1 {
		t.Fatal("a cancelled wait must not decide the approval")
	}
}

func TestHuman(t *testing.T) {
	for d, want := range map[time.Duration]string{60 * time.Second: "60 s", 15 * time.Minute: "15 min", 500 * time.Millisecond: "500 ms"} {
		if got := Human(d); got != want {
			t.Errorf("Human(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestOpenAfterClose(t *testing.T) {
	c := New(Options{Timeout: time.Second})
	c.Close()
	a := c.Open(Request{Kind: KindAction, Binding: bind})
	if len(c.List(true)) != 0 {
		t.Fatal("approval opened after Close")
	}
	if _, err := c.Wait(context.Background(), a.ID, time.Second); !errors.Is(err, ErrUsed) {
		t.Fatalf("wait after close: %v", err)
	}
}

// TestWaitDecidedAsTimeRunsOut: a decision there when the wait's time runs
// out is handed over, not reported as still open, which would cost the
// agent a repeated call.
func TestWaitDecidedAsTimeRunsOut(t *testing.T) {
	c := New(Options{Timeout: time.Minute})
	defer c.Close()
	for i := range 500 {
		a := c.Open(Request{Kind: KindAction, Binding: bind})
		if err := c.Decide(a.ID, AllowOnce, ""); err != nil {
			t.Fatal(err)
		}
		// The decision and the wait's end are both there when it looks.
		out, err := c.Wait(context.Background(), a.ID, time.Nanosecond)
		if err != nil || out.State != Used {
			t.Fatalf("try %d: %+v, %v", i, out, err)
		}
	}
}
