package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
)

func withData(t *testing.T) func(*gateway.Options) {
	return func(o *gateway.Options) { o.DataDir = t.TempDir() }
}

func TestDecodeContent(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\n\x00\xff\xfe binary ~~~ ???")
	std := base64.StdEncoding.EncodeToString(data)
	var wrapped strings.Builder
	for i := 0; i < len(std); i += 8 {
		wrapped.WriteString(std[i:min(i+8, len(std))] + "\r\n")
	}
	for name, in := range map[string]string{
		"standard":  std,
		"unpadded":  base64.RawStdEncoding.EncodeToString(data),
		"url":       base64.URLEncoding.EncodeToString(data),
		"url raw":   base64.RawURLEncoding.EncodeToString(data),
		"wrapped":   wrapped.String(),
		"data: URL": "data:image/png;base64," + std,
		"spaces":    "  " + std + "\n",
	} {
		got, res := decodeContent(in, false)
		if res != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: %q, %+v", name, got, res)
		}
	}
	if got, res := decodeContent("", false); res != nil || len(got) != 0 {
		t.Errorf("empty: %q, %+v", got, res)
	}
	for name, in := range map[string]string{
		"not base64":      "this is not base64!",
		"data: URL, text": "data:text/plain,hello",
		"bad tail":        std[:len(std)-3] + "*",
	} {
		if _, res := decodeContent(in, false); res == nil || res.Error != gateway.ErrInvalidArgument {
			t.Errorf("%s: accepted", name)
		}
	}
	// The limit counts the bytes, before decoding anything.
	big := strings.Repeat("A", base64.RawStdEncoding.EncodedLen(maxContent)+4)
	_, res := decodeContent(big, true)
	if res == nil || !strings.Contains(res.Message, "more than 10 MB") || !strings.Contains(res.Message, "Leave content out") {
		t.Errorf("too large over HTTP: %+v", res)
	}
	_, res = decodeContent(big, false)
	if res == nil || !strings.Contains(res.Message, "your own file tools") {
		t.Errorf("too large here: %+v", res)
	}
	if got, res := decodeContent(strings.Repeat("A", base64.RawStdEncoding.EncodedLen(maxContent)), false); res != nil || len(got) != maxContent {
		t.Errorf("at the limit: %d bytes, %+v", len(got), res)
	}
}

func TestToolUploadSchema(t *testing.T) {
	h := newHarness(t)
	c := h.connect(t, "claude-code")
	res, err := c.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "phone_files" {
			continue
		}
		schema, _ := json.Marshal(tool.InputSchema)
		for _, s := range []string{`"upload"`, `"content"`, `"replace"`} {
			if !strings.Contains(string(schema), s) {
				t.Fatalf("phone_files schema misses %s: %s", s, schema)
			}
		}
		var parsed struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		json.Unmarshal(schema, &parsed)
		if typ := parsed.Properties["content"]["type"]; typ != "string" {
			t.Fatalf("content's type %v", typ)
		}
		if !strings.Contains(tool.Description, "upload") {
			t.Fatalf("description: %s", tool.Description)
		}
		return
	}
	t.Fatal("no phone_files")
}

// A local agent sends a file in content, and pushes it.
func TestUploadContent(t *testing.T) {
	h := newHarness(t, withData(t))
	c := h.connect(t, "claude-code")
	c.call(t, "phone_connect", map[string]any{"task": "Send a picture"})
	dir := h.g.FilesDir("pixel")
	png := []byte("\x89PNG\r\n\x1a\nnot really")
	b64 := base64.StdEncoding.EncodeToString(png)

	text, isErr := c.call(t, "phone_files", map[string]any{"action": "upload", "paths": []string{"Pictures/cat.png"}, "content": b64})
	if isErr || !strings.HasPrefix(text, "settled") || !strings.Contains(text, "Saved Pictures/cat.png (18 B)") ||
		!strings.Contains(text, "folder: "+dir) || !strings.Contains(text, "Pictures/cat.png · 18 B · ") {
		t.Fatalf("upload:\n%s", text)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "Pictures", "cat.png")); err != nil || !bytes.Equal(b, png) {
		t.Fatalf("saved %q, %v", b, err)
	}
	// Content alone means upload.
	if text, isErr = c.call(t, "phone_files", map[string]any{"paths": []string{"note.txt"}, "content": base64.StdEncoding.EncodeToString([]byte("hi"))}); isErr {
		t.Fatalf("upload by content alone:\n%s", text)
	}
	for _, args := range []map[string]any{
		{"action": "push", "content": b64},
		{"action": "list", "replace": true},
		{"action": "upload", "paths": []string{"x.png"}, "content": "not base64!"},
		{"action": "upload", "paths": []string{"../x.png"}, "content": b64},
		{"action": "upload", "paths": []string{"Pictures/cat.png"}, "content": base64.StdEncoding.EncodeToString([]byte("other"))},
		{"action": "upload", "paths": []string{"y.png"}}, // no content, and no link over stdio
	} {
		if text, isErr := c.call(t, "phone_files", args); !isErr || !strings.HasPrefix(text, "error: invalid_argument") {
			t.Errorf("%v:\n%s", args, text)
		}
	}
	if text, _ := c.call(t, "phone_files", map[string]any{"action": "upload", "paths": []string{"y.png"}}); !strings.Contains(text, dir) {
		t.Errorf("no content over stdio, no folder named:\n%s", text)
	}

	// The push asks, and copies it.
	done := make(chan string, 1)
	go func() {
		text, _ := c.call(t, "phone_files", map[string]any{"action": "push", "paths": []string{"Pictures/cat.png"}})
		done <- text
	}()
	approve(t, h.g, "Files")
	if text := <-done; !strings.HasPrefix(text, "settled") || !strings.Contains(text, "Copied 1 file to the phone") {
		t.Fatalf("push:\n%s", text)
	}
	if b, ok := h.dev.File("Pictures/cat.png"); !ok || !bytes.Equal(b, png) {
		t.Fatalf("on the phone: %q %v", b, ok)
	}
}

