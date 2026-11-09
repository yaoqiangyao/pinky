package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/ios"
)

// providerFactories builds the real device providers. The gateway itself
// never branches on the platform.
var providerFactories = []func(log *slog.Logger) (driver.Provider, error){
	newAndroidProvider,
	newIOSProvider,
}

func newAndroidProvider(log *slog.Logger) (driver.Provider, error) {
	var noADB sync.Once
	var netFile string
	if dirs, err := api.DefaultDirs(); err == nil {
		// The phones Pinky reaches over the network without Wireless
		// debugging: moved to Wi-Fi, or away from home through Tailscale.
		netFile = filepath.Join(dirs.Config, "network-phones.json")
	}
	return android.NewProvider(android.Config{
		Client: &adb.Client{},
		// Pinky uses the adb server that runs, or starts one, with the
		// adb of the Android SDK, Homebrew or PATH, else its own
		// (adb.StartServer): at the first List, and whenever the server
		// went away. Without any adb (a build without Pinky's own), it
		// lists no phones and CheckHost says why.
		StartServer: func(ctx context.Context) error {
			t, err := adb.StartServer(ctx)
			switch {
			case errors.Is(err, adb.ErrNoADB):
				noADB.Do(func() { log.Warn("android: no adb server, and no adb to start one", "err", err) })
				return err
			case err != nil:
				log.Warn("android: starting an adb server", "err", err)
				return err
			}
			if t.Path != "" {
				log.Info("android: started an adb server", "adb", t.Path, "from", t.Source, "version", t.Version)
			}
			return nil
		},
		// adb's own discovery stops seeing the network after the
		// computer's network changes; the driver then reconnects the
		// phones paired over Wi-Fi itself. Not while PINKY_DEVICES limits
		// Pinky to some phones: the others belong to other tools.
		ReconnectWiFi: strings.TrimSpace(os.Getenv("PINKY_DEVICES")) == "",
		NetworkFile:   netFile,
		Logf: func(format string, args ...any) {
			log.Debug(fmt.Sprintf(format, args...), "platform", "android")
		},
	}), nil
}

// newIOSProvider always succeeds: without usbmuxd (Linux without the
// package or before an iPhone is plugged in, Windows without Apple
// Devices) it lists no phones and says what is missing (CheckHost), and it
// finds phones once usbmuxd runs.
func newIOSProvider(log *slog.Logger) (driver.Provider, error) {
	p := ios.NewProvider()
	p.Log = log
	// Under PINKY_DEVICES the provider does not even look at the other
	// phones, over a cable or Wi-Fi: other tools use them.
	p.Only = onlyDevices()
	// On Linux and Windows, Pinky finds iPhones on Wi-Fi itself: the
	// system's usbmuxd lists them there not at all, or only while they are
	// on a cable too.
	p.WiFiMux = true
	if dirs, err := api.DefaultDirs(); err == nil {
		// Where Pinky looks for Apple's developer disk image before Xcode's.
		p.DiskImageDir = filepath.Join(dirs.Data, "DeveloperDiskImages")
	}
	if c := p.CheckHost(); c.Missing != "" {
		log.Info("iPhones need software this computer lacks", "missing", c.Missing, "detail", c.Detail)
	}
	return p, nil
}

// realProviders returns the providers for real phones. A provider that
// fails to start is skipped with a warning, so one missing tool (adb,
// usbmuxd) does not take the others down. PINKY_DEVICES, a comma-separated
// list of device ids, limits Pinky to those phones (for development, when
// other tools use the rest).
func realProviders(log *slog.Logger) []driver.Provider {
	only := onlyDevices()
	var out []driver.Provider
	for _, f := range providerFactories {
		p, err := f(log)
		if err != nil {
			log.Warn("device provider unavailable", "err", err)
			continue
		}
		if only != nil {
			op := &onlyProvider{Provider: p, only: only}
			p = op
			if pr, ok := op.Provider.(driver.Pairer); ok {
				p = &onlyPairer{onlyProvider: op, pairer: pr}
			}
			if w, ok := op.Provider.(wifiProvider); ok {
				p = &onlyWiFi{onlyProvider: op, wifi: w}
			}
		}
		out = append(out, p)
	}
	return out
}

// onlyDevices reads PINKY_DEVICES: nil when it is not set.
func onlyDevices() map[string]bool {
	v := strings.TrimSpace(os.Getenv("PINKY_DEVICES"))
	if v == "" {
		return nil
	}
	only := map[string]bool{}
	for _, id := range strings.Split(v, ",") {
		only[strings.TrimSpace(id)] = true
	}
	return only
}

// onlyProvider hides every device not in only. It is used as a pointer,
// since the gateway compares providers.
type onlyProvider struct {
	driver.Provider

	mu   sync.Mutex
	only map[string]bool
}

func (p *onlyProvider) allowed(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.only[id]
}

func (p *onlyProvider) allow(id string) {
	p.mu.Lock()
	p.only[id] = true
	p.mu.Unlock()
}

func (p *onlyProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.Provider.List(ctx)
	out := infos[:0]
	for _, info := range infos {
		if p.allowed(info.ID) {
			out = append(out, info)
		}
	}
	return out, err
}

func (p *onlyProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	if !p.allowed(id) {
		return nil, fmt.Errorf("%s is not in PINKY_DEVICES: %w", id, driver.ErrNotFound)
	}
	return p.Provider.Open(ctx, id)
}

// onlyPairer is an onlyProvider whose provider pairs phones over Wi-Fi: it
// keeps pairing available, and a phone the person pairs joins the phones
// Pinky may use.
type onlyPairer struct {
	*onlyProvider
	pairer driver.Pairer
}

