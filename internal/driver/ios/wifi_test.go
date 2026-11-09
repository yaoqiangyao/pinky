package ios

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
)

// wifiProvider is a provider over a usbmuxd list the test changes, that
// counts how often it inspects (prepares) each phone and reads a found
// phone's name.
type wifiProvider struct {
	*Provider
	mu       sync.Mutex
	list     []goios.DeviceEntry
	inspects map[string]int
	reads    map[string]int
	readErr  error
	now      time.Time
}

func newWiFiProvider(list ...goios.DeviceEntry) *wifiProvider {
	w := &wifiProvider{Provider: NewProvider(), list: list, inspects: map[string]int{}, reads: map[string]int{}, now: time.Unix(1000, 0)}
	w.Provider.now = func() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.now }
	w.listFn = func() (goios.DeviceList, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		return goios.DeviceList{DeviceList: append([]goios.DeviceEntry(nil), w.list...)}, nil
	}
	w.inspectFn = func(_ context.Context, e goios.DeviceEntry) driver.Info {
		w.mu.Lock()
		defer w.mu.Unlock()
		id := e.Properties.SerialNumber
		w.inspects[id]++
		conn := "usb"
		if e.Properties.ConnectionType == "Network" {
			conn = "wifi"
		}
		return driver.Info{ID: id, Platform: driver.IOS, Name: id, Connection: conn, State: driver.StateOnline, Backend: backendCoreDevice}
	}
	w.lost.pairFn = func(string) (string, error) { return "", errors.New("no pair record in tests") }
	w.wifi.namesFn = func(_ context.Context, e goios.DeviceEntry) (phoneNames, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.reads[e.Properties.SerialNumber]++
		if w.readErr != nil {
			return phoneNames{}, w.readErr
		}
		return phoneNames{name: "Mum's iPad", productType: "iPad13,4", version: "27.0.1"}, nil
	}
	return w
}

func (w *wifiProvider) set(list ...goios.DeviceEntry) {
	w.mu.Lock()
	w.list = list
	w.mu.Unlock()
}

func (w *wifiProvider) after(d time.Duration) {
	w.mu.Lock()
	w.now = w.now.Add(d)
	w.mu.Unlock()
}

func (w *wifiProvider) counts(id string) (inspects, reads int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inspects[id], w.reads[id]
}

func listed(t *testing.T, p *Provider) map[string]driver.Info {
	t.Helper()
	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]driver.Info{}
	for _, info := range infos {
		out[info.ID] = info
	}
	return out
}

// TestWiFiFoundPhoneLeftAlone: a phone usbmuxd lists only over Wi-Fi that
// is not the person's is listed by name, with SetupConfirmWiFi, and is
// neither inspected (which prepares it) nor opened, until UseOverWiFi.
func TestWiFiFoundPhoneLeftAlone(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"), entry("phone", 3, "USB"))
	if got := listed(t, w.Provider); len(got) != 1 || got["phone"].State != driver.StateOnline {
		t.Fatalf("Wi-Fi off: %+v; want the phone on the cable only", got)
	}
	w.SetWiFi(true)
	got := listed(t, w.Provider)
	ipad := got["ipad"]
	if ipad.State != driver.StateNeedsSetup || ipad.Setup != driver.SetupConfirmWiFi || ipad.Name != "Mum's iPad" ||
		ipad.Model != "iPad Pro 11-inch (3rd generation)" || ipad.Connection != "wifi" || ipad.OSVersion != "27.0.1" {
		t.Fatalf("found iPad = %+v", ipad)
	}
	if n, _ := w.counts("ipad"); n != 0 {
		t.Fatalf("the found iPad was inspected %d times; it must be left alone", n)
	}
	if _, err := w.Open(context.Background(), "ipad"); err == nil || !strings.Contains(err.Error(), "found on Wi-Fi") {
		t.Fatalf("Open of the found iPad = %v; want a refusal", err)
	}
	if n, _ := w.counts("ipad"); n != 0 {
		t.Fatal("Open inspected the found iPad")
	}
	if c := w.Connections(context.Background(), "ipad"); c.Ways[1].Available || c.Ways[1].Reason != "offline" {
		t.Fatalf("Connections of the found iPad = %+v; want nothing asked of it", c)
	}

	// Listed again, and again as a new attach (an iPad that sleeps comes
	// back with a new DeviceID): its name is not read again.
	listed(t, w.Provider)
	w.set(entry("ipad", 8, "Network"), entry("phone", 3, "USB"))
	w.after(time.Minute)
	listed(t, w.Provider)
	if _, reads := w.counts("ipad"); reads != 1 {
		t.Fatalf("the name was read %d times; want once", reads)
	}

	w.UseOverWiFi("ipad")
	got = listed(t, w.Provider)
	if got["ipad"].State != driver.StateOnline || got["ipad"].Connection != "wifi" {
		t.Fatalf("after Use: %+v", got["ipad"])
	}
	if n, _ := w.counts("ipad"); n != 1 {
		t.Fatalf("after Use the iPad was inspected %d times, want 1", n)
	}

	// Turned off, phones over Wi-Fi go; the cable stays.
	w.SetWiFi(false)
	if got := listed(t, w.Provider); len(got) != 1 || got["phone"].ID == "" {
		t.Fatalf("Wi-Fi off again: %+v", got)
	}
}

