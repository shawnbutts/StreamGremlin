// Package mocktwitch is a stand-in for Twitch's OAuth server
// (id.twitch.tv), for tests and for trying the app without a real Twitch
// app. It follows the documented answers; where Twitch doesn't document one
// (a denied or expired code) it picks a plausible message, and the adapter
// must not depend on its exact wording.
//
// It also serves the API endpoints chat uses under /helix, and the
// EventSub WebSocket at /ws.
//
// Use it from tests through New, or standalone with cmd/mocktwitch, whose
// /activate page stands in for twitch.tv/activate and /chat for typing chat.
package mocktwitch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is a mock id.twitch.tv.
type Server struct {
	mu sync.Mutex

	// Settings; change them before or between calls.
	ClientIDs      map[string]bool // accepted Client IDs; empty accepts any
	CodeLifetime   time.Duration   // device code expiry (default 30 minutes)
	Interval       int             // polling interval answered, in seconds (default 1)
	AccessLifetime time.Duration   // access token lifetime (default 4 hours)
	down           bool            // answer 503 to everything; see SetDown
	// GrantScopes, when set, replaces what each sign-in is granted, to test
	// a streamer who unticks a permission.
	GrantScopes []string

	now     func() time.Time
	codes   map[string]*code // by device code
	byUser  map[string]*code // by user code
	access  map[string]*grant
	refresh map[string]*grant
	users   map[string]string // login -> user id
	revoked int
	base    string
	chat    chatState
	rw      rewardState
	pl      pollState
	ad      adState
}

type code struct {
	device, user string
	clientID     string
	scopes       []string
	expires      time.Time
	approvedAs   string
	denied, used bool
}

type grant struct {
	login, userID, clientID string
	scopes                  []string
	expires                 time.Time
	refreshToken            string
	accessTokens            int
}

// New returns a mock with defaults. base is its own URL, used in the
// verification links it hands out; set it with SetBase once listening.
func New() *Server {
	return &Server{
		ClientIDs: map[string]bool{}, CodeLifetime: 30 * time.Minute, Interval: 1, AccessLifetime: 4 * time.Hour,
		now: time.Now, codes: map[string]*code{}, byUser: map[string]*code{},
		access: map[string]*grant{}, refresh: map[string]*grant{}, users: map[string]string{},
	}
}

// SetBase sets the mock's own URL.
func (s *Server) SetBase(u string) { s.mu.Lock(); s.base = strings.TrimRight(u, "/"); s.mu.Unlock() }

// SetNow replaces the clock, for tests.
func (s *Server) SetNow(f func() time.Time) { s.mu.Lock(); s.now = f; s.mu.Unlock() }

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func userCode() string {
	const letters = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// UserID returns the id for login, assigning one the first time.
func (s *Server) UserID(login string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userIDLocked(login)
}

func (s *Server) userIDLocked(login string) string {
	login = strings.ToLower(login)
	if id, ok := s.users[login]; ok {
		return id
	}
	id := strconv.Itoa(100000 + len(s.users)*7919)
	s.users[login] = id
	return id
}

// Approve signs userCode in as login, like entering it at twitch.tv/activate.
func (s *Server) Approve(userCode, login string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byUser[strings.ToUpper(strings.TrimSpace(userCode))]
	if !ok || s.now().After(c.expires) {
		return fmt.Errorf("mocktwitch: no pending code %q", userCode)
	}
	c.approvedAs = strings.ToLower(login)
	return nil
}

// Deny refuses userCode.
func (s *Server) Deny(userCode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byUser[strings.ToUpper(userCode)]
	if !ok {
		return fmt.Errorf("mocktwitch: no pending code %q", userCode)
	}
	c.denied = true
	return nil
}

// Pending lists user codes waiting to be entered.
func (s *Server) Pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for u, c := range s.byUser {
		if !c.used && !c.denied && c.approvedAs == "" && s.now().Before(c.expires) {
			out = append(out, u)
		}
	}
	return out
}

