// Package mockyoutube is a stand-in for Google's sign-in and the parts of
// YouTube's Data API StreamGremlin uses, for tests and to try StreamGremlin
// without Google (M6, D-096). It follows Google's documented shapes: the
// authorization page redirects to the app's loopback address with a code;
// /token swaps codes (checking PKCE) and refresh tokens; /revoke ends a
// sign-in; /youtube/v3/channels answers mine=true; /youtube/v3/liveBroadcasts
// lists the live broadcasts; and liveChatMessages.streamList streams chat
// over gRPC (live.go). Use Serve, which turns on HTTP/2 for gRPC.
//
// The authorization page lists the channels to sign in as. A test, acting
// as the browser, can add mock_channel=ID (or mock_deny=1) to the link to
// skip the page.
package mockyoutube

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Channel is a YouTube channel someone can sign in as.
type Channel struct {
	ID     string
	Title  string
	Handle string // customUrl, such as "@streamer"
	// NoChannel makes a Google account without a YouTube channel.
	NoChannel bool
}

type grant struct {
	channel   string
	clientID  string
	redirect  string
	challenge string
	scope     string
}

type token struct {
	channel  string
	clientID string
	scope    string
	expires  time.Time
}

// Server is the stand-in. The zero value isn't usable; use New.
type Server struct {
	mu       sync.Mutex
	channels []Channel
	codes    map[string]grant
	access   map[string]token
	refresh  map[string]token
	secret   string // the client secret it insists on, if any
	ttl      time.Duration
	quota    bool // answers quotaExceeded
	calls    map[string]int
	revoked  []string
	now      func() time.Time

	// Live broadcasts and chat (live.go).
	seq        int
	broadcasts []*Broadcast
	chats      map[string]*chat
	drop, open int
	lastToken  string
	sent       []SentMessage
	banned     map[string]bool
	bansByID   map[string]ban
	mods       map[string]moderator
	deleted    []string
	grpc       interface {
		ServeHTTP(http.ResponseWriter, *http.Request)
	}
}

// New returns a server with two channels: "Streamer" (UCstreamer) and
// "Bot" (UCbot), plus a Google account with no channel (nochannel).
func New() *Server {
	s := &Server{
		channels: []Channel{
			{ID: "UCstreamer", Title: "Streamer", Handle: "@streamer"},
			{ID: "UCbot", Title: "Bot", Handle: "@streamerbot"},
			{ID: "nochannel", Title: "No channel", NoChannel: true},
		},
		codes: map[string]grant{}, access: map[string]token{}, refresh: map[string]token{},
		ttl: time.Hour, calls: map[string]int{}, now: time.Now, chats: map[string]*chat{}, banned: map[string]bool{},
		bansByID: map[string]ban{}, mods: map[string]moderator{},
	}
	s.grpc = s.grpcServer()
	return s
}

// RequireSecret makes /token refuse requests without this client secret.
func (s *Server) RequireSecret(secret string) { s.mu.Lock(); s.secret = secret; s.mu.Unlock() }

// SetTokenLifetime changes how long access tokens last (Google's: an hour).
func (s *Server) SetTokenLifetime(d time.Duration) { s.mu.Lock(); s.ttl = d; s.mu.Unlock() }

// QuotaExceeded makes the Data API answer 403 quotaExceeded, as Google does
// when a project's daily quota is spent.
func (s *Server) QuotaExceeded(on bool) { s.mu.Lock(); s.quota = on; s.mu.Unlock() }

// EndSignIns forgets every refresh token, as when the user removes the
// app's access or Google ends a Testing project's sign-ins after 7 days.
func (s *Server) EndSignIns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.refresh)
	clear(s.access)
}

// Calls says how many times a Data API resource was asked for.
func (s *Server) Calls(resource string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[resource]
}

// Revoked lists the tokens revoked so far.
func (s *Server) Revoked() []string { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.revoked) }

// ActiveTokens counts access tokens that still work.
func (s *Server) ActiveTokens() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.access) }

func random() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ServeHTTP serves the stand-in.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		s.grpc.ServeHTTP(w, r) // the chat stream (live.go)
		return
	}
	switch r.URL.Path {
	case "/youtube/v3/liveBroadcasts":
		s.liveBroadcasts(w, r)
	case "/youtube/v3/liveChat/messages":
		if r.Method == http.MethodDelete {
			s.deleteMessage(w, r)
		} else {
			s.insertMessage(w, r)
		}
	case "/youtube/v3/liveChat/bans":
		s.bans(w, r)
	case "/youtube/v3/liveChat/moderators":
		s.moderators(w, r)
	case "/mock", "/mock/live", "/mock/say", "/mock/superchat", "/mock/member", "/mock/end", "/mock/drop":
		s.controlPage(w, r)
	case "/o/oauth2/v2/auth":
		s.authorize(w, r)
	case "/token":
		s.token(w, r)
	case "/revoke":
		s.revoke(w, r)
	case "/youtube/v3/channels":
		s.channelsList(w, r)
	default:
		http.NotFound(w, r)
	}
}

