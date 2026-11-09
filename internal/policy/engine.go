package policy

import (
	"fmt"
	"strings"
	"sync"

	"github.com/DIYgod/pinky/internal/screen"
)

// Engine evaluates actions against the rules. It is safe for concurrent use.
type Engine struct {
	mu   sync.RWMutex
	file File
	apps *Apps
	// overrides is apps.yaml; extra holds apps added at run time.
	overrides AppOverrides
	extra     map[string]CategoryEntries
	// Runtime read-only switches from the client, on top of the file's.
	readOnlyAll     *bool
	readOnlyDevices map[string]bool

	// rules.yaml and apps.yaml as the engine last read or wrote them, what
	// Refresh has to report, and the files' version (disk.go).
	rulesDisk, appsDisk diskFile
	changes             []FileChange
	version             int
}

// New returns an engine for the given rules and app table. A nil table uses
// the bundled one.
func New(f File, apps *Apps) *Engine {
	if apps == nil {
		apps = BundledApps()
	}
	return &Engine{file: f, apps: apps, readOnlyDevices: map[string]bool{}, extra: map[string]CategoryEntries{}}
}

// Default returns an engine with the bundled rules and app table.
func Default() *Engine { return New(DefaultFile(), nil) }

// Rules returns every rule in evaluation order: the preset's, the rules
// file's, then the built-in ones.
func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rulesLocked()
}

// AddApps puts apps into a category at run time, for tests and fake devices.
func (e *Engine) AddApps(cat string, entries CategoryEntries) {
	e.mu.Lock()
	defer e.mu.Unlock()
	x := e.extra[cat]
	x.IDs = append(x.IDs, entries.IDs...)
	x.Names = append(x.Names, entries.Names...)
	e.extra[cat] = x
	e.apps.Add(cat, entries)
}

// Categories returns the categories of an app.
func (e *Engine) Categories(app screen.App) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.apps.Categories(app)
}

// IsSystem reports whether the app is a launcher or system UI, which every
// scope and allow-list includes.
func (e *Engine) IsSystem(app screen.App) bool {
	return contains(e.Categories(app), CatSystem)
}

// SetReadOnly turns read-only mode on or off. With an empty deviceID it
// applies to every device and is saved in the rules file (Settings); for
// one device it lasts until the gateway restarts (the device card).
func (e *Engine) SetReadOnly(deviceID string, on bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if deviceID == "" {
		if err := e.syncLocked(&e.rulesDisk); err != nil {
			return err
		}
		e.readOnlyAll = nil
		if e.file.ReadOnly == on {
			return nil
		}
		e.file.ReadOnly = on
		return e.saveRulesLocked()
	}
	e.readOnlyDevices[deviceID] = on
	return nil
}

// ForgetDevice drops what the rules keep about one phone, which the person
// removed from Pinky: its read-only switch, and its entry in the rules
// file's read_only_devices, which is then saved. Read-only mode for every
// device stays as it is.
func (e *Engine) ForgetDevice(deviceID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.readOnlyDevices, deviceID)
	if deviceID == "" || !contains(e.file.ReadOnlyDevices, deviceID) {
		return nil
	}
	if err := e.syncLocked(&e.rulesDisk); err != nil {
		return err
	}
	if !contains(e.file.ReadOnlyDevices, deviceID) {
		return nil // a hand edit took it out meanwhile
	}
	var keep []string
	for _, id := range e.file.ReadOnlyDevices {
		if id != deviceID {
			keep = append(keep, id)
		}
	}
	e.file.ReadOnlyDevices = keep
	return e.saveRulesLocked()
}

// ReadOnly reports whether read-only mode applies to the device.
func (e *Engine) ReadOnly(deviceID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.readOnlyLocked(deviceID)
}

func (e *Engine) readOnlyLocked(deviceID string) bool {
	all := e.file.ReadOnly
	if e.readOnlyAll != nil {
		all = *e.readOnlyAll
	}
	if all {
		return true
	}
	if on, ok := e.readOnlyDevices[deviceID]; ok {
		return on
	}
	return contains(e.file.ReadOnlyDevices, deviceID)
}

