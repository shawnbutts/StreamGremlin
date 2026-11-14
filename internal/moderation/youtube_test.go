package moderation

import (
	"context"
	"errors"
	"sync"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

type fakeYouTube struct {
	mu      sync.Mutex
	sent    []youtube.Mod
	outcome youtube.ModOutcome
}

func (f *fakeYouTube) Moderate(_ context.Context, m youtube.Mod) (youtube.ModOutcome, error) {
	if err := youtube.CheckMod(m); err != nil {
		return youtube.ModOutcome{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	o := f.outcome
	if o.State == "" {
		o = youtube.ModOutcome{State: Done}
		switch m.Action {
		case youtube.ModBan, youtube.ModTimeout:
			o.ID = "ban-" + m.ChannelID
		case youtube.ModMod:
			o.ID = "mod-" + m.ChannelID
		}
	}
	return o, nil
}

func (f *fakeYouTube) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }
func (f *fakeYouTube) last() youtube.Mod {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[len(f.sent)-1]
}

func yt(id, action string) Request {
	return Request{RequestID: id, Action: action, Platform: PlatformYouTube, UserID: "UCfan", Login: "", From: FromLive}
}

func TestYouTubeIsRefusedUntilSetUp(t *testing.T) {
	s, _, _ := newService(t)
	if _, _, err := s.Do(context.Background(), yt("r1", youtube.ModBan)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// A YouTube ban is sent once and recorded with YouTube's ban id, which
// the unban uses; a ban not made here can't be undone from here.
func TestYouTubeBanIsUndoneByItsID(t *testing.T) {
	s, _, _ := newService(t)
	f := &fakeYouTube{}
	s.UseYouTube(f)
	ctx := context.Background()
	if _, _, err := s.Do(ctx, yt("u0", youtube.ModUnban)); !errors.Is(err, ErrInvalid) || f.count() != 0 {
		t.Fatalf("an unban with no ban from here: %v", err)
	}
	to := yt("t1", youtube.ModTimeout)
	to.Seconds, to.Reason = 600, "calm down"
	rec, _, err := s.Do(ctx, to)
	if err != nil || rec.State != Done || rec.ResultID != "ban-UCfan" {
		t.Fatalf("%+v %v", rec, err)
	}
	if again, dup, _ := s.Do(ctx, to); !dup || again.ID != rec.ID || f.count() != 1 {
		t.Fatal("sent twice")
	}
	if b, err := s.YouTubeBan(ctx, "UCfan"); err != nil || b == nil || b.ID != rec.ID {
		t.Fatalf("%+v %v", b, err)
	}
	un, _, err := s.Do(ctx, yt("u1", youtube.ModUnban))
	if err != nil || un.State != Done || f.last().BanID != "ban-UCfan" {
		t.Fatalf("%+v %v %+v", un, err, f.last())
	}
	if b, _ := s.YouTubeBan(ctx, "UCfan"); b != nil {
		t.Fatal("still undoable after the unban")
	}
	list, _ := s.List(ctx, PlatformYouTube, "UCfan", 10)
	if len(list) != 2 || list[1].ResultID != "ban-UCfan" || list[1].Reason != "calm down" {
		t.Fatalf("%+v", list)
	}
}

func TestYouTubeRefusedBanIsNotUndoable(t *testing.T) {
	s, _, _ := newService(t)
	f := &fakeYouTube{outcome: youtube.ModOutcome{State: Refused, Detail: "no"}}
	s.UseYouTube(f)
	ctx := context.Background()
	if rec, _, err := s.Do(ctx, yt("b1", youtube.ModBan)); err != nil || rec.State != Refused {
		t.Fatalf("%+v %v", rec, err)
	}
	if b, _ := s.YouTubeBan(ctx, "UCfan"); b != nil {
		t.Fatal("a refused ban is undoable")
	}
}

func TestYouTubeModeratorRemovedByItsID(t *testing.T) {
	s, _, _ := newService(t)
	f := &fakeYouTube{}
	s.UseYouTube(f)
	ctx := context.Background()
	// Not made a moderator here: YouTube is asked who they are.
	if _, _, err := s.Do(ctx, yt("x0", youtube.ModUnmod)); err != nil || f.last().ModeratorID != "" {
		t.Fatalf("%v %+v", err, f.last())
	}
	if _, _, err := s.Do(ctx, yt("m1", youtube.ModMod)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Do(ctx, yt("x1", youtube.ModUnmod)); err != nil || f.last().ModeratorID != "mod-UCfan" {
		t.Fatalf("%v %+v", err, f.last())
	}
}

func TestYouTubeHasNoTwitchOnlyActions(t *testing.T) {
	s, _, _ := newService(t)
	s.UseYouTube(&fakeYouTube{})
	ctx := context.Background()
	for _, r := range []Request{
		yt("a", "warn"), yt("b", "vip"), yt("c", "shoutout"), yt("d", ChatModes),
		{RequestID: "e", Action: youtube.ModDelete, Platform: PlatformYouTube, MessageID: "m1", Reason: "no", From: FromLive},
	} {
		if _, _, err := s.Do(ctx, r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	del := Request{RequestID: "f", Action: youtube.ModDelete, Platform: PlatformYouTube, UserID: "UCfan", MessageID: "LCC.abc", From: FromWorkflow}
	if rec, _, err := s.Do(ctx, del); err != nil || rec.State != Done || rec.UserID != "UCfan" {
		t.Fatalf("%+v %v", rec, err)
	}
}
