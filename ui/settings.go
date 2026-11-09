package ui

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/agents"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

type settingsSection int

const (
	secSafety settingsSection = iota
	secPhones
	secAgents
	secPrivacy
	secAdvanced
)

type settingsState struct {
	sec     settingsSection
	adding  bool
	newApp  string
	confirm bool
}

// changed reports a change in Settings, with the settings as they were
// before it.
func (a *app) changed() {
	a.changes++
	old := a.reported
	a.reported = a.settings.clone()
	if a.opt.OnSettings != nil {
		a.opt.OnSettings(old, a.settings.clone())
	}
}

// clone returns s with a list of blocked apps of its own.
func (s Settings) clone() Settings {
	s.BlockedApps = append([]BlockedApp(nil), s.BlockedApps...)
	return s
}

func (a *app) settingsPage(c *mg.Context) {
	p := pal(c)
	s := &a.set
	pageHeader(c, func() { mg.Text(c, locales.SettingsPageTitle()).FontSize(17).FontWeight(650) })
	mg.Row(c).Grow(1).MinHeight(0).AlignItems(mg.Stretch).Children(func() {
		mg.Column(c).Width(200).Shrink(0).Padding(16, 12).Gap(2).BorderWidth(0, 1, 0, 0).BorderColor(p.line).
			Role(mg.RoleTabList).Label(locales.SettingsSectionsLabel()).Children(func() {
			tab := func(sec settingsSection, name string, needs bool) {
				on := s.sec == sec
				b := mg.ButtonBase(c.Key(sec)).Height(36).Padding(0, 12).Radius(8).Gap(8).AlignItems(mg.Center).Justify(mg.Start).
					Role(mg.RoleTab).Selected(on).Label(name)
				col := p.ink2
				switch {
				case on:
					b.Background(p.bg3)
					selectionMark(c, b)
					col = p.ink
				case b.Hovered():
					b.Background(p.bg2)
					col = p.ink
				}
				if b.Clicked() {
					s.sec = sec
				}
				b.TextColor(col).Children(func() {
					t := mg.Text(c, name).FontSize(14).Grow(1)
					if on {
						t.FontWeight(600)
					}
					if needs {
						dot(c, p.amberDot, false).Label(locales.SettingsNeedsYou())
					}
				})
			}
			tab(secPhones, locales.SettingsSectionPhones(), false)
			agentsWait := false
			for _, n := range a.needs {
				agentsWait = agentsWait || newAgentNeed(n)
			}
			tab(secAgents, locales.SettingsSectionAgents(), agentsWait)
			tab(secSafety, locales.SettingsSectionSafety(), len(a.files.list) > 0)
			tab(secPrivacy, locales.SettingsSectionPrivacy(), false)
			tab(secAdvanced, locales.SettingsSectionAdvanced(), false)
			a.feedbackLink(c)
		})
		a.watchFileProblems(c)
		a.watchGatewaySettings(c)
		mg.Scroll(c).Grow(1).MinWidth(0).Padding(24, 40).Children(func() {
			mg.Column(c).MaxWidth(640).Gap(22).Children(func() {
				switch s.sec {
				case secPhones:
					a.settingsPhones(c)
				case secAgents:
					a.settingsAgents(c)
				case secPrivacy:
					a.settingsPrivacy(c)
				case secAdvanced:
					a.settingsAdvanced(c)
				default:
					a.settingsSafety(c)
				}
			})
		})
	})
}

func settingsTitle(c *mg.Context, title, desc string) {
	p := pal(c)
	mg.Column(c).Children(func() {
		mg.Text(c, title).FontSize(20).FontWeight(650).Role(mg.RoleHeading)
		mg.Text(c, desc).FontSize(13.5).TextColor(p.ink2).Margin(4, 0, 0, 0)
	})
}

// toggleRow is a setting row with a switch; clicking its words toggles
// it too, as a form's field label does. It reports a change, which it
// saves with the settings.
func (a *app) toggleRow(c *mg.Context, key, title, desc string, last bool, on *bool) bool {
	changed := a.toggleRowFor(c, key, title, desc, last, on)
	if changed {
		a.changed()
	}
	return changed
}

