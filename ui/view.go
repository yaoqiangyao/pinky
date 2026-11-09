package ui

import (
	"runtime"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/assets"
	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
)

func iconPNG() []byte { return assets.Icon256 }

// view builds the main window: the sidebar and the page it chose.
func (a *app) view(c *mg.Context) {
	p := applyTheme(c)
	a.buildSeq++
	for _, msg := range a.toasts {
		c.Toast(msg)
	}
	a.toasts = nil
	if c.Shortcut(mg.Cmd, mg.KeyW) && a.main != nil {
		a.main.Hide()
	}
	mg.Row(c).Fill().AlignItems(mg.Stretch).Children(func() {
		a.sidebar(c)
		mg.Column(c).Grow(1).MinWidth(0).Background(p.bg).Children(func() {
			switch a.page {
			case pageActivity:
				a.activityPage(c)
			case pageSettings:
				a.settingsPage(c)
			case pageAddPhone:
				a.addPhonePage(c)
			default:
				a.phonesPage(c)
			}
		})
	})
	a.telemetryDialogs(c)
	a.updateDialogs(c)
	a.shotDialog(c)
	a.removeDialog(c)
	a.awayDialog(c)
	a.phoneWiFiDialog(c)
	a.sweepMirrors()
	// A QR pairing lasts while its step shows, and a recording plays while
	// Activity does.
	if (a.page != pageAddPhone || a.add.step != "qr" || a.add.pair.form) && a.add.pair.cancel != nil {
		a.stopPairing()
	}
	if a.page != pageActivity && a.rec.pl != nil {
		a.closeRecording()
	}
	// The panel shows approvals the page cannot: sync it after a page
	// change, once this build is done.
	if a.page != a.panelPage && a.main != nil {
		a.panelPage = a.page
		a.main.Update(a.syncPanel)
	}
}

// sidebarWidth is the width of the sidebar, and rightPanelWidth that of
// the Needs you and Tasks panel on the right of the Phone screen: the
// room between them is the phone's, or All phones'.
const (
	sidebarWidth    = 220
	rightPanelWidth = 320
)

// sidebar is the prototype's nav: the logo, the phones, Add phone, and
// Activity, Settings and Stop all at the bottom.
func (a *app) sidebar(c *mg.Context) {
	p := pal(c)
	bar := c.TitleBar()
	mg.Column(c).Width(sidebarWidth).Shrink(0).Background(p.bg2).BorderWidth(0, 1, 0, 0).BorderColor(p.line).
		Padding(0, 8, 12, 8).Gap(2).Children(func() {
		// The header: the window controls, then the brand.
		left := float32(6)
		if bar.Left > 0 {
			left = bar.Left - 8 + 10
		}
		mg.Row(c).Height(46).Shrink(0).AlignItems(mg.Center).Gap(8).Padding(0, 6, 0, left).DragWindow().Children(func() {
			mg.Image(c, logo).Size(20, 20).Label("Pinky")
			mg.Text(c, "Pinky").FontSize(14).FontWeight(650).TextColor(p.ink)
		})
		mg.Text(c, locales.SidebarPhones()).FontSize(12).FontWeight(600).TextColor(p.ink3).Padding(4, 10, 6, 10)
		a.sidebarPhones(c)
		add := phoneRow(c, a.page == pageAddPhone).MinHeight(36).TextColor(p.ink2)
		if add.Clicked() {
			a.page = pageAddPhone
			a.add.reset(a)
		}
		add.Label(locales.SidebarAddPhone()).Children(func() {
			mg.Icon(c, icPlus).FontSize(16)
			mg.Text(c, locales.SidebarAddPhone()).FontSize(14).FontWeight(500)
		})

		mg.Spacer(c)
		a.agentCard(c)
		a.updateCards(c)
		mg.Column(c).Gap(2).Padding(10, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
			footRow(c, icHistory, locales.SidebarActivity(), a.page == pageActivity, func() {
				a.page = pageActivity
				a.act.load(a)
			})
			footRow(c, icSettings, locales.SidebarSettings(), a.page == pageSettings, func() { a.page = pageSettings })
			stop := mg.ButtonBase(c).Height(34).Padding(0, 10).Radius(9).Gap(10).AlignItems(mg.Center).Justify(mg.Start).Label(locales.SidebarStopAll())
			if stop.Hovered() {
				stop.Background(p.redTint)
			}
			if stop.Clicked() {
				a.stopAll()
			}
			stop.Children(func() {
				mg.Text(c, locales.SidebarStopAll()).FontSize(13).TextColor(p.red).Grow(1)
				mg.Text(c, shortcutLabel()).FontSize(12.5).TextColor(p.red).Opacity(0.8)
			})
		})
	})
}

