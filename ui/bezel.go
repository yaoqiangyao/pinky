package ui

import (
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// The bezel Pinky draws around a phone's screen takes the screen's shape
// the right way up, which the picture gives (mirror.aspect): portrait, or
// landscape when the phone is turned, an iPad's either way. Its corners
// round with the screen's short side, so they stay the same turned, and
// less on an iPad, whose corners are rounded less for its size. Pinky
// draws no notch or Dynamic Island: the picture shows the phone's own top,
// which turns with it.

// bezelKind is the kind of phone a bezel is drawn for.
type bezelKind int

const (
	bezelAndroid bezelKind = iota
	bezelIPhone
	bezelIPad
)

// bezelOf returns the kind of a device's bezel.
func bezelOf(d gateway.DeviceView) bezelKind {
	switch {
	case d.Info.Platform != driver.IOS:
		return bezelAndroid
	case strings.HasPrefix(d.Info.Model, "iPad"):
		return bezelIPad
	}
	return bezelIPhone
}

// defaultAspect is a screen's width over its height before its picture
// comes: a phone's usual portrait.
func (k bezelKind) defaultAspect() float32 {
	switch k {
	case bezelIPhone:
		return 1206.0 / 2622
	case bezelIPad:
		return 1668.0 / 2388
	}
	return 9.0 / 20
}

// cornerShare is a screen's corner radius as a share of its short side,
// after the prototype's phones: 42 for an iPhone's screen 282 wide, 38 for
// an Android phone's 276.
func (k bezelKind) cornerShare() float32 {
	switch k {
	case bezelIPhone:
		return 0.15
	case bezelIPad:
		return 0.045
	}
	return 0.137
}

// bezelRadius returns the radius of a screen sw×sh DIPs, the right way up,
// and of the bezel pad DIPs thick around it.
func bezelRadius(k bezelKind, sw, sh, pad float32) (outer, inner float32) {
	inner = max(4, min(sw, sh)*k.cornerShare())
	return inner + pad - 1, inner
}

// fitBezel returns the size of a phone, its bezel pad DIPs thick around a
// screen of aspect (width over height), that fits in w×h DIPs, and the
// screen's size.
func fitBezel(aspect, w, h, pad float32) (pw, ph, sw, sh float32) {
	if aspect <= 0 {
		aspect = 9.0 / 20
	}
	sw, sh = w-2*pad, (w-2*pad)/aspect
	if sh > h-2*pad {
		sh = h - 2*pad
		sw = sh * aspect
	}
	sw, sh = max(sw, 1), max(sh, 1)
	return sw + 2*pad, sh + 2*pad, sw, sh
}
