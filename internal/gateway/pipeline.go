package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"sort"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/approval"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// Action is one input: phone_tap, phone_type, phone_swipe, phone_key or
// phone_launch, or a step of phone_batch.
type Action struct {
	Tool       string // tap, type, swipe, key or launch
	Ref        int
	Point      *screen.Point // tap at x,y instead of a ref
	LongPress  bool
	Text       string
	Append     bool
	Submit     bool
	Direction  string // up, down, left or right: where the finger moves
	From, To   *screen.Point
	DurationMS int
	Key        string
	App        string
	// Intent and ApprovalID are per step in phone_batch.
	Intent     string
	ApprovalID string
}

// Keys agents may press.
var agentKeys = map[string]driver.Key{
	"back": driver.KeyBack, "home": driver.KeyHome, "recents": driver.KeyRecents, "enter": driver.KeyEnter,
	"tab": driver.KeyTab, "delete": driver.KeyDelete, "forward_delete": driver.KeyForwardDelete,
	"escape": driver.KeyEscape, "dpad_up": driver.KeyDpadUp, "dpad_down": driver.KeyDpadDown,
	"dpad_left": driver.KeyDpadLeft, "dpad_right": driver.KeyDpadRight, "page_up": driver.KeyPageUp,
	"page_down": driver.KeyPageDown,
}

// KeyNames lists the keys phone_key accepts.
func KeyNames() []string {
	out := make([]string, 0, len(agentKeys))
	for k := range agentKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// check validates the arguments before anything touches the device.
func (a Action) check() *Result {
	bad := func(format string, args ...any) *Result { return errResult(ErrInvalidArgument, format, args...) }
	switch a.Tool {
	case "tap":
		if (a.Ref > 0) == (a.Point != nil) {
			return bad("phone_tap takes either ref or x and y.")
		}
	case "type":
		if a.Text == "" && !a.Submit {
			return bad("text is required.")
		}
		if n := len([]rune(a.Text)); n > MaxTextLen {
			return bad("text is %d characters; the limit is %d.", n, MaxTextLen)
		}
	case "swipe":
		if a.DurationMS != 0 && (a.DurationMS < 80 || a.DurationMS > 1200) {
			return bad("duration_ms must be between 80 and 1200.")
		}
		hasDir := a.Direction != ""
		hasPts := a.From != nil || a.To != nil
		switch {
		case hasDir && hasPts:
			return bad("phone_swipe takes a direction (optionally with ref) or from and to, not both.")
		case hasPts && (a.From == nil || a.To == nil):
			return bad("phone_swipe needs both from and to.")
		case !hasDir && !hasPts:
			return bad("phone_swipe needs a direction, or from and to.")
		case hasDir && !validDirection(a.Direction):
			return bad("direction must be up, down, left or right.")
		}
	case "key":
		if _, ok := agentKeys[a.Key]; !ok {
			return bad("unknown key %q; use one of %s.", a.Key, strings.Join(KeyNames(), ", "))
		}
	case "launch":
		if strings.TrimSpace(a.App) == "" {
			return bad("app is required: a name, package name or bundle id.")
		}
	default:
		return bad("unknown action %q.", a.Tool)
	}
	return nil
}

func validDirection(d string) bool { return d == "up" || d == "down" || d == "left" || d == "right" }

// digest is the canonical form of the arguments, for approval binding.
func (a Action) digest() string {
	p := func(pt *screen.Point) string {
		if pt == nil {
			return "-"
		}
		return fmt.Sprintf("%d,%d", pt.X, pt.Y)
	}
	return digest(fmt.Sprintf("%s|ref=%d|at=%s|long=%v|text=%q|append=%v|submit=%v|dir=%s|from=%s|to=%s|dur=%d|key=%s|app=%s",
		a.Tool, a.Ref, p(a.Point), a.LongPress, a.Text, a.Append, a.Submit, a.Direction, p(a.From), p(a.To), a.DurationMS, a.Key, a.App))
}

func digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:12])
}

// args returns the arguments as JSON for history.
func (a Action) args(c Call) string {
	m := map[string]any{}
	if a.Ref > 0 {
		m["ref"] = a.Ref
	}
	if a.Point != nil {
		m["x"], m["y"] = a.Point.X, a.Point.Y
	}
	if a.LongPress {
		m["long_press"] = true
	}
	if a.Text != "" {
		m["text"] = a.Text
	}
	if a.Append {
		m["append"] = true
	}
	if a.Submit {
		m["submit"] = true
	}
	if a.Direction != "" {
		m["direction"] = a.Direction
	}
	if a.From != nil {
		m["from"] = map[string]int{"x": a.From.X, "y": a.From.Y}
	}
	if a.To != nil {
		m["to"] = map[string]int{"x": a.To.X, "y": a.To.Y}
	}
	if a.DurationMS != 0 {
		m["duration_ms"] = a.DurationMS
	}
	if a.Key != "" {
		m["key"] = a.Key
	}
	if a.App != "" {
		m["app"] = a.App
	}
	if c.Intent != "" {
		m["intent"] = c.Intent
	}
	if c.ApprovalID != "" {
		m["approval_id"] = c.ApprovalID
	}
	return argsJSON(m)
}

