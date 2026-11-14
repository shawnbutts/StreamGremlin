package youtube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

func waitChat(t *testing.T, s *Service, state ChatState) ChatStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.ChatStatus(); st.State == state {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("chat: state %s (%s), want %s", s.ChatStatus().State, s.ChatStatus().Detail, state)
	return ChatStatus{}
}

// youtubeChat returns the stored YouTube chat, of type typ.
func youtubeChat(t *testing.T, r *rig, typ string) []events.Record {
	t.Helper()
	recs, err := r.inbox.After(context.Background(), 0, 1000, events.Filter{typ})
	if err != nil {
		t.Fatal(err)
	}
	var out []events.Record
	for _, x := range recs {
		if x.Source == events.SourceYouTube {
			out = append(out, x)
		}
	}
	return out
}

func waitMessages(t *testing.T, r *rig, n int) []events.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := youtubeChat(t, r, "chat.message.received")
		if len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				t.Fatalf("%d messages, want %d", len(got), n)
			}
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type chatPayload struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	Backlog   bool   `json:"backlog"`
	SentBy    string `json:"sent_by"`
	Chatter   struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		Role          string `json:"role"`
		IsBroadcaster bool   `json:"is_broadcaster"`
		IsModerator   bool   `json:"is_moderator"`
		IsMember      bool   `json:"is_member"`
	} `json:"chatter"`
}

