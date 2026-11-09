package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// gatewaySteps are steps as internal/gateway words them.
var gatewaySteps = []string{
	"Tapped “Place order · $12.80”",
	"Long-pressed “Photo”",
	"Tapped at 540,1200",
	"Tapped",
	"Typed “no cilantro”",
	"Typed “no cilantro” into “Note”",
	"Typed “chicken rice” and submitted",
	"Typed “chicken rice” into “Search” and submitted",
	"Swiped up",
	"Swiped down in “Messages”",
	"Swiped from 540,1800 to 540,600",
	"Pressed home",
	"Pressed forward delete",
	"Opened Messages",
	"Waiting for you: Tapped “Send SMS”",
	"Blocked by “Payments”: Tapped “Pay”",
	"Outside the scope: Opened Northbank",
	"Failed: Tapped “Pay”",
	"Connected to Pixel 9 for “Reorder lunch”",
	"Finished: done",
	"Waited for “Order placed”",
	"Asked you: “Is 3pm fine?”",
	"Asked you to take over: Sign in to Dinely",
	"Unlocked from the computer",
	"Listed the phone's Pinky folder (3 files)",
	"Copied 2 files from the phone: a.txt, b.png",
	"Copied 7 files to the phone: a, b, c, d, e, and 2 more",
	"Blocked by “Read-only”: copy to the phone",
	"Failed: list the phone's Pinky folder",
	"Received “Pictures/cat.png” (1.2 MB) from the agent",
	"Blocked by “Read-only”: receive “cat.png” from the agent",
	"Failed: receive “cat.png” from the agent",
	"Added 1 chicken rice bowl", // the fake's own words
}

func TestGatewayWordsInEnglish(t *testing.T) {
	for _, s := range gatewaySteps {
		if got := stepText(s); got != s {
			t.Errorf("stepText(%q) = %q, want it unchanged", s, got)
		}
	}
	for _, s := range []string{"Offline", "Claude Code · Reorder lunch · in the background", "Waiting for you · Pay: “X”?"} {
		if got := statusLine(gateway.DeviceView{StatusLine: s, Status: gateway.DeviceWorking}); got != s {
			t.Errorf("statusLine(%q) = %q", s, got)
		}
	}
	n := gateway.NeedView{Kind: gateway.NeedApproval, Title: "Pay: “Place order”?", Detail: "Claude Code · lunch"}
	if needTitle(n) != n.Title || needDetail(n) != n.Detail {
		t.Errorf("need changed in English: %q, %q", needTitle(n), needDetail(n))
	}
	// A step that ran on the person's Always allow says so on its second
	// line, also under the client's words for a step that may have
	// happened.
	const always = "Always allowed: “Payments” in Dinely"
	for status, want := range map[string]string{"settled": always, "uncertain": "May have happened\n" + always} {
		task := gateway.TaskView{State: gateway.TaskDone, Steps: []gateway.StepView{{Text: "Tapped “Pay”\n" + always, Status: status, Tool: "phone_tap"}}}
		if text, sub, _ := stepDisplay(task, 0); text != "Tapped “Pay”" || sub != want {
			t.Errorf("%s step: %q | %q, want %q", status, text, sub, want)
		}
	}
}

