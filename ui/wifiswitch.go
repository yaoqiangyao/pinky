package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Wi-Fi for Android phones without Wireless debugging (Android 10): Use
// Wi-Fi instead in the ⋯ menu under the mirror, in Settings › Phones and
// at the end of Add phone moves a phone on a cable to Wi-Fi (the gateway's
// SetPhoneWiFi: adb tcpip, then a connection at its Wi-Fi address), after a
// dialog that says what it opens; Stop the Wi-Fi connection undoes it. A
// phone whose Wi-Fi connection stopped (it restarted) says on its page to
// plug it in once and choose Use Wi-Fi again.

// wifiSwitcher is a gateway that moves phones to Wi-Fi this way.
type wifiSwitcher interface {
	PhoneWiFi(ctx context.Context, deviceID string) (driver.WiFiState, error)
	SetPhoneWiFi(ctx context.Context, deviceID string, on bool) (driver.WiFiState, error)
}

// phoneWiFiState is the client's part of it, by phone.
type phoneWiFiState struct {
	got     map[string]driver.WiFiState
	at      map[string]time.Time
	loading map[string]bool
	busy    map[string]bool
	err     map[string]string

	// The dialog that turns it on.
	ask     string
	askName string
	askOpen bool
}

func (s *phoneWiFiState) init() {
	if s.got != nil {
		return
	}
	s.got, s.at, s.loading = map[string]driver.WiFiState{}, map[string]time.Time{}, map[string]bool{}
	s.busy, s.err = map[string]bool{}, map[string]string{}
}

// wifiEvery is how often a page that shows a phone's Wi-Fi switch reads it
// again.
const wifiEvery = 10 * time.Second

// noWirelessDebugging reports whether an Android phone has no Wireless
// debugging (before Android 11), so Wi-Fi takes this switch.
func noWirelessDebugging(d gateway.DeviceView) bool {
	if d.Info.Platform != driver.Android {
		return false
	}
	v := d.Info.OSVersion
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	return err == nil && n < 11
}

// phoneWiFiOf returns what the gateway last said about a phone's Wi-Fi switch,
// and reads it again off the main thread when it is old.
func (a *app) phoneWiFiOf(c *mg.Context, d gateway.DeviceView) (driver.WiFiState, bool) {
	ws, ok := a.api.(wifiSwitcher)
	s := &a.phoneWiFi
	if !ok || d.Info.Platform != driver.Android {
		return driver.WiFiState{}, false
	}
	s.init()
	id := d.Info.ID
	if !s.loading[id] && !s.busy[id] && time.Since(s.at[id]) >= wifiEvery {
		s.loading[id] = true
		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			st, err := ws.PhoneWiFi(ctx, id)
			a.post(func() {
				s.loading[id], s.at[id] = false, time.Now()
				if err == nil {
					s.got[id] = st
				} else {
					delete(s.got, id)
				}
			})
		}()
	}
	c.After(wifiEvery)
	st, have := s.got[id]
	return st, have
}

// phoneWiFiOffered reports whether a phone shows the Wi-Fi switch: one without
// Wireless debugging on a cable, or one moved to Wi-Fi already.
func phoneWiFiOffered(d gateway.DeviceView, st driver.WiFiState) bool {
	return st.On || (noWirelessDebugging(d) && d.Info.Connection == "usb")
}

// phoneWiFiMenu is the switch in the ⋯ menu under the mirror; close closes the
// menu.
func (a *app) phoneWiFiMenu(c *mg.Context, d gateway.DeviceView, close func()) {
	st, have := a.phoneWiFiOf(c, d)
	if !have || !phoneWiFiOffered(d, st) || a.phoneWiFi.busy[d.Info.ID] {
		return
	}
	switch {
	case st.Stopped && d.Info.Connection == "usb":
		if menuItem(c, locales.WiFiUseAgain(), mg.Color{}).Clicked() {
			close()
			a.setPhoneWiFi(d, true)
		}
	case st.On:
		if menuItem(c, locales.WiFiStop(), mg.Color{}).Clicked() {
			close()
			a.setPhoneWiFi(d, false)
		}
	case st.Missing == "":
		if menuItem(c, locales.WiFiUse(), mg.Color{}).Clicked() {
			close()
			a.askPhoneWiFi(d)
		}
	}
}

