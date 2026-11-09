package ui

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// A phone's checkup: item by item, what Pinky needs on the phone and on
// this computer, checked for real by the gateway and the phone's driver,
// with what to do about each item that does not work. It shows on the
// phone's page in place of the mirror (from ⋯, Settings › Phones, or a
// phone that is not connected), and as the last step of Add phone.

// checkupRunner runs a phone's checkup (gateway.Checkup).
type checkupRunner interface {
	Checkup(ctx context.Context, deviceID string) (gateway.CheckupView, error)
}

// checkupState is the client's checkups: the last of each phone, and the
// phone whose checkup its page shows.
type checkupState struct {
	runs    map[string]*checkupRun
	showing string
	// family is the maker's system each phone ran at its last checkup
	// while online, for the steps of a phone that is not connected now.
	family map[string]string
}

// checkupRun is a phone's last checkup.
type checkupRun struct {
	view    gateway.CheckupView
	err     error
	have    bool // view holds a finished checkup
	running bool
	state   driver.State // the phone's state when it ran
	seq     int
}

// errNoCheckup: the gateway runs no checkups.
var errNoCheckup = errors.New("no checkups")

var icCheckup = icon(`<path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect x="9" y="3" width="6" height="4" rx="1"/><path d="m9 14 2 2 4-4"/>`)
var icMinus = icon(`<path d="M6 12h12"/>`)

// checkupOf returns the phone's last checkup, nil before the first.
func (a *app) checkupOf(id string) *checkupRun {
	if a.chk.runs == nil {
		return nil
	}
	return a.chk.runs[id]
}

// runCheckup checks the phone again in the background, unless a checkup
// of it is under way.
func (a *app) runCheckup(d gateway.DeviceView) {
	if a.chk.runs == nil {
		a.chk.runs, a.chk.family = map[string]*checkupRun{}, map[string]string{}
	}
	id := d.Info.ID
	run := a.chk.runs[id]
	if run == nil {
		run = &checkupRun{}
		a.chk.runs[id] = run
	}
	cr, ok := a.api.(checkupRunner)
	if !ok {
		run.state, run.err = d.Info.State, errNoCheckup
		return
	}
	if run.running {
		return
	}
	run.running, run.state = true, d.Info.State
	run.seq++
	seq := run.seq
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		v, err := cr.Checkup(ctx, id)
		a.post(func() {
			if run.seq != seq {
				return
			}
			run.running = false
			run.err = err
			if err == nil || len(v.Checks) > 0 {
				run.view, run.have = v, true
			}
			if err == nil && v.Family+v.System != "" {
				a.chk.family[id] = v.Family
			}
		})
	}()
}

// keepChecked runs the phone's checkup when it has none yet, and again when
// the phone's connection changed since (it came online, it went away).
func (a *app) keepChecked(d gateway.DeviceView) *checkupRun {
	run := a.checkupOf(d.Info.ID)
	if run == nil || (!run.running && run.state != d.Info.State) {
		a.runCheckup(d)
		run = a.checkupOf(d.Info.ID)
	}
	return run
}

// openCheckup shows the phone's checkup on its page, checking it afresh.
func (a *app) openCheckup(d gateway.DeviceView) {
	a.selectPhone(d.Info.ID)
	a.chk.showing = d.Info.ID
	a.runCheckup(d)
}

// checkupPanel is the phone page's middle while its checkup shows: the
// heading with its buttons, and the items, which scroll.
func (a *app) checkupPanel(c *mg.Context, d gateway.DeviceView) {
	p := pal(c)
	run := a.keepChecked(d)
	mg.Column(c).FillWidth().MaxWidth(560).Grow(1).MinHeight(0).Gap(14).Label(locales.CheckupFor(d.Info.Name)).Children(func() {
		mg.Row(c).Gap(12).AlignItems(mg.Center).Shrink(0).Children(func() {
			mg.Icon(c, icCheckup).FontSize(22).TextColor(p.ink2)
			mg.Column(c).Grow(1).MinWidth(0).Children(func() {
				mg.Text(c, locales.CheckupFor(d.Info.Name)).FontSize(19).FontWeight(650).Role(mg.RoleHeading)
				mg.Text(c, locales.CheckupIntro()).FontSize(13.5).TextColor(p.ink2)
			})
		})
		mg.Row(c).Gap(10).AlignItems(mg.Center).Shrink(0).Children(func() {
			if button(c, locales.CheckupBack(), btnOpt{icon: icChevL}).Clicked() {
				a.chk.showing = ""
			}
			again := button(c, locales.CheckupAgain(), btnOpt{}).Disabled(run != nil && run.running)
			if again.Clicked() {
				a.runCheckup(d)
			}
		})
		mg.Scroll(c).FillWidth().Grow(1).MinHeight(0).Gap(14).Padding(0, 4, 16, 0).Children(func() {
			a.checkupList(c, d, run, false)
		})
	})
}

