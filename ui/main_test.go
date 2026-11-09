package ui

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	mg "github.com/egoist/mygo/ui"
)

// testLang is the language of the client's words in tests: English, the
// labels the tests look for, whatever the computer's language is, unless a
// test asks for another with inLanguage.
var testLang atomic.Value

func TestMain(m *testing.M) {
	testLang.Store("en")
	i18n.SetLocaleFunc(func() string { return testLang.Load().(string) })
	// MyGo answers these on its main thread, which tests have none of: a
	// computer that prefers English, and picks that apply to the test.
	appPreferred = func() []string { return []string{"en-US"} }
	appLocale = func() string { return testLang.Load().(string) }
	setAppLocale = func(tag string) {
		if tag == "" {
			tag = "en"
		}
		testLang.Store(tag)
	}
	os.Exit(m.Run())
}

// testLanguages are the languages of the client's catalogs, English first:
// tests that check a layout or draw screenshots draw it in each, so a
// longer translation that no longer fits shows up.
func testLanguages() []string { return mygo.App.Languages() }

// langShort names lang in screenshot files: "en", "zh" or "ja".
func langShort(lang string) string {
	if lang == "zh-Hans" {
		return "zh"
	}
	return lang
}

// inLanguage shows the client's words in tag for the rest of the test, and
// MyGo's own in the testers given.
func inLanguage(t testing.TB, tag string, testers ...*mg.Tester) {
	t.Helper()
	testLang.Store(tag)
	for _, tt := range testers {
		tt.SetLocale(tag)
	}
	t.Cleanup(func() { testLang.Store("en") })
}
