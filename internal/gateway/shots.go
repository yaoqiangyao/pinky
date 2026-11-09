package gateway

import (
	"context"
	"fmt"
	"image"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/history"
	"github.com/DIYgod/pinky/internal/screen"
)

// History screenshots are taken in the background, so the agent never
// waits for one. When an action has settled and the screen is read, the
// result goes back to the agent while a screenshot is taken; the next
// action reuses it as its "before" picture when the screen still matches,
// instead of taking one before it acts. Other screens the agent is shown
// (phone_connect, phone_observe, phone_wait_for, a hand-back) get one the
// same way, so the action that follows has a "before" picture too.
//
// A blocked app is never captured or stored here. (OCR may read one the
// person allowed for the task, from a picture the driver keeps in memory
// only: mayCapture.) A background screenshot is
// bracketed by two reads, like the screenshots agents get: it is kept only
// when the read just before it and a later read, started after the
// screenshot returned, show the same app, which is not blocked, on an
// unlocked phone, with no input from the agent or the person in between.
// The later read is the one the next call makes anyway (an action reads
// the screen before it acts); when the task ends first, the gateway reads
// once more in the background, unless the person pressed Stop. Input drops a screenshot that is still
// being taken or waiting, and so does another app, or shotWait without a
// read. In phone_batch the next step's input comes before any read, so the
// steps in between have no pictures.
//
// A kept screenshot is written to history only once a step shows it.

// shotState is where a background screenshot is.
type shotState int

const (
	shotTaking  shotState = iota // the driver call is running
	shotWaiting                  // taken; waits for a read to confirm it
	shotKept
	shotDropped
)

// shotWait is how long a screenshot waits for a confirming read.
const shotWait = 10 * time.Minute

// bgShot is a history screenshot of one read.
type bgShot struct {
	key    string     // shotKey of the read
	app    screen.App // the read's app
	inputs uint64     // d.inputs at the read
	state  shotState
	done   chan struct{} // closed when the driver call returns
	doneAt time.Time
	expire *time.Timer // drops it after shotWait while it waits
	img    image.Image // until written to history; at history size
	id     string      // media id once written
	uses   []shotUse   // step pictures that show it once kept
	// reads are the reads it shows, which place the one-time codes it
	// covers when written (codes.go); covered is set when they are
	// covered already.
	reads   []*screen.Screen
	covered bool
}

// shotUse is a step picture waiting for a screenshot.
type shotUse struct {
	task  *task
	index int
	after bool // the step's after picture, else its before
}

// shotKey identifies what a screenshot shows, so a later read can tell
// whether it still applies: the size, app, lock and keyboard, and the tree
// elements outside the status bar by role, label and state. Refs and
// positions are left out (iOS places elements by OCR, which varies between
// reads), and so are OCR lines (a read without OCR must match one with it).
func shotKey(s *screen.Screen) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d|%s|%s|%t|%t", s.Width, s.Height, s.App.ID, s.App.Name, s.Locked, s.Keyboard)
	for _, e := range s.Elements {
		if e.Source == screen.SourceOCR || inStatusBar(s, e) {
			continue
		}
		b.WriteString("\n" + e.Role + "\x00" + e.Label + "\x00" + states(e))
	}
	return b.String()
}

// recording reports whether steps get screenshots.
func (g *Gateway) recording() bool { return g.store != nil && !g.opt.NoScreenshots }

// startShot takes a history screenshot of s, a read the agent is shown, in
// the background. It returns the screenshot that will show s: the kept one
// when it still matches, a new one, or nil when there will be none.
func (g *Gateway) startShot(d *device, s *screen.Screen) *bgShot {
	if !g.recording() || !recordableScreen(g.policy, s) {
		return nil
	}
	key := shotKey(s)
	g.mu.Lock()
	defer g.mu.Unlock()
	if k := d.kept; k != nil && k.key == key {
		return k
	}
	if d.person || d.dev == nil {
		return nil
	}
	if p := d.shot; p != nil {
		switch {
		case p.key == key:
			return p
		case p.state == shotTaking || len(p.uses) > 0:
			return nil // one at a time; a step waits for that one
		}
		g.dropShotLocked(d, p)
	}
	p := &bgShot{key: key, app: s.App, inputs: d.inputs, done: make(chan struct{}), reads: []*screen.Screen{s}}
	d.shot = p
	g.shots.add()
	dev, bg := g.screenOfLocked(d)
	go g.takeShot(d, dev, bg, p)
	return p
}

