package twitch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Twitch's Blocked Terms (D-088) through mocktwitch: read across pages,
// add (again is the same term), Twitch's busy answer tried again, a too
// short term, remove (twice is fine), and an AutoMod term's end.
func TestBlockedTerms(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	was := blockedTermRetries
	blockedTermRetries = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { blockedTermRetries = was })

	got, err := s.BlockedTerms(ctx)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	// 100 makes a full page, whose cursor leads to an empty one.
	for i := range 100 {
		r.mock.BlockTerm("streamer", fmt.Sprintf("term%03d", i), time.Time{})
	}
	soon := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	r.mock.BlockTerm("streamer", "automodded", soon)
	got, err = s.BlockedTerms(ctx)
	if err != nil || len(got) != 101 {
		t.Fatal(len(got), err)
	}
	if got[0].Text != "automodded" || got[0].ExpiresAt == nil || !got[0].ExpiresAt.Equal(soon) || got[1].ExpiresAt != nil || got[1].ID == "" {
		t.Fatalf("%+v %+v", got[0], got[1])
	}

	a, err := s.AddBlockedTerm(ctx, "  spoiler*  ")
	if err != nil || a.Text != "spoiler*" || a.ID == "" {
		t.Fatal(a, err)
	}
	again, err := s.AddBlockedTerm(ctx, "spoiler*")
	if err != nil || again.ID != a.ID || len(r.mock.BlockedTerms("streamer")) != 102 {
		t.Fatal("again:", again, err)
	}
	r.mock.SetBlockedTermsBusy(2)
	if b, err := s.AddBlockedTerm(ctx, "busyword"); err != nil || b.Text != "busyword" {
		t.Fatal("busy twice, then added:", b, err)
	}
	r.mock.SetBlockedTermsBusy(3)
	if _, err := s.AddBlockedTerm(ctx, "stillbusy"); err == nil || !strings.Contains(err.Error(), "currently being updated") {
		t.Fatal("busy three times:", err)
	}
	for _, bad := range []string{"", " x ", strings.Repeat("y", 501)} {
		if _, err := s.AddBlockedTerm(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := s.AddBlockedTerm(ctx, "a*"); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatal("Twitch's own refusal:", err)
	}

	if err := s.RemoveBlockedTerm(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveBlockedTerm(ctx, a.ID); err != nil {
		t.Fatal("twice:", err)
	}
	for _, bt := range r.mock.BlockedTerms("streamer") {
		if bt.ID == a.ID {
			t.Fatal("still there")
		}
	}

	r.mock.SetDown(true)
	if _, err := s.AddBlockedTerm(ctx, "downword"); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatal("down:", err)
	}
}

// An old sign-in without the scope says to sign in again.
func TestBlockedTermsNeedTheScope(t *testing.T) {
	r := newRig(t)
	r.mock.GrantScopes = []string{"user:read:chat", "user:write:chat"}
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	if _, err := s.BlockedTerms(context.Background()); !errors.Is(err, ErrNeedsPermission) {
		t.Fatal(err)
	}
}
