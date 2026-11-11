package mocktwitch

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Moderating, as Twitch's reference describes it (checked 2026-11-11):
// bans and timeouts, unbans, deleting a message, warnings, shoutouts,
// adding and removing VIPs and moderators, suspicious users' treatment,
// and chat settings. Every request answered is logged for tests.

// ModCall is one moderation request mocktwitch answered.
type ModCall struct {
	Method, Path, UserLogin, MessageID, Reason string
	Seconds                                    int
	Status                                     int
}

type modState struct {
	calls       []ModCall
	deleted     map[string]bool              // message ids
	warned      map[string]map[string]string // channel -> viewer -> reason
	suspicious  map[string]map[string]string // channel -> viewer -> ACTIVE_MONITORING or RESTRICTED
	lastShout   map[string]time.Time         // channel -> last shoutout
	lastShoutTo map[string]time.Time         // channel/target -> last shoutout to them
	settings    map[string]map[string]any    // channel -> chat settings
	fail        map[string]int               // "METHOD /helix/path" -> status
	hang        map[string]bool              // "METHOD /helix/path" -> answer nothing (closes the connection)
}

func (s *Server) modInit() {
	s.cardInit()
	if s.md.deleted == nil {
		s.md.deleted = map[string]bool{}
		s.md.warned = map[string]map[string]string{}
		s.md.suspicious = map[string]map[string]string{}
		s.md.lastShout = map[string]time.Time{}
		s.md.lastShoutTo = map[string]time.Time{}
		s.md.settings = map[string]map[string]any{}
		s.md.fail = map[string]int{}
		s.md.hang = map[string]bool{}
	}
}

// ModCalls lists the moderation requests answered, oldest first.
func (s *Server) ModCalls() []ModCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ModCall(nil), s.md.calls...)
}

// FailMod makes "METHOD /helix/path" answer status (0 to stop).
func (s *Server) FailMod(methodPath string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modInit()
	s.md.fail[methodPath] = status
}

// DropMod makes "METHOD /helix/path" do the request and then close the
// connection without answering (false to stop), as when the network
// fails after Twitch acted.
func (s *Server) DropMod(methodPath string, drop bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modInit()
	s.md.hang[methodPath] = drop
}

// Deleted reports whether a chat message was deleted.
func (s *Server) Deleted(messageID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modInit()
	return s.md.deleted[messageID]
}

// Warned returns the reason viewer was last warned for in channel.
func (s *Server) Warned(channel, viewer string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modInit()
	return s.md.warned[strings.ToLower(channel)][strings.ToLower(viewer)]
}

// Suspicious returns viewer's treatment in channel ("" for none).
func (s *Server) Suspicious(channel, viewer string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modInit()
	return s.md.suspicious[strings.ToLower(channel)][strings.ToLower(viewer)]
}

// IsBanned reports whether viewer is banned or timed out in channel now.
func (s *Server) IsBanned(channel, viewer string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	b, ok := s.cd.banned[strings.ToLower(channel)][strings.ToLower(viewer)]
	return ok && (b.until.IsZero() || b.until.After(s.now()))
}

// IsVIP reports whether viewer is a VIP in channel.
func (s *Server) IsVIP(channel, viewer string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardInit()
	return s.cd.vips[strings.ToLower(channel)][strings.ToLower(viewer)]
}

// IsModerator reports whether viewer is a moderator in channel.
func (s *Server) IsModerator(channel, viewer string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	return s.chat.mods[strings.ToLower(channel)][strings.ToLower(viewer)]
}

