package mocktwitch

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// session is one EventSub WebSocket connection.
type session struct {
	id        string
	out       chan []byte
	subs      map[string]string // subscription type -> broadcaster user id
	keepalive time.Duration
	quiet     bool // stop sending keepalives, to test a silent drop
	done      chan struct{}
	closeOnce sync.Once
	closeCode websocket.StatusCode
}

func (ss *session) close(code websocket.StatusCode) {
	ss.closeOnce.Do(func() {
		ss.closeCode = code
		close(ss.done)
	})
}

// ChatMessage is a message the mock delivered, for tests and the /chat page.
type ChatMessage struct {
	Channel, ChatterLogin, Text, MessageID string
}

type chatState struct {
	sessions      map[string]*session
	mods          map[string]map[string]bool // channel login -> moderator logins
	log           []ChatMessage
	dropNext      string // when set, the next send answers is_sent false with this code
	keepaliveFor  time.Duration
	subscribeWait time.Duration
	refused       map[string]bool        // subscription types answered 403
	channels      map[string]channelInfo // channel login -> title and category
}

func (s *Server) chatInit() {
	if s.chat.sessions == nil {
		s.chat.sessions = map[string]*session{}
		s.chat.mods = map[string]map[string]bool{}
		s.chat.subscribeWait = 10 * time.Second
		s.chat.refused = map[string]bool{}
		s.chat.channels = map[string]channelInfo{}
	}
}

// SetKeepalive overrides the keepalive interval sessions get, for tests.
func (s *Server) SetKeepalive(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	s.chat.keepaliveFor = d
}

// SetModerator makes userLogin a moderator in channelLogin's chat.
func (s *Server) SetModerator(channelLogin, userLogin string, mod bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	c, u := strings.ToLower(channelLogin), strings.ToLower(userLogin)
	if s.chat.mods[c] == nil {
		s.chat.mods[c] = map[string]bool{}
	}
	s.chat.mods[c][u] = mod
}

// DropNext makes the next sent message come back with is_sent false.
func (s *Server) DropNext(code string) {
	s.mu.Lock()
	s.chatInit()
	s.chat.dropNext = code
	s.mu.Unlock()
}

// ChatLog lists every message delivered to chat, oldest first.
func (s *Server) ChatLog() []ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChatMessage(nil), s.chat.log...)
}

// Subscribed reports how many sessions read channelLogin's chat.
func (s *Server) Subscribed(channelLogin string) int {
	return s.SubscribedTo(channelLogin, "channel.chat.message")
}

// SubscribedTo reports how many sessions follow one EventSub type for
// channelLogin.
func (s *Server) SubscribedTo(channelLogin, typ string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	id := s.users[strings.ToLower(channelLogin)]
	n := 0
	for _, ss := range s.chat.sessions {
		if ss.subs[typ] == id && id != "" {
			n++
		}
	}
	return n
}

// Say delivers a chat message from chatterLogin in channelLogin's chat, as a
// viewer typing in Twitch.
func (s *Server) Say(channelLogin, chatterLogin, text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sayLocked(channelLogin, chatterLogin, text)
}

func (s *Server) sayLocked(channelLogin, chatterLogin, text string) string {
	s.chatInit()
	c, u := strings.ToLower(channelLogin), strings.ToLower(chatterLogin)
	cid, uid := s.userIDLocked(c), s.userIDLocked(u)
	badges := []map[string]string{}
	if c == u {
		badges = append(badges, map[string]string{"set_id": "broadcaster", "id": "1", "info": ""})
	}
	if s.chat.mods[c][u] {
		badges = append(badges, map[string]string{"set_id": "moderator", "id": "1", "info": ""})
	}
	if b, ok := s.subBadgeLocked(c, u); ok {
		badges = append(badges, b)
	}
	msgID := random(16)
	ev := map[string]any{
		"broadcaster_user_id": cid, "broadcaster_user_login": c, "broadcaster_user_name": channelLogin,
		"chatter_user_id": uid, "chatter_user_login": u, "chatter_user_name": chatterLogin,
		"message_id": msgID, "message": map[string]any{"text": text, "fragments": fragments(text)},
		"message_type": "text", "color": "", "badges": badges,
		"cheer": nil, "reply": nil, "channel_points_custom_reward_id": nil,
		"source_broadcaster_user_id": nil,
	}
	s.chat.log = append(s.chat.log, ChatMessage{Channel: c, ChatterLogin: u, Text: text, MessageID: msgID})
	for _, ss := range s.chat.sessions {
		if ss.subs["channel.chat.message"] == cid {
			ss.send(s.envelope("notification", "channel.chat.message", map[string]any{
				"subscription": map[string]any{"type": "channel.chat.message", "version": "1", "status": "enabled"},
				"event":        ev,
			}))
		}
	}
	return msgID
}

