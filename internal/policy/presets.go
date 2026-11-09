package policy

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/DIYgod/pinky/internal/screen"
)

// Preset is a ready-made answer to "When should agents ask you?" in
// Settings. It adds Ask rules on top of the rules file.
type Preset int

// Presets.
const (
	// PresetNone leaves the rules file alone.
	PresetNone Preset = 0
	// PresetCommits asks before payments, messages, deletions and other
	// commits, calls, installs, links that leave the app and settings
	// changes. The default before 2026-11-07's change to PresetPayments.
	PresetCommits Preset = 1
	// PresetPayments asks before payments only. The default and
	// recommended preset.
	PresetPayments Preset = 2
	// PresetEvery asks before every action.
	PresetEvery Preset = 3
)

// CatBlocked holds the apps the person blocks in Settings; RuleBlocked
// denies them.
const (
	CatBlocked  = "blocked"
	RuleBlocked = "Blocked by you"
	// RuleEvery is the catch-all of PresetEvery.
	RuleEvery = "Every action"
)

func ask(name string, actions ...string) Rule {
	return Rule{Name: name, Match: Match{Action: actions}, Decision: Ask}
}

// commitRules are PresetCommits' rules, specific ones first so they name
// the result.
var commitRules = []Rule{
	ask("Payments", string(Payment)),
	ask("Messages", string(SendMessage)),
	ask("Commits", string(Commit)),
	ask("Installs and links", string(InstallApp), string(OpenURL)),
	ask("Calls", string(Call)),
	ask("Settings changes", string(ChangeSetting)),
}

// presetRules returns the rules a preset adds.
func presetRules(p Preset) []Rule {
	switch p {
	case PresetCommits:
		return commitRules
	case PresetPayments:
		return commitRules[:1]
	case PresetEvery:
		out := append([]Rule(nil), commitRules...)
		return append(out, ask(RuleEvery, string(OpTap), string(OpType), string(OpSwipe), string(OpKey), string(OpLaunch)))
	}
	return nil
}

// builtinRules are always on: apps the person blocked.
var builtinRules = []Rule{
	{Name: RuleBlocked, Match: Match{Category: StringList{CatBlocked}}, Decision: Deny},
}

// rulesLocked returns every rule in evaluation order: the preset's, then
// the file's, then the built-in ones.
func (e *Engine) rulesLocked() []Rule {
	out := append([]Rule(nil), presetRules(e.presetLocked())...)
	out = append(out, e.file.Rules...)
	return append(out, builtinRules...)
}

// migrate turns a rules file from before presets (no preset key, the old
// default Ask rules spelled out) into preset 1 without those rules.
func migrate(f *File) {
	if f.Preset != nil {
		return
	}
	old := map[string]bool{}
	for _, r := range commitRules {
		old[r.Name] = true
	}
	kept := f.Rules[:0:0]
	for _, r := range f.Rules {
		if old[r.Name] && r.Decision == Ask {
			continue
		}
		kept = append(kept, r)
	}
	f.Rules = kept
	one := int(PresetCommits)
	f.Preset = &one
}

// Preset returns the current preset.
func (e *Engine) Preset() Preset {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.presetLocked()
}

func (e *Engine) presetLocked() Preset {
	if e.file.Preset == nil {
		return PresetNone
	}
	return Preset(*e.file.Preset)
}

// SetPreset changes the preset and saves the rules file.
func (e *Engine) SetPreset(p Preset) error {
	if p < PresetNone || p > PresetEvery {
		return fmt.Errorf("unknown preset %d", p)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.syncLocked(&e.rulesDisk); err != nil {
		return err
	}
	if e.file.Preset != nil && Preset(*e.file.Preset) == p {
		return nil
	}
	v := int(p)
	e.file.Preset = &v
	return e.saveRulesLocked()
}

// HideCodes reports whether one-time codes are masked, on the screen and in
// events (codes.go).
func (e *Engine) HideCodes() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.file.MaskCodes == nil || *e.file.MaskCodes
}

