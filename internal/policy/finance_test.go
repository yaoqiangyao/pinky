package policy

import (
	"path/filepath"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// TestFieldTestFinanceApps: the money apps the field test found open to
// agents are blocked by default (field test bug 2): by id, by name on a
// read without an id, and by a name that says bank, wallet, securities,
// remittance or pay when the table does not know the app.
func TestFieldTestFinanceApps(t *testing.T) {
	e := Default()
	finance := []screen.App{
		{ID: "com.dbs.sg.dbsmbanking", Name: "digibank"},
		{ID: "com.dbs.dbspaylah", Name: "PayLah!"},
		{ID: "sg.trust", Name: "Trust"},
		{ID: "com.okex.OKExAppstoreFull", Name: "OKX"},
		{ID: "fi.ether.cashapp", Name: "ether.fi"},
		{ID: "app.ethenapay.co", Name: "Ethena"},
		{ID: "to.plasma.one.application", Name: "Plasma One"},
		{ID: "ag.jup.jupiter.ios", Name: "Jupiter"},
		{ID: "com.panda.exchangerate", Name: "Panda Remit"},
		{Name: "OKX"}, {Name: "DBS digibank"}, {Name: "Plasma One"},
		{ID: "com.example.mybank", Name: "Example Bank"}, {Name: "某某银行"}, {Name: "Example Wallet"},
		{Name: "Example Pay"}, {Name: "Example Securities"}, {Name: "某某证券"}, {Name: "QuickRemit"},
	}
	for _, a := range finance {
		if rule, ok := e.Blocked(a); !ok || rule != "Finance apps" {
			t.Errorf("%+v: blocked %v by %q", a, ok, rule)
		}
	}
	if rule, ok := e.Blocked(screen.App{ID: "sg.ndi.sp", Name: "Singpass"}); !ok || rule != "Password managers" {
		t.Errorf("Singpass: blocked %v by %q", ok, rule)
	}
	for _, a := range []screen.App{dinely, whatsapp, {Name: "Calculator"}, {Name: "Wallpapers"}, {Name: "Paydirt"},
		{Name: "Submit It"}, {Name: "Jupiter"}, {Name: "Trust"}} {
		if rule, ok := e.Blocked(a); ok {
			t.Errorf("%+v is blocked by %q", a, rule)
		}
	}
	// Settings lists them among the apps on the phones, as finance.
	got := e.BlockedApps([]screen.App{{ID: "com.example.mybank", Name: "Example Bank"}})
	found := false
	for _, b := range got {
		found = found || (b.ID == "com.example.mybank" && b.Category == CatFinance)
	}
	if !found {
		t.Fatalf("Settings misses the bank: %+v", got)
	}
}

// TestUnblockOneAppOfAPrefix: unblocking an app that a prefix entry blocks
// unblocks that app alone (field test bug 11), and lasts.
func TestUnblockOneAppOfAPrefix(t *testing.T) {
	dir := t.TempDir()
	rules, apps := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml")
	e, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	pluto := screen.App{ID: "com.cmbchina.pluto", Name: "掌上生活"}
	other := screen.App{ID: "com.cmbchina.other", Name: "Other CMB app"}
	bank := screen.App{ID: "com.example.mybank", Name: "Example Bank"}
	for _, a := range []screen.App{pluto, other, bank} {
		if _, ok := e.Blocked(a); !ok {
			t.Fatalf("%+v is not blocked to begin with", a)
		}
	}
	for _, a := range []screen.App{pluto, bank} {
		if err := e.SetAppBlocked(a, false); err != nil {
			t.Fatalf("unblocking %+v: %v", a, err)
		}
	}
	check := func(e *Engine, when string) {
		t.Helper()
		if _, ok := e.Blocked(pluto); ok {
			t.Errorf("%s: %+v still blocked", when, pluto)
		}
		if _, ok := e.Blocked(bank); ok {
			t.Errorf("%s: %+v still blocked", when, bank)
		}
		if rule, ok := e.Blocked(other); !ok || rule != "Finance apps" {
			t.Errorf("%s: the rest of the prefix: blocked %v by %q", when, ok, rule)
		}
	}
	check(e, "after unblocking")
	e, err = Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	check(e, "after a restart")

	// Blocked again, it is.
	if err := e.SetAppBlocked(pluto, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Blocked(pluto); !ok {
		t.Fatal("blocking it again did not")
	}
}

// Chinese banks' credit card apps, whose names say no 银行, are blocked by
// default: by their Android packages, their iOS bundle ids and their names.
func TestChineseCardApps(t *testing.T) {
	e := Default()
	apps := []screen.App{
		{ID: "com.bankcomm.maidanba"}, {ID: "com.cs_credit_bank"}, {ID: "com.cgb.creditcard.iphone"},
		{ID: "com.citiccard.mobilebank"}, {ID: "com.citicbank.creditspace"},
		{ID: "com.cmbchina.ccd.pluto.cmbActivity"}, {ID: "com.cmbchina.cmblife"},
		{ID: "com.spdbccc.app"}, {ID: "spdbccc.com.cn"}, {ID: "com.cmbc.cc.mbank"}, {ID: "com.cmbc.mobilePhone"},
		{ID: "com.ebank.creditcard"}, {ID: "com.cebbank.xyk"}, {ID: "com.cib.xyk"}, {ID: "com.HuaXiaBank.HuaCard"},
		{ID: "com.ccb.longjiLife"}, {ID: "com.icbc.elife"}, {ID: "com.yitong.mbank.psbc.creditcard"}, {ID: "com.psbc.creditcard"},
		{ID: "cn.com.spdb.mobilebank.per"}, {ID: "com.ccb.ccbDemo"}, {ID: "com.boc.BOCMBCI"},
		{Name: "买单吧"}, {Name: "发现精彩"}, {Name: "动卡空间"}, {Name: "掌上生活"}, {Name: "浦大喜奔"}, {Name: "全民生活"},
		{Name: "阳光惠生活"}, {Name: "兴业生活"}, {Name: "华彩生活"}, {Name: "建行生活"}, {Name: "工银e生活"},
		{Name: "邮储信用卡"}, {Name: "广银信用卡"},
	}
	for _, a := range apps {
		if rule, ok := e.Blocked(a); !ok || rule != "Finance apps" {
			t.Errorf("%+v: blocked %v by %q", a, ok, rule)
		}
	}
	for _, a := range []screen.App{{Name: "生活"}, {Name: "美团"}, {ID: "com.cs_credit_bank.other"}} {
		if rule, ok := e.Blocked(a); ok {
			t.Errorf("%+v is blocked by %q", a, rule)
		}
	}
}
