package giveaways

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Subscribers given 3 chances: chat says so instead of "the same chance";
// each entry's chances are set as it enters and kept; the fingerprint
// includes them; a draw picks a ticket among those still held; the record
// checks, and a changed chance doesn't.
func TestSubscriberChances(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, err := r.svc.Open(ctx, Settings{Title: "a key", SubscriberChances: 3, Winners: 2}, "app")
	if err != nil || !g.Weighted() {
		t.Fatal(g, err)
	}
	r.eventually("the opening", func() bool {
		all := r.chat.all()
		return strings.Contains(all, "One entry each, and subscribers get 3 chances to everyone else's 1; 2 winners will be drawn.") &&
			!strings.Contains(all, "same chance")
	})
	r.say("ann", "join", false, "subscriber")
	r.say("bob", "join", false)
	r.say("cat", "join", false)
	r.entries(g.ID, 3)
	r.eventually("you're in, with chances", func() bool {
		all := r.chat.all()
		return strings.Contains(all, "@ann (3 chances)") && strings.Contains(all, "@cat") && !strings.Contains(all, "@bob (")
	})
	d := r.detail(g.ID)
	if d.Entries[0].Chances != 3 || d.Entries[1].Chances != 1 || d.Entries[2].Chances != 1 {
		t.Fatal(d.Entries)
	}
	draws, err := r.svc.Draw(ctx, g.ID, 1)
	if err != nil || len(draws) != 1 || draws[0].Candidates != 5 {
		t.Fatal("5 tickets in play:", draws, err)
	}
	draws, err = r.svc.Draw(ctx, g.ID, 1)
	if err != nil || draws[0].Candidates != 5-max(r.detail(g.ID).Draws[0].Winner.Chances, 1) {
		t.Fatal("the first winner's tickets leave play:", draws, err)
	}
	d = r.detail(g.ID)
	if d.Fingerprint != Fingerprint(d.Entries, true) || d.Fingerprint == Fingerprint(d.Entries, false) {
		t.Fatal("the fingerprint doesn't include chances")
	}
	if bad := Verify(d); len(bad) != 0 {
		t.Fatal(bad)
	}
	changed := d
	changed.Entries = append([]Entry(nil), d.Entries...)
	changed.Entries[1].Chances = 5
	if bad := Verify(changed); len(bad) == 0 {
		t.Fatal("a changed chance passed the check")
	}
	if _, err := r.db.ExecContext(ctx, "UPDATE giveaway_entries SET chances = 10 WHERE giveaway_id = ?", g.ID); err == nil {
		t.Fatal("an entry's chances changed")
	}
	if _, err := r.db.ExecContext(ctx, "UPDATE giveaways SET subscriber_chances = 1 WHERE id = ?", g.ID); err == nil {
		t.Fatal("a giveaway's chances changed")
	}
}

// Tickets run in entry order, each entry's in a row.
func TestTickets(t *testing.T) {
	es := []Entry{{Seq: 1, Chances: 3}, {Seq: 2, Chances: 1}, {Seq: 3}, {Seq: 4, Chances: 2}}
	if n := Tickets(es); n != 7 {
		t.Fatal(n)
	}
	want := []int{0, 0, 0, 1, 2, 3, 3}
	for ticket, at := range want {
		if got := Holder(es, ticket); got != at {
			t.Fatalf("ticket %d: entry %d, want %d", ticket, got, at)
		}
	}
	if Holder(es, 7) != -1 {
		t.Fatal("a ticket out of range")
	}
}

// Picking tickets with crypto/rand: an entry with 3 chances wins about 3
// times as often as one with 1.
func TestWeightedPicksFollowTheChances(t *testing.T) {
	es := []Entry{{Seq: 1, Chances: 3}, {Seq: 2, Chances: 1}}
	wins := [2]int{}
	const n = 8000
	for i := 0; i < n; i++ {
		ticket, err := pick(Tickets(es))
		if err != nil {
			t.Fatal(err)
		}
		wins[Holder(es, ticket)]++
	}
	// Expect 6000 and 2000; the bounds are over 8 standard deviations.
	if wins[0] < 5700 || wins[0] > 6300 {
		t.Fatal(wins)
	}
}

// The weighted line format, the same in the app.
func TestTheWeightedFingerprintFormat(t *testing.T) {
	got := Fingerprint([]Entry{{Seq: 1, Platform: "twitch", UserID: "u1", Chances: 3}, {Seq: 2, Platform: "twitch", UserID: "u2", Chances: 1}}, true)
	if got != "a5af751606ed5138d5914531a4283e9fce9b5391ceeb02ad250abf3b4f8faa97" { // printf '1\ttwitch:u1\t3\n2\ttwitch:u2\t1\n' | shasum -a 256
		t.Fatal(got)
	}
}

func TestChanceLimits(t *testing.T) {
	r := newRig(t)
	for _, st := range []Settings{{Title: "x", SubscriberChances: 11}, {Title: "x", SubscriberChances: -1},
		{Title: "x", SubscriberChances: 2, SubscribersOnly: true}, {Title: "x", SubscriberChances: 2, Rules: Rules{SubMonths: 1}}} {
		if _, err := r.svc.Open(context.Background(), st, "app"); !errors.Is(err, ErrInvalid) {
			t.Error(st, err)
		}
	}
	g, err := r.svc.Open(context.Background(), Settings{Title: "plain"}, "app")
	if err != nil || g.SubscriberChances != 1 || g.Weighted() {
		t.Fatal(g, err)
	}
}
