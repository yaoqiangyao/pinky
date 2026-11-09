//go:build device

package android

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// TestDeviceStreamRecoversFromStalledReset stops the session's server with
// SIGSTOP, standing in for an encoder that never answers a reset: the
// stream must end within resetStall and a new one show pictures. It only
// reads the main display, and leaves other servers (Pinky's) alone.
//
//	PINKY_DEVICE=<serial> go test -tags device -run TestDeviceStreamRecoversFromStalledReset -v ./internal/driver/android/
func TestDeviceStreamRecoversFromStalledReset(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	d, err := NewProvider(Config{Logf: t.Logf}).OpenDevice(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if c := os.Getenv("PINKY_VIDEO_CODEC"); c != "" {
		d.cfg.VideoCodec = c
	}

	before := serverPIDs(t, ctx, d)
	st, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if n := waitPackets(st, 3, 5*time.Second); n < 3 {
		t.Fatalf("%d packets before the stall, want a configuration and frames", n)
	}
	var ours []string
	for _, pid := range serverPIDs(t, ctx, d) {
		if !slices.Contains(before, pid) {
			ours = append(ours, pid)
		}
	}
	if len(ours) != 1 {
		t.Fatalf("servers started by the test: %v (before: %v)", ours, before)
	}
	pid := ours[0]
	t.Cleanup(func() {
		_, _ = d.client.Shell(context.Background(), d.info.ID, "kill -9 "+pid+" 2>/dev/null")
	})
	t.Logf("codec %s; stopping server %s", st.Codec(), pid)
	if _, err := d.client.Shell(ctx, d.info.ID, "kill -STOP "+pid); err != nil {
		t.Fatal(err)
	}

	asked := time.Now()
	st.(interface{ RequestKeyFrame() }).RequestKeyFrame()
	ended := false
	for !ended {
		select {
		case _, ok := <-st.Packets():
			ended = !ok
		case <-time.After(resetStall + 5*time.Second):
			t.Fatalf("the stream still runs %v after a reset that went unanswered", time.Since(asked).Round(time.Millisecond))
		}
	}
	took := time.Since(asked)
	t.Logf("the stream ended %v after the unanswered reset: %v", took.Round(time.Millisecond), st.Err())
	if took < resetStall {
		t.Errorf("ended after %v, before resetStall (%v)", took, resetStall)
	}
	if err := st.Err(); err == nil || !errors.Is(err, driver.ErrOffline) || !strings.Contains(err.Error(), errResetStalled.Error()) {
		t.Errorf("stream error %v, want the stalled reset", err)
	}

	start := time.Now()
	st2, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		t.Fatalf("stream again: %v", err)
	}
	defer st2.Close()
	if n := waitPackets(st2, 3, 5*time.Second); n < 3 {
		t.Fatalf("%d packets from the new stream, want pictures again", n)
	}
	t.Logf("pictures again %v after asking for a new stream", time.Since(start).Round(time.Millisecond))
}

// serverPIDs lists the pids of the scrcpy servers on the phone.
func serverPIDs(t *testing.T, ctx context.Context, d *Device) []string {
	t.Helper()
	out, err := d.client.Shell(ctx, d.info.ID, "pgrep -f '^app_process / [c]om.genymobile.scrcpy.Server ' || true")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

// waitPackets reads up to n packets for at most d and returns the count.
func waitPackets(st driver.Stream, n int, d time.Duration) int {
	deadline := time.After(d)
	got := 0
	for got < n {
		select {
		case _, ok := <-st.Packets():
			if !ok {
				return got
			}
			got++
		case <-deadline:
			return got
		}
	}
	return got
}
