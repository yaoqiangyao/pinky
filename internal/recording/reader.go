package recording

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
)

// Info describes a recording file.
type Info struct {
	Codec         driver.Codec
	Width, Height int           // as encoded, before any turn
	Turn          int           // quarter turns clockwise that put the first picture upright
	Length        time.Duration // from the start to the end of the last frame
	Frames        int
}

// Probe reads a recording's codec, size and length. A file cut short by a
// crash is read up to its last complete fragment.
func Probe(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	idx, err := readIndex(f)
	if err != nil {
		return Info{}, err
	}
	info := Info{Codec: idx.codec, Width: idx.w, Height: idx.h, Turn: idx.turn0}
	for _, s := range idx.samples {
		info.Frames++
		if e := fromTicks(s.t + uint64(s.dur)); e > info.Length {
			info.Length = e
		}
	}
	return info, nil
}

// index is what playback needs from a file: the parameter sets of the
// sample entry and every complete sample.
type index struct {
	codec   driver.Codec
	w, h    int
	params  [][]byte
	lenSize int // bytes of each NAL unit's length
	samples []indexed
	turn0   int          // the first picture's turn, from the track's matrix
	turns   []turnChange // turns after that, from Pinky's turn boxes (turn.go)
}

type indexed struct {
	t    uint64
	dur  uint32
	key  bool
	off  int64
	size uint32
}

