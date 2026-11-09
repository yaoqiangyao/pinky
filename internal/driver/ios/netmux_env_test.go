package ios

import (
	"os"
	"testing"
)

// TestForgetInheritedMux: at start, USBMUXD_SOCKET_ADDRESS is dropped when
// it is the address another Pinky set for its own usbmuxd, and kept when
// the person set it, also beside a stale mark.
func TestForgetInheritedMux(t *testing.T) {
	for _, c := range []struct {
		name, addr, own, want string
	}{
		{"inherited from a Pinky", "tcp://127.0.0.1:50123", "tcp://127.0.0.1:50123", ""},
		{"set by the person", "unix:///run/user/1000/netmuxd.sock", "", "unix:///run/user/1000/netmuxd.sock"},
		{"set by the person after a Pinky", "unix:///run/user/1000/netmuxd.sock", "tcp://127.0.0.1:50123", "unix:///run/user/1000/netmuxd.sock"},
		{"neither", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("USBMUXD_SOCKET_ADDRESS", c.addr)
			t.Setenv(ownMuxVar, c.own)
			forgetInheritedMux()
			if got := os.Getenv("USBMUXD_SOCKET_ADDRESS"); got != c.want {
				t.Errorf("USBMUXD_SOCKET_ADDRESS = %q, want %q", got, c.want)
			}
			if _, ok := os.LookupEnv(ownMuxVar); ok {
				t.Errorf("%s still set", ownMuxVar)
			}
		})
	}
}
