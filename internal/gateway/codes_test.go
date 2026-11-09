package gateway

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/ocr"
	"github.com/DIYgod/pinky/internal/screen"
)

func TestMain(m *testing.M) {
	// Reading pictures on this computer (Vision, Windows.Media.Ocr) is slow
	// and differs between machines: tests that need it say what it reads.
	readPicture = noPictureText
	os.Exit(m.Run())
}

func noPictureText(image.Image) ([]ocr.Line, error) { return nil, driver.ErrUnsupported }

// pictureText makes reading a picture return lines, for one test.
func pictureText(t *testing.T, lines ...ocr.Line) {
	t.Helper()
	readPicture = func(image.Image) ([]ocr.Line, error) { return lines, nil }
	t.Cleanup(func() { readPicture = noPictureText })
}

// covered reports whether a pixel has the color of the boxes over codes,
// give or take what JPEG makes of it.
func covered(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	near := func(v uint32, want uint8) bool { d := int(v>>8) - int(want); return d > -16 && d < 16 }
	return near(r, codeFill.R) && near(g, codeFill.G) && near(b, codeFill.B)
}

// The demo's Messages screen shows "Dinely: Your Dinely code is 482913" in
// a row at 40,200 1000x120 of a 1080x2400 screen; its clock is at 40,24.
var codeRow = screen.Point{X: 540, Y: 260}

func TestScreenCodesMasked(t *testing.T) {
	g, dev := newTestGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Read my messages")
	dev.Show("messages")
	res := s.Observe(ctx, ObserveRequest{})
	mustStatus(t, res, StatusSettled)
	text := res.Screen.Render()
	if strings.Contains(text, "482913") || !strings.Contains(text, `"Dinely: Your Dinely code is ••••••"`) {
		t.Fatalf("code not masked:\n%s", text)
	}
	for _, e := range res.Screen.Elements {
		if strings.Contains(e.Label, "••••••") && !e.Masked {
			t.Fatalf("masked element not marked: %+v", e)
		}
	}
	// Waiting for the code does not find it.
	w := s.WaitFor(ctx, WaitForRequest{Text: "482913", TimeoutMS: 50})
	if w.Matched == nil || *w.Matched {
		t.Fatalf("wait_for found the code: %+v", w)
	}

	// An iPhone banner that OCR reads in two lines, over another app.
	dev.Show("settings")
	dev.SetOverlay([]screen.Element{
		{Role: screen.RoleText, Label: "【某银行】您本次操作的验", Source: screen.SourceOCR, Bounds: screen.Rect{X: 80, Y: 170, W: 800, H: 44}},
		{Role: screen.RoleText, Label: "证码为771234，请勿泄露。", Source: screen.SourceOCR, Bounds: screen.Rect{X: 80, Y: 222, W: 780, H: 44}},
	})
	res = s.Observe(ctx, ObserveRequest{})
	if text := res.Screen.Render(); strings.Contains(text, "771234") || !strings.Contains(text, `ocr "证码为••••••，请勿泄露。"`) {
		t.Fatalf("banner code not masked:\n%s", text)
	}

	// With Hide codes from agents off, the agent reads codes.
	if err := g.policy.SetHideCodes(false); err != nil {
		t.Fatal(err)
	}
	dev.SetOverlay(nil)
	dev.Show("messages")
	res = s.Observe(ctx, ObserveRequest{})
	if text := res.Screen.Render(); !strings.Contains(text, "482913") {
		t.Fatalf("code masked with the setting off:\n%s", text)
	}
}