// approve allows the open approval of a rule.
func approve(t *testing.T, g *gateway.Gateway, rule string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, n := range g.Needs() {
			if n.Rule == rule {
				if err := g.Decide(n.ID, gateway.AllowOnce, ""); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
	t.Fatalf("no %s approval", rule)
}

var linkRe = regexp.MustCompile(`curl --upload-file <your file> '([^']+)'`)

func uploadLinkOf(t *testing.T, text string) string {
	t.Helper()
	m := linkRe.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no upload link in:\n%s", text)
	}
	return m[1]
}

func put(t *testing.T, url, contentType string, body []byte) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

// A remote agent asks for a link and sends the file to it without the
// agent token, once.
func TestUploadLink(t *testing.T) {
	h := newHarness(t, withData(t), func(o *gateway.Options) { o.ResumeGrace = 50 * time.Millisecond })
	web := h.srv.HTTP("tok")
	ts := httptest.NewServer(web)
	defer ts.Close()
	cs := httpClient(t, ts.URL, "tok", "codex")
	defer cs.Close()
	callText(t, cs, "phone_connect", map[string]any{"task": "Send a picture"})
	dir := h.g.FilesDir("pixel")

	text := callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{`Pictures\cat.png`}})
	link := uploadLinkOf(t, text)
	if !strings.HasPrefix(link, ts.URL+UploadPath) || !strings.HasPrefix(text, "settled") || !strings.Contains(text, `paths: ["Pictures/cat.png"]`) {
		t.Fatalf("link %q in:\n%s", link, text)
	}
	if _, err := os.Stat(filepath.Join(dir, "Pictures")); !os.IsNotExist(err) {
		t.Fatalf("asking for a link made the folder: %v", err)
	}

	// Not a GET, not a form: the link still works afterwards.
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	if code, _ := put(t, link, "multipart/form-data; boundary=x", []byte("--x--")); code != http.StatusUnsupportedMediaType {
		t.Fatalf("form: %d", code)
	}

	img := bytes.Repeat([]byte{0xff, 0xd8, 0x00, 0x42}, 300_000) // 1.2 MB
	code, body := put(t, link, "image/png", img)
	if code != http.StatusOK || !strings.HasPrefix(body, "settled") || !strings.Contains(body, "Saved Pictures/cat.png (1.2 MB)") {
		t.Fatalf("PUT: %d\n%s", code, body)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "Pictures", "cat.png")); err != nil || !bytes.Equal(b, img) {
		t.Fatalf("saved %d bytes, %v", len(b), err)
	}
	// Once only.
	if code, body := put(t, link, "", img); code != http.StatusNotFound || !strings.Contains(body, "unknown, used or expired") {
		t.Fatalf("second PUT: %d\n%s", code, body)
	}

	// The name is taken now: no link without replace; with it, the file
	// is replaced.
	text = callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"Pictures/cat.png"}})
	if !strings.HasPrefix(text, "error: invalid_argument") || !strings.Contains(text, "replace: true") {
		t.Fatalf("link for a taken name:\n%s", text)
	}
	link = uploadLinkOf(t, callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"Pictures/cat.png"}, "replace": true}))
	if code, body := put(t, link, "", []byte("small")); code != http.StatusOK || !strings.Contains(body, "Replaced Pictures/cat.png (5 B)") {
		t.Fatalf("replace: %d\n%s", code, body)
	}

	// An expired link takes nothing.
	link = uploadLinkOf(t, callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"late.png"}}))
	h.srv.mu.Lock()
	for _, u := range h.srv.uploads {
		u.expires = time.Now().Add(-time.Second)
	}
	h.srv.mu.Unlock()
	if code, _ := put(t, link, "", []byte("late")); code != http.StatusNotFound {
		t.Fatalf("expired link: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "late.png")); !os.IsNotExist(err) {
		t.Fatalf("an expired link saved: %v", err)
	}

	// Too large, by what the request says: refused before anything.
	link = uploadLinkOf(t, callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"huge.bin"}}))
	req := httptest.NewRequest(http.MethodPut, link, strings.NewReader("x"))
	req.ContentLength = gateway.MaxUpload + 1
	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d %s", rec.Code, rec.Body.String())
	}

	// A path that only starts like a link still needs the token.
	for _, p := range []string{"/upload/../mcp", "/upload/%2e%2e/mcp"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		web.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK || rec.Code == http.StatusAccepted {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}

	// A session that ends takes its links with it.
	link = uploadLinkOf(t, callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"gone.png"}}))
	cs.Close()
	waitIdle(t, h.g)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		h.srv.mu.Lock()
		n := len(h.srv.uploads)
		h.srv.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d links left after the session ended", n)
		}
	}
	if code, _ := put(t, link, "", []byte("gone")); code != http.StatusNotFound {
		t.Fatalf("link of an ended session: %d", code)
	}
}

