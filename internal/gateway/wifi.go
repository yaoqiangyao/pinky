package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/history"
)

// Phones over Wi-Fi (driver.WiFiFinder: iPhones and iPads that usbmuxd lists
// over the network). Such a provider leaves a phone it reaches only over
// Wi-Fi alone until the gateway says the phone is the person's, which it is
// when Pinky has seen it before (the history store's devices: phones Pinky
// opened, and phones listed on a cable), when it is listed on a cable now,
// and when the person chose to use it (UseFoundPhone). Any other phone the
// provider finds on Wi-Fi waits, with driver.SetupConfirmWiFi, for the
// person to choose Use, or Ignore (IgnoreFoundPhone), which removes it as
// RemoveDevice does, so it stays off the list until it is plugged in.
// Agents are not told about such a phone.
//
// A phone that moves between its cable and Wi-Fi, as its cable is pulled or
// plugged in again, stays one phone with its settings and its task: the
// gateway closes its session over the old connection and opens it over the
// new one at once, and agents' calls wait for it meanwhile (moveWait)
// instead of failing.

// moveWait is how long calls wait for a phone that is moving between its
// cable and Wi-Fi. Opening an iPhone over Wi-Fi takes a few seconds.
const moveWait = 20 * time.Second

// dropWait is how long calls wait for a phone whose session over a cable
// dropped, when the person uses it over Wi-Fi too: until the next
// discovery finds it there, when the wait becomes moveWait, or not.
const dropWait = 6 * time.Second

// closeWait bounds how long opening a phone waits for its last session to
// close, so the new one does not start its video stream while the old one
// still stops its own.
const closeWait = 6 * time.Second

// SetWiFi turns using phones over Wi-Fi on or off, for the providers that
// can (Settings › Phones). Off, they list phones on a cable only: a phone on
// Wi-Fi goes offline, and its task waits for it as for any disconnection.
func (g *Gateway) SetWiFi(on bool) {
	g.mu.Lock()
	changed := g.wifiOn != on
	g.wifiOn = on
	g.mu.Unlock()
	for _, p := range g.opt.Providers {
		if w, ok := p.(driver.WiFiFinder); ok {
			w.SetWiFi(on)
		}
	}
	if changed {
		g.log.Info("phones over Wi-Fi", "on", on)
		g.discoverSoon()
	}
}

// discoverSoon runs a discovery pass in the background, so a change shows
// without waiting for the next one.
func (g *Gateway) discoverSoon() {
	go func() {
		ctx, cancel := context.WithTimeout(g.ctx, reconnectTimeout)
		defer cancel()
		g.discover(ctx)
	}()
}

// UseFoundPhone lets Pinky use a phone it found on Wi-Fi, one listed with
// driver.SetupConfirmWiFi: the person said it is theirs. Its provider
// prepares it at once (Developer Mode, the developer disk image), and the
// gateway opens it once it is ready. The choice is saved.
func (g *Gateway) UseFoundPhone(deviceID string) error {
	g.mu.Lock()
	d := g.devices[deviceID]
	if d == nil {
		g.mu.Unlock()
		return ErrUnknownDevice
	}
	if _, ok := d.provider.(driver.WiFiFinder); !ok {
		g.mu.Unlock()
		return fmt.Errorf("%s: %w", d.name(), driver.ErrUnsupported)
	}
	g.knowLocked(d)
	g.mu.Unlock()
	g.log.Info("found phone used", "device", d.id, "name", d.name())
	g.emit(Event{Kind: EventDevices, DeviceID: d.id})
	g.discoverSoon()
	return nil
}

// IgnoreFoundPhone leaves a phone Pinky found on Wi-Fi alone: it is removed
// as by RemoveDevice (it has no history), so it stays off the list while it
// is on Wi-Fi, and comes back once it is plugged in.
func (g *Gateway) IgnoreFoundPhone(ctx context.Context, deviceID string) error {
	g.mu.Lock()
	d := g.devices[deviceID]
	g.mu.Unlock()
	if d == nil {
		return ErrUnknownDevice
	}
	g.log.Info("found phone ignored", "device", deviceID, "name", d.name())
	return g.RemoveDevice(ctx, deviceID, RemoveOptions{})
}

// lockedOnWiFi reports whether the phone waits to be unlocked on Wi-Fi,
// where Pinky connects to it only once it is unlocked (driver.WiFiFinder:
// opening one that sleeps lights its screen). It waits for a step, as
// waitsForStep: agents can lease it and hand it to the person. The caller
// holds g.mu.
func lockedOnWiFi(d *device) bool {
	return !d.online && d.info.State == driver.StateNeedsSetup && d.info.Setup == driver.SetupUnlock && d.info.Connection == "wifi"
}

// foundLocked reports whether the phone waits for the person to say
// whether it is theirs. The caller holds g.mu.
func foundLocked(d *device) bool {
	return !d.online && d.info.State == driver.StateNeedsSetup && d.info.Setup == driver.SetupConfirmWiFi
}