// askPhoneWiFi opens the dialog that moves a phone to Wi-Fi.
func (a *app) askPhoneWiFi(d gateway.DeviceView) {
	s := &a.phoneWiFi
	s.init()
	s.ask, s.askName, s.askOpen = d.Info.ID, d.Info.Name, true
}

// phoneWiFiRow is a phone's Wi-Fi switch in Settings › Phones.
func (a *app) phoneWiFiRow(c *mg.Context, d gateway.DeviceView) {
	st, have := a.phoneWiFiOf(c, d)
	if !have || !(st.On || noWirelessDebugging(d)) {
		return
	}
	p := pal(c)
	s := &a.phoneWiFi
	id, name := d.Info.ID, d.Info.Name
	usb := d.Info.Connection == "usb" && d.Status != gateway.DeviceOffline
	var desc string
	var action func()
	switch {
	case s.busy[id]:
		desc = locales.WiFiBusy()
	case st.Stopped:
		desc = locales.WiFiStoppedDesc(name)
		if usb {
			action = func() {
				if button(c, locales.WiFiUseAgain(), btnOpt{small: true, kind: btnPrimary}).Clicked() {
					a.setPhoneWiFi(d, true)
				}
			}
		}
	case st.On:
		desc = locales.WiFiOnDesc(name, st.Addr)
		if st.Away {
			desc += " " + locales.WiFiAlsoAway()
		}
		action = func() {
			if button(c, locales.WiFiStop(), btnOpt{small: true}).Clicked() {
				a.setPhoneWiFi(d, false)
			}
		}
	case st.Missing == driver.WiFiNoWiFi:
		desc = locales.WiFiNoWiFi(name)
	case st.Missing == driver.WiFiNeedsCable || !usb:
		desc = locales.WiFiNeedsCable(name)
	default:
		desc = locales.WiFiOffDesc(name)
		action = func() {
			if button(c, locales.WiFiUse(), btnOpt{small: true}).Clicked() {
				a.askPhoneWiFi(d)
			}
		}
	}
	mg.Column(c).Gap(4).Padding(6, 0, 0, 0).Children(func() {
		mg.Row(c).Gap(16).AlignItems(mg.Center).Children(func() {
			mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				mg.Text(c, locales.WiFiTitle()).FontSize(13.5).FontWeight(600)
				mg.Text(c, desc).FontSize(13).TextColor(p.ink2)
			})
			if s.busy[id] {
				spinner(c)
			} else if action != nil {
				action()
			}
		})
		if msg := s.err[id]; msg != "" {
			mg.Text(c, msg).FontSize(13).TextColor(p.red).Role(mg.RoleStatus)
		}
	})
}

// setPhoneWiFi moves a phone to Wi-Fi, or stops that.
func (a *app) setPhoneWiFi(d gateway.DeviceView, on bool) {
	ws, ok := a.api.(wifiSwitcher)
	s := &a.phoneWiFi
	id, name := d.Info.ID, d.Info.Name
	if !ok || s.busy[id] {
		return
	}
	s.init()
	s.busy[id], s.err[id] = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		st, err := ws.SetPhoneWiFi(ctx, id, on)
		a.post(func() {
			s.busy[id] = false
			s.at[id] = time.Time{}
			if err != nil {
				s.err[id] = locales.WiFiFailed(name, err.Error())
				a.toasts = append(a.toasts, s.err[id])
				return
			}
			s.got[id] = st
			if a.tn.awayAt != nil {
				a.tn.awayAt[id] = time.Time{} // Use away from home may have changed with it
			}
			if on {
				a.toasts = append(a.toasts, locales.WiFiOnToast(name, st.Addr))
			} else {
				a.toasts = append(a.toasts, locales.WiFiOffToast(name))
			}
		})
	}()
}

