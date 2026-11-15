package youtube

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// waitEvents waits for n YouTube events of a type.
func waitEvents(t *testing.T, r *rig, typ string, n int) []events.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := youtubeChat(t, r, typ)
		if len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				t.Fatalf("%s: %d events, want %d", typ, len(got), n)
			}
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitStream(t *testing.T, s *Service, ok func(StreamInfo) bool) StreamInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Stream(); st != nil && ok(*st) {
			return *st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stream info: %+v", s.Stream())
	return StreamInfo{}
}

func TestViewersChanged(t *testing.T) {
	for _, c := range []struct {
		last, now int64
		want      bool
	}{{0, 0, false}, {0, 1, true}, {3, 4, true}, {3, 3, false}, {100, 105, false}, {100, 110, true}, {100, 89, true}, {1000, 1050, false}} {
		if got := ViewersChanged(c.last, c.now); got != c.want {
			t.Errorf("%d → %d: %v", c.last, c.now, got)
		}
	}
}

func TestMinuteCheck(t *testing.T) {
	r := newRig(t)
	r.check = 30 * time.Millisecond
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	r.mock.SetViewers(b.ID, 100)
	// Subscribed before the broadcast started: never announced.
	r.mock.Subscribe("UCstreamer", "UCold", "Old Fan", b.Started.Add(-time.Minute))
	s.FindNow()
	waitChat(t, s, ChatConnected)
	st := waitStream(t, s, func(st StreamInfo) bool { return st.Viewers != nil && *st.Viewers == 100 })
	if st.Title != "Stream" {
		t.Fatalf("%+v", st)
	}
	if cs := s.ChatStatus(); cs.Stream == nil || *cs.Stream.Viewers != 100 {
		t.Fatalf("chat status: %+v", cs.Stream)
	}

	// A small change isn't announced; a tenth is.
	r.mock.SetViewers(b.ID, 105)
	waitStream(t, s, func(st StreamInfo) bool { return *st.Viewers == 105 })
	r.mock.SetViewers(b.ID, 120)
	got := waitEvents(t, r, TypeViewers, 1)
	p := payloadMap(t, got[0])
	if p["viewers"] != 120.0 || p["previous"] != 100.0 || p["change"] != 20.0 {
		t.Fatalf("%v", p)
	}

	// A hidden count is unknown, not 0.
	r.mock.SetViewers(b.ID, -1)
	waitStream(t, s, func(st StreamInfo) bool { return st.Viewers == nil })

	// A title changed in YouTube Studio.
	r.mock.SetVideo(b.ID, "New title", "About it")
	up := waitEvents(t, r, typeUpdated, 1)
	if p := payloadMap(t, up[0]); p["title"] != "New title" || p["description"] != "About it" {
		t.Fatalf("%v", p)
	}

	// A new public subscriber, once however many checks see them.
	r.mock.Subscribe("UCstreamer", "UCnew", "New Fan", time.Now())
	subs := waitEvents(t, r, typeFollow, 1)
	if a := subs[0].Actor; a == nil || a.ID != "UCnew" || a.DisplayName != "New Fan" || a.Platform != events.SourceYouTube {
		t.Fatalf("%+v", subs[0].Actor)
	}
	time.Sleep(100 * time.Millisecond)
	waitEvents(t, r, typeFollow, 1)

	// When the broadcast ends, there's no stream info.
	r.mock.EndBroadcast(b.ID)
	waitChat(t, s, ChatLooking)
	if s.Stream() != nil || s.ChatStatus().Stream != nil {
		t.Fatal("stream info outlived its broadcast")
	}
}

