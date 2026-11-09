package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/recording"
	"github.com/DIYgod/pinky/internal/video"
)

// Recordings: the gateway keeps a task's screen as an MP4 file with a mark
// per step. Activity plays one in the mirror's view, with the same decoder,
// from recording.Open (or a fake's OpenRecording), and jumps to a step by
// opening the recording again at its mark.

// recState is the recordings' part of the client.
type recState struct {
	views map[string]*recEntry // by task id
	pl    *recPlayer           // the player Activity shows
}

type recEntry struct {
	rec     gateway.RecordingView
	err     error
	loading bool
	at      time.Time // when the gateway answered
}

// recordingOf returns a task's recording, looking it up in the background
// the first time; nil while it loads or when the task has none. A task
// without one is asked again after a while: the gateway may still be
// writing the file of a task that just ended.
func (a *app) recordingOf(taskID string) *gateway.RecordingView {
	rf, ok := a.api.(recordingFinder)
	if !ok || taskID == "" {
		return nil
	}
	if e, ok := a.rec.views[taskID]; ok && (e.loading || e.err == nil || time.Since(e.at) < 20*time.Second) {
		if e.loading || e.err != nil {
			return nil
		}
		return &e.rec
	}
	e := &recEntry{loading: true}
	a.rec.views[taskID] = e
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()
		rec, err := rf.Recording(ctx, taskID)
		a.post(func() {
			e.loading, e.rec, e.err, e.at = false, rec, err, time.Now()
		})
	}()
	return nil
}

// recPlayer plays a recording. Its fields are used on the main thread.
type recPlayer struct {
	a      *app
	task   string
	rec    gateway.RecordingView
	gen    int
	cancel context.CancelFunc
	player *video.Player
	bitmap *mg.Bitmap
	picW   int // the picture's size the right way up
	picH   int
	turn   int    // quarter turns clockwise that put the picture upright
	state  string // "loading", "playing", "paused", "ended", "failed"
	err    error
	pos    time.Duration // where the picture shown is, from the recording's start
	from   time.Duration // where the stream was opened
	base   time.Duration // added to the stream's PTS, when they count from where it opened
	first  bool          // the next picture is the stream's first
}

// openRecording starts playing a task's recording from its start.
func (a *app) openRecording(taskID string, rec gateway.RecordingView) {
	a.closeRecording()
	pl := &recPlayer{a: a, task: taskID, rec: rec}
	a.rec.pl = pl
	pl.play(0)
}

func (a *app) closeRecording() {
	if pl := a.rec.pl; pl != nil {
		pl.stop()
		a.rec.pl = nil
	}
}

// stop ends the stream, keeping the picture.
func (pl *recPlayer) stop() {
	pl.gen++
	if pl.cancel != nil {
		pl.cancel()
		pl.cancel = nil
	}
	if p := pl.player; p != nil {
		pl.player = nil
		go p.Close()
	}
}

// play opens the recording at from; the picture stays until the first new
// one, so a jump does not flash.
func (pl *recPlayer) play(from time.Duration) {
	pl.stop()
	gen := pl.gen
	ctx, cancel := context.WithCancel(pl.a.ctx)
	pl.cancel = cancel
	pl.from, pl.pos, pl.state, pl.err, pl.first = from, from, "loading", nil, true
	a, rec := pl.a, pl.rec
	go func() {
		var s driver.Stream
		var err error
		if rp, ok := a.api.(recordingPlayer); ok {
			s, err = rp.OpenRecording(ctx, rec, from)
		} else {
			s, err = recording.Open(ctx, rec.Path, from)
		}
		var p *video.Player
		if err == nil {
			if p, err = video.NewPlayer(s, video.PlayerOptions{YUV: true}); err != nil {
				s.Close()
			}
		}
		a.post(func() {
			if pl.gen != gen || a.rec.pl != pl {
				if p != nil {
					go p.Close()
				}
				return
			}
			if err != nil {
				pl.state, pl.err = "failed", err
				return
			}
			pl.player, pl.state = p, "playing"
			go func() {
				<-p.Done()
				a.post(func() {
					if pl.player != p {
						return
					}
					pl.player = nil
					go p.Close()
					if e := p.Err(); e != nil && !errors.Is(e, io.EOF) && !errors.Is(e, context.Canceled) {
						pl.state, pl.err = "failed", e
						return
					}
					pl.state, pl.pos = "ended", pl.rec.Length
				})
			}()
		})
	}()
}

