package ui

import (
	"context"
	"image"
	"os/exec"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/video"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// turnedStream marks every packet of a stream with a turn, as an iPhone or
// iPad on iOS 27 does when it is turned (driver.Packet.Turn).
type turnedStream struct {
	driver.Stream
	turn int
	out  chan driver.Packet
}

func turned(s driver.Stream, turn int) *turnedStream {
	t := &turnedStream{Stream: s, turn: turn, out: make(chan driver.Packet, 64)}
	go func() {
		defer close(t.out)
		for p := range s.Packets() {
			p.Turn = turn
			t.out <- p
		}
	}()
	return t
}

func (t *turnedStream) Packets() <-chan driver.Packet { return t.out }

// turnAPI is the fake with one stream, turned.
type turnAPI struct {
	*fakeapi.API
	turn int
	open []driver.Stream
}

func (s *turnAPI) Stream(ctx context.Context, id string, _ driver.StreamOptions) (driver.Stream, error) {
	st, err := video.StartTestStream(ctx, video.TestStreamOptions{Codec: driver.H264, Width: 1080, Height: 2400, FPS: 30, Source: "pal100bars"})
	if err != nil {
		return nil, err
	}
	s.open = append(s.open, st)
	return turned(st, s.turn), nil
}

// TestMirrorTurnsPicture plays a picture the phone sends a quarter turn
// from upright: the mirror is landscape, shows the picture turned the
// right way up, sends touches back in the picture's own pixels, and a
// screenshot is upright too.
func TestMirrorTurnsPicture(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	bars := barColors(t, "pal100bars")
	ta := newTestApp(t, fakeapi.OnePhone)
	sa := &turnAPI{API: ta.api, turn: 1}
	ta.app.api = sa
	ta.app.videoErr = nil
	t.Cleanup(func() {
		ta.closeMirrors()
		for _, s := range sa.open {
			s.Close()
		}
	})
	ta.sync()
	if err := ta.api.StopAll(); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the task to stop", func() bool { return !ta.anyRunning() })
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.frameUntil(t, tt, "the turned picture", func() bool {
		m := ta.mirrors["pixel-9"]
		return m != nil && m.state == "playing" && m.bitmap != nil && m.picW == 2400 && m.picH == 1080
	})
	m := ta.mirrors["pixel-9"]
	if m.turn != 1 {
		t.Fatalf("turn %d", m.turn)
	}
	// The layout follows the picture's new shape on the next build.
	ta.sync()
	tt.Frame()
	tt.Frame()
	rf, ok := tt.Find(locales.MirrorScreenLabel("Pixel 9"))
	if !ok {
		t.Fatalf("no mirror: %q", tt.Texts())
	}
	r := image.Rect(int(rf.X), int(rf.Y), int(rf.X+rf.W), int(rf.Y+rf.H))
	if got := float64(r.Dx()) / float64(r.Dy()); got < 2400.0/1080-0.05 || got > 2400.0/1080+0.05 {
		t.Fatalf("the mirror is %v, of aspect %.3f; want landscape 2.222", r, got)
	}
	// Turned back the other way, what the mirror shows is the stream's
	// picture: its bars run left to right again.
	shown := tt.Image().SubImage(r).(*image.RGBA)
	back := turnRGBA(shown, 3)
	if off := barsOff(back, back.Bounds(), bars); off > 16 {
		t.Errorf("the picture is not turned upright: bars %d levels off", off)
	}
	// The upright top-left corner is the stream picture's bottom-left.
	if p := m.toPicture(screen.Point{X: 0, Y: 0}); p != (screen.Point{X: 0, Y: 2399}) {
		t.Fatalf("top-left goes to %v", p)
	}
	if p := m.toPicture(screen.Point{X: 2399, Y: 1079}); p != (screen.Point{X: 1079, Y: 0}) {
		t.Fatalf("bottom-right goes to %v", p)
	}
	pic, turn := m.lastPicture()
	if pic == nil || turn != 1 {
		t.Fatalf("screenshot %v, turn %d", pic, turn)
	}
	if b := turnRGBA(pic.RGBA(), turn).Bounds(); b != image.Rect(0, 0, 2400, 1080) {
		t.Fatalf("an upright screenshot of %v", b)
	}
}

// turnRecAPI plays the fake's recordings marked with a quarter turn, as a
// recording of a turned iPhone or iPad plays back (recording.Open).
type turnRecAPI struct{ *fakeapi.API }

func (a turnRecAPI) OpenRecording(ctx context.Context, rec gateway.RecordingView, from time.Duration) (driver.Stream, error) {
	s, err := a.API.OpenRecording(ctx, rec, from)
	if err != nil {
		return nil, err
	}
	return turned(s, 1), nil
}

// TestRecordingTurnsPicture plays a recording of a turned phone: the
// player takes the upright shape and shows the picture turned.
func TestRecordingTurnsPicture(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.app.api = turnRecAPI{ta.api}
	ta.page = pageActivity
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "Activity", func() bool { tt.Frame(); return ta.act.loaded })
	ta.act.sel = "h-lunch"
	ta.waitFor(t, "the recording", func() bool { tt.Frame(); return hasText(tt, "Watch recording") })
	if err := tt.Click("Watch recording"); err != nil {
		t.Fatal(err)
	}
	pl := ta.rec.pl
	t.Cleanup(ta.closeRecording)
	ta.waitFor(t, "the turned picture", func() bool {
		tt.Frame()
		return pl.bitmap != nil && pl.picW == 1200 && pl.picH == 540
	})
	if pl.turn != 1 {
		t.Fatalf("turn %d", pl.turn)
	}
	tt.Frame()
	r, ok := tt.Find("Recording of Reorder last week's lunch")
	if !ok {
		t.Fatal("no player")
	}
	if r.W <= r.H {
		t.Fatalf("the player is %vx%v, not landscape", r.W, r.H)
	}
}
