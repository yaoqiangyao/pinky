package ui

import (
	"fmt"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
)

// rightPanel is the column of Needs you and Tasks.
func (a *app) rightPanel(c *mg.Context) {
	p := pal(c)
	mg.Scroll(c).Width(rightPanelWidth).Shrink(0).BorderWidth(0, 0, 0, 1).BorderColor(p.line).Background(p.panel).
		Padding(20, 16).Gap(24).Label(locales.NeedsAndTasksLabel()).Children(func() {
		if len(a.needs) > 0 {
			mg.Column(c).Label(locales.NeedsSection()).Children(func() {
				sectionLabel(c, locales.NeedsSection())
				mg.Column(c).Gap(10).Children(func() {
					for _, n := range a.needs {
						a.needCard(c.Key("need-"+n.ID), n, false)
					}
				})
			})
		}
		if d := a.device(a.sel); d != nil && a.isLocked(*d) && len(a.needsOf(d.Info.ID)) == 0 {
			a.lockCard(c, *d)
		}
		if d := a.device(a.sel); d != nil && d.Features.Companion {
			a.companionCard(c, *d)
		}
		mg.Column(c).Label(locales.TasksSection()).Children(func() {
			sectionLabel(c, locales.TasksSection())
			mg.Column(c).Gap(8).Children(func() { a.taskList(c) })
		})
	})
}

func (a *app) taskList(c *mg.Context) {
	p := pal(c)
	shown := 0
	// The selected phone's task first, expanded, then the others' running
	// tasks, one row each.
	if a.sel != allPhones {
		if t := a.taskOf(a.sel); t != nil {
			a.taskCard(c.Key("task-"+t.ID), *t)
			shown++
		}
	}
	for _, d := range a.devices {
		if d.Info.ID == a.sel {
			continue
		}
		t := a.taskOf(d.Info.ID)
		if t == nil || (!running(t.State) && a.sel != allPhones) {
			continue
		}
		a.taskRow(c.Key("row-"+t.ID), *t, d)
		shown++
	}
	if shown == 0 {
		mg.Column(c).Padding(10, 2).Gap(6).Children(func() {
			if a.opt.AgentSetup != nil && a.setup.loaded && !a.agentsConnected() {
				mg.Text(c, locales.TasksNoAgent()).FontSize(14).TextColor(p.ink2)
				if linkButton(c, locales.ConnectTitle()).AlignSelf(mg.Start).Clicked() {
					a.page, a.set.sec = pageSettings, secAgents
				}
				return
			}
			mg.Text(c, locales.TasksNone()).FontSize(14).TextColor(p.ink2)
		})
	}
}

// needCard is the prototype's .ncard, for an approval, a handoff or an
// unlock. In the floating panel (floating true) it has no outer card.
func (a *app) needCard(c *mg.Context, n gateway.NeedView, floating bool) {
	p := pal(c)
	card := mg.Column(c).Padding(14).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(2)
	card.Children(func() {
		needEyebrow(c, n)
		mg.Text(c, needTitle(n)).FontSize(16).FontWeight(650).Margin(6, 0, 0, 0)
		detail := needDetail(n)
		if newAgentNeed(n) {
			_, picker := a.api.(agentScoper)
			detail = agentRequestDetail(n, picker)
		}
		if detail != "" {
			mg.Text(c, detail).FontSize(13.5).TextColor(p.ink2)
		}
		switch {
		case n.Kind == gateway.NeedUnlock:
			a.unlockControls(c, n)
		case n.Kind == gateway.NeedHandoff:
			a.handoffControls(c, n)
		case newAgentNeed(n):
			a.agentRequestControls(c, n)
		case n.Unblock:
			a.unblockControls(c, n)
		default:
			if a.cardShot(n) {
				b := n.Screenshot.Bounds()
				_, zoom := marksOf(n).focus(b.Dx(), b.Dy())
				h := figureHeight(cardFigureW, cardFigure, cardWide, cardLens, b.Dx(), b.Dy(), zoom)
				a.approvalFigure(c, n, func() { a.shotOpen = n.ID }).Height(h).Margin(12, 0, 0, 0)
			}
			a.approvalControls(c, n)
		}
	})
}

