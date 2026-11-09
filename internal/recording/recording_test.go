package recording

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/recording/recordingtest"
	"github.com/DIYgod/pinky/internal/video"
)

// The test streams are ffmpeg test patterns (recordingtest): 30 frames at
// 10 frames a second with a key frame every second, at 96x64.

// packets returns a test stream cut into packets as drivers send them.
func packets(t *testing.T, name string, codec driver.Codec) []driver.Packet {
	t.Helper()
	data := map[string]func() []byte{"testsrc.h264": recordingtest.H264, "testsrc.hevc": recordingtest.HEVC,
		"rotated.h264": recordingtest.Rotated}[name]
	if data == nil {
		t.Fatalf("no test stream %s", name)
	}
	return recordingtest.Packets(codec, data())
}

// write records pkts at fps frames a second and returns the file.
func write(t *testing.T, dir string, codec driver.Codec, pkts []driver.Packet, fps int) string {
	t.Helper()
	path := filepath.Join(dir, "t.mp4")
	w, err := Create(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	frame := 0
	for _, p := range pkts {
		if err := w.Write(p, time.Duration(frame)*time.Second/time.Duration(fps)); err != nil {
			t.Fatal(err)
		}
		if !p.Config {
			frame++
		}
	}
	if err := w.Close(time.Duration(frame) * time.Second / time.Duration(fps)); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecordAndProbe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		codec driver.Codec
	}{{"testsrc.h264", driver.H264}, {"testsrc.hevc", driver.HEVC}} {
		t.Run(tc.name, func(t *testing.T) {
			pkts := packets(t, tc.name, tc.codec)
			path := write(t, t.TempDir(), tc.codec, pkts, 10)
			info, err := Probe(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Codec != tc.codec || info.Width != 96 || info.Height != 64 || info.Frames != 30 {
				t.Fatalf("info %+v", info)
			}
			if d := info.Length - 3*time.Second; d < -time.Millisecond || d > time.Millisecond {
				t.Errorf("length %v, want 3 s", info.Length)
			}
			ffprobe(t, path, tc.codec, 30)
		})
	}
}

// ffprobe checks the file with FFmpeg's own reader and decoder, when they
// are installed.
func ffprobe(t *testing.T, path string, codec driver.Codec, frames int) {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Log("ffprobe not installed; skipping its check")
		return
	}
	out, err := exec.Command(bin, "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, out)
	}
	name := "h264"
	if codec == driver.HEVC {
		name = "hevc"
	}
	want := name + ",96,64," + strconv.Itoa(frames)
	// A turned recording's side data adds an empty field at the end.
	if got := strings.TrimRight(strings.TrimSpace(string(out)), ","); got != want {
		t.Errorf("ffprobe: %q, want %q", got, want)
	}
}

