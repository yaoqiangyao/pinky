package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/video"
)

// mirror is a phone's live screen: its stream, decoded on a goroutine of
// its own by a video.Player, and the person's input on it. One per phone,
// shown by the Phone screen or a tile of All phones. Its fields are used
// on the main thread.
type mirror struct {
	a       *app
	id      string
	agent   bool // the display an agent works on in the background, which takes no input
	gen     int
	cancel  context.CancelFunc
	player  *video.Player
	bitmap  *mg.Bitmap
	frame   *video.Frame // the picture the bitmap shows
	state   string       // "connecting", "playing", "failed"
	opening bool         // a try to open the stream is under way
	err     error
	retry   time.Time
	used    int
	closed  bool

	picW, picH int     // the stream's picture size the right way up: the device's pixels
	turn       int     // quarter turns clockwise that put the picture upright (driver.Packet.Turn)
	boxW, boxH float32 // the screen's size on the window, in DIPs

	down      bool         // a touch is down
	lastMove  time.Time    // throttles moves
	scrolling bool         // a wheel or touchpad gesture is underway
	scrollAt  screen.Point // where its finger started
	scrollPos [2]float64   // where its finger is
	scrollSeq int
	compose   string // an input method's composition
}

// mirrorFor returns the phone's mirror, opening its stream if needed.
func (a *app) mirrorFor(id string) *mirror {
	m := a.mirrors[id]
	if m == nil {
		m = &mirror{a: a, id: id}
		a.mirrors[id] = m
		m.open()
	}
	m.used = a.buildSeq
	if m.state == "failed" && !m.opening && time.Now().After(m.retry) && a.videoErr == nil {
		m.open()
	}
	return m
}

// sweepMirrors closes the mirrors this build did not show.
func (a *app) sweepMirrors() {
	for id, m := range a.mirrors {
		if m.used != a.buildSeq {
			m.close()
			delete(a.mirrors, id)
		}
	}
}

func (a *app) closeMirrors() {
	for id, m := range a.mirrors {
		m.close()
		delete(a.mirrors, id)
	}
}

func (m *mirror) open() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.player != nil {
		p := m.player
		go p.Close()
		m.player = nil
	}
	if m.a.videoErr != nil {
		m.state, m.err = "failed", m.a.videoErr
		return
	}
	m.gen++
	gen := m.gen
	ctx, cancel := context.WithCancel(m.a.ctx)
	m.cancel = cancel
	// A retry keeps showing why the last try failed until this one is
	// done, rather than a spinner every few seconds for a phone that has
	// no picture.
	if m.state != "failed" {
		m.state, m.err = "connecting", nil
	}
	m.opening = true
	a := m.a
	go func() {
		logf("pinky: mirror %s: opening the stream (gen %d, agent %v)", m.id, gen, m.agent)
		var s driver.Stream
		var err error
		if m.agent {
			if as, ok := a.api.(agentStreamer); ok {
				s, err = as.AgentStream(ctx, m.id, driver.StreamOptions{})
			} else {
				err = fmt.Errorf("agent displays: %w", driver.ErrNotFound)
			}
		} else {
			s, err = a.api.Stream(ctx, m.id, driver.StreamOptions{})
		}
		var p *video.Player
		if err == nil {
			p, err = video.NewPlayer(s, video.PlayerOptions{YUV: true})
			if err != nil {
				s.Close()
			}
		}
		logf("pinky: mirror %s: stream opened (gen %d): %v", m.id, gen, err)
		a.post(func() {
			logf("pinky: mirror %s: posted (gen %d, closed %v, current gen %d)", m.id, gen, m.closed, m.gen)
			if m.closed || m.gen != gen {
				if p != nil {
					go p.Close()
				}
				return
			}
			m.opening = false
			if err != nil {
				m.state, m.err = "failed", err
				m.retry = time.Now().Add(3 * time.Second)
				if errors.Is(err, video.ErrNotFound) {
					a.videoErr = err
					return
				}
				m.retryLater()
				return
			}
			m.player, m.state = p, "playing"
			go func() {
				<-p.Done()
				a.post(func() {
					if m.player == p && !m.closed {
						m.state, m.err = "failed", p.Err()
						m.retry = time.Now().Add(2 * time.Second)
						m.retryLater()
					}
				})
			}()
		})
	}()
}

