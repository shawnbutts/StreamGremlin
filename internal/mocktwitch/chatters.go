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
	// subsErr: when set, Get Broadcaster Subscriptions answers this status.
	subsErr int
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

// subscriptionsLocked answers Get Broadcaster Subscriptions from the
// subscribers set with SetSubMonths, as Twitch's reference describes it
// (channel:read:subscriptions; broadcaster_id must be the token's user;
// pages of up to 100, 20 by default). The broadcaster isn't listed.
func (s *Server) subscriptionsLocked(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("broadcaster_id") == "" {
		writeErr(w, 400, "The broadcaster_id query parameter is required.")
		return
	}
	g, ok := s.helixAuth(w, r, "channel:read:subscriptions")
	if !ok {
		return
	}
	if s.ch.subsErr != 0 {
		writeErr(w, s.ch.subsErr, "mocktwitch was told to fail")
		return
	}
	if q.Get("broadcaster_id") != g.userID {
		writeErr(w, 401, "The ID in broadcaster_id must match the user ID found in the request's OAuth token.")
		return
	}
	first := 20
	if f := q.Get("first"); f != "" {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 100 {
			writeErr(w, 400, "first is 1 to 100")
			return
		}
		first = n
	}
	s.viewersInit()
	var all []string
	for login, months := range s.vw.subMonths[g.login] {
		if months > 0 {
			all = append(all, login)
		}
	}
	slices.Sort(all)
	start, _ := strconv.Atoi(q.Get("after"))
	start = max(0, min(start, len(all)))
	end := min(start+first, len(all))
	data := []map[string]any{}
	for _, login := range all[start:end] {
		data = append(data, map[string]any{"broadcaster_id": g.userID, "broadcaster_login": g.login, "broadcaster_name": g.login,
			"gifter_id": "", "gifter_login": "", "gifter_name": "", "is_gift": false, "plan_name": "Channel Subscription", "tier": "1000",
			"user_id": s.userIDLocked(login), "user_login": login, "user_name": login})
	}
	page := map[string]any{}
	if end < len(all) {
		page["cursor"] = strconv.Itoa(end)
	}
	writeJSON(w, map[string]any{"data": data, "pagination": page, "total": len(all), "points": len(all)})
}

// FailSubscriptions makes Get Broadcaster Subscriptions answer status (0
// to stop).
func (s *Server) FailSubscriptions(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ch.subsErr = status
}