func TestSetStream(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	ctx := context.Background()
	title := "Late night"
	if _, err := s.SetStream(ctx, StreamChange{Title: &title}); !errors.Is(err, ErrNotLive) {
		t.Fatalf("not live: %v", err)
	}
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	before := s.quota.status(ctx).Used
	for _, bad := range []StreamChange{{}, {Title: new(string)}, {Title: ptr("a <b>")}, {Description: ptr(string(make([]byte, 5001)))}} {
		if _, err := s.SetStream(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	info, err := s.SetStream(ctx, StreamChange{Title: &title})
	if err != nil || info.Title != title {
		t.Fatalf("%+v %v", info, err)
	}
	// The rest of the snippet is sent back, so nothing is deleted.
	ups := r.mock.VideoUpdates()
	if len(ups) != 1 || ups[0].Title != title || ups[0].CategoryID != "20" || !slices.Equal(ups[0].Tags, []string{"stand-in"}) {
		t.Fatalf("%+v", ups)
	}
	if used := s.quota.status(ctx).Used - before; used < CostVideosList+CostVideosUpdate {
		t.Fatalf("quota counted %d", used)
	}
	desc := "Playing until late"
	if _, err := s.SetStream(ctx, StreamChange{Description: &desc}); err != nil {
		t.Fatal(err)
	}
	ups = r.mock.VideoUpdates()
	if len(ups) != 2 || ups[1].Title != title || ups[1].Description != desc {
		t.Fatalf("%+v", ups)
	}
	waitEvents(t, r, typeUpdated, 2)
	if st := s.Stream(); st == nil || st.Title != title || st.Description != desc {
		t.Fatalf("%+v", st)
	}
	_ = b
}

func ptr(s string) *string { return &s }

func TestPolls(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	ctx := context.Background()
	if _, err := s.StartPoll(ctx, NewPoll{Question: "Q", Options: []string{"a", "b"}}); !errors.Is(err, ErrNotLive) {
		t.Fatalf("not live: %v", err)
	}
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	for _, bad := range []NewPoll{{Question: "", Options: []string{"a", "b"}}, {Question: "Q", Options: []string{"a"}},
		{Question: "Q", Options: []string{"a", "b", "c", "d", "e"}}, {Question: "Q", Options: []string{"a", " "}}} {
		if _, err := s.StartPoll(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	// A blank choice is left out.
	p, err := s.StartPoll(ctx, NewPoll{Question: " Next game? ", Options: []string{"Chess", " ", "Go"}})
	if err != nil || p.ID == "" || p.Question != "Next game?" || len(p.Choices) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := s.StartPoll(ctx, NewPoll{Question: "Again?", Options: []string{"y", "n"}}); !errors.Is(err, ErrPollRunning) {
		t.Fatalf("second poll: %v", err)
	}
	started := waitEvents(t, r, TypePollStarted, 1)
	if pl := payloadMap(t, started[0]); pl["title"] != "Next game?" || pl["poll_id"] != p.ID {
		t.Fatalf("%v", pl)
	}
	r.mock.Vote(b.ChatID, 1)
	r.mock.Vote(b.ChatID, 1)
	r.mock.Vote(b.ChatID, 0)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cp := s.Poll(); cp != nil && cp.Choices[1].Votes == 2 && cp.Choices[0].Votes == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", s.Poll())
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The update event is written just after the count changes.
	for {
		ups := youtubeChat(t, r, TypePollUpdated)
		if len(ups) > 0 {
			if pl := payloadMap(t, ups[len(ups)-1]); pl["votes"] == 3.0 {
				if pl["leader"] != "Go" {
					t.Fatalf("%v", pl)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no update for all three votes: %d updates", len(ups))
		}
		time.Sleep(5 * time.Millisecond)
	}
	ended, err := s.EndPoll(ctx)
	if err != nil || ended.Status != "closed" || ended.Choices[1].Votes != 2 {
		t.Fatalf("%+v %v", ended, err)
	}
	end := waitEvents(t, r, TypePollEnded, 1)
	if pl := payloadMap(t, end[0]); pl["status"] != "completed" || pl["leader"] != "Go" {
		t.Fatalf("%v", pl)
	}
	if _, err := s.EndPoll(ctx); !errors.Is(err, ErrNoPoll) {
		t.Fatalf("end twice: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	waitEvents(t, r, TypePollEnded, 1) // the stream's answer doesn't end it again

	// A poll closed on YouTube's own page ends when the stream stops showing it.
	if _, err := s.StartPoll(ctx, NewPoll{Question: "Snack?", Options: []string{"Chips", "Fruit"}}); err != nil {
		t.Fatal(err)
	}
	waitEvents(t, r, TypePollStarted, 2)
	deadline = time.Now().Add(5 * time.Second)
	for s.pollSeenNow() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	r.mock.ClosePoll(b.ChatID)
	waitEvents(t, r, TypePollEnded, 2)
	if cp := s.Poll(); cp == nil || cp.Status != "closed" {
		t.Fatalf("%+v", cp)
	}
	if cs := s.ChatStatus(); cs.Poll == nil || cs.Poll.Question != "Snack?" {
		t.Fatalf("%+v", cs.Poll)
	}
}

// A poll made by someone else (in YouTube Studio) is followed too, and a
// poll message is never stored as chat.
func TestPollFromYouTube(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	s.FindNow()
	waitChat(t, s, ChatConnected)
	r.mock.Add(b.ChatID, livechat.Message{Author: livechat.Author{ChannelID: "UCstreamer", IsChatOwner: true},
		Snippet: livechat.Snippet{Type: livechat.TypePoll, Poll: &livechat.Poll{Question: "Q?", Status: livechat.PollActive,
			Options: []livechat.PollOption{{Text: "a"}, {Text: "b"}}}}})
	waitEvents(t, r, TypePollStarted, 1)
	if len(youtubeChat(t, r, "chat.message.received")) != 0 {
		t.Fatal("a poll stored as chat")
	}
}

func (s *Service) pollSeenNow() string {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.pollSeen
}