// TestWiFiFoundPhoneSilent: a found phone that does not answer is not
// listed, and is asked again after recheckOffline.
func TestWiFiFoundPhoneSilent(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	w.readErr = errors.New("no answer within 8s")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a silent found phone was listed: %+v", got)
	}
	listed(t, w.Provider)
	if _, reads := w.counts("ipad"); reads != 1 {
		t.Fatalf("%d reads within recheckOffline, want 1", reads)
	}
	w.readErr = nil
	w.after(recheckOffline)
	if got := listed(t, w.Provider); got["ipad"].Setup != driver.SetupConfirmWiFi {
		t.Fatalf("after it answered: %+v", got)
	}
}

// TestWiFiForgottenPhone: a phone the person removed or ignored is not
// listed over Wi-Fi at all, and not even read; on a cable it is, and is the
// person's once the gateway says so.
func TestWiFiForgottenPhone(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	w.UseOverWiFi("ipad")
	w.Forget("ipad")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a forgotten phone was listed: %+v", got)
	}
	if n, reads := w.counts("ipad"); n != 0 || reads != 0 {
		t.Fatalf("a forgotten phone was touched: %d inspects, %d reads", n, reads)
	}
	if _, err := w.Open(context.Background(), "ipad"); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("Open of a forgotten phone = %v", err)
	}
	w.set(entry("ipad", 9, "USB"), entry("ipad", 7, "Network"))
	if got := listed(t, w.Provider); got["ipad"].State != driver.StateOnline || got["ipad"].Connection != "usb" {
		t.Fatalf("on a cable: %+v", got)
	}
	w.Remember("ipad")
	w.UseOverWiFi("ipad")
	w.set(entry("ipad", 7, "Network"))
	if got := listed(t, w.Provider); got["ipad"].State != driver.StateOnline || got["ipad"].Connection != "wifi" {
		t.Fatalf("unplugged after it came back: %+v", got)
	}
}

// TestWiFiCableMoves: a phone the person uses moves from the cable to
// Wi-Fi and back as the same phone, inspected again for each connection.
func TestWiFiCableMoves(t *testing.T) {
	w := newWiFiProvider(entry("phone", 3, "USB"), entry("phone", 7, "Network"))
	w.SetWiFi(true)
	w.UseOverWiFi("phone")
	if got := listed(t, w.Provider)["phone"]; got.Connection != "usb" {
		t.Fatalf("on both: %+v; want the cable", got)
	}
	w.set(entry("phone", 7, "Network"))
	if got := listed(t, w.Provider)["phone"]; got.Connection != "wifi" || got.State != driver.StateOnline {
		t.Fatalf("unplugged: %+v", got)
	}
	w.set(entry("phone", 11, "USB"), entry("phone", 7, "Network"))
	if got := listed(t, w.Provider)["phone"]; got.Connection != "usb" {
		t.Fatalf("plugged in again: %+v", got)
	}
	if n, _ := w.counts("phone"); n != 3 {
		t.Fatalf("%d inspections, want one per connection", n)
	}
}

func TestBounded(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	_, err := bounded(context.Background(), 20*time.Millisecond, func() (int, error) { <-block; return 1, nil })
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("bounded = %v after %v", err, time.Since(start))
	}
	v, err := bounded(context.Background(), time.Second, func() (int, error) { return 7, nil })
	if v != 7 || err != nil {
		t.Fatalf("bounded = %d, %v", v, err)
	}
}