// moderateLocked answers a moderation request; ok is false when the path
// isn't one of them.
func (s *Server) moderateLocked(w http.ResponseWriter, r *http.Request) bool {
	key := r.Method + " " + r.URL.Path
	scopes := map[string]string{
		"POST /helix/moderation/bans": "moderator:manage:banned_users", "DELETE /helix/moderation/bans": "moderator:manage:banned_users",
		"DELETE /helix/moderation/chat": "moderator:manage:chat_messages", "POST /helix/moderation/warnings": "moderator:manage:warnings",
		"POST /helix/chat/shoutouts": "moderator:manage:shoutouts", "POST /helix/channels/vips": "channel:manage:vips",
		"DELETE /helix/channels/vips": "channel:manage:vips", "POST /helix/moderation/moderators": "channel:manage:moderators",
		"DELETE /helix/moderation/moderators":       "channel:manage:moderators",
		"POST /helix/moderation/suspicious_users":   "moderator:manage:suspicious_users",
		"DELETE /helix/moderation/suspicious_users": "moderator:manage:suspicious_users",
		"PATCH /helix/chat/settings":                "moderator:manage:chat_settings",
	}
	scope, ok := scopes[key]
	if !ok {
		return false
	}
	s.modInit()
	g, ok := s.helixAuth(w, r, scope)
	if !ok {
		return true
	}
	q := r.URL.Query()
	call := ModCall{Method: r.Method, Path: r.URL.Path, MessageID: q.Get("message_id")}
	status := 200
	defer func() {
		call.Status = status
		s.md.calls = append(s.md.calls, call)
	}()
	fail := func(code int, msg string) {
		status = code
		writeErr(w, code, msg)
	}
	if st := s.md.fail[key]; st != 0 {
		fail(st, "mocktwitch was told to fail")
		return true
	}
	channel := g.login
	idField := "broadcaster_id"
	if r.URL.Path == "/helix/chat/shoutouts" {
		idField = "from_broadcaster_id"
	}
	if q.Get(idField) != g.userID {
		fail(401, "The ID in "+idField+" must match the user ID found in the request's OAuth token.")
		return true
	}
	needsMod := !strings.HasPrefix(r.URL.Path, "/helix/channels/vips") && r.URL.Path != "/helix/moderation/moderators"
	if needsMod && q.Get("moderator_id") != g.userID {
		fail(401, "The ID in moderator_id must match the user ID found in the request's OAuth token.")
		return true
	}
	var in struct {
		Data struct {
			UserID   string `json:"user_id"`
			Duration int    `json:"duration"`
			Reason   string `json:"reason"`
		} `json:"data"`
		UserID string `json:"user_id"`
		Status string `json:"status"`
	}
	var raw []byte
	if r.Body != nil {
		raw, _ = io.ReadAll(io.LimitReader(r.Body, 1<<16))
		_ = json.Unmarshal(raw, &in)
	}
	target := in.Data.UserID
	if target == "" {
		target = in.UserID
	}
	if target == "" {
		target = q.Get("user_id")
	}
	if target == "" {
		target = q.Get("to_broadcaster_id")
	}
	viewer := s.loginOfLocked(target)
	call.UserLogin, call.Reason, call.Seconds = viewer, in.Data.Reason, in.Data.Duration
	if r.URL.Path != "/helix/moderation/chat" && r.URL.Path != "/helix/chat/settings" && viewer == "" {
		fail(400, "The user specified in the user_id field was not found.")
		return true
	}
	if s.cd.banned[channel] == nil {
		s.cd.banned[channel] = map[string]banned{}
	}
	if s.cd.vips[channel] == nil {
		s.cd.vips[channel] = map[string]bool{}
	}
	if s.chat.mods[channel] == nil {
		s.chat.mods[channel] = map[string]bool{}
	}
	ban, isBanned := s.cd.banned[channel][viewer]
	if isBanned && !ban.until.IsZero() && !ban.until.After(s.now()) {
		isBanned = false
	}
	switch key {
	case "POST /helix/moderation/bans":
		switch {
		case viewer == channel:
			fail(400, "The user specified in the user_id field may not be banned.")
			return true
		case len([]rune(in.Data.Reason)) > 500:
			fail(400, "The text in the reason field is too long.")
			return true
		case in.Data.Duration < 0 || in.Data.Duration > 1_209_600:
			fail(400, "The value in the duration field is not valid.")
			return true
		case isBanned && ban.until.IsZero():
			fail(400, "The user specified in the user_id field is already banned.")
			return true
		}
		b := banned{at: s.now().UTC(), reason: in.Data.Reason, by: channel}
		if in.Data.Duration > 0 {
			b.until = s.now().Add(time.Duration(in.Data.Duration) * time.Second).UTC()
		}
		s.cd.banned[channel][viewer] = b
		var end any
		if !b.until.IsZero() {
			end = b.until.Format(time.RFC3339)
		}
		if s.md.hang[key] {
			hangUp(w)
			return true
		}
		writeJSON(w, map[string]any{"data": []any{map[string]any{"broadcaster_id": g.userID, "moderator_id": g.userID,
			"user_id": target, "created_at": b.at.Format(time.RFC3339), "end_time": end}}})
		return true
	case "DELETE /helix/moderation/bans":
		if !isBanned {
			fail(400, "The user specified in the user_id query parameter is not banned.")
			return true
		}
		delete(s.cd.banned[channel], viewer)
	case "DELETE /helix/moderation/chat":
		id := q.Get("message_id")
		found := false
		for _, m := range s.chat.log {
			if m.MessageID == id && strings.EqualFold(m.Channel, channel) {
				found = true
				if m.ChatterLogin == channel {
					fail(400, "You may not delete the broadcaster's messages.")
					return true
				}
				if s.chat.mods[channel][m.ChatterLogin] {
					fail(400, "You may not delete another moderator's messages.")
					return true
				}
			}
		}
		if !found {
			fail(404, "The ID in message_id was not found.")
			return true
		}
		s.md.deleted[id] = true
	case "POST /helix/moderation/warnings":
		if strings.TrimSpace(in.Data.Reason) == "" || len([]rune(in.Data.Reason)) > 500 {
			fail(400, "The reason is required and up to 500 characters.")
			return true
		}
		if s.md.warned[channel] == nil {
			s.md.warned[channel] = map[string]string{}
		}
		s.md.warned[channel][viewer] = in.Data.Reason
		writeJSON(w, map[string]any{"data": []any{map[string]any{"broadcaster_id": g.userID, "user_id": target,
			"moderator_id": g.userID, "reason": in.Data.Reason}}})
		return true
	case "POST /helix/chat/shoutouts":
		now := s.now()
		switch {
		case viewer == channel:
			fail(400, "The broadcaster may not give themselves a Shoutout.")
			return true
		case now.Sub(s.md.lastShout[channel]) < 2*time.Minute:
			fail(429, "The broadcaster may send a Shoutout once every 2 minutes.")
			return true
		case now.Sub(s.md.lastShoutTo[channel+"/"+viewer]) < time.Hour:
			fail(429, "The broadcaster may send the same broadcaster a Shoutout once every 60 minutes.")
			return true
		}
		s.md.lastShout[channel], s.md.lastShoutTo[channel+"/"+viewer] = now, now
	case "POST /helix/channels/vips":
		switch {
		case isBanned:
			fail(400, "The user in user_id is banned from the broadcaster's chat room.")
			return true
		case s.chat.mods[channel][viewer]:
			fail(422, "The user in user_id is a moderator. To make them a VIP, you must first remove them as a moderator.")
			return true
		case s.cd.vips[channel][viewer]:
			fail(422, "The user in user_id is already a VIP.")
			return true
		}
		s.cd.vips[channel][viewer] = true
	case "DELETE /helix/channels/vips":
		if !s.cd.vips[channel][viewer] {
			fail(422, "The user in user_id is not a VIP in the broadcaster's channel.")
			return true
		}
		delete(s.cd.vips[channel], viewer)
	case "POST /helix/moderation/moderators":
		switch {
		case s.chat.mods[channel][viewer]:
			fail(400, "The user in user_id is already a moderator in the broadcaster's chat room.")
			return true
		case s.cd.vips[channel][viewer]:
			fail(422, "The user in user_id is a VIP. To make them a moderator, you must first remove them as a VIP.")
			return true
		}
		s.chat.mods[channel][viewer] = true
	case "DELETE /helix/moderation/moderators":
		if !s.chat.mods[channel][viewer] {
			fail(400, "The user in user_id is not a moderator in the broadcaster's chat room.")
			return true
		}
		delete(s.chat.mods[channel], viewer)
	case "POST /helix/moderation/suspicious_users":
		if in.Status != "ACTIVE_MONITORING" && in.Status != "RESTRICTED" {
			fail(400, "status must be ACTIVE_MONITORING or RESTRICTED")
			return true
		}
		if s.md.suspicious[channel] == nil {
			s.md.suspicious[channel] = map[string]string{}
		}
		s.md.suspicious[channel][viewer] = in.Status
		writeJSON(w, map[string]any{"data": []any{map[string]any{"user_id": target, "broadcaster_id": g.userID,
			"moderator_id": g.userID, "status": in.Status, "types": []string{"MANUALLY_ADDED"}}}})
		return true
	case "DELETE /helix/moderation/suspicious_users":
		delete(s.md.suspicious[channel], viewer)
		writeJSON(w, map[string]any{"data": []any{map[string]any{"user_id": target, "broadcaster_id": g.userID,
			"moderator_id": g.userID, "status": "NO_TREATMENT", "types": []string{}}}})
		return true
	case "PATCH /helix/chat/settings":
		return s.patchChatSettingsLocked(w, raw, channel, g.userID, &status)
	}
	if s.md.hang[key] {
		hangUp(w)
		return true
	}
	status = 204
	w.WriteHeader(204)
	return true
}

