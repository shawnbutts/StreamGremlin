package twitch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// Rewards against mocktwitch, which follows Twitch's documented rules:
// made, listed (managed or not), changed, refused when made elsewhere,
// redemptions completed and refunded once, deleted.
func TestChannelPointRewards(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	s := r.configured()
	if _, err := s.Rewards(ctx); !errors.Is(err, ErrNotSignedIn) {
		t.Fatal("rewards without a sign-in:", err)
	}
	r.signIn(s, RoleStreamer, "Streamer")
	dash := r.mock.AddDashboardReward("streamer", "Hydrate", 100)

	hug, created, err := s.CreateReward(ctx, RewardChange{Title: ptr("Group hug"), Cost: ptr(500), Prompt: ptr("Who gets it?"),
		InputRequired: ptr(true), CooldownSeconds: ptr(60)})
	if err != nil || !created || !hug.Managed || hug.Cost != 500 || hug.CooldownSeconds != 60 || !hug.InputRequired {
		t.Fatal(hug, created, err)
	}
	// Asking again after an unclear answer doesn't make a second one.
	again, created, err := s.CreateReward(ctx, RewardChange{Title: ptr("group hug"), Cost: ptr(500)})
	if err != nil || created || again.ID != hug.ID {
		t.Fatal(again, created, err)
	}
	// Another reward's title is refused, in words.
	if _, _, err := s.CreateReward(ctx, RewardChange{Title: ptr("Hydrate"), Cost: ptr(5)}); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "has that title") {
		t.Fatal(err)
	}
	for _, bad := range []RewardChange{{Title: ptr(""), Cost: ptr(1)}, {Title: ptr(strings.Repeat("x", 46)), Cost: ptr(1)},
		{Title: ptr("x"), Cost: ptr(0)}, {Title: ptr("x"), Cost: ptr(1), Prompt: ptr(strings.Repeat("p", 201))},
		{Title: ptr("x"), Cost: ptr(1), CooldownSeconds: ptr(MaxRewardCooldown + 1)}, {Title: ptr("x"), Cost: ptr(1), BackgroundColor: ptr("purple")}} {
		if _, _, err := s.CreateReward(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	list, err := s.Rewards(ctx)
	if err != nil || len(list) != 2 || list[0].Title != "Group hug" || !list[0].Managed || list[1].Title != "Hydrate" || list[1].Managed {
		t.Fatalf("%+v %v", list, err)
	}
	if got, err := s.Reward(ctx, "HYDRATE"); err != nil || got.ID != dash {
		t.Fatal(got, err)
	}
	// A reward made on Twitch is trigger only.
	if _, err := s.UpdateReward(ctx, dash, RewardChange{Paused: ptr(true)}); !errors.Is(err, ErrNotManaged) {
		t.Fatal(err)
	}
	if _, err := s.Redemptions(ctx, dash); !errors.Is(err, ErrNotManaged) {
		t.Fatal(err)
	}
	up, err := s.UpdateReward(ctx, hug.ID, RewardChange{Paused: ptr(true), Cost: ptr(750), CooldownSeconds: ptr(0)})
	if err != nil || !up.Paused || up.Cost != 750 || up.CooldownSeconds != 0 || up.Title != "Group hug" {
		t.Fatal(up, err)
	}

	// Redemptions: done once, a repeat harmless, the other way refused.
	first := r.mock.Redeem("streamer", "Fan", "Group hug", 0, "everyone")
	second := r.mock.Redeem("streamer", "Pal", "Group hug", 0, "me")
	waiting, err := s.Redemptions(ctx, hug.ID)
	if err != nil || len(waiting) != 2 || waiting[0].ID != first || waiting[0].Input != "everyone" || waiting[0].Status != "unfulfilled" {
		t.Fatalf("%+v %v", waiting, err)
	}
	if d, repeated, err := s.SetRedemption(ctx, hug.ID, first, true); err != nil || repeated || d.Status != "fulfilled" {
		t.Fatal(d, repeated, err)
	}
	if _, repeated, err := s.SetRedemption(ctx, hug.ID, first, true); err != nil || !repeated {
		t.Fatal(repeated, err)
	}
	if _, _, err := s.SetRedemption(ctx, hug.ID, first, false); !errors.Is(err, ErrAlreadyHandled) || !strings.Contains(err.Error(), "done") {
		t.Fatal(err)
	}
	if _, _, err := s.SetRedemption(ctx, hug.ID, second, false); err != nil || r.mock.RedemptionStatus(second) != "CANCELED" {
		t.Fatal(err, r.mock.RedemptionStatus(second))
	}
	if waiting, _ := s.Redemptions(ctx, hug.ID); len(waiting) != 0 {
		t.Fatal(waiting)
	}

	if ok, err := s.DeleteReward(ctx, hug.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := s.DeleteReward(ctx, hug.ID); ok || err != nil {
		t.Fatal("deleting again:", ok, err)
	}
	if _, err := s.DeleteReward(ctx, dash); !errors.Is(err, ErrNotManaged) {
		t.Fatal(err)
	}
}

func TestRewardsNeedChannelPointsAndThePermission(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "Streamer")
	r.mock.SetAffiliate("streamer", false)
	if _, err := s.Rewards(ctx); !errors.Is(err, ErrNoChannelPoints) || !strings.Contains(err.Error(), "Affiliate or Partner") {
		t.Fatal(err)
	}
	if _, _, err := s.CreateReward(ctx, RewardChange{Title: ptr("x"), Cost: ptr(1)}); !errors.Is(err, ErrNoChannelPoints) {
		t.Fatal(err)
	}

	// An older sign-in without the permission: reading still works with the
	// read permission, changing asks to sign in again.
	r2 := newRig(t)
	r2.mock.GrantScopes = []string{"user:read:chat", "user:write:chat", "channel:read:redemptions"}
	s2 := r2.configured()
	r2.signIn(s2, RoleStreamer, "Streamer")
	if _, err := s2.Rewards(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s2.CreateReward(ctx, RewardChange{Title: ptr("x"), Cost: ptr(1)}); !errors.Is(err, ErrNeedsManage) {
		t.Fatal(err)
	}
}