// toggleRowFor is toggleRow for a choice the gateway keeps rather than the
// settings file.
func (a *app) toggleRowFor(c *mg.Context, key, title, desc string, last bool, on *bool) bool {
	p := pal(c)
	changed := false
	r := mg.Row(c.Key(key)).Gap(16).Padding(10, 0).AlignItems(mg.Center)
	if !last {
		r.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	}
	r.Children(func() {
		words := mg.Column(c).Grow(1).MinWidth(0).Gap(2).Cursor(mg.CursorPointer)
		if words.Clicked() {
			*on = !*on
			changed = true
		}
		words.Children(func() {
			mg.Text(c, title).FontWeight(600)
			if desc != "" {
				mg.Text(c, desc).FontSize(13.5).TextColor(p.ink2)
			}
		})
		if toggle(c.Key(key+"-switch"), on, title).Changed() {
			changed = true
		}
	})
	return changed
}

// settingRow is the prototype's .trow in Settings: a label and a
// description, with a control at the end.
func settingRow(c *mg.Context, title, desc string, last bool, control func()) {
	p := pal(c)
	r := mg.Row(c).Gap(16).Padding(10, 0).AlignItems(mg.Center)
	if !last {
		r.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	}
	r.Children(func() {
		mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			mg.Text(c, title).FontWeight(600)
			if desc != "" {
				mg.Text(c, desc).FontSize(13.5).TextColor(p.ink2)
			}
		})
		if control != nil {
			control()
		}
	})
}

func (a *app) settingsSafety(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	settingsTitle(c, locales.SettingsSectionSafety(), locales.SettingsSafetyIntro())
	a.fileProblemNotes(c)
	a.offWhile(c, rulesYAML).Gap(8).Label(locales.SettingsSafetyAskGroup()).Children(func() {
		mg.Text(c, locales.SettingsSafetyAskTitle()).FontSize(15).FontWeight(650).Margin(0, 0, 4, 0)
		presets := []struct{ title, desc string }{
			{locales.SettingsSafetyPresetCommits(), locales.SettingsSafetyPresetCommitsDesc()},
			{locales.SettingsSafetyPresetPayments(), locales.SettingsSafetyPresetPaymentsDesc()},
			{locales.SettingsSafetyPresetEvery(), locales.SettingsSafetyPresetEveryDesc()},
		}
		mg.Column(c).Gap(8).Role(mg.RoleRadioGroup).Label(locales.SettingsSafetyAskTitle()).Children(func() {
			for i, pr := range presets {
				v := i + 1
				opt := mg.RadioBase(c.Key(v), &st.Preset, v).Gap(12).Padding(10, 14).Radius(10).AlignItems(mg.Start).FillWidth()
				on := st.Preset == v
				border := p.line
				if on {
					border = p.accent
					opt.Shadow(0, 0, 0, 1, p.accent)
				} else if opt.Hovered() {
					border = p.line2
				}
				if opt.Changed() {
					a.changed()
				}
				opt.Border(1, border).Background(p.panel).Children(func() {
					rb := mg.Box(c).Size(16, 16).Radius(8).Margin(2, 0, 0, 0).Shrink(0).Background(p.panel)
					if on {
						rb.Border(5, p.accentBtn)
					} else {
						rb.Border(1.5, p.line2)
					}
					mg.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
						mg.Text(c, pr.title).FontWeight(600)
						mg.Text(c, pr.desc).FontSize(13.5).TextColor(p.ink2)
					})
				})
			}
		})
	})
	a.offWhile(c, appsYAML).Gap(8).Label(locales.SettingsSafetyBlockedApps()).Children(func() {
		mg.Row(c).Gap(12).AlignItems(mg.End).Children(func() {
			mg.Column(c).Grow(1).Children(func() {
				mg.Text(c, locales.SettingsSafetyBlockedApps()).FontSize(15).FontWeight(650)
				mg.Text(c, locales.SettingsSafetyBlockedIntro()).FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
			})
			if button(c, locales.SettingsSafetyAddApp(), btnOpt{}).Clicked() {
				a.set.adding = !a.set.adding
			}
		})
		if a.set.adding {
			a.addBlockedApp(c)
		}
		mg.Column(c).Children(func() {
			for i, b := range st.BlockedApps {
				i, b := i, b
				mg.Row(c.Key("b-"+b.Name)).Gap(12).Padding(7, 0).AlignItems(mg.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
					appIcon(c, b.Name, 28, 8, 13, blockedColor(b.Name))
					mg.Text(c, b.Name).Grow(1)
					mg.Text(c, blockedKind(b.Kind)).FontSize(13.5).TextColor(p.ink2)
					rm := mg.ButtonBase(c).Size(26, 26).Radius(6).Center().Label(locales.SettingsSafetyUnblockApp(b.Name)).Tooltip(locales.SettingsSafetyUnblock())
					if rm.Hovered() {
						rm.Background(p.bg3)
					}
					if rm.Clicked() {
						st.BlockedApps = append(st.BlockedApps[:i:i], st.BlockedApps[i+1:]...)
						a.changed()
					}
					rm.TextColor(p.ink3).Children(func() { mg.Icon(c, icX).FontSize(14) })
				})
			}
		})
	})
	a.alwaysAllowed(c)
	mg.Column(c).Label(locales.SettingsSafetyProtections()).Children(func() {
		a.offWhile(c, rulesYAML).Children(func() {
			if a.toggleRow(c, "ro", locales.SettingsSafetyReadOnly(), locales.SettingsSafetyReadOnlyDesc(), false, &st.ReadOnly) {
				a.applyReadOnly(st.ReadOnly)
			}
		})
		desc := locales.SettingsSafetyUnlockDescOther()
		switch runtime.GOOS {
		case "darwin":
			desc = locales.SettingsSafetyUnlockDescMac()
		case "windows":
			desc = locales.SettingsSafetyUnlockDescWindows()
		}
		if a.pin.ready && !a.pin.supported {
			settingRow(c, locales.SettingsUnlockFromComputer(), locales.UnlockNotAvailable(), false, nil)
		} else {
			a.toggleRow(c, "unlock", locales.SettingsUnlockFromComputer(), desc, false, &st.UnlockFromComputer)
		}
		a.offWhile(c, rulesYAML).Children(func() {
			a.toggleRow(c, "codes", locales.SettingsSafetyHideCodes(), locales.SettingsSafetyHideCodesDesc(), true, &st.HideCodes)
		})
	})
	a.rulesSection(c)
}

