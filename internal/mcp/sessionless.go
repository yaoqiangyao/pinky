package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Clients that send no session id. Pinky is a stateful Streamable HTTP
// server: initialize returns an Mcp-Session-Id, which the requests after
// it carry. Some clients cannot keep it (Grok Bot's connector sends fixed
// headers only, GitHub DIYgod/pinky#10): they initialize, then send every
// request without an id, which the SDK would take as the start of a new
// session and refuse ("invalid during session initialization").
//
// For such requests Pinky keeps a session of its own per client, told
// apart by User-Agent, and sends them into it, as a client that kept the
// id would. It initializes that session with what the client's own
// initialize asked (its name, so the person sees and approves the agent by
// name), and uses it until it times out like any session. Sessions that
// clients made themselves are never used for other requests: requests
// that carry an id go to that session, untouched. All of it needs the
// agent token, like every request.

const sessionIDHeader = "Mcp-Session-Id"

// defaultProtocol is the version a session Pinky opens for a client asks
// for when the client's own initialize is not known.
const defaultProtocol = "2025-06-18"

// sessionless keeps the sessions of clients that send no session id.
type sessionless struct {
	mu      sync.Mutex
	sticky  map[string]string       // User-Agent → the session Pinky keeps for it
	inits   map[string][]initRecord // User-Agent → its recent initializes without an id
	claimed map[string]time.Time    // session ids that clients sent, when last
}

// initRecord is an initialize that came without a session id.
type initRecord struct {
	session string
	params  json.RawMessage
	at      time.Time
}

// maxInits bounds the initializes kept per User-Agent.
const maxInits = 8

func newSessionless() *sessionless {
	return &sessionless{sticky: map[string]string{}, inits: map[string][]initRecord{}, claimed: map[string]time.Time{}}
}

func (sl *sessionless) reset() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	clear(sl.sticky)
	clear(sl.inits)
	clear(sl.claimed)
}

// serveWithoutID serves a POST that carries no session id. It reports
// whether it answered the request; otherwise the caller passes r on, with
// the session id set when Pinky keeps a session for the client.
func (h *HTTP) serveWithoutID(w http.ResponseWriter, r *http.Request) bool {
	body, ok := peekBody(r)
	if !ok {
		return false // too large or unreadable: the SDK answers
	}
	method, params := jsonrpcMethod(body)
	agent := r.Header.Get("User-Agent")
	switch method {
	case "initialize":
		// A session of the client's own; remember what it asked for.
		h.handler.ServeHTTP(w, r)
		if sid := w.Header().Get(sessionIDHeader); sid != "" {
			h.sl.noteInit(agent, sid, params)
		}
		return true
	case "":
		return false // not JSON-RPC: the SDK answers
	}
	sid := h.keptSession(r, agent)
	if sid == "" {
		return false
	}
	if method == "notifications/initialized" {
		// Pinky initialized the session it keeps itself.
		w.WriteHeader(http.StatusAccepted)
		return true
	}
	r.Header.Set(sessionIDHeader, sid)
	return false
}

// keptSession returns the session Pinky keeps for the client with this
// User-Agent, opening one when it has none, or "" when it cannot.
func (h *HTTP) keptSession(r *http.Request, agent string) string {
	sl := h.sl
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sid := sl.sticky[agent]; sid != "" && h.alive(sid) {
		return sid
	}
	params := sl.initParamsLocked(agent)
	if params == nil {
		version := r.Header.Get("Mcp-Protocol-Version")
		if version == "" || version >= "2026-07-28" {
			version = defaultProtocol
		}
		params, _ = json.Marshal(map[string]any{"protocolVersion": version, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "agent", "version": ""}})
	}
	sid := h.openSession(r, params)
	if sid != "" {
		sl.sticky[agent] = sid
	}
	return sid
}

// openSession initializes a session for a client that will not keep its
// id, with the client's own initialize parameters, and returns its id.
func (h *HTTP) openSession(r *http.Request, params json.RawMessage) string {
	send := func(sid string, msg map[string]any) http.Header {
		b, _ := json.Marshal(msg)
		req := r.Clone(r.Context())
		req.Method = http.MethodPost
		req.Body = io.NopCloser(bytes.NewReader(b))
		req.ContentLength = int64(len(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Del(sessionIDHeader)
		if sid != "" {
			req.Header.Set(sessionIDHeader, sid)
		}
		rec := &headerRecorder{header: http.Header{}}
		h.handler.ServeHTTP(rec, req)
		return rec.header
	}
	sid := send("", map[string]any{"jsonrpc": "2.0", "id": "pinky-initialize", "method": "initialize", "params": params}).Get(sessionIDHeader)
	if sid != "" {
		send(sid, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	}
	return sid
}

// alive reports whether a session is still open.
func (h *HTTP) alive(sid string) bool {
	for ss := range h.srv.Sessions() {
		if ss.ID() == sid {
			return true
		}
	}
	return false
}

// claim notes that a client sent a session's id: it keeps its sessions,
// and its initializes say nothing about the clients that do not.
func (sl *sessionless) claim(sid string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	now := time.Now()
	if _, ok := sl.claimed[sid]; !ok {
		for id, at := range sl.claimed {
			if now.Sub(at) > httpSessionTimeout {
				delete(sl.claimed, id)
			}
		}
	}
	sl.claimed[sid] = now
}

func (sl *sessionless) noteInit(agent, sid string, params json.RawMessage) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	now := time.Now()
	recs := slices.DeleteFunc(sl.inits[agent], func(ir initRecord) bool { return now.Sub(ir.at) > httpSessionTimeout })
	recs = append(recs, initRecord{session: sid, params: params, at: now})
	if len(recs) > maxInits {
		recs = recs[len(recs)-maxInits:]
	}
	sl.inits[agent] = recs
}

// initParamsLocked returns the parameters of the newest initialize from
// this User-Agent whose session nobody sent an id for: the client that
// does not keep ids.
func (sl *sessionless) initParamsLocked(agent string) json.RawMessage {
	recs := sl.inits[agent]
	for i := len(recs) - 1; i >= 0; i-- {
		if _, ok := sl.claimed[recs[i].session]; !ok && len(recs[i].params) > 0 {
			return recs[i].params
		}
	}
	return nil
}

// peekBody reads a request's body and puts it back.
func peekBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxMessage+1))
	rest := r.Body
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(b), rest), rest}
	if err != nil || len(b) > maxMessage {
		return nil, false
	}
	return b, true
}

// jsonrpcMethod returns the method of a JSON-RPC message, and its params
// for initialize. In a batch, initialize wins, then the first method.
func jsonrpcMethod(body []byte) (string, json.RawMessage) {
	type message struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	trimmed := bytes.TrimSpace(body)
	var msgs []message
	if strings.HasPrefix(string(trimmed), "[") {
		if json.Unmarshal(trimmed, &msgs) != nil {
			return "", nil
		}
	} else {
		var m message
		if json.Unmarshal(trimmed, &m) != nil {
			return "", nil
		}
		msgs = []message{m}
	}
	for _, m := range msgs {
		if m.Method == "initialize" {
			return m.Method, m.Params
		}
	}
	for _, m := range msgs {
		if m.Method != "" {
			return m.Method, nil
		}
	}
	return "", nil
}

// headerRecorder takes a response Pinky sends itself, keeping the headers.
type headerRecorder struct {
	header http.Header
	code   int
}

func (h *headerRecorder) Header() http.Header         { return h.header }
func (h *headerRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (h *headerRecorder) WriteHeader(code int)        { h.code = code }
func (h *headerRecorder) Flush()                      {}