func argsJSON(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Act runs one action through the pipeline.
func (s *Session) Act(ctx context.Context, c Call, a Action) *Result {
	if res := a.check(); res != nil {
		return res
	}
	l, res := s.acquire(c.Device)
	if res != nil {
		return res
	}
	defer s.done(l)
	ctx, cancel := mergeCtx(ctx, l.ctx)
	defer cancel()
	return s.g.finish(l, s.g.act(ctx, l, c, a))
}

// gate refuses a call the device cannot take now. Reads (action false) pass
// while the person is in control or an approval is open.
func (g *Gateway) gate(l *lease, approvalID string, action bool) *Result {
	g.mu.Lock()
	d := l.dev
	for !l.ended && g.waitMovingLocked(l.ctx, d) {
		// It was moving between its cable and Wi-Fi, or Pinky was opening
		// it again (wifi.go).
	}
	switch {
	case l.ended:
		g.mu.Unlock()
		return statusResult(StatusStopped, "The lease ended (%s). End the task, or call phone_connect again.", l.endedBy)
	case waitingLocked(d):
		res := setupResultLocked(d, fmt.Sprintf("%s is connected but waits for a step before Pinky can use it. Nothing was done.", d.name()))
		g.mu.Unlock()
		return res
	case !d.online:
		left := g.opt.ReconnectGrace - time.Since(d.offlineSince)
		g.mu.Unlock()
		if left < 0 {
			left = 0
		}
		return errResult(ErrDeviceOffline, "%s is offline. The lease is kept for another %s in case it reconnects.", d.name(), approval.Human(left))
	}
	if !action {
		g.mu.Unlock()
		return nil
	}
	if d.person {
		first := !l.tookOverSeen
		l.tookOverSeen = true
		g.mu.Unlock()
		if first {
			return g.withCurrentScreen(l, statusResult(StatusTookOver,
				"The person took over the phone. Nothing was done. Call phone_handoff to wait for control and their note."))
		}
		return g.withCurrentScreen(l, statusResult(StatusPaused,
			"The person is in control and the agent is paused. Call phone_handoff to wait for control."))
	}
	if busy, ok := g.approvals.Busy(d.id); ok && busy.ID != approvalID {
		g.mu.Unlock()
		tool := strings.TrimPrefix(busy.Binding.Tool, "phone_")
		return &Result{Status: StatusPending, ApprovalID: busy.ID, Rule: busy.Rule,
			Message: fmt.Sprintf("An approval is open on this phone (%s: %s), so it takes no other action. Nothing was done. Repeat that %s call with approval_id %q, or use phone_observe meanwhile.",
				busy.Rule, busy.Title, tool, busy.ID)}
	}
	if d.locked && l.bg == nil {
		g.mu.Unlock()
		return g.withCurrentScreen(l, statusResult(StatusLocked,
			"The phone is locked. Nothing was done. Call phone_handoff so the person can unlock it."))
	}
	l.handedBack, l.handBackNote = false, ""
	g.mu.Unlock()
	if g.scopeOf(l.session).Mode == ScopeWatchOnly {
		return errResult(ErrScopeDenied, "This agent may only watch: the person gave it watch-only access.")
	}
	return nil
}

// withCurrentScreen adds the current screen to a result.
func (g *Gateway) withCurrentScreen(l *lease, res *Result) *Result {
	ctx, cancel := context.WithTimeout(l.ctx, 10*time.Second)
	defer cancel()
	cur, err := g.observe(ctx, l.dev, true)
	if err != nil {
		return res
	}
	return g.attachScreen(l, res, cur)
}

func (g *Gateway) attachScreen(l *lease, res *Result, cur *screen.Screen) *Result {
	sr := g.screenResult(l, cur, false)
	res.Screen, res.Diff, res.Full = sr.Screen, sr.Diff, sr.Full
	if res.Rule == "" && sr.Rule != "" && res.Status != StatusBlocked {
		res.Message = strings.TrimSpace(res.Message + " " + sr.Message)
	}
	return res
}

// prep is an action resolved against the current screen.
type prep struct {
	target *screen.Element
	point  screen.Point
	hasPt  bool
	from   screen.Point
	to     screen.Point
	dur    time.Duration
	launch driver.App
	url    bool
	input  policy.Input
	desc   string // plain-language description for history
	// decider is the element whose verdict decided, which may enclose the
	// target (a Pay button around a price label).
	decider *screen.Element
	// unplaced is set when the ref is on the screen without a position.
	unplaced bool
	// exempted lists the rules the person always allows in the app that
	// let the action run without asking (policy.Verdict.Exempted); its
	// step says so (exemptedLine).
	exempted []string
	// ocr lists the text that only OCR reads under or next to the finger
	// (ocrForVerdict). It counts in the verdict like the elements under
	// the finger but is never the target, which an approval checks again
	// on a read without OCR.
	ocr []screen.Element
	// unblock is set when the approval for this launch asks the person to
	// allow a blocked app for the task (unblock.go).
	unblock bool
}

// act is the pipeline for one action on a lease that is already acquired.
func (g *Gateway) act(ctx context.Context, l *lease, c Call, a Action) *Result {
	if res := g.gate(l, c.ApprovalID, true); res != nil {
		return res
	}
	if c.ApprovalID != "" {
		return g.resume(ctx, l, c, a)
	}
	g.readyScreen(ctx, l)
	d := l.dev
	d.act.Lock()
	// Check again under the device's action lock: another call may have
	// opened an approval, or the person may have taken over, meanwhile.
	if res := g.gate(l, "", true); res != nil {
		d.act.Unlock()
		return res
	}
	if a.Tool == "launch" {
		if res := g.launchUnread(ctx, l, c, a); res != nil {
			d.act.Unlock()
			return res
		}
	}
	cur, err := g.startScreen(ctx, l, c)
	if err != nil {
		d.act.Unlock()
		return g.readError(l, err)
	}
	if cur.Locked {
		d.act.Unlock()
		return g.attachScreen(l, statusResult(StatusLocked,
			"The phone is locked. Nothing was done. Call phone_handoff so the person can unlock it."), cur)
	}
	p, res := g.prepare(ctx, l, a, cur)
	if res != nil && p.unplaced {
		// The element is there but has no position in this read, as happens
		// with OCR-placed elements (iOS): one more read usually places it.
		if again, err := g.observe(ctx, d, false); err == nil && !again.Locked {
			cur = again
			p, res = g.prepare(ctx, l, a, cur)
		}
	}
	if res != nil {
		d.act.Unlock()
		g.recordRefusal(l, c, a, p, res)
		return res
	}
	g.ocrForVerdict(ctx, d, p, cur)
	v := g.evaluate(p, cur)
	v, _ = g.unblockAsk(l, p, v)
	switch v.Decision {
	case policy.Deny:
		d.act.Unlock()
		res := refusal(v)
		g.recordRefusal(l, c, a, p, res)
		return res
	case policy.Ask:
		if !g.granted(l, v, p) {
			info, res := g.ask(ctx, l, c, a, p, v, cur)
			d.act.Unlock()
			if res != nil {
				return res
			}
			return g.awaitApproval(ctx, l, c, a, info)
		}
	}
	p.exempted = v.Exempted
	res = g.execute(ctx, l, c, a, p, cur, -1)
	d.act.Unlock()
	return exemptedNote(res, v, p)
}

// startScreen reads the screen an action starts from. A phone_batch step
// may start from the read that ended the step before, if that settled less
// than reuseAge ago: the steps run back to back, and a read costs seconds
// on some phones.
func (g *Gateway) startScreen(ctx context.Context, l *lease, c Call) (*screen.Screen, error) {
	if c.reuse {
		g.mu.Lock()
		after, at := l.after, l.afterAt
		g.mu.Unlock()
		if after != nil && time.Since(at) < reuseAge {
			return after, nil
		}
	}
	return g.observe(ctx, l.dev, false)
}

// launchUnread runs phone_launch without reading the screen first, when it
// can: whether an app may be opened depends on the app and the task, not
// on the screen, and a driver that reports locks keeps d.locked current
// (gate checked it). It returns nil when the launch takes the usual path:
// the driver reports locks only through reads, a step's picture waits for
// the read to confirm it (see shots.go), or the policy asks and the person
// has not allowed it for the task.
func (g *Gateway) launchUnread(ctx context.Context, l *lease, c Call, a Action) *Result {
	g.mu.Lock()
	_, reports := l.dev.dev.(driver.LockReporter)
	if l.bg != nil {
		// A background display is never locked, but whether an app runs
		// on it shows only after the launch: read first.
		reports = false
	}
	waits := g.stepWaitsLocked(l.dev)
	g.mu.Unlock()
	if !reports || waits {
		return nil
	}
	p, res := g.prepare(ctx, l, a, nil)
	if res != nil {
		g.recordRefusal(l, c, a, p, res)
		return res
	}
	v, ask := g.unblockAsk(l, p, g.evaluate(p, nil))
	if ask {
		return nil // the person may allow the blocked app: ask with the screen read
	}
	switch {
	case v.Decision == policy.Deny:
		res := refusal(v)
		g.recordRefusal(l, c, a, p, res)
		return res
	case v.Decision == policy.Ask && !g.granted(l, v, p):
		return nil
	}
	p.exempted = v.Exempted
	return exemptedNote(g.execute(ctx, l, c, a, p, nil, -1), v, p)
}

// evaluate runs the policy on the action. A tap or swipe is checked
// against every element under the finger, not only the smallest one, so a
// price label inside a Pay button still counts as tapping Pay, and against
// the text only OCR reads there (p.ocr), so "Pay" on a web page without
// accessibility elements counts too. The strictest verdict wins;
// p.decider records which element decided.
func (g *Gateway) evaluate(p *prep, cur *screen.Screen) policy.Verdict {
	v := g.policy.Evaluate(p.input)
	p.decider = p.target
	pt, ok := p.finger()
	if !ok {
		return v
	}
	under := make([]screen.Element, 0, len(p.ocr)+8)
	for _, e := range cur.Elements {
		if e.Source == screen.SourceOCR || e.Bounds.Empty() || !e.Bounds.Contains(pt) {
			continue
		}
		if p.target != nil && e.Ref == p.target.Ref && e.Ref != 0 {
			continue
		}
		under = append(under, e)
	}
	under = append(under, p.ocr...)
	for i := range under {
		e := under[i]
		in := p.input
		in.Target = &e
		switch w := g.policy.Evaluate(in); {
		case w.Decision > v.Decision:
			v = w
			p.decider = &e
		case w.Decision == policy.Allow && v.Decision == policy.Allow && len(v.Exempted) == 0 && len(w.Exempted) > 0:
			// The Pay button around the price label: what lets the tap
			// through is the person's Always allow for it.
			v.Exempted = w.Exempted
		}
	}
	return v
}

// finger returns where a tap or swipe first touches the screen.
func (p *prep) finger() (screen.Point, bool) {
	switch p.input.Op {
	case policy.OpTap:
		return p.point, p.hasPt
	case policy.OpSwipe:
		return p.from, true
	}
	return screen.Point{}, false
}

// ocrForVerdict collects into p.ocr the text that only OCR reads under or
// next to the finger of a tap or swipe: on web views and canvas apps
// "还款" or "Pay" may be nowhere in the element tree. The read an action
// starts from has no OCR (it would slow every action), so on phones whose
// driver reads text (readsText) it reads again with all the OCR text, but
// only where the tree says little about the point: no element there, an
// unlabelled one, or a large one such as a web view. Without a second read
// the action is checked as before.
func (g *Gateway) ocrForVerdict(ctx context.Context, d *device, p *prep, cur *screen.Screen) {
	pt, ok := p.finger()
	if !ok || cur == nil {
		return
	}
	src := cur
	if !hasOCR(cur) {
		if !vagueAt(cur, p.target) || !g.readsText(d) {
			return
		}
		o, err := g.observeOCR(ctx, d, ocrAll)
		if err != nil {
			return
		}
		src = o
	}
	for _, e := range src.Elements {
		if e.Source == screen.SourceOCR && e.Label != "" && !ocrInStatusBar(src, e) && ocrReach(e.Bounds).Contains(pt) {
			p.ocr = append(p.ocr, e)
		}
	}
}

// vagueAt reports whether the tree element under the finger leaves open
// what the finger is on: there is none, it has no label, or it covers a
// sixth of the screen or more (a web view, a canvas, a backdrop).
func vagueAt(s *screen.Screen, target *screen.Element) bool {
	if target == nil || strings.TrimSpace(target.Label) == "" {
		return true
	}
	return s.Width > 0 && s.Height > 0 && target.Bounds.W*target.Bounds.H*6 >= s.Width*s.Height
}

// ocrReach is the area around an OCR line where a tap counts as a tap on
// it: OCR boxes hug the letters, while the button around them reaches
// about a line's height further on every side.
func ocrReach(r screen.Rect) screen.Rect {
	m := r.H
	return screen.Rect{X: r.X - m, Y: r.Y - m, W: r.W + 2*m, H: r.H + 2*m}
}

func hasOCR(s *screen.Screen) bool {
	if s == nil {
		return false
	}
	for _, e := range s.Elements {
		if e.Source == screen.SourceOCR {
			return true
		}
	}
	return false
}

func refusal(v policy.Verdict) *Result {
	if v.Scope {
		return &Result{Error: ErrScopeDenied, Rule: v.Rule, Message: v.Reason + " Nothing was done."}
	}
	msg := v.Reason
	if msg == "" {
		msg = fmt.Sprintf("The rule %q refused this action.", v.Rule)
	}
	return &Result{Status: StatusBlocked, Rule: v.Rule, Message: msg + " Nothing was done. Choose another way, or ask the person."}
}

// granted reports whether the person allowed, for the rest of the task,
// every rule that asked about this action, in this app.
func (g *Gateway) granted(l *lease, v policy.Verdict, p *prep) bool {
	if len(v.Asks) == 0 {
		return false
	}
	app := grantApp(p)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range v.Asks {
		if !l.grants[grantKey(r, app)] {
			return false
		}
	}
	return true
}

// grantApp is the app a task grant applies to: the app being opened for
// phone_launch, else the foreground app.
func grantApp(p *prep) screen.App {
	if p.input.Op == policy.OpLaunch {
		return p.input.Launch
	}
	return p.input.App
}

// prepare resolves refs, points and apps against the current screen and
// builds the policy input. A non-nil result refuses the action. cur may be
// nil for phone_launch only, which does not depend on the screen.
func (g *Gateway) prepare(ctx context.Context, l *lease, a Action, cur *screen.Screen) (*prep, *Result) {
	d := l.dev
	p := &prep{}
	scope := g.scopeOf(l.session)
	var fg screen.App
	if cur != nil {
		fg = cur.App
	}
	g.mu.Lock()
	apps := append([]string(nil), l.apps...)
	seen := l.baseline
	readOnly := l.readOnly
	fgAllowed := allowsLocked(l, fg)
	g.mu.Unlock()
	p.input = policy.Input{Device: d.id, App: fg, Screen: cur, TaskApps: apps, LongPress: a.LongPress,
		ReadOnly: scope.Mode == ScopeReadOnly || readOnly, AllowBlocked: fgAllowed}
	if scope.Mode == ScopeApps {
		p.input.ScopeApps = scope.Apps
	}

	if rule, blocked := g.policy.Blocked(fg); blocked && !fgAllowed && a.Tool != "launch" && !(a.Tool == "key" && a.Key == "home") {
		p.desc = describe(a, p)
		res := &Result{Status: StatusBlocked, Rule: rule,
			Message: fmt.Sprintf("The foreground app is blocked by the rule %q, so Pinky hides it and takes no action there. Nothing was done. Press home or launch another app.", rule)}
		return p, g.attachScreen(l, res, cur)
	}

	byRef := func(ref int) (*screen.Element, *Result) {
		e, ok := cur.Find(ref)
		if !ok {
			e, ok = relocate(seen, cur, ref)
		}
		if !ok {
			return nil, g.attachScreen(l, errResult(ErrNotFound, "[%d] is not on the screen anymore. Nothing was done.", ref), cur)
		}
		if e.Bounds.Empty() {
			p.unplaced = true
			return nil, errResult(ErrNotFound, "[%d] has no position on the screen; tap by x and y instead.", ref)
		}
		return &e, nil
	}
	inBounds := func(pt screen.Point) bool {
		return cur.Width <= 0 || cur.Height <= 0 || (pt.X >= 0 && pt.Y >= 0 && pt.X < cur.Width && pt.Y < cur.Height)
	}

	switch a.Tool {
	case "tap":
		p.input.Op = policy.OpTap
		if a.Ref > 0 {
			e, res := byRef(a.Ref)
			if res != nil {
				return p, res
			}
			p.target, p.point = e, tapPoint(cur, *e)
		} else {
			if !inBounds(*a.Point) {
				return p, errResult(ErrInvalidArgument, "%d,%d is outside the %dx%d screen.", a.Point.X, a.Point.Y, cur.Width, cur.Height)
			}
			p.point = *a.Point
			p.target = hitTest(cur, p.point)
		}
		p.hasPt = true
	case "type":
		p.input.Op = policy.OpType
		p.input.Text, p.input.Submit = a.Text, a.Submit
		if a.Ref > 0 {
			e, res := byRef(a.Ref)
			if res != nil {
				return p, res
			}
			if e.Role != screen.RoleInput && !e.Has(screen.StateEditable) && !e.Has(screen.StateFocused) {
				return p, errResult(ErrInvalidArgument, "[%d] is a %s, not a text field. Nothing was done.", a.Ref, e.Role)
			}
			p.target, p.point, p.hasPt = e, e.Bounds.Center(), true
		} else if f, ok := cur.Focused(); ok {
			p.target = &f
		} else if credentialOnScreen(cur) {
			// The focused field is unknown and might be the password field.
			return p, &Result{Status: StatusBlocked, Rule: policy.RuleCredentials,
				Message: "This screen has a password or code field and Pinky cannot tell which field has focus. Nothing was done. Pass ref to type into a specific field, or call phone_handoff for credentials."}
		}
	case "swipe":
		p.input.Op = policy.OpSwipe
		p.dur = 300 * time.Millisecond
		if a.DurationMS > 0 {
			p.dur = time.Duration(a.DurationMS) * time.Millisecond
		}
		if a.From != nil {
			if !inBounds(*a.From) || !inBounds(*a.To) {
				return p, errResult(ErrInvalidArgument, "from and to must lie inside the %dx%d screen.", cur.Width, cur.Height)
			}
			p.from, p.to = *a.From, *a.To
			p.target = hitTest(cur, p.from)
		} else {
			area := screen.Rect{X: 0, Y: 0, W: cur.Width, H: cur.Height}
			if a.Ref > 0 {
				e, res := byRef(a.Ref)
				if res != nil {
					return p, res
				}
				p.target, area = e, e.Bounds
			}
			p.from, p.to = swipeLine(area, a.Direction)
		}
	case "key":
		p.input.Op = policy.OpKey
		p.input.Key = a.Key
		if f, ok := cur.Focused(); ok {
			p.target = &f
		}
	case "launch":
		p.input.Op = policy.OpLaunch
		app, url, res := g.resolveLaunch(ctx, d, a.App)
		if res != nil {
			return p, res
		}
		p.launch, p.url = app, url
		// An app clone is judged as the app it copies: the two read
		// alike on the screen.
		orig := app.Original()
		p.input.Launch = screen.App{ID: orig.ID, Name: orig.Name}
		opener := false
		if lo, ok := g.driverOf(d).(linkOpener); ok && url {
			// The rules judge the app that opens the link: blocking or
			// scoping it covers its links too, and so does the person's
			// allowing it for the task.
			browser := lo.LinkApp()
			p.input.Launch = screen.App{ID: browser.ID, Name: browser.Name}
			opener = true
		}
		p.input.LaunchURL = url
		p.input.AllowBlocked = (!url || opener) && g.allows(l, p.input.Launch)
	}
	p.input.Target = p.target
	p.desc = describe(a, p)
	return p, nil
}

func credentialOnScreen(s *screen.Screen) bool {
	for i := range s.Elements {
		e := &s.Elements[i]
		if (e.Role == screen.RoleInput || e.Has(screen.StateEditable)) && policy.IsCredentialField(e) {
			return true
		}
	}
	return false
}

// swipeLine returns a swipe across the middle of area, where direction is
// where the finger moves: "up" scrolls content down.
func swipeLine(area screen.Rect, dir string) (from, to screen.Point) {
	c := area.Center()
	dy, dx := area.H*3/10, area.W*3/10
	switch dir {
	case "up":
		return screen.Point{X: c.X, Y: c.Y + dy}, screen.Point{X: c.X, Y: c.Y - dy}
	case "down":
		return screen.Point{X: c.X, Y: c.Y - dy}, screen.Point{X: c.X, Y: c.Y + dy}
	case "left":
		return screen.Point{X: c.X + dx, Y: c.Y}, screen.Point{X: c.X - dx, Y: c.Y}
	}
	return screen.Point{X: c.X - dx, Y: c.Y}, screen.Point{X: c.X + dx, Y: c.Y}
}

// tapPoint is where a tap on the element t by ref lands: its centre, unless
// t is a control and something drawn on it lies there that would take the
// tap instead: another control, or anything at all when t covers half the
// screen or more. iOS puts a full-screen "Dismiss context menu" button
// behind a context menu, and a tap at its centre hit the menu item lying
// there (field test #29). Then the tap lands on the point of t farthest
// from the elements on it, and from its edges and the status bar, when one
// is clear of them.
//
// Trees list what is drawn later, on top, later, so the elements listed
// after t are drawn on it and those before it lie under it. A backdrop's
// place in its tree is the app's reading order, though, so for an element
// that large every other element counts at first, and only those listed
// after it when that leaves no point clear.
func tapPoint(s *screen.Screen, t screen.Element) screen.Point {
	c := t.Bounds.Center()
	if !actionable(t) || t.Ref == 0 {
		return c
	}
	big := s.Width > 0 && s.Height > 0 && t.Bounds.W*t.Bounds.H*2 >= s.Width*s.Height
	var all, above []screen.Rect
	covered, after := false, false
	for _, e := range s.Elements {
		if e.Ref == t.Ref {
			after = true
			continue
		}
		if e.Bounds.Empty() || encloses(e.Bounds, t.Bounds) || !overlaps(e.Bounds, t.Bounds) || (!after && !big) {
			continue // what holds t, what lies elsewhere, and what lies under a control of usual size
		}
		all = append(all, e.Bounds)
		if after {
			above = append(above, e.Bounds)
		}
		if e.Bounds.Contains(c) && (big || (actionable(e) && e.Label != t.Label)) {
			covered = true
		}
	}
	if !covered {
		return c
	}
	if p, ok := clearPoint(s, t.Bounds, all); ok {
		return p
	}
	if len(above) < len(all) {
		if p, ok := clearPoint(s, t.Bounds, above); ok {
			return p
		}
	}
	return c // nowhere clear: tap where it always did
}

// clearPoint returns the point of area farthest from the rectangles on it,
// from its edges and from the status bar, if that is clear of them all by
// a margin.
func clearPoint(s *screen.Screen, area screen.Rect, on []screen.Rect) (screen.Point, bool) {
	c := area.Center()
	if top := s.Height * 6 / 100; area.Y < top && area.Y+area.H > 2*top {
		area.H -= top - area.Y
		area.Y = top
	}
	step := max(4, min(area.W, area.H)/32)
	best, clear := c, -1
	for y := area.Y + step/2; y < area.Y+area.H; y += step {
		for x := area.X + step/2; x < area.X+area.W; x += step {
			pt := screen.Point{X: x, Y: y}
			d := min(x-area.X, area.X+area.W-1-x, y-area.Y, area.Y+area.H-1-y)
			for _, r := range on {
				if d = min(d, distance(pt, r)); d < clear {
					break // worse than the best so far
				}
			}
			if d > clear || (d == clear && closer(pt, best, c)) {
				best, clear = pt, d
			}
		}
	}
	return best, clear >= max(4, min(s.Width, s.Height)/60)
}

// actionable reports whether an agent could mean to act on e.
func actionable(e screen.Element) bool {
	switch e.Role {
	case screen.RoleButton, screen.RoleLink, screen.RoleInput, screen.RoleSwitch, screen.RoleCheckbox, screen.RoleTab:
		return true
	}
	return e.Has(screen.StateEditable)
}

// encloses reports whether a holds all of b.
func encloses(a, b screen.Rect) bool {
	return a.X <= b.X && a.Y <= b.Y && a.X+a.W >= b.X+b.W && a.Y+a.H >= b.Y+b.H
}

func overlaps(a, b screen.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// distance is how far p lies from r along the farther axis, 0 inside it.
func distance(p screen.Point, r screen.Rect) int {
	dx := max(r.X-p.X, 0, p.X-(r.X+r.W-1))
	dy := max(r.Y-p.Y, 0, p.Y-(r.Y+r.H-1))
	return max(dx, dy)
}

// closer reports whether a is nearer to c than b.
func closer(a, b, c screen.Point) bool {
	da := (a.X-c.X)*(a.X-c.X) + (a.Y-c.Y)*(a.Y-c.Y)
	db := (b.X-c.X)*(b.X-c.X) + (b.Y-c.Y)*(b.Y-c.Y)
	return da < db
}

// hitTest returns the smallest tree element under p. OCR text is never the
// target, since an approval checks its target again on a read without OCR;
// the verdict weighs the OCR text under the finger as well (evaluate).
func hitTest(s *screen.Screen, p screen.Point) *screen.Element {
	var best *screen.Element
	for i := range s.Elements {
		e := &s.Elements[i]
		if e.Source == screen.SourceOCR || e.Bounds.Empty() || !e.Bounds.Contains(p) {
			continue
		}
		if best == nil || e.Bounds.W*e.Bounds.H < best.Bounds.W*best.Bounds.H {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	cp := *best
	return &cp
}

// linkOpener is implemented by drivers that open a link in a fixed app.
type linkOpener interface {
	LinkApp() driver.App
}

// resolveLaunch finds the app phone_launch names.
func (g *Gateway) resolveLaunch(ctx context.Context, d *device, name string) (driver.App, bool, *Result) {
	name = strings.TrimSpace(name)
	if strings.Contains(name, "://") {
		return driver.App{ID: name, Name: name}, true, nil
	}
	dev := g.driverOf(d)
	if dev == nil {
		return driver.App{}, false, errResult(ErrDeviceOffline, "%s is offline.", d.name())
	}
	apps, err := dev.Apps(ctx)
	if errors.Is(err, driver.ErrUnsupported) {
		return driver.App{ID: name, Name: name}, false, nil
	}
	if err != nil {
		return driver.App{}, false, errResult(ErrInternal, "Listing apps failed: %v", err)
	}
	m := findApp(apps, name)
	if m.ok {
		return m.app, false, nil
	}
	if len(m.similar) > 0 {
		return driver.App{}, false, errResult(ErrNotFound, "No installed app is named %q. Similar: %s. Call phone_launch again with the exact name or id of the one you mean.",
			name, appList(m.similar))
	}
	return driver.App{}, false, errResult(ErrNotFound, "No installed app matches %q; call phone_apps to see what is installed.", name)
}

// ask opens an approval for an action and records the waiting step.
func (g *Gateway) ask(ctx context.Context, l *lease, c Call, a Action, p *prep, v policy.Verdict, cur *screen.Screen) (approval.Info, *Result) {
	shot := g.rawShot(ctx, l.dev, cur)
	target := screen.Rect{}
	switch {
	case p.decider != nil:
		target = p.decider.Bounds
	case p.target != nil:
		target = p.target.Bounds
	case p.hasPt:
		target = screen.Rect{X: p.point.X - 24, Y: p.point.Y - 24, W: 48, H: 48}
	}
	var point, to screen.Point
	switch {
	case a.Tool == "swipe":
		point, to = p.from, p.to
	case p.hasPt:
		point = p.point
	}
	detail := l.session.agent.Name
	if c.Intent != "" {
		detail += " · " + c.Intent
	} else {
		detail += " · " + l.task.title
	}
	app := appLabel(cur.App)
	if p.unblock {
		app = appLabel(p.input.Launch) // the card is about the app to open
	}
	info := g.approvals.Open(approval.Request{
		Kind: approval.KindAction, Binding: g.binding(l, a), Rule: v.Rule, Title: approvalTitle(a, p, v),
		Detail: detail, App: app, Agent: l.session.agent.Name, TaskID: l.task.id,
		DeviceName: l.dev.name(), Screenshot: shot, Target: target, Point: point, To: to, Data: takeSnapshot(cur, p, v),
	})
	if res := g.afterOpen(l, info); res != nil {
		return info, res
	}
	before := g.shotID(g.keepImage(l.dev, cur, shot, false))
	g.openStep(l, info, "phone_"+a.Tool, a.args(c), "Waiting for you: "+p.desc, target, p.point, before)
	return info, nil
}

// afterOpen checks a just-opened approval against what happened while it
// was being prepared: a Stop or a takeover in that moment could not
// withdraw it yet, so it is withdrawn here.
func (g *Gateway) afterOpen(l *lease, info approval.Info) *Result {
	g.mu.Lock()
	ended, by := l.ended, l.endedBy
	person := l.dev.person && info.Kind != approval.KindHandoff
	if person {
		l.tookOverSeen = true
	}
	g.mu.Unlock()
	switch {
	case ended:
		g.approvals.Withdraw(info.ID, by)
		return statusResult(StatusStopped, "The lease ended (%s). Nothing was done.", by)
	case person:
		g.approvals.Withdraw(info.ID, reasonTookOver)
		return g.withCurrentScreen(l, statusResult(StatusTookOver,
			"The person took over the phone. Nothing was done. Call phone_handoff to wait for control and their note."))
	}
	return nil
}

// errTaken means another call with the same approval id received the
// decision.
var errTaken = errors.New("approval taken by another call")

func takenResult(id string) *Result {
	return errResult(ErrInvalidArgument, "Another call with approval_id %s received the decision; this one did nothing.", id)
}

func (g *Gateway) binding(l *lease, a Action) approval.Binding {
	return approval.Binding{Session: l.session.id, Device: l.dev.id, Lease: l.id, Tool: "phone_" + a.Tool, Args: a.digest()}
}

func approvalTitle(a Action, p *prep, v policy.Verdict) string {
	label := ""
	switch {
	case p.decider != nil:
		label = p.decider.Label
	case p.target != nil:
		label = p.target.Label
	}
	switch {
	case v.Has(policy.Payment) && label != "":
		return fmt.Sprintf("Pay: “%s”?", label)
	case v.Has(policy.SendMessage):
		if a.Text != "" {
			return fmt.Sprintf("Send “%s”?", clip(a.Text, 80))
		}
		return "Send the message?"
	case a.Tool == "launch":
		return fmt.Sprintf("Open %s?", p.launch.Name)
	case v.Has(policy.ChangeSetting) && label != "":
		return fmt.Sprintf("Change “%s”?", label)
	case label != "":
		return fmt.Sprintf("%s “%s”?", capitalize(verbFor(a)), label)
	}
	return capitalize(p.desc) + "?"
}

func verbFor(a Action) string {
	switch a.Tool {
	case "tap":
		if a.LongPress {
			return "long-press"
		}
		return "tap"
	case "type":
		return "type into"
	case "swipe":
		return "swipe"
	case "key":
		return "press " + a.Key + " in"
	}
	return a.Tool
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func appLabel(a screen.App) string {
	if a.Name != "" {
		return a.Name
	}
	return a.ID
}

func (g *Gateway) appName(d *device) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d.last != nil {
		if _, blocked := g.policy.Blocked(d.last.App); !blocked {
			return appLabel(d.last.App)
		}
	}
	return ""
}

// awaitApproval waits for the person's answer and runs the action if they
// allow it.
func (g *Gateway) awaitApproval(ctx context.Context, l *lease, c Call, a Action, info approval.Info) *Result {
	out, err := g.waitApproval(ctx, l, info.ID, info, c.Progress)
	if errors.Is(err, errTaken) {
		return takenResult(info.ID)
	}
	if err != nil {
		return g.cancelled(l, info.ID)
	}
	switch out.State {
	case approval.Open:
		return g.pendingResult(l, info)
	case approval.Withdrawn:
		g.closeStep(info.ID, string(StatusDenied), 0)
		return g.withdrawnResult(l, out)
	}
	g.recordDecision(info, out)
	snap, _ := info.Data.(*snapshot)
	unblock := snap != nil && snap.unblock
	switch {
	case out.Decision.Allows() && unblock:
		// Any Allow allows the app for the rest of the task: opening it
		// once would leave the agent nothing it may read.
		g.mu.Lock()
		l.unblocked = append(l.unblocked, snap.grantApp)
		g.mu.Unlock()
		return g.runApproved(ctx, l, c, a, info, out)
	case out.Decision.Allows():
		if snap != nil && out.Decision != approval.AllowOnce {
			g.mu.Lock()
			for _, r := range snap.asks {
				l.grants[grantKey(r, snap.grantApp)] = true
			}
			g.mu.Unlock()
		}
		return g.runApproved(ctx, l, c, a, info, out)
	case out.Decision == approval.TakeOver:
		g.closeStep(info.ID, string(StatusTookOver), out.Waited)
		return g.withCurrentScreen(l, statusResult(StatusTookOver, "The person took over instead. Nothing was done. Call phone_handoff to wait for control."))
	}
	g.closeStep(info.ID, string(StatusDenied), out.Waited)
	res := &Result{Status: StatusDenied, Rule: info.Rule, Note: denyNote(out), Message: "The person denied this action. Nothing was done."}
	switch {
	case out.TimedOut:
		res.Message = "Nobody answered in time, which counts as Deny. Nothing was done."
	case unblock:
		// Asked once per task: the next launch of it is refused at once.
		g.mu.Lock()
		l.kept = append(l.kept, snap.grantApp)
		g.mu.Unlock()
		res.Message = fmt.Sprintf("The person kept %s blocked for this task. Nothing was done.", appLabel(snap.grantApp))
	}
	return res
}

// waitApproval blocks for up to MaxBlock, sending progress every 5 s.
func (g *Gateway) waitApproval(ctx context.Context, l *lease, id string, info approval.Info, progress Progress) (approval.Outcome, error) {
	start := time.Now()
	deadline := start.Add(g.opt.MaxBlock)
	total := g.opt.MaxBlock.Seconds()
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return approval.Outcome{State: approval.Open}, nil
		}
		if progress != nil {
			answer := time.Until(info.Deadline)
			if answer < 0 {
				answer = 0
			}
			progress(time.Since(start).Seconds(), total, fmt.Sprintf("Waiting for the person: %s (%s left to answer)", info.Title, approval.Human(answer)))
		}
		slice := 5 * time.Second
		if left < slice {
			slice = left
		}
		out, err := g.approvals.Wait(ctx, id, slice)
		if err != nil {
			if l != nil {
				g.mu.Lock()
				ended, by := l.ended, l.endedBy
				g.mu.Unlock()
				if ended {
					g.approvals.Withdraw(id, by)
					return approval.Outcome{State: approval.Withdrawn, Reason: by}, nil
				}
			}
			if errors.Is(err, approval.ErrUsed) {
				return out, errTaken
			}
			if errors.Is(err, approval.ErrExpired) || errors.Is(err, approval.ErrUnknown) {
				return approval.Outcome{State: approval.Withdrawn, Reason: "expired"}, nil
			}
			return out, err
		}
		if out.State != approval.Open {
			return out, nil
		}
	}
}

func (g *Gateway) pendingResult(l *lease, info approval.Info) *Result {
	g.setTaskState(l, TaskWaiting)
	left := time.Until(info.Deadline)
	what := "approve"
	if info.Kind == approval.KindHandoff {
		what = "hand control back"
	}
	return &Result{Status: StatusPending, ApprovalID: info.ID, Rule: info.Rule,
		Message: fmt.Sprintf("Still waiting for the person to %s (%s). Nothing was done yet. Repeat the same call with approval_id %q; the person has %s left.",
			what, info.Title, info.ID, approval.Human(left))}
}

func (g *Gateway) withdrawnResult(l *lease, out approval.Outcome) *Result {
	if out.Reason == OutcomeStopped {
		return statusResult(StatusStopped, "The person pressed Stop; the lease is gone. End the task.")
	}
	if out.Reason == reasonTookOver {
		g.mu.Lock()
		l.tookOverSeen = true
		g.mu.Unlock()
		return g.withCurrentScreen(l, statusResult(StatusTookOver, "The person took over the phone. Nothing was done. Call phone_handoff to wait for control and their note."))
	}
	return &Result{Status: StatusDenied, Note: denyNote(out), Message: "The approval was withdrawn (" + out.Reason + "). Nothing was done."}
}

func (g *Gateway) cancelled(l *lease, id string) *Result {
	g.mu.Lock()
	ended := l.ended
	g.mu.Unlock()
	if ended {
		return statusResult(StatusStopped, "The lease ended.")
	}
	// An MCP cancellation withdraws the approval panel without acting.
	g.approvals.Withdraw(id, "the agent cancelled the call")
	g.closeStep(id, "cancelled", 0)
	g.setTaskState(l, TaskWorking)
	return errResult(ErrInternal, "The call was cancelled; the approval was withdrawn.")
}

// checkApproval validates an approval_id. It returns nil when the id may be
// waited on, an error result when the call is wrong, or an empty result
// when the call should ask again.
func (g *Gateway) checkApproval(id string, b approval.Binding) *Result {
	err := g.approvals.Check(id, b)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, approval.ErrMismatch):
		return errResult(ErrInvalidArgument, "approval_id %s belongs to a different call (another device, tool or arguments). Nothing was done. Repeat the original call exactly, or call without approval_id to ask again.", id)
	case errors.Is(err, approval.ErrExpired):
		return &Result{} // ask again
	case errors.Is(err, approval.ErrUsed):
		return errResult(ErrInvalidArgument, "approval_id %s was already used. Nothing was done. Observe the screen, then call again without approval_id.", id)
	}
	return errResult(ErrInvalidArgument, "Unknown approval_id %q. Call again without approval_id.", id)
}