func (pl *recPlayer) pause() {
	if pl.state == "playing" || pl.state == "loading" {
		pl.stop()
		pl.state = "paused"
	}
}

// resume plays on from the picture shown, or from the start once it ended.
func (pl *recPlayer) resume() {
	from := pl.pos
	if pl.state == "ended" || from >= pl.rec.Length {
		from = 0
	}
	pl.play(from)
}

func (pl *recPlayer) seek(at time.Duration) {
	pl.play(max(0, min(at, pl.rec.Length)))
}

// take shows the newest picture, at paint time.
func (pl *recPlayer) take(pt *mg.Painter) {
	p := pl.player
	if p == nil {
		return
	}
	if f := p.Take(); f != nil {
		pl.bitmap = showPlanes(pl.bitmap, f.YUV)
		w, h := f.Width, f.Height
		if f.Turn%2 == 1 {
			w, h = h, w
		}
		if pl.picW > 0 && (w != pl.picW || h != pl.picH) {
			pl.a.post(func() {}) // the phone turned: lay the player out again
		}
		pl.picW, pl.picH, pl.turn = w, h, f.Turn
		if pl.first {
			// PTS count from the recording's start, as recording.Open
			// sends them; a stream counting from where it opened starts
			// near zero instead, well before from.
			pl.first, pl.base = false, 0
			if pl.from > 10*time.Second && f.PTS < pl.from-10*time.Second {
				pl.base = pl.from - f.PTS
			}
		}
		pl.pos = max(0, min(pl.base+f.PTS, pl.rec.Length))
	}
	pt.AnimationFrameRate(60)
}

// markAt returns the index of the last mark at or before at, or -1.
func (pl *recPlayer) markAt(at time.Duration) int {
	idx := -1
	for i, m := range pl.rec.Marks {
		if m.At <= at {
			idx = i
		}
	}
	return idx
}