// cardShot reports whether a Needs you card shows its approval's
// screenshot: where the middle of the window does not show that phone's
// screen with the target marked, as for another phone, All phones, or a
// locked phone or one without video.
func (a *app) cardShot(n gateway.NeedView) bool {
	if n.Screenshot == nil || n.Kind != gateway.NeedApproval || newAgentNeed(n) || a.noting[n.ID] {
		return false
	}
	if a.sel != n.DeviceID {
		return true
	}
	if d := a.device(n.DeviceID); d != nil && a.isLocked(*d) {
		return true
	}
	m := a.mirrors[n.DeviceID]
	return m != nil && m.state == "failed"
}

func needEyebrow(c *mg.Context, n gateway.NeedView) {
	p := pal(c)
	text := n.DeviceName
	if n.App != "" {
		text += " · " + n.App
	}
	if text == "" {
		text = locales.NeedsFirstConnection() // a New agent request is about no phone
	}
	mg.Row(c).Gap(7).AlignItems(mg.Center).Children(func() {
		dot(c, p.amberDot, false)
		mg.Text(c, text).FontSize(12.5).FontWeight(600).TextColor(p.amber).SingleLine()
	})
}

// handoffControls answer a handoff: the agent asked the person to do
// something on the phone and waits. Done hands the phone back with the
// note, Take over gives the person the mirror, Decline tells the agent no.
// On a locked phone, unlocking from the computer is the first step.
func (a *app) handoffControls(c *mg.Context, n gateway.NeedView) {
	p := pal(c)
	agent := agentOf(n)
	locked := a.unlocks[n.DeviceID]
	// The person took over and the agent waits for them to hand back.
	holding := false
	for _, t := range a.tasks {
		holding = holding || (t.ID == n.TaskID && t.State == gateway.TaskPaused)
	}
	// The phone waits for the step the agent asks for: nothing to take
	// over, and the gateway ends the handoff once the phone is ready.
	d := a.device(n.DeviceID)
	setup := d != nil && d.Status == gateway.DeviceNeedsSetup
	switch {
	case setup:
		mg.Text(c, locales.NeedsHandoffSetup(agent)).FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
	case holding && !locked:
		mg.Text(c, locales.NeedsHandoffInControl(agent)).FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
	case locked && a.canUnlockHere(n.DeviceID):
		a.unlockButton(c, n.DeviceID).Margin(12, 0, 0, 0).FillWidth()
		// Unlocking hands the phone back only when unlocking is all the
		// agent asked for.
		how := locales.NeedsHandoffLockedHereThenDone(agent)
		if n.EndsOnUnlock {
			how = locales.NeedsHandoffLockedHere(agent)
		}
		mg.Text(c, how).FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
	case locked:
		mg.Text(c, locales.NeedsHandoffLocked(a.unlockElsewhere(n.DeviceID, locales.NeedsThenClickDone()))).
			FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
		if d := a.device(n.DeviceID); d != nil && a.pinMissing(*d) {
			mg.Column(c).Margin(8, 0, 0, 0).Gap(8).Children(func() { a.pinForm(c, *d, "card") })
		}
	default:
		mg.Text(c, locales.NeedsHandoffHowTo()).FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
	}
	noting := a.noting[n.ID]
	if noting {
		mg.Text(c, locales.NeedsNoteFor(agent)).FontSize(13).FontWeight(600).Margin(10, 0, 0, 0)
		note := a.notes[n.ID]
		mg.TextArea(c.Key("hnote-"+n.ID), &note).Label(locales.NeedsNoteFor(agent)).Placeholder(locales.NeedsHandoffNotePlaceholder()).
			Lines(2, 4).Margin(6, 0, 0, 0).AutoFocus()
		a.notes[n.ID] = note
	}
	note := strings.TrimSpace(a.notes[n.ID])
	declineButton := locked || holding || setup
	mg.Grid(c).Columns(2).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		if declineButton {
			if button(c, locales.NeedsDecline(), btnOpt{}).Tooltip(locales.NeedsDeclineTooltip(agent)).Clicked() {
				a.decide(n, gateway.Deny, note)
			}
		} else if button(c, locales.PhoneTakeOver(), btnOpt{}).Tooltip(locales.NeedsTakeOverTooltip()).Clicked() {
			a.decide(n, gateway.TakeOver, "")
		}
		done := btnOpt{kind: btnPrimary}
		if locked {
			done = btnOpt{}
		}
		if button(c, locales.NeedsDone(), done).Tooltip(locales.NeedsDoneTooltip(agent)).Clicked() {
			a.decide(n, gateway.AllowOnce, note)
		}
	})
	mg.Row(c).Gap(16).Margin(10, 0, 0, 0).Children(func() {
		if !noting && linkButton(c, locales.NeedsAddNote()).Clicked() {
			a.noting[n.ID] = true
		}
		if !declineButton && linkButton(c, locales.NeedsDecline()).Tooltip(locales.NeedsDeclineTooltip(agent)).Clicked() {
			a.decide(n, gateway.Deny, note)
		}
	})
}

