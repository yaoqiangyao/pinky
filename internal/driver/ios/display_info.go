package ios

import (
	"context"
	"fmt"
)

const (
	deviceInfoService     = "com.apple.coredevice.deviceinfo"
	featureGetDisplayInfo = "com.apple.coredevice.feature.getdisplayinfo"
)

// screenInfo describes the main display, as CoreDevice reads it.
//
// Orientation, measured on 2026-11-08 on an iPhone 13 mini and an iPad Pro
// 11-inch (3rd generation), iOS 27.0.1, in every orientation each allows:
//
//   - Width and Height are the display's own frame, the way its panel is
//     mounted, whatever way the device is turned: 1125x2436 on the iPhone,
//     2388x1668 on the iPad, whose panel is landscape.
//   - Orientation (currentOrientation) is how far the interface is turned
//     from that frame, clockwise: the video is in the display's frame and
//     turns upright by it.
//   - Native (nativeOrientation) is how far the display's frame is turned
//     from the device held the usual way up (rot0 on the iPhone, rot270 on
//     the iPad). The touch panel's 0..65535 range is in that upright-held
//     frame, so a touch turns by both.
//   - Screenshots and remoteAXService's frames are the interface's, the
//     right way up, and so are Pinky's coordinates (screen).
type screenInfo struct {
	// Width and Height are the size UIKit renders at (points times scale),
	// in the display's own frame. The panel itself may be slightly smaller
	// (1080x2340 on an iPhone 13 mini, against 1125x2436 rendered).
	Width, Height int
	Scale         float64 // pixels per point
	Orientation   string  // rot0, rot90, rot180, rot270: the interface's turn from the display's frame
	Native        string  // rot0, ...: the display's turn from the device held the usual way up
	Backlight     string  // activeOn, off, ...
}

// rotQuarter reads rot0, rot90, rot180 and rot270 as quarter turns
// clockwise; anything else is none.
func rotQuarter(rot string) quarter {
	switch rot {
	case "rot90":
		return 1
	case "rot180":
		return 2
	case "rot270":
		return 3
	}
	return 0
}

// turn is how far the interface is turned from the display's frame,
// clockwise: what puts the video upright.
func (s screenInfo) turn() quarter { return rotQuarter(s.Orientation) }

// touchTurn is how far the interface is turned from the touch panel's
// frame, the device held the usual way up.
func (s screenInfo) touchTurn() quarter {
	return (rotQuarter(s.Native) + s.turn()) & 3
}

// upright returns the screen the right way up: the size swapped when the
// interface is turned a quarter from the display's frame.
func (s screenInfo) upright() screenInfo {
	s.Width, s.Height = turnSize(s.Width, s.Height, s.turn())
	return s
}

// orientationOf names the orientation of a screen the right way up.
func orientationOf(w, h int) string {
	if w > h {
		return "landscape"
	}
	return "portrait"
}

// readDisplay returns the main display's entry of CoreDevice's display
// info, as the phone sends it.
func readDisplay(ctx context.Context, svc *cdService) (map[string]any, error) {
	out, err := svc.invoke(ctx, featureGetDisplayInfo, "", nil)
	if err != nil {
		return nil, err
	}
	m, ok := primaryDisplay(out)
	if !ok {
		return nil, fmt.Errorf("display info: no main display")
	}
	return m, nil
}

// primaryDisplay finds the main display's entry in a getdisplayinfo reply.
func primaryDisplay(out map[string]any) (map[string]any, bool) {
	displays, _ := out["displays"].([]any)
	for _, d := range displays {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if p, _ := m["primary"].(bool); p {
			return m, true
		}
	}
	return nil, false
}

func readScreenInfo(ctx context.Context, svc *cdService) (screenInfo, error) {
	out, err := svc.invoke(ctx, featureGetDisplayInfo, "", nil)
	if err != nil {
		return screenInfo{}, err
	}
	return parseScreenInfo(out)
}

// parseScreenInfo reads the main display from a getdisplayinfo reply.
func parseScreenInfo(out map[string]any) (screenInfo, error) {
	m, ok := primaryDisplay(out)
	if !ok {
		return screenInfo{}, fmt.Errorf("display info: no main display")
	}
	si := screenInfo{Scale: 1}
	if b, ok := m["bounds"].([]any); ok && len(b) == 2 {
		if wh, ok := b[1].([]any); ok && len(wh) == 2 {
			si.Width, si.Height = int(toFloat(wh[0])), int(toFloat(wh[1]))
		}
	}
	if s := toFloat(m["pointScale"]); s > 0 {
		si.Scale = s
	}
	si.Orientation, _ = m["currentOrientation"].(string)
	si.Native, _ = m["nativeOrientation"].(string)
	si.Backlight, _ = m["backlightState"].(string)
	if si.Backlight == "" {
		// iOS 27.2 moved it next to "displays".
		si.Backlight, _ = out["backlightState"].(string)
	}
	if si.Width <= 0 || si.Height <= 0 {
		return si, fmt.Errorf("display info: no size for the main display")
	}
	return si, nil
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	default:
		return float64(toInt64(v))
	}
}
