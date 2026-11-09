package locales

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// catalog reads a catalog's messages, without the "@" entries.
func catalog(t *testing.T, file string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	out := map[string]string{}
	for k, v := range raw {
		if strings.HasPrefix(k, "@") {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			t.Fatalf("%s: %s: %v", file, k, err)
		}
		out[k] = s
	}
	return out
}

// argRe finds the arguments of an ICU message: {name} and the {name, …}
// of plural, select and formats.
var argRe = regexp.MustCompile(`\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,}]`)

// args returns the names of a message's arguments, sorted, without
// repeats. Quoted braces ('{') are not arguments.
func args(msg string) []string {
	msg = regexp.MustCompile(`'[{}#][^']*'`).ReplaceAllString(msg, "")
	var out []string
	for _, m := range argRe.FindAllStringSubmatch(msg, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// TestTranslations checks that every English message has a translation
// in each other catalog with the same arguments, and that no catalog
// has a key English lacks.
func TestTranslations(t *testing.T) {
	en := catalog(t, "en.json")
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == "en.json" {
			continue
		}
		tr := catalog(t, name)
		for k, msg := range en {
			got, ok := tr[k]
			if !ok {
				t.Errorf("%s lacks %q (%q)", name, k, msg)
				continue
			}
			if a, b := args(msg), args(got); !reflect.DeepEqual(a, b) {
				t.Errorf("%s %s: English has the arguments %v, translation %v", name, k, a, b)
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s %s: empty translation", name, k)
			}
		}
		for k := range tr {
			if _, ok := en[k]; !ok {
				t.Errorf("%s has %q, which en.json lacks", name, k)
			}
		}
	}
}

// TestLanguageMatch checks which catalog a language reads: Chinese written
// in simplified characters reads Chinese, Japanese reads Japanese, other
// languages English.
func TestLanguageMatch(t *testing.T) {
	zh, ja, en := In("zh-Hans").SidebarStopAll(), In("ja").SidebarStopAll(), In("en").SidebarStopAll()
	if zh == en {
		t.Fatalf("the Chinese catalog says %q as English does", zh)
	}
	if ja == en {
		t.Fatalf("the Japanese catalog says %q as English does", ja)
	}
	for _, tag := range []string{"zh-CN", "zh-Hans-SG", "zh"} {
		if got := In(tag).SidebarStopAll(); got != zh {
			t.Errorf("%s says %q, want the Chinese %q", tag, got, zh)
		}
	}
	for _, tag := range []string{"ja", "ja-JP"} {
		if got := In(tag).SidebarStopAll(); got != ja {
			t.Errorf("%s says %q, want the Japanese %q", tag, got, ja)
		}
	}
	for _, tag := range []string{"en-GB", "de", "zh-Hant"} {
		if got := In(tag).SidebarStopAll(); got != en {
			t.Errorf("%s says %q, want the English %q", tag, got, en)
		}
	}
}