// agentRequestControls answer an agent's first connection: what it may do,
// then Deny or Approve. An agent that asked to only browse and read is
// offered that first.
func (a *app) agentRequestControls(c *mg.Context, n gateway.NeedView) {
	p := pal(c)
	scope := gateway.Scope{Mode: gateway.ScopeEverything}
	if n.ReadOnly {
		scope.Mode = gateway.ScopeReadOnly
	}
	if _, ok := a.api.(agentScoper); ok {
		if n.ReadOnly {
			mg.Text(c, locales.NeedsAgentAskedReadOnly(agentOf(n))).FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
		}
		mg.Text(c, locales.NeedsLetAgent(agentOf(n))).FontSize(13).FontWeight(600).Margin(12, 0, 6, 0)
		scope = a.scopePicker(c, n.ID, locales.NeedsLetAgent(agentOf(n)), scope)
	}
	noApps := scope.Mode == gateway.ScopeApps && len(scope.Apps) == 0
	mg.Grid(c).Columns(2).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		if button(c, locales.NeedsDeny(), btnOpt{}).Clicked() {
			a.decide(n, gateway.Deny, "")
		}
		ok := button(c, locales.NeedsApprove(), btnOpt{kind: btnPrimary}).Disabled(noApps)
		if noApps {
			ok.Tooltip(locales.NeedsApproveNoApps())
		}
		if ok.Clicked() {
			a.approveAgent(n, scope)
		}
	})
	_ = p
}

// unblockControls answer an agent's wish to open a blocked app: what
// allowing it does, then Deny or Allow for this task. Any Allow counts for
// the task (NeedView.Unblock).
func (a *app) unblockControls(c *mg.Context, n gateway.NeedView) {
	p := pal(c)
	mg.Text(c, locales.NeedsUnblockHow(nonEmpty(n.App, locales.PanelThisApp()), ruleName(n.Rule), agentOf(n))).
		FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0)
	mg.Grid(c).Columns(2).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		if button(c, locales.NeedsDeny(), btnOpt{}).Clicked() {
			a.decide(n, gateway.Deny, "")
		}
		if button(c, locales.NeedsAllowTask(), btnOpt{kind: btnPrimary}).Clicked() {
			a.decide(n, gateway.AllowTask, "")
		}
	})
}