// ExpireAccessTokens makes every access token for login expire now, as
// after four hours. Refresh tokens still work.
func (s *Server) ExpireAccessTokens(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.access {
		if g.login == strings.ToLower(login) {
			g.expires = s.now().Add(-time.Second)
		}
	}
}

// Disconnect ends every token for login, as when the user removes the app
// under Twitch's Connections settings.
func (s *Server) Disconnect(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	login = strings.ToLower(login)
	for t, g := range s.access {
		if g.login == login {
			delete(s.access, t)
		}
	}
	for t, g := range s.refresh {
		if g.login == login {
			delete(s.refresh, t)
		}
	}
}

// Tokens lists every access and refresh token ever issued to login that is
// still live, so tests can check none leaks into files or logs.
func (s *Server) Tokens(login string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for t, g := range s.access {
		if g.login == strings.ToLower(login) {
			out = append(out, t, g.refreshToken)
		}
	}
	return out
}

// Revoked counts successful revocations.
func (s *Server) Revoked() int { s.mu.Lock(); defer s.mu.Unlock(); return s.revoked }

// ValidTokens counts live access tokens for login.
func (s *Server) ValidTokens(login string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.access {
		if g.login == strings.ToLower(login) && s.now().Before(g.expires) {
			n++
		}
	}
	return n
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "message": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// SetDown makes the mock answer 503 to everything but /activate and /chat,
// as if Twitch were down. Safe while requests are being answered.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// ServeHTTP answers the documented OAuth endpoints and the /activate page.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	down := s.down
	s.mu.Unlock()
	if down && r.URL.Path != "/activate" && r.URL.Path != "/chat" {
		writeErr(w, http.StatusServiceUnavailable, "Service Unavailable")
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/oauth2/device":
		s.device(w, r)
	case r.Method == "POST" && r.URL.Path == "/oauth2/token":
		s.token(w, r)
	case r.Method == "GET" && r.URL.Path == "/oauth2/validate":
		s.validate(w, r)
	case r.Method == "POST" && r.URL.Path == "/oauth2/revoke":
		s.revoke(w, r)
	case r.URL.Path == "/activate":
		s.activate(w, r)
	case strings.HasPrefix(r.URL.Path, "/helix/"):
		s.helix(w, r)
	case r.URL.Path == "/ws":
		s.ws(w, r)
	case r.URL.Path == "/chat":
		s.chatForm(w, r)
	default:
		writeErr(w, http.StatusNotFound, "Not Found")
	}
}

func (s *Server) clientOK(id string) bool {
	return id != "" && (len(s.ClientIDs) == 0 || s.ClientIDs[id])
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PostForm.Get("client_secret") != "" {
		writeErr(w, 400, "mocktwitch: a public client must not send client_secret")
		return
	}
	if !s.clientOK(r.PostForm.Get("client_id")) {
		writeErr(w, 400, "invalid client")
		return
	}
	c := &code{device: random(20), user: userCode(), clientID: r.PostForm.Get("client_id"),
		scopes: strings.Fields(r.PostForm.Get("scopes")), expires: s.now().Add(s.CodeLifetime)}
	s.codes[c.device] = c
	s.byUser[c.user] = c
	writeJSON(w, map[string]any{
		"device_code": c.device, "user_code": c.user, "expires_in": int(s.CodeLifetime.Seconds()),
		"interval": s.Interval, "verification_uri": s.base + "/activate?public=true&device-code=" + c.user,
	})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := r.PostForm
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Get("client_secret") != "" {
		writeErr(w, 400, "mocktwitch: a public client must not send client_secret")
		return
	}
	if !s.clientOK(f.Get("client_id")) {
		writeErr(w, 400, "invalid client")
		return
	}
	switch f.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		c, ok := s.codes[f.Get("device_code")]
		switch {
		case !ok || c.used || c.clientID != f.Get("client_id") || s.now().After(c.expires):
			writeErr(w, 400, "invalid device code")
		case c.denied:
			writeErr(w, 400, "authorization denied") // undocumented by Twitch
		case c.approvedAs == "":
			writeErr(w, 400, "authorization_pending")
		default:
			c.used = true
			scopes := c.scopes
			if s.GrantScopes != nil {
				scopes = s.GrantScopes
			}
			writeJSON(w, s.issueLocked(&grant{login: c.approvedAs, userID: s.userIDLocked(c.approvedAs), clientID: c.clientID, scopes: scopes}))
		}
	case "refresh_token":
		g, ok := s.refresh[f.Get("refresh_token")]
		if !ok || g.clientID != f.Get("client_id") {
			writeErr(w, 400, "Invalid refresh token")
			return
		}
		delete(s.refresh, f.Get("refresh_token")) // single use
		if g.accessTokens >= 50 {
			writeErr(w, 400, "Invalid refresh token")
			return
		}
		writeJSON(w, s.issueLocked(&grant{login: g.login, userID: g.userID, clientID: g.clientID, scopes: g.scopes, accessTokens: g.accessTokens}))
	default:
		writeErr(w, 400, "unsupported grant type")
	}
}

