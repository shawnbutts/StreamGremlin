package mocktwitch

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Facts about viewers, for giveaway entry rules: who follows a channel and
// since when, when an account was made, and how long a viewer has been
// subscribed (sent in chat as the subscriber badge's info).

type viewerState struct {
	followers   map[string]map[string]time.Time // channel login -> viewer login -> followed at
	created     map[string]time.Time            // login -> account made
	subMonths   map[string]map[string]int       // channel login -> viewer login -> months
	followErr   int                             // when set, Get Channel Followers answers this status
	followCalls int
}

func (s *Server) viewersInit() {
	if s.vw.followers == nil {
		s.vw.followers = map[string]map[string]time.Time{}
		s.vw.created = map[string]time.Time{}
		s.vw.subMonths = map[string]map[string]int{}
	}
}

// SetFollower makes viewer follow channel since at, or stop following when
// at is zero. Follow does the same with the time now, and sends the event.
func (s *Server) SetFollower(channel, viewer string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setFollowerLocked(channel, viewer, at)
}

func (s *Server) setFollowerLocked(channel, viewer string, at time.Time) {
	s.viewersInit()
	c, v := strings.ToLower(channel), strings.ToLower(viewer)
	if at.IsZero() {
		delete(s.vw.followers[c], v)
		return
	}
	if s.vw.followers[c] == nil {
		s.vw.followers[c] = map[string]time.Time{}
	}
	s.vw.followers[c][v] = at.UTC()
}

// SetAccountCreated sets when login's account was made (by default, the
// start of 2020).
func (s *Server) SetAccountCreated(login string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewersInit()
	s.vw.created[strings.ToLower(login)] = at.UTC()
}

// SetSubMonths makes viewer a subscriber to channel for months (0: not a
// subscriber). Their chat then carries a subscriber badge.
func (s *Server) SetSubMonths(channel, viewer string, months int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewersInit()
	c := strings.ToLower(channel)
	if s.vw.subMonths[c] == nil {
		s.vw.subMonths[c] = map[string]int{}
	}
	s.vw.subMonths[c][strings.ToLower(viewer)] = months
}

// FailFollowers makes Get Channel Followers answer status (0 to stop).
func (s *Server) FailFollowers(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vw.followErr = status
}

// FollowerCalls counts Get Channel Followers calls answered.
func (s *Server) FollowerCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vw.followCalls
}

func (s *Server) loginOfLocked(id string) string {
	for login, uid := range s.users {
		if uid == id {
			return login
		}
	}
	return ""
}

func (s *Server) createdLocked(login string) time.Time {
	s.viewersInit()
	if t, ok := s.vw.created[login]; ok {
		return t
	}
	return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
}

func (s *Server) subBadgeLocked(channel, viewer string) (map[string]string, bool) {
	s.viewersInit()
	m := s.vw.subMonths[channel][viewer]
	if m < 1 {
		return nil, false
	}
	return map[string]string{"set_id": "subscriber", "id": "0", "info": strconv.Itoa(m)}, true
}

// followersLocked answers Get Channel Followers for one user_id, as the
// broadcaster or a moderator.
func (s *Server) followersLocked(w http.ResponseWriter, r *http.Request) {
	g, ok := s.helixAuth(w, r, "moderator:read:followers")
	if !ok {
		return
	}
	s.viewersInit()
	s.vw.followCalls++
	if s.vw.followErr != 0 {
		writeErr(w, s.vw.followErr, "mocktwitch was told to fail")
		return
	}
	q := r.URL.Query()
	channel := s.loginOfLocked(q.Get("broadcaster_id"))
	if channel == "" {
		writeErr(w, 400, "unknown broadcaster_id")
		return
	}
	if channel != g.login && !s.chat.mods[channel][g.login] {
		writeErr(w, 401, "The user isn't the broadcaster or a moderator")
		return
	}
	data := []any{}
	viewer := s.loginOfLocked(q.Get("user_id"))
	if at, ok := s.vw.followers[channel][viewer]; ok && viewer != "" {
		data = append(data, map[string]string{"user_id": q.Get("user_id"), "user_login": viewer, "user_name": viewer,
			"followed_at": at.Format(time.RFC3339)})
	}
	writeJSON(w, map[string]any{"total": len(s.vw.followers[channel]), "data": data, "pagination": map[string]any{}})
}