func TestScreenshotCoversCodes(t *testing.T) {
	g, dev := newTestGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Read my messages")
	dev.Show("messages")
	res := s.Observe(ctx, ObserveRequest{Screenshot: true})
	mustStatus(t, res, StatusSettled)
	if res.Screenshot == nil {
		t.Fatalf("no screenshot: %s", res.Message)
	}
	if !covered(res.Screenshot.At(codeRow.X, codeRow.Y)) {
		t.Fatalf("code not covered: %v", res.Screenshot.At(codeRow.X, codeRow.Y))
	}
	if covered(res.Screenshot.At(100, 52)) {
		t.Fatal("the clock is covered")
	}
	if !strings.Contains(res.Message, "One-time codes on the screen are covered in the screenshot.") {
		t.Fatalf("message: %q", res.Message)
	}

	// No code: nothing covered, nothing said.
	dev.Show("settings")
	res = s.Observe(ctx, ObserveRequest{Screenshot: true})
	if res.Screenshot == nil || covered(res.Screenshot.At(codeRow.X, codeRow.Y)) || strings.Contains(res.Message, "covered") {
		t.Fatalf("covered without a code: %q", res.Message)
	}
}

// A code that only the picture shows (in an image, or come between reads)
// is covered where this computer reads it.
func TestScreenshotCoversCodesOnlyThePictureShows(t *testing.T) {
	g, dev := newTestGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Check settings")
	dev.Show("settings")
	pictureText(t,
		ocr.Line{Text: "Settings", Confidence: 1, X: 0.05, Y: 0.09, W: 0.3, H: 0.03},
		ocr.Line{Text: "验证码 482913", Confidence: 1, X: 0.1, Y: 0.8, W: 0.5, H: 0.03},
	)
	res := s.Observe(ctx, ObserveRequest{Screenshot: true})
	if res.Screenshot == nil {
		t.Fatalf("no screenshot: %s", res.Message)
	}
	if !covered(res.Screenshot.At(380, 1956)) {
		t.Fatal("the code the picture shows is not covered")
	}
	if covered(res.Screenshot.At(200, 252)) {
		t.Fatal("text without a code is covered")
	}
	if text := res.Screen.Render(); strings.Contains(text, "验证码") {
		t.Fatalf("the picture's text reached the element list:\n%s", text)
	}
}

// A code a read found but could not place withholds the screenshot unless
// the picture can be read.
func TestScreenshotWithheldWhenACodeHasNoPlace(t *testing.T) {
	g, dev := newTestGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Check settings")
	dev.Show("settings")
	dev.SetOverlay([]screen.Element{{Role: screen.RoleText, Label: "Your code is 482913", Source: screen.SourceTree}})
	res := s.Observe(ctx, ObserveRequest{Screenshot: true})
	mustStatus(t, res, StatusSettled)
	if res.Screenshot != nil || !strings.Contains(res.Message, "No screenshot: a one-time code is on the screen, and Pinky could not tell where to cover it.") {
		t.Fatalf("screenshot with an unplaced code: %q", res.Message)
	}
	if text := res.Screen.Render(); !strings.Contains(text, `"Your code is ••••••"`) {
		t.Fatalf("code not masked:\n%s", text)
	}

	pictureText(t, ocr.Line{Text: "Your code is 482913", Confidence: 1, X: 0.1, Y: 0.6, W: 0.5, H: 0.03})
	res = s.Observe(ctx, ObserveRequest{Screenshot: true})
	if res.Screenshot == nil || !covered(res.Screenshot.At(380, 1476)) {
		t.Fatalf("screenshot not given, or the code OCR placed not covered: %q", res.Message)
	}
}

// mediaImage reads a history screenshot.
func mediaImage(t *testing.T, g *Gateway, id string) image.Image {
	t.Helper()
	p, err := g.store.MediaPath(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// at scales a point of the 1080x2400 demo screen to a picture of it.
func at(img image.Image, p screen.Point) (int, int) {
	b := img.Bounds()
	return p.X * b.Dx() / 1080, p.Y * b.Dy() / 2400
}

func TestHistoryShotsCoverCodes(t *testing.T) {
	g, hd, _ := shotGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Read my messages")
	g.Flush()
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Messages"}), StatusSettled)
	g.Flush()
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled) // confirms the picture
	steps := toolSteps(t, g, "Read my messages", "phone_launch")
	if len(steps) != 1 || steps[0].After == "" {
		t.Fatalf("steps: %+v", steps)
	}
	img := mediaImage(t, g, steps[0].After)
	if x, y := at(img, codeRow); !covered(img.At(x, y)) {
		t.Fatalf("code not covered in history: %v", img.At(x, y))
	}
	if x, y := at(img, screen.Point{X: 100, Y: 52}); covered(img.At(x, y)) {
		t.Fatal("the clock is covered in history")
	}

	// A code with no place: the step keeps no picture.
	hd.Device.SetOverlay([]screen.Element{{Role: screen.RoleText, Label: "Your code is 771234", Source: screen.SourceTree}})
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	g.Flush()
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Messages"}), StatusSettled)
	g.Flush()
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled)
	steps = toolSteps(t, g, "Read my messages", "phone_launch")
	if len(steps) != 2 || steps[1].After != "" {
		t.Fatalf("a picture with an unplaced code was kept: %+v", steps)
	}
}

