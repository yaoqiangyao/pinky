package ios

import "testing"

func TestParseScreenInfoBacklight(t *testing.T) {
	display := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"primary":            true,
			"bounds":             []any{[]any{0.0, 0.0}, []any{1179.0, 2556.0}},
			"pointScale":         3.0,
			"currentOrientation": "rot0",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	tests := []struct {
		name string
		out  map[string]any
		want string
	}{
		{"in the display (iOS 27.0)", map[string]any{
			"displays": []any{display(map[string]any{"backlightState": "activeOn"})},
		}, "activeOn"},
		{"at the top level (iOS 27.2)", map[string]any{
			"displays":       []any{display(nil)},
			"backlightState": "off",
		}, "off"},
		{"the display's wins", map[string]any{
			"displays":       []any{display(map[string]any{"backlightState": "activeDimmed"})},
			"backlightState": "off",
		}, "activeDimmed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			si, err := parseScreenInfo(tt.out)
			if err != nil {
				t.Fatal(err)
			}
			if si.Backlight != tt.want {
				t.Errorf("Backlight = %q, want %q", si.Backlight, tt.want)
			}
			if si.Width != 1179 || si.Height != 2556 || si.Scale != 3 || si.Orientation != "rot0" {
				t.Errorf("got %+v", si)
			}
		})
	}
}

// The iPad Pro's reply (iOS 27.0.1) held the usual way up: its panel is
// mounted landscape, so the interface is a quarter turn from the display.
func TestParseScreenInfoOrientation(t *testing.T) {
	out := map[string]any{
		"displays": []any{map[string]any{
			"primary":            true,
			"bounds":             []any{[]any{0.0, 0.0}, []any{2388.0, 1668.0}},
			"pointScale":         2.0,
			"currentOrientation": "rot90",
			"nativeOrientation":  "rot270",
		}},
		"backlightState": "activeOn",
	}
	si, err := parseScreenInfo(out)
	if err != nil {
		t.Fatal(err)
	}
	if si.Orientation != "rot90" || si.Native != "rot270" || si.Backlight != "activeOn" {
		t.Fatalf("got %+v", si)
	}
	if u := si.upright(); u.Width != 1668 || u.Height != 2388 {
		t.Fatalf("upright %dx%d", u.Width, u.Height)
	}
	if si.touchTurn() != 0 {
		t.Fatalf("touch turn %d: held the usual way up, the touch panel is the screen", si.touchTurn())
	}
}