func payloadOf(t *testing.T, r events.Record) chatPayload {
	t.Helper()
	var p chatPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// live signs your channel in and returns the service once it's looking.
func live(t *testing.T, r *rig) *Service {
	t.Helper()
	if r.look == 0 {
		r.look = time.Hour // only FindNow and sign-ins look, unless a test says
	}
	s := r.configured()
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
	waitChat(t, s, ChatLooking)
	return s
}

func waitDetail(t *testing.T, s *Service, state ChatState, has string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.ChatStatus(); st.State == state && strings.Contains(st.Detail, has) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("chat: %+v, want %s with %q", s.ChatStatus(), state, has)
}

func TestChatOffUntilSignedIn(t *testing.T) {
	r := newRig(t)
	s := r.service()
	waitDetail(t, s, ChatOff, "Google project")
	if err := s.SetClient(context.Background(), testClientID, secretsZero()); err != nil {
		t.Fatal(err)
	}
	waitDetail(t, s, ChatOff, "Sign in")
	if r.mock.Calls("liveBroadcasts") != 0 || r.mock.Streams() != 0 {
		t.Fatal("YouTube was asked before a sign-in")
	}
}

func TestChatReadsTheLiveBroadcast(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	if st := s.ChatStatus(); st.NextCheck == nil || st.CheckedAt == nil || st.Broadcast != nil {
		t.Fatalf("%+v", st)
	}
	b := r.mock.GoLive("UCstreamer", "Friday stream")
	r.mock.Say(b.ChatID, "UCviewer1", "Early Bird", "said before StreamGremlin looked")
	s.FindNow()
	st := waitChat(t, s, ChatConnected)
	if st.Broadcast == nil || st.Broadcast.ID != b.ID || st.Broadcast.Title != "Friday stream" || st.Broadcast.StartedAt.IsZero() {
		t.Fatalf("%+v", st.Broadcast)
	}
	r.mock.Say(b.ChatID, "UCviewer2", "Viewer Two", "hello from YouTube")
	r.mock.Say(b.ChatID, "UCstreamer", "Streamer", "welcome!")
	r.mock.Add(b.ChatID, livechat.Message{
		Snippet: livechat.Snippet{Type: livechat.TypeText, Text: "a mod and member", AuthorChannelID: "UCmod"},
		Author:  livechat.Author{ChannelID: "UCmod", DisplayName: "Mod", IsChatModerator: true, IsChatSponsor: true},
	})
	recs := waitMessages(t, r, 4)

	early := payloadOf(t, recs[0])
	if early.Text != "said before StreamGremlin looked" || !early.Backlog {
		t.Fatalf("%+v", early)
	}
	viewer := payloadOf(t, recs[1])
	if viewer.Text != "hello from YouTube" || viewer.Backlog || viewer.Chatter.ID != "UCviewer2" ||
		viewer.Chatter.DisplayName != "Viewer Two" || viewer.Chatter.Role != "viewer" {
		t.Fatalf("%+v", viewer)
	}
	if a := recs[1].Actor; a == nil || a.Platform != "youtube" || a.ID != "UCviewer2" || a.DisplayName != "Viewer Two" {
		t.Fatalf("%+v", recs[1].Actor)
	}
	if recs[1].ChannelID != "UCstreamer" || recs[1].DeliveryQuality != events.Confirmed || recs[1].OccurredAt.IsZero() {
		t.Fatalf("%+v", recs[1])
	}
	if p := payloadOf(t, recs[2]); p.Chatter.Role != "broadcaster" || !p.Chatter.IsBroadcaster {
		t.Fatalf("%+v", p)
	}
	if p := payloadOf(t, recs[3]); p.Chatter.Role != "moderator" || !p.Chatter.IsModerator || !p.Chatter.IsMember {
		t.Fatalf("%+v", p)
	}
	// Only the event log reads YouTube chat in this slice.
	if recs[1].Source != events.SourceYouTube {
		t.Fatal("YouTube chat isn't log-only")
	}
	// Quota: a lookup per FindNow or sign-in, 5 for the stream, and the
	// minute check (D-102), once its first check is done.
	for deadline := time.Now().Add(5 * time.Second); r.mock.Calls("subscriptions.list") == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	q := s.Status(context.Background()).Quota
	want := CostChannelsList + r.mock.Calls("liveBroadcasts")*CostBroadcastsList + r.mock.Streams()*CostStreamList +
		r.mock.Calls("videos.list")*CostVideosList + r.mock.Calls("subscriptions.list")*CostSubscriptionsList
	if q.Used != want || r.mock.Streams() != 1 {
		t.Fatalf("used %d, want %d (%d streams)", q.Used, want, r.mock.Streams())
	}
	// FindNow while reading changes nothing.
	calls := r.mock.Calls("liveBroadcasts")
	s.FindNow()
	time.Sleep(50 * time.Millisecond)
	if r.mock.Calls("liveBroadcasts") != calls {
		t.Fatal("looked again while reading chat")
	}
}

func TestChatResumesAfterADropWithoutRepeats(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.Say(b.ChatID, "UCv", "V", "one")
	waitMessages(t, r, 1)
	r.mock.DropStreams()
	r.mock.Say(b.ChatID, "UCv", "V", "two")
	r.mock.Say(b.ChatID, "UCv", "V", "three")
	recs := waitMessages(t, r, 3)
	deadline := time.Now().Add(5 * time.Second)
	for r.mock.Streams() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.mock.LastPageToken() == "" {
		t.Fatal("the stream didn't resume with its page token")
	}
	for i, want := range []string{"one", "two", "three"} {
		if p := payloadOf(t, recs[i]); p.Text != want || p.Backlog {
			t.Fatalf("%d: %+v", i, p)
		}
	}
	// A further drop and message: still each once.
	r.mock.DropStreams()
	r.mock.Say(b.ChatID, "UCv", "V", "four")
	waitMessages(t, r, 4)
	time.Sleep(100 * time.Millisecond)
	waitMessages(t, r, 4)
	waitChat(t, s, ChatConnected)
}

func TestChatResumesAfterARestart(t *testing.T) {
	r := newRig(t)
	r.look = time.Hour
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.Say(b.ChatID, "UCv", "V", "before")
	waitMessages(t, r, 1)
	s.Close()
	for i := range 7 {
		r.mock.Say(b.ChatID, "UCv", "V", "while closed "+string(rune('a'+i)))
	}
	s2 := r.service()
	waitChat(t, s2, ChatConnected)
	recs := waitMessages(t, r, 8)
	if r.mock.LastPageToken() == "" {
		t.Fatal("didn't resume from the saved page token")
	}
	// All 7 arrive (more than the 5 a fresh stream gets), none marked as
	// from before StreamGremlin was reading: it was, until the restart.
	for _, x := range recs[1:] {
		if p := payloadOf(t, x); p.Backlog {
			t.Fatalf("%+v", p)
		}
	}
}

func TestChatEndsAndLooksForTheNextBroadcast(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "First")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.EndBroadcast(b.ID)
	st := waitChat(t, s, ChatLooking)
	if !strings.Contains(st.Detail, "First") || st.Broadcast != nil {
		t.Fatalf("%+v", st)
	}
	b2 := r.mock.GoLive("UCstreamer", "Second")
	s.FindNow()
	st = waitChat(t, s, ChatConnected)
	if st.Broadcast.ID != b2.ID {
		t.Fatalf("%+v", st.Broadcast)
	}
	r.mock.Say(b2.ChatID, "UCv", "V", "hi again")
	waitMessages(t, r, 1)
}