// blockedKinds name the kinds of blocked apps, which the settings keep in
// English: policy.CategoryLabel's (cmd/pinky) and the defaults'.
var blockedKinds = map[string]func() string{
	"Finance":        locales.SettingsSafetyKindFinance,
	"Passwords":      locales.SettingsSafetyKindPasswords,
	"Blocked by you": locales.RuleBlockedByYou,
	"Banking":        locales.SettingsSafetyKindBanking,
	"Wallet":         locales.SettingsSafetyKindWallet,
	"Messaging":      locales.SettingsSafetyKindMessaging,
	"Social":         locales.SettingsSafetyKindSocial,
	"Stores":         locales.SettingsSafetyKindStores,
	"Browsers":       locales.SettingsSafetyKindBrowsers,
	"Phone":          locales.SettingsSafetyKindPhone,
	"Settings":       locales.SettingsSafetyKindSettings,
	"System":         locales.SettingsSafetyKindSystem,
}

// blockedKind is a blocked app's kind as Settings › Safety shows it. A
// category of the person's own app table keeps its name.
func blockedKind(kind string) string {
	if f := blockedKinds[kind]; f != nil {
		return f()
	}
	return kind
}

func blockedColor(name string) string {
	switch name {
	case "Northbank":
		return "#1e3a8a"
	case "Vaultly":
		return "#334155"
	case "Coinpouch":
		return "#b45309"
	}
	return ""
}