// sidebarPhones are the sidebar's rows of phones, after All phones when
// there are two or more. They scroll when there are more than the window
// shows, between the brand and Add phone, which stay in view with
// Activity, Settings and Stop all. The selected phone's row comes into
// view when the selection changes, as by a tile of All phones.
func (a *app) sidebarPhones(c *mg.Context) {
	p := pal(c)
	phones := a.page == pagePhones
	reveal := func(row mg.Element, id string) {
		if a.selShown != id {
			a.selShown = id
			row.ScrollIntoView()
		}
	}
	// The margins and padding leave room for the rows' focus rings, which
	// the view would clip, without moving the rows.
	ring := float32(3)
	list := mg.Scroll(c).MinHeight(0).Margin(-ring, -8).Padding(ring, 8).Gap(2).Label(locales.SidebarPhones())
	list.Children(func() {
		if len(a.devices) >= 2 {
			on := phones && a.sel == allPhones
			row := phoneRow(c, on)
			if row.Clicked() {
				a.selectPhone(allPhones)
			}
			if on {
				reveal(row, allPhones)
			}
			row.Label(locales.SidebarAllPhones()).Children(func() {
				mg.Icon(c, icGrid).FontSize(16).TextColor(p.ink2)
				mg.Text(c, locales.SidebarAllPhones()).FontSize(14).FontWeight(600)
			})
		}
		for _, d := range a.devices {
			d := d
			on := phones && a.sel == d.Info.ID
			row := phoneRow(c.Key(d.Info.ID), on)
			if row.Clicked() {
				a.selectPhone(d.Info.ID)
			}
			if on {
				reveal(row, d.Info.ID)
			}
			if a.canRemove() {
				row.ContextMenu(func(m *mg.Menu) { a.removeMenuItem(m, d) })
			}
			row.Label(locales.SidebarPhoneRow(d.Info.Name, statusLine(d))).Children(func() {
				statusDot(c, d)
				mg.Column(c).Grow(1).MinWidth(0).Children(func() {
					name := mg.Text(c, d.Info.Name).FontSize(14).FontWeight(600).SingleLine()
					if d.Status == gateway.DeviceOffline {
						name.TextColor(p.ink2)
					}
					mg.Text(c, statusLine(d)).FontSize(12).TextColor(p.ink2).SingleLine()
				})
			})
		}
	})
}

// phoneRow is the prototype's .prow.
func phoneRow(c *mg.Context, on bool) mg.Element {
	p := pal(c)
	row := mg.ButtonBase(c).MinHeight(40).Padding(6, 10).Radius(9).Gap(10).AlignItems(mg.Center).Justify(mg.Start).FillWidth()
	switch {
	case on:
		row.Background(p.sel)
		selectionMark(c, row)
	case row.Hovered():
		row.Background(p.bg3)
	}
	return row
}

// footRow is the prototype's .frow.
func footRow(c *mg.Context, ic *mg.SVG, text string, on bool, click func()) {
	p := pal(c)
	row := mg.ButtonBase(c).Height(34).Padding(0, 10).Radius(9).Gap(10).AlignItems(mg.Center).Justify(mg.Start).Label(text)
	col := p.ink2
	switch {
	case on:
		row.Background(p.sel)
		selectionMark(c, row)
		col = p.ink
	case row.Hovered():
		row.Background(p.bg3)
		col = p.ink
	}
	if row.Clicked() {
		click()
	}
	row.TextColor(col).Children(func() {
		mg.Icon(c, ic).FontSize(16)
		t := mg.Text(c, text).FontSize(14)
		if on {
			t.FontWeight(600)
		}
	})
}

// statusDot is a phone's dot: green and pulsing while an agent works,
// amber when it needs the person or setup, gray at rest, a ring offline.
func statusDot(c *mg.Context, d gateway.DeviceView) mg.Element {
	p := pal(c)
	switch d.Status {
	case gateway.DeviceWorking:
		return dot(c, p.green, true)
	case gateway.DeviceNeedsYou, gateway.DeviceNeedsSetup, gateway.DeviceLocked:
		return dot(c, p.amberDot, false)
	case gateway.DeviceOffline:
		return hollowDot(c)
	}
	return dot(c, p.ink3, false)
}

// pageHeader is the 58-high header of a page, which drags the window.
func pageHeader(c *mg.Context, fn func()) {
	p := pal(c)
	pad := float32(24)
	if bar := c.TitleBar(); bar.Right > 0 {
		pad = bar.Right + 12
	}
	mg.Row(c).MinHeight(58).Shrink(0).Padding(10, pad, 10, 24).Gap(10).AlignItems(mg.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line).DragWindow().Children(fn)
}

// unlockWords names the platform's way to prove presence and keep the PIN.
func unlockWords() (button, keep string) {
	switch runtime.GOOS {
	case "darwin":
		return locales.PlatformUnlockMac(), locales.PlatformUnlockKeepMac()
	case "windows":
		return locales.PlatformUnlockWindows(), locales.PlatformUnlockKeepWindows()
	}
	return locales.PlatformUnlockOther(), locales.PlatformUnlockKeepOther()
}
