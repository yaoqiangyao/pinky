package policy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DIYgod/pinky/internal/screen"
)

// Exemption is an "Always allow" the person chose on an approval: in one
// app, the rule the approval named, and the rules that asked with it, no
// longer ask about the actions that rule asks about. "Always allow
// payments in Dinely" lifts Payments, and Commits with it, for payments in
// Dinely; another commit there, such as Delete, still asks.
//
// An exemption only lifts asks. It never lifts a deny (blocked apps,
// finance and password apps, credential fields), read-only mode, an
// agent's scope or a task's app list, and it does not apply while every
// action asks (PresetEvery). The rules file keeps exemptions under
// always_allow; Settings › Safety lists them.
type Exemption struct {
	// Rule is the rule the approval named, such as "Payments". The
	// exemption applies to an action only when this rule asks about it.
	Rule string `yaml:"rule"`
	// With lists the rules that asked together with Rule, such as
	// "Commits" for a payment. They no longer ask about those actions
	// either.
	With StringList `yaml:"with,flow,omitempty"`
	// App is the app's package name or bundle id, and AppName its display
	// name. An exemption with an id matches apps with that id; an app the
	// phone reports without an id, or an exemption without one, matches by
	// name.
	App     string `yaml:"app,omitempty"`
	AppName string `yaml:"app_name,omitempty"`
}

// Rules returns Rule and With.
func (x Exemption) Rules() []string { return append([]string{x.Rule}, x.With...) }

// matches reports whether the exemption is for the app.
func (x Exemption) matches(app screen.App) bool {
	id, name := clonePackage(strings.TrimSpace(app.ID)), strings.TrimSpace(app.Name)
	if x.App != "" && id != "" {
		return x.App == id
	}
	return x.AppName != "" && strings.EqualFold(x.AppName, name)
}

// sameApp reports whether two exemptions are for the same app.
func (x Exemption) sameApp(y Exemption) bool {
	if x.App != "" && y.App != "" {
		return x.App == y.App
	}
	return x.AppName != "" && strings.EqualFold(x.AppName, y.AppName)
}

// exemptable reports whether "Always allow" may lift a rule: any rule that
// asks, but not a task's app list, nor PresetEvery's catch-all, nor the
// built-in rules that deny.
func exemptable(rule string) bool {
	switch rule {
	case "", RuleTaskApps, RuleEvery, RuleCredentials, RuleReadOnly, RuleAgentScope, RuleBlocked:
		return false
	}
	return true
}

// ErrNotExemptable means an "Always allow" cannot be kept for an action:
// every action asks (PresetEvery), the app is unknown, or no rule that
// asked can be lifted, as with a task's app list.
var ErrNotExemptable = errors.New("always allow cannot be kept for this action")

// checkExemptions checks and tidies a rules file's always_allow entries.
func checkExemptions(f *File) error {
	for i := range f.AlwaysAllow {
		x := &f.AlwaysAllow[i]
		x.Rule, x.App, x.AppName = strings.TrimSpace(x.Rule), strings.TrimSpace(x.App), strings.TrimSpace(x.AppName)
		for j := range x.With {
			x.With[j] = strings.TrimSpace(x.With[j])
		}
		switch {
		case x.Rule == "":
			return fmt.Errorf("always_allow entry %d has no rule", i+1)
		case x.App == "" && x.AppName == "":
			return fmt.Errorf("always_allow %q has no app or app_name", x.Rule)
		}
		for _, r := range x.Rules() {
			if !exemptable(r) {
				return fmt.Errorf("always_allow: the rule %q cannot be always allowed", r)
			}
		}
	}
	return nil
}

// Exemptions returns the person's "Always allow" answers, oldest first.
func (e *Engine) Exemptions() []Exemption {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Exemption, len(e.file.AlwaysAllow))
	for i, x := range e.file.AlwaysAllow {
		x.With = append(StringList(nil), x.With...)
		out[i] = x
	}
	return out
}

// CanExempt reports whether Exempt would keep an "Always allow" for these
// asks (Verdict.Asks) in this app. It cannot while rules.yaml has an edit
// that cannot be used (FileError).
func (e *Engine) CanExempt(asks []string, app screen.App) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.exemptionLocked(asks, app)
	return ok && e.rulesDisk.err == nil
}

