package rounds

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

type rig struct {
	s      *Service
	ledger *points.Ledger
	vs     *viewers.Service
	db     *storage.DB
	now    time.Time
	rolls  []int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{ledger: points.New(db, nil), vs: viewers.New(db, nil, nil), db: db, now: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
	r.s = New(Options{DB: db, Ledger: r.ledger, Viewers: r.vs, Now: func() time.Time { return r.now }, Roll: func(sides int) int {
		v := r.rolls[0]
		r.rolls = r.rolls[1:]
		return v
	}})
	return r
}

var lim = Limits{MaxStake: 100, MaxMultiplier: 2.5, MaxRoundSeconds: 60}

func fan(name string) *events.Actor {
	return &events.Actor{Platform: "twitch", ID: "id-" + strings.ToLower(name), Login: strings.ToLower(name), DisplayName: name}
}

// give gives a viewer points to play with.
func (r *rig) give(t *testing.T, a *events.Actor, n int64) string {
	t.Helper()
	v, err := r.vs.Ensure(context.Background(), *a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ledger.Credit(context.Background(), points.Op{ID: "seed:" + v.ID, Viewer: v.ID, Amount: n, By: "test"}); err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func (r *rig) bal(t *testing.T, id string) int64 {
	t.Helper()
	b, err := r.ledger.Balance(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func isRefused(err error, want string) bool {
	return errors.Is(err, ErrRefused) && strings.Contains(err.Error(), want)
}

// A round: stakes taken once, rolls recorded, payouts within the limits,
// each once; the ledger balances.
func TestARound(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	f, p := r.give(t, fan("Fan"), 200), r.give(t, fan("Pal"), 30)
	rd, err := r.s.Open(ctx, "dice", "1.3.0", "Gamble", "Roll 1-100: 60 or more pays twice the stake.", "job/1", lim)
	if err != nil {
		t.Fatal(err)
	}
	// The same key is the same round.
	if again, err := r.s.Open(ctx, "dice", "1.3.0", "Gamble", "Roll 1-100: 60 or more pays twice the stake.", "job/1", lim); err != nil || again.ID != rd.ID {
		t.Fatalf("again: %v %v", again.ID, err)
	}
	st, bal, err := r.s.Stake(ctx, "dice", rd.ID, fan("Fan"), 50, lim)
	if err != nil || st.Amount != 50 || bal != 150 || st.Name != "Fan" {
		t.Fatalf("stake: %+v %d %v", st, bal, err)
	}
	for _, c := range []struct {
		actor  *events.Actor
		amount int64
		want   string
	}{
		{fan("Fan"), 10, "Fan is in this round already, with 50"},
		{fan("Pal"), 50, "Pal has 30 points, not 50"},
		{fan("Pal"), 101, "a stake is 1 to 100"},
		{nil, 10, "only a viewer's own command"},
	} {
		if _, _, err := r.s.Stake(ctx, "dice", rd.ID, c.actor, c.amount, lim); !isRefused(err, c.want) {
			t.Errorf("%d: %v, want %q", c.amount, err, c.want)
		}
	}
	if _, _, err := r.s.Stake(ctx, "dice", rd.ID, fan("Pal"), 30, lim); err != nil {
		t.Fatal(err)
	}
	if b, err := r.s.Balance(ctx, fan("Pal"), lim); err != nil || b != 0 {
		t.Errorf("Pal's balance: %d %v", b, err)
	}
	if _, _, err := r.s.Stake(ctx, "other", rd.ID, fan("Pal"), 1, lim); !isRefused(err, "there's no round") {
		t.Errorf("another plugin's round: %v", err)
	}
	r.rolls = []int{73}
	if v, err := r.s.Roll(ctx, "dice", rd.ID, "Fan's roll", 100); err != nil || v != 73 {
		t.Fatalf("roll: %d %v", v, err)
	}
	if _, err := r.s.Roll(ctx, "dice", rd.ID, "x", 1); !isRefused(err, "2 to 1000000 sides") {
		t.Errorf("1 side: %v", err)
	}
	// Payouts: at most 2.5 × the stake, only to stakers.
	if err := r.s.Settle(ctx, "dice", rd.ID, map[string]int64{f: 126}, lim); !isRefused(err, "0 to 125 for a stake of 50") {
		t.Errorf("too much: %v", err)
	}
	if err := r.s.Settle(ctx, "dice", rd.ID, map[string]int64{"v_nobody": 1}, lim); !isRefused(err, "no stake") {
		t.Errorf("nobody: %v", err)
	}
	if err := r.s.Settle(ctx, "dice", rd.ID, map[string]int64{f: 100}, lim); err != nil {
		t.Fatal(err)
	}
	if r.bal(t, f) != 250 || r.bal(t, p) != 0 {
		t.Errorf("balances: Fan %d, Pal %d", r.bal(t, f), r.bal(t, p))
	}
	if err := r.s.Settle(ctx, "dice", rd.ID, map[string]int64{f: 100}, lim); !isRefused(err, "settled") {
		t.Errorf("twice: %v", err)
	}
	if _, err := r.s.Open(ctx, "dice", "1.3.0", "Gamble", "rules", "job/1", lim); !isRefused(err, "settled already") {
		t.Errorf("reopened: %v", err)
	}
	list, err := r.s.List(ctx, "dice", 10)
	if err != nil || len(list) != 1 || list[0].State != "settled" || len(list[0].Stakes) != 2 || len(list[0].Rolls) != 1 ||
		list[0].Rolls[0].Value != 73 || list[0].Payouts[f] != 100 || list[0].Rules != "Roll 1-100: 60 or more pays twice the stake." {
		t.Fatalf("list: %+v %v", list, err)
	}
	if problems, err := r.ledger.Audit(ctx); err != nil || len(problems) != 0 {
		t.Errorf("ledger: %v %v", problems, err)
	}
	// Rolls are append-only.
	if _, err := r.db.ExecContext(ctx, "DELETE FROM plugin_round_rolls"); err == nil {
		t.Error("a roll was deleted")
	}
}

// Rounds that can't finish are refunded, each stake once.
func TestRefunds(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	f := r.give(t, fan("Fan"), 100)
	open := func(key string) Round {
		t.Helper()
		rd, err := r.s.Open(ctx, "dice", "1.3.0", "Round "+key, "rules", key, lim)
		if err != nil {
			t.Fatal(err)
		}
		return rd
	}
	a := open("a")
	if _, _, err := r.s.Stake(ctx, "dice", a.ID, fan("Fan"), 40, lim); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Cancel(ctx, "dice", a.ID, "nobody else joined"); err != nil {
		t.Fatal(err)
	}
	if r.bal(t, f) != 100 {
		t.Fatalf("after cancel: %d", r.bal(t, f))
	}
	if err := r.s.Cancel(ctx, "dice", a.ID, ""); !isRefused(err, "refunded") {
		t.Errorf("twice: %v", err)
	}

	// Past its time: refused, then swept.
	b := open("b")
	if _, _, err := r.s.Stake(ctx, "dice", b.ID, fan("Fan"), 10, lim); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(61 * time.Second)
	if _, _, err := r.s.Stake(ctx, "dice", b.ID, fan("Fan"), 10, lim); !isRefused(err, "ran out of time") {
		t.Errorf("late: %v", err)
	}
	if n, err := r.s.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if r.bal(t, f) != 100 {
		t.Errorf("after sweep: %d", r.bal(t, f))
	}

	// Up to three open; the plugin stopping refunds them.
	for _, k := range []string{"c", "d", "e"} {
		rd := open(k)
		if _, _, err := r.s.Stake(ctx, "dice", rd.ID, fan("Fan"), 5, lim); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.s.Open(ctx, "dice", "1.3.0", "Four", "rules", "f", lim); !isRefused(err, "up to 3 rounds open") {
		t.Errorf("fourth: %v", err)
	}
	if n, err := r.s.RefundOpen(ctx, "dice", "the plugin was turned off"); err != nil || n != 3 {
		t.Fatalf("refund open: %d %v", n, err)
	}
	if r.bal(t, f) != 100 {
		t.Errorf("after turning off: %d", r.bal(t, f))
	}
	list, _ := r.s.List(ctx, "dice", 10)
	reasons := map[string]bool{}
	for _, rd := range list {
		reasons[rd.Reason] = true
	}
	if !reasons["the plugin was turned off"] || !reasons["it ran past its 1m0s"] || !reasons["nobody else joined"] {
		t.Errorf("reasons: %v", reasons)
	}
	if problems, err := r.ledger.Audit(ctx); err != nil || len(problems) != 0 {
		t.Errorf("ledger: %v %v", problems, err)
	}
}

// A stop in the middle: a settling round is paid when StreamGremlin
// starts, each payout once; an open one is refunded from what the ledger
// holds, even if the round's own list missed the stake.
func TestRecover(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	f, p := r.give(t, fan("Fan"), 100), r.give(t, fan("Pal"), 100)
	a, _ := r.s.Open(ctx, "dice", "1.3.0", "A", "rules", "a", lim)
	_, _, _ = r.s.Stake(ctx, "dice", a.ID, fan("Fan"), 20, lim) // checked by the balances below
	_, _, _ = r.s.Stake(ctx, "dice", a.ID, fan("Pal"), 20, lim)
	// Paid Fan, then stopped before Pal and before closing.
	if _, err := r.db.ExecContext(ctx, `UPDATE plugin_rounds SET state = 'settling', payouts = ? WHERE id = ?`, `{"`+f+`":40,"`+p+`":30}`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ledger.Credit(ctx, points.Op{ID: opPrefix("dice", a.ID) + "pay:" + f, Viewer: f, Amount: 40, By: "plugin dice"}); err != nil {
		t.Fatal(err)
	}
	b, _ := r.s.Open(ctx, "dice", "1.3.0", "B", "rules", "b", lim)
	_, _, _ = r.s.Stake(ctx, "dice", b.ID, fan("Fan"), 10, lim)
	if _, err := r.db.ExecContext(ctx, "DELETE FROM plugin_round_stakes WHERE round_id = ?", b.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if r.bal(t, f) != 120 || r.bal(t, p) != 110 {
		t.Errorf("after recovering: Fan %d (want 120), Pal %d (want 110)", r.bal(t, f), r.bal(t, p))
	}
	list, _ := r.s.List(ctx, "dice", 10)
	for _, rd := range list {
		if rd.State == "open" || rd.State == "settling" {
			t.Errorf("%s is %s", rd.Title, rd.State)
		}
	}
}
