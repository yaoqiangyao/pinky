// Package approval holds the calls that wait for the person: Ask actions,
// an agent's first connection, phone_request_approval and phone_handoff.
//
// Only the client decides. The gateway exposes Decide on its client API and
// never to agents, so a prompt-injected agent cannot approve its own action.
// An approval:
//
//   - times out from when it opens (60 s by default) and then counts as Deny;
//   - is bound to the session, device, lease, tool and arguments of the call
//     that opened it, and a call with other arguments is refused;
//   - is single-use: the call that receives the decision consumes it;
//   - keeps a decision made while no call is waiting until the next call
//     with its id, for as long as the timeout.
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"sort"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

// Decision is the person's answer.
type Decision string

// Decisions. They match gateway.Decision.
const (
	AllowOnce   Decision = "allow_once"
	AllowTask   Decision = "allow_task"
	AllowAlways Decision = "allow_always"
	Deny        Decision = "deny"
	TakeOver    Decision = "take_over"
)

// Allows reports whether the decision lets the call go ahead.
func (d Decision) Allows() bool { return d == AllowOnce || d == AllowTask || d == AllowAlways }

// Valid reports whether d is a known decision.
func (d Decision) Valid() bool { return d.Allows() || d == Deny || d == TakeOver }

// Kind says what an approval holds.
type Kind string

// Kinds.
const (
	KindAction  Kind = "action"  // an Ask action
	KindAgent   Kind = "agent"   // an agent's first connection
	KindRequest Kind = "request" // phone_request_approval
	KindHandoff Kind = "handoff" // phone_handoff: the agent waits for the person
)

// Binding ties an approval to the call that opened it.
type Binding struct {
	Session string
	Device  string
	Lease   string
	Tool    string
	Args    string // a digest of the arguments
}

// Request describes what to show the person.
type Request struct {
	Kind    Kind
	Binding Binding
	Rule    string // the rule that asked, such as "Payments"
	Title   string // "Pay $12.80?"
	Detail  string // "Claude Code · 1 chicken rice bowl"
	App     string // the app's display name
	Agent   string
	TaskID  string
	// DeviceName names the phone on the card.
	DeviceName string
	Screenshot image.Image
	Target     screen.Rect
	// Point is where the finger goes down and, for a swipe, To where it
	// lifts, in Screenshot pixels; both zero when the action has no
	// position.
	Point, To screen.Point
	// Timeout overrides the center's timeout; handoffs wait longer.
	Timeout time.Duration
	// Data is the gateway's own state, such as what to re-check on approval.
	Data any
}

// State is where an approval is in its life.
type State int

// States.
const (
	Open      State = iota // waiting for the person
	Decided                // answered, waiting for the call to pick it up
	Withdrawn              // taken back without a decision
	Used                   // a call received the decision
)

func (s State) String() string {
	return [...]string{"open", "decided", "withdrawn", "used"}[s]
}

// Info is a snapshot of one approval.
type Info struct {
	ID string
	Request
	Created   time.Time
	Deadline  time.Time
	State     State
	Decision  Decision
	Note      string
	TimedOut  bool
	DecidedAt time.Time
	Reason    string // why it was withdrawn
}

// Outcome is what a waiting call learns.
type Outcome struct {
	// State is Open when the wait ended before a decision (the call should
	// return approval_pending), Withdrawn, or Used when a decision arrived.
	State    State
	Decision Decision
	Note     string
	TimedOut bool
	Reason   string
	// Waited is how long the person took, from opening to the decision.
	Waited time.Duration
}

// Errors.
var (
	ErrUnknown  = errors.New("unknown approval id")
	ErrMismatch = errors.New("approval id belongs to a different call")
	ErrUsed     = errors.New("approval id was already used")
	ErrExpired  = errors.New("approval expired before a call picked it up")
	ErrNotOpen  = errors.New("approval is not waiting for a decision")
	ErrDecision = errors.New("unknown decision")
)

// Options configure a Center.
type Options struct {
	// Timeout is how long the person has to answer; default 60 s.
	Timeout time.Duration
	// Keep is how long a decision waits for the next call; default Timeout.
	Keep time.Duration
	// Remember is how long finished ids are remembered, to answer late
	// calls; default 10 minutes.
	Remember time.Duration
	// OnChange runs after the set of approvals changed, without locks held.
	OnChange func()
	// OnExpire runs, without locks held, when a decision expires because no
	// call picked it up.
	OnExpire func(Info)
}