func (p *onlyPairer) StartPairing(ctx context.Context) (driver.Pairing, error) {
	pg, err := p.pairer.StartPairing(ctx)
	if err != nil {
		return nil, err
	}
	return &allowOnPair{Pairing: pg, p: p.onlyProvider}, nil
}

func (p *onlyPairer) PairWithCode(ctx context.Context, addr, code string) (driver.Info, error) {
	info, err := p.pairer.PairWithCode(ctx, addr, code)
	if err == nil {
		p.allow(info.ID)
	}
	return info, err
}

// ConnectWiFi connects a paired phone at the address it shows, and lets it
// in as a pairing does.
func (p *onlyPairer) ConnectWiFi(ctx context.Context, addr string) (driver.Info, error) {
	wc, ok := p.pairer.(driver.WiFiConnector)
	if !ok {
		return driver.Info{}, fmt.Errorf("connecting by address: %w", driver.ErrUnsupported)
	}
	info, err := wc.ConnectWiFi(ctx, addr)
	if err == nil {
		p.allow(info.ID)
	}
	return info, err
}

// Tailnet, ConnectTailnet, Away and SetAway pass Tailscale on
// (driver.Tailnet); a phone connected through it joins the phones Pinky
// may use, as a pairing does.
func (p *onlyPairer) Tailnet(ctx context.Context) (driver.TailnetStatus, error) {
	t, ok := p.pairer.(driver.Tailnet)
	if !ok {
		return driver.TailnetStatus{}, fmt.Errorf("tailscale: %w", driver.ErrUnsupported)
	}
	return t.Tailnet(ctx)
}

func (p *onlyPairer) ConnectTailnet(ctx context.Context, addr string) (driver.Info, error) {
	t, ok := p.pairer.(driver.Tailnet)
	if !ok {
		return driver.Info{}, fmt.Errorf("tailscale: %w", driver.ErrUnsupported)
	}
	info, err := t.ConnectTailnet(ctx, addr)
	if err == nil {
		p.allow(info.ID)
	}
	return info, err
}

func (p *onlyPairer) Away(ctx context.Context, id string) (driver.Away, error) {
	t, ok := p.pairer.(driver.Tailnet)
	if !ok || !p.allowed(id) {
		return driver.Away{}, fmt.Errorf("tailscale: %w", driver.ErrUnsupported)
	}
	return t.Away(ctx, id)
}

func (p *onlyPairer) SetAway(ctx context.Context, id string, on bool) (driver.Away, error) {
	t, ok := p.pairer.(driver.Tailnet)
	if !ok || !p.allowed(id) {
		return driver.Away{}, fmt.Errorf("tailscale: %w", driver.ErrUnsupported)
	}
	return t.SetAway(ctx, id, on)
}

// PhoneWiFi and SetPhoneWiFi pass the Wi-Fi switch for Android phones
// without Wireless debugging on (driver.WiFiSwitch).
func (p *onlyPairer) PhoneWiFi(ctx context.Context, id string) (driver.WiFiState, error) {
	w, ok := p.pairer.(driver.WiFiSwitch)
	if !ok || !p.allowed(id) {
		return driver.WiFiState{}, fmt.Errorf("wi-fi: %w", driver.ErrUnsupported)
	}
	return w.PhoneWiFi(ctx, id)
}

func (p *onlyPairer) SetPhoneWiFi(ctx context.Context, id string, on bool) (driver.WiFiState, error) {
	w, ok := p.pairer.(driver.WiFiSwitch)
	if !ok || !p.allowed(id) {
		return driver.WiFiState{}, fmt.Errorf("wi-fi: %w", driver.ErrUnsupported)
	}
	return w.SetPhoneWiFi(ctx, id, on)
}

// wifiProvider is a provider that reaches phones over Wi-Fi by itself and
// is told which phones the person removed (iPhones).
type wifiProvider interface {
	driver.WiFiFinder
	driver.Forgetter
}

// onlyWiFi is an onlyProvider whose provider reaches phones over Wi-Fi by
// itself: it passes on which phones are the person's, which to leave
// alone, and whether to use Wi-Fi at all.
type onlyWiFi struct {
	*onlyProvider
	wifi wifiProvider
}

func (p *onlyWiFi) SetWiFi(on bool)       { p.wifi.SetWiFi(on) }
func (p *onlyWiFi) UseOverWiFi(id string) { p.wifi.UseOverWiFi(id) }
func (p *onlyWiFi) Forget(id string)      { p.wifi.Forget(id) }
func (p *onlyWiFi) Remember(id string)    { p.wifi.Remember(id) }
func (p *onlyWiFi) Disconnect(ctx context.Context, id string) error {
	return p.wifi.Disconnect(ctx, id)
}

// RestartDeviceService passes the restart of usbmuxd on, when the provider
// can.
func (p *onlyWiFi) RestartDeviceService(ctx context.Context, reason string) error {
	if r, ok := p.wifi.(driver.DeviceServiceRestarter); ok {
		return r.RestartDeviceService(ctx, reason)
	}
	return fmt.Errorf("restarting the device service: %w", driver.ErrUnsupported)
}

type allowOnPair struct {
	driver.Pairing
	p *onlyProvider
}

func (a *allowOnPair) Wait(ctx context.Context) (driver.Info, error) {
	info, err := a.Pairing.Wait(ctx)
	if err == nil {
		a.p.allow(info.ID)
	}
	return info, err
}

// Progress passes the pairing's progress on (driver.PairingReporter).
func (a *allowOnPair) Progress() driver.PairingProgress {
	if r, ok := a.Pairing.(driver.PairingReporter); ok {
		return r.Progress()
	}
	return driver.PairingProgress{}
}
