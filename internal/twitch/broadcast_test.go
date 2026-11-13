package twitch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The channel's own requests (D-086) through mocktwitch: the title, the
// category by exact name or Twitch's search, the stream read while
// offline and live, a marker and a clip only while live, a clip that
// never appears, and follow age by name.
func TestChannelRequests(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	s.clipChecks = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}

	if err := s.SetTitle(ctx, "  Building a bot  "); err != nil {
		t.Fatal(err)
	}
	if title, _ := r.mock.Channel("streamer"); title != "Building a bot" {
		t.Fatalf("title %q", title)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("é", 141)} {
		if err := s.SetTitle(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	c, err := s.SetCategory(ctx, "Minecraft")
	if err != nil || c.Name != "Minecraft" || c.ID != "27471" || !c.Exact {
		t.Fatal(c, err)
	}
	c, err = s.SetCategory(ctx, "elden")
	if err != nil || c.Name != "Elden Ring" || c.Exact {
		t.Fatal("searched:", c, err)
	}
	if _, cat := r.mock.Channel("streamer"); cat != "Elden Ring" {
		t.Fatalf("category %q", cat)
	}
	if _, err := s.SetCategory(ctx, "no such game anywhere"); !errors.Is(err, ErrNoCategory) {
		t.Fatal(err)
	}
	if _, err := s.SetCategory(ctx, " "); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}

	st, err := s.Stream(ctx)
	if err != nil || st.Live || st.StartedAt != nil || st.Title != "Building a bot" || st.Category != "Elden Ring" {
		t.Fatalf("offline: %+v %v", st, err)
	}
	if _, err := s.CreateMarker(ctx, "here"); !errors.Is(err, ErrNotLive) {
		t.Fatal("marker offline:", err)
	}
	if _, err := s.CreateClip(ctx, "", 0); !errors.Is(err, ErrNotLive) {
		t.Fatal("clip offline:", err)
	}

	r.mock.GoLive("streamer")
	r.mock.SetViewers("streamer", 12)
	st, err = s.Stream(ctx)
	if err != nil || !st.Live || st.StartedAt == nil || st.Viewers != 12 || st.Title != "Building a bot" {
		t.Fatalf("live: %+v %v", st, err)
	}
	m, err := s.CreateMarker(ctx, "a great moment")
	if err != nil || m.ID == "" || len(r.mock.Markers()) != 1 || r.mock.Markers()[0].Description != "a great moment" {
		t.Fatal(m, err)
	}
	if _, err := s.CreateMarker(ctx, strings.Repeat("m", 141)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}

	clip, err := s.CreateClip(ctx, "What a jump", 20)
	if err != nil || clip.ID == "" || clip.URL != "https://clips.twitch.tv/"+clip.ID || clip.EditURL == "" {
		t.Fatal(clip, err)
	}
	if got := r.mock.Clips(); len(got) != 1 || got[0].Title != "What a jump" || got[0].Duration != 20 {
		t.Fatalf("%+v", got)
	}
	for _, d := range []float64{4, 61} {
		if _, err := s.CreateClip(ctx, "", d); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v seconds: %v", d, err)
		}
	}
	// A clip that takes a moment is waited for; one Twitch never makes fails.
	r.mock.SetClipDelay(15*time.Millisecond, false)
	if _, err := s.CreateClip(ctx, "", 0); err != nil {
		t.Fatal("slow clip:", err)
	}
	r.mock.SetClipDelay(0, true)
	if _, err := s.CreateClip(ctx, "", 0); !errors.Is(err, ErrClipNotMade) {
		t.Fatal("failed clip:", err)
	}

	r.mock.Say("streamer", "fan", "hi")
	r.mock.SetFollower("streamer", "fan", time.Now().Add(-400*24*time.Hour))
	f, err := s.FollowerByLogin(ctx, "@Fan")
	if err != nil || !f.Follows || f.Login != "fan" || time.Since(f.FollowedAt) < 399*24*time.Hour {
		t.Fatal(f, err)
	}
	r.mock.Say("streamer", "lurker", "hi")
	if f, err := s.FollowerByLogin(ctx, "lurker"); err != nil || f.Follows {
		t.Fatal(f, err)
	}
	if _, err := s.FollowerByLogin(ctx, "nobody_here_at_all"); !errors.Is(err, ErrNoSuchUser) {
		t.Fatal(err)
	}

	// Twitch down: a clip's fate is unknown, never sent again.
	r.mock.SetDown(true)
	if _, err := s.CreateClip(ctx, "", 0); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatal("down:", err)
	}
}

// An old sign-in without the new scopes says to sign in again.
func TestChannelRequestsNeedTheScopes(t *testing.T) {
	r := newRig(t)
	r.mock.GrantScopes = []string{"user:read:chat", "user:write:chat"}
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	ctx := context.Background()
	if err := s.SetTitle(ctx, "x"); !errors.Is(err, ErrNeedsPermission) {
		t.Fatal(err)
	}
	if _, err := s.CreateClip(ctx, "", 0); !errors.Is(err, ErrNeedsPermission) {
		t.Fatal(err)
	}
}

func TestDurationWords(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:            "less than a minute",
		time.Minute:                 "1 minute",
		2*time.Hour + 5*time.Minute: "2 hours 5 minutes",
		3 * time.Hour:               "3 hours",
		26 * time.Hour:              "1 day 2 hours",
	} {
		if got := DurationWords(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	at := time.Date(2024, 3, 31, 12, 0, 0, 0, time.UTC)
	for now, want := range map[time.Time]string{
		time.Date(2024, 3, 31, 18, 0, 0, 0, time.UTC): "today",
		time.Date(2024, 4, 20, 12, 0, 0, 0, time.UTC): "20 days",
		time.Date(2024, 4, 30, 12, 0, 0, 0, time.UTC): "30 days", // a month later by the calendar is still under one
		time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC):  "1 month",
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC): "2 years 2 months",
		time.Date(2025, 3, 31, 12, 0, 0, 0, time.UTC): "1 year",
	} {
		if got := SinceWords(at, now); got != want {
			t.Errorf("%v: %q, want %q", now, got, want)
		}
	}
}
