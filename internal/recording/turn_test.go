package recording

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// TestRecordTurns records a phone sending its picture a quarter turn from
// upright, then turned back halfway through: the file says the first turn
// in its track (which other players honour), playback carries every
// picture's turn, and FFmpeg still reads all of it.
func TestRecordTurns(t *testing.T) {
	pkts := packets(t, "testsrc.h264", driver.H264)
	frame := 0
	for i := range pkts {
		pkts[i].Turn = 1
		if frame >= 15 {
			pkts[i].Turn = 0 // turned back in the middle of a fragment
		}
		if !pkts[i].Config {
			frame++
		}
	}
	path := write(t, t.TempDir(), driver.H264, pkts, 10)
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Turn != 1 || info.Frames != 30 || info.Width != 96 || info.Height != 64 {
		t.Fatalf("info %+v", info)
	}
	s, err := Open(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := collect(t, s, 10*time.Second)
	if !got[0].Config || got[0].Turn != 1 {
		t.Fatalf("config packet turn %d", got[0].Turn)
	}
	for i, p := range got[1:] {
		want := 1
		if i >= 15 {
			want = 0
		}
		if p.Turn != want {
			t.Fatalf("picture %d: turn %d, want %d", i, p.Turn, want)
		}
	}
	// From the middle: the key frame at 2 s is already turned back.
	s2, err := Open(context.Background(), path, 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if mid := collect(t, s2, 5*time.Second); mid[0].Turn != 0 || mid[1].Turn != 0 {
		t.Fatalf("from the middle: turns %d %d", mid[0].Turn, mid[1].Turn)
	}
	ffprobe(t, path, driver.H264, 30)
	if bin, err := exec.LookPath("ffprobe"); err == nil {
		out, err := exec.Command(bin, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream_side_data=rotation", "-of", "csv=p=0", path).CombinedOutput()
		// FFmpeg names a quarter turn clockwise -90.
		if err != nil || strings.TrimSpace(string(out)) != "-90" {
			t.Errorf("ffprobe's rotation: %q %v", out, err)
		}
	}
}

func TestTurnMatrix(t *testing.T) {
	for q := 0; q < 4; q++ {
		if got := matrixTurn(turnMatrix(q, 96, 64)); got != q {
			t.Errorf("turn %d reads back as %d", q, got)
		}
	}
	// A file without a matrix, or with one that is no quarter turn, is
	// upright.
	if matrixTurn([9]int32{}) != 0 || matrixTurn([9]int32{0x10000, 0x8000, 0, 0, 0x10000, 0, 0, 0, 0x40000000}) != 0 {
		t.Fatal("not a quarter turn")
	}
	// Recordings made before turns were kept play upright, as before.
	path := write(t, t.TempDir(), driver.H264, packets(t, "testsrc.h264", driver.H264), 10)
	if info, err := Probe(path); err != nil || info.Turn != 0 {
		t.Fatalf("an upright recording: %+v %v", info, err)
	}
}
