package mocktwitch

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// subType is an EventSub subscription type the mock accepts, with what
// Twitch's docs say it needs.
type subType struct {
	version string
	scopes  []string // any one of them; none: no authorization needed
	cond    string   // the condition naming the channel
}

var subTypes = map[string]subType{
	"channel.chat.message":         {"1", []string{"user:read:chat"}, "broadcaster_user_id"},
	"channel.follow":               {"2", []string{"moderator:read:followers"}, "broadcaster_user_id"},
	"channel.subscribe":            {"1", []string{"channel:read:subscriptions"}, "broadcaster_user_id"},
	"channel.subscription.message": {"1", []string{"channel:read:subscriptions"}, "broadcaster_user_id"},
	"channel.subscription.gift":    {"1", []string{"channel:read:subscriptions"}, "broadcaster_user_id"},
	"channel.cheer":                {"1", []string{"bits:read"}, "broadcaster_user_id"},
	"channel.raid":                 {"1", nil, "to_broadcaster_user_id"},
	"channel.channel_points_custom_reward_redemption.add": {"1",
		[]string{"channel:read:redemptions", "channel:manage:redemptions"}, "broadcaster_user_id"},
	"stream.online":               {"1", nil, "broadcaster_user_id"},
	"stream.offline":              {"1", nil, "broadcaster_user_id"},
	"channel.update":              {"2", nil, "broadcaster_user_id"},
	"channel.hype_train.begin":    {"2", []string{"channel:read:hype_train"}, "broadcaster_user_id"},
	"channel.hype_train.progress": {"2", []string{"channel:read:hype_train"}, "broadcaster_user_id"},
	"channel.hype_train.end":      {"2", []string{"channel:read:hype_train"}, "broadcaster_user_id"},
}

// RefuseType makes Twitch refuse new subscriptions of typ with 403, as for
// a channel that can't have them.
func (s *Server) RefuseType(typ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	s.chat.refused[typ] = true
}

