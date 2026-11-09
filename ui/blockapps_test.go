package ui

import (
	"slices"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Settings › Safety › Add app offers the fake Pixel's apps: typing narrows
// them, a click blocks one by its id, an exact name typed finds the app,
// and a name no app has gets a warning before it is blocked as typed.
func TestAddAppPicksFromThePhones(t *testing.T) {
	ga := newGatewayApp(t, true)
	ga.page, ga.set.sec = pageSettings, secSafety
	tt := mg.NewTester(ga.view, 1280, 2600)
	ga.waitFor(t, "the phones", func() bool { tt.Frame(); return len(ga.devices) > 0 })
	open := func() {
		t.Helper()
		if err := tt.Click(locales.SettingsSafetyAddApp()); err != nil {
			t.Fatal(err)
		}
		ga.waitFor(t, "the phones' apps", func() bool { tt.Frame(); return ga.apps.loaded && !ga.apps.at.IsZero() && len(ga.apps.list) > 0 })
		tt.Frame()
	}
	blocked := func(name, id string) bool {
		return slices.ContainsFunc(ga.settings.BlockedApps, func(b BlockedApp) bool { return b.Name == name && b.ID == id })
	}

	open()
	if !hasText(tt, locales.SettingsSafetyAppsOnPhones()) || !hasText(tt, "Chatter") || !hasText(tt, "com.chatter.app") {
		t.Fatalf("the phones' apps are not offered: %q", tt.Texts())
	}
	tt.Type("chat")
	tt.Frame()
	if !hasText(tt, "Chatter") || hasText(tt, "Dinely") || hasText(tt, locales.SettingsSafetyAppNoMatch("chat")) {
		t.Fatalf("typing did not narrow the apps: %q", tt.Texts())
	}
	if err := tt.Click(locales.SettingsSafetyBlockApp("Chatter")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !blocked("Chatter", "com.chatter.app") || ga.set.adding {
		t.Fatalf("Chatter not blocked by its id: %+v", ga.settings.BlockedApps)
	}

	// An exact name, typed: the app of that name, by its id.
	open()
	if _, ok := tt.Find(locales.SettingsSafetyBlockApp("Chatter")); ok {
		t.Fatal("Chatter is offered again")
	}
	tt.Type("dinely")
	tt.Frame()
	if err := tt.Click(locales.SettingsSafetyBlock()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !blocked("Dinely", "com.dinely.app") {
		t.Fatalf("Dinely not blocked by its id: %+v", ga.settings.BlockedApps)
	}

	// A typo: warned, and blocked by the name as typed.
	open()
	tt.Type("Nortbank")
	tt.Frame()
	if !hasText(tt, locales.SettingsSafetyAppNoMatch("Nortbank")) {
		t.Fatalf("no warning for a name no app has: %q", tt.Texts())
	}
	if err := tt.Click(locales.SettingsSafetyBlock()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !blocked("Nortbank", "") {
		t.Fatalf("the typed name is not blocked: %+v", ga.settings.BlockedApps)
	}
}

func TestAppMatches(t *testing.T) {
	list := []gateway.InstalledApp{
		{ID: "com.cmbchina.ccd.pfmobile", Name: "掌上生活", Blocked: true},
		{ID: "com.example.chat", Name: "Chatter"},
		{ID: "com.example.chatgpt", Name: "ChatGPT"},
		{ID: "com.tencent.xin", Name: "WeChat", Aliases: []string{"微信"}},
	}
	got, exact := appMatches(list, "chatter")
	if !exact || len(got) != 1 || got[0].Name != "Chatter" {
		t.Fatalf("chatter: %v %+v", exact, got)
	}
	got, exact = appMatches(list, "Chat")
	if exact || len(got) != 3 {
		t.Fatalf("Chat: %v %+v", exact, got)
	}
	if got, exact = appMatches(list, "微信"); !exact || len(got) != 1 || got[0].ID != "com.tencent.xin" {
		t.Fatalf("an alias: %v %+v", exact, got)
	}
	// A blocked app is not offered, and its name is no typo.
	if got, exact = appMatches(list, "掌上生活"); !exact || len(got) != 0 {
		t.Fatalf("a blocked app: %v %+v", exact, got)
	}
	if x, ok := installedApp(list, "WECHAT"); !ok || x.ID != "com.tencent.xin" {
		t.Fatalf("installedApp: %+v %v", x, ok)
	}
}
