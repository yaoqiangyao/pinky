package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
)

// rawClient speaks MCP over HTTP by hand, as clients that are not built
// on an SDK do.
type rawClient struct {
	t      *testing.T
	url    string
	token  string
	name   string
	agent  string // User-Agent
	nextID atomic.Int64
}

// post sends one JSON-RPC message and returns the response's JSON-RPC
// message (from JSON or the first SSE event), its session id and status.
func (c *rawClient) post(session string, msg map[string]any) (map[string]any, string, int) {
	c.t.Helper()
	b, _ := json.Marshal(msg)
	req, _ := http.NewRequest(http.MethodPost, c.url+HTTPPath, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.agent)
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
		req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	text := string(body)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(text, "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				text = data
				break
			}
		}
	}
	json.Unmarshal([]byte(text), &out)
	return out, resp.Header.Get("Mcp-Session-Id"), resp.StatusCode
}

// open starts a session: initialize, then the initialized notification.
func (c *rawClient) open() string {
	c.t.Helper()
	_, sid, code := c.post("", map[string]any{"jsonrpc": "2.0", "id": c.nextID.Add(1), "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": c.name, "version": "1"}}})
	if code != http.StatusOK || sid == "" {
		c.t.Fatalf("initialize: %d, session %q", code, sid)
	}
	if _, _, code := c.post(sid, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); code != http.StatusAccepted {
		c.t.Fatalf("initialized: %d", code)
	}
	return sid
}

// call calls a tool in session sid and returns its text.
func (c *rawClient) call(sid, tool string, args map[string]any) string {
	c.t.Helper()
	out, _, code := c.post(sid, map[string]any{"jsonrpc": "2.0", "id": c.nextID.Add(1), "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	if code != http.StatusOK {
		c.t.Fatalf("%s: status %d", tool, code)
	}
	res, _ := out["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		c.t.Fatalf("%s: no result: %v", tool, out)
	}
	return content[0].(map[string]any)["text"].(string)
}

// close ends a session with DELETE.
func (c *rawClient) close(sid string) {
	req, _ := http.NewRequest(http.MethodDelete, c.url+HTTPPath, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Mcp-Session-Id", sid)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

// oneShot calls a tool the way some clients do: in a session of its own,
// whose id it never sends again; with closing, it ends that session after.
func (c *rawClient) oneShot(closing bool, tool string, args map[string]any) string {
	c.t.Helper()
	sid := c.open()
	text := c.call(sid, tool, args)
	if closing {
		c.close(sid)
	}
	return text
}

// A client that opens a new MCP session for every call, and never sends
// a session id twice, works through a task as one: each call continues
// the phone its last one left, approvals included. Other agents still find
// the phone in use.
func TestOneSessionPerCall(t *testing.T) {
	for _, closing := range []bool{false, true} {
		t.Run(fmt.Sprintf("closing=%v", closing), func(t *testing.T) {
			h := newHarness(t, func(o *gateway.Options) { o.MaxBlock = 300 * time.Millisecond })
			ts := httptest.NewServer(h.srv.HTTP("tok"))
			defer ts.Close()
			grok := &rawClient{t: t, url: ts.URL, token: "tok", name: "grok-bot", agent: "GrokBot/1.0"}
			call := func(tool string, args map[string]any) string { return grok.oneShot(closing, tool, args) }

			text := call("phone_connect", map[string]any{"device": "Pixel 9", "task": "Reorder lunch"})
			if !strings.HasPrefix(text, "settled · Pixel 9") {
				t.Fatalf("connect:\n%s", text)
			}
			text = call("phone_launch", map[string]any{"app": "Dinely", "device": "Pixel 9"})
			if !strings.HasPrefix(text, "settled") {
				t.Fatalf("launch in the next session:\n%s", text)
			}
			text = call("phone_tap", map[string]any{"ref": refOf(t, text, "Orders")})
			text = call("phone_tap", map[string]any{"ref": refOf(t, text, "Reorder · Chicken rice bowl")})
			text = call("phone_tap", map[string]any{"ref": refOf(t, text, "Cart · 1")})
			if !strings.HasPrefix(text, "settled") {
				t.Fatalf("taps:\n%s", text)
			}
			if d := h.g.Devices()[0]; !strings.HasPrefix(d.StatusLine, "Grok Bot (remote) · Reorder lunch") {
				t.Fatalf("device %q", d.StatusLine)
			}

			// Another remote agent finds the phone in use, between calls too.
			codex := httpClient(t, ts.URL, "tok", "codex")
			defer codex.Close()
			if text := callText(t, codex, "phone_connect", map[string]any{"device": "Pixel 9", "task": "Mine"}); !strings.HasPrefix(text, "error: lease_held") {
				t.Fatalf("another agent:\n%s", text)
			}

			// Paying asks; the approval id works from the next session.
			place := refOf(t, text, "Place order · $12.80")
			text = call("phone_tap", map[string]any{"ref": place, "intent": "Pay $12.80"})
			m := regexp.MustCompile(`approval_id: (\S+)`).FindStringSubmatch(text)
			if !strings.HasPrefix(text, "approval_pending") || m == nil {
				t.Fatalf("pay:\n%s", text)
			}
			id := m[1]
			done := make(chan string, 1)
			go func() {
				done <- call("phone_tap", map[string]any{"ref": place, "intent": "Pay $12.80", "approval_id": id})
			}()
			time.Sleep(100 * time.Millisecond)
			if err := h.g.Decide(id, gateway.AllowOnce, "enjoy"); err != nil {
				t.Fatal(err)
			}
			if text := <-done; !strings.HasPrefix(text, "settled") || !strings.Contains(text, "Order placed") {
				t.Fatalf("approved pay:\n%s", text)
			}
			if text := call("phone_release", map[string]any{"summary": "Ordered"}); !strings.Contains(text, "Released Pixel 9") {
				t.Fatalf("release:\n%s", text)
			}
			if d := h.g.Devices()[0]; d.Status != gateway.DeviceIdle {
				t.Fatalf("after release: %+v", d)
			}
		})
	}
}

// A client may keep a GET stream open for the whole session: calls still
// go through, and the stream sees no error.
func TestCallsWithAnOpenGETStream(t *testing.T) {
	h := newHarness(t)
	ts := httptest.NewServer(h.srv.HTTP("tok"))
	defer ts.Close()
	c := &rawClient{t: t, url: ts.URL, token: "tok", name: "grok-bot", agent: "GrokBot/1.0"}
	sid := c.open()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+HTTPPath, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Mcp-Session-Id", sid)
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("GET stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	streamEnded := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, bufio.NewReader(resp.Body))
		streamEnded <- err
	}()

	for i, step := range []struct {
		tool string
		args map[string]any
	}{
		{"phone_connect", map[string]any{"task": "Lunch"}},
		{"phone_launch", map[string]any{"app": "Calculator"}},
		{"phone_observe", nil},
		{"phone_release", nil},
	} {
		start := time.Now()
		text := c.call(sid, step.tool, step.args)
		if !strings.HasPrefix(text, "settled") {
			t.Fatalf("call %d, %s, with the GET stream open:\n%s", i, step.tool, text)
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Fatalf("%s took %v with the GET stream open", step.tool, took)
		}
	}
	select {
	case err := <-streamEnded:
		t.Fatalf("the GET stream ended during the calls: %v", err)
	default:
	}
	// A GET without a session id gets 405: no stream here, go on with
	// POST.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+HTTPPath, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed || resp2.Header.Get("Allow") != "POST" {
		t.Fatalf("GET without a session: %d, Allow %q", resp2.StatusCode, resp2.Header.Get("Allow"))
	}
}