func TestGatewayWordsInChinese(t *testing.T) {
	inLanguage(t, "zh-Hans")
	zh := locales.In("zh-Hans")
	want := map[string]string{
		"Tapped “Place order · $12.80”":                    zh.GwDidTap("Place order · $12.80"),
		"Long-pressed “Photo”":                             zh.GwDidLongPress("Photo"),
		"Tapped at 540,1200":                               zh.GwDidTapAt("540", "1200"),
		"Tapped":                                           zh.GwDidTapBare(),
		"Typed “no cilantro”":                              zh.GwDidType("no cilantro"),
		"Typed “no cilantro” into “Note”":                  zh.GwDidTypeInto("no cilantro", "Note"),
		"Typed “chicken rice” and submitted":               zh.GwDidTypeSubmit("chicken rice"),
		"Typed “chicken rice” into “Search” and submitted": zh.GwDidTypeIntoSubmit("chicken rice", "Search"),
		"Swiped up":                                                zh.GwDidSwipe(zh.GwDirUp()),
		"Swiped down in “Messages”":                                zh.GwDidSwipeIn(zh.GwDirDown(), "Messages"),
		"Swiped from 540,1800 to 540,600":                          zh.GwDidSwipeFromTo("540", "1800", "540", "600"),
		"Pressed home":                                             zh.GwDidPress(zh.GwKeyHome()),
		"Pressed forward delete":                                   zh.GwDidPress(zh.GwKeyForwardDelete()),
		"Opened Messages":                                          zh.GwDidOpen("Messages"),
		"Waiting for you: Tapped “Send SMS”":                       zh.GwStepWaiting(zh.GwDoTap("Send SMS")),
		"Blocked by “Payments”: Tapped “Pay”":                      zh.GwStepBlocked(zh.RulePayments(), zh.GwDoTap("Pay")),
		"Outside the scope: Opened Northbank":                      zh.GwStepOutside(zh.GwDoOpen("Northbank")),
		"Failed: Tapped “Pay”":                                     zh.GwStepFailed(zh.GwDoTap("Pay")),
		"Connected to Pixel 9 for “Reorder lunch”":                 zh.GwStepConnected("Pixel 9", "Reorder lunch"),
		"Finished: done":                                           zh.GwStepFinished(zh.GwOutcomeDone()),
		"Waited for “Order placed”":                                zh.GwStepWaited("Order placed"),
		"Asked you: “Is 3pm fine?”":                                zh.GwStepAskedYou("“Is 3pm fine?”"),
		"Asked you to take over: Sign in to Dinely":                zh.GwStepAskedTakeOver("Sign in to Dinely"),
		"Unlocked from the computer":                               zh.GwStepUnlocked(),
		"Listed the phone's Pinky folder (3 files)":                zh.GwStepListedFolder("3"),
		"Copied 2 files from the phone: a.txt, b.png":              zh.GwStepCopiedFrom("2", zh.GwFilesList("a.txt"+zh.ListSeparator()+"b.png")),
		"Blocked by “Read-only”: copy to the phone":                zh.GwStepBlocked(zh.RuleReadOnly(), zh.GwDoCopyToPhone()),
		"Failed: list the phone's Pinky folder":                    zh.GwStepFailed(zh.GwDoListFolder()),
		"Received “Pictures/cat.png” (1.2 MB) from the agent":      zh.GwStepReceivedFile("Pictures/cat.png", "1.2 MB"),
		"Blocked by “Read-only”: receive “cat.png” from the agent": zh.GwStepBlocked(zh.RuleReadOnly(), zh.GwDoReceiveFile("cat.png")),
		"Failed: receive “cat.png” from the agent":                 zh.GwStepFailed(zh.GwDoReceiveFile("cat.png")),
		"Added 1 chicken rice bowl":                                "Added 1 chicken rice bowl",
		"Copied 7 files to the phone: a, b, and 2 more":            zh.GwStepCopiedTo("7", zh.GwFilesList("a"+zh.ListSeparator()+"b"+zh.ListSeparator()+zh.GwFilesMore("2"))),
	}
	for in, out := range want {
		if got := stepText(in); got != out {
			t.Errorf("stepText(%q) = %q, want %q", in, got, out)
		}
		if got := stepText(in); got == in && in != "Added 1 chicken rice bowl" {
			t.Errorf("stepText(%q) stayed English", in)
		}
	}
	titles := map[string]string{
		"Pay: “Place order · $12.80”?":     zh.GwAskPay("Place order · $12.80"),
		"Send “See you at 3”?":             zh.GwAskSend("See you at 3"),
		"Send the message?":                zh.GwAskSendMessage(),
		"Open Northbank?":                  zh.GwAskOpen("Northbank"),
		"Change “Wi-Fi”?":                  zh.GwAskChange("Wi-Fi"),
		"Tap “Delete”?":                    zh.GwAskTap("Delete"),
		"Type into “Message”?":             zh.GwAskTypeInto("Message"),
		"Press enter in “Message”?":        zh.GwAskPressIn(zh.GwKeyEnter(), "Message"),
		"Tapped at 540,1200?":              zh.GwAskQuestion(zh.GwDoTapAt("540", "1200")),
		"Let Codex use your phones?":       zh.GwAskAgent("Codex"),
		"Copy “ticket.pdf” to Pixel 9?":    zh.GwAskQuestion(zh.GwDoCopyFile("ticket.pdf", "Pixel 9")),
		"Copy 3 files to Pixel 9?":         zh.GwAskQuestion(zh.GwDoCopyFiles("3", "Pixel 9")),
		"Something the gateway never says": "Something the gateway never says",
	}
	for in, out := range titles {
		n := gateway.NeedView{Kind: gateway.NeedApproval, Title: in, Rule: "Payments"}
		if in == "Let Codex use your phones?" {
			n.Rule = "New agent"
		}
		if got := needTitle(n); got != out {
			t.Errorf("needTitle(%q) = %q, want %q", in, got, out)
		}
	}
	handoff := gateway.NeedView{Kind: gateway.NeedHandoff, Title: "Sign in to Dinely?"}
	if got := needTitle(handoff); got != handoff.Title {
		t.Errorf("a handoff's title is the agent's words, got %q", got)
	}
	unlock := gateway.NeedView{Kind: gateway.NeedUnlock, Title: "Unlock Pixel 9", Detail: "Claude Code is waiting for the phone to unlock · Reorder lunch"}
	if got, want := needTitle(unlock)+" / "+needDetail(unlock), zh.GwUnlockTitle("Pixel 9")+" / "+zh.GwUnlockDetail("Claude Code", "Reorder lunch"); got != want {
		t.Errorf("unlock need: %q, want %q", got, want)
	}
	agent := gateway.NeedView{Kind: gateway.NeedApproval, Rule: "New agent", Detail: "Codex wants to connect for “Book a table”. Allowing gives it everything access.",
		Created: time.Date(2026, 11, 7, 3, 51, 0, 0, time.Local)}
	wants := zh.GwAgentWants("Codex", "Book a table")
	if got, want := needDetail(agent), wants+zh.GwAgentGives(zh.ScopeLineEverything()); got != want {
		t.Errorf("new agent detail: %q, want %q", got, want)
	}
	if got, want := agentRequestDetail(agent, true), zh.SentencePair(wants, zh.AgentRequestAskedAt("03:51")); got != want {
		t.Errorf("new agent detail with the picker: %q, want %q", got, want)
	}
	status := map[string]string{
		"Offline":                                         zh.GwStatusOffline(),
		"Offline · USB debugging is off":                  zh.GwStatusOfflineDetail("USB debugging is off"),
		"Offline · reconnecting for Reorder lunch":        zh.GwStatusReconnecting("Reorder lunch"),
		"Waiting for you · Pay: “Place order”?":           zh.GwStatusWaiting(zh.GwAskPay("Place order")),
		"Locked · unlock to continue Reorder lunch":       zh.GwStatusLockedTask("Reorder lunch"),
		"You're in control · Claude Code is paused":       zh.GwStatusInControlPaused("Claude Code"),
		"Claude Code · Reorder lunch · in the background": zh.GwStatusBackground("Claude Code · Reorder lunch"),
		"Claude Code · Reorder lunch":                     "Claude Code · Reorder lunch",
		"Idle":                                            zh.GwStatusIdle(),
	}
	for in, out := range status {
		if got := statusLine(gateway.DeviceView{StatusLine: in, Status: gateway.DeviceWorking}); got != out {
			t.Errorf("statusLine(%q) = %q, want %q", in, got, out)
		}
	}
}

