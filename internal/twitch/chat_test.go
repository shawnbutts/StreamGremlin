package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// chatRig signs in the streamer (and optionally a bot) and waits for chat.
func chatRig(t *testing.T, withBot bool) (*rig, *Service) {
	t.Helper()
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "Streamer")
	if withBot {
		r.signIn(s, RoleBot, "GremlinBot")
	}
	waitChat(t, s, ChatConnected)
	return r, s
}

func waitChat(t *testing.T, s *Service, want ChatState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.ChatStatus().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("chat %s (%s), want %s", s.ChatStatus().State, s.ChatStatus().Detail, want)
}

// waitChatSays waits for the chat state with a detail containing says:
// chat is off with no reason before the service works one out.
func waitChatSays(t *testing.T, s *Service, want ChatState, says string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.ChatStatus(); st.State == want && strings.Contains(st.Detail, says) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("chat %+v, want %s saying %q", s.ChatStatus(), want, says)
}

// waitEvents waits until the inbox holds n events, and returns them.
func waitEvents(t *testing.T, in *events.Inbox, n int) []events.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs, err := in.After(context.Background(), 0, 100, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) >= n || time.Now().After(deadline) {
			if len(recs) != n {
				t.Fatalf("%d events, want %d: %+v", len(recs), n, recs)
			}
			return recs
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func payload(t *testing.T, r events.Record) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChatMessagesAndCommandsReachTheInbox(t *testing.T) {
	r, _ := chatRig(t, false)
	r.mock.Say("streamer", "SomeViewer", "hello gremlins")
	r.mock.SetModerator("streamer", "modfriend", true)
	r.mock.Say("streamer", "ModFriend", "!SO @someviewer  now")
	recs := waitEvents(t, r.inbox, 3)

	msg := recs[0]
	p := payload(t, msg)
	chatter := p["chatter"].(map[string]any)
	if msg.Type != "chat.message.received" || msg.Source != "twitch" || msg.DeliveryQuality != events.Confirmed ||
		p["text"] != "hello gremlins" || chatter["login"] != "someviewer" || chatter["role"] != "viewer" ||
		msg.Actor.DisplayName != "SomeViewer" || msg.SourceEventID != p["message_id"] || msg.ChannelID == "" {
		t.Fatalf("%+v %v", msg, p)
	}
	cmd := recs[2]
	cp := payload(t, cmd)
	if recs[1].Type != "chat.message.received" || cmd.Type != "chat.command.received" || cp["command"] != "so" ||
		cp["args"] != "@someviewer  now" || cmd.CausationID != recs[1].EventID || cmd.SourceEventID != "cmd:"+recs[1].SourceEventID ||
		cp["chatter"].(map[string]any)["is_moderator"] != true {
		t.Fatalf("%+v %v", cmd, cp)
	}
}

func TestTheBotsOwnMessagesAreNotViewerChat(t *testing.T) {
	r, s := chatRig(t, true)
	res, err := s.SendChat(context.Background(), SendRequest{Text: "I am the bot"})
	if err != nil || res.SentAs != RoleBot || res.Login != "gremlinbot" || res.MessageID == "" {
		t.Fatalf("%+v %v", res, err)
	}
	log := r.mock.ChatLog()
	if len(log) != 1 || log[0].ChatterLogin != "gremlinbot" || log[0].Text != "I am the bot" {
		t.Fatalf("%+v", log)
	}
	r.mock.Say("streamer", "viewer", "after the bot")
	// The bot's echo is kept for the Live screen as its own type, never as
	// viewer chat (D-087); only the viewer's message is chat.
	recs := waitEvents(t, r.inbox, 2)
	if recs[0].Type != events.ChatSent || payload(t, recs[0])["sent_by"] != "bot" || payload(t, recs[0])["text"] != "I am the bot" ||
		recs[1].Type != "chat.message.received" || payload(t, recs[1])["text"] != "after the bot" {
		t.Fatal(recs)
	}
	if m := s.ChatStatus().BotModerator; m == nil || *m {
		t.Fatalf("bot moderator %v, want known false", m)
	}
}

func TestBotModeratorStatusRaisesItsLimit(t *testing.T) {
	r := newRig(t)
	r.mock.SetModerator("streamer", "gremlinbot", true)
	s := r.configured()
	r.signIn(s, RoleStreamer, "Streamer")
	r.signIn(s, RoleBot, "GremlinBot")
	waitChat(t, s, ChatConnected)
	deadline := time.Now().Add(5 * time.Second)
	for m := s.ChatStatus().BotModerator; m == nil || !*m; m = s.ChatStatus().BotModerator {
		if time.Now().After(deadline) {
			t.Fatal("never learned the bot is a moderator")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// As a moderator it isn't held to one message a second.
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := s.SendChat(context.Background(), SendRequest{Text: "fast"}); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Fatalf("a moderator bot waited: %v", time.Since(start))
	}
}

func TestSendingAsYouAndFallbacks(t *testing.T) {
	r, s := chatRig(t, false)
	ctx := context.Background()
	res, err := s.SendChat(ctx, SendRequest{Text: "from me", As: SendBot})
	if err != nil || res.SentAs != RoleStreamer || !strings.Contains(res.Note, "No bot account") {
		t.Fatalf("%+v %v", res, err)
	}
	// Your own messages are real chat.
	if recs := waitEvents(t, r.inbox, 1); payload(t, recs[0])["text"] != "from me" {
		t.Fatal(recs)
	}
	for name, req := range map[string]SendRequest{
		"empty":   {Text: "   "},
		"too big": {Text: strings.Repeat("é", MaxChatRunes+1)},
		"as who":  {Text: "x", As: "mod"},
	} {
		if _, err := s.SendChat(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.SendChat(ctx, SendRequest{Text: strings.Repeat("é", MaxChatRunes), As: SendStreamer}); err != nil {
		t.Fatalf("500 characters: %v", err)
	}
	r.mock.DropNext("msg_rejected")
	var drop *DropError
	if _, err := s.SendChat(ctx, SendRequest{Text: "held", As: SendStreamer}); !errors.As(err, &drop) || drop.Code != "msg_rejected" {
		t.Fatalf("dropped: %v", err)
	}
}

func TestARefusedBotSendsAsYouInstead(t *testing.T) {
	r, s := chatRig(t, true)
	r.mock.Disconnect("gremlinbot") // the bot's token stops working
	res, err := s.SendChat(context.Background(), SendRequest{Text: "still gets through"})
	if err != nil || res.SentAs != RoleStreamer || !strings.Contains(res.Note, "bot's sign-in") {
		t.Fatalf("%+v %v", res, err)
	}
	waitFor(t, s.bot, StateNeedsSignIn)
}

func TestNotSignedInCantSend(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	if _, err := s.SendChat(context.Background(), SendRequest{Text: "hi"}); !errors.Is(err, ErrNotSignedIn) {
		t.Fatal(err)
	}
	waitChatSays(t, s, ChatOff, "Sign in")
}

func TestChatSurvivesReconnectWithoutDuplicates(t *testing.T) {
	r, s := chatRig(t, false)
	r.mock.Say("streamer", "viewer", "before")
	waitEvents(t, r.inbox, 1)
	r.mock.Reconnect()
	time.Sleep(400 * time.Millisecond) // the handover and the old connection closing
	if s.ChatStatus().State != ChatConnected {
		t.Fatalf("%+v", s.ChatStatus())
	}
	r.mock.Say("streamer", "viewer", "after")
	recs := waitEvents(t, r.inbox, 2)
	if payload(t, recs[1])["text"] != "after" || r.mock.Subscribed("streamer") != 1 {
		t.Fatalf("%+v, %d subscribed", recs, r.mock.Subscribed("streamer"))
	}
}

func TestChatReconnectsAfterADrop(t *testing.T) {
	r, s := chatRig(t, false)
	changes := s.Changes()
	r.mock.DropConnections()
	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("no change after the drop")
	}
	waitChat(t, s, ChatConnected) // resubscribed on a new session
	r.mock.Say("streamer", "viewer", "back again")
	if recs := waitEvents(t, r.inbox, 1); payload(t, recs[0])["text"] != "back again" {
		t.Fatal(recs)
	}
}

func TestSilentConnectionIsNoticed(t *testing.T) {
	r := newRig(t)
	r.mock.SetKeepalive(time.Second) // EventSub counts in whole seconds
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	waitChat(t, s, ChatConnected)
	r.mock.Silence()
	waitChatSays(t, s, ChatDisconnected, "no keepalive")
	if !strings.Contains(s.ChatStatus().Detail, "no keepalive") || !strings.Contains(s.ChatStatus().Detail, "doesn't resend") {
		t.Fatal(s.ChatStatus())
	}
	waitChat(t, s, ChatConnected)
}

func TestRevocationEndsChatAndChecksTheSignIn(t *testing.T) {
	r, s := chatRig(t, false)
	r.mock.Disconnect("streamer")
	r.mock.Revoke("streamer", "authorization_revoked")
	waitFor(t, s.streamer, StateNeedsSignIn)
	waitChat(t, s, ChatOff)
}

func TestSigningOutStopsChat(t *testing.T) {
	r, s := chatRig(t, false)
	if err := s.streamer.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitChat(t, s, ChatOff)
	deadline := time.Now().Add(3 * time.Second)
	for r.mock.Subscribed("streamer") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the session stayed open")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestChatNeedsTheReadScope(t *testing.T) {
	r := newRig(t)
	r.mock.GrantScopes = []string{"user:write:chat"}
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	waitChatSays(t, s, ChatOff, "allow reading chat")
}

// TestTwitchExamplePayload reads the channel.chat.message example from
// Twitch's EventSub reference.
func TestTwitchExamplePayload(t *testing.T) {
	raw := `{"broadcaster_user_id":"1971641","broadcaster_user_login":"streamer","broadcaster_user_name":"streamer",
	"chatter_user_id":"4145994","chatter_user_login":"viewer32","chatter_user_name":"viewer32",
	"message_id":"cc106a89-1814-919d-454c-f4f2f970aae7",
	"message":{"text":"Hi chat","fragments":[{"type":"text","text":"Hi chat","cheermote":null,"emote":null,"mention":null}]},
	"color":"#00FF7F","badges":[{"set_id":"moderator","id":"1","info":""},{"set_id":"subscriber","id":"12","info":"16"},{"set_id":"sub-gifter","id":"1","info":""}],
	"message_type":"text","cheer":null,"reply":null,"channel_points_custom_reward_id":null,
	"source_broadcaster_user_id":null,"source_broadcaster_user_login":null,"source_broadcaster_user_name":null,
	"source_message_id":null,"source_badges":null}`
	var ev chatEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	msg, cmd := chatEnvelopes(ev, time.Now())
	if cmd != nil {
		t.Fatal("not a command")
	}
	var p map[string]any
	_ = json.Unmarshal(msg.Payload, &p)
	c := p["chatter"].(map[string]any)
	if c["role"] != "moderator" || c["is_subscriber"] != true || c["is_broadcaster"] != false || p["color"] != "#00FF7F" {
		t.Fatal(p)
	}
	if e, ok := p["emotes"].([]any); !ok || len(e) != 0 {
		t.Fatalf("emotes %v", p["emotes"])
	}
	// Emotes' names, once per use, for the emote filter (D-085).
	if err := json.Unmarshal([]byte(`{"text":"Kappa hi Kappa","fragments":[{"type":"emote","text":"Kappa"},
		{"type":"text","text":" hi "},{"type":"emote","text":"Kappa"}]}`), &ev.Message); err != nil {
		t.Fatal(err)
	}
	msg, _ = chatEnvelopes(ev, time.Now())
	_ = json.Unmarshal(msg.Payload, &p)
	if e, _ := p["emotes"].([]any); len(e) != 2 || e[0] != "Kappa" {
		t.Fatalf("emotes %v", p["emotes"])
	}
	// In a shared chat, another channel's "!command" isn't ours.
	ev.Message.Text, ev.SourceBroadcasterUserID = "!lurk", "999"
	if _, cmd := chatEnvelopes(ev, time.Now()); cmd != nil {
		t.Fatal("a shared-chat message made a command")
	}
}

func TestLimiter(t *testing.T) {
	var l limiter
	now := time.Unix(1000, 0)
	for i := 0; i < 18; i++ {
		wait, ok := l.reserve("bot", now.Add(time.Duration(i)*time.Second), 18, true, 3*time.Second)
		if !ok || wait != 0 {
			t.Fatalf("send %d: %v %v", i, wait, ok)
		}
	}
	if _, ok := l.reserve("bot", now.Add(18*time.Second), 18, true, 3*time.Second); ok {
		t.Fatal("19th message within 30 seconds")
	}
	if _, ok := l.reserve("bot", now.Add(31*time.Second), 18, true, 3*time.Second); !ok {
		t.Fatal("the window should have moved on")
	}
	// One a second: the next waits for its slot.
	var m limiter
	m.reserve("bot", now, 18, true, 3*time.Second)
	if wait, ok := m.reserve("bot", now.Add(300*time.Millisecond), 18, true, 3*time.Second); !ok || wait != 700*time.Millisecond {
		t.Fatalf("%v %v", wait, ok)
	}
	if _, ok := m.reserve("bot", now.Add(300*time.Millisecond), 18, true, 1*time.Second); ok {
		t.Fatal("a slot beyond the longest wait was handed out")
	}
}