func TestNothingBeforeTheFirstKeyFrame(t *testing.T) {
	pkts := packets(t, "testsrc.h264", driver.H264)
	dir := t.TempDir()
	path := filepath.Join(dir, "t.mp4")
	w, err := Create(path, driver.H264)
	if err != nil {
		t.Fatal(err)
	}
	// Pictures without a configuration first, then a few after it that
	// are not key frames: none can be decoded, so no file yet.
	for i, p := range pkts[1:5] {
		if err := w.Write(p, time.Duration(i)*100*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	if w.Started() {
		t.Fatal("started without a configuration")
	}
	if err := w.Close(time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a recording without a key frame left a file: %v", err)
	}
}

func TestCutShortStillPlays(t *testing.T) {
	pkts := packets(t, "testsrc.h264", driver.H264)
	path := write(t, t.TempDir(), driver.H264, pkts, 10)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A crash in the middle of the last fragment.
	if err := os.WriteFile(path, data[:len(data)-200], 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames == 0 || info.Frames >= 30 {
		t.Fatalf("frames %d after the cut, want some but not all", info.Frames)
	}
	// FFmpeg opens it too; it also decodes what it can of the cut fragment.
	if bin, err := exec.LookPath("ffprobe"); err == nil {
		out, err := exec.Command(bin, "-v", "quiet", "-count_frames", "-select_streams", "v:0",
			"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
		n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil || n < info.Frames {
			t.Errorf("ffprobe on the cut file: %v, %q", err, out)
		}
	}
}

func collect(t *testing.T, s driver.Stream, max time.Duration) []driver.Packet {
	t.Helper()
	var out []driver.Packet
	deadline := time.After(max)
	for {
		select {
		case p, ok := <-s.Packets():
			if !ok {
				if err := s.Err(); err != nil {
					t.Fatal(err)
				}
				return out
			}
			out = append(out, p)
		case <-deadline:
			t.Fatalf("playback did not end in %v", max)
		}
	}
}

func TestPlayback(t *testing.T) {
	pkts := packets(t, "testsrc.h264", driver.H264)
	path := write(t, t.TempDir(), driver.H264, pkts, 10)

	t.Run("from the start", func(t *testing.T) {
		s, err := Open(context.Background(), path, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.Codec() != driver.H264 {
			t.Fatalf("codec %s", s.Codec())
		}
		if w, h := s.Size(); w != 96 || h != 64 {
			t.Fatalf("size %dx%d", w, h)
		}
		begin := time.Now()
		got := collect(t, s, 10*time.Second)
		took := time.Since(begin)
		if len(got) != 31 || !got[0].Config || !got[1].KeyFrame {
			t.Fatalf("%d packets; first config %v, then key frame %v", len(got), got[0].Config, len(got) > 1 && got[1].KeyFrame)
		}
		// The original pictures come back unchanged, AUDs aside.
		orig := pictures(pkts)
		for i, p := range got[1:] {
			if !bytes.Equal(p.Data, orig[i].Data) || p.KeyFrame != orig[i].KeyFrame {
				t.Fatalf("picture %d differs", i)
			}
			if want := time.Duration(i) * 100 * time.Millisecond; absDur(p.PTS-want) > time.Millisecond {
				t.Fatalf("picture %d at %v, want %v", i, p.PTS, want)
			}
		}
		// Paced: the last picture is 2.9 s in.
		if took < 2700*time.Millisecond || took > 4*time.Second {
			t.Errorf("playback took %v, want about 2.9 s", took)
		}
	})

	t.Run("from the middle", func(t *testing.T) {
		s, err := Open(context.Background(), path, 2500*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		begin := time.Now()
		got := collect(t, s, 5*time.Second)
		took := time.Since(begin)
		// From the key frame at 2 s: frames 20 to 29, the first six at
		// once, then four at the recorded pace.
		if len(got) != 11 || !got[0].Config || !got[1].KeyFrame || got[1].PTS != 2*time.Second {
			t.Fatalf("%d packets, first picture at %v", len(got), got[1].PTS)
		}
		if took < 300*time.Millisecond || took > 1500*time.Millisecond {
			t.Errorf("playback took %v, want about 0.4 s", took)
		}
	})

	t.Run("close stops it", func(t *testing.T) {
		s, err := Open(context.Background(), path, 0)
		if err != nil {
			t.Fatal(err)
		}
		<-s.Packets()
		done := make(chan struct{})
		go func() { s.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not stop playback")
		}
	})
}

func pictures(pkts []driver.Packet) []driver.Packet {
	var out []driver.Packet
	for _, p := range pkts {
		if !p.Config {
			// Drop the AUDs the test streams carry, as the recording does.
			var nals [][]byte
			for _, n := range video.SplitNALs(p.Data) {
				if !isAUD(driver.H264, video.NALType(driver.H264, n)) {
					nals = append(nals, n)
				}
			}
			out = append(out, driver.Packet{Data: annexB(nals), KeyFrame: p.KeyFrame})
		}
	}
	return out
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// A rotation sends new parameter sets: they reach the player before the
// next key frame.
func TestParameterSetsChange(t *testing.T) {
	first := packets(t, "testsrc.h264", driver.H264)[:12] // config and 11 pictures
	second := packets(t, "rotated.h264", driver.H264)
	path := write(t, t.TempDir(), driver.H264, append(append([]driver.Packet(nil), first...), second...), 10)
	s, err := Open(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := collect(t, s, 10*time.Second)
	var configs []int
	for i, p := range got {
		if p.Config {
			configs = append(configs, i)
		}
	}
	if len(configs) != 2 || configs[0] != 0 || !got[configs[1]+1].KeyFrame {
		t.Fatalf("configuration packets at %v of %d", configs, len(got))
	}
	if !bytes.Equal(got[configs[1]].Data, second[0].Data) {
		t.Fatal("the second configuration differs from the stream's")
	}
}

// A live stream from ffmpeg, as the demo phones send, with the Mi 10 Pro's
// YCgCo tag, recorded for a second.
func TestRecordLiveStream(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := video.StartTestStream(ctx, video.TestStreamOptions{Width: 320, Height: 640, FPS: 30, Matrix: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	path := filepath.Join(t.TempDir(), "live.mp4")
	w, err := Create(path, src.Codec())
	if err != nil {
		t.Fatal(err)
	}
	// A second and a half from the first packet: ffmpeg starting up, slow
	// on a busy machine, does not count.
	var begin time.Time
	for p := range src.Packets() {
		if begin.IsZero() {
			begin = time.Now()
		}
		if err := w.Write(p, time.Since(begin)); err != nil {
			t.Fatal(err)
		}
		if time.Since(begin) > 1500*time.Millisecond {
			break
		}
	}
	if err := w.Close(time.Since(begin)); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 640 || info.Frames < 20 {
		t.Fatalf("info %+v", info)
	}
}
