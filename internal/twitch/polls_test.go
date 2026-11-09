package twitch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Polls through mocktwitch, which follows Twitch's documented rules: one
// at a time, events at each change, and the limits refused in words.
func TestPolls(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	n := NewPoll{Title: "What next?", Choices: []string{"Celeste", " Hades ", ""}, Seconds: 60}
	p, created, err := s.StartPoll(ctx, n)
	if err != nil || !created || len(p.Choices) != 2 || p.Choices[1].Title != "Hades" || p.Status != "active" {
		t.Fatal(p, created, err)
	}
	// Asking again after an unclear answer: the same poll, not a refusal.
	if again, created, err := s.StartPoll(ctx, n); err != nil || created || again.ID != p.ID {
		t.Fatal(again, created, err)
	}
	if _, _, err := s.StartPoll(ctx, NewPoll{Title: "Other?", Choices: []string{"a", "b"}, Seconds: 60}); !errors.Is(err, ErrPollRunning) {
		t.Fatal(err)
	}
	r.mock.Vote("streamer", 1, 3)
	r.mock.Vote("streamer", 0, 1)
	if _, err := s.EndPoll(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndPoll(ctx, false); !errors.Is(err, ErrNoPoll) {
		t.Fatal("ending twice:", err)
	}
	recs := waitEvents(t, r.inbox, 4)
	if recs[0].Type != "chat.poll.started" || recs[3].Type != "chat.poll.ended" {
		t.Fatalf("%v %v", recs[0].Type, recs[3].Type)
	}
	if p := payload(t, recs[2]); p["leader"] != "Hades" || p["votes"] != float64(4) || p["tied"] != false {
		t.Fatalf("%v", p)
	}
	if p := payload(t, recs[3]); p["status"] != "terminated" || p["leader"] != "Hades" {
		t.Fatalf("%v", p)
	}
	for _, bad := range []NewPoll{
		{Title: strings.Repeat("q", 61), Choices: []string{"a", "b"}, Seconds: 60},
		{Title: "q", Choices: []string{"a"}, Seconds: 60},
		{Title: "q", Choices: []string{"a", "b", "c", "d", "e", "f"}, Seconds: 60},
		{Title: "q", Choices: []string{"a", strings.Repeat("b", 26)}, Seconds: 60},
		{Title: "q", Choices: []string{"a", "b"}, Seconds: 14},
		{Title: "q", Choices: []string{"a", "b"}, Seconds: 1801},
		{Title: "q", Choices: []string{"a", "b"}, Seconds: 60, ChannelPointsPerVote: 1_000_001},
	} {
		if _, _, err := s.StartPoll(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestPredictions(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	n := NewPrediction{Title: "Will I win?", Outcomes: []string{"Yes", "No"}, Seconds: 120}
	p, created, err := s.StartPrediction(ctx, n)
	if err != nil || !created || len(p.Outcomes) != 2 || p.Outcomes[0].Color != "blue" || p.Outcomes[1].Color != "pink" {
		t.Fatal(p, created, err)
	}
	if again, created, err := s.StartPrediction(ctx, n); err != nil || created || again.ID != p.ID {
		t.Fatal(again, created, err)
	}
	r.mock.Bet("streamer", "Fan", 1, 500)
	if got, repeated, err := s.EndPrediction(ctx, PredictionLock, ""); err != nil || repeated || got.Status != "locked" {
		t.Fatal(got, repeated, err)
	}
	if _, _, err := s.EndPrediction(ctx, PredictionResolve, "maybe"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if got, _, err := s.EndPrediction(ctx, PredictionResolve, "2"); err != nil || got.Status != "resolved" {
		t.Fatal(got, err)
	}
	if st, winner := r.mock.PredictionStatus("streamer"); st != "RESOLVED" || winner != "No" {
		t.Fatal(st, winner)
	}
	if _, repeated, err := s.EndPrediction(ctx, PredictionResolve, "no"); err != nil || !repeated {
		t.Fatal("resolving again:", repeated, err)
	}
	if _, _, err := s.EndPrediction(ctx, PredictionCancel, ""); !errors.Is(err, ErrNoPrediction) {
		t.Fatal("cancel after resolve:", err)
	}
	recs := waitEvents(t, r.inbox, 4)
	types := []string{}
	for _, x := range recs {
		types = append(types, x.Type)
	}
	if strings.Join(types, ",") != "chat.prediction.started,chat.prediction.updated,chat.prediction.locked,chat.prediction.ended" {
		t.Fatal(types)
	}
	if end := payload(t, recs[3]); end["status"] != "resolved" || end["winner"] != "No" {
		t.Fatalf("%v", end)
	}
	// Cancel refunds: a new one, cancelled.
	if _, _, err := s.StartPrediction(ctx, NewPrediction{Title: "Again?", Outcomes: []string{"a", "b", "c"}, Seconds: 30}); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.EndPrediction(ctx, PredictionCancel, ""); err != nil || got.Status != "canceled" {
		t.Fatal(got, err)
	}
	for _, bad := range []NewPrediction{
		{Title: strings.Repeat("q", 46), Outcomes: []string{"a", "b"}, Seconds: 60},
		{Title: "q", Outcomes: []string{"a"}, Seconds: 60},
		{Title: "q", Outcomes: []string{"a", "b"}, Seconds: 29},
		{Title: "q", Outcomes: []string{"a", strings.Repeat("b", 26)}, Seconds: 60},
	} {
		if _, _, err := s.StartPrediction(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestPollsNeedAnAffiliateAndThePermission(t *testing.T) {
	ctx := context.Background()
	r, s := chatRig(t, false)
	r.mock.SetAffiliate("streamer", false)
	if _, _, err := s.StartPoll(ctx, NewPoll{Title: "q", Choices: []string{"a", "b"}, Seconds: 60}); !errors.Is(err, ErrNoChannelPoints) ||
		!strings.Contains(err.Error(), "Affiliate or Partner") {
		t.Fatal(err)
	}
	r2 := newRig(t)
	r2.mock.GrantScopes = []string{"user:read:chat", "user:write:chat"}
	s2 := r2.configured()
	r2.signIn(s2, RoleStreamer, "Streamer")
	if _, err := s2.LatestPoll(ctx); !errors.Is(err, ErrNeedsPermission) {
		t.Fatal(err)
	}
}