func (a *app) settingsPhones(c *mg.Context) {
	p := pal(c)
	settingsTitle(c, locales.SettingsSectionPhones(), locales.SettingsPhonesIntro())
	mg.Column(c).Label(locales.SettingsSectionPhones()).Children(func() {
		if len(a.devices) == 0 {
			mg.Text(c, locales.SettingsPhonesNone()).TextColor(p.ink2).Padding(10, 0)
		}
		for i, d := range a.devices {
			d := d
			last := i == len(a.devices)-1
			box := mg.Column(c.Key(d.Info.ID)).Padding(10, 0).Gap(4).Label(d.Info.Name)
			if !last {
				box.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
			}
			box.Children(func() {
				if foundOnWiFi(d) {
					// Found on Wi-Fi: nothing to set until the person says
					// it is theirs (wifi.go).
					mg.Row(c).Gap(16).AlignItems(mg.Center).Children(func() {
						mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							mg.Text(c, d.Info.Name).FontWeight(600)
							line := locales.SettingsPhonesFound()
							if l := foundLine(d); l != "" {
								line = l + " · " + line
							}
							mg.Text(c, line).FontSize(13.5).TextColor(p.ink2)
						})
						a.foundButtons(c, d, true)
					})
					return
				}
				mg.Row(c).Gap(16).AlignItems(mg.Center).Children(func() {
					mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						mg.Text(c, d.Info.Name).FontWeight(600)
						line := osLine(d)
						if cl := companionLine(a.companionFor(d)); cl != "" {
							line += " · " + cl
						}
						mg.Text(c, line).FontSize(13.5).TextColor(p.ink2)
					})
					mg.Row(c).Gap(6).AlignItems(mg.Center).Children(func() {
						word := locales.SettingsPhonesConnected()
						switch {
						case d.Status == gateway.DeviceConnecting:
							dot(c, p.ink3, false)
							word = locales.SettingsPhonesConnecting()
						case d.Status == gateway.DeviceOffline:
							hollowDot(c)
							word = locales.SettingsPhonesOffline()
						case d.Info.State != driver.StateOnline:
							dot(c, p.amberDot, false)
							word = locales.SettingsPhonesNeedsSetup()
						default:
							dot(c, p.green, false)
						}
						mg.Text(c, word).FontSize(13).TextColor(p.ink2)
					})
					if a.canRemove() && quietButton(c.Key("remove-"+d.Info.ID), locales.RemovePhoneButton()).
						Label(locales.RemovePhoneButtonLabel(d.Info.Name)).Clicked() {
						a.askRemove(d)
					}
				})
				// Unlocking from this computer: the phone's PIN. A saved one
				// can be removed even with unlocking turned off.
				if a.pin.saved[d.Info.ID] || (d.Features.Unlock && a.settings.UnlockFromComputer) {
					mg.Column(c).Gap(6).Padding(6, 0, 0, 0).Children(func() {
						mg.Text(c, locales.SettingsUnlockFromComputer()).FontSize(13.5).FontWeight(600)
						a.pinForm(c, d, "settings")
					})
				}
				if d.Features.Background {
					a.backgroundRow(c, d, true)
				}
				a.connectionRows(c, d)
				a.awayRow(c, d)
				a.phoneWiFiRow(c, d)
				mg.Row(c).Padding(4, 0, 0, 0).Children(func() {
					if linkButton(c, locales.PhoneCheckup()).Label(locales.SettingsPhonesCheckupLabel(d.Info.Name)).Clicked() {
						a.openCheckup(d)
					}
				})
			})
		}
	})
	mg.Row(c).Children(func() {
		if button(c, locales.SettingsPhonesAdd(), btnOpt{}).Clicked() {
			a.page = pageAddPhone
			a.add.reset(a)
		}
	})
	mg.Column(c).Label(locales.SettingsPhonesWiFi()).Children(func() {
		if a.toggleRow(c, "ios-wifi", locales.SettingsPhonesWiFi(), locales.SettingsPhonesWiFiDesc(), true, &a.settings.IOSWiFi) {
			a.applyWiFi(a.settings.IOSWiFi)
		}
	})
	a.awayKeepAwakeRow(c)
}

// agentsState is Settings › Agents': the gateway's agents and the scopes
// being chosen.
type agentsState struct {
	list     []gateway.AgentView
	loaded   bool
	err      error
	seq      int
	lastLoad time.Time
	asks     int               // open first-connection requests, when last read
	editing  string            // the agent whose scope is being changed
	choice   map[string]string // scope mode being chosen, by agent or need id
	apps     map[string]string // apps typed for "Use only the apps I choose"
}

func (s *agentsState) load(a *app) {
	al, ok := a.api.(agentLister)
	if !ok {
		return
	}
	s.seq++
	seq := s.seq
	s.lastLoad = time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		list, err := al.Agents(ctx)
		a.post(func() {
			if seq != s.seq {
				return
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			s.list, s.err, s.loaded = list, err, true
		})
	}()
}

