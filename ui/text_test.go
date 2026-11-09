package ui

import (
	"strings"
	"testing"
	"time"
	"unicode"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

func TestLowerFirst(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"Sign in to Dinely":                    "sign in to Dinely",
		"A":                                    "a",
		"USB debugging is off":                 "USB debugging is off", // an acronym
		"iPhone asks to trust":                 "iPhone asks to trust",
		"42 files":                             "42 files",
		"\u00c9cole":                           "\u00e9cole",                           // Ecole with an acute accent
		"\u00c9COLE":                           "\u00c9COLE",                           // an acronym, accented
		"\u041f\u0440\u0438":                   "\u043f\u0440\u0438",                   // Russian
		"\u4f60\u63a5\u7ba1\u4e86\u624b\u673a": "\u4f60\u63a5\u7ba1\u4e86\u624b\u673a", // Chinese has no case
		"\u30c6\u30b9\u30c8":                   "\u30c6\u30b9\u30c8",                   // Japanese
		"\xff\xfe":                             "\xff\xfe",                             // not UTF-8: left alone
	} {
		if got := lowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHandoffBannerWithChineseReason shows an agent's reason written in
// Chinese whole, in the English UI and in the Chinese one: the banner
// lowers an English reason's first letter, which once cut a Chinese
// character in half.
func TestHandoffBannerWithChineseReason(t *testing.T) {
	// "You took over the phone. Hand control back when you're done."
	const reason = "\u4f60\u63a5\u7ba1\u4e86\u624b\u673a\u3002\u5904\u7406\u5b8c\u540e\u8bf7\u4ea4\u8fd8\u63a7\u5236\u6743\u3002"
	shown := strings.TrimSuffix(reason, "\u3002")
	ta := newTestAppTimeout(t, fakeapi.Handoff, 10*time.Minute)
	ta.waitFor(t, "the handoff", func() bool {
		for _, n := range ta.needs {
			if n.Kind == gateway.NeedHandoff {
				return true
			}
		}
		return false
	})
	for i := range ta.needs {
		if ta.needs[i].Kind == gateway.NeedHandoff {
			ta.needs[i].Title = reason
		}
	}
	for _, lang := range testLanguages() {
		tt := mg.NewTester(ta.view, 1280, 820)
		inLanguage(t, lang, tt)
		tt.Frame()
		if !hasText(tt, shown) {
			t.Errorf("%s: the banner and the card do not show the reason whole: %q", lang, tt.Texts())
		}
		if hasText(tt, "\ufffd") {
			t.Errorf("%s: a character was cut: %q", lang, tt.Texts())
		}
	}
}

// TestFeedbackDiagnosticsStayEnglish: the diagnostics in a feedback issue
// are for whoever reads the issue, in English, whatever language the app
// shows.
func TestFeedbackDiagnosticsStayEnglish(t *testing.T) {
	inLanguage(t, "zh-Hans")
	ta := newTestApp(t, fakeapi.OnePhone)
	lines := strings.Join(ta.diagnosticLines(), "\n")
	if !strings.Contains(lines, "keep 30 days") {
		t.Errorf("the diagnostics do not say the retention in English: %q", lines)
	}
	for _, r := range lines {
		if unicode.Is(unicode.Han, r) {
			t.Errorf("Chinese in the diagnostics: %q", lines)
			break
		}
	}
}
