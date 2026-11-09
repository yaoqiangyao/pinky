package gateway

import (
	"context"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// CheckupView is a phone's checkup, for the client's checkup page: what
// works, what does not and what to do about it, item by item.
type CheckupView struct {
	DeviceID string
	Platform driver.Platform
	// Family and System name the maker's system the phone runs, as its
	// driver read them (driver.Checkup); empty while the phone is not
	// online.
	Family, System string
	Checks         []driver.Check
	At             time.Time
}

// Checkup runs a phone's checkup. Every phone gets the connection items:
// whether it is connected and whether it allows this computer. A phone
// that is online then gets its driver's own items (driver.Checker), which
// check for real what Pinky needs on it; for one that is not, they wait
// until it is.
func (g *Gateway) Checkup(ctx context.Context, deviceID string) (CheckupView, error) {
	d, err := g.device(deviceID)
	if err != nil {
		return CheckupView{}, err
	}
	g.mu.Lock()
	info, online, dev, prov := d.info, d.online, d.dev, d.provider
	g.mu.Unlock()
	v := CheckupView{DeviceID: deviceID, Platform: info.Platform, At: time.Now()}
	// A phone is open once it is online; one that went away while open
	// keeps the state it was last listed in (discover marks the others
	// offline).
	if info.State == driver.StateOnline && (!online || dev == nil) {
		info.State = driver.StateOffline
	}
	// What the computer lacks for the phone's platform comes first: the
	// adb server that keeps being replaced, the usbmuxd that is missing.
	if hc, ok := prov.(driver.HostChecker); ok {
		if h := hc.CheckHost(); h.Missing != "" {
			v.Checks = append(v.Checks, driver.Check{ID: driver.CheckHost, Status: driver.CheckFail, Fix: h.Missing, Detail: h.Detail})
		}
	}
	v.Checks = append(v.Checks, ConnectionChecks(info)...)
	if c, ok := WiFiCheck(info); ok {
		// Also while offline: a phone unplugged that does not show up on
		// Wi-Fi says here why.
		v.Checks = append(v.Checks, c)
	}
	if info.State != driver.StateOnline {
		return v, nil
	}
	ck, ok := dev.(driver.Checker)
	if !ok {
		return v, nil
	}
	cu, err := ck.Checkup(ctx)
	if err != nil {
		return v, err
	}
	v.Family, v.System = cu.Family, cu.System
	v.Checks = append(v.Checks, cu.Checks...)
	v.At = time.Now()
	return v, nil
}

// WiFiCheck is the Wi-Fi item of an iPhone's or iPad's checkup, from what
// its provider knows (Info.WiFi): whether the phone lets this computer
// reach it over Wi-Fi, whether Pinky turned that on, and whether this
// computer lists it on Wi-Fi now. ok is false for phones whose provider
// does not tell.
func WiFiCheck(info driver.Info) (driver.Check, bool) {
	w := info.WiFi
	if w.Connections == "" {
		return driver.Check{}, false
	}
	c := driver.Check{ID: driver.CheckWiFi, Detail: w.Detail}
	switch {
	case info.Connection == "wifi" || w.Seen:
		c.Status = driver.CheckPass
	case w.Connections == driver.WiFiConnectionsOn || w.Connections == driver.WiFiConnectionsTurnedOn:
		c.Status, c.Fix = driver.CheckWarn, driver.FixSameNetwork
	case w.Connections == driver.WiFiConnectionsFailed:
		c.Status, c.Fix = driver.CheckFail, driver.FixReconnect
	case w.Connections == driver.WiFiConnectionsOff:
		c.Status, c.Fix = driver.CheckSkip, driver.FixWiFiSetting
	default:
		c.Status = driver.CheckSkip
	}
	return c, true
}

// ConnectionChecks are the items every checkup starts with, from what the
// phone's driver lists it as: whether it is connected, and whether it
// allows this computer. The other items wait while either fails. The fix
// of a phone that waits for a step is that step (Info.Setup).
func ConnectionChecks(info driver.Info) []driver.Check {
	conn := driver.Check{ID: driver.CheckConnection, Status: driver.CheckPass, Detail: info.Connection}
	auth := driver.Check{ID: driver.CheckAuthorized, Status: driver.CheckPass}
	switch info.State {
	case driver.StateOffline:
		conn.Status, conn.Fix, conn.Detail = driver.CheckFail, driver.FixReconnect, info.Detail
		auth.Status = driver.CheckSkip
	case driver.StateUnauthorized:
		auth.Status, auth.Fix, auth.Detail = driver.CheckFail, driver.FixAuthorize, info.Detail
		if info.Setup != "" {
			auth.Fix = info.Setup
		}
	case driver.StateNeedsSetup:
		auth.Status, auth.Fix, auth.Detail = driver.CheckFail, info.Setup, info.Detail
	}
	return []driver.Check{conn, auth}
}