// splitApps reads the apps field: names separated by commas, including
// the full-width comma and the ideographic comma of Chinese text.
func splitApps(s string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\uff0c' || r == '\u3001' }) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// scopePicker is the select of what an agent may do, with the apps field
// for "Use only the apps I choose". It returns the scope chosen.
func (a *app) scopePicker(c *mg.Context, key, label string, initial gateway.Scope) gateway.Scope {
	st := &a.agents
	if st.choice == nil {
		st.choice, st.apps = map[string]string{}, map[string]string{}
	}
	// The choice is kept as a mode, so it survives a change of language.
	mode, ok := st.choice[key]
	if !ok {
		mode = string(initial.Mode)
		st.apps[key] = strings.Join(initial.Apps, ", ")
	}
	choice := scopeLabel(gateway.ScopeMode(mode))
	labels := make([]string, len(scopeChoices))
	for i, sc := range scopeChoices {
		labels[i] = sc.label()
	}
	mg.Select(c.Key("scope-"+key), &choice, labels).Label(label).MaxWidth(360)
	scope := gateway.Scope{Mode: scopeOf(choice)}
	st.choice[key] = string(scope.Mode)
	mg.Text(c, scopeHint(scope.Mode)).FontSize(12.5).TextColor(pal(c).ink2).MaxWidth(360).Margin(4, 0, 0, 0)
	if scope.Mode == gateway.ScopeApps {
		apps := st.apps[key]
		mg.TextInput(c.Key("apps-"+key), &apps).Placeholder(locales.SettingsAgentsAppsPlaceholder()).Label(locales.SettingsAgentsApps()).MaxWidth(360)
		st.apps[key] = apps
		scope.Apps = splitApps(apps)
	}
	return scope
}

// approveAgent allows an agent's first connection with the chosen scope:
// SetAgentScope, which also approves the agent, then Decide, so the
// gateway keeps the person's choice when the agent picks up the answer.
func (a *app) approveAgent(n gateway.NeedView, scope gateway.Scope) {
	a.remember(n, gateway.AllowOnce, "")
	scoper, ok := a.api.(agentScoper)
	name := n.Agent
	a.call(n.DeviceID, locales.SettingsAgentsApprove(), func() error {
		if ok && name != "" {
			scoper.SetAgentScope(name, scope)
		}
		return a.api.Decide(n.ID, gateway.AllowOnce, "")
	})
}

func (a *app) settingsAgents(c *mg.Context) {
	p := pal(c)
	st := &a.agents
	settingsTitle(c, locales.SettingsSectionAgents(), locales.SettingsAgentsIntro())
	_, canList := a.api.(agentLister)
	_, canScope := a.api.(agentScoper)
	if canList {
		if !st.loaded || time.Since(st.lastLoad) > 5*time.Second || st.lastLoad.IsZero() {
			st.load(a)
		}
		c.After(5 * time.Second)
	}
	// First connections waiting for the person, as the prototype's request.
	for _, n := range a.needs {
		if !newAgentNeed(n) {
			continue
		}
		n := n
		mg.Column(c.Key("req-"+n.ID)).Padding(18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(10).
			Label(locales.SettingsAgentsRequest()).Children(func() {
			mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
				dot(c, p.amberDot, false)
				mg.Text(c, locales.SettingsNeedsYou()).FontSize(13).FontWeight(600).TextColor(p.amber)
			})
			mg.Column(c).Children(func() {
				mg.Text(c, needTitle(n)).FontSize(16).FontWeight(650)
				if d := agentRequestDetail(n, canScope); d != "" {
					mg.Text(c, d).FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
				}
			})
			a.agentRequestControls(c, n)
		})
	}
	type row struct {
		name, line string
		state      string // "working", "allowed", "waiting"
		scope      gateway.Scope
		known      bool
	}
	var rows []row
	working := map[string]bool{}
	for _, t := range a.tasks {
		if running(t.State) && t.Agent != "" {
			working[t.Agent] = true
		}
	}
	switch {
	case canList && st.loaded:
		for _, ag := range st.list {
			r := row{name: ag.Name, line: scopeLine(ag.Scope), scope: ag.Scope, known: true, state: "allowed"}
			if !ag.Approved {
				r.line, r.state = locales.SettingsAgentsNotAllowed(), "waiting"
			}
			if working[ag.Name] {
				r.state = "working"
			}
			rows = append(rows, r)
		}
	case a.opt.Agents != nil:
		for _, ag := range a.opt.Agents {
			r := row{name: ag.Name, line: nonEmpty(ag.Scope, locales.SettingsAgentsViaMCP()), state: "allowed"}
			if ag.Connected {
				r.state = "working"
			}
			rows = append(rows, r)
		}
	default:
		seen := map[string]bool{}
		for _, t := range append(append([]gateway.TaskView(nil), a.tasks...), a.act.tasks...) {
			if t.Agent != "" && !seen[t.Agent] {
				seen[t.Agent] = true
				r := row{name: t.Agent, line: locales.SettingsAgentsViaMCP(), state: "allowed"}
				if working[t.Agent] {
					r.state = "working"
				}
				rows = append(rows, r)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	}
	mg.Column(c).Label(locales.SettingsAgentsList()).Children(func() {
		if len(rows) == 0 {
			none := locales.SettingsAgentsNone()
			for _, st := range a.setup.list {
				if st.State == agents.Connected {
					none = locales.SettingsAgentsNoneYet()
				}
			}
			mg.Text(c, none).TextColor(p.ink2).Padding(10, 0)
		}
		for i, r := range rows {
			r := r
			last := i == len(rows)-1
			box := mg.Column(c.Key("agent-"+r.name)).Padding(10, 0).Gap(10)
			if !last {
				box.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
			}
			box.Children(func() {
				mg.Row(c).Gap(16).AlignItems(mg.Center).Children(func() {
					appIcon(c, r.name, 28, 8, 13, "#5450d6")
					mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						mg.Text(c, r.name).FontWeight(600)
						mg.Text(c, r.line).FontSize(13.5).TextColor(p.ink2)
					})
					mg.Row(c).Gap(6).AlignItems(mg.Center).Children(func() {
						switch r.state {
						case "working":
							dot(c, p.green, false)
							mg.Text(c, locales.SettingsAgentsWorking()).FontSize(13).TextColor(p.ink2)
						case "waiting":
							dot(c, p.amberDot, false)
							mg.Text(c, locales.SettingsAgentsWaiting()).FontSize(13).TextColor(p.ink2)
						default:
							dot(c, p.ink3, false)
							mg.Text(c, locales.SettingsAgentsAllowed()).FontSize(13).TextColor(p.ink2)
						}
					})
					if canScope && r.known && r.state != "waiting" {
						label := locales.SettingsChange()
						if st.editing == r.name {
							label = locales.SettingsClose()
						}
						if button(c, label, btnOpt{}).Label(locales.SettingsAgentsChangeLabel(r.name)).Clicked() {
							if st.editing == r.name {
								st.editing = ""
							} else {
								st.editing = r.name
								delete(st.choice, r.name)
							}
						}
					}
				})
				if st.editing == r.name && canScope {
					mg.Column(c).Gap(8).Padding(0, 0, 0, 44).Children(func() {
						scope := a.scopePicker(c, r.name, locales.SettingsAgentsScopeLabel(r.name), r.scope)
						if button(c, locales.SettingsSave(), btnOpt{kind: btnPrimary}).AlignSelf(mg.Start).Clicked() {
							if sc, ok := a.api.(agentScoper); ok {
								name := r.name
								go func() {
									sc.SetAgentScope(name, scope)
									a.post(func() { a.agents.load(a) })
								}()
							}
							st.editing = ""
						}
					})
				}
			})
		}
	})
	if !canList {
		mg.Text(c, locales.SettingsAgentsUnlisted()).FontSize(12.5).TextColor(p.ink3)
	}
	a.connectAgents(c)
	a.remoteAgents(c)
}

