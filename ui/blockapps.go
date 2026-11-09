package ui

import (
	"context"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Settings › Safety › Add app: the field takes an app's name or package,
// and below it the apps on the connected phones that match what is typed,
// to pick from. What matches no app on the phones at all gets a warning,
// as Pinky blocks by the exact name: a typo would block nothing. Blocking
// an app picked, or typed by its exact name, blocks it by its id.

// installedAppLister lists the apps on the connected phones
// (gateway.InstalledApps).
type installedAppLister interface {
	InstalledApps(ctx context.Context) []gateway.InstalledApp
}

// appsState is the phones' apps that Add app offers.
type appsState struct {
	list   []gateway.InstalledApp
	loaded bool
	seq    int
	at     time.Time
}

// maxAppChoices is how many matching apps Add app shows at once.
const maxAppChoices = 8

func (s *appsState) load(a *app, l installedAppLister) {
	s.seq++
	seq := s.seq
	s.at = time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		list := l.InstalledApps(ctx)
		a.post(func() {
			if seq != s.seq {
				return
			}
			s.list, s.loaded = list, true
		})
	}()
}

// appMatches returns the apps whose name, other names or id hold q,
// without case, those whose name or id is q first, and whether one is.
// Apps that are blocked already are left out.
func appMatches(list []gateway.InstalledApp, q string) (out []gateway.InstalledApp, exact bool) {
	q = strings.ToLower(strings.TrimSpace(q))
	var rest []gateway.InstalledApp
	for _, x := range list {
		if x.Blocked {
			if q != "" && appIs(x, q) {
				exact = true
			}
			continue
		}
		switch {
		case q == "":
			rest = append(rest, x)
		case appIs(x, q):
			exact = true
			out = append(out, x)
		case appHolds(x, q):
			rest = append(rest, x)
		}
	}
	return append(out, rest...), exact
}

// appIs reports whether q (lower case) is the app's name, one of its other
// names or its id.
func appIs(x gateway.InstalledApp, q string) bool {
	if strings.ToLower(x.ID) == q || strings.ToLower(x.Name) == q {
		return true
	}
	for _, al := range x.Aliases {
		if strings.ToLower(al) == q {
			return true
		}
	}
	return false
}

// appHolds reports whether the app's name, other names or id hold q (lower
// case).
func appHolds(x gateway.InstalledApp, q string) bool {
	if strings.Contains(strings.ToLower(x.Name), q) || strings.Contains(strings.ToLower(x.ID), q) {
		return true
	}
	for _, al := range x.Aliases {
		if strings.Contains(strings.ToLower(al), q) {
			return true
		}
	}
	return false
}

// installedApp returns the one app on the phones whose name or id is text,
// to block it by its id.
func installedApp(list []gateway.InstalledApp, text string) (gateway.InstalledApp, bool) {
	q := strings.ToLower(strings.TrimSpace(text))
	var found []gateway.InstalledApp
	for _, x := range list {
		if appIs(x, q) {
			found = append(found, x)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return gateway.InstalledApp{}, false
}

// notListed returns the apps that Settings › Safety does not list as
// blocked already, marking those it does as blocked.
func (a *app) notListed(list []gateway.InstalledApp) []gateway.InstalledApp {
	out := make([]gateway.InstalledApp, len(list))
	for i, x := range list {
		for _, b := range a.settings.BlockedApps {
			if (b.ID != "" && b.ID == x.ID) || (b.ID == "" && strings.EqualFold(b.Name, x.Name)) {
				x.Blocked = true
				break
			}
		}
		out[i] = x
	}
	return out
}

// blockApp adds an app to Settings › Safety's blocked apps, unless a row
// stands for it already.
func (a *app) blockApp(b BlockedApp) {
	st := &a.settings
	for _, x := range st.BlockedApps {
		if (b.ID != "" && x.ID == b.ID) || strings.EqualFold(x.Name, b.Name) {
			a.set.newApp, a.set.adding = "", false
			return
		}
	}
	b.Kind = "Blocked by you"
	st.BlockedApps = append(st.BlockedApps, b)
	a.set.newApp, a.set.adding = "", false
	a.apps.at = time.Time{} // read the phones' apps again next time
	a.changed()
}

// blockTyped blocks what the person typed: the app on the phones of that
// name or id, else the name or package as typed.
func (a *app) blockTyped(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if x, ok := installedApp(a.apps.list, text); ok {
		a.blockApp(BlockedApp{Name: nonEmpty(x.Name, x.ID), ID: x.ID})
		return
	}
	a.blockApp(BlockedApp{Name: text})
}

// addBlockedApp is the field of Add app and the apps it offers.
func (a *app) addBlockedApp(c *mg.Context) {
	p := pal(c)
	l, canList := a.api.(installedAppLister)
	st := &a.apps
	if canList && (st.at.IsZero() || time.Since(st.at) > 30*time.Second) {
		st.load(a, l)
	}
	mg.Column(c).Gap(8).Margin(0, 0, 6, 0).Children(func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			in := mg.TextInput(c.Key("new-blocked"), &a.set.newApp).Placeholder(locales.SettingsSafetyAppPlaceholder()).Grow(1).AutoFocus().Label(locales.SettingsSafetyAppLabel())
			add := button(c, locales.SettingsSafetyBlock(), btnOpt{kind: btnPrimary})
			if in.Submitted() || add.Clicked() {
				a.blockTyped(a.set.newApp)
			}
		})
		if !canList {
			return
		}
		q := strings.TrimSpace(a.set.newApp)
		matches, exact := appMatches(a.notListed(st.list), q)
		switch {
		case !st.loaded:
			mg.Text(c, locales.SettingsSafetyAppsLoading()).FontSize(13).TextColor(p.ink3)
			return
		case len(st.list) == 0:
			mg.Text(c, locales.SettingsSafetyAppsNoPhone()).FontSize(13).TextColor(p.ink3)
			return
		case q != "" && !exact && len(matches) == 0:
			// Blocking it would match no app on the phones: a typo, or an
			// app not on them.
			mg.Text(c, locales.SettingsSafetyAppNoMatch(q)).FontSize(13).TextColor(p.amber).Role(mg.RoleStatus)
		}
		if len(matches) == 0 {
			return
		}
		mg.Column(c).Label(locales.SettingsSafetyAppsOnPhones()).Children(func() {
			mg.Text(c, locales.SettingsSafetyAppsOnPhones()).FontSize(12.5).FontWeight(600).TextColor(p.ink2).Margin(0, 0, 2, 0)
			for i, x := range matches {
				if i == maxAppChoices {
					mg.Text(c, locales.SettingsSafetyAppsMore(len(matches)-maxAppChoices)).FontSize(12.5).TextColor(p.ink3).Padding(4, 0)
					break
				}
				x := x
				name := nonEmpty(x.Name, x.ID)
				row := mg.ButtonBase(c.Key("pick-"+x.ID)).Gap(10).Padding(5, 8).Radius(8).AlignItems(mg.Center).FillWidth().
					Label(locales.SettingsSafetyBlockApp(name))
				if row.Hovered() {
					row.Background(p.bg3)
				}
				if row.Clicked() {
					a.blockApp(BlockedApp{Name: name, ID: x.ID})
				}
				row.Children(func() {
					appIcon(c, name, 24, 7, 12, blockedColor(name))
					mg.Text(c, name).MinWidth(0).SingleLine()
					mg.Text(c, x.ID).FontSize(12.5).TextColor(p.ink3).Grow(1).MinWidth(0).SingleLine()
				})
			}
		})
	})
}
