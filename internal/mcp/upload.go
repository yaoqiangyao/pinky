package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DIYgod/pinky/internal/gateway"
)

// phone_files upload hands Pinky a file for the phone's local folder, for
// agents that cannot write there themselves: on another computer, over
// HTTP. The bytes come in the call (content, base64), which suits small
// files and agents that call tools from code; a model would have to write
// out every byte. Over HTTP an upload without content returns a link
// instead, which the agent sends the file to with curl, so the file never
// passes through the model.

// maxContent bounds a file sent in content. Base64 makes it a third
// larger, and a message holds at most maxMessage.
const maxContent = 10 * 1000 * 1000

// maxMessage bounds one message from an agent: the SDK's limit for stdio,
// which HTTP takes too, where its own default is 4 MiB.
const maxMessage = sdk.DefaultMaxLineLength

// UploadPath is where upload links point, followed by the link's token.
const UploadPath = "/upload/"

// uploadTTL is how long an upload link works. It works once.
const uploadTTL = 10 * time.Minute

// linkBaseHeader carries the scheme and host an HTTP agent reached Pinky
// at to the tools, for upload links. HTTP.ServeHTTP sets it on every
// request, over what the agent sent.
const linkBaseHeader = "X-Pinky-Link-Base"

// uploadLink is an upload a link stands for: one file, for one phone,
// from one session.
type uploadLink struct {
	gs      *gateway.Session
	device  string
	path    string
	replace bool
	expires time.Time
}

// files serves phone_files.
func (s *Server) files(ctx context.Context, req *sdk.CallToolRequest, remote bool, in filesIn) *sdk.CallToolResult {
	fr := gateway.FilesRequest{
		Call: gateway.Call{Device: strings.TrimSpace(in.Device), Intent: strings.TrimSpace(in.Intent),
			ApprovalID: strings.TrimSpace(in.ApprovalID), Progress: progress(ctx, req)},
		Action: strings.ToLower(strings.TrimSpace(in.Action)), Paths: in.Paths, Replace: in.Replace}
	if fr.Action == "" && in.Content != nil {
		fr.Action = "upload"
	}
	if fr.Action != "upload" && (in.Content != nil || in.Replace) {
		return render(&gateway.Result{Error: gateway.ErrInvalidArgument, Message: `content and replace go with action "upload".`})
	}
	gs := s.session(req, remote)
	if fr.Action == "upload" {
		switch {
		case in.Content != nil:
			data, res := decodeContent(*in.Content, remote)
			if res != nil {
				return render(res)
			}
			fr.Body = bytes.NewReader(data)
		case remote:
			return s.newUploadLink(ctx, req, gs, fr)
		}
	}
	return render(gs.Files(ctx, fr))
}

// decodeContent decodes a file sent in content: base64, standard or URL
// alphabet, padded or not, across lines or not, or a data: URL of it.
func decodeContent(s string, remote bool) ([]byte, *gateway.Result) {
	bad := func(format string, args ...any) ([]byte, *gateway.Result) {
		return nil, &gateway.Result{Error: gateway.ErrInvalidArgument, Message: fmt.Sprintf(format, args...)}
	}
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "data:"); ok {
		head, data, ok := strings.Cut(rest, ",")
		if !ok || !strings.HasSuffix(head, ";base64") {
			return bad("content is a data: URL without base64; send the file's bytes in base64.")
		}
		s = data
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	if len(s) > base64.RawStdEncoding.EncodedLen(maxContent) {
		if remote {
			return bad("content holds more than %s. Leave content out for a link to send the file to, up to %s.",
				gateway.SizeText(maxContent), gateway.SizeText(gateway.MaxUpload))
		}
		return bad("content holds more than %s. Write the file into the folder phone_files names with your own file tools instead.",
			gateway.SizeText(maxContent))
	}
	enc := base64.RawStdEncoding
	if strings.ContainsAny(s, "-_") {
		enc = base64.RawURLEncoding
	}
	b, err := enc.DecodeString(s)
	if err != nil {
		return bad("content is not base64: %v. Nothing was saved.", err)
	}
	return b, nil
}