// phoneWiFiDialog asks before a phone's adb listens on the network.
func (a *app) phoneWiFiDialog(c *mg.Context) {
	s := &a.phoneWiFi
	if !s.askOpen {
		return
	}
	d := a.device(s.ask)
	if d == nil {
		s.askOpen = false
		return
	}
	p := pal(c)
	addr := s.got[s.ask].Addr
	title := locales.WiFiDialogTitle(s.askName)
	mg.Modal(c.Key("wifi-dialog"), &s.askOpen, func() {
		mg.Column(c).Width(480).Gap(14).Label(title).Children(func() {
			mg.Text(c, title).FontSize(17).FontWeight(650).Role(mg.RoleHeading)
			mg.Text(c, locales.WiFiDialogWhat(nonEmpty(addr, "Wi-Fi"))).FontSize(13.5).TextColor(p.ink2)
			mg.Column(c).Gap(6).Padding(10, 12).Radius(8).Background(p.amberTint).Border(1, p.amberLine).Children(func() {
				mg.Row(c).Gap(8).AlignItems(mg.Start).Children(func() {
					mg.Icon(c, icAlert).FontSize(15).TextColor(p.amberDot).Shrink(0)
					mg.Text(c, locales.WiFiDialogRisk()).FontSize(13).Grow(1).MinWidth(0)
				})
			})
			mg.Text(c, locales.WiFiDialogUntil()).FontSize(13).TextColor(p.ink2)
			mg.Row(c).Gap(8).Justify(mg.End).AlignItems(mg.Center).Children(func() {
				if button(c, locales.WiFiDialogCancel(), btnOpt{}).Clicked() {
					s.askOpen = false
				}
				if button(c, locales.WiFiDialogConfirm(), btnOpt{kind: btnPrimary}).Clicked() {
					s.askOpen = false
					a.setPhoneWiFi(*d, true)
				}
			})
		})
	})
}

// networkOfflineText says why a phone reached over the network is offline
// and what to do, from its Setup; "" for any other offline phone.
func networkOfflineText(d gateway.DeviceView) string {
	name := d.Info.Name
	switch d.Info.Setup {
	case driver.SetupNetworkOff:
		if d.Info.Via == driver.ViaTailscale {
			return locales.PhoneAwayOff(name)
		}
		return locales.PhoneNetworkOff(name)
	case driver.SetupNetworkAway:
		if d.Info.Via == driver.ViaTailscale {
			return locales.PhoneTailnetAway(name)
		}
		return locales.PhoneNetworkAway(name)
	case driver.SetupNotOnWiFi:
		// An iPhone unplugged that is not seen on Wi-Fi (internal/driver/ios/lost.go).
		return locales.PhoneNotSeenOnWiFi(name)
	}
	return ""
}

// addAndroidWiFi is the end of Add phone › Android for a phone without
// Wireless debugging on a cable: how to use it without the cable.
func (a *app) addAndroidWiFi(c *mg.Context, d gateway.DeviceView) {
	st, have := a.phoneWiFiOf(c, d)
	if !have || st.On || st.Missing != "" || !noWirelessDebugging(d) || d.Info.Connection != "usb" {
		return
	}
	mg.Column(c).Gap(8).Children(func() {
		guideNote(c, locales.AddAndroidNoWirelessDebugging())
		mg.Row(c).Children(func() {
			if button(c, locales.WiFiUse(), btnOpt{small: true}).Disabled(a.phoneWiFi.busy[d.Info.ID]).Clicked() {
				a.askPhoneWiFi(d)
			}
		})
	})
}
