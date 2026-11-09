package mcp

import (
	"crypto/subtle"
	"net/http"
	"path"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPPath is where the Streamable HTTP endpoint is served.
const HTTPPath = "/mcp"

// httpSessionTimeout ends HTTP sessions that sent nothing for this long,
// and with them their leases; stdio sessions end when the agent exits.
const httpSessionTimeout = time.Hour

// HTTP serves MCP over Streamable HTTP (round three, decision 6), for
// agents that cannot start `pinky mcp`: on another machine, in a
// container, or a client that speaks only HTTP. Every request needs the
// agent token as a bearer token. Each MCP session is one gateway session,
// with the same first-connection approval and scopes as over stdio, under
// a name of its own (RemoteSuffix). The SDK refuses requests to a loopback
// address that name another host (DNS rebinding), and cross-origin
// browser requests are refused too. Upload links (UploadPath) take files
// for phone_files without the agent token: each link's own token is the
// key, for one file, once.
type HTTP struct {
	s       *Server
	srv     *sdk.Server
	handler http.Handler
	token   string
	sl      *sessionless // clients that send no session id (sessionless.go)
}

// HTTP returns the Streamable HTTP endpoint for the server's tools, at
// HTTPPath, accepting token.
func (s *Server) HTTP(token string) *HTTP {
	// A server of its own, so turning HTTP off can end exactly its
	// sessions, and its agents are named apart from local ones.
	srv := s.newSDK(true)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{SessionTimeout: httpSessionTimeout, MaxRequestBodyBytes: maxMessage})
	mux := http.NewServeMux()
	mux.Handle(HTTPPath, http.NewCrossOriginProtection().Handler(h))
	mux.Handle(UploadPath, http.NewCrossOriginProtection().Handler(http.HandlerFunc(s.serveUpload)))
	return &HTTP{s: s, srv: srv, handler: mux, token: token, sl: newSessionless()}
}

// ServeHTTP checks the bearer token, then serves MCP. Upload links need
// none.
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(path.Clean(r.URL.Path), UploadPath) {
		h.handler.ServeHTTP(w, r)
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pinky"`)
		http.Error(w, "Pinky needs the agent token: send Authorization: Bearer <token>.", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && r.Header.Get(sessionIDHeader) == "" {
		// No session, no stream to offer: 405 tells a client that keeps
		// no session id to go on with POST alone, where the SDK's 400
		// would read as a failure.
		w.Header().Set("Allow", "POST")
		http.Error(w, "Pinky offers a GET stream only within a session (Mcp-Session-Id); send requests with POST.", http.StatusMethodNotAllowed)
		return
	}
	if base := linkBase(r); base != "" {
		r.Header.Set(linkBaseHeader, base)
	} else {
		r.Header.Del(linkBaseHeader)
	}
	if r.Method == http.MethodPost {
		if sid := r.Header.Get(sessionIDHeader); sid != "" {
			h.sl.claim(sid)
		} else if h.serveWithoutID(w, r) {
			return
		}
	}
	h.handler.ServeHTTP(w, r)
}

func (h *HTTP) authorized(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || h.token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(h.token)) == 1
}

// CloseSessions ends every HTTP session, with their leases and approvals,
// as when the person turns remote agents off: at once, also the leases
// kept for an agent's next session.
func (h *HTTP) CloseSessions() {
	var all []*sdk.ServerSession
	for ss := range h.srv.Sessions() {
		all = append(all, ss)
	}
	for _, ss := range all {
		h.s.mu.Lock()
		gs := h.s.sessions[ss]
		h.s.mu.Unlock()
		if gs != nil {
			gs.CloseNow()
		}
		ss.Close()
	}
	h.s.g.EndKeptLeases()
	h.sl.reset()
}
