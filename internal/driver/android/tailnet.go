package android

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/tailnet"
)

// Phones away from the computer's network, through Tailscale
// (driver.Tailnet): the phone's adb listens on the network (network.go)
// and the driver reaches it at its Tailscale address.

// tailnetStatus reads Tailscale on this computer (Config.Tailnet).
func (p *Provider) tailnetStatus(ctx context.Context) (tailnet.Status, error) {
	st, err := p.cfg.Tailnet(ctx)
	if errors.Is(err, tailnet.ErrNotInstalled) {
		return tailnet.Status{State: tailnet.NotInstalled}, nil
	}
	return st, err
}

// Tailnet implements driver.Tailnet.
func (p *Provider) Tailnet(ctx context.Context) (driver.TailnetStatus, error) {
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return driver.TailnetStatus{}, err
	}
	out := driver.TailnetStatus{State: string(st.State), Detail: st.Detail, Tailnet: st.Tailnet, Self: st.Self.Name}
	entries, _ := p.cfg.Client.Devices(ctx)
	kept := p.net.list()
	for _, d := range st.AndroidPeers() {
		addr := d.IPv4().String()
		ph := driver.TailnetPhone{Name: d.Name, Addr: addr, Online: d.Online, Relayed: d.Relayed()}
		if !d.Online {
			ph.LastSeen = d.LastSeen
		}
		for _, n := range kept {
			if n.Tailnet != "" && (n.Node == d.ID || n.Tailnet == addr) {
				ph.ID = n.ID
			}
		}
		for _, e := range entries {
			if e.State != adb.StateDevice || serialHost(e.Serial) != addr {
				continue
			}
			ph.Connected = true
			if ph.ID == "" {
				if pr, err := p.deviceProps(ctx, e); err == nil {
					ph.ID = p.phoneID(e, pr)
				}
			}
		}
		out.Phones = append(out.Phones, ph)
	}
	return out, nil
}

// ConnectTailnet implements driver.Tailnet. The address is the phone's
// Tailscale address or name, with a port or without one (AwayPort): a
// phone set up for away from home listens there; one on Wi-Fi with
// Wireless debugging on also takes connections at the port Wireless
// debugging shows, through Tailscale too, when it is paired with this
// computer. A phone reached at AwayPort is kept as set up.
func (p *Provider) ConnectTailnet(ctx context.Context, addr string) (driver.Info, error) {
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return driver.Info{}, err
	}
	if !st.Running() {
		return driver.Info{}, fmt.Errorf("android: Tailscale is not running on this computer (%s): %w", st.State, driver.ErrUnsupported)
	}
	host, port, err := splitTailnetAddr(strings.TrimSpace(addr), st)
	if err != nil {
		return driver.Info{}, err
	}
	serial := net.JoinHostPort(host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := p.cfg.Client.Connect(ctx, serial); err != nil {
		return driver.Info{}, connectErr(serial, err)
	}
	info, err := p.waitConnected(ctx, serial)
	if err != nil {
		return info, err
	}
	if port == AwayPort && info.State == driver.StateOnline {
		node := ""
		if d, ok := st.Device(host); ok {
			node = d.ID
		}
		if err := p.net.update(info.ID, func(n *netPhone) {
			n.Name, n.Port, n.Node, n.Tailnet = info.Name, port, node, host
		}); err != nil {
			p.cfg.logf("android: keeping %s as set up for away from home: %v", info.ID, err)
		}
		p.link.soon()
	}
	return info, nil
}

// splitTailnetAddr reads an address typed or picked for ConnectTailnet: a
// Tailscale address or a device's name on the tailnet, with or without a
// port.
func splitTailnetAddr(addr string, st tailnet.Status) (string, int, error) {
	host, port := addr, AwayPort
	if h, ps, err := net.SplitHostPort(addr); err == nil {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("android: %q has no port such as 5555", addr)
		}
		host, port = h, n
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if !tailnet.Contains(a) {
			return "", 0, fmt.Errorf("android: %s is not a Tailscale address (100.64.0.0/10)", host)
		}
		return a.Unmap().String(), port, nil
	}
	// A name: the device's MagicDNS name or host name.
	for _, d := range st.Peers {
		dn := strings.ToLower(d.DNSName)
		h := strings.ToLower(strings.TrimSuffix(host, "."))
		if h != "" && (h == dn || h == strings.ToLower(d.Name) || strings.HasPrefix(dn, h+".")) && d.IPv4().IsValid() {
			return d.IPv4().String(), port, nil
		}
	}
	return "", 0, fmt.Errorf("android: no device named %q on the tailnet", host)
}