// retryLater opens the stream again once its retry is due, whether or not
// the view is built meanwhile: an idle window, or one on a locked screen,
// builds nothing.
func (m *mirror) retryLater() {
	gen := m.gen
	time.AfterFunc(max(time.Until(m.retry), 0), func() {
		m.a.post(func() {
			if !m.closed && m.gen == gen && m.state == "failed" && !m.opening && m.a.videoErr == nil {
				m.open()
			}
		})
	})
}

// missing reports an agent's display that the gateway says is not open,
// as before a background task starts it.
func (m *mirror) missing() bool {
	return m.state == "failed" && errors.Is(m.err, driver.ErrNotFound)
}

func (m *mirror) close() {
	logf("pinky: mirror %s: closed", m.id)
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	if p := m.player; p != nil {
		m.player = nil
		go p.Close()
	}
}

// aspect is the screen's width over its height the right way up, from the
// stream once it plays (landscape when the phone is turned), else the
// phone's usual portrait (bezel.go).
func (m *mirror) aspect(d gateway.DeviceView) float32 {
	if m.picW > 0 && m.picH > 0 {
		return float32(m.picW) / float32(m.picH)
	}
	return bezelOf(d).defaultAspect()
}

// lastPicture copies the picture the mirror shows, for a screenshot: the
// player writes its planes again. turn puts it the right way up.
func (m *mirror) lastPicture() (pic *video.YUV, turn int) {
	if m.frame == nil || m.frame.YUV == nil {
		return nil, 0
	}
	return m.frame.YUV.Clone(), m.turn
}

