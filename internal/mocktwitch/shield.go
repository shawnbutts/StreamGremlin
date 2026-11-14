package mocktwitch

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Shield Mode (M8 slice 1): Get and Update Shield Mode Status, and the
// channel.shield_mode.begin and .end events.

type shieldState struct {
	active bool
	by     string // the moderator's login
	at     time.Time
}

// shieldLocked answers Shield Mode's requests; ok is false for other paths.
func (s *Server) shieldLocked(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/helix/moderation/shield_mode" || (r.Method != "GET" && r.Method != "PUT") {
		return false
	}
	scope := "moderator:manage:shield_mode"
	if r.Method == "GET" {
		scope = ""
	}
	g, ok := s.helixAuth(w, r, scope)
	if !ok {
		return true
	}
	s.modInit()
	key := r.Method + " " + r.URL.Path
	if st := s.md.fail[key]; st != 0 {
		writeErr(w, st, "mocktwitch was told to fail")
		return true
	}
	if r.Method == "GET" && !contains(g.scopes, "moderator:manage:shield_mode") && !contains(g.scopes, "moderator:read:shield_mode") {
		writeErr(w, 401, "Missing scope: moderator:read:shield_mode")
		return true
	}
	q := r.URL.Query()
	if q.Get("broadcaster_id") != g.userID || q.Get("moderator_id") != g.userID {
		writeErr(w, 403, "The user in moderator_id is not one of the broadcaster's moderators.")
		return true
	}
	if s.shield == nil {
		s.shield = map[string]*shieldState{}
	}
	st := s.shield[g.login]
	if st == nil {
		st = &shieldState{}
		s.shield[g.login] = st
	}
	if r.Method == "PUT" {
		var in struct {
			Active *bool `json:"is_active"`
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if json.Unmarshal(raw, &in) != nil || in.Active == nil {
			writeErr(w, 400, "The is_active field is required.")
			return true
		}
		s.md.calls = append(s.md.calls, ModCall{Method: r.Method, Path: r.URL.Path, Status: 200})
		if s.md.hang[key] {
			hangUp(w)
			return true
		}
		changed := st.active != *in.Active
		st.active, st.by = *in.Active, g.login
		if st.active {
			st.at = s.now().UTC()
		}
		if changed {
			s.shieldEventLocked(g.userID, g.login, st)
		}
	}
	writeJSON(w, map[string]any{"data": []any{shieldJSON(s, st)}})
	return true
}

func shieldJSON(s *Server, st *shieldState) map[string]any {
	at := ""
	if !st.at.IsZero() {
		at = st.at.Format(time.RFC3339)
	}
	return map[string]any{"is_active": st.active, "moderator_id": s.userIDLocked(st.by), "moderator_login": st.by, "moderator_name": st.by,
		"last_activated_at": at}
}

func (s *Server) shieldEventLocked(channelID, moderator string, st *shieldState) {
	typ, ev := "channel.shield_mode.end", map[string]any{"ended_at": s.now().UTC().Format(time.RFC3339Nano)}
	if st.active {
		typ, ev = "channel.shield_mode.begin", map[string]any{"started_at": st.at.Format(time.RFC3339Nano)}
	}
	ev["moderator_user_id"], ev["moderator_user_login"], ev["moderator_user_name"] = s.userIDLocked(moderator), moderator, moderator
	s.notifyLocked(channelID, typ, ev)
}

// SetShieldMode turns Shield Mode on or off as a moderator would on
// Twitch's site, sending the event.
func (s *Server) SetShieldMode(channel, moderator string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shield == nil {
		s.shield = map[string]*shieldState{}
	}
	c := strings.ToLower(channel)
	st := s.shield[c]
	if st == nil {
		st = &shieldState{}
		s.shield[c] = st
	}
	if st.active == on {
		return
	}
	st.active, st.by = on, strings.ToLower(moderator)
	if on {
		st.at = s.now().UTC()
	}
	s.shieldEventLocked(s.userIDLocked(c), st.by, st)
}

// ShieldActive reports whether Shield Mode is on in channel.
func (s *Server) ShieldActive(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.shield[strings.ToLower(channel)]
	return st != nil && st.active
}