// resume handles a call that carries an approval_id.
func (g *Gateway) resume(ctx context.Context, l *lease, c Call, a Action) *Result {
	if res := g.checkApproval(c.ApprovalID, g.binding(l, a)); res != nil {
		if res.Error != "" {
			return res
		}
		c.ApprovalID = ""
		return g.act(ctx, l, c, a)
	}
	info, err := g.approvals.Get(c.ApprovalID)
	if err != nil {
		return errResult(ErrInvalidArgument, "Unknown approval_id %q.", c.ApprovalID)
	}
	return g.awaitApproval(ctx, l, c, a, info)
}

// runApproved re-checks the screen and runs an approved action.
func (g *Gateway) runApproved(ctx context.Context, l *lease, c Call, a Action, info approval.Info, out approval.Outcome) *Result {
	d := l.dev
	g.readyScreen(ctx, l)
	d.act.Lock()
	defer d.act.Unlock()
	if res := g.gate(l, info.ID, true); res != nil {
		g.closeStep(info.ID, notRunStatus(res), out.Waited)
		return res
	}
	cur, err := g.observe(ctx, d, false)
	if err != nil {
		res := g.readError(l, err)
		g.closeStep(info.ID, notRunStatus(res), out.Waited)
		return res
	}
	if cur.Locked {
		g.closeStep(info.ID, string(StatusLocked), out.Waited)
		return g.attachScreen(l, statusResult(StatusLocked,
			"The phone locked while the approval waited, so nothing was done. Call phone_handoff so the person can unlock it."), cur)
	}
	snap, _ := info.Data.(*snapshot)
	p, res := g.prepare(ctx, l, a, cur)
	if res != nil || snap == nil || snap.changed(cur, p) {
		g.closeStep(info.ID, string(StatusStale), out.Waited)
		return g.attachScreen(l, statusResult(StatusStale,
			"The screen changed while the approval waited, so nothing was done. Observe and decide again."), cur)
	}
	v := g.evaluate(p, cur)
	if v.Decision == policy.Deny {
		res := refusal(v)
		g.closeStep(info.ID, notRunStatus(res), out.Waited)
		return res
	}
	g.mu.Lock()
	ref, ok := g.apSteps[info.ID]
	delete(g.apSteps, info.ID)
	g.mu.Unlock()
	idx := -1
	if ok {
		idx = ref.index
	}
	g.setTaskState(l, TaskWorking)
	res = g.execute(ctx, l, c, a, p, cur, idx)
	res.Note = out.Note
	msg := "The person allowed this once."
	switch {
	case snap.unblock:
		msg = unblockedMessage(snap.grantApp)
	case out.Decision == approval.AllowAlways && len(v.Exempted) > 0:
		// Gateway.Decide kept the answer (always.go), so the action no
		// longer asks.
		msg = fmt.Sprintf("The person allowed this, and from now on %q no longer asks in %s.", info.Rule, appLabel(grantApp(p)))
	case out.Decision != approval.AllowOnce:
		msg = fmt.Sprintf("The person allowed this, and %q for the rest of the task.", info.Rule)
	}
	res.Message = strings.TrimSpace(msg + " " + res.Message)
	return res
}