// checkupList shows a phone's checkup: a summary line, then each item.
// Compact, items that passed show their name alone.
func (a *app) checkupList(c *mg.Context, d gateway.DeviceView, run *checkupRun, compact bool) {
	p := pal(c)
	if run == nil || (!run.have && run.running) {
		statusBox(c, func() {
			spinner(c)
			mg.Text(c, locales.CheckupRunning()).Grow(1).MinWidth(0)
		})
		return
	}
	if !run.have {
		msg := locales.CheckupUnsupported()
		if run.err != nil && !errors.Is(run.err, errNoCheckup) {
			msg = locales.CheckupFailed(errText(run.err))
		}
		statusBox(c, func() {
			mg.Icon(c, icAlert).FontSize(16).TextColor(p.amberDot)
			mg.Text(c, msg).Grow(1).MinWidth(0)
		})
		return
	}
	v := run.view
	checks := sortedChecks(v.Checks)
	fix := 0
	for _, ck := range checks {
		if ck.Status == driver.CheckFail {
			fix++
		}
	}
	statusBox(c, func() {
		switch {
		case run.running:
			spinner(c)
			mg.Text(c, locales.CheckupRunning()).Grow(1).MinWidth(0)
		case run.err != nil:
			mg.Icon(c, icAlert).FontSize(16).TextColor(p.amberDot)
			mg.Text(c, locales.CheckupFailed(errText(run.err))).Grow(1).MinWidth(0)
		case fix > 0:
			dot(c, p.amberDot, false)
			mg.Text(c, locales.CheckupToFix(fix)).FontWeight(600).Grow(1).MinWidth(0)
		case d.Info.State != driver.StateOnline:
			dot(c, p.amberDot, false)
			mg.Text(c, locales.CheckupWaitOnline()).Grow(1).MinWidth(0)
		default:
			dot(c, p.green, false)
			mg.Text(c, locales.CheckupAllGood()).FontWeight(600).Grow(1).MinWidth(0)
		}
	})
	if v.System != "" {
		mg.Text(c, locales.CheckupSystem(v.System)).FontSize(12.5).TextColor(p.ink3).Margin(-8, 0, 0, 0)
	}
	mg.Column(c).Gap(2).Role(mg.RoleList).Label(locales.CheckupFor(d.Info.Name)).Children(func() {
		for i, ck := range checks {
			a.checkItem(c.Key(ck.ID), d, v, ck, i == len(checks)-1, compact && ck.Status == driver.CheckPass)
		}
	})
	if d.Info.State != driver.StateOnline && fix > 0 {
		mg.Text(c, locales.CheckupWaitOnline()).FontSize(13).TextColor(p.ink2)
	}
}

// iphoneWiFiText says what an iPhone's Wi-Fi item found.
func iphoneWiFiText(d gateway.DeviceView, ck driver.Check) string {
	switch {
	case ck.Status == driver.CheckPass && d.Info.Connection == "wifi":
		return locales.CheckConnectionWiFi()
	case ck.Status == driver.CheckPass:
		return locales.CheckWiFiIPhoneSeen()
	case ck.Fix == driver.FixSameNetwork:
		return locales.CheckWiFiIPhoneNotSeen()
	case ck.Fix == driver.FixWiFiSetting:
		return locales.CheckWiFiIPhoneOff()
	case ck.Status == driver.CheckFail:
		return locales.CheckWiFiIPhoneFail()
	default:
		return locales.CheckWiFiIPhoneUnknown()
	}
}