// hangUp closes the connection without an answer.
func hangUp(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if c, _, err := hj.Hijack(); err == nil {
			_ = c.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

func (s *Server) chatSettingsLocked(channel, id string) map[string]any {
	s.modInit()
	cs := s.md.settings[channel]
	if cs == nil {
		cs = map[string]any{"emote_mode": false, "follower_mode": false, "follower_mode_duration": nil, "slow_mode": false,
			"slow_mode_wait_time": nil, "subscriber_mode": false, "unique_chat_mode": false,
			"non_moderator_chat_delay": false, "non_moderator_chat_delay_duration": nil}
		s.md.settings[channel] = cs
	}
	out := map[string]any{"broadcaster_id": id}
	for k, v := range cs {
		out[k] = v
	}
	return out
}

// getChatSettingsLocked answers Get Chat Settings (any token).
func (s *Server) getChatSettingsLocked(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.helixAuth(w, r, ""); !ok {
		return
	}
	s.modInit()
	if st := s.md.fail["GET /helix/chat/settings"]; st != 0 {
		writeErr(w, st, "mocktwitch was told to fail")
		return
	}
	id := r.URL.Query().Get("broadcaster_id")
	channel := s.loginOfLocked(id)
	if channel == "" {
		writeErr(w, 400, "The ID in broadcaster_id is not valid.")
		return
	}
	writeJSON(w, map[string]any{"data": []any{s.chatSettingsLocked(channel, id)}})
}

// SetChatSettings sets channel's chat settings (as from Twitch's site).
func (s *Server) SetChatSettings(channel string, set map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := strings.ToLower(channel)
	cs := s.chatSettingsLocked(c, "")
	for k, v := range set {
		cs[k] = v
	}
	delete(cs, "broadcaster_id")
	s.md.settings[c] = cs
}

func (s *Server) patchChatSettingsLocked(w http.ResponseWriter, raw []byte, channel, id string, status *int) bool {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil || len(in) == 0 {
		*status = 400
		writeErr(w, 400, "The request body is missing or not valid.")
		return true
	}
	num := func(k string, lo, hi float64) bool {
		v, ok := in[k]
		if !ok {
			return true
		}
		f, isNum := v.(float64)
		return isNum && f >= lo && f <= hi
	}
	switch {
	case !num("follower_mode_duration", 0, 129600):
		*status = 400
		writeErr(w, 400, "The value in follower_mode_duration is not valid.")
		return true
	case !num("slow_mode_wait_time", 3, 120):
		*status = 400
		writeErr(w, 400, "The value in slow_mode_wait_time is not valid.")
		return true
	}
	cs := s.chatSettingsLocked(channel, id)
	for k, v := range in {
		if _, known := cs[k]; known && k != "broadcaster_id" {
			cs[k] = v
		}
	}
	if cs["slow_mode"] == false {
		cs["slow_mode_wait_time"] = nil
	} else if cs["slow_mode_wait_time"] == nil {
		cs["slow_mode_wait_time"] = float64(30)
	}
	if cs["follower_mode"] == false {
		cs["follower_mode_duration"] = nil
	} else if cs["follower_mode_duration"] == nil {
		cs["follower_mode_duration"] = float64(0)
	}
	saved := map[string]any{}
	for k, v := range cs {
		if k != "broadcaster_id" {
			saved[k] = v
		}
	}
	s.md.settings[channel] = saved
	writeJSON(w, map[string]any{"data": []any{cs}})
	return true
}
