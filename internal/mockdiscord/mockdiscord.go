// Package mockdiscord is a stand-in for Discord's webhook endpoints (D-089):
// Get Webhook with Token and Execute Webhook, as Discord's reference
// documents them, for tests and for trying StreamGremlin without Discord.
package mockdiscord

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Post is a message posted through a webhook.
type Post struct {
	WebhookID, MessageID string
	Content, Username    string
	// Parse is allowed_mentions.parse as sent; nil when allowed_mentions
	// was left out, which Discord takes as parse users.
	Parse  []string
	Embeds []map[string]any
	At     time.Time
}

type webhook struct {
	id, token, name, channel, guild string
}

// Server is the stand-in.
type Server struct {
	mu       sync.Mutex
	webhooks map[string]webhook
	posts    []Post
	next     int
	limited  int // answer this many posts with 429
	down     bool
}

// New makes a Server with no webhooks.
func New() *Server { return &Server{webhooks: map[string]webhook{}} }

// AddWebhook makes a webhook and returns its link's path (/api/webhooks/id/token).
func (s *Server) AddWebhook(name string) (id, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id = strconv.Itoa(1000000000000000000 + s.next)
	token = fmt.Sprintf("tok%040d", s.next)
	s.webhooks[id] = webhook{id: id, token: token, name: name, channel: strconv.Itoa(2000000000000000000 + s.next), guild: "3000000000000000000"}
	return id, token
}

// DeleteWebhook deletes a webhook, as when it's deleted in Discord.
func (s *Server) DeleteWebhook(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.webhooks, id)
}

// Posts returns what was posted, in order.
func (s *Server) Posts() []Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Post(nil), s.posts...)
}

// SetRateLimited answers the next n posts with 429.
func (s *Server) SetRateLimited(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limited = n
}

// SetDown makes every request fail with 503.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

var path = regexp.MustCompile(`^/api(?:/v\d+)?/webhooks/(\d+)/([A-Za-z0-9_-]+)$`)

func fail(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg, "code": code})
}

// ServeHTTP answers the webhook endpoints, and /panel lists the posts.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path == "/panel" {
		s.panelLocked(w)
		return
	}
	if s.down {
		fail(w, 503, 0, "mockdiscord is down")
		return
	}
	m := path.FindStringSubmatch(r.URL.Path)
	if m == nil {
		fail(w, 404, 0, "404: Not Found")
		return
	}
	wh, ok := s.webhooks[m[1]]
	if !ok {
		fail(w, 404, 10015, "Unknown Webhook")
		return
	}
	if wh.token != m[2] {
		fail(w, 401, 50027, "Invalid Webhook Token")
		return
	}
	switch r.Method {
	case "GET":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": wh.id, "type": 1, "name": wh.name, "channel_id": wh.channel,
			"guild_id": wh.guild, "token": wh.token, "application_id": nil, "avatar": nil})
	case "POST":
		s.executeLocked(w, r, wh)
	default:
		fail(w, 405, 0, "405: Method Not Allowed")
	}
}

func (s *Server) executeLocked(w http.ResponseWriter, r *http.Request, wh webhook) {
	if s.limited > 0 {
		s.limited--
		w.Header().Set("Retry-After", "0.05")
		fail429(w)
		return
	}
	var in struct {
		Content         string           `json:"content"`
		Username        string           `json:"username"`
		Embeds          []map[string]any `json:"embeds"`
		AllowedMentions *struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, 50109, "The request body contains invalid JSON.")
		return
	}
	if in.Content == "" && len(in.Embeds) == 0 {
		fail(w, 400, 50006, "Cannot send an empty message")
		return
	}
	if utf8.RuneCountInString(in.Content) > 2000 || len(in.Embeds) > 10 {
		fail(w, 400, 50035, "Invalid Form Body")
		return
	}
	total := 0
	for _, e := range in.Embeds {
		for _, k := range []string{"title", "description"} {
			v, _ := e[k].(string)
			total += utf8.RuneCountInString(v)
		}
		if t, _ := e["title"].(string); utf8.RuneCountInString(t) > 256 {
			fail(w, 400, 50035, "Invalid Form Body")
			return
		}
		if d, _ := e["description"].(string); utf8.RuneCountInString(d) > 4096 {
			fail(w, 400, 50035, "Invalid Form Body")
			return
		}
	}
	if total > 6000 {
		fail(w, 400, 50035, "Invalid Form Body")
		return
	}
	s.next++
	p := Post{WebhookID: wh.id, MessageID: strconv.Itoa(4000000000000000000 + s.next), Content: in.Content, Username: in.Username, Embeds: in.Embeds, At: time.Now()}
	if in.AllowedMentions != nil {
		p.Parse = append([]string{}, in.AllowedMentions.Parse...)
	}
	s.posts = append(s.posts, p)
	if r.URL.Query().Get("wait") != "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": p.MessageID, "channel_id": wh.channel, "content": p.Content, "embeds": p.Embeds, "webhook_id": wh.id})
}

func fail429(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "You are being rate limited.", "retry_after": 0.05, "global": false})
}

func (s *Server) panelLocked(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=utf-8><title>mockdiscord</title><h1>Webhooks</h1><ul>")
	for _, wh := range s.webhooks {
		fmt.Fprintf(&b, "<li>%s: <code>/api/webhooks/%s/%s</code></li>", html.EscapeString(wh.name), wh.id, wh.token)
	}
	b.WriteString("</ul><h1>Posts</h1><ol>")
	for _, p := range s.posts {
		e, _ := json.Marshal(p.Embeds)
		fmt.Fprintf(&b, "<li>%s <b>%s</b> %s <code>%s</code></li>", p.At.Format("15:04:05"), html.EscapeString(p.Username), html.EscapeString(p.Content), html.EscapeString(string(e)))
	}
	b.WriteString("</ol>")
	_, _ = w.Write([]byte(b.String()))
}