// drawTurned draws a picture into r turned turn quarter turns clockwise,
// which puts a phone's picture the right way up when the phone sends its
// display as mounted (an iPhone or iPad on iOS 27; driver.Packet.Turn).
func drawTurned(pt *mg.Painter, bm *mg.Bitmap, r mg.Rect, turn int) {
	turn &= 3
	if turn == 0 {
		pt.Image(bm, r, mg.Contain)
		return
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	in := r
	if turn%2 == 1 {
		// Drawn sideways into the box turned the other way, then turned.
		in = mg.Rect{X: cx - r.H/2, Y: cy - r.W/2, W: r.H, H: r.W}
	}
	pt.Transform(mg.Rotation(float32(90*turn)), cx, cy, func() { pt.Image(bm, in, mg.Contain) })
}

// toPicture turns a point of the upright picture back into the picture as
// the stream sends it, where the phone takes touches.
func (m *mirror) toPicture(p screen.Point) screen.Point {
	return turnPoint(p, m.picW, m.picH, (4-m.turn&3)&3)
}

// turnPoint returns where the pixel at p of a w×h picture lands when the
// picture turns q quarter turns clockwise.
func turnPoint(p screen.Point, w, h, q int) screen.Point {
	switch q & 3 {
	case 1:
		return screen.Point{X: h - 1 - p.Y, Y: p.X}
	case 2:
		return screen.Point{X: w - 1 - p.X, Y: h - 1 - p.Y}
	case 3:
		return screen.Point{X: p.Y, Y: w - 1 - p.X}
	}
	return p
}

// showPlanes shows a picture's planes in bm, made for the first one, and
// returns it. MyGo's renderers convert and scale the planes as they draw
// them, so a picture costs the CPU no more than the decoder's copy.
func showPlanes(bm *mg.Bitmap, pic *video.YUV) *mg.Bitmap {
	img := mg.YUVImage{Format: mg.I420, Width: pic.Width, Height: pic.Height,
		Planes: pic.Planes, Strides: pic.Strides, Matrix: mg.BT601, FullRange: pic.FullRange}
	if pic.NV12 {
		img.Format = mg.NV12
	}
	switch pic.Matrix {
	case video.BT709:
		img.Matrix = mg.BT709
	case video.BT2020:
		img.Matrix = mg.BT2020
	}
	if bm == nil {
		bm = mg.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	}
	bm.UpdateYUV(&img)
	return bm
}

// overlayStep is the step whose target the mirror outlines: the one in
// progress or waiting for the person.
func overlayStep(t *gateway.TaskView) (gateway.StepView, stepKind, bool) {
	if t == nil || len(t.Steps) == 0 || !running(t.State) || t.State == gateway.TaskPaused {
		return gateway.StepView{}, 0, false
	}
	i := len(t.Steps) - 1
	s := t.Steps[i]
	k := kindOf(*t, i)
	if (k != stepNow && k != stepWait) || (s.Target.Empty() && s.Point == (screen.Point{})) {
		return gateway.StepView{}, 0, false
	}
	return s, k, true
}

// agentOrTheAgent names a task's agent, for labels.
func agentOrTheAgent(t *gateway.TaskView) string {
	if t != nil && t.Agent != "" {
		return t.Agent
	}
	return locales.AgentGenericStart()
}

// mirrorView draws the screen inside a phone's bezel: the video, the
// agent's overlay, the lock, and takes the person's input unless it is a
// tile.
func (a *app) mirrorView(c *mg.Context, m *mirror, d gateway.DeviceView, t *gateway.TaskView, radius float32, tile bool) {
	p := pal(c)
	// The agent's own display is never locked and takes no input: the
	// person uses the phone's screen.
	locked := a.isLocked(d) && !m.agent
	step, kind, outline := overlayStep(t)
	scr := mg.Box(c).Fill().Radius(radius).Clip().Background(hex("#000000"))
	switch {
	case m.agent:
		scr.Label(locales.MirrorAgentScreenLabel(agentOrTheAgent(t), d.Info.Name))
	case !tile:
		scr.Focusable().FocusRing(false).Cursor(mg.CursorPointer).Label(locales.MirrorScreenLabel(d.Info.Name)).
			HandleInput(func(ev mg.InputEvent) bool { return a.mirrorInput(m, d, ev) })
		scr.TextCaret(mg.Rect{X: m.boxW/2 - 1, Y: m.boxH * 0.8, W: 2, H: 20})
	}
	scr.Draw(func(pt *mg.Painter, r mg.Rect) {
		m.boxW, m.boxH = r.W, r.H
		if pl := m.player; pl != nil {
			if f := pl.Take(); f != nil {
				if m.bitmap == nil {
					// The view was built before the first picture, with
					// the Connecting overlay; painting does not build it
					// again, and a still screen sends nothing else.
					a.post(func() {})
				}
				m.bitmap = showPlanes(m.bitmap, f.YUV)
				m.frame = f
				w, h := f.Width, f.Height
				if f.Turn%2 == 1 {
					w, h = h, w
				}
				if m.picW > 0 && (w != m.picW || h != m.picH) {
					// The phone turned: lay the mirror out again for the
					// picture's new shape.
					a.post(func() {})
				}
				m.picW, m.picH, m.turn = w, h, f.Turn
			}
			// Repaint at the stream's rate, picking up the newest
			// picture each time, without building the view again.
			pt.AnimationFrameRate(60)
		}
		if m.bitmap != nil && m.state == "playing" {
			pt.Clip(r, radius, func() { drawTurned(pt, m.bitmap, r, m.turn) })
		}
		if outline && !locked && m.picW > 0 {
			// Clipped to the screen: targets are in screen pixels, which on
			// an iPhone differ from the picture's by about 1%, so an
			// outline at the edge may reach a little past it.
			pt.Clip(r, radius, func() {
				drawOverlay(pt, r, p, step, kind, float32(m.picW), float32(m.picH), tile)
			})
		}
	})
	scr.Children(func() {
		switch {
		case locked:
			// The lock: the screen dimmed, and a pill naming it. On the
			// Phone screen both give way while the pointer is on the
			// mirror, which then shows the lock screen as it is, for the
			// person to unlock by hand: the pill sits where the PIN pad
			// comes up. Clicks go through them either way.
			shown := float32(1)
			if !tile {
				shown = scr.Animate("lock", b2f(!scr.Hovered()), 150*time.Millisecond)
			}
			mg.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(mg.RGBA(10, 12, 18, 0.62)).Opacity(shown).PassThrough()
			// The pill sits a little below the middle, as on the prototype's lock screen.
			pill := mg.Row(c).Attach(mg.AnchorCenter, mg.AnchorCenter).Top(m.boxH*0.04).Gap(8).AlignItems(mg.Center).
				Padding(9, 14).Radius(999).Background(mg.RGBA(255, 255, 255, 0.14)).Border(1, mg.RGBA(255, 255, 255, 0.22)).
				TextColor(hex("#ffffff")).Opacity(shown).PassThrough()
			if tile {
				pill.Padding(6, 10).Gap(6)
			}
			pill.Children(func() {
				mg.Icon(c, icLock).FontSize(16)
				label := locales.MirrorLocked(d.Info.Name)
				if tile {
					label = locales.MirrorLockedTile()
				}
				mg.Text(c, label).FontSize(14).FontWeight(600).NoWrap()
			})
		case m.state == "connecting" || (m.state == "playing" && m.bitmap == nil):
			mg.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(10).PassThrough().Children(func() {
				spinner(c)
				if !tile {
					mg.Text(c, locales.MirrorConnecting()).FontSize(13).TextColor(hex("#a3a3ad"))
				}
			})
		case m.state == "failed":
			mg.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(8).Padding(20).PassThrough().Children(func() {
				mg.Icon(c, icAlert).FontSize(22).TextColor(hex("#f7a74e"))
				if !tile {
					mg.Text(c, mirrorError(m.err)).FontSize(12.5).TextColor(hex("#d0d0d8")).TextAlign(mg.Center).MaxWidth(240)
				}
			})
		}
		if m.compose != "" && !tile {
			mg.Text(c, m.compose).Attach(mg.AnchorBottom, mg.AnchorBottom).Bottom(28).MaxWidth(m.boxW-32).Padding(6, 10).Radius(8).
				Background(mg.RGBA(20, 20, 24, 0.9)).TextColor(hex("#ffffff")).FontSize(14).Underline().PassThrough()
		}
	})
}

func mirrorError(err error) string {
	if err == nil {
		return locales.MirrorStreamEnded()
	}
	msg := err.Error()
	if errors.Is(err, video.ErrNotFound) {
		// The first sentence says what is missing and how to install it.
		if i := strings.Index(msg, ". "); i > 0 {
			return msg[:i+1] + " " + strings.TrimSuffix(msg[i+2:], ": "+video.ErrNotFound.Error())
		}
		return msg
	}
	if errors.Is(err, driver.ErrOffline) {
		return locales.MirrorOffline()
	}
	if errors.Is(err, driver.ErrUnsupported) {
		return locales.MirrorUnsupported()
	}
	return locales.MirrorNoPicture(msg)
}

// drawOverlay outlines the step's target and, while the agent touches,
// draws the touch with two ripples, as the prototype does.
func drawOverlay(pt *mg.Painter, r mg.Rect, p *palette, s gateway.StepView, kind stepKind, pw, ph float32, tile bool) {
	sx, sy := r.W/pw, r.H/ph
	if !s.Target.Empty() {
		box := mg.Rect{
			X: r.X + float32(s.Target.X)*sx - 5, Y: r.Y + float32(s.Target.Y)*sy - 5,
			W: float32(s.Target.W)*sx + 10, H: float32(s.Target.H)*sy + 10,
		}
		width := float32(2)
		if tile {
			width = 1.5
		}
		pt.Stroke(box, p.ovl, min(16, box.H/2), width)
	}
	if kind != stepNow || s.Point == (screen.Point{}) {
		return
	}
	cx, cy := r.X+float32(s.Point.X)*sx, r.Y+float32(s.Point.Y)*sy
	size := min(r.W*0.2, 56)
	if tile {
		size = 36
	}
	now := pt.Now()
	for _, delay := range []time.Duration{0, 900 * time.Millisecond} {
		phase := float32((now.Add(-delay).UnixMilli())%1800) / 1800
		ease := 1 - float32(math.Pow(float64(1-phase), 3))
		radius := size / 2 * (0.35 + 1.45*ease)
		alpha := 0.7 * (1 - ease)
		var ring mg.Path
		ring.Circle(cx, cy, radius)
		pt.StrokePath(&ring, 2, p.ovl.Alpha(alpha))
	}
	if !tile {
		var halo, center mg.Path
		halo.Circle(cx, cy, 9)
		pt.FillPath(&halo, mg.RGBA(255, 255, 255, 0.9))
		center.Circle(cx, cy, 6)
		pt.FillPath(&center, p.ovl)
	}
	pt.AnimationFrame()
}

// mirrorInput turns the person's pointer, wheel and keys on the mirror
// into touches, keys and text in device pixels. The gateway pauses the
// agent on any of them.
func (a *app) mirrorInput(m *mirror, d gateway.DeviceView, ev mg.InputEvent) bool {
	if m.picW == 0 || m.boxW == 0 || d.Status == gateway.DeviceOffline {
		return false
	}
	id := d.Info.ID
	toDevice := m.toDevice
	touch := func(act driver.TouchAction, pt screen.Point) {
		pt = m.toPicture(pt)
		a.call(id, locales.MirrorCallTouch(), func() error { return a.api.Touch(id, act, pt) })
	}
	key := func(k driver.Key) {
		name := string(k)
		if translated() {
			name = keyName(name)
		}
		a.call(id, locales.MirrorCallKey(name), func() error { return a.api.Key(id, k) })
	}
	switch ev.Kind {
	case mg.InputPointerDown:
		switch ev.Button {
		case 0:
			m.down = true
			touch(driver.TouchDown, toDevice(ev.X, ev.Y))
		case 1:
			key(driver.KeyBack) // right click is Back
		case 2:
			key(driver.KeyHome) // middle click is Home
		}
		return true
	case mg.InputPointerMove:
		if m.down && ev.Button == 0 {
			if time.Since(m.lastMove) >= 8*time.Millisecond {
				m.lastMove = time.Now()
				touch(driver.TouchMove, toDevice(ev.X, ev.Y))
			}
			return true
		}
		return false
	case mg.InputPointerUp:
		if ev.Button == 0 && m.down {
			m.down = false
			touch(driver.TouchUp, toDevice(ev.X, ev.Y))
		}
		return true
	case mg.InputScroll:
		a.mirrorScroll(m, id, ev, toDevice, touch)
		return true
	case mg.InputKeyDown:
		if ev.Mods&mg.Cmd != 0 && ev.Key == mg.KeyV {
			if text := mygo.Clipboard.ReadText(); text != "" {
				a.call(id, locales.MirrorCallPaste(), func() error { return a.api.Text(id, text) })
			}
			return true
		}
		if ev.Mods&(mg.Cmd|mg.Ctrl|mg.Alt) != 0 && ev.Mods&mg.Shift == 0 && ev.Key != mg.KeyBackspace {
			return false // leave shortcuts to the app
		}
		if k, ok := driverKey(ev.Key); ok {
			key(k)
			return true
		}
		return false
	case mg.InputText:
		text := strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, ev.Text)
		m.compose = ""
		if text != "" {
			a.call(id, locales.MirrorCallType(), func() error { return a.api.Text(id, text) })
		}
		return true
	case mg.InputCompose:
		m.compose = ev.Text
		if a.main != nil {
			a.main.Invalidate()
		}
		return true
	}
	return false
}