// SetHideCodes turns masking of one-time codes on or off and saves the
// rules file.
func (e *Engine) SetHideCodes(on bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.syncLocked(&e.rulesDisk); err != nil {
		return err
	}
	if e.file.MaskCodes != nil && *e.file.MaskCodes == on {
		return nil
	}
	e.file.MaskCodes = &on
	return e.saveRulesLocked()
}

// rulesHeader starts a saved rules file.
const rulesHeader = `# Pinky policy rules. Settings writes this file; you may edit it too.
# preset: 1 = payments, messages, deletions and other commits ask,
# 2 = only payments ask (the default, recommended), 3 = every action asks,
# 0 = only the rules below.
# When several rules match, the strictest wins: deny, then ask, then allow.
# always_allow holds your "Always allow" answers to approvals: in that app,
# those rules no longer ask (never a deny or read-only mode; not with preset
# 3). Settings > Safety lists them.
# The bundled defaults, with every field explained, are in Pinky's source:
# internal/policy/defaults.yaml.

`

func (e *Engine) saveRulesLocked() error {
	if e.rulesDisk.path == "" {
		return nil
	}
	data, err := yaml.Marshal(e.file)
	if err != nil {
		return err
	}
	return e.writeLocked(&e.rulesDisk, append([]byte(rulesHeader), data...))
}

// BlockedApp is an app the rules block: the agent cannot open it, see its
// screen or its notifications.
type BlockedApp struct {
	ID   string // package name or bundle id; empty for an entry by name only
	Name string // display name, when known
	// Category is the app table category that blocks it, such as "finance"
	// or "passwords"; CatBlocked when the person blocked it.
	Category string
	// Rule is the deny rule that blocks it.
	Rule string
	// ByPerson is true for apps the person blocked in Settings.
	ByPerson bool
}

// BlockedApps lists the apps the person blocked, and those of installed
// (the apps on the phones, may be nil) that a rule blocks, sorted by name.
func (e *Engine) BlockedApps(installed []screen.App) []BlockedApp {
	e.mu.RLock()
	person := e.overrides.Add[CatBlocked]
	e.mu.RUnlock()
	seen := map[string]bool{}
	var out []BlockedApp
	add := func(b BlockedApp) {
		k := b.ID + "\x00" + strings.ToLower(b.Name)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, b)
	}
	for _, a := range installed {
		if rule, blocked := e.Blocked(a); blocked {
			b := BlockedApp{ID: a.ID, Name: a.Name, Rule: rule, Category: e.blockingCategory(a, rule)}
			b.ByPerson = b.Category == CatBlocked
			add(b)
		}
	}
	for _, id := range person.IDs {
		if !seen[id+"\x00"] && !seenID(seen, id) {
			add(BlockedApp{ID: id, Category: CatBlocked, Rule: RuleBlocked, ByPerson: true})
		}
	}
	for _, n := range person.Names {
		if !seenName(seen, n) {
			add(BlockedApp{Name: n, Category: CatBlocked, Rule: RuleBlocked, ByPerson: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name+out[i].ID) < strings.ToLower(out[j].Name+out[j].ID)
	})
	return out
}

func seenID(seen map[string]bool, id string) bool {
	for k := range seen {
		if strings.HasPrefix(k, id+"\x00") {
			return true
		}
	}
	return false
}