// takeShot runs the screenshot; it then waits for a read to confirm it.
func (g *Gateway) takeShot(d *device, dev driver.Device, background bool, p *bgShot) {
	defer g.shots.done()
	defer close(p.done)
	ctx, cancel := context.WithTimeout(g.ctx, observeTimeout)
	defer cancel()
	img, err := g.shotOf(ctx, d, dev, background)
	if err == nil && img != nil {
		img = history.Downscale(img, history.MaxShotSide)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case p.state != shotTaking:
		// Dropped meanwhile.
	case err != nil || img == nil || d.inputs != p.inputs:
		g.dropShotLocked(d, p)
	default:
		p.img, p.state, p.doneAt = img, shotWaiting, stamp() // ordered against reads (clock.go)
		p.expire = time.AfterFunc(shotWait, func() {
			g.mu.Lock()
			if p.state == shotWaiting {
				g.dropShotLocked(d, p)
			}
			g.mu.Unlock()
		})
	}
}

// confirmShotLocked checks the screenshot waiting for confirmation against
// read s, which started at start. A read that started after the screenshot
// returned, with no input since, keeps it when it shows the same app and
// drops it otherwise.
func (g *Gateway) confirmShotLocked(d *device, s *screen.Screen, start time.Time) {
	p := d.shot
	if p == nil || p.state != shotWaiting || start.Before(p.doneAt) {
		return
	}
	if d.inputs != p.inputs || d.person || !recordableScreen(g.policy, s) || s.App != p.app {
		g.dropShotLocked(d, p)
		return
	}
	p.reads = append(p.reads, s)
	g.keepShotLocked(d, p)
}

// keepShotLocked makes p the device's kept screenshot and shows it in the
// step pictures waiting for it. The app is checked again, in case the
// person blocked it meanwhile.
func (g *Gateway) keepShotLocked(d *device, p *bgShot) {
	if _, blocked := g.policy.Blocked(p.app); blocked {
		g.dropShotLocked(d, p)
		return
	}
	p.state = shotKept
	p.stopExpiry()
	if d.shot == p {
		d.shot = nil
	}
	d.kept = p
	for _, u := range p.uses {
		g.showShotLocked(p, u)
	}
	p.uses = nil
}

func (g *Gateway) dropShotLocked(d *device, p *bgShot) {
	p.state, p.img, p.uses = shotDropped, nil, nil
	p.stopExpiry()
	if d.shot == p {
		d.shot = nil
	}
}

// stopExpiry stops the expiry timer, which would otherwise hold the
// screenshot for shotWait. Callers hold g.mu.
func (p *bgShot) stopExpiry() {
	if p.expire != nil {
		p.expire.Stop()
		p.expire = nil
	}
}

// inputLocked counts input about to reach the phone. A screenshot not yet
// kept is dropped, since the input may change what it shows.
func (g *Gateway) inputLocked(d *device) {
	d.inputs++
	if p := d.shot; p != nil {
		g.dropShotLocked(d, p)
	}
}

// stepWaitsLocked reports whether a step picture waits for the screenshot
// in progress, so the next read should confirm it before any input.
func (g *Gateway) stepWaitsLocked(d *device) bool {
	return d.shot != nil && len(d.shot.uses) > 0
}

// confirmAtEndLocked confirms, after a task ended, the screenshot a step
// of it waits for: no call of that task will read the screen again.
func (g *Gateway) confirmAtEndLocked(d *device) {
	p := d.shot
	if p == nil || len(p.uses) == 0 || g.closed {
		return
	}
	g.shots.add()
	go func() {
		defer g.shots.done()
		select {
		case <-p.done:
		case <-g.ctx.Done():
			return
		}
		// Actions of a task that starts meanwhile come first; their input
		// drops the screenshot.
		d.act.Lock()
		defer d.act.Unlock()
		g.mu.Lock()
		waiting := d.shot == p && p.state == shotWaiting
		g.mu.Unlock()
		if !waiting {
			return
		}
		ctx, cancel := context.WithTimeout(g.ctx, observeTimeout)
		defer cancel()
		g.observe(ctx, d, false)
	}()
}

