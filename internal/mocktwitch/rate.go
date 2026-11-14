package mocktwitch

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Twitch's rate limit (https://dev.twitch.tv/docs/api/guide#twitch-rate-limits):
// a bucket of points per user token, one point a request, refilled each
// window, with Ratelimit-Limit, -Remaining and -Reset on every answer and
// 429 when it's empty. Off unless a test turns it on.

type rateState struct {
	limit  int
	window time.Duration
	left   map[string]int
	reset  time.Time
	denied int
}

// SetRateLimit turns the rate limit on: limit requests per token each
// window. 0 turns it off.
func (s *Server) SetRateLimit(limit int, window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		s.rate = nil
		return
	}
	s.rate = &rateState{limit: limit, window: window, left: map[string]int{}, reset: s.now().Add(window)}
}

// RateDenied counts the requests answered 429.
func (s *Server) RateDenied() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rate == nil {
		return 0
	}
	return s.rate.denied
}

// rateLocked takes a point for the request's token, sending the headers;
// false when it answered 429.
func (s *Server) rateLocked(w http.ResponseWriter, r *http.Request) bool {
	rt := s.rate
	if rt == nil {
		return true
	}
	now := s.now()
	if !now.Before(rt.reset) {
		rt.left, rt.reset = map[string]int{}, now.Add(rt.window)
	}
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	left, ok := rt.left[tok]
	if !ok {
		left = rt.limit
	}
	h := w.Header()
	h.Set("Ratelimit-Limit", strconv.Itoa(rt.limit))
	h.Set("Ratelimit-Reset", strconv.FormatInt(rt.reset.Unix(), 10))
	if left == 0 {
		rt.denied++
		h.Set("Ratelimit-Remaining", "0")
		writeErr(w, 429, "Too Many Requests")
		return false
	}
	rt.left[tok] = left - 1
	h.Set("Ratelimit-Remaining", strconv.Itoa(left-1))
	return true
}
