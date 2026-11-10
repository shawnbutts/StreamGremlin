package wheels

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type fakeDisplay struct {
	mu    sync.Mutex
	pages int
	spins []SpinView
}

func (f *fakeDisplay) WheelSpin(v SpinView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spins = append(f.spins, v)
}
func (f *fakeDisplay) WheelPages() int { f.mu.Lock(); defer f.mu.Unlock(); return f.pages }
func (f *fakeDisplay) shown() []SpinView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SpinView(nil), f.spins...)
}

type rig struct {
	t       *testing.T
	db      *storage.DB
	inbox   *events.Inbox
	display *fakeDisplay
	svc     *Service
	slept   chan time.Duration
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, inbox: events.NewInbox(db, nil), display: &fakeDisplay{}, slept: make(chan time.Duration, 10)}
	r.start()
	return r
}

// start starts (or restarts) the service. Waiting for a wheel to stop is
// reported on slept and takes no time.
func (r *rig) start() {
	r.t.Helper()
	if r.svc != nil {
		r.svc.Close()
	}
	r.svc = New(Options{DB: r.db, Inbox: r.inbox, Display: r.display, Sleep: func(_ context.Context, d time.Duration) { r.slept <- d }})
	if err := r.svc.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(r.svc.Close)
}

var games = Settings{Title: "Next game", Choices: []Choice{{Label: "Mario", Weight: 2}, {Label: "Zelda"}, {Label: "Tetris", Color: "#FF8800"}}}

func (r *rig) wheel() Wheel {
	r.t.Helper()
	w, err := r.svc.Create(context.Background(), "next-game", games)
	if err != nil {
		r.t.Fatal(err)
	}
	return w
}

func (r *rig) types() []string {
	recs, _ := r.inbox.Latest(context.Background(), 50, events.Filter{"wheels.*"})
	var out []string
	for _, x := range recs {
		out = append(out, x.Type)
	}
	return out
}

// A wheel's choices are checked, weights default to 1, and the odds are
// exact and said for chat.
func TestWheelsAndOdds(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	w := r.wheel()
	if w.Version != 1 || w.SpinSeconds != DefaultSpin || w.Choices[1].Weight != 1 || w.Choices[2].Color != "#ff8800" {
		t.Fatal(w)
	}
	if got := OddsWords(w); got != "Next game: Mario 50%, Zelda 25%, Tetris 25%" {
		t.Fatal(got)
	}
	if _, err := r.svc.Create(ctx, "next-game", games); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	for _, st := range []Settings{
		{Title: "", Choices: games.Choices},
		{Title: "x", Choices: games.Choices[:1]},
		{Title: "x", Choices: []Choice{{Label: "a", Weight: MaxWeight + 1}, {Label: "b"}}},
		{Title: "x", Choices: []Choice{{Label: " "}, {Label: "b"}}},
		{Title: "x", Choices: []Choice{{Label: "a", Color: "red"}, {Label: "b"}}},
		{Title: "x", Choices: games.Choices, SpinSeconds: 21},
	} {
		if _, err := r.svc.Create(ctx, "other", st); !errors.Is(err, ErrInvalid) {
			t.Error(st, err)
		}
	}
	if _, err := r.svc.Create(ctx, "Bad Name", games); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	same, _ := r.svc.Update(ctx, "next-game", games)
	if same.Version != 1 {
		t.Fatal("an unchanged save made a new version")
	}
	changed := games
	changed.Choices = append(append([]Choice(nil), games.Choices...), Choice{Label: "Doom"})
	w2, err := r.svc.Update(ctx, "next-game", changed)
	if err != nil || w2.Version != 2 || len(w2.Choices) != 4 {
		t.Fatal(w2, err)
	}
}

// Each choice wins on exactly its weight's worth of numbers.
func TestEveryNumberLandsWhereItShould(t *testing.T) {
	cs := []Choice{{Label: "a", Weight: 3}, {Label: "b", Weight: 1}, {Label: "c", Weight: 2}}
	counts := make([]int, len(cs))
	for d := 0; d < Total(cs); d++ {
		counts[Winner(cs, d)]++
	}
	if counts[0] != 3 || counts[1] != 1 || counts[2] != 2 || Winner(cs, 6) != -1 || Winner(cs, -1) != -1 {
		t.Fatal(counts)
	}
}