// Center holds approvals.
type Center struct {
	opt Options

	mu     sync.Mutex
	all    map[string]*entry
	tombs  map[string]tomb
	closed bool
}

type entry struct {
	Info
	done  chan struct{} // closed when no longer open
	timer *time.Timer
}

type tomb struct {
	info Info
	err  error
	at   time.Time
}

// New returns a Center.
func New(o Options) *Center {
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.Keep <= 0 {
		o.Keep = o.Timeout
	}
	if o.Remember <= 0 {
		o.Remember = 10 * time.Minute
	}
	return &Center{opt: o, all: map[string]*entry{}, tombs: map[string]tomb{}}
}

// Timeout returns the default answer time.
func (c *Center) Timeout() time.Duration { return c.opt.Timeout }

func newID() string {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "ap_" + hex.EncodeToString(b[:])
}

// Open starts an approval and returns its snapshot.
func (c *Center) Open(r Request) Info {
	now := time.Now()
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = c.opt.Timeout
	}
	e := &entry{Info: Info{ID: newID(), Request: r, Created: now, Deadline: now.Add(timeout), State: Open},
		done: make(chan struct{})}
	c.mu.Lock()
	if c.closed {
		// The gateway is stopping: the approval is withdrawn at once.
		e.State, e.Reason = Withdrawn, "gateway stopped"
		close(e.done)
		c.buryLocked(e.Info, ErrUsed)
		info := e.Info
		c.mu.Unlock()
		return info
	}
	c.all[e.ID] = e
	id := e.ID
	e.timer = time.AfterFunc(timeout, func() { c.expire(id) })
	info := e.Info
	c.mu.Unlock()
	c.changed()
	return info
}

// expire runs at the deadline: an open approval becomes Deny.
func (c *Center) expire(id string) {
	c.mu.Lock()
	e, ok := c.all[id]
	if !ok || e.State != Open {
		c.mu.Unlock()
		return
	}
	e.State, e.Decision, e.TimedOut, e.DecidedAt = Decided, Deny, true, time.Now()
	e.Note = "No answer in " + Human(e.Deadline.Sub(e.Created)) + "."
	close(e.done)
	c.keepLocked(e)
	c.mu.Unlock()
	c.changed()
}

// Human writes a duration the way the panel and agents read it: "60 s",
// "15 min".
func Human(d time.Duration) string {
	switch {
	case d < 2*time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < 2*time.Minute:
		return fmt.Sprintf("%d s", int(d.Round(time.Second)/time.Second))
	default:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	}
}

// keepLocked schedules the removal of a decided approval that no call
// picks up.
func (c *Center) keepLocked(e *entry) {
	id := e.ID
	e.timer = time.AfterFunc(c.opt.Keep, func() {
		c.mu.Lock()
		e, ok := c.all[id]
		expired := ok && e.State == Decided
		var info Info
		if expired {
			info = e.Info
			delete(c.all, id)
			c.buryLocked(e.Info, ErrExpired)
		}
		c.mu.Unlock()
		if expired {
			if c.opt.OnExpire != nil {
				c.opt.OnExpire(info)
			}
			c.changed()
		}
	})
}

func (c *Center) buryLocked(info Info, err error) {
	info.Screenshot = nil
	info.Data = nil
	now := time.Now()
	c.tombs[info.ID] = tomb{info: info, err: err, at: now}
	for id, t := range c.tombs {
		if now.Sub(t.at) > c.opt.Remember {
			delete(c.tombs, id)
		}
	}
}

func (c *Center) changed() {
	if c.opt.OnChange != nil {
		c.opt.OnChange()
	}
}

// Get returns the approval's snapshot. For an id that is finished it
// returns the last snapshot and the reason it is gone (ErrUsed or
// ErrExpired).
func (c *Center) Get(id string) (Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.all[id]; ok {
		return e.Info, nil
	}
	if t, ok := c.tombs[id]; ok {
		return t.info, t.err
	}
	return Info{}, ErrUnknown
}

// Check verifies that a call may use the approval.
func (c *Center) Check(id string, b Binding) error {
	info, err := c.Get(id)
	if err != nil {
		return err
	}
	if info.Binding != b {
		return ErrMismatch
	}
	return nil
}