// recordingView is the recording player: the phone's screen as the mirror
// shows it, Play and Pause, the time, and a timeline with a mark per step.
func (a *app) recordingView(c *mg.Context, t gateway.TaskView, pl *recPlayer) {
	p := pal(c)
	if pl.state == "playing" || pl.state == "loading" {
		c.After(250 * time.Millisecond) // the time and the current step move on
	}
	mg.Row(c).Gap(20).AlignItems(mg.Start).Wrap().Children(func() {
		h := float32(440)
		aspect := float32(9.0 / 19.5)
		if pl.picW > 0 && pl.picH > 0 {
			aspect = float32(pl.picW) / float32(pl.picH)
		}
		const bezel = 7
		w := (h-2*bezel)*aspect + 2*bezel
		mg.Box(c).Size(w, h).Shrink(0).Radius(34).Background(p.phone).Padding(bezel).Label(locales.RecordingPlayerLabel(t.Title)).
			Children(func() {
				scr := mg.Box(c).Fill().Radius(28).Clip().Background(hex("#000000"))
				scr.Draw(func(pt *mg.Painter, r mg.Rect) {
					pl.take(pt)
					if pl.bitmap != nil {
						pt.Clip(r, 28, func() { drawTurned(pt, pl.bitmap, r, pl.turn) })
					}
					// What the step at this moment acted on, for a moment after
					// its mark.
					if i := pl.markAt(pl.pos); i >= 0 && pl.picW > 0 {
						mk := pl.rec.Marks[i]
						if pl.pos-mk.At < 2*time.Second && mk.Step >= 0 && mk.Step < len(t.Steps) {
							if s := t.Steps[mk.Step]; !s.Target.Empty() {
								pt.Clip(r, 28, func() {
									drawOverlay(pt, r, p, s, stepDone, float32(pl.picW), float32(pl.picH), false)
								})
							}
						}
					}
				})
				scr.Children(func() {
					switch {
					case pl.state == "failed":
						mg.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(8).Padding(20).Children(func() {
							mg.Icon(c, icAlert).FontSize(22).TextColor(hex("#f7a74e"))
							mg.Text(c, recordingError(pl.err)).FontSize(12.5).TextColor(hex("#d0d0d8")).TextAlign(mg.Center).MaxWidth(220)
						})
					case pl.bitmap == nil:
						mg.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Children(func() { spinner(c) })
					}
				})
			})
		mg.Column(c).Basis(220).Grow(1).MinWidth(0).Gap(4).Label(locales.RecordingStepsLabel()).Children(func() {
			cur := pl.markAt(pl.pos)
			mg.Text(c, locales.RecordingJumpHeading()).FontSize(12.5).FontWeight(600).TextColor(p.ink3).Margin(0, 0, 4, 0)
			for i, mk := range pl.rec.Marks {
				i, mk := i, mk
				text := stepText(mk.Text)
				if mk.Step >= 0 && mk.Step < len(t.Steps) {
					text, _, _ = stepDisplay(t, mk.Step)
				}
				row := mg.ButtonBase(c.Key(i)).Padding(6, 10).Radius(8).Gap(10).AlignItems(mg.Center).Justify(mg.Start).FillWidth().
					Label(locales.RecordingJumpTo(clock(mk.At), text)).TextColor(p.ink)
				switch {
				case i == cur:
					row.Background(p.bg3)
					selectionMark(c, row)
				case row.Hovered():
					row.Background(p.bg3.Mix(p.bg2, 0.5))
				}
				if row.Clicked() {
					pl.seek(mk.At)
				}
				row.Children(func() {
					mg.Text(c, clock(mk.At)).FontSize(12.5).TextColor(p.ink3).Width(40).Shrink(0)
					tx := mg.Text(c, text).FontSize(13.5).SingleLine().Grow(1).MinWidth(0)
					if i == cur {
						tx.FontWeight(600)
					}
				})
			}
			if len(pl.rec.Marks) == 0 {
				mg.Text(c, locales.RecordingNoMarks()).FontSize(13).TextColor(p.ink2)
			}
		})
	})
	// Play, the timeline and the time.
	mg.Row(c).Gap(12).AlignItems(mg.Center).Children(func() {
		label, icon := locales.RecordingPause(), icPause
		switch pl.state {
		case "paused", "failed":
			label, icon = locales.RecordingPlay(), icPlay
		case "ended":
			label, icon = locales.RecordingPlayAgain(), icPlay
		}
		b := button(c, "", btnOpt{icon: icon, label: label}).Width(34).Padding(0).Tooltip(label)
		if b.Clicked() {
			if label == locales.RecordingPause() {
				pl.pause()
			} else {
				pl.resume()
			}
		}
		a.recordingTimeline(c, t, pl).Grow(1).MinWidth(0)
		mg.Text(c, clock(pl.pos)+" / "+clock(pl.rec.Length)).FontSize(13).TextColor(p.ink2).Shrink(0)
	})
}

// recordingTimeline is the recording's length with a mark per step, larger
// where the person was asked; a click jumps there.
func (a *app) recordingTimeline(c *mg.Context, t gateway.TaskView, pl *recPlayer) mg.Element {
	p := pal(c)
	length := max(pl.rec.Length, time.Millisecond)
	bar := mg.Box(c).Height(30).Focusable().Role(mg.RoleSlider).Label(locales.RecordingPosition()).Cursor(mg.CursorPointer)
	var width float32
	bar.HandleInput(func(ev mg.InputEvent) bool {
		switch ev.Kind {
		case mg.InputPointerDown:
			if ev.Button == 0 && width > 0 {
				pl.seek(time.Duration(float64(length) * float64(max(0, min(ev.X/width, 1)))))
				return true
			}
		case mg.InputKeyDown:
			switch ev.Key {
			case mg.KeyLeft:
				pl.seek(pl.pos - 5*time.Second)
				return true
			case mg.KeyRight:
				pl.seek(pl.pos + 5*time.Second)
				return true
			}
		}
		return false
	})
	bar.Draw(func(pt *mg.Painter, r mg.Rect) {
		width = r.W
		y := r.Y + r.H/2
		pt.Fill(mg.Rect{X: r.X, Y: y - 2, W: r.W, H: 4}, p.bg3, 2)
		done := r.W * float32(float64(pl.pos)/float64(length))
		pt.Fill(mg.Rect{X: r.X, Y: y - 2, W: done, H: 4}, p.accent, 2)
		for _, mk := range pl.rec.Marks {
			x := r.X + r.W*float32(float64(mk.At)/float64(length))
			size, col := float32(8), p.ink3
			if mk.Step >= 0 && mk.Step < len(t.Steps) && kindOf(t, mk.Step) == stepYou {
				size, col = 12, p.ink
			}
			var ring, d mg.Path
			ring.Circle(x, y, size/2+2)
			pt.FillPath(&ring, p.bg2)
			d.Circle(x, y, size/2)
			pt.FillPath(&d, col)
		}
		var head mg.Path
		head.Circle(r.X+done, y, 7)
		pt.FillPath(&head, p.accentBtn)
	})
	return bar
}