// Content works over HTTP too, past the SDK's own 4 MiB limit for a
// request.
func TestUploadContentOverHTTP(t *testing.T) {
	h := newHarness(t, withData(t))
	ts := httptest.NewServer(h.srv.HTTP("tok"))
	defer ts.Close()
	cs := httpClient(t, ts.URL, "tok", "codex")
	defer cs.Close()
	callText(t, cs, "phone_connect", map[string]any{"task": "Send a picture"})
	data := bytes.Repeat([]byte("0123456789"), 600_000) // 6 MB
	text := callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"big.bin"},
		"content": base64.StdEncoding.EncodeToString(data)})
	if !strings.HasPrefix(text, "settled") || !strings.Contains(text, "Saved big.bin (6 MB)") {
		t.Fatalf("upload:\n%s", text)
	}
	if b, err := os.ReadFile(filepath.Join(h.g.FilesDir("pixel"), "big.bin")); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("saved %d bytes, %v", len(b), err)
	}
}

// Read-only and watch-only agents get no link, and neither does an
// iPhone.
func TestUploadLinkRules(t *testing.T) {
	h := newHarness(t, withData(t))
	ts := httptest.NewServer(h.srv.HTTP("tok"))
	defer ts.Close()
	cs := httpClient(t, ts.URL, "tok", "codex")
	defer cs.Close()
	callText(t, cs, "phone_connect", map[string]any{"task": "Send a picture"})
	h.g.SetAgentScope("Codex (remote)", gateway.Scope{Mode: gateway.ScopeReadOnly})
	if text := callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"cat.png"}}); !strings.HasPrefix(text, "error: scope_denied") {
		t.Fatalf("read-only agent:\n%s", text)
	}
	if text := callText(t, cs, "phone_files", map[string]any{"action": "upload", "paths": []string{"cat.png"}, "content": "bWVvdw=="}); !strings.HasPrefix(text, "error: scope_denied") {
		t.Fatalf("read-only agent with content:\n%s", text)
	}
	h.srv.mu.Lock()
	n := len(h.srv.uploads)
	h.srv.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d links for a read-only agent", n)
	}
}

func TestLinkBase(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://192.168.1.5:7465/mcp", nil)
	if got := linkBase(r); got != "http://192.168.1.5:7465" {
		t.Fatalf("plain: %q", got)
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "mac.tail1234.ts.net, 127.0.0.1:7465")
	if got := linkBase(r); got != "https://mac.tail1234.ts.net" {
		t.Fatalf("behind a proxy: %q", got)
	}
	r.Header.Set("X-Forwarded-Host", "evil.example/'; rm -rf ~")
	if got := linkBase(r); got != "" {
		t.Fatalf("odd host: %q", got)
	}
	// What the agent sends in the header is not what the tools see.
	h := newHarness(t, withData(t))
	web := h.srv.HTTP("tok")
	var seen string
	web.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Get(linkBaseHeader) })
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7465/mcp", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set(linkBaseHeader, "http://elsewhere")
	web.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "http://127.0.0.1:7465" {
		t.Fatalf("link base %q", seen)
	}
}