// approvalControls are Deny, Allow and More options, or the note editor
// of Deny with a note.
func (a *app) approvalControls(c *mg.Context, n gateway.NeedView) {
	p := pal(c)
	noting := a.noting[n.ID]
	if noting {
		mg.Text(c, locales.NeedsNoteFor(agentOf(n))).FontSize(13).FontWeight(600).Margin(10, 0, 0, 0)
		note, ok := a.notes[n.ID]
		if !ok {
			note = ""
		}
		mg.TextArea(c.Key("note-"+n.ID), &note).Label(locales.NeedsNoteFor(agentOf(n))).Placeholder(locales.NeedsDenyNotePlaceholder()).
			Lines(2, 4).Margin(6, 0, 0, 0).AutoFocus()
		a.notes[n.ID] = note
	}
	mg.Grid(c).Columns(2).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		if button(c, locales.NeedsDeny(), btnOpt{}).Clicked() {
			a.decide(n, gateway.Deny, strings.TrimSpace(a.notes[n.ID]))
		}
		if button(c, locales.NeedsAllow(), btnOpt{kind: btnPrimary}).Clicked() {
			a.decide(n, gateway.AllowOnce, "")
		}
	})
	if noting {
		return
	}
	more := linkButton(c, locales.NeedsMoreOptions()).Margin(10, 0, 0, 0).AlignSelf(mg.Start)
	more.PopoverMenuButton(func(m *mg.Menu) {
		// Only where the rest of the task skips the question: not the
		// agent's own questions, where a yes answers that question only.
		if n.AllowTask && m.Item(locales.NeedsAllowTask()).Chosen() {
			a.decide(n, gateway.AllowTask, "")
		}
		// Only where the gateway can keep it: not the agent's own questions,
		// files, opening apps or links, or while every action asks.
		if n.AlwaysAllow && m.Item(alwaysAllowLabel(n)).Chosen() {
			a.decide(n, gateway.AllowAlways, "")
		}
		if m.Item(locales.PhoneTakeOver()).Chosen() {
			a.decide(n, gateway.TakeOver, "")
		}
		if m.Item(locales.NeedsDenyWithNote()).Chosen() {
			a.noting[n.ID] = true
		}
	})
	_ = p
}

func agentOf(n gateway.NeedView) string {
	if n.Agent != "" {
		return n.Agent
	}
	return locales.AgentGeneric()
}

// taskStatus words a task's state for its card.
func (a *app) taskStatus(c *mg.Context, t gateway.TaskView) (string, mg.Color, bool) {
	p := pal(c)
	d := a.device(t.DeviceID)
	switch t.State {
	case gateway.TaskWorking:
		if d != nil && a.isLocked(*d) {
			return locales.TasksStatusLocked(), p.amberDot, false
		}
		if d != nil && d.Status == gateway.DeviceNeedsSetup {
			// Connected while the phone waited for a step, or it needs one
			// again: the agent waits for the person (device_needs_setup).
			return locales.TasksStatusNeedsSetup(), p.amberDot, false
		}
		return locales.TasksStatusWorking(), p.green, true
	case gateway.TaskWaiting:
		if d != nil && a.isLocked(*d) {
			return locales.TasksStatusLocked(), p.amberDot, false
		}
		return locales.TasksStatusWaiting(), p.amberDot, false
	case gateway.TaskPaused:
		return locales.TasksStatusPaused(), p.ink3, false
	case gateway.TaskDone:
		if denied(t) {
			return locales.TasksStatusEnded(), p.ink3, false
		}
		return locales.TasksStatusDone(), p.green, false
	case gateway.TaskStopped:
		return locales.TasksStatusStopped(), p.ink3, false
	case gateway.TaskFailed:
		return locales.TasksStatusFailed(), p.red, false
	}
	return string(t.State), p.ink3, false
}

// stepWord is the short state at the end of a collapsed task row.
func (a *app) stepWord(t gateway.TaskView, d gateway.DeviceView) string {
	switch {
	case t.State == gateway.TaskPaused:
		return locales.TasksRowPaused()
	case t.State == gateway.TaskDone && denied(t):
		return locales.TasksRowEnded()
	case t.State == gateway.TaskDone:
		return locales.TasksRowDone()
	case t.State == gateway.TaskStopped:
		return locales.TasksRowStopped()
	case t.State == gateway.TaskFailed:
		return locales.TasksRowFailed()
	case a.isLocked(d):
		return locales.TasksRowLocked()
	case t.State == gateway.TaskWaiting:
		return locales.TasksRowWaiting()
	case d.Status == gateway.DeviceNeedsSetup:
		return locales.TasksRowNeedsSetup()
	}
	return locales.TasksRowStep(len(t.Steps))
}

