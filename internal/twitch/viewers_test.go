package twitch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Viewer facts against mocktwitch: a follower and since when, someone who
// doesn't follow, an account's age, a viewer who unfollows (never cached),
// a token without the followers scope, and Twitch down.
func TestViewerFacts(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	since := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	r.mock.SetFollower("streamer", "alice", since)
	alice, bob := r.mock.UserID("alice"), r.mock.UserID("bob")
	if f, err := s.Follower(ctx, alice); err != nil || !f.Follows || !f.FollowedAt.Equal(since) {
		t.Fatal("alice:", f, err)
	}
	if f, err := s.Follower(ctx, bob); err != nil || f.Follows {
		t.Fatal("bob:", f, err)
	}
	r.mock.SetFollower("streamer", "alice", time.Time{})
	if f, err := s.Follower(ctx, alice); err != nil || f.Follows {
		t.Fatal("alice unfollowed, but:", f, err)
	}
	made := time.Date(2019, 5, 6, 0, 0, 0, 0, time.UTC)
	r.mock.SetAccountCreated("alice", made)
	if at, err := s.AccountCreated(ctx, alice); err != nil || !at.Equal(made) {
		t.Fatal(at, err)
	}
	if _, err := s.Follower(ctx, "not-digits"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	r.mock.FailFollowers(401)
	if _, err := s.Follower(ctx, bob); !errors.Is(err, ErrNeedsPermission) {
		t.Fatal("401:", err)
	}
	r.mock.FailFollowers(0)
	r.mock.SetDown(true)
	if _, err := s.Follower(ctx, bob); err == nil {
		t.Fatal("Twitch down gave an answer")
	}
	if _, err := s.AccountCreated(ctx, bob); err == nil {
		t.Fatal("Twitch down gave an answer")
	}
}

// Get Chatters against mocktwitch: pages followed to the end, the streamer
// and the bot left out, each viewer once; without the scope, Twitch
// refusing, and Twitch down are errors, never an empty list.
func TestChatters(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, true)
	logins := []string{"streamer", "gremlinbot"}
	for i := range 2100 {
		logins = append(logins, fmt.Sprintf("viewer%d", i))
	}
	logins = append(logins, "viewer7") // listed twice
	r.mock.SetChatters("streamer", logins...)
	got, err := s.Chatters(ctx)
	if err != nil || len(got) != 2100 || got[0].Login != "viewer0" || got[0].Platform != "twitch" || got[0].ID != r.mock.UserID("viewer0") {
		t.Fatal(len(got), err)
	}
	if n := r.mock.ChattersCalls(); n != 3 {
		t.Fatal("pages:", n)
	}
	r.mock.SetChatters("streamer")
	if got, err := s.Chatters(ctx); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	r.mock.FailChatters(403)
	if _, err := s.Chatters(ctx); err == nil {
		t.Fatal("a refusal gave an answer")
	}
	r.mock.FailChatters(401)
	if _, err := s.Chatters(ctx); !errors.Is(err, ErrNeedsPermission) {
		t.Fatal("401:", err)
	}
	r.mock.FailChatters(0)
	r.mock.SetDown(true)
	if _, err := s.Chatters(ctx); err == nil {
		t.Fatal("Twitch down gave an answer")
	}
}