// notRunStatus is the step status for an approved action that did not run.
func notRunStatus(res *Result) string {
	switch {
	case res.Error != "":
		return string(res.Error)
	case res.Status == StatusPending:
		return "cancelled"
	}
	return string(res.Status)
}

// preflight checks, right before the driver acts, that nothing changed
// since the gate: the lease is still there, the person did not take over,
// no approval opened and the call was not cancelled.
func (g *Gateway) preflight(ctx context.Context, l *lease) *Result {
	if res := g.gate(l, "", true); res != nil {
		return res
	}
	if ctx.Err() != nil {
		return errResult(ErrInternal, "The call was cancelled. Nothing was done.")
	}
	return nil
}

// execute performs an allowed action, settles, reads the screen and records
// the step. It runs with d.act held. step is the index of a step opened by
// an approval, or -1. cur is the screen the action starts from, nil for a
// launch that did not read it. Screenshots never hold the result up: the
// before picture is the kept one when it shows cur, and the after picture
// is taken in the background (see shots.go).
func (g *Gateway) execute(ctx context.Context, l *lease, c Call, a Action, p *prep, cur *screen.Screen, step int) *Result {
	d := l.dev
	start := time.Now()
	before := g.shotID(g.beforeShot(d, cur))
	from := g.leftFrom(d, cur)
	if res := g.preflight(ctx, l); res != nil {
		g.recordStep(l, step, a, c, p, res, start, before, "", "")
		return res
	}
	// The person taking over cancels the driver call in progress.
	actx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	dev, onBG := g.screenOfLocked(d)
	d.inflight = cancel
	g.inputLocked(d)
	g.mu.Unlock()
	if dev == nil {
		cancel()
		res := errResult(ErrDeviceOffline, "%s is offline. Nothing was done.", d.name())
		g.recordStep(l, step, a, c, p, res, start, before, "", "")
		return res
	}
	err := g.perform(actx, dev, a, p)
	if err != nil && onBG && a.Tool == "launch" && errors.Is(err, driver.ErrUnsupported) {
		// The app will not run on a background display: the task moves
		// to the phone's own screen, which may be off or locked.
		var res *Result
		dev, res = g.leaveForLaunch(actx, l, p, err)
		if res != nil {
			cancel()
			g.mu.Lock()
			d.inflight = nil
			g.mu.Unlock()
			g.recordStep(l, step, a, c, p, res, start, before, "", "")
			return res
		}
		err = g.perform(actx, dev, a, p)
	}
	g.mu.Lock()
	d.inflight = nil
	g.mu.Unlock()
	cancel()
	if err != nil {
		res := g.actionError(l, err)
		g.recordStep(l, step, a, c, p, res, start, before, "", "")
		return res
	}
	settled := g.settle(ctx, d)
	next, err := g.observe(ctx, d, true)
	if err != nil {
		res := statusResult(StatusUncertain, "The action was sent, but the screen could not be read afterwards (%v). Observe before anything else; do not repeat it blindly.", err)
		g.recordStep(l, step, a, c, p, res, start, before, "", "")
		return res
	}
	if g.leaves(a, p, from) && stillIn(next, from.App) {
		next, settled = g.awaitLeave(ctx, d, from.App, next, settled)
	}
	res := g.screenResult(l, next, false)
	res.Status = StatusSettled
	g.mu.Lock()
	l.after, l.afterAt = nil, time.Time{}
	if settled {
		l.after, l.afterAt = next, time.Now()
	}
	g.mu.Unlock()
	if !settled {
		res.Settle = "timeout"
	}
	if next.Locked {
		res.Message = strings.TrimSpace(res.Message + " The phone is locked now.")
	}
	shot := g.startShot(d, next)
	text := body(res.Screen)
	if _, blocked := g.policy.Blocked(next.App); blocked {
		text = "" // allowed for the task: history keeps the steps only
	}
	idx := g.recordStep(l, step, a, c, p, res, start, before, "", text)
	g.useShot(shot, l.task, idx, true)
	return res
}