// mockEmotes are the words the stand-in Twitch sends as emotes.
var mockEmotes = map[string]bool{"Kappa": true, "KEKW": true, "LUL": true, "PogChamp": true}

// fragments splits a message as Twitch does: emotes apart from the text
// around them.
func fragments(text string) []any {
	out := []any{}
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out = append(out, map[string]any{"type": "text", "text": plain.String()})
			plain.Reset()
		}
	}
	for i, w := range strings.Split(text, " ") {
		if i > 0 {
			plain.WriteString(" ")
		}
		if mockEmotes[w] {
			flush()
			out = append(out, map[string]any{"type": "emote", "text": w, "emote": map[string]any{"id": "1"}})
			continue
		}
		plain.WriteString(w)
	}
	flush()
	return out
}

// Reconnect asks every session to move to a new connection, as Twitch does
// before maintenance. Subscriptions carry over.
func (s *Server) Reconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.chat.sessions {
		u := strings.Replace(s.base, "http", "ws", 1) + "/ws?reconnect=" + ss.id
		ss.send(s.envelope("session_reconnect", "", map[string]any{"session": map[string]any{
			"id": ss.id, "status": "reconnecting", "keepalive_timeout_seconds": nil, "reconnect_url": u,
		}}))
	}
}

// Revoke sends a revocation for every chat subscription of channelLogin.
func (s *Server) Revoke(channelLogin, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.users[strings.ToLower(channelLogin)]
	for _, ss := range s.chat.sessions {
		if ss.subs["channel.chat.message"] == id {
			delete(ss.subs, "channel.chat.message")
			ss.send(s.envelope("revocation", "channel.chat.message", map[string]any{
				"subscription": map[string]any{"type": "channel.chat.message", "version": "1", "status": status},
			}))
		}
	}
}

// DropConnections closes every session at once, as a network failure would.
func (s *Server) DropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ss := range s.chat.sessions {
		ss.close(websocket.StatusGoingAway)
		delete(s.chat.sessions, id)
	}
}

// Silence stops keepalives on every session, so the client must notice by itself.
func (s *Server) Silence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.chat.sessions {
		ss.quiet = true
	}
}

func (ss *session) send(b []byte) {
	select {
	case ss.out <- b:
	default: // a stuck client: drop, as a real server would close it
	}
}

func (s *Server) envelope(kind, subType string, payload any) []byte {
	meta := map[string]any{"message_id": random(16), "message_type": kind, "message_timestamp": s.now().UTC().Format(time.RFC3339Nano)}
	if subType != "" {
		meta["subscription_type"], meta["subscription_version"] = subType, "1"
	}
	b, _ := json.Marshal(map[string]any{"metadata": meta, "payload": payload})
	return b
}

