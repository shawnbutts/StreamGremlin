package twitch

import (
	"context"
	"errors"
	"testing"
)

func refusal(t *testing.T, err error) string {
	t.Helper()
	var r *AdRefusedError
	if !errors.As(err, &r) {
		t.Fatalf("not a refusal: %v", err)
	}
	return r.Reason
}

// Ads against mocktwitch: offline refused (nothing happened), live
// accepted, the cooldown refused, snoozes until none are left, Twitch down
// is unknown, and a break event, even in the shapes Twitch's examples use.
func TestAds(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	if sc, err := s.Ads(ctx); err != nil || sc.NextAdAt != nil || sc.LastAdAt != nil {
		t.Fatal("offline:", sc, err)
	}
	if _, err := s.StartCommercial(ctx, 60); refusal(t, err) != "not_live" {
		t.Fatal(err)
	}
	if _, err := s.StartCommercial(ctx, 181); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	r.mock.HoldAdEvents(true)
	r.mock.GoLive("streamer")
	res, err := s.StartCommercial(ctx, 90)
	if err != nil || res.Length != 90 || res.RetryAfter != 480 {
		t.Fatal(res, err)
	}
	if _, err := s.StartCommercial(ctx, 90); refusal(t, err) != "cooldown" {
		t.Fatal(err)
	}
	sc, err := s.Ads(ctx)
	if err != nil || sc.NextAdAt == nil || sc.LastAdAt == nil || sc.SnoozeCount != 3 {
		t.Fatal(sc, err)
	}
	for i := 2; i >= 0; i-- {
		after, err := s.SnoozeAd(ctx)
		if err != nil || after.SnoozeCount != i || !after.NextAdAt.After(*sc.NextAdAt) {
			t.Fatal(after, err)
		}
	}
	if _, err := s.SnoozeAd(ctx); refusal(t, err) != "no_snoozes" {
		t.Fatal(err)
	}
	r.mock.SetDown(true)
	if _, err := s.StartCommercial(ctx, 30); !errors.Is(err, ErrAdOutcomeUnknown) {
		t.Fatal("Twitch down:", err)
	}
	r.mock.SetDown(false)

	r.mock.AdBreak("streamer", 90, true)
	recs := waitEvents(t, r.inbox, 2) // going live, then the break
	var got bool
	for _, x := range recs {
		if x.Type == "ads.break.started" {
			p := payload(t, x)
			if p["duration_seconds"] != float64(90) || p["is_automatic"] != true || x.Actor != nil || x.SourceEventID[:9] != "ad_break:" {
				t.Fatalf("%+v %v", x, p)
			}
			got = true
		}
	}
	if !got {
		t.Fatal("no break event")
	}

	r.mock.SetAffiliate("streamer", false)
	if _, err := s.StartCommercial(ctx, 30); refusal(t, err) != "not_eligible" {
		t.Fatal(err)
	}
}

func TestAdTimesInEveryDocumentedShape(t *testing.T) {
	for raw, want := range map[string]string{
		`"2026-11-09T20:00:00Z"`: "2026-11-09T20:00:00Z", `1794254400`: "2026-11-09T20:00:00Z", `"1794254400"`: "2026-11-09T20:00:00Z",
		`""`: "", `0`: "", `null`: "",
	} {
		got, err := adTime([]byte(raw))
		if err != nil || (want == "" && got != nil) || (want != "" && (got == nil || got.Format("2006-01-02T15:04:05Z") != want)) {
			t.Errorf("%s: %v %v", raw, got, err)
		}
	}
	if _, err := adTime([]byte(`"soon"`)); err == nil {
		t.Error("a time that can't be read was guessed")
	}
}