// keepChoices are the retention limits Settings offers, with the current
// one when it is something else (from the settings file).
func keepChoices(cur int) []int {
	out := []int{7, 30, 90, 365, 0}
	for _, d := range out {
		if d == cur {
			return out
		}
	}
	return append([]int{cur}, out...)
}

// keepLabel is a retention limit as the select shows it, in the language
// shown. The select maps a label back to its limit in the same frame.
func keepLabel(days int) string { return keepLabelIn(locales.In(i18n.Locale()), days) }

// keepLabelIn names a retention in the language of m; feedback's
// diagnostics are English.
func keepLabelIn(m locales.Messages, days int) string {
	switch {
	case days <= 0:
		return m.SettingsPrivacyKeepForever()
	case days == 365:
		return m.SettingsPrivacyKeepYear()
	}
	return m.SettingsPrivacyKeepDays(days)
}

func (a *app) settingsPrivacy(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	settingsTitle(c, locales.SettingsSectionPrivacy(), locales.SettingsPrivacyIntro())
	mg.Column(c).Label(locales.SettingsSectionPrivacy()).Children(func() {
		settingRow(c, locales.SettingsPrivacyKeep(), locales.SettingsPrivacyKeepDesc(), false, func() {
			choices := keepChoices(st.KeepDays)
			labels := make([]string, len(choices))
			for i, d := range choices {
				labels[i] = keepLabel(d)
			}
			cur := keepLabel(st.KeepDays)
			if mg.Select(c.Key("keep"), &cur, labels).Label(locales.SettingsPrivacyKeep()).Changed() {
				for i, l := range labels {
					if l == cur {
						st.KeepDays = choices[i]
					}
				}
				a.changed()
				a.applyRetention(st.KeepDays)
			}
		})
		if a.toggleRow(c, "rec", locales.SettingsPrivacyRecord(), locales.SettingsPrivacyRecordDesc(), false, &st.Record) {
			a.applyRecording(st.Record)
		}
		settingRow(c, locales.SettingsPrivacyDeleteAll(), locales.SettingsPrivacyDeleteAllDesc(), true, func() {
			b := button(c, locales.SettingsPrivacyDeleteButton(), btnOpt{})
			if _, ok := a.api.(historyWiper); !ok {
				b.Disabled(true).Tooltip(locales.SettingsPrivacyCannotDelete())
			} else if b.Clicked() {
				a.set.confirm = true
			}
		})
	})
	a.privacyTelemetry(c)
	if a.set.confirm {
		switch mg.AlertDialog(c, &a.set.confirm, locales.SettingsPrivacyDeleteTitle(),
			locales.SettingsPrivacyDeleteMessage(),
			locales.SettingsCancel(), locales.SettingsPrivacyDeleteConfirm()) {
		case 1:
			a.wipeHistory()
		}
	}
	_ = p
}