// TestStepDisplayInChinese checks the client's words about steps.
func TestStepDisplayInChinese(t *testing.T) {
	inLanguage(t, "zh-Hans")
	zh := locales.In("zh-Hans")
	step := func(text, status string, state gateway.TaskState) string {
		task := gateway.TaskView{State: state, Steps: []gateway.StepView{{Text: text, Status: status, Tool: "phone_tap"}}}
		text, sub, _ := stepDisplay(task, 0)
		if sub != "" {
			return text + " | " + sub
		}
		return text
	}
	tapPay := zh.GwDoTap("Pay")
	for _, c := range []struct {
		text, status string
		state        gateway.TaskState
		want         string
	}{
		{"Waiting for you: Tapped “Pay”", "approval_pending", gateway.TaskWaiting, zh.StepWaiting(tapPay)},
		{"Waiting for you: Tapped “Pay”", "approval_pending", gateway.TaskWorking, zh.StepYouAnswered(tapPay)},
		{"Waiting for you: Tapped “Pay”", "approval_denied", gateway.TaskDone, zh.StepYouDenied(tapPay)},
		{"Waiting for you: Tapped “Pay”", "user_took_over", gateway.TaskPaused, tapPay + " | " + zh.StepNotDoneTookOver()},
		{"Waiting for you: Tapped “Pay”", "stale", gateway.TaskDone, tapPay + " | " + zh.StepNotDoneStale()},
		{"Asked you to take over: Sign in", "settled", gateway.TaskWorking, zh.StepYouHandedBack() + " | " + zh.StepAsked("Sign in")},
		{"Asked you to take over: Sign in", "approval_denied", gateway.TaskWorking, zh.StepYouDeclined() + " | " + zh.StepAsked("Sign in")},
		{"Asked you: “Is 3pm fine?”", "settled", gateway.TaskWorking, zh.StepYouAgreed("“Is 3pm fine?”")},
		{"Tapped “Reorder”", "settled", gateway.TaskDone, zh.GwDidTap("Reorder")},
		{"Tapped “Reorder”", "not_found", gateway.TaskDone, zh.GwDidTap("Reorder") + " | " + zh.StepFailedCode(zh.StepCodeNotFound())},
		{"Opened Dinely\nYour note: “No”", "settled", gateway.TaskDone, zh.GwDidOpen("Dinely") + " | " + zh.GwStepYourNote("“No”")},
		{"Tapped “Pay”\nAlways allowed: “Payments” in Dinely", "settled", gateway.TaskDone,
			zh.GwDidTap("Pay") + " | " + zh.GwStepAlwaysAllowed(zh.RulePayments(), "Dinely")},
		{"Tapped “Pay”\nAlways allowed: “Payments” in Dinely", "uncertain", gateway.TaskDone,
			zh.GwDidTap("Pay") + " | " + zh.StepMayHaveHappened() + "\n" + zh.GwStepAlwaysAllowed(zh.RulePayments(), "Dinely")},
		// A rule of the person's own keeps its name, " in " and all.
		{"Tapped “Go”\nAlways allowed: “Sign in” in Dinely", "settled", gateway.TaskDone,
			zh.GwDidTap("Go") + " | " + zh.GwStepAlwaysAllowed("Sign in", "Dinely")},
	} {
		if got := step(c.text, c.status, c.state); got != c.want {
			t.Errorf("%q (%s, %s) = %q, want %q", c.text, c.status, c.state, got, c.want)
		}
	}
}