// oneVPN says why Tailscale may be off on a phone that has it.
const oneVPN = "Android runs one VPN at a time, so another VPN or proxy app on the phone turns Tailscale off."

// Away implements driver.Tailnet.
func (p *Provider) Away(ctx context.Context, id string) (driver.Away, error) {
	n, kept := p.net.get(id)
	on := kept && n.Tailnet != ""
	out := driver.Away{On: on, Port: n.port()}
	if on {
		out.Addr = n.Tailnet
	}
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return out, err
	}
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return out, wrapErr(err)
	}
	c := p.conns(ctx, entries, id)
	local := c.usb
	if local == "" {
		local = c.local
	}
	// other: the phone's Tailscale is on another tailnet. Its addresses
	// unknown here include one in Tailscale's own IPv6 range; one in
	// 100.64.0.0/10 alone may be another VPN's, as Cloudflare WARP's.
	other := false
	switch {
	case c.tail != "":
		out.Listening = true
		h, ps, _ := net.SplitHostPort(c.tail)
		out.Addr = h
		out.Port, _ = strconv.Atoi(ps)
	case local != "":
		if _, addrs, err := p.phoneAddrs(ctx, local); err == nil {
			// The first address the tailnet knows, IPv4 first; else the
			// first, to say it is another tailnet's.
			out.Addr = ""
			for _, a := range addrs {
				if _, known := st.Device(a.String()); known {
					out.Addr = a.String()
					break
				}
			}
			if out.Addr == "" && len(addrs) > 0 {
				out.Addr = addrs[0].String()
				other = slices.ContainsFunc(addrs, netip.Addr.Is6)
			}
		}
		out.Listening = p.listenPort(ctx, local) == out.Port
	}
	switch {
	case !st.Running():
		out.Missing = driver.AwayNoTailscaleHere
		out.Detail = "Tailscale is not running on this computer"
		if st.Detail != "" {
			out.Detail += " (" + st.Detail + ")"
		}
		out.Detail += "."
	case out.Addr == "" && local != "":
		out.Missing = driver.AwayNoTailscaleThere
		out.Detail = "Tailscale is not connected on the phone: install it there, or open it, and sign in to the same account as on this computer. " + oneVPN
	case out.Addr != "":
		if _, ok := st.Device(out.Addr); ok {
			break
		}
		if other {
			out.Missing = driver.AwayOtherTailnet
			out.Detail = "The phone's Tailscale is signed in to another tailnet than this computer's."
		} else {
			out.Missing = driver.AwayNoTailscaleThere
			out.Detail = "The phone's VPN is not this computer's tailnet: another VPN than Tailscale, or Tailscale on another tailnet. " + oneVPN
		}
	}
	if out.Missing == "" && local == "" && c.tail == "" && !on {
		out.Missing = driver.AwayNotConnected
		out.Detail = "Connect the phone to this computer by cable or Wi-Fi to set it up."
	}
	return out, nil
}