// TestWiFiFoundPhoneHeld: a found phone that drops off usbmuxd's list, as
// an iPad that sleeps does every minute or so, stays listed for foundHold.
func TestWiFiFoundPhoneHeld(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	listed(t, w.Provider)
	w.set()
	w.after(foundHold - time.Second)
	if got := listed(t, w.Provider); got["ipad"].Setup != driver.SetupConfirmWiFi {
		t.Fatalf("asleep for a moment: %+v", got)
	}
	w.set(entry("ipad", 8, "Network")) // back, as a new attach
	listed(t, w.Provider)
	w.set()
	w.after(foundHold - time.Second)
	if got := listed(t, w.Provider); len(got) != 1 {
		t.Fatalf("the hold did not start again when it came back: %+v", got)
	}
	w.after(2 * time.Second)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("gone longer than foundHold: %+v", got)
	}
	if _, reads := w.counts("ipad"); reads != 1 {
		t.Fatalf("%d reads, want 1", reads)
	}
}

// TestGoneMarksOffline: once the tunnel a device was opened over stops, as
// when its cable is pulled, errors read as driver.ErrOffline.
func TestGoneMarksOffline(t *testing.T) {
	tun := &Tunnel{done: make(chan struct{})}
	d := &Device{l: &link{tunnel: tun}}
	failed := errors.New("connect com.apple.accessibility.axAuditDaemon.remoteserver: Lockdown connection failed")
	if err := d.gone(failed); errors.Is(err, driver.ErrOffline) {
		t.Fatalf("with the tunnel up: %v", err)
	}
	close(tun.done)
	if err := d.gone(failed); !errors.Is(err, driver.ErrOffline) || !strings.Contains(err.Error(), "Lockdown") {
		t.Fatalf("with the tunnel down: %v", err)
	}
	if d.gone(nil) != nil {
		t.Fatal("no error became one")
	}
}

// TestWiFiLockedHeld: a phone the person uses that is locked on Wi-Fi is
// listed as waiting to be unlocked, also while it drops off
// usbmuxd's list as it sleeps, for sleepHold; unlocked, it is not.
func TestWiFiLockedHeld(t *testing.T) {
	w := newWiFiProvider()
	w.SetWiFi(true)
	w.UseOverWiFi("ipad")
	info := w.lockedOnWiFi(driver.Info{ID: "ipad", Platform: driver.IOS, Name: "iPad", Connection: "wifi", State: driver.StateOnline})
	if info.State != driver.StateNeedsSetup || info.Setup != driver.SetupUnlock {
		t.Fatalf("locked on Wi-Fi: %+v", info)
	}
	w.after(sleepHold - time.Second)
	if got := listed(t, w.Provider); got["ipad"].Setup != driver.SetupUnlock {
		t.Fatalf("asleep, off usbmuxd's list: %+v", got)
	}
	if held, ok := w.stillAsleep("ipad"); !ok || held.Setup != driver.SetupUnlock {
		t.Fatalf("a look without an answer while asleep: %+v, %v", held, ok)
	}
	w.after(2 * time.Second)
	if _, ok := w.stillAsleep("ipad"); ok {
		t.Fatal("still asleep after sleepHold")
	}
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("gone longer than sleepHold: %+v", got)
	}
	w.lockedOnWiFi(info)
	w.awake("ipad")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("held after it was unlocked: %+v", got)
	}
	w.lockedOnWiFi(info)
	w.Forget("ipad")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("held after it was removed: %+v", got)
	}
}

// TestWiFiDozing: a phone without a passcode that comes back on Wi-Fi a
// moment after it dropped off is most likely asleep: it is neither listed
// nor inspected (which opens it) until usbmuxd has listed it for
// awakeAfter. One seen locked has a passcode and does not wait.
func TestWiFiDozing(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	w.UseOverWiFi("ipad")
	if got := listed(t, w.Provider); got["ipad"].State != driver.StateOnline {
		t.Fatalf("first seen: %+v", got)
	}
	w.set() // asleep: off usbmuxd's list
	w.after(20 * time.Second)
	listed(t, w.Provider)
	w.set(entry("ipad", 8, "Network")) // back for a moment
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("back a moment after it dropped: %+v", got)
	}
	n, _ := w.counts("ipad")
	w.after(awakeAfter - time.Second)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("listed for less than awakeAfter: %+v", got)
	}
	if m, _ := w.counts("ipad"); m != n {
		t.Fatal("a dozing phone was inspected")
	}
	w.after(2 * time.Second)
	if got := listed(t, w.Provider); got["ipad"].State != driver.StateOnline {
		t.Fatalf("listed for awakeAfter: %+v", got)
	}

	// With a passcode (seen locked), the lock rule covers its sleep.
	w.noteLocked("ipad")
	w.set()
	listed(t, w.Provider)
	w.set(entry("ipad", 9, "Network"))
	if got := listed(t, w.Provider); got["ipad"].ID == "" {
		t.Fatalf("a phone with a passcode waited: %+v", got)
	}

	// Plugged in and unplugged is not a drop.
	w2 := newWiFiProvider(entry("phone", 1, "USB"), entry("phone", 2, "Network"))
	w2.SetWiFi(true)
	w2.UseOverWiFi("phone")
	listed(t, w2.Provider)
	w2.set(entry("phone", 2, "Network"))
	if got := listed(t, w2.Provider); got["phone"].Connection != "wifi" {
		t.Fatalf("unplugged: %+v", got)
	}
}

