//go:build e2e

package main

// What the end-to-end tests of updates share: the gateway's version on its
// socket, quitting Pinky, and MyGo's automation socket.

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/api"
)

// gatewayVersion is the version of the gateway that answers on the
// socket, or "".
func gatewayVersion(d api.Dirs) string {
	conn, err := api.Dial(d)
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, reply, err := api.Greet(conn, api.Hello{Role: api.RoleControl})
	if err != nil {
		return ""
	}
	return reply.Version
}

func quitPinky(d api.Dirs) {
	ctl, err := api.DialControl(d)
	if err != nil {
		return
	}
	ctl.Call("quit", nil, nil)
	ctl.Close()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if gatewayVersion(d) == "" {
			return
		}
	}
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// newsTexts are the texts of the What's new card after an update to
// version, in each of Pinky's languages: an e2e build on macOS shares
// Pinky.app's defaults, and so the language picked in its Settings.
func newsTexts(version string) [][]string {
	var out [][]string
	for _, l := range []string{"en", "zh-Hans", "ja"} {
		m := locales.In(l)
		out = append(out, []string{m.UpdateNewsCardTitle(), m.UpdateNewsNow(version)})
	}
	return out
}

// waitLabels waits for the main window to show every text of one of the
// alternatives, and fails with what it shows when it does not.
func waitLabels(t *testing.T, c *automation, d time.Duration, alternatives [][]string) []string {
	t.Helper()
	var labels []string
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		labels = c.labels(t)
		for _, want := range alternatives {
			if containsAll(labels, want...) {
				return labels
			}
		}
	}
	t.Fatalf("the window does not show %q; it shows: %s", alternatives, strings.Join(labels, " · "))
	return nil
}

func containsAll(labels []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, l := range labels {
			if strings.Contains(l, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// automation talks to MyGo's automation socket.
type automation struct {
	conn net.Conn
	r    *bufio.Reader
	id   int
}

func dialAutomation(t *testing.T, sock string) *automation {
	t.Helper()
	var conn net.Conn
	waitFor(t, "the automation socket", 30*time.Second, func() bool {
		var err error
		conn, err = net.Dial("unix", sock)
		return err == nil
	})
	return &automation{conn: conn, r: bufio.NewReader(conn)}
}

func (c *automation) call(method string, params, result any) error {
	c.id++
	req := map[string]any{"id": c.id, "method": method}
	if params != nil {
		req["params"] = params
	}
	line, _ := json.Marshal(req)
	c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return err
	}
	answer, err := c.r.ReadBytes('\n')
	if err != nil {
		return err
	}
	var res struct {
		Result json.RawMessage `json:"result"`
		Error  *struct{ Code, Message string }
	}
	if err := json.Unmarshal(answer, &res); err != nil {
		return err
	}
	if res.Error != nil {
		return errors.New(res.Error.Code + ": " + res.Error.Message)
	}
	if result != nil {
		return json.Unmarshal(res.Result, result)
	}
	return nil
}

// labels reads the labels and values of the main window's elements.
func (c *automation) labels(t *testing.T) []string {
	t.Helper()
	var ws struct {
		Windows []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		}
	}
	if err := c.call("windows", nil, &ws); err != nil {
		t.Logf("windows: %v", err)
		return nil
	}
	var out []string
	for _, w := range ws.Windows {
		if w.Title != "Pinky" {
			continue
		}
		c.call("idle", map[string]any{"window": w.ID, "timeout": 5000}, nil)
		var tree struct {
			Nodes []struct {
				Label, Value string
			} `json:"nodes"`
		}
		if err := c.call("tree", map[string]any{"window": w.ID}, &tree); err != nil {
			t.Logf("tree: %v", err)
			continue
		}
		for _, n := range tree.Nodes {
			for _, s := range []string{n.Label, n.Value} {
				if s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}
