package twitch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Moderating against mocktwitch: each action does what Twitch documents;
// Twitch's refusals come back refused with its words; a request Twitch
// acts on but never answers is unknown, not refused or done; bad input is
// stopped before anything is sent.
func TestModerate(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	r.mock.SetModerator("streamer", "mo", true)
	ann, mo, streamer := r.mock.UserID("ann"), r.mock.UserID("mo"), r.mock.UserID("streamer")
	do := func(m Mod) ModOutcome {
		t.Helper()
		o, err := s.Moderate(ctx, m)
		if err != nil {
			t.Fatal(m, err)
		}
		return o
	}
	done := func(m Mod) {
		t.Helper()
		if o := do(m); o.State != ModDone {
			t.Fatalf("%+v: %+v", m, o)
		}
	}
	refused := func(m Mod, words string) {
		t.Helper()
		if o := do(m); o.State != ModRefused || !strings.Contains(o.Detail, words) {
			t.Fatalf("%+v: %+v, want refused with %q", m, o, words)
		}
	}

	done(Mod{Action: ModTimeout, UserID: ann, Seconds: 600, Reason: "calm down"})
	if !r.mock.IsBanned("streamer", "ann") {
		t.Fatal("not timed out")
	}
	done(Mod{Action: ModBan, UserID: ann, Reason: "spam"}) // a timeout becomes a ban
	refused(Mod{Action: ModBan, UserID: ann}, "already banned")
	refused(Mod{Action: ModTimeout, UserID: ann, Seconds: 60}, "already banned")
	done(Mod{Action: ModUnban, UserID: ann})
	refused(Mod{Action: ModUnban, UserID: ann}, "is not banned")
	refused(Mod{Action: ModBan, UserID: streamer}, "may not be banned")

	done(Mod{Action: ModWarn, UserID: ann, Reason: "be nice"})
	if r.mock.Warned("streamer", "ann") != "be nice" {
		t.Fatal("not warned")
	}

	id := r.mock.Say("streamer", "ann", "buy followers at …")
	done(Mod{Action: ModDelete, MessageID: id})
	if !r.mock.Deleted(id) {
		t.Fatal("not deleted")
	}
	refused(Mod{Action: ModDelete, MessageID: r.mock.Say("streamer", "mo", "hi")}, "another moderator")
	refused(Mod{Action: ModDelete, MessageID: "no-such-message"}, "not found")

	done(Mod{Action: ModVIP, UserID: ann})
	refused(Mod{Action: ModVIP, UserID: ann}, "already a VIP")
	refused(Mod{Action: ModMod, UserID: ann}, "first remove them as a VIP")
	done(Mod{Action: ModUnVIP, UserID: ann})
	refused(Mod{Action: ModUnVIP, UserID: ann}, "not a VIP")
	done(Mod{Action: ModMod, UserID: ann})
	if !r.mock.IsModerator("streamer", "ann") {
		t.Fatal("not a moderator")
	}
	done(Mod{Action: ModUnmod, UserID: ann})
	refused(Mod{Action: ModUnmod, UserID: ann}, "not a moderator")
	refused(Mod{Action: ModVIP, UserID: mo}, "first remove them as a moderator")

	done(Mod{Action: ModShoutout, UserID: ann})
	refused(Mod{Action: ModShoutout, UserID: mo}, "Twitch says to wait") // one every 2 minutes

	done(Mod{Action: ModRestrict, UserID: ann})
	if r.mock.Suspicious("streamer", "ann") != "RESTRICTED" {
		t.Fatal("not restricted")
	}
	done(Mod{Action: ModMonitor, UserID: ann})
	done(Mod{Action: ModUnsuspect, UserID: ann})
	if r.mock.Suspicious("streamer", "ann") != "" {
		t.Fatal("still suspicious")
	}

	// Twitch acts but the answer never comes: unknown, not refused.
	r.mock.DropMod("POST /helix/moderation/bans", true)
	if o := do(Mod{Action: ModTimeout, UserID: ann, Seconds: 60}); o.State != ModUnknown || !strings.Contains(o.Detail, "may or may not") {
		t.Fatalf("dropped: %+v", o)
	}
	r.mock.DropMod("POST /helix/moderation/bans", false)
	r.mock.FailMod("POST /helix/moderation/warnings", 500)
	if o := do(Mod{Action: ModWarn, UserID: ann, Reason: "x"}); o.State != ModUnknown {
		t.Fatalf("500: %+v", o)
	}
	r.mock.FailMod("POST /helix/moderation/warnings", 401)
	refused(Mod{Action: ModWarn, UserID: ann, Reason: "x"}, "Sign in to Twitch again")

	// Stopped before sending.
	calls := len(r.mock.ModCalls())
	for _, m := range []Mod{
		{Action: "kick", UserID: ann},
		{Action: ModTimeout, UserID: ann},
		{Action: ModTimeout, UserID: ann, Seconds: MaxTimeout + 1},
		{Action: ModBan, UserID: ann, Seconds: 5},
		{Action: ModWarn, UserID: ann},
		{Action: ModVIP, UserID: ann, Reason: "x"},
		{Action: ModBan, UserID: "ann"},
		{Action: ModDelete},
		{Action: ModBan, UserID: ann, Reason: strings.Repeat("x", MaxModReason+1)},
	} {
		if _, err := s.Moderate(ctx, m); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", m, err)
		}
	}
	if len(r.mock.ModCalls()) != calls {
		t.Fatal("an invalid request reached Twitch")
	}
}

// Signed out, nothing is sent and it says why.
func TestModerateSignedOut(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	o, err := s.Moderate(context.Background(), Mod{Action: ModBan, UserID: "123"})
	if err != nil || o.State != ModRefused || !strings.Contains(o.Detail, "Sign in") {
		t.Fatal(o, err)
	}
}

// Chat modes: read, changed, and Twitch's limits checked first.
func TestChatSettings(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	cs, err := s.ChatSettings(ctx)
	if err != nil || cs.Slow || cs.EmoteOnly {
		t.Fatal(cs, err)
	}
	on, wait := true, 10
	cs, o, err := s.ChangeChatSettings(ctx, ChatSettingsChange{Slow: &on, SlowSeconds: &wait, EmoteOnly: &on})
	if err != nil || o.State != ModDone || !cs.Slow || cs.SlowSeconds == nil || *cs.SlowSeconds != 10 || !cs.EmoteOnly {
		t.Fatal(cs, o, err)
	}
	r.mock.SetChatSettings("streamer", map[string]any{"subscriber_mode": true})
	if cs, _ = s.ChatSettings(ctx); !cs.SubscribersOnly || !cs.Slow {
		t.Fatal("changed on Twitch:", cs)
	}
	off := false
	if cs, o, _ = s.ChangeChatSettings(ctx, ChatSettingsChange{Slow: &off}); o.State != ModDone || cs.Slow || cs.SlowSeconds != nil {
		t.Fatal(cs, o)
	}
	big, small := MaxFollowerMinutes+1, 2
	for _, c := range []ChatSettingsChange{{}, {FollowersOnly: &on, FollowerMinutes: &big}, {Slow: &on, SlowSeconds: &small}, {SlowSeconds: &wait}} {
		if _, _, err := s.ChangeChatSettings(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	r.mock.FailMod("PATCH /helix/chat/settings", 403)
	if _, o, _ = s.ChangeChatSettings(ctx, ChatSettingsChange{EmoteOnly: &off}); o.State != ModRefused {
		t.Fatal(o)
	}
	r.mock.SetDown(true)
	if _, err := s.ChatSettings(ctx); err == nil {
		t.Fatal("Twitch down gave settings")
	}
}