// ws serves the EventSub WebSocket.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	keepalive := 10 * time.Second
	if v, err := strconv.Atoi(r.URL.Query().Get("keepalive_timeout_seconds")); err == nil {
		keepalive = time.Duration(min(max(v, 10), 600)) * time.Second
	}
	s.mu.Lock()
	s.chatInit()
	if s.chat.keepaliveFor > 0 {
		keepalive = s.chat.keepaliveFor
	}
	ss := &session{id: random(12), out: make(chan []byte, 256), subs: map[string]string{}, keepalive: keepalive, done: make(chan struct{})}
	if old, ok := s.chat.sessions[r.URL.Query().Get("reconnect")]; ok {
		for k, v := range old.subs {
			ss.subs[k] = v // subscriptions carry over
		}
		go func() { // the old connection closes after the new welcome
			time.Sleep(200 * time.Millisecond)
			s.mu.Lock()
			delete(s.chat.sessions, old.id)
			s.mu.Unlock()
			old.close(websocket.StatusNormalClosure)
		}()
	}
	s.chat.sessions[ss.id] = ss
	wait := s.chat.subscribeWait
	welcome := s.envelope("session_welcome", "", map[string]any{"session": map[string]any{
		"id": ss.id, "status": "connected", "connected_at": s.now().UTC().Format(time.RFC3339Nano),
		"keepalive_timeout_seconds": int(keepalive.Seconds()), "reconnect_url": nil,
	}})
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.chat.sessions[ss.id] == ss {
			delete(s.chat.sessions, ss.id)
		}
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() { // the client may only answer pings; anything else closes it (4001)
		for {
			if _, _, err := c.Read(ctx); err != nil {
				cancel()
				return
			}
			ss.close(4001)
		}
	}()
	write := func(b []byte) bool {
		wctx, wc := context.WithTimeout(ctx, 5*time.Second)
		defer wc()
		return c.Write(wctx, websocket.MessageText, b) == nil
	}
	if !write(welcome) {
		return
	}
	unused := time.NewTimer(wait)
	defer unused.Stop()
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ss.done:
			_ = c.Close(ss.closeCode, "")
			return
		case <-unused.C:
			s.mu.Lock()
			empty := len(ss.subs) == 0
			s.mu.Unlock()
			if empty {
				_ = c.Close(4003, "connection unused")
				return
			}
		case b := <-ss.out:
			if !write(b) {
				return
			}
			tick.Reset(keepalive)
		case <-tick.C:
			s.mu.Lock()
			quiet := ss.quiet
			s.mu.Unlock()
			if !quiet && !write(s.envelope("session_keepalive", "", map[string]any{})) {
				return
			}
		}
	}
}

