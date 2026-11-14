package youtube

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

func TestCheckModRefusesBadRequests(t *testing.T) {
	for _, m := range []Mod{
		{Action: "warn", ChannelID: "UCx"},
		{Action: ModDelete},
		{Action: ModDelete, MessageID: "a b"},
		{Action: ModTimeout, ChannelID: "UCx"},
		{Action: ModTimeout, ChannelID: "UCx", Seconds: MaxTimeout + 1},
		{Action: ModBan, ChannelID: "UCx", Seconds: 5},
		{Action: ModBan, ChannelID: "bad id"},
		{Action: ModUnban, ChannelID: "UCx"},
		{Action: ModBan, ChannelID: "UCx", BanID: "b1"},
		{Action: ModMod, ChannelID: "UCx", ModeratorID: "m1"},
	} {
		if err := CheckMod(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", m, err)
		}
	}
	for _, m := range []Mod{
		{Action: ModDelete, MessageID: "LCC.Cg8KDQoLX3Rlc3QtaWQ"},
		{Action: ModTimeout, ChannelID: "UC_x-1", Seconds: 300},
		{Action: ModUnban, ChannelID: "UCx", BanID: "ban1"},
		{Action: ModUnmod, ChannelID: "UCx"},
	} {
		if err := CheckMod(m); err != nil {
			t.Errorf("%+v: %v", m, err)
		}
	}
}

func TestModerateNeedsALiveChat(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	o, err := s.Moderate(context.Background(), Mod{Action: ModTimeout, ChannelID: "UCfan", Seconds: 60})
	if err != nil || o.State != ModRefused || !strings.Contains(o.Detail, "not live") {
		t.Fatalf("%+v %v", o, err)
	}
	if r.mock.Calls("liveChatBans.insert") != 0 {
		t.Fatal("sent")
	}
}

func TestModerateDeleteBanAndUndo(t *testing.T) {
	r := newRig(t)
	s, chatID := reading(t, r)
	ctx := context.Background()
	msg := r.mock.Say(chatID, "UCfan", "Fan", "spam spam")
	waitMessages(t, r, 1)
	used := s.quota.status(ctx).Used

	// Delete: 20 units; Live learns it once, whether from StreamGremlin or
	// the tombstone.
	o, err := s.Moderate(ctx, Mod{Action: ModDelete, MessageID: msg})
	if err != nil || o.State != ModDone {
		t.Fatalf("%+v %v", o, err)
	}
	if got := r.mock.Deleted(); len(got) != 1 || got[0] != msg {
		t.Fatalf("%v", got)
	}
	del := waitRecords(t, r, events.ChatDeleted, 1)
	if p := payloadMap(t, del[0]); p["message_id"] != msg {
		t.Fatalf("%+v", p)
	}
	// Again: YouTube says it isn't there.
	if o, _ := s.Moderate(ctx, Mod{Action: ModDelete, MessageID: msg}); o.State != ModRefused || !strings.Contains(o.Detail, "already") {
		t.Fatalf("%+v", o)
	}

	// Timeout: 200 units, YouTube's ban id kept; the chat shows it.
	o, err = s.Moderate(ctx, Mod{Action: ModTimeout, ChannelID: "UCfan", Seconds: 600})
	if err != nil || o.State != ModDone || o.ID == "" || !r.mock.Banned("UCfan") {
		t.Fatalf("%+v %v", o, err)
	}
	to := waitRecords(t, r, TypeTimedOut, 1)
	if p := payloadMap(t, to[0]); p["duration_seconds"] != 600.0 || to[0].Actor.ID != "UCfan" {
		t.Fatalf("%+v", p)
	}
	if u, _ := s.Moderate(ctx, Mod{Action: ModUnban, ChannelID: "UCfan", BanID: o.ID}); u.State != ModDone || r.mock.Banned("UCfan") {
		t.Fatalf("%+v", u)
	}
	if u, _ := s.Moderate(ctx, Mod{Action: ModUnban, ChannelID: "UCfan", BanID: o.ID}); u.State != ModRefused {
		t.Fatalf("undone twice: %+v", u)
	}
	if spent := s.quota.status(ctx).Used - used; spent != 20+20+200+200+200 {
		t.Fatalf("spent %d units", spent)
	}
}

func TestModeratorsAddedAndRemoved(t *testing.T) {
	r := newRig(t)
	s, chatID := reading(t, r)
	ctx := context.Background()
	o, err := s.Moderate(ctx, Mod{Action: ModMod, ChannelID: "UCfan"})
	if err != nil || o.State != ModDone || o.ID == "" {
		t.Fatalf("%+v %v", o, err)
	}
	// A moderator can't be banned.
	if b, _ := s.Moderate(ctx, Mod{Action: ModBan, ChannelID: "UCfan"}); b.State != ModRefused || !strings.Contains(b.Detail, "moderator") {
		t.Fatalf("%+v", b)
	}
	// Removed by the id YouTube gave: no list asked.
	if u, _ := s.Moderate(ctx, Mod{Action: ModUnmod, ChannelID: "UCfan", ModeratorID: o.ID}); u.State != ModDone {
		t.Fatalf("%+v", u)
	}
	if r.mock.Calls("liveChatModerators.list") != 0 || len(r.mock.Moderators(chatID)) != 0 {
		t.Fatal("listed, or still a moderator")
	}
	// One added in YouTube Studio is looked up (50 units), once a while.
	r.mock.AddModerator(chatID, "UCbot")
	if u, _ := s.Moderate(ctx, Mod{Action: ModUnmod, ChannelID: "UCbot"}); u.State != ModDone {
		t.Fatalf("%+v", u)
	}
	if u, _ := s.Moderate(ctx, Mod{Action: ModUnmod, ChannelID: "UCnobody"}); u.State != ModRefused || !strings.Contains(u.Detail, "aren't a moderator") {
		t.Fatalf("%+v", u)
	}
	if n := r.mock.Calls("liveChatModerators.list"); n != 2 {
		t.Fatalf("listed %d times: removing forgets the list", n)
	}
	if list, err := s.Moderators(ctx); err != nil || len(list) != 0 || r.mock.Calls("liveChatModerators.list") != 2 {
		t.Fatalf("%+v %v: the list is kept a while", list, err)
	}
}

func TestModerateWithTheQuotaSpentOrNoAnswer(t *testing.T) {
	r := newRig(t)
	s, _ := reading(t, r)
	ctx := context.Background()
	r.mock.QuotaExceeded(true)
	o, _ := s.Moderate(ctx, Mod{Action: ModBan, ChannelID: "UCfan"})
	if o.State != ModRefused || !strings.Contains(o.Detail, "quota") {
		t.Fatalf("%+v", o)
	}
	if s.QuotaLeft(ctx) != 0 {
		t.Fatal("quota left after Google said it's spent")
	}
	// No answer: unknown, never retried.
	if o := modOutcome(errUnreachable); o.State != ModUnknown {
		t.Fatalf("%+v", o)
	}
}

func TestADeletionInYouTubesOwnChatIsRecorded(t *testing.T) {
	r := newRig(t)
	_, chatID := reading(t, r)
	msg := r.mock.Say(chatID, "UCfan", "Fan", "oops")
	waitMessages(t, r, 1)
	r.mock.Add(chatID, livechat.Message{ID: msg, Snippet: livechat.Snippet{Type: livechat.TypeTombstone}})
	del := waitRecords(t, r, events.ChatDeleted, 1)
	if p := payloadMap(t, del[0]); p["message_id"] != msg || p["live_chat_id"] != chatID {
		t.Fatalf("%+v", p)
	}
}
