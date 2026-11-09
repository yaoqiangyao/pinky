package android

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// Phones the person removed from Pinky (driver.Forgetter). The gateway
// keeps them off its list; the driver stops connecting to them by itself
// (the Wi-Fi reconnects, Config.ReconnectWiFi) and, as the person removes
// one, drops the connections over the network that it can. It leaves
// adb's own files alone: the phone stays paired with this computer, which
// other tools that share the adb server may rely on, and pairing it again
// in Pinky, or plugging it in, adds it back.

// Forget implements driver.Forgetter.
func (p *Provider) Forget(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	if p.forgotten == nil {
		p.forgotten = map[string]bool{}
	}
	p.forgotten[id] = true
	p.mu.Unlock()
}

// Remember implements driver.Forgetter.
func (p *Provider) Remember(id string) {
	p.mu.Lock()
	delete(p.forgotten, id)
	p.mu.Unlock()
}

// forgot reports whether the person removed the phone with this id.
func (p *Provider) forgot(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return id != "" && p.forgotten[id]
}

// Disconnect implements driver.Forgetter. It drops the phone's connections
// over Wi-Fi that were made by address: by the driver, reconnecting it or
// for ConnectWiFi and pairings, or by anyone else (`adb connect`). One the
// adb server's own discovery made stays, since the server cannot drop it by
// name (adb.Client.Disconnect) and would make it again; Pinky just no
// longer uses it. A USB connection stays too. A phone Pinky reached over
// the network without Wireless debugging (network.go) is forgotten there,
// and its adb stops listening, when the phone can be reached.
func (p *Provider) Disconnect(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if n, ok := p.net.get(id); ok {
		if err := p.net.update(id, func(m *netPhone) { *m = netPhone{} }); err != nil {
			p.cfg.logf("android: %s: forgetting its addresses: %v", id, err)
		}
		if err := p.stopListening(ctx, id, n); err != nil {
			p.cfg.logf("android: %s: %v", id, err)
		}
	}
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return wrapErr(err)
	}
	var errs []error
	for _, e := range entries {
		if !e.Wireless() || !p.connectionOf(ctx, e, id) {
			continue
		}
		if !isAddr(e.Serial) {
			p.cfg.logf("android: %s stays connected to the adb server through %s, which the server made by itself; Pinky no longer uses it", id, e.Serial)
			continue
		}
		if err := p.cfg.Client.Disconnect(ctx, e.Serial); err != nil && !errors.Is(err, adb.ErrDeviceNotFound) {
			errs = append(errs, err)
			continue
		}
		p.cfg.logf("android: disconnected %s at %s", id, e.Serial)
		if p.wifi != nil {
			p.wifi.drop(e.Serial)
		}
	}
	return errors.Join(errs...)
}

// connectionOf reports whether an adb connection is the phone's: listed
// under its id (a Wi-Fi connection that is offline, which List names by
// its serial), named after the phone's guid by the server's discovery
// ("adb-<serial>-<suffix>._adb-tls-connect._tcp"), or one whose
// properties name the phone.
func (p *Provider) connectionOf(ctx context.Context, e adb.DeviceEntry, id string) bool {
	if e.Serial == id || guidSerial(strings.TrimSuffix(e.Serial, connectSuffix)) == id {
		return true
	}
	p.mu.Lock()
	pr, ok := p.props[e.Serial+"#"+e.TransportID]
	p.mu.Unlock()
	if !ok && e.State == adb.StateDevice {
		var err error
		if pr, err = p.deviceProps(ctx, e); err != nil {
			return false
		}
		ok = true
	}
	return ok && p.phoneID(e, pr) == id
}

// guidSerial returns the phone's hardware serial in its wireless debugging
// guid, "adb-<serial>-<suffix>", or "" for anything else.
func guidSerial(guid string) string {
	rest, ok := strings.CutPrefix(guid, "adb-")
	if !ok {
		return ""
	}
	i := strings.LastIndexByte(rest, '-')
	if i <= 0 {
		return ""
	}
	return rest[:i]
}

// isAddr reports whether an adb serial is a network address,
// "192.168.1.42:41000" or "[fe80::1]:5555".
func isAddr(serial string) bool {
	host, port, err := net.SplitHostPort(serial)
	return err == nil && host != "" && port != ""
}

var _ driver.Forgetter = (*Provider)(nil)
