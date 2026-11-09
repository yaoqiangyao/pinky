package netmux

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"howett.net/plist"
)

// The lockdown heartbeat. A usbmuxd that lists a phone on the network keeps
// a connection to the phone's com.apple.mobile.heartbeat service, and
// answers each "Marco" the phone sends with "Polo"; macOS's usbmuxd, Apple's
// on Windows and netmuxd all do. Without it the phone still answers
// lockdown and some services (syslog_relay), but closes AMFI, the image
// mounter and CoreDeviceProxy right after the TLS handshake, so Pinky can
// neither check Developer Mode nor start the tunnel (iPhone 18 Pro Max, iOS
// 27.2, on Windows, 2026-11-09: with the heartbeat answered, the same
// requests worked at once). So the Mux keeps one for every phone it lists,
// for as long as it lists it at that address.

// heartbeatRetry is the longest wait before a heartbeat that ended or could
// not start is started again; it doubles from a second while it keeps
// failing.
const heartbeatRetry = 30 * time.Second

// heartbeatService is the phone's heartbeat service.
const heartbeatService = "com.apple.mobile.heartbeat"

// heartbeatConn is a connection to a phone's heartbeat service.
type heartbeatConn interface {
	Reader() io.Reader
	Writer() io.Writer
	Close() error
}

// connectHeartbeat starts the heartbeat service on the phone listed with
// DeviceID id, through the usbmuxd go-ios uses (this Mux, once
// USBMUXD_SOCKET_ADDRESS points at it).
func connectHeartbeat(p Phone, id int) (heartbeatConn, error) {
	e := goios.DeviceEntry{DeviceID: id, Properties: goios.DeviceProperties{SerialNumber: p.UDID, ConnectionType: "Network"}}
	c, err := goios.ConnectToService(e, heartbeatService)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// heartbeat keeps the heartbeat of the phone listed with DeviceID id going
// until ctx ends.
func (m *Mux) heartbeat(ctx context.Context, p Phone, id int) {
	connect := m.HeartbeatFn
	if connect == nil {
		connect = connectHeartbeat
	}
	wait := time.Second
	for ctx.Err() == nil {
		c, err := connect(p, id)
		if err == nil {
			start := time.Now()
			stop := context.AfterFunc(ctx, func() { c.Close() })
			err = answerHeartbeat(c)
			stop()
			c.Close()
			if time.Since(start) > heartbeatRetry {
				wait = time.Second // it ran for a while: start again soon
			}
		}
		if ctx.Err() != nil {
			return
		}
		m.log.Debug("netmux: the phone's heartbeat ended; starting it again", "device", p.UDID, "err", err, "in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(2*wait, heartbeatRetry)
	}
}

// answerHeartbeat answers the phone's "Marco"s with "Polo" until the
// connection ends.
func answerHeartbeat(c heartbeatConn) error {
	polo, err := plist.Marshal(map[string]any{"Command": "Polo"}, plist.XMLFormat)
	if err != nil {
		return err
	}
	msg := binary.BigEndian.AppendUint32(nil, uint32(len(polo)))
	msg = append(msg, polo...)
	for {
		var n uint32
		if err := binary.Read(c.Reader(), binary.BigEndian, &n); err != nil {
			return err
		}
		if n > 1<<20 {
			return fmt.Errorf("heartbeat message of %d bytes", n)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.Reader(), body); err != nil {
			return err
		}
		var m struct{ Command string }
		if _, err := plist.Unmarshal(body, &m); err != nil {
			return err
		}
		if m.Command != "Marco" {
			continue // "SleepyTime" and the like: nothing to answer
		}
		if _, err := c.Writer().Write(msg); err != nil {
			return err
		}
	}
}