func recordingError(err error) string {
	switch {
	case err == nil:
		return locales.RecordingEnded()
	case errors.Is(err, driver.ErrUnsupported):
		return locales.RecordingUnsupported()
	case errors.Is(err, driver.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return locales.RecordingGone()
	case errors.Is(err, video.ErrNotFound):
		return err.Error()
	}
	return locales.RecordingPlayFailed(err.Error())
}

// --- export ---

// exportFolder is where exports go: the Desktop, else the home folder.
func exportFolder() (string, error) {
	dir, err := mygo.App.Path(mygo.PathDesktop)
	if err != nil || dir == "" {
		return os.UserHomeDir()
	}
	return dir, nil
}

func revealFile(path string) { mygo.Shell.ShowItemInFolder(path) }

// exportHistory writes tasks (all finished ones when ids is empty) into a
// new folder on the Desktop as an HTML report and JSON, through the
// gateway when it exports, and shows the report in the Finder.
func (a *app) exportHistory(ids []string, name string) {
	exp, gw := a.api.(historyExporter)
	var known []gateway.TaskView
	for _, t := range a.act.tasks {
		for _, id := range ids {
			if t.ID == id {
				known = append(known, t)
			}
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
		defer cancel()
		base, err := a.exportTo()
		var dir, path string
		if err == nil {
			now := time.Now()
			dir = filepath.Join(base, freeName(base, locales.ExportFolderName(fileName(name), now.Format("2006-01-02"), now.Format("15.04.05"))))
			err = os.MkdirAll(dir, 0o755)
		}
		if err == nil && gw {
			path, err = exp.ExportHistory(ctx, ids, dir)
		}
		if (err == nil && !gw) || errors.Is(err, driver.ErrUnsupported) {
			path, err = a.exportHere(ctx, ids, known, dir)
		}
		a.post(func() {
			if err != nil {
				a.toasts = append(a.toasts, locales.ExportFailed(err.Error()))
				return
			}
			a.toasts = append(a.toasts, locales.ExportDone(filepath.Base(dir), filepath.Base(base)))
			if a.reveal != nil {
				a.reveal(path)
			}
		})
	}()
}

// exportHere writes the report itself, for a gateway that does not.
func (a *app) exportHere(ctx context.Context, ids []string, known []gateway.TaskView, dir string) (string, error) {
	tasks := known
	if len(ids) == 0 || len(known) < len(ids) {
		all, err := a.api.History(ctx, gateway.HistoryQuery{})
		if err != nil {
			return "", err
		}
		tasks = all
		if len(ids) > 0 {
			want := map[string]bool{}
			for _, id := range ids {
				want[id] = true
			}
			tasks = tasks[:0:0]
			for _, t := range all {
				if want[t.ID] {
					tasks = append(tasks, t)
				}
			}
		}
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.json"), append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "report.html")
	return path, os.WriteFile(path, []byte(reportHTML(tasks)), 0o644)
}

// fileName keeps what a file name may hold.
func fileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return '-'
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return strings.TrimSpace(s)
}

// freeName returns name, or name with a number, so it names nothing in dir
// yet. A folder it cannot look into gets name, for MkdirAll to report.
func freeName(dir, name string) string {
	try := name
	for i := 2; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(dir, try)); err != nil {
			return try
		}
		try = fmt.Sprintf("%s %d", name, i)
	}
	return try
}
