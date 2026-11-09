package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// streamAPI is the fake with the streams a test picks, one per time the
// client opens one.
type streamAPI struct {
	*fakeapi.API
	mu   sync.Mutex
	next []video.TestStreamOptions
	open []*video.TestStream
}

func (s *streamAPI) Stream(ctx context.Context, id string, _ driver.StreamOptions) (driver.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.next) == 0 {
		return nil, fmt.Errorf("no more streams of %s: %w", id, driver.ErrUnsupported)
	}
	o := s.next[0]
	s.next = s.next[1:]
	st, err := video.StartTestStream(ctx, o)
	if err != nil {
		return nil, err
	}
	s.open = append(s.open, st)
	return st, nil
}

// barColors returns the colors of an ffmpeg bars source's eight bars, as
// ffmpeg makes them in RGB.
func barColors(t *testing.T, source string) [8][3]int {
	t.Helper()
	const w = 160 // bars of 20 pixels, so that chroma never blends two
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi",
		"-i", fmt.Sprintf("%s=size=%dx2:rate=1", source, w), "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil || len(out) < 3*w {
		t.Fatalf("ffmpeg's %s: %v", source, err)
	}
	var bars [8][3]int
	for i := range bars {
		p := out[3*(20*i+10):]
		bars[i] = [3]int{int(p[0]), int(p[1]), int(p[2])}
	}
	return bars
}

// barsOff returns how far, in levels, the colors in the middle of the
// eight bars of a picture drawn in r are from want: the largest
// difference in R, G or B.
func barsOff(img *image.RGBA, r image.Rectangle, want [8][3]int) int {
	worst := 0
	for i, c := range want {
		x := r.Min.X + (2*i+1)*r.Dx()/16
		y := r.Min.Y + r.Dy()*3/10
		got := img.RGBAAt(x, y)
		for k, v := range []uint8{got.R, got.G, got.B} {
			worst = max(worst, int(v)-c[k], c[k]-int(v))
		}
	}
	return worst
}

// TestMirrorShowsPlanes plays a phone through the mirror as the decoder's
// planes: a USB stream of H.264 at 30 frames a second, then, as when the
// phone moves to Wi-Fi or Tailscale and its stream opens again, HEVC at
// another size, 15 frames a second and 1 Mbit/s, as Android phones send
// through Tailscale. Each shows in its colors at its size, keeps showing
// between the slower stream's pictures, and a screenshot has the
// stream's full size.
func TestMirrorShowsPlanes(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	away := driver.HEVC
	if out, _ := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output(); runtime.GOOS != "darwin" && !bytes.Contains(out, []byte("libx265")) {
		away = driver.H264 // no HEVC encoder for the test stream
	}
	usb, wifi := barColors(t, "pal100bars"), barColors(t, "pal75bars")
	apart := 0
	for i := range usb {
		for k := range 3 {
			apart = max(apart, usb[i][k]-wifi[i][k], wifi[i][k]-usb[i][k])
		}
	}
	if apart < 48 {
		t.Fatalf("the two sources' bars are only %d levels apart", apart)
	}

	ta := newTestApp(t, fakeapi.OnePhone)
	sa := &streamAPI{API: ta.api, next: []video.TestStreamOptions{
		{Codec: driver.H264, Width: 1080, Height: 2400, FPS: 30, Source: "pal100bars"},
		{Codec: away, Width: 590, Height: 1278, FPS: 15, Bitrate: 1_000_000, Source: "pal75bars"},
	}}
	ta.app.api = sa
	ta.app.videoErr = nil
	t.Cleanup(ta.closeMirrors)
	ta.sync()
	if err := ta.api.StopAll(); err != nil { // no step outlined on the mirror
		t.Fatal(err)
	}
	ta.waitFor(t, "the task to stop", func() bool { return !ta.anyRunning() })
	tt := mg.NewTester(ta.view, 1280, 820)
	screenOf := func() image.Rectangle {
		r, ok := tt.Find(locales.MirrorScreenLabel("Pixel 9"))
		if !ok {
			t.Fatalf("no mirror: %q", tt.Texts())
		}
		return image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
	}
	playing := func(w, h int) func() bool {
		return func() bool {
			m := ta.mirrors["pixel-9"]
			return m != nil && m.state == "playing" && m.bitmap != nil && m.picW == w && m.picH == h
		}
	}

	ta.frameUntil(t, tt, "the USB stream's picture", playing(1080, 2400))
	if off := barsOff(tt.Image(), screenOf(), usb); off > 12 {
		t.Errorf("the USB stream's bars show %d levels off", off)
	}
	m := ta.mirrors["pixel-9"]
	if f := m.frame; f == nil || f.YUV == nil || f.Image != nil {
		t.Fatalf("the mirror shows %+v, not the decoder's planes", f)
	}

	// The phone moves to Wi-Fi: its stream ends, and the mirror opens it
	// again once it may.
	sa.mu.Lock()
	sa.open[0].Close()
	sa.mu.Unlock()
	ta.frameUntil(t, tt, "the stream's end", func() bool { return m.state == "failed" })
	m.retry = time.Now().Add(-time.Second)
	ta.frameUntil(t, tt, "the slower stream's picture", playing(590, 1278))
	r := screenOf()
	if off := barsOff(tt.Image(), r, wifi); off > 12 {
		t.Errorf("the slower stream's bars show %d levels off", off)
	}
	if got, want := float64(r.Dx())/float64(r.Dy()), 590.0/1278; got < want-0.01 || got > want+0.01 {
		t.Errorf("the mirror is %v, of aspect %.3f; want the stream's %.3f", r, got, want)
	}
	// Between the stream's pictures, painting takes none and keeps the
	// last one.
	for range 5 {
		tt.Frame()
	}
	if off := barsOff(tt.Image(), r, wifi); off > 12 {
		t.Errorf("between pictures, the bars show %d levels off", off)
	}

	pic, _ := m.lastPicture()
	if pic == nil {
		t.Fatal("no picture for a screenshot")
	}
	shot := pic.RGBA()
	if b := shot.Bounds(); b != image.Rect(0, 0, 590, 1278) {
		t.Fatalf("a screenshot of %v, want the stream's 590x1278", b)
	}
	if off := barsOff(shot, shot.Bounds(), wifi); off > 12 {
		t.Errorf("the screenshot's bars are %d levels off", off)
	}
}
