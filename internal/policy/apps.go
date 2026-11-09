package policy

import (
	_ "embed"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/DIYgod/pinky/internal/screen"
)

//go:embed apps.yaml
var bundledAppsYAML []byte

// App categories in the bundled table.
const (
	CatFinance   = "finance"
	CatPasswords = "passwords"
	CatMessaging = "messaging"
	CatSocial    = "social"
	CatStores    = "stores"
	CatBrowsers  = "browsers"
	CatPhone     = "phone"
	CatSettings  = "settings"
	// CatSystem holds launchers and system UI. They are inside every app
	// scope and allow-list, so agents can always navigate.
	CatSystem = "system"
)

// CategoryEntries lists the apps of one category.
type CategoryEntries struct {
	// IDs are package names or bundle ids. A trailing "*" matches a prefix,
	// such as "com.chase.*".
	IDs []string `yaml:"ids"`
	// Names are display names, matched without case, for backends that
	// cannot read the foreground app's id.
	Names []string `yaml:"names"`
}

// AppOverrides is the format of apps.yaml in the config directory: apps to
// add to categories, and ids or names to take out of the bundled table.
type AppOverrides struct {
	Add     map[string]CategoryEntries `yaml:"add"`
	Exclude []string                   `yaml:"exclude"`
}

// Apps maps apps to categories.
type Apps struct {
	ids      map[string][]string // exact id -> categories
	prefixes map[string][]string // id prefix -> categories
	names    map[string][]string // lower-case name -> categories
	// excluded holds the exact ids and lower-case names that apps.yaml
	// takes out of every category. They beat a prefix entry, so one app of
	// "com.cmbchina.*" can be unblocked, and a finance name (financeName).
	excluded map[string]bool
}

// BundledApps returns the table that ships with Pinky.
func BundledApps() *Apps {
	var m map[string]CategoryEntries
	if err := yaml.Unmarshal(bundledAppsYAML, &m); err != nil {
		panic("policy: bundled apps.yaml: " + err.Error())
	}
	a := &Apps{ids: map[string][]string{}, prefixes: map[string][]string{}, names: map[string][]string{},
		excluded: map[string]bool{}}
	for cat, e := range m {
		a.add(cat, e)
	}
	return a
}

func (a *Apps) add(cat string, e CategoryEntries) {
	for _, id := range e.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if p, ok := strings.CutSuffix(id, "*"); ok {
			a.prefixes[p] = appendUnique(a.prefixes[p], cat)
		} else {
			a.ids[id] = appendUnique(a.ids[id], cat)
		}
	}
	for _, n := range e.Names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			a.names[n] = appendUnique(a.names[n], cat)
		}
	}
}

// Add puts apps into a category.
func (a *Apps) Add(cat string, e CategoryEntries) { a.add(cat, e) }

// Apply adds and removes entries.
func (a *Apps) Apply(o AppOverrides) {
	for cat, e := range o.Add {
		a.add(cat, e)
	}
	for _, x := range o.Exclude {
		x = strings.TrimSpace(x)
		if p, ok := strings.CutSuffix(x, "*"); ok {
			delete(a.prefixes, p)
			continue
		}
		delete(a.ids, x)
		delete(a.names, strings.ToLower(x))
		if x != "" {
			a.excluded[x], a.excluded[strings.ToLower(x)] = true, true
		}
	}
}

// Categories returns the categories of an app, sorted. An app the table
// does not know whose name says it is a bank, a credit card, a wallet, a
// brokerage or a remittance service ("DBS digibank", "Trust Wallet",
// "某某银行", "某行信用卡") is a finance app, unless apps.yaml excludes it.
func (a *Apps) Categories(app screen.App) []string {
	var out []string
	id, name := clonePackage(strings.TrimSpace(app.ID)), strings.ToLower(strings.TrimSpace(app.Name))
	if id != "" && !a.excluded[id] {
		out = append(out, a.ids[id]...)
		for p, cats := range a.prefixes {
			if strings.HasPrefix(id, p) {
				for _, c := range cats {
					out = appendUnique(out, c)
				}
			}
		}
	}
	if name != "" && !a.excluded[name] {
		for _, c := range a.names[name] {
			out = appendUnique(out, c)
		}
	}
	if !a.excluded[id] && !a.excluded[name] && financeName(name) {
		out = appendUnique(out, CatFinance)
	}
	sort.Strings(out)
	return out
}

// financeNameParts mark a bank, a credit card, a wallet, a brokerage or a
// remittance service in a lower-case app name.
var financeNameParts = []string{
	"bank", "wallet", "remit", "securities",
	"银行", "銀行", "은행", "信用卡", "证券", "證券", "証券", "증권", "钱包", "錢包", "ウォレット", "지갑",
}

// financeName reports whether a lower-case app name is a finance app's:
// it has one of financeNameParts, or the word "pay" ("Google Pay").
func financeName(name string) bool {
	return name != "" && (containsAny(name, financeNameParts) || anyWord(tokenize(name), words("pay")))
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

var appsTemplate = []byte(`# App categories for Pinky's rules.
#
# Pinky ships a table of apps by category (finance, passwords, messaging,
# social, stores, browsers, phone, settings, system). This file adds to it
# and takes entries out; the bundled table keeps getting updates.
#
# add:
#   finance:
#     ids: [com.example.mybank]      # package name or bundle id; "com.bank.*" matches a prefix
#     names: [My Bank]               # display name, for backends without ids
#   messaging:
#     ids: [com.example.chat]
# exclude:
#   - com.paypal.android.p2pmobile   # take an id or name out of every category

add: {}
exclude: []
`)
