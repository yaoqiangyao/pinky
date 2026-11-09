package ios

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

func TestTurnPoint(t *testing.T) {
	// A 4×6 picture: its corners after each clockwise turn.
	w, h := 4, 6
	tl, tr, bl := screen.Point{X: 0, Y: 0}, screen.Point{X: 3, Y: 0}, screen.Point{X: 0, Y: 5}
	cases := []struct {
		q          quarter
		tl, tr, bl screen.Point
	}{
		{0, tl, tr, bl},
		// A quarter turn clockwise: the top-left corner goes to the top
		// right, the top-right to the bottom right, the bottom-left to the
		// top left.
		{1, screen.Point{X: 5, Y: 0}, screen.Point{X: 5, Y: 3}, screen.Point{X: 0, Y: 0}},
		{2, screen.Point{X: 3, Y: 5}, screen.Point{X: 0, Y: 5}, screen.Point{X: 3, Y: 0}},
		{3, screen.Point{X: 0, Y: 3}, screen.Point{X: 0, Y: 0}, screen.Point{X: 5, Y: 3}},
	}
	for _, c := range cases {
		got := []screen.Point{turnPoint(tl, w, h, c.q), turnPoint(tr, w, h, c.q), turnPoint(bl, w, h, c.q)}
		want := []screen.Point{c.tl, c.tr, c.bl}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("turn %d: corner %d at %v, want %v", c.q, i, got[i], want[i])
			}
		}
		// Every pixel comes back with the inverse turn.
		tw, th := turnSize(w, h, c.q)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p := screen.Point{X: x, Y: y}
				q := turnPoint(p, w, h, c.q)
				if q.X < 0 || q.Y < 0 || q.X >= tw || q.Y >= th {
					t.Fatalf("turn %d: %v lands outside %dx%d at %v", c.q, p, tw, th, q)
				}
				if back := turnPoint(q, tw, th, c.q.inverse()); back != p {
					t.Fatalf("turn %d: %v -> %v -> %v", c.q, p, q, back)
				}
			}
		}
	}
}

func TestTurnNV12(t *testing.T) {
	w, h := 4, 6
	y := make([]byte, w*h)
	for i := range y {
		y[i] = byte(i)
	}
	uv := make([]byte, w*h/2)
	for i := range uv {
		uv[i] = byte(100 + i)
	}
	for q := quarter(0); q < 4; q++ {
		ty, tuv, tw, th := turnNV12(y, uv, w, h, q)
		if ww, hh := turnSize(w, h, q); tw != ww || th != hh {
			t.Fatalf("turn %d: size %dx%d", q, tw, th)
		}
		for row := 0; row < h; row++ {
			for col := 0; col < w; col++ {
				p := turnPoint(screen.Point{X: col, Y: row}, w, h, q)
				if ty[p.Y*tw+p.X] != y[row*w+col] {
					t.Fatalf("turn %d: luma of (%d,%d) not at %v", q, col, row, p)
				}
			}
		}
		for row := 0; row < h/2; row++ {
			for col := 0; col < w/2; col++ {
				p := turnPoint(screen.Point{X: col, Y: row}, w/2, h/2, q)
				i, j := 2*(row*(w/2)+col), 2*(p.Y*(tw/2)+p.X)
				if tuv[j] != uv[i] || tuv[j+1] != uv[i+1] {
					t.Fatalf("turn %d: chroma of (%d,%d) not at %v", q, col, row, p)
				}
			}
		}
		by, buv, bw, bh := turnNV12(ty, tuv, tw, th, q.inverse())
		if bw != w || bh != h || !bytes.Equal(by, y) || !bytes.Equal(buv, uv) {
			t.Fatalf("turn %d and back changed the picture", q)
		}
	}
}

func TestTurnImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(10, 20, 13, 22)) // 3×2, not at the origin
	img.Set(10, 20, color.NRGBA{255, 0, 0, 255})      // top left: red
	img.Set(12, 21, color.NRGBA{0, 0, 255, 255})      // bottom right: blue
	got := turnImage(img, 1)
	if b := got.Bounds(); b.Dx() != 2 || b.Dy() != 3 {
		t.Fatalf("bounds %v", b)
	}
	// A quarter turn clockwise puts the top left at the top right.
	if r, _, _, _ := got.At(1, 0).RGBA(); r>>8 != 255 {
		t.Fatalf("top right %v", got.At(1, 0))
	}
	if _, _, b, _ := got.At(0, 2).RGBA(); b>>8 != 255 {
		t.Fatalf("bottom left %v", got.At(0, 2))
	}
	if turnImage(img, 0) != image.Image(img) {
		t.Fatal("no turn must return the picture as it is")
	}
}

// withEXIF puts an EXIF block with the orientation tag right after a
// JPEG's start marker, as UIKit writes it.
func withEXIF(t *testing.T, j []byte, orientation uint16, bo binary.ByteOrder) []byte {
	t.Helper()
	var tiff bytes.Buffer
	if bo == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	binary.Write(&tiff, bo, uint16(42))
	binary.Write(&tiff, bo, uint32(8))
	binary.Write(&tiff, bo, uint16(2)) // two entries
	// ImageWidth first, so the orientation is not the first entry.
	binary.Write(&tiff, bo, uint16(0x0100))
	binary.Write(&tiff, bo, uint16(4))
	binary.Write(&tiff, bo, uint32(1))
	binary.Write(&tiff, bo, uint32(640))
	binary.Write(&tiff, bo, uint16(0x0112))
	binary.Write(&tiff, bo, uint16(3))
	binary.Write(&tiff, bo, uint32(1))
	binary.Write(&tiff, bo, orientation)
	binary.Write(&tiff, bo, uint16(0))
	binary.Write(&tiff, bo, uint32(0))
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write(j[:2])
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(seg)+2))
	out.Write(seg)
	out.Write(j[2:])
	return out.Bytes()
}

func TestEXIFTurn(t *testing.T) {
	j := jpegBytes(t, 8, 16, color.White)
	if q := exifTurn(j); q != 0 {
		t.Fatalf("no EXIF: turn %d", q)
	}
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o, want := range map[uint16]quarter{1: 0, 3: 2, 6: 1, 8: 3, 2: 0, 5: 0} {
			b := withEXIF(t, j, o, bo)
			if q := exifTurn(b); q != want {
				t.Errorf("%v orientation %d: turn %d, want %d", bo, o, q, want)
			}
		}
	}
	if q := exifTurn([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF}); q != 0 {
		t.Fatalf("a cut-off segment: turn %d", q)
	}
	// The MJPEG reader carries the turn with the frame.
	var s bytes.Buffer
	b := withEXIF(t, j, 8, binary.BigEndian)
	fmt.Fprintf(&s, "--BoundaryString\r\nContent-type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(b))
	s.Write(b)
	f, err := newMJPEGReader(&s, nil).next(context.Background())
	if err != nil || f.turn != 3 {
		t.Fatalf("frame turn %d, %v", f.turn, err)
	}
}

// fakeTurningWDA is WebDriverAgent on an iPad whose interface is in
// portrait or landscape: its tree and window size follow the interface,
// as XCTest's do.
type fakeTurningWDA struct {
	mu        sync.Mutex
	landscape bool
	alert     bool // an alert up, whose window XCTest gives in the panel's frame
	actions   []string
}