// checkOrder is the order items show in; others follow.
var checkOrder = []string{driver.CheckHost, driver.CheckADB, driver.CheckConnection, driver.CheckAuthorized, driver.CheckCompanion,
	driver.CheckDeveloperMode, driver.CheckDiskImage, driver.CheckHelper,
	driver.CheckInput, driver.CheckScreen, driver.CheckBackground, driver.CheckWiFi, driver.CheckLocalNetwork}

func sortedChecks(in []driver.Check) []driver.Check {
	rank := func(id string) int {
		for i, o := range checkOrder {
			if o == id {
				return i
			}
		}
		return len(checkOrder)
	}
	out := append([]driver.Check(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].ID) < rank(out[j].ID) })
	return out
}

// checkItem is one item: its mark, its name, what Pinky found and, for one
// that failed or has a limit, what to do.
func (a *app) checkItem(c *mg.Context, d gateway.DeviceView, v gateway.CheckupView, ck driver.Check, last, short bool) {
	p := pal(c)
	words := a.checkWords(d, v, ck)
	if short {
		words = checkWords{title: words.title}
	}
	pad := float32(10)
	if short {
		pad = 6
	}
	row := mg.Row(c).Gap(12).AlignItems(mg.Start).Padding(pad, 2).Role(mg.RoleListItem).Label(words.title)
	if !last {
		row.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	}
	row.Children(func() {
		checkMark(c, ck.Status)
		mg.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			mg.Text(c, words.title).FontSize(14.5).FontWeight(600)
			if words.text != "" {
				t := mg.Text(c, words.text).FontSize(13.5).LineHeight(1.4)
				if ck.Status == driver.CheckPass || ck.Status == driver.CheckSkip {
					t.TextColor(p.ink2)
				}
			}
			for i, step := range words.steps {
				mg.Row(c.Key(i)).Gap(6).AlignItems(mg.Start).Children(func() {
					mg.Text(c, "•").FontSize(13.5).TextColor(p.ink3)
					mg.Text(c, step).FontSize(13.5).LineHeight(1.4).Grow(1).MinWidth(0)
				})
			}
			if words.path != "" {
				menuPath(c, words.path, "")
			}
			if words.note != "" {
				mg.Text(c, words.note).FontSize(12.5).TextColor(p.ink3).LineHeight(1.4)
			}
			if ck.Detail != "" && (ck.Status == driver.CheckFail || a.settings.DeveloperMode) && words.detail {
				mg.Text(c, ck.Detail).FontSize(12).TextColor(p.ink3).Selectable()
			}
			a.checkAction(c, d, ck)
		})
	})
}

// checkMark is an item's mark: a check, a cross, a warning or a dash.
func checkMark(c *mg.Context, st driver.CheckStatus) {
	p := pal(c)
	box := mg.Box(c).Size(22, 22).Radius(11).Center().Shrink(0).Margin(1, 0, 0, 0)
	switch st {
	case driver.CheckPass:
		box.Background(p.green).Label(locales.CheckupPassLabel()).Children(func() {
			mg.Icon(c, icCheck).FontSize(13).TextColor(p.white)
		})
	case driver.CheckFail:
		box.Background(p.red).Label(locales.CheckupFailLabel()).Children(func() {
			mg.Icon(c, icX).FontSize(13).TextColor(p.white)
		})
	case driver.CheckWarn:
		box.Background(p.amberTint).Border(1, p.amberLine).Label(locales.CheckupWarnLabel()).Children(func() {
			mg.Icon(c, icAlert).FontSize(13).TextColor(p.amberDot)
		})
	default:
		box.Border(1, p.line2).Label(locales.CheckupSkipLabel()).Children(func() {
			mg.Icon(c, icMinus).FontSize(13).TextColor(p.ink3)
		})
	}
}

// checkWords is an item in the person's words.
type checkWords struct {
	title string
	text  string   // what Pinky found
	steps []string // what to do, in order
	path  string   // where, as a menu path
	note  string
	// detail shows the driver's own words under a failed item, where the
	// client's do not say it all.
	detail bool
}