// leftFrom is the screen an action started from: cur, or for a launch that
// did not read it (launchUnread), the phone's last read.
func (g *Gateway) leftFrom(d *device, cur *screen.Screen) *screen.Screen {
	if cur != nil {
		return cur
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return d.last
}

// leaves reports whether an action takes the phone out of the app in front
// on cur: home in an app other than the launcher, or opening another app.
func (g *Gateway) leaves(a Action, p *prep, cur *screen.Screen) bool {
	if cur == nil || cur.Locked || (cur.App.ID == "" && cur.App.Name == "") {
		return false
	}
	switch {
	case a.Tool == "key" && a.Key == "home":
		return !g.policy.IsSystem(cur.App)
	case a.Tool == "launch" && p != nil && !p.url:
		orig := p.launch.Original() // a clone reads as its original
		to := screen.App{ID: orig.ID, Name: orig.Name}
		if to.ID == to.Name {
			to.ID = "" // a phone that cannot list its apps: only the name is known
		}
		return !sameApp(to, cur.App)
	}
	return false
}

// awaitLeave waits, up to SettleMax, for a read that shows another app in
// front than from, then settles and reads again. An iPhone names the app
// in front with a delay, and the read right after home can still show the
// app the agent left (field test #25): a blocked app that seemed to stay
// in front gave the agent "no change" and refused its next action. It
// keeps next when the app does not change.
func (g *Gateway) awaitLeave(ctx context.Context, d *device, from screen.App, next *screen.Screen, settled bool) (*screen.Screen, bool) {
	deadline := time.Now().Add(g.opt.SettleMax)
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, g.opt.PollInterval, nil) {
			break
		}
		s, err := g.observe(ctx, d, false)
		if err != nil {
			break
		}
		if stillIn(s, from) {
			continue
		}
		settled = g.settle(ctx, d)
		if o, err := g.observe(ctx, d, true); err == nil {
			return o, settled
		}
		return s, settled
	}
	return next, settled
}