func (f *fakeTurningWDA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	if rest, ok := strings.CutPrefix(path, "/session/s1"); ok {
		path = rest
	}
	pw, ph := 834.0, 1210.0
	if f.landscape {
		pw, ph = ph, pw
	}
	switch r.Method + " " + path {
	case "POST /session":
		fmt.Fprint(w, `{"value":{"sessionId":"s1","capabilities":{}},"sessionId":"s1"}`)
	case "POST /appium/settings":
		fmt.Fprint(w, `{"value":null}`)
	case "POST /actions":
		f.actions = append(f.actions, string(b))
		fmt.Fprint(w, `{"value":null}`)
	case "GET /window/size":
		fmt.Fprintf(w, `{"value":{"width":%v,"height":%v}}`, pw, ph)
	case "GET /wda/activeAppInfo":
		fmt.Fprint(w, `{"value":{"bundleId":"com.apple.Preferences","name":"Settings"}}`)
	case "GET /wda/locked":
		fmt.Fprint(w, `{"value":false}`)
	case "GET /source":
		if f.alert {
			// As the iPad Simulator gave Settings' Enable Dictation? alert
			// in landscape: the window and everything in it in the
			// portrait panel's frame.
			fmt.Fprintf(w, `{"value":{"type":"Application","label":"Settings","rect":{"x":0,"y":0,"width":%v,"height":%v},"isVisible":"1","children":[
				{"type":"Window","isVisible":"1","rect":{"x":0,"y":0,"width":834,"height":1210},"children":[
					{"type":"Alert","label":"Enable Dictation?","isAccessible":"1","isVisible":"1","rect":{"x":247,"y":445,"width":365,"height":320},"children":[
						{"type":"Button","label":"Not Now","isAccessible":"1","isVisible":"1","rect":{"x":319,"y":461,"width":49,"height":288}}]}]}]}}`, pw, ph)
			return
		}
		// The sidebar's General row stays at the left in both.
		fmt.Fprintf(w, `{"value":{"type":"Application","label":"Settings","rect":{"x":0,"y":0,"width":%v,"height":%v},"isVisible":"1","children":[
			{"type":"Button","label":"General","isAccessible":"1","isVisible":"1","rect":{"x":26,"y":318,"width":288,"height":53}},
			{"type":"Button","label":"Keyboard","isAccessible":"1","isVisible":"1","rect":{"x":%v,"y":700,"width":300,"height":44}}]}}`,
			pw, ph, pw-320)
	default:
		w.WriteHeader(404)
		fmt.Fprintf(w, `{"value":{"error":"unknown command","message":"%s"}}`, path)
	}
}

// lastTap returns where the last action put the finger down, in points.
func (f *fakeTurningWDA) lastTap(t *testing.T) (float64, float64) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.actions) == 0 {
		t.Fatal("no action")
	}
	var a struct {
		Actions []struct {
			Actions []map[string]any `json:"actions"`
		} `json:"actions"`
	}
	json.Unmarshal([]byte(f.actions[len(f.actions)-1]), &a)
	first := a.Actions[0].Actions[0]
	return first["x"].(float64), first["y"].(float64)
}