// Blocked reports whether a deny rule blocks the whole app, and names the
// rule. The agent gets neither the screen nor the notifications of a blocked
// app, and its steps are not recorded.
func (e *Engine) Blocked(app screen.App) (rule string, blocked bool) {
	if app.ID == "" && app.Name == "" {
		return "", false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.blockedLocked(app)
}

// Evaluate decides one action.
func (e *Engine) Evaluate(in Input) Verdict {
	e.mu.RLock()
	defer e.mu.RUnlock()

	app := in.App
	if in.Op == OpLaunch {
		app = in.Launch
	}
	cats := e.apps.Categories(app)
	kinds := detect(in, cats)
	system := contains(cats, CatSystem)

	if in.Op == OpType && IsCredentialField(in.Target) {
		return Verdict{Decision: Deny, Rule: RuleCredentials, Kinds: kinds,
			Reason: "Pinky never types into password or one-time-code fields; call phone_handoff so the person can fill it."}
	}
	if len(in.ScopeApps) > 0 && !system && !appIn(app, in.ScopeApps) && !(in.Op == OpKey && in.Key == "home") {
		return Verdict{Decision: Deny, Rule: RuleAgentScope, Kinds: kinds, Scope: true,
			Reason: fmt.Sprintf("%s is outside this agent's scope.", appName(app))}
	}
	taskAsk := false
	if len(in.TaskApps) > 0 && !system && !appIn(app, in.TaskApps) {
		switch in.Op {
		case OpLaunch:
			return Verdict{Decision: Deny, Rule: RuleTaskApps, Kinds: kinds, Scope: true,
				Reason: fmt.Sprintf("%s is not in this task's app list (%s).", appName(app), strings.Join(in.TaskApps, ", "))}
		case OpType:
			taskAsk = true
		}
	}

	v := Verdict{Decision: Allow, Kinds: kinds}
	var asks []string
	lift := in.AllowBlocked && !contains(cats, CatPasswords)
	for _, r := range e.rulesLocked() {
		if !r.matches(app, cats, kinds, in) {
			continue
		}
		if lift && r.Decision == Deny && r.Match.appLevel() {
			continue // the person allowed this blocked app for the task
		}
		if r.Decision == Ask {
			asks = append(asks, r.Name)
		}
		if r.Decision > v.Decision || (v.Rule == "" && r.Decision == v.Decision && r.Decision != Allow) {
			v.Decision, v.Rule = r.Decision, r.Name
		}
	}
	if v.Rule != "" {
		v.Reason = reason(v, app)
	}
	if taskAsk {
		asks = append(asks, RuleTaskApps)
		if v.Decision < Ask {
			v.Decision, v.Rule = Ask, RuleTaskApps
			v.Reason = askReason(v, app)
		}
	}
	if v.Decision == Ask {
		v.Asks = asks
	}

	if in.ReadOnly || e.readOnlyLocked(in.Device) {
		switch {
		case v.Decision == Ask && onlyEvery(v.Asks):
			// Read-only still lets the agent look around; with "every
			// action asks" those steps keep asking.
		case v.Decision == Ask:
			v.Decision = Deny
			v.Reason = fmt.Sprintf("Read-only mode is on, and this would need approval (%s).", v.Rule)
			v.Rule = RuleReadOnly
		case v.Decision == Allow && in.Op == OpType && !IsSearchField(in.Target):
			v.Decision, v.Rule = Deny, RuleReadOnly
			v.Reason = "Read-only mode is on: the agent may type only into search fields."
		}
	}
	// The person's "Always allow" answers come last: they lift asks, never
	// a deny or read-only mode.
	return e.liftLocked(v, app)
}

// askReason explains an Ask verdict.
func askReason(v Verdict, app screen.App) string {
	if v.Rule == RuleTaskApps {
		return fmt.Sprintf("Typing into %s, which is not in this task's app list.", appName(app))
	}
	return reason(v, app)
}

// onlyEvery reports whether PresetEvery's catch-all is the only rule that
// asked.
func onlyEvery(asks []string) bool {
	return len(asks) == 1 && asks[0] == RuleEvery
}

func reason(v Verdict, app screen.App) string {
	switch {
	case v.Decision == Deny && len(v.Kinds) == 0:
		return fmt.Sprintf("%s is blocked by the rule %q.", appName(app), v.Rule)
	case len(v.Kinds) > 0:
		ks := make([]string, len(v.Kinds))
		for i, k := range v.Kinds {
			ks[i] = string(k)
		}
		return fmt.Sprintf("The rule %q matched this action (%s).", v.Rule, strings.Join(ks, ", "))
	}
	return fmt.Sprintf("The rule %q matched this action.", v.Rule)
}

func appName(app screen.App) string {
	switch {
	case app.Name != "":
		return app.Name
	case app.ID != "":
		return app.ID
	}
	return "This app"
}

func (r Rule) matchesApp(app screen.App, cats []string) bool {
	m := r.Match
	if len(m.App) > 0 && !appIn(app, m.App) {
		return false
	}
	if len(m.Category) > 0 && !anyIn(cats, m.Category) {
		return false
	}
	return true
}

func (r Rule) matches(app screen.App, cats []string, kinds []Kind, in Input) bool {
	m := r.Match
	if !r.matchesApp(app, cats) {
		return false
	}
	// Whole-app rules never stop the agent from pressing Home to leave.
	if m.appLevel() && in.Op == OpKey && in.Key == "home" {
		return false
	}
	if len(m.Action) > 0 {
		ok := false
		for _, a := range m.Action {
			if Op(a) == in.Op || hasKind(kinds, Kind(a)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(m.Label) > 0 {
		if in.Target == nil {
			return false
		}
		l := strings.ToLower(in.Target.Label)
		ok := false
		for _, s := range m.Label {
			if s != "" && strings.Contains(l, strings.ToLower(s)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// appIn reports whether the app matches any entry, by id or by name.
func appIn(app screen.App, list []string) bool {
	for _, x := range list {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if app.ID != "" && (x == app.ID || (strings.HasSuffix(x, "*") && strings.HasPrefix(app.ID, strings.TrimSuffix(x, "*")))) {
			return true
		}
		if app.Name != "" && strings.EqualFold(x, app.Name) {
			return true
		}
	}
	return false
}

func anyIn(have, want []string) bool {
	for _, w := range want {
		if contains(have, w) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// FilterEvents removes notifications from blocked apps and masks one-time
// codes in the rest, and in toasts and dialogs, as the rules file says.
func (e *Engine) FilterEvents(events []screen.Event) []screen.Event {
	return e.FilterEventsNamed(events, nil)
}

// FilterEventsNamed is FilterEvents with the display names of the apps
// that posted the notifications: name returns an app id's name, "" when it
// is not known, and may be nil. An app blocked by its name in Settings ›
// Safety, or by a name that says it is a bank, is matched by it as well as
// by its id.
func (e *Engine) FilterEventsNamed(events []screen.Event, name func(id string) string) []screen.Event {
	if len(events) == 0 {
		return events
	}
	e.mu.RLock()
	mask := e.file.MaskCodes == nil || *e.file.MaskCodes
	e.mu.RUnlock()
	out := make([]screen.Event, 0, len(events))
	for _, ev := range events {
		if ev.Kind == screen.EventNotification && e.NotificationBlocked(ev, name) {
			continue
		}
		if mask {
			ev.Text = MaskCodes(ev.Text)
		}
		out = append(out, ev)
	}
	return out
}

// NotificationBlocked reports whether a notification event comes from a
// blocked app: by its app's id and name (name may be nil, see
// FilterEventsNamed), or, when the driver could not tell the app, by the
// name that starts its text the way an iPhone's banners do ("Chase, Payment
// received").
func (e *Engine) NotificationBlocked(ev screen.Event, name func(id string) string) bool {
	if ev.Kind != screen.EventNotification {
		return false
	}
	if ev.App == "" {
		n, _, ok := strings.Cut(ev.Text, ", ")
		if !ok {
			return false
		}
		_, blocked := e.Blocked(screen.App{Name: strings.TrimSpace(n)})
		return blocked
	}
	// A driver that knows no id passes the app's name.
	app := screen.App{ID: ev.App, Name: ev.App}
	if name != nil {
		if n := strings.TrimSpace(name(ev.App)); n != "" {
			app.Name = n
		}
	}
	_, blocked := e.Blocked(app)
	return blocked
}