// checkWords puts an item into words, with the steps of the phone's maker.
func (a *app) checkWords(d gateway.DeviceView, v gateway.CheckupView, ck driver.Check) checkWords {
	if d.Info.Platform == driver.IOS {
		if w, ok := a.iphoneCheckWords(d, ck); ok {
			return w // checkup_iphone.go
		}
	}
	android := d.Info.Platform == driver.Android
	b := brandByID(brandOfFamily(a.familyOf(d.Info.ID, v)))
	w := checkWords{title: checkTitle(ck.ID), detail: true}
	pass := ck.Status == driver.CheckPass
	switch ck.ID {
	case driver.CheckHost:
		if android {
			w.title = locales.CheckADBTitle()
		}
		w.text = hostWords(ck.Fix)
		w.detail = true
		if w.text == "" {
			w.text, w.detail = ck.Detail, false
		}
	case driver.CheckADB:
		if pass {
			w.text, w.detail = locales.CheckADBPass(), false
		} else {
			w.text = locales.CheckADBFail()
		}
	case driver.CheckConnection:
		switch {
		case pass && d.Info.Via == driver.ViaTailscale:
			w.text = locales.CheckConnectionTailscale()
		case pass && d.Info.Connection == "wifi":
			w.text = locales.CheckConnectionWiFi()
		case pass:
			w.text = locales.CheckConnectionUSB()
		case android:
			w.text = locales.CheckConnectionAndroidFail(d.Info.Name)
			w.steps = []string{locales.CheckConnectionAndroidStepCable(), locales.CheckConnectionAndroidStepDebugging()}
			w.path = b.dev()
			w.note = locales.CheckConnectionAndroidNote()
		default:
			w.text = locales.CheckConnectionFail(d.Info.Name)
		}
	case driver.CheckAuthorized:
		switch {
		case pass:
			w.text, w.detail = locales.CheckAuthorizedPass(), false
		case ck.Status == driver.CheckSkip:
			w.text, w.detail = locales.CheckAuthorizedSkip(), false
		case android && ck.Fix == driver.FixAuthorize:
			w.text, w.detail = locales.CheckAuthorizedAndroidFail(), false
			w.note = locales.AddAndroidNoPrompt()
		case ck.Fix == driver.FixUSBPermission || (android && ck.Fix == "" && runtime.GOOS == "linux"):
			// Android lists a phone as needing setup when the computer may
			// not open its USB device (Linux without udev rules)…
			w.text = locales.CheckUSBPermission()
			w.note = locales.CheckUSBPermissionNote()
		case android:
			// …or, on Windows, when its USB debugging has no driver
			// (FixUSBDriver): the driver says which to install.
			w.text = locales.CheckUSBNotReady()
		default:
			w.text = nonEmpty(ck.Detail, locales.CheckAuthorizedFail())
			w.detail = false
		}
	case driver.CheckCompanion:
		switch ck.Fix {
		case "":
			if pass {
				w.text, w.detail = locales.CheckCompanionPass(), false
			} else {
				w.text = locales.CheckCompanionNone()
			}
		case driver.FixInstallCompanion:
			w.text, w.detail = locales.CheckCompanionMissing(), false
		case driver.FixTurnOnCompanion:
			w.text, w.detail = locales.CheckCompanionOff(), false
			switch b.id {
			case "xiaomi":
				w.note = locales.CheckCompanionOffXiaomi()
			case "oppo":
				w.note = locales.CheckCompanionOffOppo()
			case "vivo":
				w.note = locales.CheckCompanionOffVivo()
			case "huawei", "honor":
				w.note = locales.CheckCompanionOffHuawei()
			default:
				w.note = locales.CheckCompanionOffOther()
			}
		case driver.FixUpdateCompanion:
			w.text, w.detail = locales.CheckCompanionOutdated(), false
			if a.comp.state[d.Info.ID] == driver.CompanionPublicKey {
				w.text = locales.CheckCompanionPublicKey()
			}
		}
		if msg := a.comp.err[d.Info.ID]; msg != "" && !pass {
			w.note = msg
		}
	case driver.CheckInput:
		switch {
		case pass:
			w.text, w.detail = locales.CheckInputPass(), false
		case ck.Fix == driver.FixInputPermission:
			w.text = locales.CheckInputRefused()
			w.steps, w.path, w.note = inputSteps(b)
		case ck.Fix == driver.FixRestart:
			w.text = locales.CheckInputRefused()
			w.steps = []string{locales.CheckInputRestart()}
		default:
			w.text = locales.CheckInputFail()
		}
	case driver.CheckScreen:
		switch {
		case pass:
			w.text, w.detail = locales.CheckScreenPass(), false
		case ck.Fix == driver.FixVideoEncoder:
			w.text = locales.CheckScreenNoVideo()
		default:
			w.text = locales.CheckScreenFail()
		}
	case driver.CheckBackground:
		switch {
		case pass:
			w.text, w.detail = locales.CheckBackgroundPass(), false
		case ck.Fix == driver.FixUnlockedOnly:
			w.text, w.detail = locales.CheckBackgroundUnlocked(), false
		case ck.Fix == driver.FixNewerAndroid:
			w.text, w.detail = locales.CheckBackgroundOld(d.Info.OSVersion), false
		default:
			w.text = locales.CheckBackgroundFail()
		}
	case driver.CheckWiFi:
		if d.Info.Platform == driver.IOS {
			// From what Pinky knows of the phone's Wi-Fi connection
			// (gateway.WiFiCheck).
			w.text, w.detail = iphoneWiFiText(d, ck), true
			if d.Info.WiFi.Connections == driver.WiFiConnectionsTurnedOn && ck.Status != driver.CheckFail {
				w.note = locales.CheckWiFiIPhoneTurnedOn()
			}
			break
		}
		switch {
		case pass && d.Info.Via == driver.ViaTailscale:
			w.text, w.detail = locales.CheckConnectionTailscale(), false
		case pass && d.Info.Connection == "wifi":
			w.text, w.detail = locales.CheckConnectionWiFi(), false
		case pass:
			w.text, w.detail = locales.CheckWiFiPass(), false
		case ck.Fix == driver.FixNewerAndroid:
			w.text, w.detail = locales.CheckWiFiOld(d.Info.OSVersion), false
		default:
			w.text = locales.CheckWiFiFail()
		}
	case driver.CheckLocalNetwork:
		switch {
		case pass:
			w.text, w.detail = locales.CheckLocalNetworkPass(), false
		case ck.Fix == driver.FixLocalNetwork:
			w.text, w.detail = locales.CheckLocalNetworkFail(), false
			w.note = locales.CheckLocalNetworkNote()
		default:
			w.text = locales.CheckLocalNetworkUnknown()
		}
	default:
		w.title = nonEmpty(ck.ID, "?")
		w.text, w.detail = ck.Detail, false
	}
	return w
}