// newUploadLink checks an upload and returns a link to send its file to.
func (s *Server) newUploadLink(ctx context.Context, req *sdk.CallToolRequest, gs *gateway.Session, fr gateway.FilesRequest) *sdk.CallToolResult {
	fr.Check = true
	res := gs.Files(ctx, fr)
	if res.IsError() || res.Status != gateway.StatusSettled || len(res.Files) != 1 {
		return render(res)
	}
	name := res.Files[0].Path
	token := rand.Text()
	now := time.Now()
	s.mu.Lock()
	for t, u := range s.uploads {
		if now.After(u.expires) {
			delete(s.uploads, t)
		}
	}
	s.uploads[token] = &uploadLink{gs: gs, device: res.Device, path: name, replace: fr.Replace, expires: now.Add(uploadTTL)}
	s.mu.Unlock()

	url, where := UploadPath+token, " at the address you reach Pinky's /mcp at"
	if req.Extra != nil {
		if base := req.Extra.Header.Get(linkBaseHeader); base != "" {
			// Through a tunnel or proxy, this may be Pinky's own address.
			url, where = base+UploadPath+token, " (if this address does not reach Pinky from your machine, use the one you reach its /mcp at, with the same path)"
		}
	}
	return render(&gateway.Result{Status: gateway.StatusSettled, DeviceName: res.DeviceName, Message: fmt.Sprintf(
		"Send the file to this link%s within %s, once, as the request body (up to %s), from your own shell:\n"+
			"curl --upload-file <your file> '%s'\n"+
			"Pinky saves it as %s in its folder for the phone, on its own computer, and answers with how it went. "+
			"Then copy it to the phone with phone_files { action: \"push\", paths: [%q] }, which the person approves first.",
		where, approxMinutes(uploadTTL), gateway.SizeText(gateway.MaxUpload), url, name, name)})
}

func approxMinutes(d time.Duration) string {
	return fmt.Sprintf("%d minutes", int(d/time.Minute))
}

// takeUpload returns the upload a link's token stands for and forgets it,
// or nil for an unknown, used or expired one.
func (s *Server) takeUpload(token string) *uploadLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.uploads[token]
	delete(s.uploads, token)
	if u == nil || time.Now().After(u.expires) {
		return nil
	}
	return u
}

// forgetUploadsLocked drops the links of a session that ended.
func (s *Server) forgetUploadsLocked(gs *gateway.Session) {
	for t, u := range s.uploads {
		if u.gs == gs {
			delete(s.uploads, t)
		}
	}
}

// serveUpload takes a file sent to an upload link. The link's token stands
// for the agent token, so a shell command can send the file without it.
// The answer is the text phone_files would return.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	reply := func(code int, msg string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(code)
		fmt.Fprintln(w, msg)
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		w.Header().Set("Allow", "PUT, POST")
		reply(http.StatusMethodNotAllowed, "Send the file with PUT, as curl --upload-file does.")
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		reply(http.StatusUnsupportedMediaType, "Send the file itself as the request body, as curl --upload-file does, not in a form.")
		return
	}
	if r.ContentLength > gateway.MaxUpload {
		reply(http.StatusRequestEntityTooLarge, fmt.Sprintf("The file is larger than %s, more than Pinky takes. Nothing was saved.", gateway.SizeText(gateway.MaxUpload)))
		return
	}
	u := s.takeUpload(strings.TrimPrefix(r.URL.Path, UploadPath))
	if u == nil {
		reply(http.StatusNotFound, `This upload link is unknown, used or expired. Call phone_files { action: "upload" } again for a new one.`)
		return
	}
	// The session that asked for the link, or the newer one of its agent
	// that carries on with the phone.
	res := u.gs.Current().Files(r.Context(), gateway.FilesRequest{Call: gateway.Call{Device: u.device}, Action: "upload",
		Paths: []string{u.path}, Body: r.Body, Replace: u.replace})
	var b strings.Builder
	writeResult(&b, res, "")
	reply(uploadStatus(res), strings.TrimRight(b.String(), "\n"))
}

// uploadStatus is the HTTP status for an upload's result.
func uploadStatus(res *gateway.Result) int {
	switch {
	case res.Error == gateway.ErrInvalidArgument:
		return http.StatusBadRequest
	case res.Error == gateway.ErrScopeDenied, res.Status == gateway.StatusBlocked:
		return http.StatusForbidden
	case res.Error == gateway.ErrDeviceOffline:
		return http.StatusServiceUnavailable
	case res.IsError():
		return http.StatusInternalServerError
	case res.Status != gateway.StatusSettled:
		return http.StatusConflict
	}
	return http.StatusOK
}

// linkBase is the scheme and host an agent reached Pinky at, as a proxy in
// front of it says or else as the request does.
func linkBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "http" || p == "https" {
		scheme = p
	}
	host := r.Host
	if fh, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Host"), ","); strings.TrimSpace(fh) != "" {
		host = strings.TrimSpace(fh)
	}
	if host == "" || strings.ContainsAny(host, "/ '\"\\") {
		return ""
	}
	return scheme + "://" + host
}
