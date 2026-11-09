package gateway

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/DIYgod/pinky/internal/ocr"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// One-time codes in pictures of the screen, while "Hide codes from
// agents" is on. Reads come with their codes masked (policy.MaskScreen, in
// observeOCR), and the elements masked keep their places, so pictures get
// those places covered:
//
//   - The screenshot an agent asks for (phone_observe) is covered where the
//     reads just before and after it found codes and, where this computer
//     reads text from pictures, where the picture itself shows one: a code
//     in an image, or one that came between the reads. When a read found a
//     code it could not place and the picture cannot be read, the agent gets
//     no screenshot.
//   - History screenshots are covered where the reads they belong to found
//     codes; one with a code whose place is unknown is not kept.
//   - The approval card's picture is the person's own and is not covered;
//     history keeps it covered.
//   - Recordings are not covered: they stay on this computer, are never
//     sent to agents, and show the screen as it was.

// codeFill is the color of the boxes over codes.
var codeFill = color.RGBA{0x8E, 0x8E, 0x93, 0xFF}

// errCodeShot means a read found a one-time code but not where it is.
var errCodeShot = errors.New("a one-time code is on the screen, and Pinky could not tell where to cover it")

// readPicture reads the text in a picture. Tests replace it.
var readPicture = readPictureHere

// readPictureHere reads the text in a picture with this computer's
// recognizer, where it has one.
func readPictureHere(img image.Image) ([]ocr.Line, error) {
	if err := ocr.Supported(); err != nil {
		return nil, err
	}
	return ocr.RecognizeImage(img, ocr.Options{Languages: ocr.Languages})
}

// coverCodes covers the one-time codes that reads of the screen found in a
// picture of it, and with read, those that the picture itself shows where
// this computer can read it. It returns the picture (a copy when anything
// is covered) and how many boxes it covered; ok is false when a code could
// not be placed, and the picture must not be used.
func (g *Gateway) coverCodes(img image.Image, read bool, reads ...*screen.Screen) (out image.Image, covered int, ok bool) {
	if img == nil || !g.policy.HideCodes() {
		return img, 0, true
	}
	b := img.Bounds()
	boxes, unplaced := codePlaces(b.Dx(), b.Dy(), reads...)
	if read || unplaced {
		more, err := pictureCodes(img)
		switch {
		case err == nil:
			// What the picture shows of a code a read could not place is
			// covered now, as far as text can be read from it.
			boxes, unplaced = append(boxes, more...), false
		case unplaced:
			return nil, 0, false
		}
	}
	if len(boxes) == 0 {
		return img, 0, true
	}
	return coverBoxes(img, boxes), len(boxes), true
}

// codePlaces returns where reads found one-time codes (their masked
// elements), in the pixels of a w x h picture of the screen, and whether a
// code had no place.
func codePlaces(w, h int, reads ...*screen.Screen) (boxes []image.Rectangle, unplaced bool) {
	for _, s := range reads {
		if s == nil {
			continue
		}
		for _, e := range s.Elements {
			if !e.Masked {
				continue
			}
			if e.Bounds.Empty() || s.Width <= 0 || s.Height <= 0 {
				unplaced = true
				continue
			}
			if r := scaleBox(e.Bounds, s.Width, s.Height, w, h); !r.Empty() {
				boxes = append(boxes, r)
			}
		}
	}
	return boxes, unplaced
}

// scaleBox turns an element's bounds on a sw x sh screen into a box on a w
// x h picture of it, a little larger, since OCR boxes hug the letters.
func scaleBox(r screen.Rect, sw, sh, w, h int) image.Rectangle {
	pad := max(2, r.H/8)
	x0, y0 := (r.X-pad)*w/sw, (r.Y-pad)*h/sh
	x1, y1 := ((r.X+r.W+pad)*w+sw-1)/sw, ((r.Y+r.H+pad)*h+sh-1)/sh
	return image.Rect(x0, y0, x1, y1).Intersect(image.Rect(0, 0, w, h))
}

// pictureCodes reads a picture and returns where it shows one-time codes,
// by the rules of the screen's text (policy.MaskElements).
func pictureCodes(img image.Image) ([]image.Rectangle, error) {
	lines, err := readPicture(img)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	els := make([]screen.Element, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		els = append(els, screen.Element{Role: screen.RoleText, Label: l.Text, Source: screen.SourceOCR, Bounds: ocr.Rect(l, w, h)})
	}
	els, n := policy.MaskElements(els)
	if n == 0 {
		return nil, nil
	}
	boxes, _ := codePlaces(w, h, &screen.Screen{Width: w, Height: h, Elements: els})
	return boxes, nil
}

// coverBoxes returns a copy of img with the boxes, in its pixels from its
// top-left corner, filled.
func coverBoxes(img image.Image, boxes []image.Rectangle) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, img, b.Min, draw.Src)
	for _, r := range boxes {
		draw.Draw(out, r.Add(b.Min).Intersect(b), &image.Uniform{codeFill}, image.Point{}, draw.Src)
	}
	return out
}
