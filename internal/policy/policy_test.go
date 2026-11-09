package policy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// private reports whether a file is the user's alone. Windows has no Unix
// modes (Go reports 0666 for any writable file): access there follows the
// folder's access control list, which is the user's alone in their
// profile.
func private(st os.FileInfo) bool {
	return runtime.GOOS == "windows" || st.Mode().Perm() == 0o600
}

var (
	dinely   = screen.App{Name: "Dinely", ID: "com.dinely.app"}
	whatsapp = screen.App{Name: "WhatsApp", ID: "com.whatsapp"}
	chase    = screen.App{Name: "Chase", ID: "com.chase.sig.android"}
	play     = screen.App{Name: "Play Store", ID: "com.android.vending"}
	settings = screen.App{Name: "Settings", ID: "com.android.settings"}
	launcher = screen.App{Name: "Pixel Launcher", ID: "com.google.android.apps.nexuslauncher"}
)

func button(label string) *screen.Element {
	return &screen.Element{Ref: 1, Role: screen.RoleButton, Label: label, Bounds: screen.Rect{X: 0, Y: 0, W: 100, H: 50}}
}

func tap(app screen.App, label string) Input {
	return Input{Op: OpTap, App: app, Target: button(label), Screen: &screen.Screen{App: app}}
}

