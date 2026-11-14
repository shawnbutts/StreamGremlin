package moderation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// shieldTwitch is a channel with chat modes and Shield Mode.
type shieldTwitch struct {
	mu       sync.Mutex
	settings twitch.ChatSettings
	shield   bool
	// refuseShield and unknownChat make those parts fail.
	refuseShield bool
	unknownChat  bool
	readFails    bool
	changes      []twitch.ChatSettingsChange
}

func (f *shieldTwitch) Moderate(context.Context, twitch.Mod) (twitch.ModOutcome, error) {
	return twitch.ModOutcome{State: Done}, nil
}

func (f *shieldTwitch) ChangeChatSettings(_ context.Context, c twitch.ChatSettingsChange) (twitch.ChatSettings, twitch.ModOutcome, error) {
	if err := twitch.CheckChatSettings(c); err != nil {
		return twitch.ChatSettings{}, twitch.ModOutcome{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes = append(f.changes, c)
	if f.unknownChat {
		return twitch.ChatSettings{}, twitch.ModOutcome{State: Unknown, Detail: "no answer"}, nil
	}
	if c.EmoteOnly != nil {
		f.settings.EmoteOnly = *c.EmoteOnly
	}
	if c.FollowersOnly != nil {
		f.settings.FollowersOnly = *c.FollowersOnly
		f.settings.FollowerMinutes = c.FollowerMinutes
		if !*c.FollowersOnly {
			f.settings.FollowerMinutes = nil
		}
	}
	return f.settings, twitch.ModOutcome{State: Done}, nil
}

func (f *shieldTwitch) ShieldMode(context.Context) (twitch.Shield, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readFails {
		return twitch.Shield{}, errors.New("no answer")
	}
	return twitch.Shield{Active: f.shield}, nil
}

func (f *shieldTwitch) SetShieldMode(_ context.Context, on bool) (twitch.Shield, twitch.ModOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuseShield {
		return twitch.Shield{}, twitch.ModOutcome{State: Refused, Detail: "Sign in to Twitch again"}, nil
	}
	f.shield = on
	return twitch.Shield{Active: on}, twitch.ModOutcome{State: Done}, nil
}

func (f *shieldTwitch) ChatSettings(context.Context) (twitch.ChatSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readFails {
		return twitch.ChatSettings{}, errors.New("no answer")
	}
	return f.settings, nil
}

func req(id, action string, on *bool) Request {
	return Request{RequestID: id, Action: action, Platform: PlatformTwitch, On: on, From: FromLive}
}

func TestShieldModeRequest(t *testing.T) {
	f := &shieldTwitch{}
	_, _, db := newService(t)
	s := New(db, f, nil)
	ctx := context.Background()
	on := true
	rec, _, err := s.Do(ctx, req("s1", ShieldMode, &on))
	if err != nil || rec.State != Done || rec.Shield == nil || !rec.Shield.Active || !f.shield {
		t.Fatalf("%+v %v", rec, err)
	}
	list, _ := s.List(ctx, "", "", 10)
	if len(list) != 1 || list[0].On == nil || !*list[0].On {
		t.Fatalf("%+v", list)
	}
	// Asked again, nothing is sent; on is required, and nothing else taken.
	if _, dup, _ := s.Do(ctx, req("s1", ShieldMode, &on)); !dup {
		t.Fatal("sent twice")
	}
	for _, bad := range []Request{req("s2", ShieldMode, nil), req("s3", Lock, &on), {RequestID: "s4", Action: ShieldMode, Platform: PlatformTwitch,
		On: &on, UserID: "1", From: FromLive}, {RequestID: "s5", Action: twitch.ModBan, Platform: PlatformTwitch, UserID: "1", On: &on, From: FromLive}} {
		if _, _, err := s.Do(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// A Twitch without Shield Mode refuses, in words.
	plain, _, _ := newService(t)
	if rec, _, _ := plain.Do(ctx, req("p1", ShieldMode, &on)); rec.State != Refused || !strings.Contains(rec.Answer, "isn't available") {
		t.Fatalf("%+v", rec)
	}
}

// A lock turns on Shield Mode, emote-only and followers-only, records
// what was there, and an unlock puts back only that, once.
func TestLockAndUnlock(t *testing.T) {
	ten, thirty := 10, 30
	f := &shieldTwitch{settings: twitch.ChatSettings{Slow: true}}
	_, _, db := newService(t)
	s := New(db, f, nil)
	ctx := context.Background()
	if rec, _, _ := s.Do(ctx, req("u0", Unlock, nil)); rec.State != Refused || !strings.Contains(rec.Answer, "isn't locked") {
		t.Fatalf("unlock with nothing locked: %+v", rec)
	}
	rec, _, err := s.Do(ctx, req("l1", Lock, nil))
	if err != nil || rec.State != Done || !f.shield || !f.settings.EmoteOnly || !f.settings.FollowersOnly || *f.settings.FollowerMinutes != ten {
		t.Fatalf("%+v %v %+v", rec, err, f.settings)
	}
	if open, _ := s.Locked(ctx); open == nil || open.ID != rec.ID || open.Lock == nil || open.Lock.Before == nil || !open.Lock.Before.Slow {
		t.Fatalf("open: %+v", open)
	}
	if again, _, _ := s.Do(ctx, req("l2", Lock, nil)); again.State != Refused || !strings.Contains(again.Answer, "locked already") {
		t.Fatalf("second lock: %+v", again)
	}
	un, _, err := s.Do(ctx, req("u1", Unlock, nil))
	if err != nil || un.State != Done || un.UnlockOf != rec.ID || f.shield || f.settings.EmoteOnly || f.settings.FollowersOnly || !f.settings.Slow {
		t.Fatalf("%+v %v %+v", un, err, f.settings)
	}
	if open, _ := s.Locked(ctx); open != nil {
		t.Fatal("still locked")
	}
	if again, _, _ := s.Do(ctx, req("u2", Unlock, nil)); again.State != Refused {
		t.Fatalf("unlock twice: %+v", again)
	}

	// Already on before: kept on, and a longer follow time kept.
	f.shield, f.settings = true, twitch.ChatSettings{FollowersOnly: true, FollowerMinutes: &thirty}
	if rec, _, _ := s.Do(ctx, req("l3", Lock, nil)); rec.State != Done || *f.settings.FollowerMinutes != thirty || rec.Lock.ShieldOn {
		t.Fatalf("%+v %+v", rec, f.settings)
	}
	if un, _, _ := s.Do(ctx, req("u3", Unlock, nil)); un.State != Done || !f.shield || !f.settings.FollowersOnly ||
		*f.settings.FollowerMinutes != thirty || f.settings.EmoteOnly {
		t.Fatalf("%+v %+v", un, f.settings)
	}
}

// A lock that partly fails says so, and still unlocks what it did; when
// Twitch can't say what was there, unlocking turns it all off.
func TestLockPartsAndUnknowns(t *testing.T) {
	f := &shieldTwitch{refuseShield: true}
	_, _, db := newService(t)
	s := New(db, f, nil)
	ctx := context.Background()
	rec, _, _ := s.Do(ctx, req("l1", Lock, nil))
	if rec.State != Done || !strings.Contains(rec.Answer, "refused Shield Mode") || !f.settings.EmoteOnly {
		t.Fatalf("%+v", rec)
	}
	f.refuseShield = false
	if un, _, _ := s.Do(ctx, req("u1", Unlock, nil)); un.State != Done || f.settings.EmoteOnly || f.shield {
		t.Fatalf("%+v %+v", un, f)
	}
	// Twitch can't be read, and the chat change gets no answer: unknown,
	// still counted as locked, and the unlock turns everything off.
	f.readFails, f.unknownChat = true, true
	if rec, _, _ := s.Do(ctx, req("l2", Lock, nil)); rec.State != Unknown {
		t.Fatalf("%+v", rec)
	}
	if open, _ := s.Locked(ctx); open == nil {
		t.Fatal("an unknown lock isn't open")
	}
	f.unknownChat = false
	f.settings = twitch.ChatSettings{EmoteOnly: true, FollowersOnly: true}
	if un, _, _ := s.Do(ctx, req("u2", Unlock, nil)); un.State != Done || f.settings.EmoteOnly || f.settings.FollowersOnly || f.shield {
		t.Fatalf("%+v %+v", un, f.settings)
	}
}