// Exempt keeps an "Always allow" for the rules that asked about an action
// (Verdict.Asks, the first naming it) in an app, and saves the rules file.
// Rules it cannot lift are left out; ErrNotExemptable means none is left,
// the app is unknown or every action asks. An exemption for the same rule
// and app takes the new rules in. The engine applies it even when saving
// fails, but not while rules.yaml has an edit that cannot be used, which
// it returns (FileError).
func (e *Engine) Exempt(asks []string, app screen.App) (Exemption, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.syncLocked(&e.rulesDisk); err != nil {
		return Exemption{}, err
	}
	x, ok := e.exemptionLocked(asks, app)
	if !ok {
		return Exemption{}, ErrNotExemptable
	}
	for i := range e.file.AlwaysAllow {
		y := &e.file.AlwaysAllow[i]
		if y.Rule != x.Rule || !y.sameApp(x) {
			continue
		}
		changed := false
		for _, r := range x.With {
			if !contains(y.With, r) {
				y.With = append(y.With, r)
				changed = true
			}
		}
		if y.App == "" && x.App != "" {
			y.App, changed = x.App, true
		}
		if y.AppName == "" && x.AppName != "" {
			y.AppName, changed = x.AppName, true
		}
		got := *y
		got.With = append(StringList(nil), y.With...)
		if !changed {
			return got, nil
		}
		return got, e.saveRulesLocked()
	}
	e.file.AlwaysAllow = append(e.file.AlwaysAllow, x)
	return x, e.saveRulesLocked()
}

// Unexempt takes back an "Always allow", as Exemptions listed it, and
// saves the rules file: its rules ask again in its app.
func (e *Engine) Unexempt(x Exemption) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.syncLocked(&e.rulesDisk); err != nil {
		return err
	}
	kept := e.file.AlwaysAllow[:0:0]
	found := false
	for _, y := range e.file.AlwaysAllow {
		if y.Rule == x.Rule && y.App == x.App && y.AppName == x.AppName {
			found = true
			continue
		}
		kept = append(kept, y)
	}
	if !found {
		return nil
	}
	e.file.AlwaysAllow = kept
	return e.saveRulesLocked()
}

// exemptionLocked is the exemption Exempt would keep.
func (e *Engine) exemptionLocked(asks []string, app screen.App) (Exemption, bool) {
	id, name := clonePackage(strings.TrimSpace(app.ID)), strings.TrimSpace(app.Name)
	if e.presetLocked() == PresetEvery || (id == "" && name == "") {
		return Exemption{}, false
	}
	var rules []string
	for _, r := range asks {
		if exemptable(r) && !contains(rules, r) {
			rules = append(rules, r)
		}
	}
	if len(rules) == 0 {
		return Exemption{}, false
	}
	x := Exemption{Rule: rules[0], App: id, AppName: name}
	if len(rules) > 1 {
		x.With = append(StringList(nil), rules[1:]...)
	}
	return x, true
}

// liftLocked takes the asks the person always allows in the app out of an
// Ask verdict, and lets the action through when no rule is left asking.
// It runs after everything that denies, read-only mode included.
func (e *Engine) liftLocked(v Verdict, app screen.App) Verdict {
	if v.Decision != Ask || len(e.file.AlwaysAllow) == 0 || e.presetLocked() == PresetEvery {
		return v
	}
	// Which exemptions apply depends on the rules that asked, not on what
	// another exemption lifted, so their order does not matter.
	lift := map[string]bool{}
	for _, x := range e.file.AlwaysAllow {
		if contains(v.Asks, x.Rule) && x.matches(app) {
			for _, r := range x.Rules() {
				lift[r] = true
			}
		}
	}
	var rest, lifted []string
	for _, r := range v.Asks {
		if lift[r] && exemptable(r) {
			lifted = append(lifted, r)
		} else {
			rest = append(rest, r)
		}
	}
	if len(lifted) == 0 {
		return v
	}
	v.Exempted = lifted
	if len(rest) == 0 {
		v.Decision, v.Rule, v.Asks, v.Reason = Allow, "", nil, ""
		return v
	}
	v.Rule, v.Asks = rest[0], rest
	v.Reason = askReason(v, app)
	return v
}