// keepImage keeps a screenshot that a caller took and checked right after
// read s (the approval panel's, or one the agent asked for), so steps can
// reuse it; covered says its one-time codes are covered already. It
// returns nil for a blocked app or without history screenshots.
func (g *Gateway) keepImage(d *device, s *screen.Screen, img image.Image, covered bool) *bgShot {
	if !g.recording() || img == nil || !recordableScreen(g.policy, s) {
		return nil
	}
	p := &bgShot{key: shotKey(s), app: s.App, img: img, state: shotKept, reads: []*screen.Screen{s}, covered: covered}
	g.mu.Lock()
	defer g.mu.Unlock()
	if d.person {
		return nil
	}
	if q := d.shot; q != nil && q.key == p.key && len(q.uses) == 0 {
		g.dropShotLocked(d, q) // this one replaces it
	}
	d.kept = p
	return p
}

// beforeShot returns the kept screenshot when it shows s, or the last read
// when s is nil, for a step's before picture.
func (g *Gateway) beforeShot(d *device, s *screen.Screen) *bgShot {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s == nil {
		s = d.last
	}
	if s == nil || d.kept == nil || d.kept.key != shotKey(s) {
		return nil
	}
	return d.kept
}

// shotID writes a kept screenshot to history, once, and returns its media
// id; "" when p is nil or not kept.
func (g *Gateway) shotID(p *bgShot) string {
	if p == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if p.state != shotKept {
		return ""
	}
	return g.writeShotLocked(p)
}

// writeShotLocked writes p to history, with its one-time codes and what
// its reads hid of blocked apps (hidden.go) covered where the reads place
// them; a picture with a code or a hidden card they could not place is not
// kept.
func (g *Gateway) writeShotLocked(p *bgShot) string {
	if p.id != "" || p.img == nil || g.store == nil {
		return p.id
	}
	img := p.img
	var boxes []image.Rectangle
	if !p.covered {
		b := img.Bounds()
		var unplaced bool
		// Blocked apps' notifications and cards (hidden.go).
		if boxes, unplaced = hiddenPlaces(b.Dx(), b.Dy(), p.reads...); unplaced {
			p.img = nil
			return ""
		}
		if g.policy.HideCodes() {
			var codes []image.Rectangle
			if codes, unplaced = codePlaces(b.Dx(), b.Dy(), p.reads...); unplaced {
				p.img = nil
				return ""
			}
			boxes = append(boxes, codes...)
		}
	}
	id := history.NewID("m_")
	p.id, p.img, p.reads = id, nil, nil
	g.rec.do(func(ctx context.Context) error {
		if len(boxes) > 0 {
			img = coverBoxes(img, boxes)
		}
		_, err := g.store.SaveScreenshotAs(ctx, id, img)
		return err
	})
	return id
}

// useShot shows p in a step's picture once it is kept.
func (g *Gateway) useShot(p *bgShot, t *task, index int, after bool) {
	if p == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	u := shotUse{task: t, index: index, after: after}
	switch p.state {
	case shotTaking, shotWaiting:
		p.uses = append(p.uses, u)
	case shotKept:
		g.showShotLocked(p, u)
	}
}

// showShotLocked writes p to history if needed and sets it as a step's
// picture, in memory and in history.
func (g *Gateway) showShotLocked(p *bgShot, u shotUse) {
	id := g.writeShotLocked(p)
	if id == "" {
		return
	}
	t := u.task
	for i := len(t.steps) - 1; i >= 0; i-- {
		if t.steps[i].Index == u.index {
			if u.after {
				t.steps[i].After = id
			} else {
				t.steps[i].Before = id
			}
			break
		}
	}
	if g.store != nil {
		before, after := id, ""
		if u.after {
			before, after = "", id
		}
		taskID, index := t.id, u.index
		g.rec.do(func(ctx context.Context) error { return g.store.SetStepMedia(ctx, taskID, index, before, after) })
	}
	g.emit(Event{Kind: EventTasks, DeviceID: t.deviceID, TaskID: t.id})
}

// running counts goroutines and waits until none runs. Unlike a
// sync.WaitGroup, it may be waited on while goroutines start.
type running struct {
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

func (r *running) add() {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
}

func (r *running) done() {
	r.mu.Lock()
	r.n--
	if r.n == 0 && r.cond != nil {
		r.cond.Broadcast()
	}
	r.mu.Unlock()
}

func (r *running) wait() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cond == nil {
		r.cond = sync.NewCond(&r.mu)
	}
	for r.n > 0 {
		r.cond.Wait()
	}
}