// Wait blocks until the approval is decided, max passes, or ctx ends. A
// decision is handed to exactly one call; that call consumes the approval.
// When max passes first, the outcome's State is Open and the approval stays.
func (c *Center) Wait(ctx context.Context, id string, max time.Duration) (Outcome, error) {
	c.mu.Lock()
	e, ok := c.all[id]
	if !ok {
		c.mu.Unlock()
		if _, err := c.Get(id); err != nil {
			return Outcome{}, err
		}
		return Outcome{}, ErrUnknown
	}
	done := e.done
	c.mu.Unlock()

	var timeout <-chan time.Time
	if max > 0 {
		t := time.NewTimer(max)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-done:
	case <-timeout:
		// A decision that came as the time ran out is handed over all the
		// same: select picks either when both are there.
		select {
		case <-done:
		default:
			return Outcome{State: Open}, nil
		}
	case <-ctx.Done():
		return Outcome{State: Open}, ctx.Err()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok = c.all[id]
	if !ok {
		// Another call took it, or it expired, in between.
		if t, ok := c.tombs[id]; ok {
			return Outcome{}, t.err
		}
		return Outcome{}, ErrUnknown
	}
	out := Outcome{State: e.State, Decision: e.Decision, Note: e.Note, TimedOut: e.TimedOut, Reason: e.Reason,
		Waited: e.DecidedAt.Sub(e.Created)}
	switch e.State {
	case Decided:
		out.State = Used
		e.State = Used
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(c.all, id)
		c.buryLocked(e.Info, ErrUsed)
	case Withdrawn:
		delete(c.all, id)
		c.buryLocked(e.Info, ErrUsed)
	}
	return out, nil
}

// Decide records the person's answer. Only the client calls it.
func (c *Center) Decide(id string, d Decision, note string) error {
	if !d.Valid() {
		return ErrDecision
	}
	c.mu.Lock()
	e, ok := c.all[id]
	if !ok {
		c.mu.Unlock()
		if _, err := c.Get(id); err != nil && !errors.Is(err, ErrUnknown) {
			return ErrNotOpen
		}
		return ErrUnknown
	}
	if e.State != Open {
		c.mu.Unlock()
		return ErrNotOpen
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.State, e.Decision, e.Note, e.DecidedAt = Decided, d, note, time.Now()
	close(e.done)
	c.keepLocked(e)
	c.mu.Unlock()
	c.changed()
	return nil
}

// Withdraw takes back an open or decided approval without acting, for a
// cancelled call, a closed session or Stop. Waiting calls see Withdrawn.
func (c *Center) Withdraw(id, reason string) {
	c.mu.Lock()
	e, ok := c.all[id]
	if !ok || (e.State != Open && e.State != Decided) {
		c.mu.Unlock()
		return
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	wasOpen := e.State == Open
	e.State, e.Reason = Withdrawn, reason
	if wasOpen {
		close(e.done)
	}
	// Keep it briefly so a waiting call can read the reason.
	id = e.ID
	time.AfterFunc(c.opt.Keep, func() {
		c.mu.Lock()
		if e, ok := c.all[id]; ok && e.State == Withdrawn {
			delete(c.all, id)
			c.buryLocked(e.Info, ErrUsed)
		}
		c.mu.Unlock()
	})
	c.mu.Unlock()
	c.changed()
}

// WithdrawWhere withdraws every open or decided approval that f selects
// and returns them.
func (c *Center) WithdrawWhere(f func(Info) bool, reason string) []Info {
	var out []Info
	for _, info := range c.List(true) {
		if f(info) {
			c.Withdraw(info.ID, reason)
			out = append(out, info)
		}
	}
	return out
}

// Rebind moves the open and decided approvals f selects to another
// session: a newer session of the agent continues the lease they belong
// to, and repeats their calls.
func (c *Center) Rebind(f func(Info) bool, session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.all {
		if f(e.Info) {
			e.Binding.Session = session
		}
	}
}

// List returns the approvals waiting for the person, oldest first. With
// pending true it also includes decided ones that no call picked up yet.
func (c *Center) List(pending bool) []Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Info
	for _, e := range c.all {
		if e.State == Open || (pending && e.State == Decided) {
			out = append(out, e.Info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Busy returns the approval that holds a device: open, or decided and not
// yet picked up. While one exists the device takes no other action.
func (c *Center) Busy(device string) (Info, bool) {
	if device == "" {
		return Info{}, false
	}
	for _, info := range c.List(true) {
		if info.Binding.Device == device && info.Kind != KindAgent {
			return info, true
		}
	}
	return Info{}, false
}

// Close withdraws everything; approvals opened later are withdrawn at once.
func (c *Center) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.WithdrawWhere(func(Info) bool { return true }, "gateway stopped")
}
