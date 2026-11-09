// Package demo provides scripted fake phones for `pinky daemon --fake` and
// for tests: a home screen, a food-ordering app with a payment, a chat app,
// Settings and a wallet app that the default rules block. Nothing here
// touches a real device.
package demo

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/video"
)

// PolicyApps returns the app categories the demo apps belong to, to add to
// the policy engine: Chatter is a messaging app.
func PolicyApps() map[string]policy.CategoryEntries {
	return map[string]policy.CategoryEntries{
		policy.CatMessaging: {IDs: []string{Chatter}},
	}
}

// World returns the phones `pinky daemon --fake` serves: a Pixel and an
// iPhone, each behind its own provider.
func World() ([]driver.Provider, []*Device) {
	pixel := NewDevice("fake-pixel-9", "Pixel 9 (fake)", driver.Android)
	pixel.PutFile("Notes/groceries.txt", []byte("Rice, eggs, spring onions\n"))
	iphone := NewDevice("fake-iphone-17", "iPhone 17 (fake)", driver.IOS)
	return []driver.Provider{NewProvider(driver.Android, pixel), NewProvider(driver.IOS, iphone)}, []*Device{pixel, iphone}
}

// App ids of the demo apps.
const (
	Dinely   = "com.dinely.app"
	Chatter  = "com.chatter.app"
	Calc     = "com.google.android.calculator"
	Messages = "com.google.android.apps.messaging"
)

// Provider serves demo devices.
type Provider struct {
	platform driver.Platform
	mu       sync.Mutex
	devices  []*Device
}

// NewProvider returns a provider for the given platform and devices.
func NewProvider(platform driver.Platform, devices ...*Device) *Provider {
	return &Provider{platform: platform, devices: devices}
}

// Platform implements driver.Provider.
func (p *Provider) Platform() driver.Platform { return p.platform }

// List implements driver.Provider.
func (p *Provider) List(context.Context) ([]driver.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []driver.Info
	for _, d := range p.devices {
		info := d.Info()
		if d.Offline() {
			info.State = driver.StateOffline
			info.Detail = "Unplugged"
		}
		out = append(out, info)
	}
	return out, nil
}

// Open implements driver.Provider. Android phones come wrapped in Android,
// with a Pinky folder and background displays; use Of for the phone.
func (p *Provider) Open(_ context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.devices {
		if d.info.ID == id {
			if d.Offline() {
				return nil, driver.ErrOffline
			}
			if d.Info().Platform == driver.Android {
				return Android{d}, nil
			}
			return d, nil
		}
	}
	return nil, fmt.Errorf("demo device %q: %w", id, driver.ErrNotFound)
}

// Input is one recorded input.
type Input struct {
	Kind string // tap, long_press, swipe, type, key, launch, touch
	P    screen.Point
	To   screen.Point
	Text string
}

// Device is a scripted phone.
type Device struct {
	mu      sync.Mutex
	info    driver.Info
	w, h    int
	home    screen.App
	stack   []string // screens, the last is shown
	focus   string   // id of the focused input
	fields  map[string]string
	cart    int
	chat    []string
	wifi    bool
	locked  bool
	offline bool
	events  []screen.Event
	inputs  []Input
	onInput func(Input)
	slow    time.Duration
	person  chan struct{}
	noVideo bool
	// screenOff is set while the screen is off (SetScreen; Wake clears it).
	screenOff bool
	// files is the Pinky folder of Android phones.
	files map[string]file
	// overlay is shown over every unlocked screen (SetOverlay).
	overlay []screen.Element
}

// NewDevice returns a demo phone. Android phones use the Pixel launcher,
// iPhones SpringBoard.
func NewDevice(id, name string, platform driver.Platform) *Device {
	d := &Device{
		info: driver.Info{ID: id, Platform: platform, Name: name, Model: name, Connection: "usb", Backend: "demo",
			State: driver.StateOnline},
		w: 1080, h: 2400, fields: map[string]string{}, wifi: true,
		chat: []string{"Sam: Are we still on for Saturday?"},
	}
	switch platform {
	case driver.IOS:
		d.info.OSVersion = "27.0"
		d.h = 2340
		d.home = screen.App{Name: "Home screen", ID: "com.apple.springboard"}
	default:
		d.info.OSVersion = "16"
		d.home = screen.App{Name: "Pixel Launcher", ID: "com.google.android.apps.nexuslauncher"}
	}
	d.stack = []string{"home"}
	return d
}

// Info implements driver.Device.
func (d *Device) Info() driver.Info {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

// SetOffline unplugs or plugs in the phone.
func (d *Device) SetOffline(off bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.offline = off
}

// Offline reports whether the phone is unplugged.
func (d *Device) Offline() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.offline
}

// SetLocked locks or unlocks the phone.
func (d *Device) SetLocked(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.locked = on
}

// SetSlow makes every input take this long, to test cancellation.
func (d *Device) SetSlow(t time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.slow = t
}