// The approval card shows the person the screen as it is; history keeps
// the picture with the code covered.
func TestApprovalPictureCoveredInHistoryOnly(t *testing.T) {
	g, hd, _ := shotGateway(t)
	ctx := context.Background()
	s, _ := connect(t, g, "Reply to Sam")
	hd.Device.Show("chat")
	code := screen.Element{Role: screen.RoleText, Label: "Sam: the door code is 4821", Source: screen.SourceTree,
		Bounds: screen.Rect{X: 40, Y: 1200, W: 1000, H: 100}}
	hd.Device.SetOverlay([]screen.Element{code})
	obs := s.Observe(ctx, ObserveRequest{})
	if text := obs.Screen.Render(); strings.Contains(text, "4821") {
		t.Fatalf("code not masked:\n%s", text)
	}
	done := make(chan *Result, 1)
	go func() { done <- tap(s, ref(t, obs, "Send")) }()
	n := waitNeed(t, g)
	if n.Screenshot == nil {
		t.Fatal("the approval card has no picture")
	}
	c := code.Bounds.Center()
	if covered(n.Screenshot.At(c.X, c.Y)) {
		t.Fatal("the person's approval card is covered")
	}
	if err := g.Decide(n.ID, Deny, ""); err != nil {
		t.Fatal(err)
	}
	<-done
	steps := toolSteps(t, g, "Reply to Sam", "phone_tap")
	if len(steps) != 1 || steps[0].Before == "" {
		t.Fatalf("steps: %+v", steps)
	}
	img := mediaImage(t, g, steps[0].Before)
	if x, y := at(img, c); !covered(img.At(x, y)) {
		t.Fatal("the approval's picture is not covered in history")
	}
}

// TestPictureCodesWithThisComputersOCR draws code messages and other
// numbers in a system font of macOS, reads them with this computer's
// recognizer and checks what gets covered.
func TestPictureCodesWithThisComputersOCR(t *testing.T) {
	if ocr.Supported() != nil {
		t.Skip("no OCR here")
	}
	data, err := os.ReadFile("/System/Library/Fonts/Hiragino Sans GB.ttc")
	if err != nil {
		t.Skipf("no Chinese font: %v", err)
	}
	coll, err := opentype.ParseCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	f, err := coll.Font(0)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 40, DPI: 96})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	lines := []struct {
		text string
		code bool
	}{
		{"【京东】验证码：482913，5分钟内有效", true},
		{"订单 778899 已发货，客服 4008 123 456", false},
		{"Your Example verification code is 520734", true},
		{"Total: $1280.00, due November 1, 2026", false},
	}
	img := image.NewRGBA(image.Rect(0, 0, 1080, 900))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.Black, Face: face}
	for i, l := range lines {
		d.Dot = fixed.P(40, 160*(i+1))
		d.DrawString(l.text)
	}
	readPicture = readPictureHere
	t.Cleanup(func() { readPicture = noPictureText })
	boxes, err := pictureCodes(img)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range lines {
		// A point in the middle of the line's letters.
		p := image.Pt(300, 160*(i+1)-14)
		in := false
		for _, b := range boxes {
			in = in || p.In(b)
		}
		if in != l.code {
			read, _ := readPictureHere(img)
			t.Errorf("%q: covered %v, want %v (boxes %v, read %+v)", l.text, in, l.code, boxes, read)
		}
	}
}