// allowCounter records the phones Pinky turned Wi-Fi connections on for.
type allowCounter struct {
	mu    sync.Mutex
	calls map[string]int
	err   error
}

func (a *allowCounter) fn(_ context.Context, e goios.DeviceEntry) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[e.Properties.SerialNumber]++
	return a.err == nil, a.err
}

func (a *allowCounter) count(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[id]
}

// TestWiFiAllowedOnCable: while Pinky uses phones over Wi-Fi, it turns on
// Wi-Fi connections (Finder's "Show this iPhone when on Wi-Fi") for a phone
// on a cable that trusts this computer, once per attach; not with the
// switch off, not for a phone that does not trust it yet, not over Wi-Fi,
// not for one the person removed; again a minute after it failed.
func TestWiFiAllowedOnCable(t *testing.T) {
	w := newWiFiProvider(entry("phone", 3, "USB"), entry("new", 4, "USB"), entry("ipad", 7, "Network"), entry("gone", 5, "USB"))
	a := &allowCounter{calls: map[string]int{}}
	w.wifi.allowFn = a.fn
	trusts := map[string]bool{"phone": true, "ipad": true, "gone": true}
	inspect := w.inspectFn
	w.inspectFn = func(ctx context.Context, e goios.DeviceEntry) driver.Info {
		info := inspect(ctx, e)
		w.mu.Lock()
		defer w.mu.Unlock()
		if !trusts[info.ID] {
			return driver.Info{ID: info.ID, Platform: driver.IOS, Connection: info.Connection,
				State: driver.StateUnauthorized, Setup: driver.SetupTrust}
		}
		return info
	}
	list := func() {
		t.Helper()
		listed(t, w.Provider)
		w.wifi.allowing.Wait()
	}

	// The switch off: the phone's value is left alone.
	list()
	if n := a.count("phone"); n != 0 {
		t.Fatalf("with Wi-Fi off: %d", n)
	}
	w.SetWiFi(true)
	w.UseOverWiFi("ipad")
	w.Forget("gone")
	list()
	list()
	if a.count("phone") != 1 || a.count("new") != 0 || a.count("ipad") != 0 || a.count("gone") != 0 {
		t.Fatalf("calls %v; want the trusted phone on the cable once", a.calls)
	}
	// It trusts this computer now: on the same attach, once it does.
	w.mu.Lock()
	trusts["new"] = true
	w.mu.Unlock()
	w.after(time.Hour) // inspected again: it was not online
	list()
	if a.count("new") != 1 {
		t.Fatalf("after trusting: %v", a.calls)
	}
	// Plugged in again: looked at again.
	w.set(entry("phone", 11, "USB"))
	list()
	if a.count("phone") != 2 {
		t.Fatalf("plugged in again: %v", a.calls)
	}
	// A failure is tried again after wifiAllowRetry, not every List.
	a.mu.Lock()
	a.err = errors.New("no answer")
	a.mu.Unlock()
	w.set(entry("phone", 12, "USB"))
	list()
	list()
	if a.count("phone") != 3 {
		t.Fatalf("after a failure: %v", a.calls)
	}
	a.mu.Lock()
	a.err = nil
	a.mu.Unlock()
	w.after(wifiAllowRetry)
	list()
	list()
	if a.count("phone") != 4 {
		t.Fatalf("a minute after the failure: %v", a.calls)
	}
	// Turning the switch off changes nothing on the phone.
	w.SetWiFi(false)
	w.set(entry("phone", 13, "USB"))
	list()
	if a.count("phone") != 4 {
		t.Fatalf("with Wi-Fi turned off: %v", a.calls)
	}
}