func TestWDALandscape(t *testing.T) {
	f := &fakeTurningWDA{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	// Opened in portrait: 1668×2420 pixels at 2x.
	d := &WDADevice{c: newWDAURL(srv.URL, ""), scale: 2, width: 1668, height: 2420}
	ctx := context.Background()
	element := func(s *screen.Screen, label string) screen.Element {
		for _, e := range s.Elements {
			if e.Label == label {
				return e
			}
		}
		t.Fatalf("no %q in %+v", label, s.Elements)
		return screen.Element{}
	}

	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != 1668 || s.Height != 2420 || s.Orientation != "portrait" {
		t.Fatalf("portrait screen %dx%d %s", s.Width, s.Height, s.Orientation)
	}

	f.mu.Lock()
	f.landscape = true
	f.mu.Unlock()
	s, err = d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != 2420 || s.Height != 1668 || s.Orientation != "landscape" {
		t.Fatalf("landscape screen %dx%d %s", s.Width, s.Height, s.Orientation)
	}
	// Frames are in points at the screen's scale, not stretched to the
	// portrait width.
	if g := element(s, "General").Bounds; g != (screen.Rect{X: 52, Y: 636, W: 576, H: 106}) {
		t.Fatalf("General at %+v", g)
	}
	k := element(s, "Keyboard").Bounds
	if k.X != 1780 || k.X+k.W > s.Width {
		t.Fatalf("Keyboard at %+v on a %d wide screen", k, s.Width)
	}
	// A tap on the right of the landscape screen reaches it, not the
	// portrait width's edge.
	if err := d.Tap(ctx, k.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	if x, y := f.lastTap(t); x != 1040.25 || y != 722.25 {
		t.Fatalf("tap at %v,%v points", x, y)
	}

	// The unlock swipe reads the orientation itself: up the middle of the
	// landscape screen.
	f.mu.Lock()
	f.landscape = false
	f.mu.Unlock()
	if w, h := d.sizeNow(ctx); w != 1668 || h != 2420 {
		t.Fatalf("size now %dx%d", w, h)
	}
	f.mu.Lock()
	f.landscape = true
	f.mu.Unlock()
	if w, h := d.sizeNow(ctx); w != 2420 || h != 1668 {
		t.Fatalf("size now %dx%d", w, h)
	}
}

func TestWDALandscapeMirrorTouch(t *testing.T) {
	f := &fakeTurningWDA{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	d := &WDADevice{c: newWDAURL(srv.URL, ""), scale: 2, width: 1668, height: 2420}
	// The mirror's picture is the screen at half size, turned upright:
	// 1210×834 in landscape. A tap on its right edge lands on the right of
	// the screen.
	d.video = fixedSizeStream{w: 1210, h: 834}
	ctx := context.Background()
	d.Touch(ctx, driver.TouchDown, screen.Point{X: 1200, Y: 400})
	if err := d.Touch(ctx, driver.TouchUp, screen.Point{X: 1200, Y: 400}); err != nil {
		t.Fatal(err)
	}
	if x, y := f.lastTap(t); x != 1200.25 || y != 400.25 {
		t.Fatalf("tap at %v,%v points", x, y)
	}
	if w, h := d.size(); w != 2420 || h != 1668 {
		t.Fatalf("size %dx%d after a touch on a landscape picture", w, h)
	}
}

// fixedSizeStream is a stream that only has a size.
type fixedSizeStream struct{ w, h int }

func (s fixedSizeStream) Codec() driver.Codec           { return driver.H264 }
func (s fixedSizeStream) Size() (int, int)              { return s.w, s.h }
func (s fixedSizeStream) Packets() <-chan driver.Packet { return nil }
func (s fixedSizeStream) Err() error                    { return nil }
func (s fixedSizeStream) Close() error                  { return nil }

func TestWDASidewaysAlert(t *testing.T) {
	f := &fakeTurningWDA{landscape: true, alert: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	// The MJPEG server's frames carry the panel's turn: 8, three quarter
	// turns clockwise, as in the Simulator's LANDSCAPE.
	frame := withEXIF(t, jpegBytes(t, 8, 12, color.White), 8, binary.BigEndian)
	mj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=BoundaryString")
		fmt.Fprintf(w, "--BoundaryString\r\nContent-type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
		w.Write(frame)
	}))
	defer mj.Close()
	d := &WDADevice{c: newWDAURL(srv.URL, mj.URL), scale: 2, width: 1668, height: 2420}
	s, err := d.Observe(context.Background(), driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got screen.Rect
	for _, e := range s.Elements {
		if e.Label == "Not Now" {
			got = e.Bounds
		}
	}
	// Where the button shows on the landscape screen: 576×98 pixels around
	// 1210,981, wide and short.
	if want := (screen.Rect{X: 922, Y: 932, W: 576, H: 98}); got != want {
		t.Fatalf("Not Now at %+v, want %+v", got, want)
	}
	// Without a picture to tell the way, a quarter turn clockwise.
	d = &WDADevice{c: newWDAURL(srv.URL, ""), scale: 2, width: 1668, height: 2420}
	if q := d.landscapeTurn(context.Background()); q != 1 {
		t.Fatalf("turn without a picture: %d", q)
	}
}

func TestTurnTree(t *testing.T) {
	n := wdaNode{Rect: wdaRect{X: 247, Y: 445, Width: 365, Height: 320}}
	for q, want := range map[quarter]wdaRect{
		0: {X: 247, Y: 445, Width: 365, Height: 320},
		1: {X: 445, Y: 247, Width: 320, Height: 365},
		2: {X: 222, Y: 445, Width: 365, Height: 320},
		3: {X: 445, Y: 222, Width: 320, Height: 365},
	} {
		c := n
		turnTree(&c, 834, 1210, q)
		if c.Rect != want {
			t.Errorf("turn %d: %+v, want %+v", q, c.Rect, want)
		}
	}
	// Only windows the size of the screen turned sideways count, and only
	// in landscape.
	root := wdaNode{Rect: wdaRect{Width: 1210, Height: 834}, Children: []wdaNode{
		{Type: "Window", Rect: wdaRect{Width: 1210, Height: 834}},
		{Type: "Window", Rect: wdaRect{Width: 834, Height: 1210}},
		{Type: "Window", Rect: wdaRect{Width: 1668, Height: 2420}},
	}}
	if w := sidewaysWindows(&root); len(w) != 1 || w[0] != &root.Children[1] {
		t.Fatalf("sideways windows %v", w)
	}
	root.Rect = wdaRect{Width: 834, Height: 1210}
	if w := sidewaysWindows(&root); len(w) != 0 {
		t.Fatalf("portrait: sideways windows %v", w)
	}
}

// rotationWDA answers GET /rotation, /screenshot and nothing else: an iPad
// upside down, whose screenshot XCTest leaves as the panel is.
type rotationWDA struct {
	z    int
	shot []byte
}

func (f *rotationWDA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/rotation":
		fmt.Fprintf(w, `{"value":{"x":0,"y":0,"z":%d}}`, f.z)
	case "/session":
		fmt.Fprint(w, `{"value":{"sessionId":"s1","capabilities":{}},"sessionId":"s1"}`)
	case "/session/s1/appium/settings":
		fmt.Fprint(w, `{"value":null}`)
	case "/session/s1/screenshot", "/screenshot":
		fmt.Fprintf(w, `{"value":%q}`, base64.StdEncoding.EncodeToString(f.shot))
	default:
		w.WriteHeader(404)
		fmt.Fprintf(w, `{"value":{"error":"unknown command","message":"%s"}}`, r.URL.Path)
	}
}

func TestWDAUpsideDown(t *testing.T) {
	// A 4×6 screenshot with its top-left pixel red, as XCTest gives it
	// upside down: the panel as it is, so red is the screen's bottom right.
	img := image.NewNRGBA(image.Rect(0, 0, 4, 6))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	f := &rotationWDA{z: 180, shot: b.Bytes()}
	srv := httptest.NewServer(f)
	defer srv.Close()
	d := &WDADevice{c: newWDAURL(srv.URL, ""), scale: 1, width: 4, height: 6}
	ctx := context.Background()
	got, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := got.At(3, 5).RGBA(); r>>8 != 255 {
		t.Fatalf("upside down, the screenshot is not turned: bottom right %v", got.At(3, 5))
	}
	if q := d.cachedPictureTurn(); q != 2 {
		t.Fatalf("frames' extra turn %d", q)
	}
	// The MJPEG reader gives frames tagged up the extra turn.
	var s bytes.Buffer
	j := jpegBytes(t, 8, 12, color.White)
	fmt.Fprintf(&s, "--BoundaryString\r\nContent-Length: %d\r\n\r\n", len(j))
	s.Write(j)
	r := newMJPEGReader(&s, nil)
	r.upside = d.cachedPictureTurn
	if fr, err := r.next(ctx); err != nil || fr.turn != 2 {
		t.Fatalf("frame turn %d, %v", fr.turn, err)
	}
	// OCR reads it upright too.
	if up := d.uprightShot(ctx, b.Bytes()); bytes.Equal(up, b.Bytes()) {
		t.Fatal("OCR's picture not turned")
	}

	// In landscape GET /rotation says which way, for the sideways windows,
	// and the screenshots are left as WDA turned them.
	d = &WDADevice{c: newWDAURL(srv.URL, ""), scale: 1, width: 4, height: 6}
	f.z = 270
	if q := d.landscapeTurn(ctx); q != 3 {
		t.Fatalf("landscape turn %d, want 3", q)
	}
	if q := d.pictureTurn(ctx); q != 0 {
		t.Fatalf("landscape picture turn %d", q)
	}
	if up := d.uprightShot(ctx, b.Bytes()); !bytes.Equal(up, b.Bytes()) {
		t.Fatal("a landscape screenshot was turned again")
	}
}
