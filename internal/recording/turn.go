package recording

import (
	"bytes"
	"encoding/binary"
	"io"
	"sort"
)

// Turns. An iPhone or iPad on iOS 27 sends its display as the panel is
// mounted, with the quarter turns that put each picture upright beside it
// (driver.Packet.Turn); recordings keep them, so playback shows the screen
// the right way up as the mirror did.
//
//   - The track header's matrix turns the first picture, the standard way
//     a video says it is turned (FFmpeg reads it as a rotation of -90 for a
//     quarter turn), so players that honour it show the recording upright
//     as long as the phone was not turned during it.
//   - Every turn after that goes into a box of its own right before the
//     fragment whose samples it starts at: a 'uuid' box with turnBoxID,
//     a version byte, then a 32-bit count and per turn the sample's decode
//     time (64 bits, in timescale ticks) and the turn (one byte, 0 to 3).
//     Other readers skip boxes they do not know.

// turnBoxID identifies Pinky's 'uuid' box of turns.
var turnBoxID = [16]byte{0x8b, 0x9f, 0x31, 0x80, 0xd6, 0x4d, 0x46, 0xb4, 0xa2, 0x00, 0xcd, 0x56, 0x32, 0x6c, 0xa1, 0xed}

// turnChange is a turn that starts at a sample's decode time.
type turnChange struct {
	t    uint64
	turn int
}

// turnMatrix is a track header's matrix that turns a w×h picture q quarter
// turns clockwise: ISO/IEC 14496-12 maps (p, q) to (a·p + c·q + x,
// b·p + d·q + y); a, b, c, d, x and y are 16.16 fixed point, w 2.30.
func turnMatrix(q, w, h int) [9]int32 {
	const one = 0x10000
	switch q & 3 {
	case 1:
		return [9]int32{0, one, 0, -one, 0, 0, int32(h) * one, 0, 0x40000000}
	case 2:
		return [9]int32{-one, 0, 0, 0, -one, 0, int32(w) * one, int32(h) * one, 0x40000000}
	case 3:
		return [9]int32{0, -one, 0, one, 0, 0, 0, int32(w) * one, 0x40000000}
	}
	return [9]int32{one, 0, 0, 0, one, 0, 0, 0, 0x40000000}
}

// matrixTurn reads a track header's matrix back as quarter turns
// clockwise; a matrix that is no quarter turn (or none) is 0.
func matrixTurn(m [9]int32) int {
	a, b, c, d := m[0], m[1], m[3], m[4]
	switch {
	case a == 0 && b > 0 && c < 0 && d == 0:
		return 1
	case a < 0 && b == 0 && c == 0 && d < 0:
		return 2
	case a == 0 && b < 0 && c > 0 && d == 0:
		return 3
	}
	return 0
}

// writeTurnBox writes the 'uuid' box of turns.
func writeTurnBox(w io.Writer, turns []turnChange) error {
	var b bytes.Buffer
	size := 8 + 16 + 1 + 4 + 9*len(turns)
	binary.Write(&b, binary.BigEndian, uint32(size))
	b.WriteString("uuid")
	b.Write(turnBoxID[:])
	b.WriteByte(0) // version
	binary.Write(&b, binary.BigEndian, uint32(len(turns)))
	for _, c := range turns {
		binary.Write(&b, binary.BigEndian, c.t)
		b.WriteByte(byte(c.turn & 3))
	}
	_, err := w.Write(b.Bytes())
	return err
}

// readTurnBox reads the body of a 'uuid' box (after its size and type):
// the turns, or false when it is not Pinky's.
func readTurnBox(body []byte) ([]turnChange, bool) {
	if len(body) < 16+1+4 || !bytes.Equal(body[:16], turnBoxID[:]) || body[16] != 0 {
		return nil, false
	}
	n := int(binary.BigEndian.Uint32(body[17:21]))
	rest := body[21:]
	if n < 0 || n > len(rest)/9 {
		return nil, false
	}
	out := make([]turnChange, n)
	for i := range out {
		e := rest[9*i:]
		out[i] = turnChange{t: binary.BigEndian.Uint64(e), turn: int(e[8] & 3)}
	}
	return out, true
}

// turnAt returns the turn of the sample at decode time t: the last change
// at or before it, else the first picture's.
func (idx *index) turnAt(t uint64) int {
	i := sort.Search(len(idx.turns), func(i int) bool { return idx.turns[i].t > t })
	if i == 0 {
		return idx.turn0
	}
	return idx.turns[i-1].turn
}