// setupStep is the step a phone that is connected but not online waits
// for, in the words its checkup gives the step (the "authorized" item,
// whose fix is the step): an iPhone's setup steps (setupWords), allowing
// USB debugging, the computer's access to the phone over USB; the
// driver's words for a step the client has none for.
func (a *app) setupStep(d gateway.DeviceView) checkWords {
	for _, ck := range gateway.ConnectionChecks(d.Info) {
		if ck.ID == driver.CheckAuthorized {
			return a.checkWords(d, gateway.CheckupView{DeviceID: d.Info.ID, Platform: d.Info.Platform}, ck)
		}
	}
	return checkWords{text: d.Info.Detail}
}

// hostWords says what the computer lacks for Android phones, by the
// Android driver's HostCheck codes; "" for codes of other platforms.
func hostWords(missing string) string {
	switch missing {
	case "adb":
		return locales.CheckHostNoADB()
	case "adb_server":
		return locales.CheckHostADBServer()
	case "adb_old":
		return locales.CheckHostADBOld()
	case "adb_restarts":
		return locales.CheckHostADBRestarts()
	}
	return ""
}

// familyOf is the maker's system of the phone, from this checkup, or the
// last one that read it.
func (a *app) familyOf(id string, v gateway.CheckupView) string {
	if v.Family != "" || v.System != "" {
		return v.Family
	}
	return a.chk.family[id]
}

// inputSteps says how to let the computer tap on a maker's phones.
func inputSteps(b brand) (steps []string, path, note string) {
	switch b.id {
	case "xiaomi":
		return []string{locales.CheckInputXiaomi()}, b.dev(), locales.CheckInputXiaomiNote()
	case "oppo":
		return []string{locales.CheckInputOppo()}, b.dev(), ""
	}
	return []string{locales.CheckInputOther()}, b.dev(), locales.CheckInputOtherNote()
}

