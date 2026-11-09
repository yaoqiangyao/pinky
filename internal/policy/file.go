package policy

import (
	_ "embed"
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:embed defaults.yaml
var defaultRulesYAML []byte

// File is the rules file, rules.yaml in the config directory.
type File struct {
	// Preset adds a ready-made set of Ask rules (see Preset). Nil in files
	// from before presets, which Load migrates.
	Preset *int `yaml:"preset,omitempty"`
	// ReadOnly turns on read-only mode for every device.
	ReadOnly bool `yaml:"read_only"`
	// ReadOnlyDevices turns on read-only mode for these device ids.
	ReadOnlyDevices []string `yaml:"read_only_devices"`
	// MaskCodes masks one-time codes on the screen and in events, and
	// covers them in screenshots (codes.go). Default true.
	MaskCodes *bool  `yaml:"mask_codes"`
	Rules     []Rule `yaml:"rules"`
	// AlwaysAllow holds the person's "Always allow" answers to approvals
	// (see Exemption), oldest first.
	AlwaysAllow []Exemption `yaml:"always_allow,omitempty"`
}

// DefaultFile returns the bundled default rules.
func DefaultFile() File {
	f, err := ParseFile(defaultRulesYAML)
	if err != nil {
		panic("policy: bundled defaults.yaml: " + err.Error())
	}
	return f
}

// DefaultRulesYAML returns the bundled rules file, with its comments.
func DefaultRulesYAML() []byte { return append([]byte(nil), defaultRulesYAML...) }

// ParseFile parses a rules file and checks it.
func ParseFile(data []byte) (File, error) {
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return File{}, err
	}
	for i, r := range f.Rules {
		if err := checkRule(i, r); err != nil {
			return File{}, err
		}
	}
	if err := checkExemptions(&f); err != nil {
		return File{}, err
	}
	return f, nil
}

// checkRule checks the i-th rule of a rules file.
func checkRule(i int, r Rule) error {
	if r.Name == "" {
		return fmt.Errorf("rule %d has no name", i+1)
	}
	m := r.Match
	if len(m.App) == 0 && len(m.Category) == 0 && len(m.Action) == 0 && len(m.Label) == 0 {
		return fmt.Errorf("rule %q matches everything; give it an app, category, action or label", r.Name)
	}
	for _, a := range m.Action {
		if !knownAction(a) {
			return fmt.Errorf("rule %q: unknown action %q", r.Name, a)
		}
	}
	if r.Decision < Allow || r.Decision > Deny {
		return fmt.Errorf("rule %q: unknown decision %d", r.Name, int(r.Decision))
	}
	return nil
}

func knownAction(a string) bool {
	switch Kind(a) {
	case Commit, Payment, SendMessage, InstallApp, OpenURL, Call, ChangeSetting:
		return true
	}
	switch Op(a) {
	case OpTap, OpType, OpSwipe, OpKey, OpLaunch:
		return true
	}
	return false
}

// Load reads the rules file and the app table overrides from the given
// paths. A missing rules file is created with the bundled defaults, and a
// missing app file with a commented template, so the person can edit both.
// Empty paths use the bundled defaults without touching the disk. The
// engine takes in later edits of the files (see Refresh).
func Load(rulesPath, appsPath string) (*Engine, error) {
	e := New(DefaultFile(), nil)
	e.rulesDisk.path, e.appsDisk.path = rulesPath, appsPath
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshLocked(true)
	e.changes, e.version = nil, 0
	for _, d := range []*diskFile{&e.rulesDisk, &e.appsDisk} {
		if d.err != nil {
			return nil, fmt.Errorf("%s: %w", d.path, d.err)
		}
	}
	return e, nil
}

// migrateLegacy gives a rules file from before presets a preset. A file
// that spells out all six old default Ask rules becomes preset 1 without
// them; any other file keeps its rules as they are, with no preset.
func migrateLegacy(f *File) {
	if f.Preset != nil {
		return
	}
	have := 0
	for _, want := range commitRules {
		for _, r := range f.Rules {
			if r.Name == want.Name && r.Decision == Ask {
				have++
				break
			}
		}
	}
	if have == len(commitRules) {
		migrate(f)
		return
	}
	none := int(PresetNone)
	f.Preset = &none
}

// Reload reads the files again, as Load does, keeping runtime read-only
// switches and apps added with AddApps. A file that cannot be used leaves
// the rules in force as they were, and is returned as a *FileError.
func (e *Engine) Reload() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshLocked(true)
	return e.problemLocked()
}

// UnmarshalYAML accepts "allow", "ask" or "deny".
func (d *Decision) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := ParseDecision(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalYAML writes the decision as a word.
func (d Decision) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalYAML accepts a scalar or a sequence.
func (l *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*l = StringList{s}
		return nil
	case yaml.SequenceNode:
		var ss []string
		if err := n.Decode(&ss); err != nil {
			return err
		}
		*l = ss
		return nil
	}
	return fmt.Errorf("line %d: want a string or a list of strings", n.Line)
}