// TestLanguageSetting picks Chinese in Settings › Advanced, then the
// system's language again.
func TestLanguageSetting(t *testing.T) {
	t.Cleanup(func() { langPicked, langPickedSet = "", false })
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secAdvanced
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	tags, labels := languageChoices()
	if len(tags) != 4 || tags[0] != "" || labels[0] != "System" {
		t.Fatalf("choices %q %q", tags, labels)
	}
	got := map[string]string{}
	for i := 1; i < len(tags); i++ {
		got[tags[i]] = labels[i]
	}
	want := map[string]string{"en": "English", "ja": i18n.Autonym("ja"), "zh-Hans": i18n.Autonym("zh-Hans")}
	if len(got) != len(want) {
		t.Fatalf("choices %q %q", tags, labels)
	}
	for tag, label := range want {
		if got[tag] != label {
			t.Fatalf("tag %q label %q, want %q (choices %q %q)", tag, got[tag], label, tags, labels)
		}
	}
	if !tt.HasText("Language") || languageNow() != "" {
		t.Fatalf("Settings › Advanced shows no Language row following the system (now %q)", languageNow())
	}
	pickLanguage("zh-Hans")
	tt.Frame()
	zh := locales.In("zh-Hans")
	if !tt.HasText(zh.SettingsLanguage()) || !tt.HasText(zh.SettingsAdvancedDevMode()) || languageNow() != "zh-Hans" {
		t.Errorf("after picking Simplified Chinese: Language row %v, now %q", tt.HasText(zh.SettingsLanguage()), languageNow())
	}
	pickLanguage("ja")
	tt.Frame()
	ja := locales.In("ja")
	if !tt.HasText(ja.SettingsLanguage()) || !tt.HasText(ja.SettingsAdvancedDevMode()) || languageNow() != "ja" {
		t.Errorf("after picking Japanese: Language row %v, now %q", tt.HasText(ja.SettingsLanguage()), languageNow())
	}
	pickLanguage("")
	tt.Frame()
	if !tt.HasText("Language") || languageNow() != "" {
		t.Errorf("after picking System: now %q", languageNow())
	}
}

// TestLanguageFollowsAJapaneseSystem: a computer that prefers Japanese
// reads Pinky in Japanese, and Settings › Advanced › Language says it
// follows the system.
func TestLanguageFollowsAJapaneseSystem(t *testing.T) {
	t.Cleanup(func() { langPicked, langPickedSet = "", false })
	prefer := appPreferred
	t.Cleanup(func() { appPreferred = prefer })
	appPreferred = func() []string { return []string{"ja-JP", "en-US"} }
	// MyGo shows the catalog's language that i18n.Match finds for the
	// system's languages.
	if got := i18n.Match(appPreferred(), mygo.App.Languages()); got != "ja" {
		t.Fatalf("a computer that prefers Japanese reads %q", got)
	}
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secAdvanced
	tt := mg.NewTester(ta.view, 1280, 820)
	inLanguage(t, "ja", tt)
	tt.Frame()
	ja := locales.In("ja")
	if languageNow() != "" || !tt.HasText(ja.SettingsLanguageSystem()) || !tt.HasText(ja.SettingsLanguage()) {
		t.Errorf("on a Japanese system: now %q, %q", languageNow(), tt.Texts())
	}
}
