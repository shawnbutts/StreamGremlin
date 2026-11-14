package mocktwitch

import (
	"net/http"
	"sort"
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
	subGifters  map[string]map[string]string    // channel login -> viewer login -> gifter login ("" anonymous)
	followErr   int                             // when set, Get Channel Followers answers this status
	followCalls int
}

func (s *Server) viewersInit() {
	if s.vw.followers == nil {
		s.vw.followers = map[string]map[string]time.Time{}
		s.vw.created = map[string]time.Time{}
		s.vw.subMonths = map[string]map[string]int{}
		s.vw.subGifters = map[string]map[string]string{}
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

// The viewer card's lists (Get VIPs, Get Moderators, Get Banned Users), as
// Twitch's reference describes them: the broadcaster's token, user_id
// filters up to 100, ids not on the list ignored.

type cardState struct {
	vips   map[string]map[string]bool   // channel login -> viewer login
	banned map[string]map[string]banned // channel login -> viewer login
	// fail: when set for a path, that endpoint answers the status.
	fail map[string]int
}

type banned struct {
	until  time.Time // zero: doesn't end
	at     time.Time
	reason string
	by     string
}

func (s *Server) cardInit() {
	if s.cd.vips == nil {
		s.cd.vips = map[string]map[string]bool{}
		s.cd.banned = map[string]map[string]banned{}
		s.cd.fail = map[string]int{}
	}
}

// SetVIP makes viewer a VIP in channel, or not.
func (s *Server) SetVIP(channel, viewer string, vip bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	c := strings.ToLower(channel)
	if s.cd.vips[c] == nil {
		s.cd.vips[c] = map[string]bool{}
	}
	s.cd.vips[c][strings.ToLower(viewer)] = vip
}

// SetBanned bans viewer from channel (until zero: for good), or lifts it
// when banned is false.
func (s *Server) SetBanned(channel, viewer string, isBanned bool, until time.Time, reason, by string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	c, v := strings.ToLower(channel), strings.ToLower(viewer)
	if s.cd.banned[c] == nil {
		s.cd.banned[c] = map[string]banned{}
	}
	if !isBanned {
		delete(s.cd.banned[c], v)
		return
	}
	s.cd.banned[c][v] = banned{until: until.UTC(), at: s.now().UTC(), reason: reason, by: by}
}

// FailCard makes one of the card's endpoints ("/helix/channels/vips",
// "/helix/moderation/moderators", "/helix/moderation/banned") answer status
// (0 to stop).
func (s *Server) FailCard(path string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	s.cd.fail[path] = status
}

// cardListLocked answers the three list endpoints.
func (s *Server) cardListLocked(w http.ResponseWriter, r *http.Request, scopes ...string) {
	s.cardInit()
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	g, ok := s.access[tok]
	if ok {
		has := false
		for _, sc := range scopes {
			has = has || contains(g.scopes, sc)
		}
		if !has {
			writeErr(w, 401, "Missing scope: "+strings.Join(scopes, " or "))
			return
		}
	}
	if _, ok := s.helixAuth(w, r, ""); !ok {
		return
	}
	if st := s.cd.fail[r.URL.Path]; st != 0 {
		writeErr(w, st, "mocktwitch was told to fail")
		return
	}
	q := r.URL.Query()
	if q.Get("broadcaster_id") != g.userID {
		writeErr(w, 401, "The ID in broadcaster_id must match the user ID found in the request's OAuth token.")
		return
	}
	ids := q["user_id"]
	if len(ids) > 100 {
		writeErr(w, 400, "The maximum number of user_id is 100.")
		return
	}
	channel := g.login
	if len(ids) == 0 && r.URL.Path == "/helix/moderation/banned" {
		s.bannedPageLocked(w, r, channel)
		return
	}
	data := []map[string]string{}
	for _, id := range ids {
		v := s.loginOfLocked(id)
		if v == "" {
			continue
		}
		row := map[string]string{"user_id": id, "user_login": v, "user_name": v}
		switch r.URL.Path {
		case "/helix/channels/vips":
			if !s.cd.vips[channel][v] {
				continue
			}
		case "/helix/moderation/moderators":
			if !s.chat.mods[channel][v] {
				continue
			}
		case "/helix/moderation/banned":
			b, ok := s.cd.banned[channel][v]
			if !ok {
				continue
			}
			row["expires_at"] = ""
			if !b.until.IsZero() {
				row["expires_at"] = b.until.Format(time.RFC3339)
			}
			row["created_at"] = b.at.Format(time.RFC3339)
			row["reason"] = b.reason
			row["moderator_id"], row["moderator_login"], row["moderator_name"] = s.userIDLocked(b.by), b.by, b.by
		}
		data = append(data, row)
	}
	writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
}

// SetSubGift marks viewer's subscription to channel as a gift from gifter
// ("" for an anonymous gift). Set the subscription with SetSubMonths.
func (s *Server) SetSubGift(channel, viewer, gifter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewersInit()
	c := strings.ToLower(channel)
	if s.vw.subGifters[c] == nil {
		s.vw.subGifters[c] = map[string]string{}
	}
	s.vw.subGifters[c][strings.ToLower(viewer)] = strings.ToLower(gifter)
}

// bannedPageLocked answers Get Banned Users without user_id: every ban and
// timeout, by login, first (default 20, up to 100) at a time.
func (s *Server) bannedPageLocked(w http.ResponseWriter, r *http.Request, channel string) {
	q := r.URL.Query()
	first := 20
	if n, err := strconv.Atoi(q.Get("first")); err == nil {
		if n < 1 || n > 100 {
			writeErr(w, 400, "first is 1 to 100")
			return
		}
		first = n
	}
	var logins []string
	for v := range s.cd.banned[channel] {
		logins = append(logins, v)
	}
	sort.Strings(logins)
	after := q.Get("after")
	data := []map[string]string{}
	cursor := ""
	for _, v := range logins {
		if after != "" && v <= after {
			continue
		}
		if len(data) == first {
			cursor = data[len(data)-1]["user_login"]
			break
		}
		b := s.cd.banned[channel][v]
		row := map[string]string{"user_id": s.userIDLocked(v), "user_login": v, "user_name": v, "created_at": b.at.Format(time.RFC3339),
			"reason": b.reason, "expires_at": ""}
		if !b.until.IsZero() {
			row["expires_at"] = b.until.Format(time.RFC3339)
		}
		data = append(data, row)
	}
	page := map[string]any{}
	if cursor != "" {
		page["cursor"] = cursor
	}
	writeJSON(w, map[string]any{"data": data, "pagination": page})
}

// Bans lists who's banned (or timed out) in channel, by login.
func (s *Server) Bans(channel string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	var out []string
	for v := range s.cd.banned[strings.ToLower(channel)] {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// BanViewer bans viewer in channel, as a moderator on Twitch's site would.
func (s *Server) BanViewer(channel, viewer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	c := strings.ToLower(channel)
	if s.cd.banned[c] == nil {
		s.cd.banned[c] = map[string]banned{}
	}
	s.userIDLocked(viewer)
	s.cd.banned[c][strings.ToLower(viewer)] = banned{at: s.now().UTC(), by: c}
}

// UnbanViewer lifts a ban, as a moderator on Twitch's site would.
func (s *Server) UnbanViewer(channel, viewer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	delete(s.cd.banned[strings.ToLower(channel)], strings.ToLower(viewer))
}
