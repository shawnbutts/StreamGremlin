package mocktwitch

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Get Chatters: who's connected to a channel's chat, as Twitch's reference
// describes it (moderator:read:chatters; moderator_id must be the token's
// user and the broadcaster or a moderator; pages of up to 1000).

type chatterState struct {
	list  map[string][]string // channel login -> viewer logins, in join order
	err   int                 // when set, Get Chatters answers this status
	calls int
}

// SetChatters sets who's in channel's chat (none to empty it).
func (s *Server) SetChatters(channel string, logins ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch.list == nil {
		s.ch.list = map[string][]string{}
	}
	l := make([]string, 0, len(logins))
	for _, v := range logins {
		l = append(l, strings.ToLower(v))
	}
	s.ch.list[strings.ToLower(channel)] = l
}

// FailChatters makes Get Chatters answer status (0 to stop).
func (s *Server) FailChatters(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ch.err = status
}

// ChattersCalls counts Get Chatters calls answered.
func (s *Server) ChattersCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch.calls
}

func (s *Server) chattersLocked(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("broadcaster_id") == "" || q.Get("moderator_id") == "" {
		writeErr(w, 400, "The broadcaster_id and moderator_id query parameters are required.")
		return
	}
	g, ok := s.helixAuth(w, r, "moderator:read:chatters")
	if !ok {
		return
	}
	s.ch.calls++
	if s.ch.err != 0 {
		writeErr(w, s.ch.err, "mocktwitch was told to fail")
		return
	}
	if q.Get("moderator_id") != g.userID {
		writeErr(w, 401, "The ID in the moderator_id query parameter must match the user ID in the access token.")
		return
	}
	channel := s.loginOfLocked(q.Get("broadcaster_id"))
	if channel == "" {
		writeErr(w, 400, "The ID in the broadcaster_id query parameter is not valid.")
		return
	}
	if channel != g.login && !s.chat.mods[channel][g.login] {
		writeErr(w, 403, "The user in the moderator_id query parameter is not one of the broadcaster's moderators.")
		return
	}
	first := 100
	if f := q.Get("first"); f != "" {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 1000 {
			writeErr(w, 400, "first is 1 to 1000")
			return
		}
		first = n
	}
	all := slices.Clone(s.ch.list[channel])
	start, _ := strconv.Atoi(q.Get("after"))
	start = max(0, min(start, len(all)))
	end := min(start+first, len(all))
	data := []map[string]string{}
	for _, login := range all[start:end] {
		data = append(data, map[string]string{"user_id": s.userIDLocked(login), "user_login": login, "user_name": login})
	}
	page := map[string]any{}
	if end < len(all) {
		page["cursor"] = strconv.Itoa(end)
	}
	writeJSON(w, map[string]any{"data": data, "pagination": page, "total": len(all)})
}