var page = template.Must(template.New("p").Parse(`<!doctype html><meta charset="utf-8"><title>mockyoutube sign-in</title>
<h1>Sign in to StreamGremlin (stand-in for Google)</h1><p>This isn't Google. Pick a channel:</p><ul>
{{range .Links}}<li><a href="{{.URL}}">{{.Title}}</a></li>{{end}}</ul>`))

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	ru, err := url.Parse(redirect)
	if err != nil || ru.Scheme != "http" || (ru.Hostname() != "127.0.0.1" && ru.Hostname() != "::1" && ru.Hostname() != "localhost") {
		http.Error(w, "redirect_uri must be a loopback address", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" || q.Get("client_id") == "" || q.Get("code_challenge") == "" ||
		q.Get("code_challenge_method") != "S256" || q.Get("scope") == "" {
		http.Error(w, "missing or wrong parameters", http.StatusBadRequest)
		return
	}
	back := func(v url.Values) {
		v.Set("state", q.Get("state"))
		u := *ru
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound) //nolint:gosec // a test server; redirect_uri is checked to be loopback above
	}
	if q.Get("mock_deny") == "1" {
		back(url.Values{"error": {"access_denied"}})
		return
	}
	if ch := q.Get("mock_channel"); ch != "" {
		s.mu.Lock()
		known := slices.ContainsFunc(s.channels, func(c Channel) bool { return c.ID == ch })
		code := random()
		if known {
			s.codes[code] = grant{channel: ch, clientID: q.Get("client_id"), redirect: redirect, challenge: q.Get("code_challenge"), scope: q.Get("scope")}
		}
		s.mu.Unlock()
		if !known {
			http.Error(w, "unknown channel", http.StatusBadRequest)
			return
		}
		back(url.Values{"code": {code}})
		return
	}
	type link struct{ URL, Title string }
	var links []link
	s.mu.Lock()
	for _, c := range s.channels {
		v := r.URL.Query()
		v.Set("mock_channel", c.ID)
		links = append(links, link{URL: r.URL.Path + "?" + v.Encode(), Title: c.Title})
	}
	s.mu.Unlock()
	v := r.URL.Query()
	v.Set("mock_deny", "1")
	links = append(links, link{URL: r.URL.Path + "?" + v.Encode(), Title: "Cancel (deny access)"})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, struct{ Links []link }{links})
}

func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.ParseForm() != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "POST a form")
		return
	}
	f := r.PostForm
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secret != "" && f.Get("client_secret") != s.secret {
		oauthErr(w, http.StatusUnauthorized, "invalid_client", "Unauthorized")
		return
	}
	var t token
	var newRefresh string
	switch f.Get("grant_type") {
	case "authorization_code":
		g, ok := s.codes[f.Get("code")]
		delete(s.codes, f.Get("code")) // a code works once
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		switch {
		case !ok:
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "Malformed auth code.")
			return
		case g.clientID != f.Get("client_id") || g.redirect != f.Get("redirect_uri"):
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "Bad Request")
			return
		case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "Invalid code verifier.")
			return
		}
		t = token{channel: g.channel, clientID: g.clientID, scope: g.scope}
		newRefresh = random()
		s.refresh[newRefresh] = t
	case "refresh_token":
		rt, ok := s.refresh[f.Get("refresh_token")]
		if !ok || rt.clientID != f.Get("client_id") {
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "Token has been expired or revoked.")
			return
		}
		t = rt
	default:
		oauthErr(w, http.StatusBadRequest, "unsupported_grant_type", "Invalid grant_type")
		return
	}
	at := random()
	t.expires = s.now().Add(s.ttl)
	s.access[at] = t
	out := map[string]any{"access_token": at, "expires_in": int(s.ttl.Seconds()), "scope": t.scope, "token_type": "Bearer"}
	if newRefresh != "" {
		out["refresh_token"] = newRefresh // Google sends it only with the first answer
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.ParseForm() != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "POST a form")
		return
	}
	tok := r.PostForm.Get("token")
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.access[tok]
	if !ok {
		t, ok = s.refresh[tok]
	}
	if !ok {
		oauthErr(w, http.StatusBadRequest, "invalid_token", "Token expired or revoked")
		return
	}
	s.revoked = append(s.revoked, tok)
	// Revoking either ends the whole grant, as Google's does.
	for k, v := range s.access {
		if v.channel == t.channel && v.clientID == t.clientID {
			delete(s.access, k)
		}
	}
	for k, v := range s.refresh {
		if v.channel == t.channel && v.clientID == t.clientID {
			delete(s.refresh, k)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func apiErr(w http.ResponseWriter, status int, reason, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": status, "message": msg, "errors": []map[string]string{{"reason": reason, "message": msg}},
	}})
}

// authed checks the bearer token; call with mu held.
func (s *Server) authed(w http.ResponseWriter, r *http.Request, resource string) (token, bool) {
	s.calls[resource]++
	at, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	t, known := s.access[at]
	if !ok || !known || s.now().After(t.expires) {
		apiErr(w, http.StatusUnauthorized, "authError", "Request had invalid authentication credentials.")
		return token{}, false
	}
	if s.quota {
		apiErr(w, http.StatusForbidden, "quotaExceeded", "The request cannot be completed because you have exceeded your quota.")
		return token{}, false
	}
	return t, true
}

func (s *Server) channelsList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.authed(w, r, "channels")
	if !ok {
		return
	}
	if r.URL.Query().Get("mine") != "true" {
		apiErr(w, http.StatusBadRequest, "invalidCriteria", "only mine=true is served here")
		return
	}
	items := []any{}
	for _, c := range s.channels {
		if c.ID == t.channel && !c.NoChannel {
			items = append(items, map[string]any{"kind": "youtube#channel", "id": c.ID,
				"snippet": map[string]any{"title": c.Title, "customUrl": c.Handle}})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#channelListResponse", "items": items,
		"pageInfo": map[string]int{"totalResults": len(items), "resultsPerPage": 5}})
}

// String describes the server, for its command.
func (s *Server) String() string { return fmt.Sprintf("mockyoutube (%d channels)", len(s.channels)) }