// stillIn reports whether s shows app in front, unlocked.
func stillIn(s *screen.Screen, app screen.App) bool {
	return !s.Locked && sameApp(s.App, app)
}

// sameApp compares apps by id, or by name when one has no id.
func sameApp(a, b screen.App) bool {
	if a.ID != "" && b.ID != "" {
		return a.ID == b.ID
	}
	return a.Name != "" && strings.EqualFold(a.Name, b.Name)
}

func (g *Gateway) perform(ctx context.Context, dev driver.Device, a Action, p *prep) error {
	switch a.Tool {
	case "tap":
		return dev.Tap(ctx, p.point, driver.TapOptions{LongPress: a.LongPress})
	case "type":
		if p.hasPt && p.target != nil && !p.target.Has(screen.StateFocused) {
			if err := dev.Tap(ctx, p.point, driver.TapOptions{}); err != nil {
				return err
			}
			if !sleepCtx(ctx, g.opt.PollInterval, nil) {
				return ctx.Err()
			}
		}
		return dev.Type(ctx, a.Text, driver.TypeOptions{Append: a.Append, Submit: a.Submit})
	case "swipe":
		return dev.Swipe(ctx, p.from, p.to, p.dur)
	case "key":
		return dev.Key(ctx, agentKeys[a.Key])
	case "launch":
		id := p.launch.ID
		if id == "" {
			id = p.launch.Name
		}
		return dev.Launch(ctx, id)
	}
	return fmt.Errorf("unknown action %q", a.Tool)
}

// actionError maps a driver error from an input to a result. Only errors
// that prove nothing happened become errors; the rest are uncertain.
func (g *Gateway) actionError(l *lease, err error) *Result {
	switch {
	case errors.Is(err, driver.ErrUnsupported):
		return errResult(ErrUnsupported, "This phone cannot do that: %v. Nothing was done.", err)
	case errors.Is(err, driver.ErrNotFound):
		return errResult(ErrNotFound, "Not found: %v. Nothing was done.", err)
	case errors.Is(err, driver.ErrLocked):
		g.setLocked(l.dev, true)
		return statusResult(StatusLocked, "The phone is locked. Call phone_handoff so the person can unlock it.")
	case errors.Is(err, driver.ErrOffline):
		g.setOffline(l.dev, "")
		return statusResult(StatusUncertain, "The phone dropped during the action, which may or may not have happened. Observe when it is back before anything else; do not repeat it blindly.")
	}
	return statusResult(StatusUncertain, "The action may or may not have happened (%v). Observe before anything else; do not repeat it blindly.", err)
}

