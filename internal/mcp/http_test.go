package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DIYgod/pinky/internal/gateway"
)

// bearer adds the agent token to every request.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func httpClient(t *testing.T, url, token, name string) *sdk.ClientSession {
	t.Helper()
	tr := &sdk.StreamableClientTransport{Endpoint: url + HTTPPath, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearer{token, http.DefaultTransport}}}
	cl := sdk.NewClient(&sdk.Implementation{Name: name, Version: "1.0"}, nil)
	cs, err := cl.Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func callText(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(*sdk.TextContent).Text
}

func waitIdle(t *testing.T, g *gateway.Gateway) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if g.Devices()[0].Status == gateway.DeviceIdle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the phone is still %s", g.Devices()[0].Status)
}

func TestHTTP(t *testing.T) {
	h := newHarness(t, func(o *gateway.Options) { o.ResumeGrace = 50 * time.Millisecond })
	web := h.srv.HTTP("secret-token")
	ts := httptest.NewServer(web)
	defer ts.Close()

	// Without the token, or with another, nothing is served.
	for _, auth := range []string{"", "Bearer nope", "Basic secret-token"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+HTTPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Authorization %q: status %d", auth, resp.StatusCode)
		}
	}

	// With it, a session works like one over stdio, and ends with the
	// client: the phone is free again once no newer session of the agent
	// continued its task (ResumeGrace).
	cs := httpClient(t, ts.URL, "secret-token", "codex")
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 14 {
		t.Fatalf("%d tools, %v", len(tools.Tools), err)
	}
	text := callText(t, cs, "phone_connect", map[string]any{"task": "Lunch"})
	if !strings.HasPrefix(text, "settled") {
		t.Fatalf("connect:\n%s", text)
	}
	if d := h.g.Devices()[0]; d.Status != gateway.DeviceWorking || !strings.HasPrefix(d.StatusLine, "Codex (remote) · ") {
		t.Fatalf("device %+v", d)
	}
	cs.Close()
	waitIdle(t, h.g)

	// Turning remote agents off ends the sessions it carries.
	cs2 := httpClient(t, ts.URL, "secret-token", "codex")
	defer cs2.Close()
	if text := callText(t, cs2, "phone_connect", map[string]any{"task": "Lunch"}); !strings.HasPrefix(text, "settled") {
		t.Fatalf("connect:\n%s", text)
	}
	web.CloseSessions()
	waitIdle(t, h.g)
}

// The first connection of an agent asks the person over HTTP too.
func TestHTTPNewAgentAsks(t *testing.T) {
	h := newHarness(t, func(o *gateway.Options) { o.TrustNewAgents = false; o.MaxBlock = 300 * time.Millisecond })
	ts := httptest.NewServer(h.srv.HTTP("tok"))
	defer ts.Close()
	cs := httpClient(t, ts.URL, "tok", "codex")
	defer cs.Close()
	text := callText(t, cs, "phone_connect", map[string]any{"task": "Lunch"})
	if !strings.HasPrefix(text, "approval_pending") || !strings.Contains(text, "rule: New agent") {
		t.Fatalf("first connection over HTTP:\n%s", text)
	}
}