func TestChatTurnedOffIsNotRetriedInALoop(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "No chat")
	r.mock.TurnChatOff(b.ChatID)
	s.FindNow()
	waitChat(t, s, ChatLooking)
	time.Sleep(20 * time.Millisecond)
	st := s.ChatStatus()
	if !strings.Contains(st.Detail, "turned off") {
		t.Fatalf("%+v", st)
	}
	streams := r.mock.Streams()
	// Asking again finds it, but skips the chat that's off.
	s.FindNow()
	time.Sleep(100 * time.Millisecond)
	if r.mock.Streams() != streams || streams != 1 {
		t.Fatalf("%d streams", r.mock.Streams())
	}
}

func TestChatStopsAtQuotaUntilTheReset(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	r.mock.GoLive("UCstreamer", "Stream")
	r.mock.QuotaExceeded(true)
	s.FindNow()
	st := waitChat(t, s, ChatQuota)
	if st.NextCheck == nil || !st.NextCheck.Equal(s.Status(context.Background()).Quota.ResetsAt) {
		t.Fatalf("%+v", st)
	}
	if r.mock.Streams() != 0 {
		t.Fatal("a stream was opened with the quota spent")
	}
	calls := r.mock.Calls("liveBroadcasts")
	s.FindNow()
	time.Sleep(50 * time.Millisecond)
	if r.mock.Calls("liveBroadcasts") != calls {
		t.Fatal("YouTube was asked with the quota spent")
	}
}

func TestChatQuotaRefusedOnTheStream(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.QuotaExceeded(true)
	r.mock.DropStreams()
	waitChat(t, s, ChatQuota)
	_ = b
	if !s.Status(context.Background()).Quota.Exhausted {
		t.Fatal("the stream's quota refusal wasn't remembered")
	}
}

func TestChatStopsWhenYouSignOut(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	if err := s.streamer.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitChat(t, s, ChatOff)
	deadline := time.Now().Add(5 * time.Second)
	for r.mock.OpenStreams() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.mock.OpenStreams() != 0 {
		t.Fatal("the stream stayed open after signing out")
	}
}

func TestBotMessagesAreNotViewerChat(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	signIn(t, s.bot, "UCbot")
	waitFor(t, s.bot, StateConnected)
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.Say(b.ChatID, "UCbot", "Bot", "beep")
	r.mock.Say(b.ChatID, "UCv", "V", "hi")
	recs := waitMessages(t, r, 1)
	if payloadOf(t, recs[0]).Text != "hi" {
		t.Fatal("the bot's message was stored as viewer chat")
	}
	sent := youtubeChat(t, r, events.ChatSent)
	if len(sent) != 1 || payloadOf(t, sent[0]).SentBy != "bot" || payloadOf(t, sent[0]).Text != "beep" {
		t.Fatalf("%+v", sent)
	}
}

func TestTwitchGoingLiveLooksAtOnce(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Simulcast")
	calls := r.mock.Calls("liveBroadcasts")
	// A test event doesn't count; Twitch's real one does.
	for _, q := range []events.Quality{events.Synthetic, events.Confirmed} {
		_, err := r.inbox.Accept(context.Background(), events.Envelope{
			EventID: events.NewID(), Type: "broadcast.stream.started", Source: "twitch", SourceEventID: "online-" + string(q),
			OccurredAt: time.Now(), Payload: json.RawMessage(`{}`), DeliveryQuality: q, CorrelationID: "c",
		})
		if err != nil {
			t.Fatal(err)
		}
		if q == events.Synthetic {
			time.Sleep(100 * time.Millisecond)
			if r.mock.Calls("liveBroadcasts") != calls {
				t.Fatal("a test event looked")
			}
		}
	}
	st := waitChat(t, s, ChatConnected)
	if st.Broadcast.ID != b.ID {
		t.Fatalf("%+v", st)
	}
}

func TestChatChecksRegularlyWhileNotLive(t *testing.T) {
	r := newRig(t)
	r.look = 30 * time.Millisecond
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Found by itself")
	st := waitChat(t, s, ChatConnected)
	if st.Broadcast.ID != b.ID {
		t.Fatalf("%+v", st)
	}
}

func secretsZero() secrets.Value { return secrets.Value{} }