// Notify posts a notification, shown with the next observation.
func (d *Device) Notify(app, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, screen.Event{Kind: screen.EventNotification, App: app, Text: text})
}

// SetOverlay shows elements over every unlocked screen, for tests: a
// notification banner, or lines that only OCR reads. Nil removes them.
func (d *Device) SetOverlay(els []screen.Element) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overlay = append([]screen.Element(nil), els...)
}

// Show replaces the current screen, for tests: "home", "dinely", "orders",
// "cart", "account", "chats", "chat", "settings", "wallet", "calc".
func (d *Device) Show(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stack = append(d.stack[:1:1], name)
	if name == "home" {
		d.stack = []string{"home"}
	}
}

// Current returns the name of the screen shown.
func (d *Device) Current() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stack[len(d.stack)-1]
}

// Inputs returns the inputs recorded so far.
func (d *Device) Inputs() []Input {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Input(nil), d.inputs...)
}

// OnInput runs f after each input, outside the device's lock.
func (d *Device) OnInput(f func(Input)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onInput = f
}

// Cart returns the number of items in the Dinely cart.
func (d *Device) Cart() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cart
}

// AddToCart puts another item into the Dinely cart, as if the person did.
func (d *Device) AddToCart() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cart++
}

// Chat returns the chat with Sam.
func (d *Device) Chat() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.chat...)
}

func (d *Device) check(ctx context.Context) error {
	d.mu.Lock()
	off, slow := d.offline, d.slow
	d.mu.Unlock()
	if off {
		return driver.ErrOffline
	}
	if slow > 0 {
		t := time.NewTimer(slow)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (d *Device) record(in Input) {
	d.mu.Lock()
	d.inputs = append(d.inputs, in)
	f := d.onInput
	d.mu.Unlock()
	if f != nil {
		f(in)
	}
}

// Observe implements driver.Device.
func (d *Device) Observe(ctx context.Context, _ driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	off := d.offline
	d.mu.Unlock()
	if off {
		return nil, driver.ErrOffline
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.render()
	s.Events = d.events
	d.events = nil
	s.CapturedAt = time.Now()
	return s, nil
}

// Locked implements driver.LockReporter.
func (d *Device) Locked(context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offline {
		return false, driver.ErrOffline
	}
	return d.locked, nil
}

// Screenshot implements driver.Device: boxes for the elements on a colored
// background.
func (d *Device) Screenshot(context.Context) (image.Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offline {
		return nil, driver.ErrOffline
	}
	s := d.render()
	img := image.NewRGBA(image.Rect(0, 0, s.Width, s.Height))
	bg := color.RGBA{0xF4, 0xF1, 0xEA, 0xFF}
	switch s.App.ID {
	case Dinely:
		bg = color.RGBA{0xFF, 0xF3, 0xE0, 0xFF}
	case Chatter:
		bg = color.RGBA{0xE8, 0xF4, 0xFF, 0xFF}
	}
	if s.Locked {
		bg = color.RGBA{0x20, 0x22, 0x30, 0xFF}
	}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	for _, e := range s.Elements {
		c := color.RGBA{0xDD, 0xDD, 0xDD, 0xFF}
		switch e.Role {
		case screen.RoleButton:
			c = color.RGBA{0xFF, 0xBE, 0x1A, 0xFF}
		case screen.RoleInput:
			c = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
		case screen.RoleSwitch:
			c = color.RGBA{0x8C, 0xC8, 0x6E, 0xFF}
		}
		r := image.Rect(e.Bounds.X, e.Bounds.Y, e.Bounds.X+e.Bounds.W, e.Bounds.Y+e.Bounds.H)
		draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
	}
	return img, nil
}

// Tap implements driver.Device.
func (d *Device) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	if err := d.check(ctx); err != nil {
		return err
	}
	kind := "tap"
	if opt.LongPress {
		kind = "long_press"
	}
	d.mu.Lock()
	s := d.render()
	var hit *screen.Element
	for i := range s.Elements {
		e := &s.Elements[i]
		if e.Bounds.Contains(p) && (hit == nil || e.Bounds.W*e.Bounds.H < hit.Bounds.W*hit.Bounds.H) {
			hit = e
		}
	}
	if hit != nil && !d.locked {
		d.press(hit.ID)
	}
	d.mu.Unlock()
	d.record(Input{Kind: kind, P: p})
	return nil
}

// Swipe implements driver.Device.
func (d *Device) Swipe(ctx context.Context, from, to screen.Point, _ time.Duration) error {
	if err := d.check(ctx); err != nil {
		return err
	}
	d.record(Input{Kind: "swipe", P: from, To: to})
	return nil
}

// Type implements driver.Device: it types into the focused field.
func (d *Device) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	if err := d.check(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	if d.focus != "" && !d.locked {
		if opt.Append {
			d.fields[d.focus] += text
		} else {
			d.fields[d.focus] = text
		}
		if opt.Submit {
			d.submit()
		}
	}
	d.mu.Unlock()
	d.record(Input{Kind: "type", Text: text})
	return nil
}

// Key implements driver.Device.
func (d *Device) Key(ctx context.Context, k driver.Key) error {
	if err := d.check(ctx); err != nil {
		return err
	}
	if d.info.Platform == driver.IOS && k == driver.KeyRecents {
		return fmt.Errorf("recents on iPhone: %w", driver.ErrUnsupported)
	}
	d.mu.Lock()
	if !d.locked {
		switch k {
		case driver.KeyBack:
			if len(d.stack) > 1 {
				d.stack = d.stack[:len(d.stack)-1]
			}
			d.focus = ""
		case driver.KeyHome:
			d.stack = []string{"home"}
			d.focus = ""
		case driver.KeyEnter:
			d.submit()
		}
	}
	d.mu.Unlock()
	d.record(Input{Kind: "key", Text: string(k)})
	return nil
}

// Launch implements driver.Device.
func (d *Device) Launch(ctx context.Context, app string) error {
	if err := d.check(ctx); err != nil {
		return err
	}
	var scr string
	for _, a := range d.apps() {
		if a.ID == app || strings.EqualFold(a.Name, app) {
			scr = appScreens[a.ID]
		}
	}
	if scr == "" {
		return fmt.Errorf("app %q: %w", app, driver.ErrNotFound)
	}
	d.mu.Lock()
	if !d.locked {
		d.stack = []string{"home", scr}
		d.focus = ""
	}
	d.mu.Unlock()
	d.record(Input{Kind: "launch", Text: app})
	return nil
}

var appScreens = map[string]string{
	Dinely: "dinely", Chatter: "chats", "com.android.settings": "settings", "com.apple.Preferences": "settings",
	"com.google.android.apps.walletnfcrel": "wallet", "com.apple.Passbook": "wallet", Calc: "calc", Messages: "messages",
}

// Apps implements driver.Device.
func (d *Device) Apps(context.Context) ([]driver.App, error) {
	if d.Offline() {
		return nil, driver.ErrOffline
	}
	return d.apps(), nil
}

func (d *Device) apps() []driver.App {
	if d.info.Platform == driver.IOS {
		return []driver.App{
			{ID: Dinely, Name: "Dinely"}, {ID: Chatter, Name: "Chatter"},
			{ID: "com.apple.Preferences", Name: "Settings", System: true}, {ID: "com.apple.Passbook", Name: "Wallet", System: true},
		}
	}
	return []driver.App{
		{ID: Dinely, Name: "Dinely"}, {ID: Chatter, Name: "Chatter"}, {ID: Calc, Name: "Calculator", System: true},
		{ID: "com.android.settings", Name: "Settings", System: true}, {ID: Messages, Name: "Messages", System: true},
		{ID: "com.google.android.apps.walletnfcrel", Name: "Google Wallet"},
	}
}

// Stream implements driver.Device. Demo phones have no video.
// Stream implements driver.Device: the drawn screen, encoded to H.264 by
// the ffmpeg command at the phone's own size, so the mirror shows it and
// its touches land in the phone's pixels. Without ffmpeg there is no video.
func (d *Device) Stream(ctx context.Context, _ driver.StreamOptions) (driver.Stream, error) {
	d.mu.Lock()
	off := d.noVideo
	d.mu.Unlock()
	if off {
		return nil, fmt.Errorf("video is off for this demo phone: %w", driver.ErrUnsupported)
	}
	img, err := d.Screenshot(ctx)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	s, err := video.FrameStream(ctx, b.Dx()&^1, b.Dy()&^1, 12, func() image.Image {
		img, err := d.Screenshot(context.Background())
		if err != nil {
			return nil
		}
		return img
	})
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, driver.ErrUnsupported)
	}
	return s, nil
}