// wipeHistory deletes the gateway's history, from Settings › Privacy.
func (a *app) wipeHistory() {
	w, ok := a.api.(historyWiper)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		err := w.WipeHistory(ctx)
		a.post(func() {
			if err != nil {
				a.toasts = append(a.toasts, locales.SettingsPrivacyDeleteFailed(err.Error()))
				return
			}
			a.mediaCache = map[string]*mediaEntry{}
			a.act.tasks, a.act.sel = nil, ""
			a.act.load(a)
			a.toasts = append(a.toasts, locales.SettingsPrivacyDeleted())
		})
	}()
}

func (a *app) settingsAdvanced(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	settingsTitle(c, locales.SettingsSectionAdvanced(), locales.SettingsAdvancedIntro())
	mg.Column(c).Label(locales.SettingsSectionAdvanced()).Children(func() {
		a.languageRow(c)
		a.toggleRow(c, "dev", locales.SettingsAdvancedDevMode(), locales.SettingsAdvancedDevModeDesc(), false, &st.DeveloperMode)
		if st.DeveloperMode {
			var parts []string
			for _, d := range a.devices {
				if d.Info.Backend != "" {
					parts = append(parts, fmt.Sprintf("%s: %s", d.Info.Name, d.Info.Backend))
				}
			}
			backends := strings.Join(parts, ". ")
			if backends == "" {
				backends = locales.SettingsAdvancedNoPhones()
			} else {
				backends += "."
			}
			settingRow(c, locales.SettingsAdvancedBackends(), backends, false, nil)
			conn := locales.SettingsAdvancedConnStdio(agentCommand())
			if st.RemoteAgents {
				conn = locales.SettingsAdvancedConnHTTP(agentCommand(), RemoteURL(st.RemoteAddr))
			}
			settingRow(c, locales.SettingsAdvancedConnection(), conn, false, nil)
			if v := videoLine(a); v != "" {
				settingRow(c, locales.SettingsAdvancedVideo(), v, false, nil)
			}
		}
		settingRow(c, locales.SettingsAdvancedRulesFile(), locales.SettingsAdvancedRulesFileDesc(), true, func() {
			b := button(c, locales.SettingsAdvancedEditRules(), btnOpt{})
			if a.opt.RulesFile == "" {
				b.Disabled(true).Tooltip(locales.SettingsAdvancedNoRulesFile())
			} else if b.Clicked() {
				path := a.opt.RulesFile
				go func() {
					if err := mygo.Shell.OpenPath(path); err != nil {
						a.post(func() { a.toasts = append(a.toasts, locales.SettingsAdvancedRulesOpenFailed(err.Error())) })
					}
				}()
			}
		})
		a.fileProblemNotes(c)
	})
	a.settingsUpdates(c)
	_ = p
}
