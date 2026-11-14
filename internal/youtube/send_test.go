package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// reading signs you in, goes live and waits until chat is read.
func reading(t *testing.T, r *rig) (*Service, string) {
	t.Helper()
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	return s, b.ChatID
}

func waitRecords(t *testing.T, r *rig, typ string, n int) []events.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := youtubeChat(t, r, typ)
		if len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				t.Fatalf("%s: %d, want %d", typ, len(got), n)
			}
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSendNeedsASignInAndALiveBroadcast(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	if _, err := s.SendChat(context.Background(), SendRequest{Text: "hi"}); !errors.Is(err, ErrNotSignedIn) {
		t.Fatal(err)
	}
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
	if _, err := s.SendChat(context.Background(), SendRequest{Text: "hi"}); !errors.Is(err, ErrNotLive) {
		t.Fatal(err)
	}
	for _, bad := range []SendRequest{{Text: " "}, {Text: strings.Repeat("x", MaxSendRunes+1)}, {Text: "x", As: "nobody"}} {
		if _, err := s.SendChat(context.Background(), bad); !errors.Is(err, ErrInvalid) {
			t.Fatal(bad, err)
		}
	}
	if len(r.mock.Sent()) != 0 {
		t.Fatal("sent")
	}
}

func TestSendAsYouAndAsTheBot(t *testing.T) {
	r := newRig(t)
	s, chatID := reading(t, r)
	ctx := context.Background()

	// No bot: as you, with a note when the bot was asked for. What you type
	// in the app is you chatting.
	res, err := s.SendChat(ctx, SendRequest{Text: "from the app", As: SendBot})
	if err != nil || res.SentAs != RoleStreamer || res.Channel != "Streamer" || len(res.MessageIDs) != 1 || !strings.Contains(res.Note, "No bot") {
		t.Fatalf("%+v %v", res, err)
	}
	recs := waitRecords(t, r, "chat.message.received", 1)
	if p := payloadOf(t, recs[0]); p.Text != "from the app" || p.Chatter.Role != "broadcaster" {
		t.Fatalf("%+v", p)
	}

	// A workflow's message as you is stored as sent, never as chat.
	if _, err := s.SendChat(ctx, SendRequest{Text: "!hello from a workflow", As: SendStreamer, Workflow: "wf1"}); err != nil {
		t.Fatal(err)
	}
	sent := waitRecords(t, r, events.ChatSent, 1)
	if p := payloadOf(t, sent[0]); p.SentBy != "workflow" || p.Text != "!hello from a workflow" {
		t.Fatalf("%+v", p)
	}
	if n := len(youtubeChat(t, r, "chat.command.received")); n != 0 {
		t.Fatal("a workflow's own message made a command")
	}

	// The bot, once signed in.
	signIn(t, s.bot, "UCbot")
	waitFor(t, s.bot, StateConnected)
	res, err = s.SendChat(ctx, SendRequest{Text: "beep"})
	if err != nil || res.SentAs != RoleBot || res.Channel != "Bot" || res.Note != "" {
		t.Fatalf("%+v %v", res, err)
	}
	waitRecords(t, r, events.ChatSent, 2)
	got := r.mock.Sent()
	if len(got) != 3 || got[2].Channel != "UCbot" || got[2].ChatID != chatID {
		t.Fatalf("%+v", got)
	}

	// A banned bot: YouTube's refusal, said in words.
	r.mock.Ban("UCbot")
	if _, err := s.SendChat(ctx, SendRequest{Text: "again"}); err == nil || !strings.Contains(err.Error(), "doesn't let Bot post") {
		t.Fatal(err)
	}
}

func TestLongMessagesAreSplitAndCounted(t *testing.T) {
	r := newRig(t)
	s, _ := reading(t, r)
	before := s.Status(context.Background()).Quota.Used
	text := strings.Repeat("word ", 60) // 300 characters
	res, err := s.SendChat(context.Background(), SendRequest{Text: text})
	if err != nil || len(res.MessageIDs) != 2 || !strings.Contains(res.Note, "2 messages") {
		t.Fatalf("%+v %v", res, err)
	}
	for _, m := range r.mock.Sent() {
		if len([]rune(m.Text)) > MaxPartRunes || strings.HasPrefix(m.Text, " ") || strings.HasSuffix(m.Text, " ") {
			t.Fatalf("%q", m.Text)
		}
	}
	if used := s.Status(context.Background()).Quota.Used - before; used != 2*CostMessageInsert {
		t.Fatalf("used %d", used)
	}
	parts := splitMessage(strings.Repeat("x", 450), 200)
	if len(parts) != 3 || len(parts[0]) != 200 || len(parts[2]) != 50 {
		t.Fatalf("%v", parts)
	}
}

func TestSendWithTheQuotaSpent(t *testing.T) {
	r := newRig(t)
	s, _ := reading(t, r)
	r.mock.QuotaExceeded(true)
	if _, err := s.SendChat(context.Background(), SendRequest{Text: "hi"}); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatal(err)
	}
	calls := r.mock.Calls("liveChatMessages.insert")
	if _, err := s.SendChat(context.Background(), SendRequest{Text: "again"}); !errors.Is(err, ErrQuotaExhausted) ||
		r.mock.Calls("liveChatMessages.insert") != calls {
		t.Fatal("asked again with the quota spent", err)
	}
}

func TestYouTubeCommandsAndRoles(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	// Said before StreamGremlin was reading: stored, but no command.
	r.mock.Say(b.ChatID, "UCearly", "Early", "!hello earlier")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.Add(b.ChatID, livechat.Message{
		Snippet: livechat.Snippet{Type: livechat.TypeText, Text: "!so @friend now", AuthorChannelID: "UCmember"},
		Author:  livechat.Author{ChannelID: "UCmember", DisplayName: "Member", IsChatSponsor: true},
	})
	cmds := waitRecords(t, r, "chat.command.received", 1)
	waitMessages(t, r, 2)
	var p struct {
		Command string   `json:"command"`
		Args    string   `json:"args"`
		ArgList []string `json:"arg_list"`
		Chatter struct {
			Role         string `json:"role"`
			IsSubscriber bool   `json:"is_subscriber"`
			IsMember     bool   `json:"is_member"`
		} `json:"chatter"`
	}
	if err := jsonUnmarshal(cmds[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Command != "so" || p.Args != "@friend now" || len(p.ArgList) != 2 || p.Chatter.Role != "subscriber" ||
		!p.Chatter.IsSubscriber || !p.Chatter.IsMember {
		t.Fatalf("%+v", p)
	}
	if cmds[0].Actor.Platform != "youtube" || cmds[0].CausationID == "" {
		t.Fatalf("%+v", cmds[0])
	}
	// A drop and resume doesn't make the command twice.
	r.mock.DropStreams()
	r.mock.Say(b.ChatID, "UCv", "V", "after")
	waitMessages(t, r, 3)
	time.Sleep(50 * time.Millisecond)
	waitRecords(t, r, "chat.command.received", 1)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