func checkTitle(id string) string {
	switch id {
	case driver.CheckHost:
		return locales.CheckHostTitle()
	case driver.CheckADB:
		return locales.CheckADBTitle()
	case driver.CheckConnection:
		return locales.CheckConnectionTitle()
	case driver.CheckAuthorized:
		return locales.CheckAuthorizedTitle()
	case driver.CheckCompanion:
		return locales.CheckCompanionTitle()
	case driver.CheckInput:
		return locales.CheckInputTitle()
	case driver.CheckScreen:
		return locales.CheckScreenTitle()
	case driver.CheckBackground:
		return locales.CheckBackgroundTitle()
	case driver.CheckWiFi:
		return locales.CheckWiFiTitle()
	case driver.CheckLocalNetwork:
		return locales.CheckLocalNetworkTitle()
	}
	if t := iphoneCheckTitle(id); t != "" {
		return t
	}
	return id
}

// checkAction is an item's button, where Pinky can fix it.
func (a *app) checkAction(c *mg.Context, d gateway.DeviceView, ck driver.Check) {
	if d.Info.Platform == driver.IOS && a.iphoneCheckAction(c, d, ck) {
		return // checkup_iphone.go
	}
	id := d.Info.ID
	switch ck.Fix {
	case driver.FixInstallCompanion, driver.FixTurnOnCompanion, driver.FixUpdateCompanion:
		label := locales.CompanionInstall()
		switch ck.Fix {
		case driver.FixTurnOnCompanion:
			label = locales.CompanionTurnOn()
		case driver.FixUpdateCompanion:
			label = locales.CompanionUpdate()
		}
		busy := !a.comp.installing[id].IsZero()
		mg.Row(c).Gap(10).AlignItems(mg.Center).Margin(4, 0, 0, 0).Children(func() {
			if busy {
				spinner(c)
				mg.Text(c, a.allowHint(id)).FontSize(13).FontWeight(600).Grow(1).MinWidth(0)
				c.After(time.Second)
				return
			}
			if button(c, label, btnOpt{small: true, kind: btnPrimary}).Clicked() {
				a.installCompanion(d)
			}
		})
	case driver.FixLocalNetwork:
		if runtime.GOOS == "darwin" {
			mg.Row(c).Margin(4, 0, 0, 0).Children(func() {
				if button(c, locales.PairingOpenLocalNetwork(), btnOpt{small: true}).Clicked() {
					openURL(c, localNetworkSettings, func(error) {})
				}
			})
		}
	}
}

// menuPath draws a menu path as chips joined by arrows: the way through
// Settings, without a screenshot. tap, when set, is the item at its end to
// tap 7 times.
func menuPath(c *mg.Context, path, tap string) {
	p := pal(c)
	parts := pathParts(path)
	all := parts
	if tap != "" {
		all = append(append([]string(nil), parts...), tap)
	}
	mg.Row(c).Gap(4).Wrap().AlignItems(mg.Center).Role(mg.RoleGroup).Label(strings.Join(all, " › ")).Children(func() {
		chip := func(i int, s string, target bool) {
			b := mg.Row(c.Key(i)).Gap(5).Padding(3, 8).Radius(6).AlignItems(mg.Center).Background(p.bg3)
			if target {
				b.Background(p.accentTint).Border(1, p.accent)
			}
			b.Children(func() {
				t := mg.Text(c, s).FontSize(12.5).FontWeight(600)
				if target {
					t.TextColor(p.accentInk)
					mg.Text(c, locales.GuideTapTimes()).FontSize(11.5).FontWeight(700).TextColor(p.accentInk)
				}
			})
		}
		for i, s := range parts {
			if i > 0 {
				mg.Icon(c.Key(-1-i), icChevR).FontSize(12).TextColor(p.ink3)
			}
			chip(i, s, false)
		}
		if tap != "" {
			if len(parts) > 0 {
				mg.Icon(c.Key("tap-arrow"), icChevR).FontSize(12).TextColor(p.ink3)
			}
			chip(len(parts), tap, true)
		}
	})
}

// errText is an error for a status line, without a final period.
func errText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return locales.CheckupTimeout()
	}
	return strings.TrimSuffix(err.Error(), ".")
}