// Touch implements driver.Toucher: a touch up taps where it lifts.
func (d *Device) Touch(ctx context.Context, a driver.TouchAction, p screen.Point) error {
	if a == driver.TouchUp {
		return d.Tap(ctx, p, driver.TapOptions{})
	}
	d.record(Input{Kind: "touch:" + string(a), P: p})
	return nil
}

// Close implements driver.Device.
func (d *Device) Close() error { return nil }

// SetVideo turns the phone's video stream on (the default) or off, for
// tests of a mirror without a picture.
func (d *Device) SetVideo(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noVideo = !on
}

// PersonInput implements driver.PersonWatcher.
func (d *Device) PersonInput() <-chan struct{} { return d.personCh() }

func (d *Device) personCh() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.person == nil {
		d.person = make(chan struct{}, 1)
	}
	return d.person
}

// PersonTouch acts as if the person touched the phone itself.
func (d *Device) PersonTouch() {
	ch := d.personCh()
	select {
	case ch <- struct{}{}:
	default:
	}
}

var (
	_ driver.Provider      = (*Provider)(nil)
	_ driver.Device        = (*Device)(nil)
	_ driver.Toucher       = (*Device)(nil)
	_ driver.LockReporter  = (*Device)(nil)
	_ driver.PersonWatcher = (*Device)(nil)
)