// readError maps an error from reading the screen.
func (g *Gateway) readError(l *lease, err error) *Result {
	switch {
	case errors.Is(err, driver.ErrOffline):
		g.setOffline(l.dev, "")
		g.mu.Lock()
		defer g.mu.Unlock()
		if waitingLocked(l.dev) {
			// Closed meanwhile, as it needs a step again.
			return setupResultLocked(l.dev, fmt.Sprintf("%s is connected but waits for a step before Pinky can use it.", l.dev.name()))
		}
		return errResult(ErrDeviceOffline, "%s is offline.", l.dev.name())
	case errors.Is(err, driver.ErrLocked):
		g.setLocked(l.dev, true)
		return statusResult(StatusLocked, "The phone is locked. Call phone_handoff so the person can unlock it.")
	case errors.Is(err, driver.ErrUnsupported):
		return errResult(ErrUnsupported, "%v", err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errResult(ErrInternal, "The call was cancelled.")
	}
	return errResult(ErrInternal, "Reading the screen failed: %v", err)
}

// observeTimeout bounds one screen read, so a stuck driver cannot hold a
// call forever. It is generous: the first read on an iPhone loads the OCR
// model (15 s measured, about a minute the first time on a new Mac).
const observeTimeout = 60 * time.Second

// readOCR is how a read asks the driver for OCR.
type readOCR int

const (
	ocrOff readOCR = iota
	// ocrAuto: the text the driver finds its tree misses (iOS: what OCR
	// reads that no element shows; Android: over web views and canvases
	// whose tree is empty or sparse).
	ocrAuto
	// ocrAll: all the text no element shows, however complete the tree
	// looks: the agent asked for it, or a finger's spot is checked
	// (driver.ObserveOptions.OCRAll).
	ocrAll
)

// observe reads the screen, assigns refs and collects events. With ocr,
// the driver adds the text its tree misses (ocrAuto).
func (g *Gateway) observe(ctx context.Context, d *device, ocr bool) (*screen.Screen, error) {
	mode := ocrOff
	if ocr {
		mode = ocrAuto
	}
	return g.observeOCR(ctx, d, mode)
}

// observeOCR is observe with the kind of OCR the read asks for.
func (g *Gateway) observeOCR(ctx context.Context, d *device, mode readOCR) (*screen.Screen, error) {
	g.mu.Lock()
	dev, onBG := g.screenOfLocked(d)
	video := d.video
	g.mu.Unlock()
	if dev == nil {
		return nil, driver.ErrOffline
	}
	ctx, cancel := context.WithTimeout(ctx, observeTimeout)
	defer cancel()
	start := stamp() // ordered against the pictures it vouches for (clock.go)
	// The recording learns of the read before it runs: while it runs, what
	// it may show limits what other reads vouch for (video.go).
	var readTok uint64
	if video != nil {
		readTok = video.readStarting(start)
	}
	opt := driver.ObserveOptions{OCR: mode != ocrOff, OCRAll: mode == ocrAll}
	// Asked on every read: an iPhone whose remoteAXService fails, or one
	// below iOS 27 without the helper, takes a picture to place the tree's
	// elements, OCR or not.
	opt.MayCapture = func(s *screen.Screen) bool { return g.mayCapture(d, s) }
	if opt.OCR {
		// A picture from the live video, for drivers whose screenshots are
		// slow (an iPhone over Wi-Fi) or missing (an Android background
		// display); it shares the mirror's stream.
		key := hubKey(d.id, onBG)
		opt.Frame = func(ctx context.Context) (image.Image, error) { return g.frameShot(ctx, key, dev) }
	}
	s, err := dev.Observe(ctx, opt)
	if err != nil && video != nil {
		video.readFailed(readTok)
	}
	if err != nil && onBG && errors.Is(err, driver.ErrUnsupported) {
		// The display cannot be read (Android keeps some virtual displays
		// from accessibility services): the task moves to the phone's own
		// screen, which can.
		g.mu.Lock()
		left := false
		if l := d.lease; l != nil && l.bg != nil && sameDevice(l.bg, dev) {
			g.leaveBackgroundLocked(l, fmt.Sprintf("Pinky cannot read the background display (%v), so the task runs on the phone's own screen.", err))
			left = true
		}
		g.mu.Unlock()
		if left {
			g.wake(ctx, d)
			return g.observeOCR(ctx, d, mode)
		}
	}
	if err != nil {
		if errors.Is(err, driver.ErrOffline) {
			g.setOffline(d, "")
		}
		return nil, err
	}
	if s == nil {
		if video != nil {
			video.readFailed(readTok)
		}
		return nil, errors.New("the driver returned no screen")
	}
	// One-time codes are masked before anything else sees the read: the
	// agent, history, approvals and refs (policy.MaskScreen).
	g.policy.MaskScreen(s)
	// So are blocked apps' notifications and cards on other screens
	// (hidden.go); their notification events are matched by name too.
	names := g.hideBlocked(ctx, d, dev, s)
	d.refs.Assign(s)
	evs := g.policy.FilterEventsNamed(s.Events, names)
	_, blocked := g.policy.Blocked(s.App)
	s.Events = nil
	g.mu.Lock()
	if blocked && !allowsLocked(d.lease, s.App) {
		// Toasts and dialogs on a blocked app's screen are the app's own.
		evs = notificationsOnly(evs)
	}
	d.events = append(d.events, evs...)
	if n := len(d.events); n > 50 {
		d.events = append([]screen.Event(nil), d.events[n-50:]...)
	}
	d.last = s
	if mode != ocrOff && !d.ocr {
		d.ocr = hasOCR(s)
	}
	g.confirmShotLocked(d, s, start)
	_, reporter := dev.(driver.LockReporter)
	moved := false
	if _, bg := g.screenOfLocked(d); bg != onBG {
		moved = true // the task moved to another screen meanwhile
	}
	person := d.person
	g.mu.Unlock()
	switch {
	case video != nil && moved:
		video.readFailed(readTok)
	case video != nil:
		video.read(readTok, start, recordableScreen(g.policy, s) && !person)
	}
	// A background display is always unlocked; the phone's lock shows only
	// on its own screen.
	if !onBG && (s.Locked || !reporter) {
		g.setLocked(d, s.Locked)
	}
	return s, nil
}

// mayCapture reports whether a driver may capture a screen it read, to
// read its text by OCR (driver.ObserveOptions.MayCapture): not a locked
// phone, whose lock screen can show any app's notifications, nor an app
// Pinky cannot name, nor a blocked app, unless the person allowed that app
// for the lease's task (the person chose so on 2026-11-08). The picture
// stays in the driver's memory and is dropped once read: only text and
// positions come back, which the agent sees only where it sees the screen
// (agentView). Screenshots, history and recordings still take no picture
// of a blocked app (recordableScreen).
func (g *Gateway) mayCapture(d *device, s *screen.Screen) bool {
	if recordableScreen(g.policy, s) {
		return true
	}
	if s == nil || s.Locked {
		return false
	}
	if _, blocked := g.policy.Blocked(s.App); !blocked {
		return false // an app Pinky cannot name
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return allowsLocked(d.lease, s.App)
}

// readsText reports whether reads with OCR may add text on the device's
// screen: one has (an iPhone's), or its driver can read text from pixels
// on this computer (driver.TextReader).
func (g *Gateway) readsText(d *device) bool {
	g.mu.Lock()
	seen := d.ocr
	dev, _ := g.screenOfLocked(d)
	g.mu.Unlock()
	if seen {
		return true
	}
	// Without the lock: the first answer may load the recognizer.
	tr, ok := dev.(driver.TextReader)
	return ok && tr.ReadsText()
}

// noOCR reports whether the driver of the device's screen says that this
// computer cannot read text from pixels.
func (g *Gateway) noOCR(d *device) bool {
	g.mu.Lock()
	dev, _ := g.screenOfLocked(d)
	g.mu.Unlock()
	tr, ok := dev.(driver.TextReader)
	return ok && !tr.ReadsText()
}

// noOCRMessage answers phone_observe { ocr: true } where noOCR.
const noOCRMessage = "This computer cannot read text from the screen's pixels (no OCR), so text that no element shows is missing; a screenshot shows it."

func notificationsOnly(evs []screen.Event) []screen.Event {
	var keep []screen.Event
	for _, ev := range evs {
		if ev.Kind == screen.EventNotification {
			keep = append(keep, ev)
		}
	}
	return keep
}

// errScreenChanged means the screen changed while a screenshot was taken.
var errScreenChanged = errors.New("the screen changed while the screenshot was taken")

// errLockedShot means the phone is locked: its lock screen can show
// notifications from blocked apps.
var errLockedShot = errors.New("the phone is locked, and its lock screen can show notifications from blocked apps")

// shotCover counts the boxes a screenshot got: over one-time codes, and
// over blocked apps' notifications and cards (hidden.go).
type shotCover struct{ codes, hidden int }

// screenshot takes a screenshot of the screen s describes, for an agent.
// It reads the screen again afterwards and drops the image when the
// foreground app changed, is blocked or unknown, or the phone locked, so a
// blocked app that comes to the front in between is never captured. What
// the reads hid of blocked apps is covered (coverHidden), and so are
// one-time codes (coverCodes).
func (g *Gateway) screenshot(ctx context.Context, d *device, s *screen.Screen) (img image.Image, cov shotCover, err error) {
	dev, bg := g.screenOf(d)
	if dev == nil {
		return nil, cov, driver.ErrOffline
	}
	if s.Locked {
		return nil, cov, errLockedShot
	}
	if !recordableScreen(g.policy, s) {
		return nil, cov, errScreenChanged
	}
	if _, unplaced := hiddenPlaces(1, 1, s); unplaced {
		return nil, cov, errHiddenShot
	}
	img, err = g.shotOf(ctx, d, dev, bg)
	if err != nil {
		return nil, cov, err
	}
	after, err := g.observe(ctx, d, false)
	if err != nil {
		return nil, cov, err
	}
	if !recordableScreen(g.policy, after) || after.App != s.App {
		return nil, cov, errScreenChanged
	}
	img, cov.hidden, err = coverHidden(img, s, after)
	if err != nil {
		return nil, shotCover{}, err
	}
	img, codes, ok := g.coverCodes(img, true, s, after)
	if !ok {
		return nil, shotCover{}, errCodeShot
	}
	cov.codes = codes
	return img, cov, nil
}

// agentView hides a blocked app's screen, unless the person allowed the
// app for the lease's task.
func (g *Gateway) agentView(l *lease, raw *screen.Screen) (*screen.Screen, string) {
	if rule, blocked := g.policy.Blocked(raw.App); blocked && !g.allows(l, raw.App) {
		return &screen.Screen{Width: raw.Width, Height: raw.Height, App: screen.App{Name: "blocked app"},
			Orientation: raw.Orientation, Locked: raw.Locked, CapturedAt: raw.CapturedAt}, rule
	}
	cp := *raw
	cp.Elements = make([]screen.Element, 0, len(raw.Elements))
	for _, e := range raw.Elements {
		// Text read from the status bar's pixels (clock, signal and battery
		// icons as OCR sees them) only adds noise to every diff.
		if e.Source == screen.SourceOCR && ocrInStatusBar(raw, e) {
			continue
		}
		cp.Elements = append(cp.Elements, e)
	}
	cp.Events = nil
	return &cp, ""
}

// screenResult builds the agent's view of a fresh screen, as a diff against
// what it saw last, and makes it the new baseline.
func (g *Gateway) screenResult(l *lease, raw *screen.Screen, full bool) *Result {
	view, rule := g.agentView(l, raw)
	g.mu.Lock()
	d := l.dev
	evs := d.events
	d.events = nil
	if rule != "" {
		evs = notificationsOnly(evs)
	}
	view.Events = evs
	prev := l.baseline
	l.baseline = view
	name := d.name()
	g.mu.Unlock()
	res := &Result{Device: d.id, DeviceName: name, Screen: view}
	lines, isFull := screen.Diff(prev, view)
	if full || isFull {
		res.Full = true
	} else {
		res.Diff = lines
	}
	if rule != "" {
		res.Rule = rule
		res.Message = fmt.Sprintf("The foreground app is blocked by the rule %q; Pinky hides its screen. Press home or launch another app.", rule)
	}
	return res
}

// settle waits until the screen stops changing, up to SettleMax.
func (g *Gateway) settle(ctx context.Context, d *device) bool {
	dev, _ := g.screenOf(d)
	if dev == nil {
		return false
	}
	if s, ok := dev.(driver.Settler); ok {
		settled, err := s.WaitSettled(ctx, g.opt.SettleMax)
		return err == nil && settled
	}
	deadline := time.Now().Add(g.opt.SettleMax)
	if !sleepCtx(ctx, g.opt.PollInterval, nil) {
		return false
	}
	prev, err := g.observe(ctx, d, false)
	if err != nil {
		return false
	}
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, g.opt.PollInterval, nil) {
			return false
		}
		cur, err := g.observe(ctx, d, false)
		if err != nil {
			return false
		}
		if body(cur) == body(prev) {
			return true
		}
		prev = cur
	}
	return false
}

