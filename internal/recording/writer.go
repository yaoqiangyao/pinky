package recording

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Eyevinn/mp4ff/mp4"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
)

// Writer remuxes a phone's video stream into a fragmented MP4 file. It is
// not safe for concurrent use.
//
// The file is created with the first key frame that follows a
// configuration packet; until then packets are dropped, and a recording
// that never gets one leaves no file.
type Writer struct {
	path  string
	codec driver.Codec

	f       *os.File
	params  paramSets // the current parameter sets
	inband  [][]byte  // changed parameter sets, for the next key frame
	seq     uint32
	pending []sample // the fragment being collected
	last    uint64   // time of the newest sample, in ticks
	end     uint64   // where the last flushed fragment ends
	w, h    int
	err     error
	closed  bool
	turn    int          // the newest sample's turn (driver.Packet.Turn)
	turns   []turnChange // turns since the last fragment, for its turn box
}

type sample struct {
	t    uint64 // ticks from the start of the recording
	dur  uint64
	key  bool
	data []byte // length-prefixed NAL units
}

// Create returns a writer for a recording at path in codec. The folder is
// created when needed; the file itself only with the first key frame.
func Create(path string, codec driver.Codec) (*Writer, error) {
	if codec != driver.H264 && codec != driver.HEVC {
		return nil, fmt.Errorf("recording: unsupported codec %q", codec)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Writer{path: path, codec: codec}, nil
}

// Path returns the file's path.
func (w *Writer) Path() string { return w.path }

// Started reports whether the file exists, that is, whether a key frame
// was written.
func (w *Writer) Started() bool { return w.f != nil }

// Length is the time of the newest frame from the start, or the whole
// length once closed.
func (w *Writer) Length() time.Duration {
	if w.end > w.last {
		return fromTicks(w.end)
	}
	return fromTicks(w.last)
}

// Size returns the video's size, once started.
func (w *Writer) Size() (int, int) { return w.w, w.h }

// Write adds a packet shown at time at from the start of the recording.
// Times must not go back; a packet at or before the previous one is moved
// just after it. A failed write closes the file and every later Write
// returns the error.
func (w *Writer) Write(p driver.Packet, at time.Duration) error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return errors.New("recording: writer closed")
	}
	if p.Config {
		var ps paramSets
		for _, nal := range video.SplitNALs(p.Data) {
			if isParamSet(w.codec, video.NALType(w.codec, nal)) {
				ps.add(w.codec, nal)
			}
		}
		w.setParams(ps)
		return nil
	}
	var nals [][]byte
	var inline paramSets
	for _, nal := range video.SplitNALs(p.Data) {
		t := video.NALType(w.codec, nal)
		switch {
		case len(nal) == 0 || isAUD(w.codec, t):
		case isParamSet(w.codec, t):
			inline.add(w.codec, nal)
		default:
			nals = append(nals, nal)
		}
	}
	if !inline.empty() {
		w.setParams(inline)
	}
	if len(nals) == 0 {
		return nil
	}
	if w.f == nil {
		if !p.KeyFrame || w.params.empty() {
			return nil // nothing can be decoded before the first key frame
		}
		if err := w.start(p.Turn & 3); err != nil {
			return w.fail(err)
		}
		w.inband = nil
		w.turn = p.Turn & 3
	}
	if p.KeyFrame && len(w.inband) > 0 {
		nals = append(append([][]byte(nil), w.inband...), nals...)
		w.inband = nil
	}
	s := sample{t: ticks(at), key: p.KeyFrame, data: lengthPrefixed(nals)}
	if n := len(w.pending); n > 0 {
		if s.t <= w.last {
			s.t = w.last + 1
		}
		prev := &w.pending[n-1]
		prev.dur = s.t - prev.t
		// A fragment holds about a second; one starts early at a key frame,
		// so most fragments start with one and seeking stays cheap.
		span := s.t - w.pending[0].t
		if span >= ticks(fragmentLength) || (s.key && span >= ticks(fragmentLength/4)) {
			if err := w.flush(); err != nil {
				return w.fail(err)
			}
		}
	}
	if turn := p.Turn & 3; turn != w.turn {
		w.turns = append(w.turns, turnChange{t: s.t, turn: turn})
		w.turn = turn
	}
	w.last = s.t
	w.pending = append(w.pending, s)
	return nil
}