// taskCard is the prototype's expanded .tcard.
func (a *app) taskCard(c *mg.Context, t gateway.TaskView) {
	p := pal(c)
	d := a.device(t.DeviceID)
	status, col, live := a.taskStatus(c, t)
	run := running(t.State)
	mg.Column(c).Padding(14).Radius(12).Border(1, p.line).Background(p.panel).Children(func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(mg.Center).Children(func() {
				dot(c, col, live)
				mg.Text(c, status).FontSize(13).TextColor(p.ink2).SingleLine()
			})
			// Not while the phone is not open: offline, or waiting for a step.
			canTake := run && t.State != gateway.TaskPaused && d != nil && !a.isLocked(*d) && d.Status != gateway.DeviceInControl &&
				d.Status != gateway.DeviceNeedsSetup && d.Status != gateway.DeviceOffline && d.Status != gateway.DeviceConnecting
			if canTake && button(c, locales.PhoneTakeOver(), btnOpt{small: true}).Clicked() {
				a.takeOver(t.DeviceID)
			}
			if run && button(c, locales.TasksStop(), btnOpt{small: true, kind: btnRed}).Clicked() {
				a.stop(t.DeviceID)
			}
		})
		mg.Text(c, t.Title).FontSize(16).FontWeight(650).LineHeight(1.3).Margin(8, 0, 0, 0)
		meta := []string{}
		if t.Agent != "" {
			meta = append(meta, t.Agent)
		}
		if t.ReadOnly {
			meta = append(meta, locales.TasksReadOnly())
		}
		meta = append(meta, t.DeviceName, elapsed(taskDuration(t, c.Now())))
		mg.Text(c, strings.Join(meta, " · ")).FontSize(13).TextColor(p.ink2)
		if run {
			c.After(time.Second) // the elapsed time ticks
		}
		tech := a.settings.DeveloperMode && a.details[t.ID]
		if tech {
			techList(c, t)
		} else {
			a.stepList(c, t, 8)
		}
		if a.settings.DeveloperMode {
			mg.Row(c).Margin(12, 0, 0, 0).Padding(10, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).
				Gap(10).AlignItems(mg.Center).Children(func() {
				mg.Column(c).Grow(1).Children(func() {
					mg.Text(c, locales.TasksDetails()).FontWeight(500)
					mg.Text(c, locales.TasksDeveloperModeOn()).FontSize(12.5).TextColor(p.ink2)
				})
				on := a.details[t.ID]
				if toggle(c.Key("details-"+t.ID), &on, locales.TasksShowTechnicalDetails()).Changed() {
					a.details[t.ID] = on
				}
			})
		}
	})
}