// knowLocked records that a phone is the person's, so its provider
// prepares and opens it over Wi-Fi too, and saves it among the phones
// Pinky has seen. Only for providers that reach phones over Wi-Fi by
// themselves. The caller holds g.mu.
func (g *Gateway) knowLocked(d *device) {
	w, ok := d.provider.(driver.WiFiFinder)
	if !ok || g.known[d.id] {
		return
	}
	g.known[d.id] = true
	w.UseOverWiFi(d.id)
	if g.store != nil {
		info := d.info
		g.rec.do(func(ctx context.Context) error {
			return g.store.SaveDevice(ctx, history.Device{ID: info.ID, Platform: string(info.Platform), Name: info.Name,
				Model: info.Model, OSVersion: info.OSVersion, Backend: info.Backend, LastSeen: time.Now()})
		})
	}
}

// loadKnown reads the phones Pinky has seen and tells the providers that
// reach phones over Wi-Fi by themselves that they are the person's, before
// the first discovery.
func (g *Gateway) loadKnown() {
	recs, err := g.store.SeenDevices(context.Background())
	if err != nil {
		g.log.Warn("history: devices", "err", err)
		return
	}
	for _, rec := range recs {
		id := rec.ID
		g.known[id] = true
		g.names[id] = rec
		for _, p := range g.opt.Providers {
			if w, ok := p.(driver.WiFiFinder); ok {
				w.UseOverWiFi(id)
			}
		}
	}
}

// movingWhy says where a phone that moves between its cable and Wi-Fi
// goes, for the log and its offline line meanwhile.
func movingWhy(to string) string {
	if to == "wifi" {
		return "moving to Wi-Fi"
	}
	return "moving to the cable"
}

// connectingLine is the status line of a phone being opened: "Moving to
// Wi-Fi" or "Moving to the cable" while it moves, else "Connecting". The
// caller holds g.mu.
func connectingLine(d *device) string {
	if time.Now().Before(d.moving) {
		if d.info.Connection == "wifi" {
			return "Moving to Wi-Fi"
		}
		return "Moving to the cable"
	}
	return "Connecting"
}

// connectionOf is the connection a phone opened over: listed, the one its
// provider listed as Open began (Pinky's drivers pick a phone's connection
// the same way for List and Open), or the session's word when the listing
// named none. A move is the listing naming another connection than this one
// (updateDevice), so it comes from the listing too: a driver whose sessions
// name another connection than its List (a test's fake listing a demo phone
// on Wi-Fi) would else be moved, and opened again, at every discovery.
func connectionOf(listed string, dev driver.Device) string {
	if listed != "" {
		return listed
	}
	return dev.Info().Connection
}

// connectWait is how long calls wait for a phone Pinky is opening: one
// that dropped and is back, listed and shown as Connecting. An Android
// phone opens in a second or two, an iPhone over Wi-Fi in a few. Before,
// such calls got device_offline though the phone was back, which
// TestOfflineAndReconnect ran into on Windows, where opening is slower.
const connectWait = 20 * time.Second

// backBy is until when calls wait for a phone that is offline: while it
// moves between its cable and Wi-Fi, or while Pinky opens it. The caller
// holds g.mu.
func backBy(d *device) time.Time {
	until := d.moving
	if d.opening && d.info.State == driver.StateOnline {
		if t := d.openingAt.Add(connectWait); t.After(until) {
			until = t
		}
	}
	return until
}

// expectedBack reports whether a phone that is offline is expected back in
// a moment: it moves between its cable and Wi-Fi, or Pinky opens it.
func (g *Gateway) expectedBack(d *device) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !d.online && time.Now().Before(backBy(d))
}

// waitMovingLocked waits, while a phone moves between its cable and Wi-Fi
// or Pinky opens it (backBy), until it is back, the wait is over, the open
// failed or ctx ends. It reports whether it waited; the caller then looks
// at the phone again. The caller holds g.mu, which is released while
// waiting and held again on return.
func (g *Gateway) waitMovingLocked(ctx context.Context, d *device) bool {
	until := backBy(d)
	if d.online || !time.Now().Before(until) {
		return false
	}
	ch := d.wake
	g.mu.Unlock()
	t := time.NewTimer(time.Until(until))
	select {
	case <-ch:
	case <-t.C:
	case <-ctx.Done():
	}
	t.Stop()
	g.mu.Lock()
	return ctx.Err() == nil
}

// RestartDeviceService restarts the computer's service a phone goes through,
// when its provider can (driver.DeviceServiceRestarter: usbmuxd, which can
// lose an iPhone on Wi-Fi), as the person asked on the phone's page. The
// system asks for an administrator's password, showing reason; every phone of
// the platform disconnects for a moment, and tasks on them wait as for any
// short disconnection. It returns driver.ErrCanceled when the person
// declines.
func (g *Gateway) RestartDeviceService(ctx context.Context, deviceID, reason string) error {
	g.mu.Lock()
	d := g.devices[deviceID]
	var p driver.Provider
	if d != nil {
		p = d.provider // discovery sets it again under g.mu
	}
	g.mu.Unlock()
	if d == nil {
		return ErrUnknownDevice
	}
	r, ok := p.(driver.DeviceServiceRestarter)
	if !ok {
		return fmt.Errorf("%s: %w", d.name(), driver.ErrUnsupported)
	}
	g.log.Info("restarting the device service", "platform", p.Platform(), "for", deviceID)
	if err := r.RestartDeviceService(ctx, reason); err != nil {
		g.log.Warn("restart the device service", "err", err)
		return err
	}
	g.discoverSoon()
	return nil
}
