package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// handEdit saves a rules file as most editors do: to a new file renamed
// over the old one. Windows refuses to rename a file over one that anything
// holds open, whatever it shares, also for the microseconds a look at the
// file's size and time takes (os.Stat), and these tests look every 5 ms
// where the app looks every 2 s: one rename in about 40 was refused ("Access
// is denied", TestHandEditedRules 5 of 200 runs on Windows CI, 2026-11-08).
// There it tries again, as a person whose editor said so would.
func handEdit(t *testing.T, path, data string) {
	t.Helper()
	tmp := path + ".edit"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	err := os.Rename(tmp, path)
	for try := 0; err != nil && runtime.GOOS == "windows" && try < 100; try++ {
		time.Sleep(time.Millisecond)
		err = os.Rename(tmp, path)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// eventually waits for cond, for at most two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// TestHandEditedRules edits rules.yaml by hand while the gateway runs: the
// next action follows the edit without a restart or a look at Settings,
// Settings lists its rules and refuses a change made from the rules before
// it, and an edit that does not parse changes nothing, keeps Always allow
// from being kept, and is listed until it is fixed.
func TestHandEditedRules(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.yaml")
	g, _ := newTestGateway(t, func(o *Options) {
		o.Policy = loadPolicy(t, dir)
		o.RulesEvery = 5 * time.Millisecond
		o.MaxBlock = 50 * time.Millisecond
	})
	before := g.Rules().File

	// No preset: only placing an order asks, under a rule of the person's.
	edit := "preset: 0\nrules:\n" +
		"  - name: Finance apps\n    match: { category: finance }\n    decision: deny\n" +
		"  - name: Orders ask\n    match: { label: place order }\n    decision: ask\n"
	handEdit(t, rules, edit)
	eventually(t, "the edit to take effect", func() bool {
		r := g.policy.FileRules() // the engine's, without the refresh Rules does
		return len(r) == 2 && r[1].Name == "Orders ask"
	})
	s, _ := connect(t, g, "Lunch")
	place := ref(t, toCart(t, s), "Place order · $12.80")
	res := tap(s, place)
	mustStatus(t, res, StatusPending)
	if n := g.Needs()[0]; n.Rule != "Orders ask" || !n.AlwaysAllow {
		t.Fatalf("need %+v", n)
	}
	if err := g.Decide(res.ApprovalID, Deny, ""); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, s.Act(ctx, Call{ApprovalID: res.ApprovalID}, Action{Tool: "tap", Ref: place}), StatusDenied)

	// Settings lists the file's rules, and refuses a change made from the
	// rules before the edit.
	if got := g.Rules().File; len(got) != 2 || got[1].Name != "Orders ask" || got[1].Decision != "ask" {
		t.Fatalf("Rules after the edit: %+v", got)
	}
	if err := g.SetRules(before, before[:1]); !errors.Is(err, ErrRulesChanged) {
		t.Fatalf("SetRules from the rules before the edit: %v", err)
	}
	if data, _ := os.ReadFile(rules); string(data) != edit {
		t.Fatalf("a refused change wrote rules.yaml:\n%s", data)
	}

	// An edit that does not parse changes nothing, and is listed.
	broken := edit + "  - name: [Orders\n"
	handEdit(t, rules, broken)
	eventually(t, "the problem", func() bool { return len(g.policy.Problems()) == 1 })
	probs := g.FileProblems()
	if len(probs) != 1 || probs[0].File != "rules.yaml" || probs[0].Path != rules || !strings.Contains(probs[0].Error, "yaml:") {
		t.Fatalf("FileProblems %+v", probs)
	}
	// The order still asks, but Always allow cannot be kept: offered no
	// more, and when given all the same, it allows the task and says so.
	res = tap(s, place)
	mustStatus(t, res, StatusPending)
	if n := g.Needs()[0]; n.Rule != "Orders ask" || n.AlwaysAllow {
		t.Fatalf("need with a broken rules.yaml: %+v", n)
	}
	err := g.Decide(res.ApprovalID, AllowAlways, "")
	if err == nil || !strings.Contains(err.Error(), "rules.yaml cannot be used") {
		t.Fatalf("Always allow with a broken rules.yaml: %v", err)
	}
	res = s.Act(ctx, Call{ApprovalID: res.ApprovalID}, Action{Tool: "tap", Ref: place})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "for the rest of the task") {
		t.Fatalf("message: %s", res.Message)
	}
	if got := g.Exemptions(); len(got) != 0 {
		t.Fatalf("exemptions %+v", got)
	}
	if data, _ := os.ReadFile(rules); string(data) != broken {
		t.Fatalf("the broken rules.yaml was written over:\n%s", data)
	}

	// Fixed, it is listed no more.
	handEdit(t, rules, edit)
	eventually(t, "the fix", func() bool { return len(g.FileProblems()) == 0 })
}
