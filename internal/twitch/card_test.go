package twitch

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The viewer card against mocktwitch: every fact known; a viewer who is
// nothing in particular (all "no", never unknown); a gift and an anonymous
// gift; a timeout and a timeout that has ended; one endpoint refusing or
// failing makes that fact unknown, with why, and leaves the rest; Twitch
// down makes every fact unknown, never "no".
func TestViewerCard(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	since := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	made := time.Date(2019, 5, 6, 0, 0, 0, 0, time.UTC)
	r.mock.SetFollower("streamer", "alice", since)
	r.mock.SetAccountCreated("alice", made)
	r.mock.SetSubMonths("streamer", "alice", 3)
	r.mock.SetVIP("streamer", "alice", true)
	r.mock.SetModerator("streamer", "alice", true)
	alice, bob := r.mock.UserID("alice"), r.mock.UserID("bob")

	c, err := s.ViewerCard(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if c.Login != "alice" || c.CreatedAt == nil || !c.CreatedAt.Equal(made) || c.Picture == "" {
		t.Fatalf("account: %+v", c)
	}
	if c.Follow == nil || !c.Follow.Follows || !c.Follow.FollowedAt.Equal(since) {
		t.Fatalf("follow: %+v", c.Follow)
	}
	if c.Subscription == nil || !c.Subscription.Subscribed || c.Subscription.Tier != 1 || c.Subscription.Gift {
		t.Fatalf("sub: %+v", c.Subscription)
	}
	if c.VIP == nil || !*c.VIP || c.Moderator == nil || !*c.Moderator || c.Ban == nil || c.Ban.Banned {
		t.Fatalf("vip %v mod %v ban %+v", c.VIP, c.Moderator, c.Ban)
	}
	if len(c.Unknown) != 0 {
		t.Fatal("unknown:", c.Unknown)
	}

	// Bob: nothing in particular, and every answer is a "no", not unknown.
	c, _ = s.ViewerCard(ctx, bob)
	if c.Follow == nil || c.Follow.Follows || c.Subscription == nil || c.Subscription.Subscribed ||
		c.VIP == nil || *c.VIP || c.Moderator == nil || *c.Moderator || c.Ban == nil || c.Ban.Banned || len(c.Unknown) != 0 {
		t.Fatalf("bob: %+v", c)
	}

	// Gifts: named and anonymous.
	r.mock.SetSubMonths("streamer", "bob", 1)
	r.mock.SetSubGift("streamer", "bob", "carol")
	if c, _ = s.ViewerCard(ctx, bob); !c.Subscription.Gift || c.Subscription.Gifter != "carol" || c.Subscription.Anonymous {
		t.Fatalf("gift: %+v", c.Subscription)
	}
	r.mock.SetSubGift("streamer", "bob", "")
	if c, _ = s.ViewerCard(ctx, bob); !c.Subscription.Gift || c.Subscription.Gifter != "" || !c.Subscription.Anonymous {
		t.Fatalf("anonymous gift: %+v", c.Subscription)
	}

	// Bans: for good, a timeout, and a timeout that has ended.
	r.mock.SetBanned("streamer", "bob", true, time.Time{}, "spam", "streamer")
	if c, _ = s.ViewerCard(ctx, bob); !c.Ban.Banned || c.Ban.Until != nil || c.Ban.Reason != "spam" || c.Ban.By != "streamer" {
		t.Fatalf("ban: %+v", c.Ban)
	}
	until := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	r.mock.SetBanned("streamer", "bob", true, until, "", "streamer")
	if c, _ = s.ViewerCard(ctx, bob); !c.Ban.Banned || c.Ban.Until == nil || !c.Ban.Until.Equal(until) {
		t.Fatalf("timeout: %+v", c.Ban)
	}
	r.mock.SetBanned("streamer", "bob", true, time.Now().Add(-time.Minute), "", "streamer")
	if c, _ = s.ViewerCard(ctx, bob); c.Ban == nil || c.Ban.Banned {
		t.Fatalf("ended timeout: %+v", c.Ban)
	}

	// One fact refused or failing: unknown, with why; the rest still read.
	r.mock.FailCard("/helix/channels/vips", 401)
	r.mock.FailCard("/helix/moderation/banned", 500)
	c, _ = s.ViewerCard(ctx, alice)
	if c.VIP != nil || c.Ban != nil || c.Moderator == nil || c.Follow == nil {
		t.Fatalf("partial: %+v", c)
	}
	if len(c.Unknown) != 2 || c.Unknown[0].Fact != FactVIP || c.Unknown[1].Fact != FactBanned ||
		c.Unknown[0].Why != "Sign in to Twitch again and allow StreamGremlin to see this." || c.Unknown[1].Why != "Twitch didn't answer." {
		t.Fatalf("unknown: %+v", c.Unknown)
	}
	r.mock.FailCard("/helix/channels/vips", 0)
	r.mock.FailCard("/helix/moderation/banned", 0)

	// Twitch down: every fact unknown, none "no".
	r.mock.SetDown(true)
	c, err = s.ViewerCard(ctx, bob)
	if err != nil || c.Follow != nil || c.Subscription != nil || c.VIP != nil || c.Moderator != nil || c.Ban != nil || len(c.Unknown) != 6 {
		t.Fatalf("down: %+v %v", c, err)
	}
	if c.Unknown[0].Fact != FactAccount {
		t.Fatal("order:", c.Unknown)
	}

	if _, err := s.ViewerCard(ctx, "not-digits"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// Signed out: every fact unknown, saying to sign in.
func TestViewerCardSignedOut(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	c, err := s.ViewerCard(context.Background(), "123")
	if err != nil || len(c.Unknown) != 6 || c.Unknown[0].Why != "Sign in with your Twitch account to see this." {
		t.Fatalf("%+v %v", c, err)
	}
}
