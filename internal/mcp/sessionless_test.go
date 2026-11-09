package mcp

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
)

// idless sends what Grok Bot sends (GitHub DIYgod/pinky#10): fixed headers,
// so no request after initialize carries the session id.
type idless struct{ *rawClient }

func (c idless) initialize() {
	c.t.Helper()
	out, sid, code := c.post("", map[string]any{"jsonrpc": "2.0", "id": c.nextID.Add(1), "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": c.name, "version": "1.0"}}})
	if code != http.StatusOK || sid == "" || out["result"] == nil {
		c.t.Fatalf("initialize: %d %v", code, out)
	}
	if _, _, code := c.post("", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); code != http.StatusAccepted {
		c.t.Fatalf("initialized without a session id: %d", code)
	}
}

func (c idless) call(tool string, args map[string]any) string {
	c.t.Helper()
	return c.rawClient.call("", tool, args)
}

func (c idless) get() int {
	req, _ := http.NewRequest(http.MethodGet, c.url+HTTPPath, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.agent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestClientWithoutSessionID(t *testing.T) {
	h := newHarness(t, func(o *gateway.Options) { o.MaxBlock = 300 * time.Millisecond })
	ts := httptest.NewServer(h.srv.HTTP("tok"))
	defer ts.Close()
	grok := idless{&rawClient{t: t, url: ts.URL, token: "tok", name: "grok", agent: "GrokBot/1.0 (+https://x.ai)"}}

	grok.initialize()
	if code := grok.get(); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET stream without a session id: %d, want 405", code)
	}
	text := grok.call("phone_connect", map[string]any{"device": "Pixel 9", "task": "Reorder lunch"})
	if !strings.HasPrefix(text, "settled · Pixel 9") {
		t.Fatalf("connect:\n%s", text)
	}
	text = grok.call("phone_launch", map[string]any{"app": "Dinely"})
	if !strings.HasPrefix(text, "settled") {
		t.Fatalf("the next call found no phone:\n%s", text)
	}
	text = grok.call("phone_tap", map[string]any{"ref": refOf(t, text, "Orders")})
	// It may initialize again, as after a reconnect: same session.
	grok.initialize()
	text = grok.call("phone_tap", map[string]any{"ref": refOf(t, text, "Reorder · Chicken rice bowl")})
	text = grok.call("phone_tap", map[string]any{"ref": refOf(t, text, "Cart · 1")})
	if !strings.HasPrefix(text, "settled") {
		t.Fatalf("taps:\n%s", text)
	}
	if d := h.g.Devices()[0]; !strings.HasPrefix(d.StatusLine, "Grok (remote) · Reorder lunch") {
		t.Fatalf("device %q: the person should see the agent by its own name", d.StatusLine)
	}

	// Paying asks; the call repeats with the approval id.
	place := refOf(t, text, "Place order · $12.80")
	text = grok.call("phone_tap", map[string]any{"ref": place, "intent": "Pay $12.80"})
	m := regexp.MustCompile(`approval_id: (\S+)`).FindStringSubmatch(text)
	if !strings.HasPrefix(text, "approval_pending") || m == nil {
		t.Fatalf("pay:\n%s", text)
	}
	done := make(chan string, 1)
	go func() {
		done <- grok.call("phone_tap", map[string]any{"ref": place, "intent": "Pay $12.80", "approval_id": m[1]})
	}()
	time.Sleep(100 * time.Millisecond)
	if err := h.g.Decide(m[1], gateway.AllowOnce, ""); err != nil {
		t.Fatal(err)
	}
	if text := <-done; !strings.HasPrefix(text, "settled") || !strings.Contains(text, "Order placed") {
		t.Fatalf("approved pay:\n%s", text)
	}

	// Others keep their own sessions: an agent that keeps its id, and
	// another client without one (told apart by its User-Agent).
	codex := httpClient(t, ts.URL, "tok", "codex")
	defer codex.Close()
	if text := callText(t, codex, "phone_tap", map[string]any{"ref": 1}); !strings.Contains(text, "No phone is connected in this session") {
		t.Fatalf("an agent that keeps its id reached Grok's phone:\n%s", text)
	}
	other := idless{&rawClient{t: t, url: ts.URL, token: "tok", name: "other-bot", agent: "OtherBot/2"}}
	other.initialize()
	if text := other.call("phone_connect", map[string]any{"device": "Pixel 9", "task": "Mine"}); !strings.HasPrefix(text, "error: lease_held · holder: Grok (remote)") {
		t.Fatalf("another client without an id:\n%s", text)
	}
	if text := other.call("phone_tap", map[string]any{"ref": 1}); !strings.Contains(text, "No phone is connected in this session") {
		t.Fatalf("another client without an id reached Grok's phone:\n%s", text)
	}

	if text := grok.call("phone_release", map[string]any{"summary": "Ordered"}); !strings.Contains(text, "Released Pixel 9") {
		t.Fatalf("release:\n%s", text)
	}
}

// A client that never initializes (Pinky restarted under it) still gets a
// session, under a plain name.
func TestClientWithoutInitialize(t *testing.T) {
	h := newHarness(t)
	ts := httptest.NewServer(h.srv.HTTP("tok"))
	defer ts.Close()
	c := idless{&rawClient{t: t, url: ts.URL, token: "tok", name: "x", agent: "Bare/1"}}
	if text := c.call("phone_connect", map[string]any{"task": "Lunch"}); !strings.HasPrefix(text, "settled") {
		t.Fatalf("connect:\n%s", text)
	}
	if text := c.call("phone_observe", nil); !strings.HasPrefix(text, "settled") {
		t.Fatalf("observe:\n%s", text)
	}
	if d := h.g.Devices()[0]; !strings.HasPrefix(d.StatusLine, "Agent (remote) · Lunch") {
		t.Fatalf("device %q", d.StatusLine)
	}
	// Without the token, nothing.
	other := idless{&rawClient{t: t, url: ts.URL, token: "wrong", name: "x", agent: "Bare/1"}}
	out, _, code := other.post("", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "phone_observe", "arguments": map[string]any{}}})
	if code != http.StatusUnauthorized || out != nil {
		t.Fatalf("wrong token: %d %v", code, out)
	}
}
