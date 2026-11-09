package ios

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/mdns"
	goios "github.com/danielpaulus/go-ios/ios"
)

// fakeBonjour is a search that finds what the test puts on the network.
type fakeBonjour struct {
	mu       sync.Mutex
	services []mdns.Service
	closed   bool
}

func (f *fakeBonjour) Services() []mdns.Service {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mdns.Service(nil), f.services...)
}

func (f *fakeBonjour) Err() error { return nil }

func (f *fakeBonjour) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeBonjour) set(instances ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services = nil
	for _, in := range instances {
		f.services = append(f.services, mdns.Service{Instance: in, Type: mobdevService})
	}
}

// TestLostPhone: a phone the person uses that usbmuxd stopped listing but
// that shows up on the network for lostAfter is listed as lost, with its
// name; once usbmuxd lists it again it is not. Phones without a pair record
// (Android) are left out, and the search stops when nothing is missing.
func TestLostPhone(t *testing.T) {
	w := newWiFiProvider(entry("iphone", 7, "Network"))
	w.lost.anyOS = true
	net := &fakeBonjour{}
	browses := 0
	w.lost.browseFn = func() (bonjourSource, error) { browses++; return net, nil }
	w.lost.pairFn = func(udid string) (string, error) {
		if udid == "iphone" {
			return "08:87:C7:2A:8A:DB", nil
		}
		return "", errors.New("ReadPair failed with errorcode '22'")
	}
	w.SetWiFi(true)
	w.UseOverWiFi("iphone")
	w.UseOverWiFi("3e8be48e") // an Android phone the gateway knows
	if got := listed(t, w.Provider); got["iphone"].Name != "iphone" {
		t.Fatalf("listed by usbmuxd: %+v", got)
	}
	if browses != 0 {
		t.Fatal("searched while every phone was listed")
	}

	w.set() // usbmuxd lost it
	net.set("08:87:c7:2a:8a:db@fe80::a87:c7ff:fe2a:8adb-supportsRP-26", "c0:17:54:70:5d:7f@fe80::c217:54ff:fe70:5d7f-supportsRP-26")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("lost at once: %+v", got)
	}
	if browses != 1 {
		t.Fatalf("%d searches, want 1", browses)
	}
	w.after(lostAfter)
	got := listed(t, w.Provider)
	lost := got["iphone"]
	if lost.Setup != driver.SetupDeviceService || lost.State != driver.StateNeedsSetup || lost.Name != "iphone" || lost.Connection != "wifi" || len(got) != 1 {
		t.Fatalf("after lostAfter on the network: %+v", got)
	}

	// Gone from the network too: not lost, just away.
	net.set()
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("off the network: %+v", got)
	}
	net.set("08:87:c7:2a:8a:db@fe80::a87:c7ff:fe2a:8adb-supportsRP-26")
	listed(t, w.Provider)
	w.after(lostAfter - time.Second)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("back on the network for less than lostAfter: %+v", got)
	}

	// usbmuxd lists it again: the search ends lostIdle later.
	w.set(entry("iphone", 8, "Network"))
	listed(t, w.Provider)
	w.after(lostIdle)
	listed(t, w.Provider)
	if !net.closed {
		t.Fatal("the search went on with nothing missing")
	}
}

// TestRestartDeviceService runs usbmuxd's restart through the system's
// request for an administrator, and reads a declined one as canceled.
func TestRestartDeviceService(t *testing.T) {
	p := NewProvider()
	var got []string
	p.restartFn = func(_ context.Context, command, reason string) error {
		got = append(got, command, reason)
		return nil
	}
	if err := p.RestartDeviceService(context.Background(), "Pinky restarts the device service."); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "/usr/bin/killall usbmuxd" || got[1] != "Pinky restarts the device service." {
		t.Fatalf("ran %q", got)
	}
	if s := appleScriptString(`say "hi" \ bye`); s != `"say \"hi\" \\ bye"` {
		t.Fatalf("appleScriptString = %s", s)
	}
	p.restartFn = func(context.Context, string, string) error {
		return errors.New("restarting the device service: " + driver.ErrCanceled.Error())
	}
	if err := p.RestartDeviceService(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("declined: %v", err)
	}
}

// TestLostSearchPauses: a search that sees none of the missing phones on
// the network for lostQuiet pauses for lostPause, then looks again.
func TestLostSearchPauses(t *testing.T) {
	w := newWiFiProvider()
	w.lost.anyOS = true
	browses := 0
	var last *fakeBonjour
	w.lost.browseFn = func() (bonjourSource, error) { browses++; last = &fakeBonjour{}; return last, nil }
	w.lost.pairFn = func(string) (string, error) { return "08:87:c7:2a:8a:db", nil }
	w.SetWiFi(true)
	w.UseOverWiFi("iphone") // away from home: not on usbmuxd's list, not on the network
	listed(t, w.Provider)
	if browses != 1 {
		t.Fatalf("%d searches, want 1", browses)
	}
	w.after(lostQuiet)
	listed(t, w.Provider)
	if !last.closed {
		t.Fatal("the search went on with nothing to find")
	}
	w.after(lostPause - time.Second)
	listed(t, w.Provider)
	if browses != 1 {
		t.Fatal("searched again during the pause")
	}
	w.after(2 * time.Second)
	listed(t, w.Provider)
	if browses != 2 {
		t.Fatalf("%d searches after the pause, want 2", browses)
	}
}