// commitsEngine is the bundled policy with "Ask before payments, messages
// and deletions" (preset 1), whose rules ask about every kind detect finds,
// so the tests of detection read its verdicts. The default, preset 2, asks
// before payments only.
func commitsEngine(t testing.TB) *Engine {
	t.Helper()
	e := Default()
	if err := e.SetPreset(PresetCommits); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCommitDetection(t *testing.T) {
	commits := []string{
		"Send", "Pay $12.80", "Place order · $12.80", "Checkout", "Buy now", "Book a table", "Confirm",
		"Submit", "Delete", "Transfer", "Post", "Slide to pay", "Sign out",
		"发送", "立即支付 ¥12.80", "提交订单", "去结算(3)", "确认支付", "同意并支付", "删除",
		"傳送", "結帳", "注文を確定する", "購入する", "送信", "결제하기", "보내기", "주문하기",
		"Enviar", "Comprar ahora", "Passer la commande", "Payer", "Jetzt kaufen", "Zahlungspflichtig bestellen",
		"Finalizar compra", "Acquista ora", "Оформить заказ", "Отправить", "Yes, delete", "OK, send",
	}
	for _, l := range commits {
		if !isCommitLabel(l) {
			t.Errorf("%q should be a commit", l)
		}
	}
	notCommits := []string{
		"Orders", "Order history", "Order details", "Payment methods", "Sign in", "Search", "Back",
		"Settings", "Cart · 1", "Order by date", "订单详情", "我的订单", "支付方式", "注文履歴", "微信",
		"Send feedback about this long paragraph that goes on and on", "Share", "",
	}
	for _, l := range notCommits {
		if isCommitLabel(l) {
			t.Errorf("%q should not be a commit", l)
		}
	}
}

// TestDefaultRules checks the bundled rules under preset 1, where every
// kind asks, and under the default, preset 2, where only payments do; the
// kinds are detected all the same.
func TestDefaultRules(t *testing.T) {
	if p := Default().Preset(); p != PresetPayments {
		t.Fatalf("the default preset is %d, want %d (payments only)", p, PresetPayments)
	}
	cases := []struct {
		name string
		in   Input
		want Decision
		rule string
		kind Kind
	}{
		{"payment", tap(dinely, "Place order · $12.80"), Ask, "Payments", Payment},
		{"commit", tap(dinely, "Delete"), Ask, "Commits", Commit},
		{"navigation", tap(dinely, "Orders"), Allow, "", ""},
		{"finance app", tap(chase, "Accounts"), Deny, "Finance apps", ""},
		{"home in finance app", Input{Op: OpKey, Key: "home", App: chase}, Allow, "", ""},
		{"launch finance app", Input{Op: OpLaunch, Launch: chase}, Deny, "Finance apps", ""},
		{"install", tap(play, "Install"), Ask, "Installs and links", InstallApp},
		{"paid install", tap(play, "$4.99"), Ask, "Payments", Payment},
		{"call", tap(dinely, "Call restaurant"), Ask, "Calls", Call},
		{"settings toggle", Input{Op: OpTap, App: settings, Target: &screen.Element{Role: screen.RoleSwitch, Label: "Wi-Fi"}}, Ask, "Settings changes", ChangeSetting},
		{"settings navigation", tap(settings, "Network & internet"), Allow, "", ""},
		{"link", Input{Op: OpTap, App: dinely, Target: &screen.Element{Role: screen.RoleLink, Label: "https://example.com/offer"}}, Ask, "Installs and links", OpenURL},
		{"send message", Input{Op: OpType, App: whatsapp, Text: "hi", Submit: true, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Ask, "Messages", SendMessage},
		{"enter in chat", Input{Op: OpKey, Key: "enter", App: whatsapp, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Ask, "Messages", SendMessage},
		{"type in chat", Input{Op: OpType, App: whatsapp, Text: "hi", Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Allow, "", ""},
		{"newline in chat", Input{Op: OpType, App: whatsapp, Text: "hi\n", Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Ask, "Messages", SendMessage},
		{"search submit in chat", Input{Op: OpType, App: whatsapp, Text: "sam", Submit: true, Target: &screen.Element{Role: screen.RoleInput, Label: "Search"}}, Allow, "", ""},
		{"password", Input{Op: OpType, App: dinely, Text: "x", Target: &screen.Element{Role: screen.RoleInput, Label: "", States: []string{screen.StatePassword}}}, Deny, RuleCredentials, ""},
		{"otp", Input{Op: OpType, App: dinely, Text: "1", Target: &screen.Element{Role: screen.RoleInput, Label: "Verification code"}}, Deny, RuleCredentials, ""},
		{"otp zh", Input{Op: OpType, App: dinely, Text: "1", Target: &screen.Element{Role: screen.RoleInput, Label: "请输入验证码"}}, Deny, RuleCredentials, ""},
		{"promo code", Input{Op: OpType, App: dinely, Text: "SAVE10", Target: &screen.Element{Role: screen.RoleInput, Label: "Promo code"}}, Allow, "", ""},
		{"dialog ok", Input{Op: OpTap, App: dinely, Target: button("OK"), Screen: &screen.Screen{Elements: []screen.Element{{Role: screen.RoleText, Label: "Delete this message?"}, *button("OK")}}}, Ask, "Commits", Commit},
		{"plain ok", Input{Op: OpTap, App: dinely, Target: button("OK"), Screen: &screen.Screen{Elements: []screen.Element{{Role: screen.RoleText, Label: "Saved"}}}}, Allow, "", ""},
	}
	for _, e := range []*Engine{commitsEngine(t), Default()} {
		for _, c := range cases {
			want, rule := c.want, c.rule
			if e.Preset() == PresetPayments && want == Ask && rule != "Payments" {
				want, rule = Allow, ""
			}
			v := e.Evaluate(c.in)
			if v.Decision != want || v.Rule != rule {
				t.Errorf("preset %d, %s: got %v %q (kinds %v), want %v %q", e.Preset(), c.name, v.Decision, v.Rule, v.Kinds, want, rule)
			}
			if c.kind != "" && !v.Has(c.kind) {
				t.Errorf("preset %d, %s: kinds %v miss %s", e.Preset(), c.name, v.Kinds, c.kind)
			}
		}
	}
}

func TestBlockedApps(t *testing.T) {
	e := Default()
	if rule, ok := e.Blocked(chase); !ok || rule != "Finance apps" {
		t.Fatalf("Chase not blocked: %q %v", rule, ok)
	}
	if _, ok := e.Blocked(screen.App{Name: "1Password"}); !ok {
		t.Fatal("password manager by name not blocked")
	}
	if _, ok := e.Blocked(screen.App{ID: "com.hsbc.hsbcnet"}); !ok {
		t.Fatal("prefix entry com.hsbc.* not matched")
	}
	if _, ok := e.Blocked(dinely); ok {
		t.Fatal("Dinely blocked")
	}
}

func TestReadOnly(t *testing.T) {
	e := Default()
	e.SetReadOnly("dev1", true)
	v := e.Evaluate(Input{Op: OpTap, Device: "dev1", App: dinely, Target: button("Pay $3")})
	if v.Decision != Deny || v.Rule != RuleReadOnly {
		t.Fatalf("read-only commit: %v %q", v.Decision, v.Rule)
	}
	v = e.Evaluate(Input{Op: OpType, Device: "dev1", App: dinely, Text: "x", Target: &screen.Element{Role: screen.RoleInput, Label: "Note"}})
	if v.Decision != Deny || v.Rule != RuleReadOnly {
		t.Fatalf("read-only typing: %v %q", v.Decision, v.Rule)
	}
	v = e.Evaluate(Input{Op: OpType, Device: "dev1", App: dinely, Text: "rice", Target: &screen.Element{Role: screen.RoleInput, Label: "Search dishes"}})
	if v.Decision != Allow {
		t.Fatalf("read-only search typing: %v %q", v.Decision, v.Rule)
	}
	v = e.Evaluate(Input{Op: OpTap, Device: "dev2", App: dinely, Target: button("Pay $3")})
	if v.Decision != Ask {
		t.Fatalf("other device should not be read-only: %v", v.Decision)
	}
	v = e.Evaluate(Input{Op: OpTap, Device: "dev2", App: dinely, Target: button("Pay $3"), ReadOnly: true})
	if v.Decision != Deny {
		t.Fatalf("read-only scope: %v", v.Decision)
	}
}

func TestTaskAppsAndScope(t *testing.T) {
	e := Default()
	apps := []string{"Dinely"}
	v := e.Evaluate(Input{Op: OpLaunch, Launch: whatsapp, TaskApps: apps})
	if v.Decision != Deny || !v.Scope || v.Rule != RuleTaskApps {
		t.Fatalf("launch outside task apps: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpLaunch, Launch: dinely, TaskApps: apps})
	if v.Decision != Allow {
		t.Fatalf("launch inside task apps: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpType, App: whatsapp, Text: "x", TaskApps: apps, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}})
	if v.Decision != Ask || v.Rule != RuleTaskApps {
		t.Fatalf("typing outside task apps: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpTap, App: launcher, TaskApps: apps, Target: button("Dinely")})
	if v.Decision != Allow {
		t.Fatalf("launcher is always in the list: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpTap, App: whatsapp, ScopeApps: apps, Target: button("Chats")})
	if v.Decision != Deny || !v.Scope || v.Rule != RuleAgentScope {
		t.Fatalf("agent scope: %+v", v)
	}
}

func TestRulesFile(t *testing.T) {
	f, err := ParseFile([]byte(`
rules:
  - name: Chatter sends
    match: { app: com.chatter.app, action: [commit, send_message] }
    decision: ask
  - name: No deleting
    match: { label: delete }
    decision: deny
`))
	if err != nil {
		t.Fatal(err)
	}
	e := New(f, nil)
	chatter := screen.App{Name: "Chatter", ID: "com.chatter.app"}
	if v := e.Evaluate(tap(chatter, "Send")); v.Decision != Ask || v.Rule != "Chatter sends" {
		t.Fatalf("app rule: %+v", v)
	}
	if v := e.Evaluate(tap(chatter, "Delete chat")); v.Decision != Deny || v.Rule != "No deleting" {
		t.Fatalf("strictest wins: %+v", v)
	}
	if v := e.Evaluate(tap(dinely, "Send")); v.Decision != Allow {
		t.Fatalf("no rule for Dinely in this file: %+v", v)
	}
	if _, err := ParseFile([]byte("rules:\n  - name: x\n    match: { action: fly }\n    decision: ask\n")); err == nil {
		t.Fatal("unknown action accepted")
	}
	if _, err := ParseFile([]byte("rules:\n  - name: x\n    match: {}\n    decision: deny\n")); err == nil {
		t.Fatal("match-everything rule accepted")
	}
}

func TestLoadCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	rules, apps := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml")
	e, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Rules()) == 0 {
		t.Fatal("no default rules")
	}
	for _, p := range []string{rules, apps} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !private(st) {
			t.Errorf("%s mode %v", p, st.Mode().Perm())
		}
	}
	if err := os.WriteFile(apps, []byte("add:\n  finance:\n    ids: [com.dinely.app]\nexclude: [com.chase.sig.android]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Blocked(dinely); !ok {
		t.Fatal("app added in apps.yaml is not blocked")
	}
	if _, ok := e.Blocked(screen.App{ID: "com.chase.sig.android"}); ok {
		t.Fatal("excluded app still blocked")
	}
}

func TestMaskCodes(t *testing.T) {
	cases := map[string]string{
		"Your verification code is 482913": "Your verification code is ••••••",
		"G-123456 is your Google code":     "G-•••••• is your Google code",
		"【银行】您的验证码为 839201，5分钟内有效":         "【银行】您的验证码为 ••••••，5分钟内有效",
		"Code: 123 456":                      "Code: ••• •••",
		"Lunch at 12:30, see you":            "Lunch at 12:30, see you",
		"Order 1234567 shipped":              "Order 1234567 shipped",
		"Your code costs $1234":              "Your code costs $1234",
		"Your shopping order 123456 shipped": "Your shopping order 123456 shipped",
		"PIN: 4821":                          "PIN: ••••",
	}
	for in, want := range cases {
		if got := MaskCodes(in); got != want {
			t.Errorf("MaskCodes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterEvents(t *testing.T) {
	e := Default()
	got := e.FilterEvents([]screen.Event{
		{Kind: screen.EventNotification, App: "com.chase.sig.android", Text: "Payment received"},
		{Kind: screen.EventNotification, App: "com.google.android.apps.messaging", Text: "Your code is 556677"},
		{Kind: screen.EventToast, Text: "Added to cart"},
	})
	if len(got) != 2 {
		t.Fatalf("blocked notification kept: %+v", got)
	}
	if !strings.Contains(got[0].Text, "••••••") {
		t.Fatalf("code not masked: %q", got[0].Text)
	}
}

func TestAsksListsEveryRule(t *testing.T) {
	e := commitsEngine(t)
	v := e.Evaluate(Input{Op: OpTap, App: settings, Target: button("Erase all data")})
	if v.Decision != Ask || len(v.Asks) < 2 || v.Asks[0] != v.Rule {
		t.Fatalf("erase in Settings: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpType, App: whatsapp, Text: "x", Submit: true, TaskApps: []string{"Chatter"},
		Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}})
	if v.Decision != Ask || v.Rule != "Messages" || len(v.Asks) != 2 || v.Asks[1] != RuleTaskApps {
		t.Fatalf("send outside the task's apps: %+v", v)
	}
	if v := e.Evaluate(tap(dinely, "Orders")); len(v.Asks) != 0 {
		t.Fatalf("allowed action lists asks: %+v", v)
	}
}

func TestThirdPartyLaunchersAreSystem(t *testing.T) {
	e := Default()
	for _, id := range []string{"app.lawnchair.play", "app.lawnchair", "com.teslacoilsw.launcher", "com.apple.springboard"} {
		if !e.IsSystem(screen.App{ID: id}) {
			t.Errorf("%s is not a system app", id)
		}
	}
}

func TestPresets(t *testing.T) {
	dir := t.TempDir()
	rules, apps := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml")
	e, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if e.Preset() != PresetPayments {
		t.Fatalf("default preset %d", e.Preset())
	}
	send := Input{Op: OpType, App: whatsapp, Text: "hi", Submit: true, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}

	// Payments only, the default.
	if v := e.Evaluate(tap(dinely, "Place order · $12.80")); v.Decision != Ask || v.Rule != "Payments" {
		t.Fatalf("payments only, payment: %+v", v)
	}
	for name, in := range map[string]Input{"delete": tap(dinely, "Delete"), "send": send} {
		if v := e.Evaluate(in); v.Decision != Allow {
			t.Fatalf("payments only, %s: %+v", name, v)
		}
	}
	if v := e.Evaluate(tap(chase, "Accounts")); v.Decision != Deny {
		t.Fatalf("payments only still blocks finance apps: %+v", v)
	}

	if err := e.SetPreset(PresetCommits); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]Input{"delete": tap(dinely, "Delete"), "send": send} {
		if v := e.Evaluate(in); v.Decision != Ask {
			t.Fatalf("payments, messages and deletions, %s: %+v", name, v)
		}
	}

	if err := e.SetPreset(PresetEvery); err != nil {
		t.Fatal(err)
	}
	if v := e.Evaluate(tap(dinely, "Orders")); v.Decision != Ask || v.Rule != RuleEvery {
		t.Fatalf("every action, navigation: %+v", v)
	}
	if v := e.Evaluate(tap(dinely, "Pay $3")); v.Rule != "Payments" {
		t.Fatalf("every action names the specific rule first: %+v", v)
	}
	e.SetReadOnly("", true)
	if v := e.Evaluate(tap(dinely, "Orders")); v.Decision != Ask {
		t.Fatalf("read-only with every action: navigation should still ask: %+v", v)
	}
	if v := e.Evaluate(tap(dinely, "Pay $3")); v.Decision != Deny || v.Rule != RuleReadOnly {
		t.Fatalf("read-only with every action: a payment is denied: %+v", v)
	}
	e.SetReadOnly("", false)

	if err := e.SetHideCodes(false); err != nil {
		t.Fatal(err)
	}
	if got := e.FilterEvents([]screen.Event{{Kind: screen.EventNotification, Text: "Your code is 556677"}}); got[0].Text != "Your code is 556677" {
		t.Fatalf("codes masked with HideCodes off: %q", got[0].Text)
	}
	if err := e.SetReadOnly("", true); err != nil {
		t.Fatal(err)
	}
	// The settings survive a restart.
	e2, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Preset() != PresetEvery || e2.HideCodes() || !e2.ReadOnly("") || !e2.ReadOnly("any device") {
		t.Fatalf("after reload: preset %d hide codes %v read-only %v", e2.Preset(), e2.HideCodes(), e2.ReadOnly(""))
	}
	if st, _ := os.Stat(rules); !private(st) {
		t.Fatalf("rules file mode %v", st.Mode().Perm())
	}
	if err := e.SetPreset(Preset(7)); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestLegacyRulesFile(t *testing.T) {
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.yaml")
	legacy := `rules:
  - { name: Finance apps, match: { category: finance }, decision: deny }
  - { name: Password managers, match: { category: passwords }, decision: deny }
  - { name: Payments, match: { action: payment }, decision: ask }
  - { name: Messages, match: { action: send_message }, decision: ask }
  - { name: Commits, match: { action: commit }, decision: ask }
  - { name: Installs and links, match: { action: [install_app, open_url] }, decision: ask }
  - { name: Calls, match: { action: call }, decision: ask }
  - { name: Settings changes, match: { action: change_setting }, decision: ask }
`
	os.WriteFile(rules, []byte(legacy), 0o600)
	e, err := Load(rules, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Preset() != PresetCommits || len(e.Rules()) != len(commitRules)+2+len(builtinRules) {
		t.Fatalf("migrated: preset %d, %d rules", e.Preset(), len(e.Rules()))
	}
	// A hand-edited file without the Commits rule keeps exactly its rules.
	os.WriteFile(rules, []byte(strings.Replace(legacy, "  - { name: Commits, match: { action: commit }, decision: ask }\n", "", 1)), 0o600)
	e, _ = Load(rules, "")
	if e.Preset() != PresetNone {
		t.Fatalf("custom legacy file got preset %d", e.Preset())
	}
	if v := e.Evaluate(tap(dinely, "Delete")); v.Decision != Allow {
		t.Fatalf("custom legacy file asks for commits: %+v", v)
	}
}

func TestBlockAndUnblockApps(t *testing.T) {
	dir := t.TempDir()
	rules, apps := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml")
	e, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetAppBlocked(dinely, true); err != nil {
		t.Fatal(err)
	}
	if rule, ok := e.Blocked(dinely); !ok || rule != RuleBlocked {
		t.Fatalf("blocked app: %q %v", rule, ok)
	}
	if err := e.SetAppBlocked(screen.App{Name: "My Bank"}, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Blocked(screen.App{ID: "com.mybank", Name: "my bank"}); !ok {
		t.Fatal("app blocked by name not blocked")
	}
	installed := []screen.App{dinely, chase, whatsapp, {ID: "com.1password.1password", Name: "1Password"}}
	got := e.BlockedApps(installed)
	byID := map[string]BlockedApp{}
	for _, b := range got {
		byID[b.ID+"|"+b.Name] = b
	}
	if b := byID["com.dinely.app|Dinely"]; !b.ByPerson || b.Category != CatBlocked {
		t.Fatalf("Dinely: %+v (all %+v)", b, got)
	}
	if b := byID["com.chase.sig.android|Chase"]; b.ByPerson || b.Category != CatFinance || b.Rule != "Finance apps" {
		t.Fatalf("Chase: %+v", b)
	}
	if b := byID["com.1password.1password|1Password"]; b.Category != CatPasswords {
		t.Fatalf("1Password: %+v", b)
	}
	if b := byID["|My Bank"]; !b.ByPerson {
		t.Fatalf("My Bank: %+v (all %+v)", b, got)
	}
	if _, ok := byID["com.whatsapp|WhatsApp"]; ok {
		t.Fatal("WhatsApp listed as blocked")
	}

	// Unblocking: one the person blocked, and a bundled banking app.
	if err := e.SetAppBlocked(dinely, false); err != nil {
		t.Fatal(err)
	}
	if err := e.SetAppBlocked(chase, false); err != nil {
		t.Fatal(err)
	}
	for _, a := range []screen.App{dinely, chase} {
		if _, ok := e.Blocked(a); ok {
			t.Fatalf("%s still blocked", a.Name)
		}
	}
	// It all survives a restart.
	e2, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e2.Blocked(chase); ok {
		t.Fatal("unblocked Chase blocked again after reload")
	}
	if _, ok := e2.Blocked(screen.App{Name: "My Bank"}); !ok {
		t.Fatal("My Bank unblocked after reload")
	}
	if err := e2.SetAppBlocked(chase, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := e2.Blocked(chase); !ok {
		t.Fatal("blocking Chase again failed")
	}

	// An app named by a deny rule in rules.yaml cannot be unblocked here.
	f, _ := ParseFile([]byte("preset: 1\nrules:\n  - { name: No Dinely, match: { app: com.dinely.app }, decision: deny }\n"))
	e3 := New(f, nil)
	if err := e3.SetAppBlocked(dinely, false); !errors.Is(err, ErrBlockedByRule) {
		t.Fatalf("unblock an app a rule names: %v", err)
	}
}

// GitHub issue #18: an Android app clone's id ("com.tencent.mm@999",
// driver.CloneID) is judged by its package, as the app it copies.
func TestAppCloneIDs(t *testing.T) {
	e := Default()
	clone := screen.App{ID: chase.ID + "@999"}
	if rule, ok := e.Blocked(clone); !ok || rule != "Finance apps" {
		t.Fatalf("a bank's clone not blocked: %q %v", rule, ok)
	}
	if _, ok := e.Blocked(screen.App{ID: "com.hsbc.hsbcnet@128"}); !ok {
		t.Fatal("a clone's id did not match a prefix entry")
	}
	if _, ok := e.Blocked(screen.App{ID: dinely.ID + "@999"}); ok {
		t.Fatal("Dinely's clone blocked")
	}
	v := e.Evaluate(Input{Op: OpLaunch, Launch: screen.App{ID: whatsapp.ID + "@999"}, TaskApps: []string{whatsapp.ID}})
	if v.Decision != Allow {
		t.Fatalf("a clone of an app in the task's list: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpType, App: whatsapp, Text: "x", TaskApps: []string{whatsapp.ID + "@999"}, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}})
	if v.Decision != Allow {
		t.Fatalf("the screen of a clone in the task's list: %+v", v)
	}
	for id, want := range map[string]string{
		"com.tencent.mm@999": "com.tencent.mm",
		"com.tencent.mm":     "com.tencent.mm",
		"com.tencent.mm@":    "com.tencent.mm@",
		"a@b.c":              "a@b.c",
		"":                   "",
	} {
		if got := clonePackage(id); got != want {
			t.Errorf("clonePackage(%q) = %q; want %q", id, got, want)
		}
	}
}