// TestWiFiConnectionState: List says what Pinky knows of each phone's Wi-Fi
// connection (Info.WiFi), and logs each change once.
func TestWiFiConnectionState(t *testing.T) {
	w := newWiFiProvider(entry("phone", 3, "USB"), entry("other", 4, "Network"))
	var logs bytes.Buffer
	w.Log = slog.New(slog.NewTextHandler(&logs, nil))
	a := &allowCounter{calls: map[string]int{}}
	w.wifi.allowFn = a.fn
	w.UseOverWiFi("other")
	state := func(id string) driver.WiFiConnection {
		t.Helper()
		got := listed(t, w.Provider)
		w.wifi.allowing.Wait()
		got = listed(t, w.Provider)
		return got[id].WiFi
	}
	count := func(s string) int { return strings.Count(logs.String(), s) }

	// The switch off: nothing looked at, and said why.
	if got := state("phone"); got.Connections != driver.WiFiConnectionsOff || got.Seen || !strings.Contains(got.Detail, "is off") {
		t.Fatalf("with Wi-Fi off: %+v", got)
	}
	w.SetWiFi(true)
	if got := state("phone"); got != (driver.WiFiConnection{Connections: driver.WiFiConnectionsTurnedOn}) {
		t.Fatalf("turned on: %+v", got)
	}
	state("phone")
	if n := count("turned on Wi-Fi connections"); n != 1 {
		t.Fatalf("logged %d times; want once:\n%s", n, logs.String())
	}
	if got := state("other"); got != (driver.WiFiConnection{Connections: driver.WiFiConnectionsOn, Seen: true}) {
		t.Fatalf("a phone listed over the network: %+v", got)
	}
	// Seen on Wi-Fi too, then unplugged: Pinky turned them on, and it is
	// there.
	w.set(entry("phone", 3, "USB"), entry("phone", 9, "Network"))
	if got := state("phone"); got != (driver.WiFiConnection{Connections: driver.WiFiConnectionsTurnedOn, Seen: true}) {
		t.Fatalf("on both: %+v", got)
	}
	if count("iphone: listed over Wi-Fi") != 2 { // other, then phone
		t.Fatalf("listing over Wi-Fi not logged:\n%s", logs.String())
	}

	// Plugged in again, the phone refuses: the reason is kept and logged.
	a.mu.Lock()
	a.err = errors.New("Failed setting 'EnableWifiConnections' to 'true' with err: NotAllowed")
	a.mu.Unlock()
	w.set(entry("phone", 12, "USB"))
	if got := state("phone"); got.Connections != driver.WiFiConnectionsFailed || got.Seen || !strings.Contains(got.Detail, "NotAllowed") {
		t.Fatalf("refused: %+v", got)
	}
	if count("could not turn on Wi-Fi connections") != 1 || count("no longer listed over Wi-Fi") != 1 {
		t.Fatalf("logs:\n%s", logs.String())
	}

	// Not trusted yet, and not answering: not looked at, and why.
	trusted := false
	inspect := w.inspectFn
	w.inspectFn = func(ctx context.Context, e goios.DeviceEntry) driver.Info {
		info := inspect(ctx, e)
		if !trusted {
			return driver.Info{ID: info.ID, Platform: driver.IOS, Connection: "usb", State: driver.StateUnauthorized, Setup: driver.SetupTrust}
		}
		return driver.Info{ID: info.ID, Platform: driver.IOS, Connection: "usb", State: driver.StateOffline, Detail: "The iPhone does not answer: EOF"}
	}
	w.set(entry("phone", 13, "USB"))
	if got := state("phone"); got.Connections != driver.WiFiConnectionsUnknown || !strings.Contains(got.Detail, "trust") {
		t.Fatalf("not trusted: %+v", got)
	}
	trusted = true
	w.set(entry("phone", 14, "USB"))
	if got := state("phone"); got.Connections != driver.WiFiConnectionsUnknown || !strings.Contains(got.Detail, "lockdown could not be read: The iPhone does not answer") {
		t.Fatalf("not answering: %+v", got)
	}
	if count("Wi-Fi connections not looked at") != 3 { // off, untrusted, no answer
		t.Fatalf("logs:\n%s", logs.String())
	}
}