// readIndex reads the boxes of a fragmented MP4 file, skipping sample
// data. It stops quietly at a box cut short.
func readIndex(r io.ReaderAt) (*index, error) {
	idx := &index{lenSize: 4}
	var pos int64
	var moof *mp4.MoofBox
	var moofAt int64
	var trex *mp4.TrexBox
	haveMoov := false
	for {
		var hdr [16]byte
		if n, err := r.ReadAt(hdr[:8], pos); n < 8 {
			if err == io.EOF || err == nil || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		head := int64(8)
		switch size {
		case 1:
			if n, _ := r.ReadAt(hdr[8:16], pos+8); n < 8 {
				return finish(idx, haveMoov)
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
			head = 16
		case 0:
			return finish(idx, haveMoov) // runs to the end: never written by Writer
		}
		if size < head {
			return nil, fmt.Errorf("recording: bad box %q at %d", typ, pos)
		}
		switch typ {
		case "moov", "moof":
			if size > 64<<20 {
				return nil, fmt.Errorf("recording: %s box of %d bytes", typ, size)
			}
			buf := make([]byte, size)
			if n, _ := r.ReadAt(buf, pos); int64(n) < size {
				return finish(idx, haveMoov) // cut short
			}
			box, err := mp4.DecodeBox(uint64(pos), bytes.NewReader(buf))
			if err != nil {
				return nil, fmt.Errorf("recording: %s: %w", typ, err)
			}
			if typ == "moov" {
				mv, ok := box.(*mp4.MoovBox)
				if !ok {
					return nil, errors.New("recording: bad moov box")
				}
				if err := idx.readMoov(mv); err != nil {
					return nil, err
				}
				if mv.Mvex != nil && mv.Mvex.Trex != nil {
					trex = mv.Mvex.Trex
				}
				haveMoov = true
			} else {
				moof, _ = box.(*mp4.MoofBox)
				moofAt = pos
			}
		case "uuid":
			if size <= 1<<20 {
				buf := make([]byte, size-head)
				if n, _ := r.ReadAt(buf, pos+head); int64(n) < size-head {
					return finish(idx, haveMoov) // cut short
				}
				if turns, ok := readTurnBox(buf); ok {
					idx.turns = append(idx.turns, turns...)
				}
			}
		case "mdat":
			end := pos + size
			// The whole mdat must be there: a crash can cut the last one.
			var probe [1]byte
			if n, _ := r.ReadAt(probe[:], end-1); n < 1 {
				return finish(idx, haveMoov)
			}
			if moof != nil && haveMoov {
				idx.addFragment(moof, moofAt, trex, pos+head, end)
			}
			moof = nil
		}
		pos += size
	}
	return finish(idx, haveMoov)
}

func finish(idx *index, haveMoov bool) (*index, error) {
	if !haveMoov {
		return nil, errors.New("recording: not a recording (no moov box)")
	}
	return idx, nil
}

func (idx *index) readMoov(mv *mp4.MoovBox) error {
	if mv.Trak == nil || mv.Trak.Mdia == nil || mv.Trak.Mdia.Minf == nil || mv.Trak.Mdia.Minf.Stbl == nil ||
		mv.Trak.Mdia.Minf.Stbl.Stsd == nil {
		return errors.New("recording: no video track")
	}
	stsd := mv.Trak.Mdia.Minf.Stbl.Stsd
	if mv.Trak.Tkhd != nil {
		idx.turn0 = matrixTurn(mv.Trak.Tkhd.Matrix)
	}
	switch {
	case stsd.AvcX != nil && stsd.AvcX.AvcC != nil:
		c := stsd.AvcX.AvcC
		idx.codec, idx.w, idx.h = driver.H264, int(stsd.AvcX.Width), int(stsd.AvcX.Height)
		idx.params = append(append(idx.params, c.SPSnalus...), c.PPSnalus...)
	case stsd.HvcX != nil && stsd.HvcX.HvcC != nil:
		c := stsd.HvcX.HvcC
		idx.codec, idx.w, idx.h = driver.HEVC, int(stsd.HvcX.Width), int(stsd.HvcX.Height)
		for _, t := range []hevc.NaluType{hevc.NALU_VPS, hevc.NALU_SPS, hevc.NALU_PPS} {
			idx.params = append(idx.params, c.GetNalusForType(t)...)
		}
		idx.lenSize = int(c.LengthSizeMinusOne) + 1
	default:
		return errors.New("recording: the video is neither H.264 nor HEVC")
	}
	return nil
}

func (idx *index) addFragment(moof *mp4.MoofBox, moofAt int64, trex *mp4.TrexBox, dataStart, dataEnd int64) {
	for _, traf := range moof.Trafs {
		if traf.Tfhd == nil {
			continue
		}
		var t uint64
		if traf.Tfdt != nil {
			t = traf.Tfdt.BaseMediaDecodeTime()
		}
		for _, trun := range traf.Truns {
			trun.AddSampleDefaultValues(traf.Tfhd, trex)
			base := moofAt
			if traf.Tfhd.HasBaseDataOffset() {
				base = int64(traf.Tfhd.BaseDataOffset)
			}
			off := base + int64(trun.DataOffset)
			for _, s := range trun.Samples {
				if off < dataStart || off+int64(s.Size) > dataEnd {
					return // not inside this mdat: written by something else
				}
				idx.samples = append(idx.samples, indexed{t: t, dur: s.Dur, key: mp4.IsSyncSampleFlags(s.Flags),
					off: off, size: s.Size})
				t += uint64(s.Dur)
				off += int64(s.Size)
			}
		}
	}
}

// Open plays the recording at path from offset from: it starts at the key
// frame at or before from and sends packets at the pace they were
// recorded, as a live stream would arrive; the frames between that key
// frame and from are sent at once, so the picture at from shows quickly.
// Closing the stream stops it. Packets carry their time in the recording
// as PTS.
func Open(ctx context.Context, path string, from time.Duration) (driver.Stream, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	idx, err := readIndex(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	start := 0
	for i, s := range idx.samples {
		if s.key && fromTicks(s.t) <= from {
			start = i
		}
	}
	for start < len(idx.samples) && !idx.samples[start].key {
		start++ // a file whose first frame is not a key frame
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &player{f: f, idx: idx, cancel: cancel, packets: make(chan driver.Packet, 8), done: make(chan struct{})}
	go p.run(ctx, start, from)
	return p, nil
}

// player is a recording played back as a driver.Stream.
type player struct {
	f       *os.File
	idx     *index
	cancel  context.CancelFunc
	packets chan driver.Packet
	done    chan struct{}

	mu  sync.Mutex
	err error
}

func (p *player) Codec() driver.Codec           { return p.idx.codec }
func (p *player) Size() (int, int)              { return p.idx.w, p.idx.h }
func (p *player) Packets() <-chan driver.Packet { return p.packets }

func (p *player) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *player) Close() error {
	p.cancel()
	<-p.done
	return nil
}

func (p *player) run(ctx context.Context, start int, from time.Duration) {
	defer close(p.done)
	defer close(p.packets)
	defer p.f.Close()
	send := func(pk driver.Packet) bool {
		select {
		case p.packets <- pk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	turn0 := 0
	if start < len(p.idx.samples) {
		turn0 = p.idx.turnAt(p.idx.samples[start].t)
	}
	if len(p.idx.params) > 0 && !send(driver.Packet{Data: annexB(p.idx.params), Config: true, Turn: turn0}) {
		return
	}
	began := time.Now()
	for _, s := range p.idx.samples[start:] {
		at := fromTicks(s.t)
		if at > from {
			wait := time.Until(began.Add(at - from))
			if wait > 0 {
				t := time.NewTimer(wait)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return
				}
			}
		}
		buf := make([]byte, s.size)
		if _, err := p.f.ReadAt(buf, s.off); err != nil {
			p.fail(err)
			return
		}
		cfg, pic := p.split(buf)
		turn := p.idx.turnAt(s.t)
		if len(cfg) > 0 && !send(driver.Packet{Data: annexB(cfg), Config: true, PTS: at, Turn: turn}) {
			return
		}
		if len(pic) > 0 && !send(driver.Packet{Data: annexB(pic), KeyFrame: s.key, PTS: at, Turn: turn}) {
			return
		}
	}
}

// split turns a sample back into NAL units: parameter sets, then the
// picture's.
func (p *player) split(b []byte) (cfg, pic [][]byte) {
	n := p.idx.lenSize
	for len(b) >= n {
		var size int
		for i := 0; i < n; i++ {
			size = size<<8 | int(b[i])
		}
		b = b[n:]
		if size > len(b) {
			break
		}
		nal := b[:size]
		b = b[size:]
		if isParamSet(p.idx.codec, video.NALType(p.idx.codec, nal)) {
			cfg = append(cfg, nal)
		} else {
			pic = append(pic, nal)
		}
	}
	return cfg, pic
}

func (p *player) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
}
