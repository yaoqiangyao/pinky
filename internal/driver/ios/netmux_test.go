//go:build !windows

// Pinky runs its own usbmuxd on Linux only (netmux.go); these tests serve
// it on a Unix socket in /tmp, which Windows does not have.

package ios

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/mdns"
	"github.com/DIYgod/pinky/internal/driver/ios/netmux"
)

type staticSource []mdns.Service

func (s staticSource) Services() []mdns.Service { return s }
func (s staticSource) Err() error               { return nil }
func (s staticSource) Close() error             { return nil }

func muxTestProvider(t *testing.T) *Provider {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pinkymux") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := NewProvider()
	p.WiFiMux = true
	p.nmux.anyOS = true
	p.nmux.dir = dir
	p.nmux.browseFn = func() (netmux.Source, error) {
		return staticSource{{Instance: "08:87:c7:2a:8a:db@fe80::1-supportsRP-24", Type: netmux.ServiceType, Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.20")}}}, nil
	}
	p.nmux.pairFn = func(udid string) (netmux.PairInfo, error) {
		return netmux.PairInfo{WiFiMAC: "08:87:c7:2a:8a:db"}, nil
	}
	p.nmux.probeFn = func(context.Context, netip.Addr) bool { return true }
	p.known["IPHONE"] = known{}
	return p
}

func TestWiFiMux(t *testing.T) {
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "")
	p := muxTestProvider(t)
	p.SetWiFi(true)
	if v := os.Getenv("USBMUXD_SOCKET_ADDRESS"); !strings.HasPrefix(v, "unix://"+p.nmux.dir) {
		t.Fatalf("USBMUXD_SOCKET_ADDRESS = %q", v)
	}
	if _, err := os.Stat(strings.TrimPrefix(os.Getenv("USBMUXD_SOCKET_ADDRESS"), "unix://")); err != nil {
		t.Fatal(err)
	}
	waitPhones := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for len(p.nmux.mux.Phones()) != n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := p.nmux.mux.Phones(); len(got) != n {
			t.Fatalf("phones = %v, want %d", got, n)
		}
	}
	waitPhones(1)
	if got := p.nmux.mux.Phones()[0]; got.UDID != "IPHONE" || got.Addr.String() != "192.168.1.20" {
		t.Errorf("phone = %+v", got)
	}
	// Wi-Fi off: the search stops and the phone leaves the list; the
	// usbmuxd stays for phones on a cable.
	p.SetWiFi(false)
	waitPhones(0)
	mux := p.nmux.mux
	p.SetWiFi(true)
	waitPhones(1)
	if p.nmux.mux != mux {
		t.Error("a second usbmuxd started")
	}
	p.SetWiFi(false)
	waitPhones(0)
}

func TestWiFiMuxLeavesAGivenUsbmuxd(t *testing.T) {
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "unix:///run/user/1000/netmuxd.sock")
	p := muxTestProvider(t)
	p.SetWiFi(true)
	if p.nmux.mux != nil || os.Getenv("USBMUXD_SOCKET_ADDRESS") != "unix:///run/user/1000/netmuxd.sock" {
		t.Errorf("started its own usbmuxd in place of the one given: %q", os.Getenv("USBMUXD_SOCKET_ADDRESS"))
	}
}

func TestWiFiMuxOnlyForTheApp(t *testing.T) {
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "")
	p := muxTestProvider(t)
	p.WiFiMux = false
	p.SetWiFi(true)
	if p.nmux.mux != nil || os.Getenv("USBMUXD_SOCKET_ADDRESS") != "" {
		t.Error("a provider without WiFiMux started a usbmuxd")
	}
}

func TestPairedUDIDsOnly(t *testing.T) {
	p := NewProvider()
	p.known["A"] = known{}
	p.wifi.use = map[string]bool{"B": true}
	got := strings.Join(p.pairedUDIDs(), ",")
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
		t.Errorf("UDIDs = %q", got)
	}
	p.Only = map[string]bool{"B": true}
	if got := strings.Join(p.pairedUDIDs(), ","); got != "B" {
		t.Errorf("UDIDs under Only = %q", got)
	}
}
