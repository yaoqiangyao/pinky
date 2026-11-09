package ios

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// What an iPhone 13 mini and an iPad Pro 11-inch (3rd generation), iOS
// 27.0.1, said about their displays on 2026-11-08, and where taps landed:
// each case is a tap that pressed the element at its upright point.
func TestPanelPointOrientations(t *testing.T) {
	iphone := func(rot string) screenInfo {
		return screenInfo{Width: 1125, Height: 2436, Scale: 3, Orientation: rot, Native: "rot0"}
	}
	ipad := func(rot string) screenInfo {
		return screenInfo{Width: 2388, Height: 1668, Scale: 2, Orientation: rot, Native: "rot270"}
	}
	cases := []struct {
		name   string
		si     screenInfo
		w, h   int          // the screen the right way up
		p      screen.Point // where the element is, the right way up
		tx, ty int          // the same point in the touch panel's frame
		tw, th int          // the touch panel's frame
	}{
		{"iPhone portrait", iphone("rot0"), 1125, 2436, screen.Point{X: 393, Y: 1350}, 393, 1350, 1125, 2436},
		// Calculator's 7 in landscape, the phone turned counterclockwise
		// (rot270) and clockwise (rot90): it read 7 both times.
		{"iPhone landscape rot270", iphone("rot270"), 2436, 1125, screen.Point{X: 393, Y: 507}, 617, 393, 1125, 2436},
		{"iPhone landscape rot90", iphone("rot90"), 2436, 1125, screen.Point{X: 393, Y: 507}, 507, 2042, 1125, 2436},
		// The iPad held the usual way up is rot90 from its landscape panel:
		// its touch frame is the screen as it is.
		{"iPad portrait", ipad("rot90"), 1668, 2388, screen.Point{X: 1247, Y: 1565}, 1247, 1565, 1668, 2388},
		// Upside down: General's Screen Capture row opened, and the
		// sidebar's General took it back.
		{"iPad upside down", ipad("rot270"), 1668, 2388, screen.Point{X: 1247, Y: 1565}, 420, 822, 1668, 2388},
		{"iPad upside down sidebar", ipad("rot270"), 1668, 2388, screen.Point{X: 300, Y: 1457}, 1367, 930, 1668, 2388},
		{"iPad landscape rot0", ipad("rot0"), 2388, 1668, screen.Point{X: 100, Y: 200}, 1467, 100, 1668, 2388},
		{"iPad landscape rot180", ipad("rot180"), 2388, 1668, screen.Point{X: 100, Y: 200}, 200, 2287, 1668, 2388},
	}
	for _, c := range cases {
		u := c.si.upright()
		if u.Width != c.w || u.Height != c.h {
			t.Errorf("%s: upright %dx%d, want %dx%d", c.name, u.Width, u.Height, c.w, c.h)
		}
		x, y := panelPoint(c.p, c.si)
		if wx, wy := scaleAxis(c.tx, c.tw), scaleAxis(c.ty, c.th); x != wx || y != wy {
			t.Errorf("%s: panel %d,%d, want %d,%d (%d,%d of %dx%d)", c.name, x, y, wx, wy, c.tx, c.ty, c.tw, c.th)
		}
		// A raw touch on the picture, which is in the display's frame,
		// lands where the same point of the upright screen does.
		back := turnPoint(c.p, c.w, c.h, c.si.turn().inverse())
		vx, vy := touchPanel(back, c.si)
		if vx != x || vy != y {
			t.Errorf("%s: a touch at %v of the picture goes to %d,%d, a tap at %v to %d,%d", c.name, back, vx, vy, c.p, x, y)
		}
	}
	// Points off the screen stay on its edge.
	si := iphone("rot90")
	if x, y := panelPoint(screen.Point{X: -10, Y: 5000}, si); x != scaleAxis(1124, 1125) || y != scaleAxis(2435, 2436) {
		t.Fatalf("off the screen: %d,%d", x, y)
	}
	if o := orientationOf(2436, 1125); o != "landscape" {
		t.Fatal(o)
	}
	if o := orientationOf(1668, 2388); o != "portrait" {
		t.Fatal(o)
	}
}

// touchPanel maps a point of the picture as Device.Touch does, with a
// picture the size of the display (no padding).
func touchPanel(p screen.Point, si screenInfo) (uint16, uint16) {
	native := fromVideo(p, si.Width, si.Height, si.Width, si.Height)
	return panelPoint(turnPoint(native, si.Width, si.Height, si.turn()), si)
}

// The packets carry the turn the display reported last.
func TestMediaPacketsCarryTurn(t *testing.T) {
	m := &media{done: make(chan struct{}), ready: make(chan struct{}), subs: map[*subscriber]struct{}{}, needKey: true}
	sub := &subscriber{ch: make(chan driver.Packet, 16), needKey: true}
	m.subs[sub] = struct{}{}
	m.setTurn(3)
	ap := []byte{hevcAP << 1, 1}
	for _, n := range [][]byte{hevcNAL(hevcVPS, 1), testHEVCSPS(2400, 1680, 0), hevcNAL(hevcPPS, 2)} {
		ap = binary.BigEndian.AppendUint16(ap, uint16(len(n)))
		ap = append(ap, n...)
	}
	feed := func(raw []byte) {
		p, err := parseRTP(raw)
		if err != nil {
			t.Fatal(err)
		}
		m.onRTP(p, len(raw))
	}
	feed(rtpPkt(false, 100, 10, 0, ap))
	feed(rtpPkt(true, 100, 11, 0, hevcNAL(19, 0xAA)))
	m.setTurn(1)
	feed(rtpPkt(true, 100, 12, 400, hevcNAL(1, 0xBB)))
	want := []int{3, 3, 1} // the config, the key frame, the next frame
	for i, w := range want {
		select {
		case p := <-sub.ch:
			if p.Turn != w {
				t.Fatalf("packet %d: turn %d, want %d", i, p.Turn, w)
			}
		default:
			t.Fatalf("packet %d missing", i)
		}
	}
}

// RTP header extensions are kept for diagnostics.
func TestRTPHeaderExtension(t *testing.T) {
	raw := []byte{0x90, 100, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0x93, 0x11, 0, 1, 0, 0x3d, 0x60, 0x64, 0xAA}
	p, err := parseRTP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Ext, raw[12:20]) || !bytes.Equal(p.Payload, []byte{0xAA}) {
		t.Fatalf("ext % x payload % x", p.Ext, p.Payload)
	}
}