func seenName(seen map[string]bool, name string) bool {
	suffix := "\x00" + strings.ToLower(name)
	for k := range seen {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// blockingCategory names the category of the rule that blocks an app.
func (e *Engine) blockingCategory(a screen.App, rule string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cats := e.apps.Categories(a)
	for _, r := range e.rulesLocked() {
		if r.Name != rule {
			continue
		}
		for _, c := range r.Match.Category {
			if contains(cats, c) {
				return c
			}
		}
	}
	return ""
}

// CategoryLabel names a blocking category for Settings: "Finance",
// "Passwords", "Blocked by you".
func CategoryLabel(cat string) string {
	switch cat {
	case CatFinance:
		return "Finance"
	case CatPasswords:
		return "Passwords"
	case CatBlocked, "":
		return "Blocked by you"
	}
	r := []rune(cat)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// ErrBlockedByRule means an app stays blocked by a rule in the rules file
// that names it, which Settings cannot change.
var ErrBlockedByRule = errors.New("blocked by a rule in rules.yaml that names the app")

// SetAppBlocked blocks or unblocks an app for agents and saves apps.yaml.
// app needs an ID, a Name or both. Unblocking an app that the bundled table
// blocks (a banking app) adds it to apps.yaml's exclude list.
func (e *Engine) SetAppBlocked(app screen.App, blocked bool) error {
	app.ID, app.Name = strings.TrimSpace(app.ID), strings.TrimSpace(app.Name)
	if app.ID == "" && app.Name == "" {
		return errors.New("an app id or name is needed")
	}
	e.mu.Lock()
	if err := e.syncLocked(&e.appsDisk); err != nil {
		e.mu.Unlock()
		return err
	}
	o := e.overrides
	if o.Add == nil {
		o.Add = map[string]CategoryEntries{}
	}
	entries := o.Add[CatBlocked]
	entries.IDs = without(entries.IDs, app.ID)
	entries.Names = without(entries.Names, app.Name)
	o.Exclude = without(without(o.Exclude, app.ID), app.Name)
	if blocked {
		if app.ID != "" {
			entries.IDs = append(entries.IDs, app.ID)
		} else {
			entries.Names = append(entries.Names, app.Name)
		}
	}
	o.Add[CatBlocked] = entries
	e.setOverridesLocked(o)
	if !blocked {
		// Still blocked by the bundled table: exclude it there.
		if rule, ok := e.blockedLocked(app); ok && rule != RuleBlocked {
			// The table may list it by id and by name; exclude both.
			for _, key := range []string{app.ID, app.Name} {
				if key != "" {
					o.Exclude = append(o.Exclude, key)
				}
			}
			e.setOverridesLocked(o)
		}
	}
	rule, still := e.blockedLocked(app)
	err := e.saveAppsLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if !blocked && still {
		return fmt.Errorf("%w (%s)", ErrBlockedByRule, rule)
	}
	return nil
}

func without(list []string, s string) []string {
	if s == "" {
		return list
	}
	out := list[:0:0]
	for _, x := range list {
		if !strings.EqualFold(x, s) {
			out = append(out, x)
		}
	}
	return out
}

// setOverridesLocked rebuilds the app table from the bundled one and o.
func (e *Engine) setOverridesLocked(o AppOverrides) {
	apps := BundledApps()
	for cat, x := range e.extra {
		apps.Add(cat, x)
	}
	apps.Apply(o)
	e.apps, e.overrides = apps, o
}

// blockedLocked is Blocked with e.mu held.
func (e *Engine) blockedLocked(app screen.App) (string, bool) {
	cats := e.apps.Categories(app)
	for _, r := range e.rulesLocked() {
		if r.Decision == Deny && r.Match.appLevel() && r.matchesApp(app, cats) {
			return r.Name, true
		}
	}
	return "", false
}

const appsHeader = `# App categories for Pinky's rules. Settings writes this file; you may edit it.
#
# Pinky ships a table of apps by category (finance, passwords, messaging,
# social, stores, browsers, phone, settings, system). "add" puts apps into
# categories ("blocked" holds the apps you block in Settings); "exclude"
# takes ids or names out of every category, such as a banking app you
# allowed. Ids are package names or bundle ids; "com.bank.*" matches a prefix.

`

func (e *Engine) saveAppsLocked() error {
	if e.appsDisk.path == "" {
		return nil
	}
	o := e.overrides
	if o.Add == nil {
		o.Add = map[string]CategoryEntries{}
	}
	if o.Exclude == nil {
		o.Exclude = []string{}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(o); err != nil {
		return err
	}
	return e.writeLocked(&e.appsDisk, append([]byte(appsHeader), buf.Bytes()...))
}
