// Package mockpretzel is a stand-in for Pretzel's now-playing address
// (D-090), answering as it was seen to on 2026-11-13, for tests and for
// trying StreamGremlin's Now playing overlay without Pretzel.
package mockpretzel

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Server is the stand-in. Accounts it doesn't know answer 400 "ID not
// found", as Pretzel does.
type Server struct {
	mu       sync.Mutex
	playing  map[string]string // user id -> the line Pretzel says
	status   int               // answer every request with this, when set
	retry    string            // Retry-After with status
	requests int
}

// New makes a Server that knows no accounts.
func New() *Server { return &Server{playing: map[string]string{}} }

// Line is what Pretzel says for a song: "Now Playing: Title by Artist -> link".
func Line(title, artist, link string) string {
	return fmt.Sprintf("Now Playing: %s by %s -> %s", title, artist, link)
}

// Set says what an account is playing, as Pretzel's whole answer.
func (s *Server) Set(userID, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playing[userID] = line
}

// Fail answers every request with status (and Retry-After, if given);
// 0 answers normally again.
func (s *Server) Fail(status int, retryAfter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.retry = status, retryAfter
}

// Requests counts the requests answered.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/panel" {
		s.panel(w, r)
		return
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/playing/twitch/")
	if !ok || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	s.requests++
	status, retry := s.status, s.retry
	line, known := s.playing[id]
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	switch {
	case status != 0:
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(http.StatusText(status)))
	case !known:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("ID not found"))
	default:
		_, _ = w.Write([]byte(line))
	}
}

// panel is a page to change the song by hand: /panel?id=…&title=…&artist=….
func (s *Server) panel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if id := q.Get("id"); id != "" && q.Get("title") != "" {
		n := s.Requests()
		s.Set(id, Line(q.Get("title"), q.Get("artist"), "https://prtzl.io/mock"+strconv.Itoa(n)))
	}
	s.mu.Lock()
	var rows strings.Builder
	for id, line := range s.playing {
		fmt.Fprintf(&rows, "<li><code>%s</code>: %s</li>", html.EscapeString(id), html.EscapeString(line))
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>mockpretzel</title><h1>mockpretzel</h1><ul>%s</ul>
<form><label>Twitch user id <input name="id"></label> <label>Title <input name="title"></label> <label>Artist <input name="artist"></label> <button>Play</button></form>`, rows.String())
}