// helixAuth checks a Helix request's Client-Id and bearer token.
func (s *Server) helixAuth(w http.ResponseWriter, r *http.Request, scope string) (*grant, bool) {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	g, ok := s.access[tok]
	switch {
	case !ok || !s.now().Before(g.expires):
		writeErr(w, 401, "Invalid OAuth token")
	case r.Header.Get("Client-Id") != g.clientID:
		writeErr(w, 401, "Client ID and OAuth token do not match")
	case scope != "" && !contains(g.scopes, scope):
		writeErr(w, 401, "Missing scope: "+scope)
	default:
		return g, true
	}
	return nil, false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) helix(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	switch {
	case r.Method == "POST" && r.URL.Path == "/helix/eventsub/subscriptions":
		s.subscribeLocked(w, r)
	case r.Method == "GET" && r.URL.Path == "/helix/channels":
		s.channelsLocked(w, r)
	case r.Method == "GET" && r.URL.Path == "/helix/channels/followers":
		s.followersLocked(w, r)
	case r.Method == "GET" && r.URL.Path == "/helix/chat/chatters":
		s.chattersLocked(w, r)
	case r.Method == "GET" && r.URL.Path == "/helix/subscriptions":
		s.subscriptionsLocked(w, r)
	case r.Method == "GET" && r.URL.Path == "/helix/chat/settings":
		s.getChatSettingsLocked(w, r)
	case s.moderateLocked(w, r):
	case s.shieldLocked(w, r):
	case s.broadcastLocked(w, r):
	case s.blockedTermsLocked(w, r):
	case r.Method == "GET" && r.URL.Path == "/helix/channels/vips":
		s.cardListLocked(w, r, "channel:read:vips", "channel:manage:vips")
	case r.Method == "GET" && r.URL.Path == "/helix/moderation/moderators":
		s.cardListLocked(w, r, "moderation:read", "channel:manage:moderators")
	case r.Method == "GET" && r.URL.Path == "/helix/moderation/banned":
		s.cardListLocked(w, r, "moderation:read", "moderator:manage:banned_users")
	case r.Method == "GET" && r.URL.Path == "/helix/users":
		s.usersLocked(w, r)
	case r.Method == "POST" && r.URL.Path == "/helix/channels/commercial":
		s.commercial(w, r)
	case r.Method == "GET" && r.URL.Path == "/helix/channels/ads":
		s.adSchedule(w, r)
	case r.Method == "POST" && r.URL.Path == "/helix/channels/ads/schedule/snooze":
		s.snooze(w, r)
	case r.URL.Path == "/helix/polls":
		s.polls(w, r)
	case r.URL.Path == "/helix/predictions":
		s.predictions(w, r)
	case r.URL.Path == "/helix/channel_points/custom_rewards":
		s.customRewards(w, r)
	case r.URL.Path == "/helix/channel_points/custom_rewards/redemptions":
		s.customRedemptions(w, r)
	case r.Method == "POST" && r.URL.Path == "/helix/chat/messages":
		var in struct {
			BroadcasterID string `json:"broadcaster_id"`
			SenderID      string `json:"sender_id"`
			Message       string `json:"message"`
			ReplyTo       string `json:"reply_parent_message_id"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.BroadcasterID == "" || in.Message == "" {
			writeErr(w, 400, "missing broadcaster_id, sender_id or message")
			return
		}
		g, ok := s.helixAuth(w, r, "user:write:chat")
		if !ok {
			return
		}
		if in.SenderID != g.userID {
			writeErr(w, 400, "sender_id must match the token")
			return
		}
		if utf8.RuneCountInString(in.Message) > 500 {
			writeErr(w, 422, "The message is too large.")
			return
		}
		res := map[string]any{"message_id": "", "is_sent": false}
		if s.chat.dropNext != "" {
			res["drop_reason"] = map[string]string{"code": s.chat.dropNext, "message": "Your message was held by mocktwitch."}
			s.chat.dropNext = ""
		} else {
			channel := ""
			for login, id := range s.users {
				if id == in.BroadcasterID {
					channel = login
				}
			}
			res["message_id"] = s.sayLocked(channel, g.login, in.Message)
			res["is_sent"] = true
		}
		writeJSON(w, map[string]any{"data": []any{res}})
	case r.Method == "GET" && r.URL.Path == "/helix/moderation/channels":
		g, ok := s.helixAuth(w, r, "user:read:moderated_channels")
		if !ok {
			return
		}
		if r.URL.Query().Get("user_id") != g.userID {
			writeErr(w, 400, "user_id must match the token")
			return
		}
		data := []any{}
		for channel, mods := range s.chat.mods {
			if mods[g.login] {
				data = append(data, map[string]string{"broadcaster_id": s.userIDLocked(channel), "broadcaster_login": channel, "broadcaster_name": channel})
			}
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	default:
		writeErr(w, 404, "Not Found")
	}
}

var chatPage = template.Must(template.New("c").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>mocktwitch: chat</title>
<style>body{font:16px system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem}input,button{font:inherit;padding:.4rem .6rem}
.note{background:#fff3cd;padding:.6rem .8rem;border-radius:6px}li{margin:.2rem 0}</style></head><body>
<p class="note">This is <strong>mocktwitch</strong>, a test stand-in for Twitch chat.</p>
<h1>Chat</h1>
<form method="post" action="/chat">
<p><label>Channel <input name="channel" value="{{.Channel}}"></label>
<label>Viewer <input name="viewer" value="{{.Viewer}}"></label></p>
<p><input name="text" size="50" autofocus> <button>Send</button></p>
</form>
<h2>Send an event</h2>
<form method="post" action="/chat">
<input type="hidden" name="channel" value="{{.Channel}}">
<p><select name="event">
<option value="follow">Follow</option><option value="sub">New sub</option><option value="resub">Resub</option>
<option value="gift">Gift 5 subs</option><option value="cheer">Cheer</option><option value="raid">Raid</option>
<option value="redeem">Redeem a reward</option><option value="online">Go live</option><option value="offline">Go offline</option>
</select>
<label>From <input name="viewer" value="{{.Viewer}}"></label>
<label>Amount <input name="amount" value="100" size="5"></label>
<label>Reward or text <input name="text" value="Hydrate"></label>
<button>Send event</button></p>
</form>
<ol>{{range .Log}}<li><strong>{{.ChatterLogin}}</strong> in {{.Channel}}: {{.Text}}</li>{{end}}</ol>
</body></html>`))

// chatForm lets a person type chat as any viewer, in standalone use.
func (s *Server) chatForm(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Channel, Viewer string
		Log             []ChatMessage
	}{Channel: "teststreamer", Viewer: "testviewer"}
	if r.Method == "POST" {
		_ = r.ParseForm()
		data.Channel, data.Viewer = r.PostForm.Get("channel"), r.PostForm.Get("viewer")
		if ev := r.PostForm.Get("event"); ev != "" {
			s.formEvent(data.Channel, data.Viewer, ev, r.PostForm.Get("amount"), r.PostForm.Get("text"))
		} else if t := strings.TrimSpace(r.PostForm.Get("text")); t != "" {
			s.Say(data.Channel, data.Viewer, t)
		}
	}
	data.Log = s.ChatLog()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = chatPage.Execute(w, data)
}
