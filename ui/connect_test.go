package ui

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/agents"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

func TestShellWord(t *testing.T) {
	plain := "/Applications/Pinky.app/Contents/MacOS/Pinky"
	if got := shellWord(plain); got != plain {
		t.Errorf("shellWord(%q) = %q, want it as it is", plain, got)
	}
	cases := map[string]string{
		"/Users/me/My Apps/Pinky.app/Contents/MacOS/Pinky": `'/Users/me/My Apps/Pinky.app/Contents/MacOS/Pinky'`,
		"/tmp/it's/pinky": `'/tmp/it'\''s/pinky'`,
	}
	if runtime.GOOS == "windows" {
		cases = map[string]string{
			`C:\Users\Jane Doe\AppData\Local\Programs\Pinky\Pinky.exe`: `"C:\Users\Jane Doe\AppData\Local\Programs\Pinky\Pinky.exe"`,
		}
	}
	for in, want := range cases {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	cmd := agentCommand()
	if !strings.HasSuffix(cmd, " mcp") || !strings.Contains(cmd, filepath.Base(exe)) {
		t.Errorf("agentCommand() = %q, want this executable (%s) and mcp", cmd, exe)
	}
	if strings.Contains(agentProgram(), "pinky ") || agentProgram() == "pinky" {
		t.Errorf("agentProgram() = %q: a name on the PATH, which another program can take", agentProgram())
	}
}

func TestConnectAgents(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secAgents
	tt := mg.NewTester(ta.view, 1280, 820)
	claude := "claude mcp add --scope user pinky -- " + agentCommand()
	// Without a way to change agents' settings, the commands show under
	// their labels; copying them gives their text.
	for _, want := range []string{"Connect an agent", "Command to add Pinky to Claude Code", "Command that starts Pinky for an agent"} {
		if !hasText(tt, want) {
			t.Fatalf("Settings › Agents lacks %q: %q", want, tt.Texts())
		}
	}
	for label, want := range map[string]string{
		"Copy the command for Claude Code":  claude,
		"Copy the command for other agents": agentCommand(),
	} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != want {
			t.Errorf("%s copied %q, want %q", label, tt.Clipboard(), want)
		}
	}
}

// fakeSetup is the agents on a computer that is not this one.
type fakeSetup struct {
	mu      sync.Mutex
	list    []agents.Status
	calls   []string
	err     error
	restart agents.RestartKind
	opens   map[string]bool // agents whose Connect opens Pinky's extension in their app
}

func (f *fakeSetup) Find(context.Context) []agents.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agents.Status(nil), f.list...)
}

func (f *fakeSetup) change(id string, state agents.State, what string) (agents.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, what+" "+id)
	if f.err != nil {
		return agents.Result{}, f.err
	}
	if what == "connect" && f.opens[id] {
		return agents.Result{Opened: "/data/agents/Pinky.mcpb"}, nil
	}
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].State, f.list[i].Why = state, agents.WhyNone
		}
	}
	return agents.Result{Backups: []string{"/backups/" + id}, Restart: f.restart}, nil
}

func (f *fakeSetup) Connect(_ context.Context, id string) (agents.Result, error) {
	return f.change(id, agents.Connected, "connect")
}

func (f *fakeSetup) Remove(_ context.Context, id string) (agents.Result, error) {
	return f.change(id, agents.NotConnected, "remove")
}

func (f *fakeSetup) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func someAgents() []agents.Status {
	return []agents.Status{
		{ID: "claude-code", Name: "Claude Code", Found: true, Settings: "/home/.claude.json", Restart: agents.RestartSession},
		{ID: "codex", Name: "Codex", Found: true, State: agents.Outdated, Why: agents.WhyMissing, Settings: "/home/.codex/config.toml"},
		{ID: "cursor", Name: "Cursor", Found: true, State: agents.Connected, Settings: "/home/.cursor/mcp.json"},
		{ID: "vscode", Name: "VS Code"},
		{ID: "gemini", Name: "Gemini CLI"},
	}
}