// subscribeLocked handles Create EventSub Subscription.
func (s *Server) subscribeLocked(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type      string            `json:"type"`
		Version   string            `json:"version"`
		Condition map[string]string `json:"condition"`
		Transport map[string]string `json:"transport"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Transport["method"] != "websocket" {
		writeErr(w, 400, "bad request")
		return
	}
	g, ok := s.helixAuth(w, r, "")
	if !ok {
		return
	}
	st, known := subTypes[in.Type]
	ss, found := s.chat.sessions[in.Transport["session_id"]]
	allowed := len(st.scopes) == 0
	for _, sc := range st.scopes {
		allowed = allowed || contains(g.scopes, sc)
	}
	switch {
	case !known || in.Version != st.version:
		writeErr(w, 400, "unknown subscription type or version")
	case !found:
		writeErr(w, 400, "session does not exist or has already disconnected")
	case !allowed:
		writeErr(w, 403, "subscription missing proper authorization")
	case in.Type == "channel.chat.message" && in.Condition["user_id"] != g.userID:
		writeErr(w, 403, "user_id must match the token")
	case in.Type == "channel.follow" && in.Condition["moderator_user_id"] != g.userID:
		writeErr(w, 403, "moderator_user_id must match the token")
	case in.Condition[st.cond] == "":
		writeErr(w, 400, "missing condition "+st.cond)
	case s.chat.refused[in.Type]:
		writeErr(w, 403, "subscription missing proper authorization")
	case ss.subs[in.Type] != "":
		writeErr(w, 409, "subscription already exists")
	default:
		ss.subs[in.Type] = in.Condition[st.cond]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": random(8), "status": "enabled",
			"type": in.Type, "version": in.Version, "cost": 0}}, "total": 1, "total_cost": 0, "max_total_cost": 10})
	}
}

// Subscriptions lists the types sessions hold for channelLogin.
func (s *Server) Subscriptions(channelLogin string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	id := s.users[strings.ToLower(channelLogin)]
	seen := map[string]bool{}
	var out []string
	for _, ss := range s.chat.sessions {
		for t, c := range ss.subs {
			if c == id && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Notify delivers an event of type typ to sessions subscribed for
// channelLogin, as Twitch does, and returns its message id. A messageID
// given again is a redelivery.
func (s *Server) Notify(channelLogin, typ string, event map[string]any, messageID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	c := strings.ToLower(channelLogin)
	cid := s.userIDLocked(c)
	if messageID == "" {
		messageID = random(16)
	}
	ev := map[string]any{"broadcaster_user_id": cid, "broadcaster_user_login": c, "broadcaster_user_name": channelLogin}
	if typ == "channel.raid" {
		ev = map[string]any{"to_broadcaster_user_id": cid, "to_broadcaster_user_login": c, "to_broadcaster_user_name": channelLogin}
	}
	for k, v := range event {
		ev[k] = v
	}
	meta := map[string]any{"message_id": messageID, "message_type": "notification", "message_timestamp": s.now().UTC().Format(time.RFC3339Nano),
		"subscription_type": typ, "subscription_version": subTypes[typ].version}
	b, _ := json.Marshal(map[string]any{"metadata": meta, "payload": map[string]any{
		"subscription": map[string]any{"type": typ, "version": subTypes[typ].version, "status": "enabled"}, "event": ev}})
	for _, ss := range s.chat.sessions {
		if ss.subs[typ] == cid {
			ss.send(b)
		}
	}
	return messageID
}

// user is the event fields naming a viewer.
func (s *Server) user(login string) map[string]any {
	l := strings.ToLower(login)
	return map[string]any{"user_id": s.UserID(l), "user_login": l, "user_name": login}
}

func merge(a, b map[string]any) map[string]any {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// Follow: viewer follows channel.
func (s *Server) Follow(channel, viewer string) string {
	return s.Notify(channel, "channel.follow", merge(s.user(viewer), map[string]any{"followed_at": s.now().UTC().Format(time.RFC3339Nano)}), "")
}

// Subscribe: a new sub (gift true for someone given one).
func (s *Server) Subscribe(channel, viewer, tier string, gift bool) string {
	return s.Notify(channel, "channel.subscribe", merge(s.user(viewer), map[string]any{"tier": tier, "is_gift": gift}), "")
}

// Resub: a viewer shares their resub with a message.
func (s *Server) Resub(channel, viewer, tier string, months, streak int, text string) string {
	return s.Notify(channel, "channel.subscription.message", merge(s.user(viewer), map[string]any{
		"tier": tier, "message": map[string]any{"text": text, "emotes": []any{}},
		"cumulative_months": months, "streak_months": streak, "duration_months": 1}), "")
}

// Gift: a viewer (or someone anonymous, viewer "") gives total subs.
func (s *Server) Gift(channel, viewer, tier string, total int) string {
	ev := map[string]any{"tier": tier, "total": total, "is_anonymous": viewer == "", "cumulative_total": nil,
		"user_id": nil, "user_login": nil, "user_name": nil}
	if viewer != "" {
		merge(ev, s.user(viewer))
		ev["cumulative_total"] = total
	}
	return s.Notify(channel, "channel.subscription.gift", ev, "")
}

// Cheer: bits from a viewer (or anonymous, viewer "").
func (s *Server) Cheer(channel, viewer string, bits int, text string) string {
	ev := map[string]any{"bits": bits, "message": text, "is_anonymous": viewer == "", "user_id": nil, "user_login": nil, "user_name": nil}
	if viewer != "" {
		merge(ev, s.user(viewer))
	}
	return s.Notify(channel, "channel.cheer", ev, "")
}

// Raid: fromChannel raids channel with viewers.
func (s *Server) Raid(channel, fromChannel string, viewers int) string {
	f := strings.ToLower(fromChannel)
	return s.Notify(channel, "channel.raid", map[string]any{"from_broadcaster_user_id": s.UserID(f), "from_broadcaster_user_login": f,
		"from_broadcaster_user_name": fromChannel, "viewers": viewers}, "")
}

// Redeem: a viewer redeems a channel point reward. Returns the redemption id.
// A reward by that title on the channel (made through the API or with
// AddDashboardReward) gets a redemption its app can complete or refund.
func (s *Server) Redeem(channel, viewer, reward string, cost int, input string) string {
	id := random(12)
	s.mu.Lock()
	rewardID, rewardCost, status := s.redeemLocked(channel, viewer, reward, id, input, cost)
	s.mu.Unlock()
	s.Notify(channel, "channel.channel_points_custom_reward_redemption.add", merge(s.user(viewer), map[string]any{
		"id": id, "user_input": input, "status": status, "redeemed_at": s.now().UTC().Format(time.RFC3339Nano),
		"reward": map[string]any{"id": rewardID, "title": reward, "cost": rewardCost, "prompt": ""}}), "")
	return id
}

// HypeTrain sends a Hype Train event, v2, as Twitch's reference shows it:
// phase is begin, progress or end. A contributor is a viewer's login,
// with what they gave (bits or subscription) and how much. Returns the
// message id.
func (s *Server) HypeTrain(channel, phase, trainID string, level, total, progress, goal int, top ...HypeContribution) string {
	now := s.now().UTC()
	tops := []map[string]any{}
	for _, c := range top {
		u := s.user(c.Login)
		tops = append(tops, map[string]any{"user_id": u["user_id"], "user_login": u["user_login"], "user_name": u["user_name"],
			"type": c.Type, "total": c.Total})
	}
	e := map[string]any{"id": trainID, "total": total, "level": level, "top_contributions": tops, "shared_train_participants": nil,
		"started_at": now.Add(-time.Minute).Format(time.RFC3339Nano), "is_shared_train": false, "type": "regular"}
	if phase == "end" {
		e["ended_at"] = now.Format(time.RFC3339Nano)
		e["cooldown_ends_at"] = now.Add(time.Hour).Format(time.RFC3339Nano)
	} else {
		e["progress"], e["goal"] = progress, goal
		e["expires_at"] = now.Add(5 * time.Minute).Format(time.RFC3339Nano)
	}
	if phase == "begin" {
		e["all_time_high_level"], e["all_time_high_total"] = 4, 2500
	}
	return s.Notify(channel, "channel.hype_train."+phase, e, "")
}

// HypeContribution is one top contribution to a Hype Train.
type HypeContribution struct {
	Login string
	Type  string // bits, subscription or other
	Total int
}

// GoLive and GoOffline: the channel's stream starts or ends.
func (s *Server) GoLive(channel string) string {
	return s.Notify(channel, "stream.online", map[string]any{"id": random(8), "type": "live", "started_at": s.now().UTC().Format(time.RFC3339Nano)}, "")
}

func (s *Server) GoOffline(channel string) string {
	return s.Notify(channel, "stream.offline", map[string]any{}, "")
}

// UpdateChannel changes the channel's title and category, and says so.
func (s *Server) UpdateChannel(channel, title, category string) string {
	s.mu.Lock()
	s.chatInit()
	s.chat.channels[strings.ToLower(channel)] = channelInfo{title, category}
	s.mu.Unlock()
	return s.Notify(channel, "channel.update", map[string]any{"title": title, "language": "en", "category_id": "cat-" + strings.ToLower(category),
		"category_name": category, "content_classification_labels": []any{}}, "")
}

type channelInfo struct{ title, category string }

// usersLocked answers Get Users by id, with a profile picture on Twitch's
// picture host for each (the address isn't a real picture).
func (s *Server) usersLocked(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.helixAuth(w, r, ""); !ok {
		return
	}
	ids := r.URL.Query()["id"]
	if len(ids) == 0 || len(ids) > 100 {
		writeErr(w, 400, "between 1 and 100 id parameters")
		return
	}
	data := []map[string]string{}
	for _, id := range ids {
		data = append(data, map[string]string{"id": id, "login": "user" + id, "display_name": "User" + id,
			"profile_image_url": "https://static-cdn.jtvnw.net/jtv_user_pictures/mock-" + id + "-profile_image-300x300.png"})
	}
	writeJSON(w, map[string]any{"data": data})
}

// channelsLocked answers Get Channel Information.
func (s *Server) channelsLocked(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.helixAuth(w, r, ""); !ok {
		return
	}
	id := r.URL.Query().Get("broadcaster_id")
	data := []any{}
	for login, uid := range s.users {
		if uid == id {
			ch := s.chat.channels[login]
			data = append(data, map[string]any{"broadcaster_id": uid, "broadcaster_login": login, "broadcaster_name": login,
				"title": ch.title, "game_name": ch.category, "game_id": "cat-" + strings.ToLower(ch.category), "broadcaster_language": "en",
				"tags": []any{}, "content_classification_labels": []any{}, "is_branded_content": false, "delay": 0})
		}
	}
	writeJSON(w, map[string]any{"data": data})
}

// RevokeType revokes every session's subscription of typ for channelLogin.
func (s *Server) RevokeType(channelLogin, typ, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.users[strings.ToLower(channelLogin)]
	for _, ss := range s.chat.sessions {
		if ss.subs[typ] == id {
			delete(ss.subs, typ)
			ss.send(s.envelope("revocation", typ, map[string]any{
				"subscription": map[string]any{"type": typ, "version": subTypes[typ].version, "status": status},
			}))
		}
	}
}

// formEvent sends an event from the /chat page.
func (s *Server) formEvent(channel, viewer, kind, amount, text string) {
	n, err := strconv.Atoi(amount)
	if err != nil || n < 1 {
		n = 1
	}
	switch kind {
	case "follow":
		s.Follow(channel, viewer)
	case "sub":
		s.Subscribe(channel, viewer, "1000", false)
	case "resub":
		s.Resub(channel, viewer, "1000", n, n, text)
	case "gift":
		s.Gift(channel, viewer, "1000", 5)
	case "cheer":
		s.Cheer(channel, viewer, n, text)
	case "raid":
		s.Raid(channel, viewer, n)
	case "redeem":
		s.Redeem(channel, viewer, text, n, "")
	case "online":
		s.GoLive(channel)
	case "offline":
		s.GoOffline(channel)
	}
}