// setParams records new parameter sets. Before the file starts they go
// into its sample entry; after, changed ones go into the next key frame.
func (w *Writer) setParams(ps paramSets) {
	if ps.empty() {
		return
	}
	if w.f != nil && !ps.equal(w.params) {
		w.inband = ps.all()
	}
	w.params = ps
}

// start creates the file and writes the init segment, whose track turns
// the first picture turn quarter turns clockwise (turn.go).
func (w *Writer) start(turn int) error {
	init := mp4.NewMP4Init()
	init.AddChild(mp4.NewFtyp("iso6", 0, []string{"iso6", "isom", "mp41"}))
	moov := mp4.NewMoovBox()
	init.AddChild(moov)
	moov.AddChild(mp4.CreateMvhd())
	moov.AddChild(mp4.NewMvexBox())
	trak := init.AddEmptyTrack(timescale, "video", "und")
	var err error
	if w.codec == driver.HEVC {
		err = trak.SetHEVCDescriptor("hvc1", w.params.vps, w.params.sps, w.params.pps, nil, true)
	} else {
		err = trak.SetAVCDescriptor("avc1", w.params.sps, w.params.pps, true)
	}
	if err != nil {
		return fmt.Errorf("recording: parameter sets: %w", err)
	}
	w.w, w.h = int(trak.Tkhd.Width>>16), int(trak.Tkhd.Height>>16)
	trak.Tkhd.Matrix = turnMatrix(turn, w.w, w.h)
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := init.Encode(f); err != nil {
		f.Close()
		os.Remove(w.path)
		return err
	}
	w.f = f
	return nil
}

// flush writes the collected samples as one fragment.
func (w *Writer) flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	if len(w.turns) > 0 {
		if err := writeTurnBox(w.f, w.turns); err != nil {
			return err
		}
		w.turns = w.turns[:0]
	}
	w.seq++
	frag, err := mp4.CreateFragment(w.seq, 1)
	if err != nil {
		return err
	}
	for _, s := range w.pending {
		flags := mp4.NonSyncSampleFlags | mp4.SampleDependsOn1
		if s.key {
			flags = mp4.SyncSampleFlags
		}
		frag.AddFullSample(mp4.FullSample{
			Sample:     mp4.Sample{Flags: flags, Dur: uint32(s.dur), Size: uint32(len(s.data))},
			DecodeTime: s.t,
			Data:       s.data,
		})
	}
	if err := frag.Encode(w.f); err != nil {
		return err
	}
	last := w.pending[len(w.pending)-1]
	w.end = last.t + last.dur
	w.pending = w.pending[:0]
	return nil
}

// Close writes the last fragment, which ends at end (the time of the stop,
// from the start of the recording), and closes the file.
func (w *Writer) Close(end time.Duration) error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.f == nil {
		return w.err
	}
	if n := len(w.pending); n > 0 && w.err == nil {
		prev := &w.pending[n-1]
		e := ticks(end)
		prev.dur = ticks(time.Second / 30)
		if e > prev.t {
			prev.dur = e - prev.t
		}
		if err := w.flush(); err != nil {
			w.err = err
		}
	}
	if err := w.f.Close(); err != nil && w.err == nil {
		w.err = err
	}
	return w.err
}

func (w *Writer) fail(err error) error {
	w.err = err
	if w.f != nil {
		w.f.Close()
	}
	return err
}

func lengthPrefixed(nals [][]byte) []byte {
	n := 0
	for _, u := range nals {
		n += 4 + len(u)
	}
	out := make([]byte, 0, n)
	for _, u := range nals {
		out = binary.BigEndian.AppendUint32(out, uint32(len(u)))
		out = append(out, u...)
	}
	return out
}