func setupApp(t *testing.T, s fakeapi.Scenario, list []agents.Status) (*testApp, *fakeSetup) {
	ta := newTestApp(t, s)
	f := &fakeSetup{list: list, restart: agents.RestartSession}
	ta.opt.AgentSetup = f
	ta.sync()
	return ta, f
}

// noAgents is a fake gateway that no agent has connected to yet.
type noAgents struct{ *fakeapi.API }

func (noAgents) Agents(context.Context) ([]gateway.AgentView, error) { return nil, nil }

// firstRun is setupApp on a gateway that no agent has connected to.
func firstRun(t *testing.T, s fakeapi.Scenario, list []agents.Status) (*testApp, *fakeSetup) {
	ta, f := setupApp(t, s, list)
	ta.app.api = noAgents{ta.api}
	return ta, f
}

func TestConnectAgentsFound(t *testing.T) {
	ta, f := setupApp(t, fakeapi.OnePhone, someAgents())
	ta.page, ta.set.sec = pageSettings, secAgents
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	for _, want := range []string{"Claude Code", "Not connected", "Codex", "Starts a Pinky that is no longer there", "Cursor", "Connected",
		"Not on this computer: VS Code and Gemini CLI.", "Add an agent by hand"} {
		if !hasText(tt, want) {
			t.Fatalf("lacks %q: %q", want, tt.Texts())
		}
	}
	if hasText(tt, "Command that starts Pinky for an agent") {
		t.Error("the commands show before Add an agent by hand")
	}
	// Connect: the setup connects it, and the row says what to do next.
	if err := tt.Click("Connect Claude Code"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the connection", func() bool { tt.Frame(); return hasText(tt, "Start a new Claude Code session") })
	if got := f.called(); len(got) != 1 || got[0] != "connect claude-code" {
		t.Errorf("calls %q", got)
	}
	if !hasText(tt, "Pinky kept a copy of the settings as they were.") {
		t.Errorf("no word of the copy: %q", tt.Texts())
	}
	var revealed string
	ta.reveal = func(p string) { revealed = p }
	if err := tt.Click("Show the copy of Claude Code's settings"); err != nil {
		t.Fatal(err)
	}
	if revealed != "/backups/claude-code" {
		t.Errorf("showed %q", revealed)
	}
	// Update sets an outdated entry right; Remove takes Pinky out.
	if err := tt.Click("Update Pinky in Codex"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the update", func() bool { tt.Frame(); return len(f.called()) == 2 })
	if err := tt.Click("Remove Pinky from Cursor"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the removal", func() bool { tt.Frame(); return hasText(tt, "Removed.") })
	if got := f.called(); strings.Join(got, ", ") != "connect claude-code, connect codex, remove cursor" {
		t.Errorf("calls %q", got)
	}
	// A failure says why.
	f.mu.Lock()
	f.err = errors.New("Pinky left /home/.cursor/mcp.json as it is: not valid JSON at byte 3")
	f.mu.Unlock()
	if err := tt.Click("Connect Cursor"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the failure", func() bool {
		tt.Frame()
		return hasText(tt, "That didn't work: Pinky left /home/.cursor/mcp.json as it is")
	})
	// The commands still show, on asking.
	if err := tt.Click("Add an agent by hand"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !hasText(tt, "Command that starts Pinky for an agent") {
		t.Error("no commands")
	}
}

func TestConnectAgentsNoneFound(t *testing.T) {
	ta, _ := setupApp(t, fakeapi.OnePhone, []agents.Status{
		{ID: "claude-code", Name: "Claude Code"}, {ID: "claude-desktop", Name: "Claude Desktop"}, {ID: "codex", Name: "Codex"},
	})
	ta.page, ta.set.sec = pageSettings, secAgents
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	for _, want := range []string{"Pinky found no agent on this computer.", "Get Claude Code", "Get Claude Desktop", "Pinky also works with Codex."} {
		if !hasText(tt, want) {
			t.Fatalf("lacks %q: %q", want, tt.Texts())
		}
	}
	var opened string
	saved := openURL
	openURL = func(_ *mg.Context, u string, done func(error)) { opened = u; done(nil) }
	t.Cleanup(func() { openURL = saved })
	if err := tt.Click("Get Claude Code"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(opened, "https://claude.com/") {
		t.Errorf("opened %q", opened)
	}
}

func TestAgentCard(t *testing.T) {
	one := []agents.Status{{ID: "claude-code", Name: "Claude Code", Found: true}, {ID: "codex", Name: "Codex"}}
	ta, f := firstRun(t, fakeapi.NoPhones, one)
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	if !hasText(tt, "Pinky found Claude Code on this computer.") {
		t.Fatalf("no card: %q", tt.Texts())
	}
	// One found: the card connects it.
	if err := tt.Click("Connect Claude Code"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the connection", func() bool { tt.Frame(); return !hasText(tt, "Pinky found Claude Code") })
	if got := f.called(); len(got) != 1 || got[0] != "connect claude-code" {
		t.Errorf("calls %q", got)
	}

	// Several found: Choose… opens Settings › Agents.
	ta, _ = firstRun(t, fakeapi.NoPhones, []agents.Status{
		{ID: "claude-code", Name: "Claude Code", Found: true}, {ID: "codex", Name: "Codex", Found: true}, {ID: "cursor", Name: "Cursor", Found: true},
	})
	tt = mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	if !hasText(tt, "Pinky found Claude Code, Codex and Cursor on this computer.") {
		t.Fatalf("no card: %q", tt.Texts())
	}
	if err := tt.Click("Choose…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ta.page != pageSettings || ta.set.sec != secAgents || hasText(tt, "Pinky found Claude Code, Codex") {
		t.Errorf("Choose… went to page %v, section %v", ta.page, ta.set.sec)
	}

	// None found: where to get one; Not now hides the card.
	ta, _ = firstRun(t, fakeapi.NoPhones, []agents.Status{{ID: "claude-code", Name: "Claude Code"}})
	tt = mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	if !hasText(tt, "Pinky lets an AI agent use your phone.") {
		t.Fatalf("no card: %q", tt.Texts())
	}
	if err := tt.Click("Not now"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if hasText(tt, "Pinky lets an AI agent use your phone.") {
		t.Error("Not now kept the card")
	}

	// An agent that can't start Pinky: Fix.
	ta, f = firstRun(t, fakeapi.NoPhones, []agents.Status{
		{ID: "claude-code", Name: "Claude Code", Found: true, State: agents.Outdated, Why: agents.WhyCoreutils},
	})
	tt = mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	if !hasText(tt, "Claude Code can't start Pinky") {
		t.Fatalf("no card: %q", tt.Texts())
	}
	if err := tt.Click("Fix Pinky in Claude Code"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the fix", func() bool { tt.Frame(); return !hasText(tt, "can't start Pinky") })
	if got := f.called(); len(got) != 1 || got[0] != "connect claude-code" {
		t.Errorf("calls %q", got)
	}
}

func TestAgentCardWaitsForAnAgent(t *testing.T) {
	// An agent that ran a task can reach Pinky: no card.
	ta, _ := setupApp(t, fakeapi.OnePhone, []agents.Status{{ID: "claude-code", Name: "Claude Code", Found: true}})
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "a task", func() bool { tt.Frame(); return ta.setup.loaded && len(ta.tasks) > 0 })
	tt.Frame()
	if hasText(tt, "Pinky found Claude Code") {
		t.Error("a card while an agent works")
	}
}

func TestTasksSayNoAgent(t *testing.T) {
	ta, _ := firstRun(t, fakeapi.Companion, []agents.Status{{ID: "codex", Name: "Codex"}})
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	tt.Frame()
	if len(ta.tasks) > 0 {
		t.Skip("the scenario runs a task")
	}
	if !hasText(tt, "No agent is connected to Pinky yet.") {
		t.Fatalf("lacks the hint: %q", tt.Texts())
	}
}

// TestAgentSetupScreenshots draws Settings › Agents › Connect an agent and
// the sidebar's card, in each of the client's languages, into
// $PINKY_SCREENSHOTS, to look for text that is cut or overflows.
func TestAgentSetupScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	loaded := func(ta *testApp) {
		tt := mg.NewTester(ta.view, 1280, 820)
		ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded && ta.agents.loaded })
	}
	save := func(ta *testApp, name string) {
		tt := mg.NewTester(ta.view, 1280, 820)
		tt.Frame()
		tt.Frame()
		var buf bytes.Buffer
		if err := png.Encode(&buf, tt.Image()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".png"), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, lang := range testLanguages() {
		inLanguage(t, lang)
		list := someAgents()
		list = append(list[:3:3], agents.Status{ID: "windsurf", Name: "Windsurf", Found: true,
			Problem: "Pinky can't read ~/.codeium/windsurf/mcp_config.json: not valid JSON at byte 12: expected a colon"}, list[3], list[4])
		ta, _ := setupApp(t, fakeapi.OnePhone, list)
		ta.page, ta.set.sec = pageSettings, secAgents
		loaded(ta)
		ta.changeAgent("claude-code", false)
		ta.waitFor(t, "the connection", func() bool { return len(ta.setup.done) == 1 })
		ta.setup.list[2].State = agents.Connected
		save(ta, "agents-settings-"+lang)
		ta.setup.byHand = true
		save(ta, "agents-settings-by-hand-"+lang)

		ta, _ = setupApp(t, fakeapi.OnePhone, []agents.Status{{ID: "claude-code", Name: "Claude Code"}, {ID: "claude-desktop", Name: "Claude Desktop"},
			{ID: "codex", Name: "Codex"}, {ID: "cursor", Name: "Cursor"}, {ID: "vscode", Name: "VS Code"}})
		ta.page, ta.set.sec = pageSettings, secAgents
		loaded(ta)
		save(ta, "agents-none-"+lang)

		for name, list := range map[string][]agents.Status{
			"one":    {{ID: "claude-code", Name: "Claude Code", Found: true}},
			"many":   {{ID: "claude-code", Name: "Claude Code", Found: true}, {ID: "codex", Name: "Codex", Found: true}, {ID: "cursor", Name: "Cursor", Found: true}},
			"none":   {{ID: "claude-code", Name: "Claude Code"}},
			"broken": {{ID: "claude-code", Name: "Claude Code", Found: true, State: agents.Outdated, Why: agents.WhyMissing}},
		} {
			ta, _ := firstRun(t, fakeapi.NoPhones, list)
			loaded(ta)
			save(ta, "agents-card-"+name+"-"+lang)
		}
	}
}

func TestConnectClaudeDesktop(t *testing.T) {
	ta, f := setupApp(t, fakeapi.OnePhone, []agents.Status{{ID: "claude-desktop", Name: "Claude Desktop", Found: true}})
	f.opens = map[string]bool{"claude-desktop": true}
	ta.page, ta.set.sec = pageSettings, secAgents
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the agents", func() bool { tt.Frame(); return ta.setup.loaded })
	if err := tt.Click("Connect Claude Desktop"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the extension", func() bool {
		tt.Frame()
		return hasText(tt, "Pinky's extension is open in Claude Desktop: click Install there.")
	})
	// Installed there: connected, and only Claude Desktop removes it.
	f.mu.Lock()
	f.list[0].State, f.list[0].Extension = agents.Connected, true
	f.mu.Unlock()
	ta.setup.lastLoad = time.Time{}
	ta.waitFor(t, "the installed extension", func() bool {
		tt.Frame()
		return hasText(tt, "Connected as an extension. Remove it in Claude Desktop's Settings › Extensions.")
	})
	if _, ok := tt.Find("Remove Pinky from Claude Desktop"); ok {
		t.Error("a Remove button for the extension")
	}
	if hasText(tt, "click Install there") {
		t.Error("still says to install it")
	}
}