// A spin: drawn and recorded at once; shown on a wheel overlay that's
// open; settled when the wheel stops, and only then announced; repeating
// the request returns the same spin; the record checks, and a changed one
// doesn't; and it can't be changed or deleted.
func TestASpin(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.wheel()
	r.display.pages = 1
	sp, repeat, err := r.svc.Spin(ctx, SpinRequest{Wheel: "next-game", Key: "k1", By: "the app"})
	if err != nil || repeat || sp.Total != 4 || sp.Draw < 0 || sp.Draw >= 4 || sp.Label != sp.Choices[sp.Winner].Label {
		t.Fatal(sp, err)
	}
	if d := <-r.slept; d != time.Duration(DefaultSpin)*time.Second+500*time.Millisecond {
		t.Fatal("waited", d)
	}
	done, err := r.svc.Wait(ctx, sp.ID)
	if err != nil || done.SettledAt == nil || done.Display != "shown" {
		t.Fatal(done, err)
	}
	shown := r.display.shown()
	if len(shown) != 1 || shown[0].Winner != sp.Winner || shown[0].Draw != sp.Draw || len(shown[0].Choices) != 3 {
		t.Fatal(shown)
	}
	if types := strings.Join(r.types(), ","); types != "wheels.spin.started,wheels.spin.settled" {
		t.Fatal(types)
	}
	recs, _ := r.inbox.Latest(ctx, 1, events.Filter{"wheels.spin.started"})
	if strings.Contains(string(recs[0].Payload), sp.Label) {
		t.Fatal("the started event gave the result away:", string(recs[0].Payload))
	}
	again, repeat, err := r.svc.Spin(ctx, SpinRequest{Wheel: "next-game", Key: "k1"})
	if err != nil || !repeat || again.ID != sp.ID {
		t.Fatal(again, err)
	}
	if _, _, err := r.svc.Spin(ctx, SpinRequest{Wheel: "other", Key: "k1"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if bad := Verify(done); len(bad) != 0 {
		t.Fatal(bad)
	}
	wrong := done
	wrong.Draw = (done.Draw + 3) % 4
	if Winner(done.Choices, wrong.Draw) != done.Winner && len(Verify(wrong)) == 0 {
		t.Fatal("a changed draw passed")
	}
	for _, q := range []string{"UPDATE wheel_spins SET draw = 0", "UPDATE wheel_spins SET label = 'x'", "UPDATE wheel_spins SET display = 'no_audience'",
		"DELETE FROM wheel_spins"} {
		if _, err := r.db.ExecContext(ctx, q); err == nil {
			t.Errorf("allowed: %s", q)
		}
	}
	// Deleting the wheel keeps its spins.
	if ok, _ := r.svc.Delete(ctx, "next-game"); !ok {
		t.Fatal("not deleted")
	}
	if list, _ := r.svc.Spins(ctx, "next-game", 10); len(list) != 1 {
		t.Fatal(list)
	}
}

// With no wheel overlay open, a spin settles at once (the app's preview
// still sees it). A stop before settling is recovered on restart: the
// result stands.
func TestNoAudienceAndRecovery(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.wheel()
	sp, _, _ := r.svc.Spin(ctx, SpinRequest{Wheel: "next-game"})
	done, _ := r.svc.Wait(ctx, sp.ID)
	if done.Display != "no_audience" || len(r.display.shown()) != 1 {
		t.Fatal(done)
	}
	// A spin left unsettled, as a crash would leave it.
	if _, err := r.db.ExecContext(ctx, `INSERT INTO wheel_spins (id, op_key, wheel, title, wheel_version, choices, total, draw, winner,
		label, started_by, spun_at, seconds) VALUES ('s2', 'k2', 'next-game', 'Next game', 1, '[{"label":"a","weight":1},{"label":"b","weight":1}]',
		2, 1, 1, 'b', 'the app', '2026-11-10T12:00:00.000Z', 6)`); err != nil {
		t.Fatal(err)
	}
	r.start()
	got, _ := r.svc.GetSpin(ctx, "s2")
	if got.Display != "recovered" || got.SettledAt == nil {
		t.Fatal(got)
	}
}

// Spins draw uniformly by weight with crypto/rand: 3:1 lands about 3:1.
func TestSpinsFollowTheWeights(t *testing.T) {
	cs := []Choice{{Label: "a", Weight: 3}, {Label: "b", Weight: 1}}
	wins := [2]int{}
	for i := 0; i < 8000; i++ {
		d, err := draw(Total(cs))
		if err != nil {
			t.Fatal(err)
		}
		wins[Winner(cs, d)]++
	}
	if wins[0] < 5700 || wins[0] > 6300 {
		t.Fatal(wins)
	}
}