// toDevice maps a point on the mirror, in DIPs from its corner, to the
// device's pixels.
func (m *mirror) toDevice(x, y float32) screen.Point {
	px := int(float64(x) / float64(m.boxW) * float64(m.picW))
	py := int(float64(y) / float64(m.boxH) * float64(m.picH))
	return screen.Point{X: clampInt(px, 0, m.picW-1), Y: clampInt(py, 0, m.picH-1)}
}

// mirrorScroll turns a wheel or touchpad scroll into a finger dragging the
// content: down where the pointer is, moving against the scroll, and up
// once the scrolling stops.
func (a *app) mirrorScroll(m *mirror, id string, ev mg.InputEvent, toDevice func(x, y float32) screen.Point, touch func(driver.TouchAction, screen.Point)) {
	sx := float64(m.picW) / float64(m.boxW)
	sy := float64(m.picH) / float64(m.boxH)
	dx, dy := -float64(ev.DX)*sx, -float64(ev.DY)*sy
	if !ev.Precise {
		dx, dy = dx*2.5, dy*2.5 // a wheel's notch moves the content further than a touchpad's pixels
	}
	if !m.scrolling {
		m.scrolling = true
		m.scrollAt = toDevice(ev.X, ev.Y)
		m.scrollPos = [2]float64{float64(m.scrollAt.X), float64(m.scrollAt.Y)}
		touch(driver.TouchDown, m.scrollAt)
	}
	nx, ny := m.scrollPos[0]+dx, m.scrollPos[1]+dy
	out := nx < 0 || ny < 0 || nx >= float64(m.picW) || ny >= float64(m.picH)
	if out {
		// The finger reached the edge: lift it and start again.
		last := screen.Point{X: clampInt(int(m.scrollPos[0]), 0, m.picW-1), Y: clampInt(int(m.scrollPos[1]), 0, m.picH-1)}
		touch(driver.TouchUp, last)
		touch(driver.TouchDown, m.scrollAt)
		nx, ny = float64(m.scrollAt.X)+dx, float64(m.scrollAt.Y)+dy
	}
	m.scrollPos = [2]float64{nx, ny}
	pos := screen.Point{X: clampInt(int(nx), 0, m.picW-1), Y: clampInt(int(ny), 0, m.picH-1)}
	touch(driver.TouchMove, pos)
	m.scrollSeq++
	seq := m.scrollSeq
	time.AfterFunc(140*time.Millisecond, func() {
		a.post(func() {
			if m.scrollSeq == seq && m.scrolling {
				m.scrolling = false
				touch(driver.TouchUp, pos)
			}
		})
	})
}

