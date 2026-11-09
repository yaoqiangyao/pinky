package android

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
)

// streamBuffer is how many packets a slow consumer may fall behind (about
// four seconds at 60 fps) before packets are dropped until a key frame.
const streamBuffer = 256

// stream is a consumer attached to a live session. It starts at a codec
// configuration packet, so a decoder can begin with the first packet.
type stream struct {
	l  *live
	ch chan driver.Packet

	mu        sync.Mutex
	waitStart bool      // drop packets until the next configuration packet
	dropping  bool      // the consumer fell behind; drop until a fresh start
	resetAt   time.Time // when a fresh start was last asked for
	closed    bool
	err       error
}

// Stream implements driver.Device. The stream shares the device's scrcpy
// session with input and settle detection; options that differ from the
// running session's restart it. A second Stream call replaces the first.
func (d *Device) Stream(ctx context.Context, opt driver.StreamOptions) (driver.Stream, error) {
	want := d.baseOptions()
	if opt.MaxSize > 0 {
		want.MaxSize = opt.MaxSize
	}
	if opt.MaxFPS > 0 {
		want.MaxFPS = float64(opt.MaxFPS)
	}
	l, err := d.sessionWith(ctx, &want)
	if err != nil {
		return nil, err
	}
	return l.attach(ctx)
}

// attach connects a new stream to the session and asks the encoder for a
// fresh configuration and key frame.
func (l *live) attach(ctx context.Context) (*stream, error) {
	if _, _, err := l.videoSize(ctx); err != nil {
		return nil, err
	}
	st := &stream{l: l, ch: make(chan driver.Packet, streamBuffer), waitStart: true}
	l.mu.Lock()
	old := l.sub
	l.sub = st
	l.mu.Unlock()
	if old != nil {
		old.end(errors.New("android: replaced by a new stream"))
	}
	if err := l.resetVideo(); err != nil {
		l.detach(st)
		return nil, err
	}
	return st, nil
}

func (l *live) detach(st *stream) {
	l.mu.Lock()
	if l.sub == st {
		l.sub = nil
	}
	l.mu.Unlock()
}

// push delivers a packet from the session's reader goroutine.
func (st *stream) push(p scrcpy.Packet) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	switch {
	case st.dropping:
		// The consumer fell behind. Once it has worked through half its
		// backlog, ask the encoder for a fresh configuration and key frame
		// (once, or again if that answer got lost), and resume there.
		if len(st.ch) > cap(st.ch)/2 {
			return
		}
		if !p.Config {
			if time.Since(st.resetAt) > 2*time.Second {
				st.resetAt = time.Now()
				go st.l.resetVideo()
			}
			return
		}
		st.dropping = false
	case st.waitStart:
		if !p.Config {
			return
		}
		st.waitStart = false
	}
	pkt := driver.Packet{Data: p.Data, PTS: p.PTS, Config: p.Config, KeyFrame: p.KeyFrame}
	select {
	case st.ch <- pkt:
	default:
		st.dropping = true
		st.resetAt = time.Time{}
	}
}

// RequestKeyFrame asks the encoder for a fresh configuration and key frame
// now, by restarting it (scrcpy's reset-video message), at most once a
// second. The gateway shares one stream among the mirror and the recorder,
// and asks when one of them needs to start over (a recording after a pause,
// a viewer that joined late): scrcpy sends key frames by itself only every
// 10 seconds, and not at all while the screen stays still.
func (st *stream) RequestKeyFrame() {
	st.mu.Lock()
	if st.closed || time.Since(st.resetAt) < time.Second {
		st.mu.Unlock()
		return
	}
	st.resetAt = time.Now()
	st.mu.Unlock()
	go st.l.resetVideo()
}

// end closes the stream with an error, from the session side.
func (st *stream) end(err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	st.closed = true
	st.err = err
	close(st.ch)
}

// Codec implements driver.Stream.
func (st *stream) Codec() driver.Codec { return st.l.codec }

// Size implements driver.Stream: the current video size, which changes
// when the phone rotates.
func (st *stream) Size() (int, int) {
	st.l.mu.Lock()
	defer st.l.mu.Unlock()
	return st.l.vw, st.l.vh
}

// Packets implements driver.Stream.
func (st *stream) Packets() <-chan driver.Packet { return st.ch }

// Err implements driver.Stream.
func (st *stream) Err() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.err
}

// Close implements driver.Stream. The device's session keeps running.
func (st *stream) Close() error {
	st.l.detach(st)
	st.end(nil)
	return nil
}