// TestUnpluggedPhone: a phone whose cable was just pulled, whose Wi-Fi
// connection Pinky knows is on, is lost unplugGrace after the pull when the
// network shows it and usbmuxd does not, without waiting lostAfter; and not
// seen on Wi-Fi notSeenAfter after the pull when neither shows it. Phones
// whose Wi-Fi connection Pinky does not know is on, and pulls long ago,
// keep the slow rule.
func TestUnpluggedPhone(t *testing.T) {
	const mac = "08:87:c7:2a:8a:db"
	instance := mac + "@fe80::a87:c7ff:fe2a:8adb-supportsRP-26"
	setup := func(conn func(context.Context, goios.DeviceEntry) (bool, error)) (*wifiProvider, *fakeBonjour) {
		w := newWiFiProvider(entry("iphone", 3, "USB"))
		w.lost.anyOS = true
		net := &fakeBonjour{}
		w.lost.browseFn = func() (bonjourSource, error) { return net, nil }
		w.lost.pairFn = func(string) (string, error) { return mac, nil }
		w.wifi.allowFn = conn
		w.SetWiFi(true)
		w.UseOverWiFi("iphone")
		listed(t, w.Provider) // on the cable: its Wi-Fi connection is looked at
		w.wifi.allowing.Wait()
		listed(t, w.Provider)
		return w, net
	}
	on := func(context.Context, goios.DeviceEntry) (bool, error) { return false, nil }

	// On the network, not in usbmuxd's list: lost unplugGrace after the pull.
	w, net := setup(on)
	w.set()
	net.set(instance)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("lost at the pull: %+v", got)
	}
	w.after(unplugGrace)
	if got := listed(t, w.Provider)["iphone"]; got.Setup != driver.SetupDeviceService {
		t.Fatalf("%v after the pull: %+v; want lost", unplugGrace, got)
	}
	// usbmuxd lists it after all: it is not lost.
	w.set(entry("iphone", 9, "Network"))
	if got := listed(t, w.Provider)["iphone"]; got.Setup == driver.SetupDeviceService {
		t.Fatalf("listed again: %+v", got)
	}

	// Neither shows it: not seen on Wi-Fi notSeenAfter after the pull.
	w, net = setup(on)
	w.set()
	listed(t, w.Provider)
	w.after(notSeenAfter - time.Second)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("not seen too soon: %+v", got)
	}
	w.after(time.Second)
	got := listed(t, w.Provider)["iphone"]
	if got.State != driver.StateOffline || got.Setup != driver.SetupNotOnWiFi || got.Detail != notSeenDetail {
		t.Fatalf("%v after the pull: %+v; want not seen on Wi-Fi", notSeenAfter, got)
	}
	// It shows up on the network: then usbmuxd has lost it.
	net.set(instance)
	if got := listed(t, w.Provider)["iphone"]; got.Setup != driver.SetupDeviceService {
		t.Fatalf("on the network after all: %+v", got)
	}

	// Its Wi-Fi connection did not turn on: the slow rule.
	w, net = setup(func(context.Context, goios.DeviceEntry) (bool, error) { return false, errors.New("no answer") })
	w.set()
	net.set(instance)
	listed(t, w.Provider)
	w.after(notSeenAfter)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a phone whose Wi-Fi connection is not known on: %+v", got)
	}
	w.after(lostAfter)
	if got := listed(t, w.Provider)["iphone"]; got.Setup != driver.SetupDeviceService {
		t.Fatalf("after lostAfter: %+v; want lost", got)
	}

	// Listed over Wi-Fi after the pull, then gone (asleep): the slow rule.
	w, net = setup(on)
	w.after(2 * time.Second)
	w.set(entry("iphone", 9, "Network"))
	listed(t, w.Provider)
	w.after(time.Minute)
	w.set()
	net.set(instance)
	listed(t, w.Provider)
	w.after(notSeenAfter)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a phone that came over Wi-Fi after the pull: %+v", got)
	}

	// A pull long ago: the slow rule, and no not-seen notice.
	w, net = setup(on)
	w.set()
	w.after(unplugRecent)
	listed(t, w.Provider)
	w.after(notSeenAfter)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a pull long ago: %+v", got)
	}
}