// driverKey maps the keys that type no text.
func driverKey(k mg.Key) (driver.Key, bool) {
	switch k {
	case mg.KeyEnter:
		return driver.KeyEnter, true
	case mg.KeyTab:
		return driver.KeyTab, true
	case mg.KeyBackspace:
		return driver.KeyDelete, true
	case mg.KeyDelete:
		return driver.KeyForwardDelete, true
	case mg.KeyEscape:
		return driver.KeyEscape, true
	case mg.KeyUp:
		return driver.KeyDpadUp, true
	case mg.KeyDown:
		return driver.KeyDpadDown, true
	case mg.KeyLeft:
		return driver.KeyDpadLeft, true
	case mg.KeyRight:
		return driver.KeyDpadRight, true
	case mg.KeyPageUp:
		return driver.KeyPageUp, true
	case mg.KeyPageDown:
		return driver.KeyPageDown, true
	}
	return "", false
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

// videoLine describes the decoder for Settings › Advanced.
func videoLine(a *app) string { return videoLineIn(locales.In(i18n.Locale()), a) }

// videoLineIn is videoLine in the language of m; feedback's diagnostics
// are English.
func videoLineIn(m locales.Messages, a *app) string {
	if a.videoErr != nil {
		return a.videoErr.Error()
	}
	line := video.Version()
	for _, mr := range a.mirrors {
		if mr.player == nil {
			continue
		}
		st := mr.player.Stats()
		dec := m.MirrorDecoderSoftware()
		if st.Hardware != "" {
			dec = st.Hardware
		}
		line = m.MirrorDecoderStats(line, mr.id, dec, fmt.Sprintf("%.0f", st.FPS), st.Latency.Round(100*time.Microsecond).String())
	}
	return line
}

// turnRGBA turns a picture turn quarter turns clockwise.
func turnRGBA(img *image.RGBA, turn int) *image.RGBA {
	turn &= 3
	if turn == 0 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	tw, th := w, h
	if turn%2 == 1 {
		tw, th = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := turnPoint(screen.Point{X: x, Y: y}, w, h, turn)
			si, di := img.PixOffset(img.Rect.Min.X+x, img.Rect.Min.Y+y), out.PixOffset(p.X, p.Y)
			copy(out.Pix[di:di+4], img.Pix[si:si+4])
		}
	}
	return out
}