// body is a screen without its events, for comparing screens.
func body(s *screen.Screen) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(s.Header())
	for _, e := range s.Elements {
		b.WriteByte('\n')
		b.WriteString(e.Line())
	}
	return b.String()
}

// snapshot is what an approval re-checks before acting: the target and the
// screen text shown in the panel, status bar excluded.
type snapshot struct {
	app    string
	target *screen.Element
	point  screen.Point
	text   []string
	// asks and grantApp say what "Allow for this task" grants, and
	// "Always allow" keeps (always.go).
	asks     []string
	grantApp screen.App
	// launch is set for phone_launch, whose app is not the one the panel
	// names; Always allow is not offered for it.
	launch bool
	// unblock is set when the launch asks to allow a blocked app for the
	// task: any Allow does (unblock.go).
	unblock bool
}

func takeSnapshot(s *screen.Screen, p *prep, v policy.Verdict) *snapshot {
	snap := &snapshot{app: s.App.ID + "\x00" + s.App.Name, point: p.point, text: screenText(s),
		asks: append([]string(nil), v.Asks...), grantApp: grantApp(p), launch: p.input.Op == policy.OpLaunch,
		unblock: p.unblock}
	if p.target != nil {
		t := *p.target
		snap.target = &t
	}
	return snap
}

// changed reports whether the screen differs from the snapshot in a way
// that makes the approval stale.
func (snap *snapshot) changed(cur *screen.Screen, p *prep) bool {
	if snap.app != cur.App.ID+"\x00"+cur.App.Name {
		return true
	}
	if (snap.target == nil) != (p.target == nil) {
		return true
	}
	if snap.target != nil {
		a, b := snap.target, p.target
		if a.Role != b.Role || a.Label != b.Label || !near(a.Bounds, b.Bounds) || states(*a) != states(*b) {
			return true
		}
	}
	if !near(screen.Rect{X: snap.point.X, Y: snap.point.Y}, screen.Rect{X: p.point.X, Y: p.point.Y}) {
		return true
	}
	now := screenText(cur)
	if len(now) != len(snap.text) {
		return true
	}
	for i := range now {
		if now[i] != snap.text[i] {
			return true
		}
	}
	return false
}

// relocate finds the element the agent knew as ref on the screen it last
// saw, when the fresh screen gave that element another ref. Refs follow an
// element's ancestry, which some drivers report differently from one read
// to the next (iOS apps whose views get re-wrapped), while agents are told
// that a ref lasts as long as its element keeps its label and place. It
// matches label and id, with the position within a few pixels: the element
// of the same role when exactly one matches, else the only element there of
// any role (the same iOS button also comes back as text). It gives up when
// that leaves more than one.
func relocate(seen, cur *screen.Screen, ref int) (screen.Element, bool) {
	if seen == nil || cur == nil {
		return screen.Element{}, false
	}
	old, ok := seen.Find(ref)
	if !ok || old.Source == screen.SourceOCR || old.Label == "" {
		return screen.Element{}, false
	}
	var same, any []screen.Element
	for _, e := range cur.Elements {
		if e.Ref == 0 || e.Source == screen.SourceOCR {
			continue
		}
		if e.Label == old.Label && e.ID == old.ID && near(e.Bounds, old.Bounds) {
			any = append(any, e)
			if e.Role == old.Role {
				same = append(same, e)
			}
		}
	}
	switch {
	case len(same) == 1:
		return same[0], true
	case len(same) == 0 && len(any) == 1:
		return any[0], true
	}
	return screen.Element{}, false
}

// near allows a few pixels of layout jitter.
func near(a, b screen.Rect) bool {
	const tol = 4
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	return abs(a.X-b.X) <= tol && abs(a.Y-b.Y) <= tol && abs(a.W-b.W) <= tol && abs(a.H-b.H) <= tol
}

// screenText lists the labels and states of tree elements outside the
// status bar, sorted. A screen with no tree text (a canvas app) uses its
// OCR text instead.
func screenText(s *screen.Screen) []string {
	var out, ocr []string
	for _, e := range s.Elements {
		if e.Label == "" || inStatusBar(s, e) {
			continue
		}
		if e.Source == screen.SourceOCR {
			ocr = append(ocr, e.Label)
			continue
		}
		out = append(out, e.Role+"\x00"+e.Label+"\x00"+states(e))
	}
	if len(out) == 0 {
		out = ocr
	}
	sort.Strings(out)
	return out
}

// states lists an element's states except focus, which a tap changes.
func states(e screen.Element) string {
	var out []string
	for _, st := range e.States {
		if st != screen.StateFocused {
			out = append(out, st)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func inStatusBar(s *screen.Screen, e screen.Element) bool {
	id := strings.ToLower(e.ID)
	path := strings.ToLower(e.Path)
	if strings.HasPrefix(id, "com.android.systemui:") || strings.Contains(path, "statusbar") || strings.Contains(path, "status_bar") {
		return true
	}
	return s.Height > 0 && !e.Bounds.Empty() && e.Bounds.Y >= 0 && e.Bounds.Y+e.Bounds.H <= s.Height*45/1000
}

// ocrInStatusBar reports whether an OCR line sits in the status bar: its
// center in the top 6% of the screen, which covers the taller status bars
// of iPhones with a notch or Dynamic Island (150 px of 2436 on a 13 mini).
func ocrInStatusBar(s *screen.Screen, e screen.Element) bool {
	if s.Height <= 0 || e.Bounds.Empty() {
		return false
	}
	return e.Bounds.Center().Y < s.Height*6/100
}

// describe says what an action does, in plain words for history.
func describe(a Action, p *prep) string {
	label := ""
	if p != nil && p.target != nil && p.target.Label != "" {
		label = "“" + clip(p.target.Label, 60) + "”"
	}
	switch a.Tool {
	case "tap":
		verb := "Tapped"
		if a.LongPress {
			verb = "Long-pressed"
		}
		if label != "" {
			return verb + " " + label
		}
		if a.Point != nil {
			return fmt.Sprintf("%s at %d,%d", verb, a.Point.X, a.Point.Y)
		}
		return verb
	case "type":
		s := fmt.Sprintf("Typed “%s”", clip(a.Text, 60))
		if label != "" {
			s += " into " + label
		}
		if a.Submit {
			s += " and submitted"
		}
		return s
	case "swipe":
		if a.Direction != "" {
			if label != "" {
				return fmt.Sprintf("Swiped %s in %s", a.Direction, label)
			}
			return "Swiped " + a.Direction
		}
		return fmt.Sprintf("Swiped from %d,%d to %d,%d", a.From.X, a.From.Y, a.To.X, a.To.Y)
	case "key":
		return "Pressed " + strings.ReplaceAll(a.Key, "_", " ")
	case "launch":
		name := a.App
		if p != nil && p.launch.Name != "" {
			name = p.launch.Name
		}
		return "Opened " + name
	}
	return a.Tool
}