// SetAway implements driver.Tailnet. Off on a phone moved to Wi-Fi
// (SetPhoneWiFi) only stops reaching it through Tailscale: its adb keeps
// listening for the computer's network.
func (p *Provider) SetAway(ctx context.Context, id string, on bool) (driver.Away, error) {
	if !on {
		if err := p.awayOff(ctx, id); err != nil {
			return driver.Away{}, err
		}
		// adbd restarts: what the phone says may have to wait, and its
		// connection may be gone for a moment.
		a, err := p.Away(ctx, id)
		if err != nil {
			return driver.Away{Port: AwayPort}, nil
		}
		if a.Missing == driver.AwayNotConnected {
			a.Missing, a.Detail = "", ""
		}
		return a, nil
	}
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return driver.Away{}, err
	}
	if !st.Running() {
		return driver.Away{}, fmt.Errorf("android: Tailscale is not running on this computer (%s): %w", st.State, driver.ErrUnsupported)
	}
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return driver.Away{}, wrapErr(err)
	}
	c := p.conns(ctx, entries, id)
	local := c.usb
	if local == "" {
		local = c.local
	}
	name := id
	was := true // the phone's adb listened on the network before
	var addr netip.Addr
	switch {
	case c.tail != "":
		// Connected through Tailscale already: its adb listens there.
		h, ps, _ := net.SplitHostPort(c.tail)
		addr, _ = netip.ParseAddr(h)
		if n, _ := strconv.Atoi(ps); n != AwayPort {
			return driver.Away{}, fmt.Errorf("android: %s is connected through Tailscale at port %d, which changes when Wireless debugging turns on again; connect it by cable or on this computer's Wi-Fi to set it up: %w", id, n, driver.ErrUnsupported)
		}
	case local == "":
		return driver.Away{}, fmt.Errorf("android: %s: connect the phone by cable or Wi-Fi to set it up: %w", id, driver.ErrOffline)
	default:
		_, addrs, err := p.phoneAddrs(ctx, local)
		if err != nil {
			return driver.Away{}, err
		}
		for _, a := range addrs {
			if _, ok := st.Device(a.String()); ok && a.Is4() {
				addr = a
				break
			}
		}
		if !addr.IsValid() {
			return driver.Away{}, fmt.Errorf("android: %s is not on this computer's tailnet: install Tailscale on the phone and sign in to the same account: %w", id, driver.ErrUnsupported)
		}
		if pr, err := p.deviceProps(ctx, entryOf(entries, local)); err == nil {
			name = pr.Name
		}
		was = p.listenPort(ctx, local) == AwayPort
		if err := p.listen(ctx, id, local); err != nil {
			return driver.Away{}, err
		}
	}
	node := ""
	if d, ok := st.Device(addr.String()); ok {
		node = d.ID
	}
	if c.tail == "" {
		// adbd restarts: connect through Tailscale once it listens.
		if err := p.connectWhenUp(ctx, joinAddr(addr.String(), AwayPort)); err != nil {
			if !was {
				p.undoListen(ctx, id, local)
			}
			return driver.Away{}, err
		}
	}
	if err := p.net.update(id, func(n *netPhone) {
		n.Name, n.Port, n.Node, n.Tailnet = name, AwayPort, node, addr.String()
	}); err != nil {
		return driver.Away{}, err
	}
	p.link.soon()
	return p.Away(ctx, id)
}

// awayOff stops reaching a phone through Tailscale. A phone not moved to
// Wi-Fi also has its adb stop listening on the network (a phone that
// cannot be reached keeps listening until it restarts).
func (p *Provider) awayOff(ctx context.Context, id string) error {
	n, kept := p.net.get(id)
	if !kept || n.Tailnet == "" {
		// Not set up; still stop a listening adb Pinky reaches through
		// Tailscale, as the person asked.
		entries, err := p.cfg.Client.Devices(ctx)
		if err != nil {
			return wrapErr(err)
		}
		if c := p.conns(ctx, entries, id); c.tail != "" && n.LAN == "" {
			return p.stopListening(ctx, id, netPhone{ID: id, Tailnet: serialHost(c.tail)})
		}
		return nil
	}
	if err := p.net.update(id, func(m *netPhone) { m.Node, m.Tailnet = "", "" }); err != nil {
		return err
	}
	if n.LAN != "" {
		if err := p.cfg.Client.Disconnect(ctx, n.tailSerial()); err != nil && !errors.Is(err, adb.ErrDeviceNotFound) {
			p.cfg.logf("android: disconnecting %s: %v", n.tailSerial(), err)
		}
		return nil
	}
	return p.stopListening(ctx, id, n)
}

// entryOf returns the listed connection with this serial.
func entryOf(entries []adb.DeviceEntry, serial string) adb.DeviceEntry {
	for _, e := range entries {
		if e.Serial == serial {
			return e
		}
	}
	return adb.DeviceEntry{Serial: serial}
}

var _ driver.Tailnet = (*Provider)(nil)