// stepList lists a task's last steps with their marks. A step with a
// screenshot opens it under the step on a click.
func (a *app) stepList(c *mg.Context, t gateway.TaskView, last int) {
	p := pal(c)
	from := max(0, len(t.Steps)-last)
	mg.Column(c).Gap(10).Margin(14, 0, 0, 0).Role(mg.RoleList).Children(func() {
		if from > 0 {
			mg.Text(c, locales.TasksEarlierSteps(from)).FontSize(12.5).TextColor(p.ink3).Padding(0, 0, 0, 28)
		}
		for i := from; i < len(t.Steps); i++ {
			s := t.Steps[i]
			text, sub, kind := stepDisplay(t, i)
			key := fmt.Sprintf("%s/%d", t.ID, s.Index)
			shot := a.hasShot(s)
			open := shot && a.previews[key]
			mg.Column(c.Key(i)).Gap(8).Role(mg.RoleListItem).Children(func() {
				var row mg.Element
				if shot {
					row = mg.ButtonBase(c).Radius(6).Gap(10).AlignItems(mg.Start).Justify(mg.Start).
						Label(locales.TasksStepWithShot(text)).Expanded(open)
					if row.Hovered() {
						row.Background(p.bg2)
					}
					if row.Clicked() {
						a.previews[key] = !open
					}
				} else {
					row = mg.Row(c).Gap(10).AlignItems(mg.Start)
				}
				row.Children(func() {
					mg.Box(c).Size(18, 20).Shrink(0).Center().Children(func() {
						switch kind {
						case stepWait:
							dot(c, p.amberDot, false)
						case stepNow:
							dot(c, p.green, true)
						case stepYou:
							mg.Icon(c, icPerson).FontSize(16).TextColor(p.ink2)
						default:
							mg.Icon(c, icCheck).FontSize(16).TextColor(p.ink3)
						}
					})
					mg.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						tx := mg.Text(c, text).FontSize(14)
						switch kind {
						case stepWait:
							tx.TextColor(p.amber).FontWeight(600)
						case stepNow:
							tx.FontWeight(600)
						case stepYou:
							if i == len(t.Steps)-1 && running(t.State) {
								tx.FontWeight(600)
							}
						}
						if sub != "" {
							mg.Text(c, sub).FontSize(13).TextColor(p.ink2)
						}
					})
					if shot {
						mg.Icon(c, icCamera).FontSize(14).TextColor(p.ink3).Margin(3, 0, 0, 0).Shrink(0)
					}
				})
				if open {
					a.shotView(c, t, s, 300).Margin(0, 0, 4, 28)
				}
			})
		}
	})
}

// techList shows the tool calls behind the steps, in Developer mode.
func techList(c *mg.Context, t gateway.TaskView) {
	p := pal(c)
	mg.Column(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		n := 0
		for i, s := range t.Steps {
			if s.Tool == "" {
				continue
			}
			n++
			call := s.Tool
			if s.Args != "" && s.Args != "{}" {
				call += " " + s.Args
			}
			result := s.Status
			if result == "" {
				result = "running"
			}
			if s.Elapsed > 0 {
				result += fmt.Sprintf(" · %.1f"+nbsp+"s", s.Elapsed.Seconds())
			}
			if !s.Target.Empty() {
				result += fmt.Sprintf(" · target %d,%d %dx%d", s.Target.X, s.Target.Y, s.Target.W, s.Target.H)
			}
			mg.Column(c.Key(i)).Padding(0, 0, 8, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
				mg.Text(c, call).Font("monospace").FontSize(11.5).Selectable()
				mg.Text(c, result).Font("monospace").FontSize(11.5).TextColor(p.ink2).Selectable()
			})
		}
		if n == 0 {
			mg.Text(c, locales.TasksNoToolCalls()).FontSize(13).TextColor(p.ink2)
		}
	})
}

// taskRow is a collapsed task, the prototype's .trow.
func (a *app) taskRow(c *mg.Context, t gateway.TaskView, d gateway.DeviceView) {
	p := pal(c)
	row := mg.ButtonBase(c).Padding(10, 12).Radius(10).Border(1, p.line).Background(p.panel).Gap(10).AlignItems(mg.Center).Justify(mg.Start).
		Label(locales.PhoneShow(d.Info.Name))
	if row.Hovered() {
		row.Background(p.bg2)
	}
	if row.Clicked() {
		a.selectPhone(d.Info.ID)
	}
	row.Children(func() {
		statusDot(c, d)
		mg.RichText(c,
			mg.Span{Text: d.Info.Name, Weight: 600, Size: 13.5},
			mg.Span{Text: " · " + t.Title, Size: 13.5},
		).Grow(1).MinWidth(0).SingleLine()
		mg.Text(c, a.stepWord(t, d)).FontSize(12.5).TextColor(p.ink3).Shrink(0)
	})
}
