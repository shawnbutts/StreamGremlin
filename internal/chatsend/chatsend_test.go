package chatsend

import (
	"context"
	"errors"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

type fakeTwitch struct {
	got []twitch.SendRequest
	err error
}

func (f *fakeTwitch) SendChat(_ context.Context, r twitch.SendRequest) (twitch.SendResult, error) {
	f.got = append(f.got, r)
	if f.err != nil {
		return twitch.SendResult{}, f.err
	}
	return twitch.SendResult{MessageID: "t1", SentAs: twitch.RoleBot, Login: "bot"}, nil
}

type fakeYouTube struct {
	got  []youtube.SendRequest
	live bool
	err  error
}

func (f *fakeYouTube) SendChat(_ context.Context, r youtube.SendRequest) (youtube.SendResult, error) {
	f.got = append(f.got, r)
	if !f.live {
		return youtube.SendResult{}, youtube.ErrNotLive
	}
	if f.err != nil {
		return youtube.SendResult{}, f.err
	}
	return youtube.SendResult{MessageIDs: []string{"y1"}, SentAs: youtube.RoleBot, Channel: "Bot"}, nil
}

func (f *fakeYouTube) ChatStatus() youtube.ChatStatus {
	if f.live {
		return youtube.ChatStatus{State: youtube.ChatConnected, Broadcast: &youtube.Broadcast{ID: "b"}}
	}
	return youtube.ChatStatus{State: youtube.ChatLooking}
}

func TestWhereItGoes(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		to      Where
		origin  string
		tw, yt  int
		skipped bool
	}{
		{Origin, "twitch", 1, 0, false},
		{Origin, "test", 1, 0, false},
		{Origin, "timers", 1, 0, false},
		{Origin, "youtube", 0, 1, false},
		{Twitch, "youtube", 1, 0, false},
		{YouTube, "twitch", 0, 1, false},
		{Both, "", 1, 1, false},
	} {
		tw, yt := &fakeTwitch{}, &fakeYouTube{live: true}
		res, err := Send(ctx, tw, yt, Request{Text: "hi", To: c.to, Origin: c.origin})
		if err != nil || len(tw.got) != c.tw || len(yt.got) != c.yt || (res.Skipped != nil) != c.skipped {
			t.Errorf("%+v: %+v %v (twitch %d, youtube %d)", c, res, err, len(tw.got), len(yt.got))
		}
	}
}

func TestBothSkipsYouTubeWhileNotLive(t *testing.T) {
	tw, yt := &fakeTwitch{}, &fakeYouTube{}
	res, err := Send(context.Background(), tw, yt, Request{Text: "hi", To: Both})
	if err != nil || res.Twitch == nil || res.YouTube != nil || res.Skipped["youtube"] == "" || len(yt.got) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// No YouTube at all: the same.
	res, err = Send(context.Background(), tw, nil, Request{Text: "hi", To: Both})
	if err != nil || res.Skipped["youtube"] == "" {
		t.Fatalf("%+v %v", res, err)
	}
	// YouTube alone, not live: refused.
	if _, err := Send(context.Background(), tw, yt, Request{Text: "hi", To: YouTube}); !errors.Is(err, youtube.ErrNotLive) {
		t.Fatal(err)
	}
	if _, err := Send(context.Background(), tw, nil, Request{Text: "hi", To: YouTube}); err == nil {
		t.Fatal("YouTube without YouTube")
	}
}

func TestOneRefusalWhenTheOtherSends(t *testing.T) {
	tw, yt := &fakeTwitch{err: twitch.ErrRateLimited}, &fakeYouTube{live: true}
	res, err := Send(context.Background(), tw, yt, Request{Text: "hi", To: Both})
	if err != nil || res.YouTube == nil || res.Failed["twitch"] == "" {
		t.Fatalf("%+v %v", res, err)
	}
	// Both refuse: the first refusal, with its platform.
	yt.err = youtube.ErrRateLimited
	_, err = Send(context.Background(), tw, yt, Request{Text: "hi", To: Both})
	var pe *PlatformError
	if !errors.As(err, &pe) || pe.Platform != "twitch" || !errors.Is(err, twitch.ErrRateLimited) {
		t.Fatal(err)
	}
}

func TestRepliesGoBackTheirOwnWay(t *testing.T) {
	ctx := context.Background()
	tw, yt := &fakeTwitch{}, &fakeYouTube{live: true}
	// A Twitch message: a Twitch reply.
	_, _ = Send(ctx, tw, yt, Request{Text: "thanks", Origin: "twitch", ReplyTo: "m1", ReplyName: "Fan", Workflow: "w"})
	if tw.got[0].ReplyTo != "m1" || tw.got[0].Text != "thanks" || tw.got[0].Workflow != "w" {
		t.Fatalf("%+v", tw.got)
	}
	// A YouTube message: YouTube has no replies, so @name first.
	_, _ = Send(ctx, tw, yt, Request{Text: " thanks", Origin: "youtube", ReplyTo: "y9", ReplyName: "@Fan"})
	if yt.got[0].Text != "@Fan thanks" {
		t.Fatalf("%+v", yt.got)
	}
	// A YouTube message answered on both: no Twitch reply to a YouTube id.
	_, _ = Send(ctx, tw, yt, Request{Text: "hi all", To: Both, Origin: "youtube", ReplyTo: "y9", ReplyName: "Fan"})
	if tw.got[1].ReplyTo != "" || yt.got[1].Text != "@Fan hi all" {
		t.Fatalf("%+v %+v", tw.got, yt.got)
	}
	// No reply asked: no @name.
	_, _ = Send(ctx, tw, yt, Request{Text: "plain", Origin: "youtube", ReplyName: "Fan"})
	if yt.got[2].Text != "plain" {
		t.Fatalf("%+v", yt.got)
	}
}

func TestInvalid(t *testing.T) {
	for _, r := range []Request{{Text: "x", To: "kick"}, {Text: "x", As: "mod"}} {
		if _, err := Send(context.Background(), &fakeTwitch{}, nil, r); !errors.Is(err, ErrInvalid) {
			t.Fatal(r, err)
		}
	}
}