func (s *Server) issueLocked(g *grant) map[string]any {
	access := random(15)
	g.refreshToken = random(25)
	g.expires = s.now().Add(s.AccessLifetime)
	g.accessTokens++
	s.access[access] = g
	s.refresh[g.refreshToken] = g
	return map[string]any{
		"access_token": access, "refresh_token": g.refreshToken,
		"expires_in": int(s.AccessLifetime.Seconds()), "scope": g.scopes, "token_type": "bearer",
	}
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "OAuth ")
	if !ok {
		tok, _ = strings.CutPrefix(h, "Bearer ")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.access[tok]
	if !ok || !s.now().Before(g.expires) {
		writeErr(w, 401, "invalid access token")
		return
	}
	writeJSON(w, map[string]any{
		"client_id": g.clientID, "login": g.login, "user_id": g.userID, "scopes": g.scopes,
		"expires_in": int(g.expires.Sub(s.now()).Seconds()),
	})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clientOK(r.PostForm.Get("client_id")) {
		writeErr(w, 404, "client does not exist")
		return
	}
	g, ok := s.access[r.PostForm.Get("token")]
	if !ok {
		writeErr(w, 400, "Invalid token")
		return
	}
	delete(s.access, r.PostForm.Get("token"))
	delete(s.refresh, g.refreshToken)
	s.revoked++
	w.WriteHeader(http.StatusOK)
}

var activatePage = template.Must(template.New("a").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>mocktwitch: activate</title>
<style>body{font:16px system-ui;max-width:32rem;margin:3rem auto;padding:0 1rem}input,button{font:inherit;padding:.4rem .6rem}
.note{background:#fff3cd;padding:.6rem .8rem;border-radius:6px}</style></head><body>
<p class="note">This is <strong>mocktwitch</strong>, a test stand-in for twitch.tv. Nothing here signs in to real Twitch.</p>
<h1>Activate a device</h1>
{{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}
<form method="post" action="/activate">
<p><label>Code<br><input name="code" value="{{.Code}}" autocomplete="off"></label></p>
<p><label>Sign in as (any Twitch login)<br><input name="login" value="{{.Login}}"></label></p>
<p><button name="do" value="approve">Authorize</button> <button name="do" value="deny">Cancel</button></p>
</form></body></html>`))

// activate stands in for twitch.tv/activate in standalone use.
func (s *Server) activate(w http.ResponseWriter, r *http.Request) {
	data := struct{ Code, Login, Message string }{Code: r.URL.Query().Get("device-code"), Login: "teststreamer"}
	if r.Method == "POST" {
		_ = r.ParseForm()
		data.Code, data.Login = r.PostForm.Get("code"), r.PostForm.Get("login")
		var err error
		if r.PostForm.Get("do") == "deny" {
			err = s.Deny(data.Code)
			data.Message = "Cancelled. Return to StreamGremlin."
		} else {
			err = s.Approve(data.Code, data.Login)
			data.Message = "Signed in as " + data.Login + ". Return to StreamGremlin."
		}
		if err != nil {
			data.Message = err.Error()
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = activatePage.Execute(w, data)
}
