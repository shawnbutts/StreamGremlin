package gives

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

type fakeTwitch struct {
	chat, subs []events.Actor
	err        error
}

func (f *fakeTwitch) Chatters(context.Context) ([]events.Actor, error)    { return f.chat, f.err }
func (f *fakeTwitch) Subscribers(context.Context) ([]events.Actor, error) { return f.subs, f.err }

type fakeRecent struct {
	list  []events.Actor
	since time.Time
}

func (f *fakeRecent) ChattedSince(_ context.Context, since time.Time) ([]events.Actor, error) {
	f.since = since
	return f.list, nil
}

type rig struct {
	s      *Service
	tw     *fakeTwitch
	recent *fakeRecent
	ledger *points.Ledger
	vs     *viewers.Service
	db     *storage.DB
	now    time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{tw: &fakeTwitch{}, recent: &fakeRecent{}, ledger: points.New(db, nil), vs: viewers.New(db, events.NewInbox(db, nil), nil), db: db,
		now: time.Date(2026, 11, 10, 21, 0, 0, 0, time.UTC)}
	r.s = New(Options{DB: db, Viewers: r.vs, Points: r.ledger, Twitch: r.tw, Recent: r.recent, Now: func() time.Time { return r.now }})
	return r
}

func people(ids ...string) []events.Actor {
	out := []events.Actor{}
	for _, id := range ids {
		out = append(out, events.Actor{Platform: "twitch", ID: id, Login: "u" + id, DisplayName: "U" + id})
	}
	return out
}

func (r *rig) balance(t *testing.T, id string) int64 {
	t.Helper()
	v, err := r.vs.ByAccount(context.Background(), "twitch", id)
	if errors.Is(err, viewers.ErrNotFound) {
		return 0
	}
	b, _ := r.ledger.Balance(context.Background(), v.ID, "")
	return b
}

func TestWho(t *testing.T) {
	for _, w := range []Who{{Audience: "everyone"}, {Audience: Recent}, {Audience: Recent, Minutes: MaxMinutes + 1}, {Audience: Chat, Minutes: 5}} {
		if err := w.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", w, err)
		}
	}
	if got := (Who{Audience: Recent, Minutes: 15}).Words(); got != "everyone who chatted in the last 15 minutes" {
		t.Fatal(got)
	}
}

// A preview names who's in the group without giving anything, and says why
// it's empty when it is.
func TestPreview(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.chat = people("1", "2", "2", "3")
	g, err := r.s.Preview(ctx, Who{Audience: Chat})
	if err != nil || g.Viewers != 3 || strings.Join(g.Sample, ",") != "U1,U2,U3" || g.Note != "" {
		t.Fatalf("%+v %v", g, err)
	}
	if b := r.balance(t, "1"); b != 0 {
		t.Fatal("a preview gave:", b)
	}
	g, _ = r.s.Preview(ctx, Who{Audience: Subs})
	if g.Viewers != 0 || !strings.Contains(g.Note, "Twitch listed no subscribers") {
		t.Fatalf("%+v", g)
	}
	r.recent.list = people("4")
	g, _ = r.s.Preview(ctx, Who{Audience: Recent, Minutes: 15})
	if g.Viewers != 1 || !r.recent.since.Equal(r.now.Add(-15*time.Minute)) {
		t.Fatalf("%+v %v", g, r.recent.since)
	}
	r.tw.err = errors.New("needs moderator:read:chatters")
	if _, err := r.s.Preview(ctx, Who{Audience: Chat}); !errors.Is(err, ErrList) || !strings.Contains(err.Error(), "moderator:read:chatters") {
		t.Fatal(err)
	}
}

// A give pays everyone in the group once; the same request again pays
// nothing more and returns the same give; the same request id for another
// give is refused; the group is the one worked out the first time.
func TestGive(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.subs = people("1", "2", "3")
	req := Request{Who: Who{Audience: Subs}, Amount: 100, Reason: "sub party", RequestID: "r1"}
	g, err := r.s.Give(ctx, req)
	if err != nil || !g.Applied || g.Viewers != 3 || g.Total != 300 || g.PaidAt == nil || g.Words != "subscribers" {
		t.Fatalf("%+v %v", g, err)
	}
	r.tw.subs = people("1", "2", "3", "4") // someone subscribes after
	again, err := r.s.Give(ctx, req)
	if err != nil || again.Applied || again.ID != g.ID || again.Viewers != 3 {
		t.Fatalf("%+v %v", again, err)
	}
	for id, want := range map[string]int64{"1": 100, "2": 100, "3": 100, "4": 0} {
		if b := r.balance(t, id); b != want {
			t.Errorf("%s has %d, want %d", id, b, want)
		}
	}
	req.Amount = 200
	if _, err := r.s.Give(ctx, req); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	v, _ := r.vs.ByAccount(ctx, "twitch", "1")
	h, _ := r.ledger.History(ctx, v.ID, 5)
	if len(h) != 1 || h[0].Memo != "sub party" || h[0].Kind != points.Credit {
		t.Fatalf("%+v", h)
	}
	if list, _ := r.s.List(ctx, 10); len(list) != 1 || list[0].Total != 300 {
		t.Fatalf("%+v", list)
	}
	// No reason: it says what the give was.
	r.tw.chat = people("5")
	g2, _ := r.s.Give(ctx, Request{Who: Who{Audience: Chat}, Amount: 5, RequestID: "r2"})
	if g2.Memo != "a give to everyone in chat" {
		t.Fatal(g2.Memo)
	}
}

// Nothing is given when the list can't be read, or nobody's in the group,
// and no give is recorded.
func TestNothingGiven(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.err = errors.New("Twitch is down")
	if _, err := r.s.Give(ctx, Request{Who: Who{Audience: Chat}, Amount: 10, RequestID: "r1"}); !errors.Is(err, ErrList) {
		t.Fatal(err)
	}
	r.tw.err = nil
	if _, err := r.s.Give(ctx, Request{Who: Who{Audience: Chat}, Amount: 10, RequestID: "r1"}); !errors.Is(err, ErrEmpty) ||
		!strings.Contains(err.Error(), "updates it a little late") {
		t.Fatal(err)
	}
	if list, _ := r.s.List(ctx, 10); len(list) != 0 {
		t.Fatal(list)
	}
	for _, bad := range []Request{
		{Who: Who{Audience: Chat}, Amount: 0, RequestID: "x"},
		{Who: Who{Audience: Chat}, Amount: MaxEach + 1, RequestID: "x"},
		{Who: Who{Audience: Chat}, Amount: 1},
		{Who: Who{Audience: "all"}, Amount: 1, RequestID: "x"},
		{Who: Who{Audience: Chat}, Amount: 1, RequestID: "x", Reason: strings.Repeat("a", points.MaxMemo+1)},
	} {
		if _, err := r.s.Give(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

// Ten tries at once with one request id give once.
func TestGiveConcurrent(t *testing.T) {
	r := newRig(t)
	r.tw.chat = people("1", "2")
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.s.Give(context.Background(), Request{Who: Who{Audience: Chat}, Amount: 7, RequestID: "same"})
		}()
	}
	wg.Wait()
	if a, b := r.balance(t, "1"), r.balance(t, "2"); a != 7 || b != 7 {
		t.Fatal(a, b)
	}
}

// A give interrupted part way is finished by the same request, paying only
// those not yet paid.
func TestGiveFinishedByARetry(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.chat = people("1", "2", "3")
	g, err := r.s.Give(ctx, Request{Who: Who{Audience: Chat}, Amount: 10, RequestID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	// As if it stopped after paying the first: forget it was finished, and
	// add someone who'd been missed.
	v4, _ := r.vs.Ensure(ctx, people("4")[0])
	if _, err := r.db.Exec("UPDATE points_gives SET paid_at = NULL WHERE id = ?", g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec("INSERT INTO points_give_members (give, viewer) VALUES (?, ?)", g.ID, v4.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := r.s.Give(ctx, Request{Who: Who{Audience: Chat}, Amount: 10, RequestID: "r1"}); err != nil || again.PaidAt == nil {
		t.Fatal(again, err)
	}
	for _, id := range []string{"1", "2", "3", "4"} {
		if b := r.balance(t, id); b != 10 {
			t.Errorf("%s has %d", id, b)
		}
	}
}

// Undoing takes the give back from everyone, never below zero: whoever has
// spent some is named and keeps theirs, and undoing again later, once they
// have enough, finishes it. A finished undo is harmless to repeat, and a
// retry of the give after it pays nothing.
func TestUndo(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.chat = people("1", "2")
	req := Request{Who: Who{Audience: Chat}, Amount: 50, RequestID: "r1"}
	g, _ := r.s.Give(ctx, req)
	v2, _ := r.vs.ByAccount(ctx, "twitch", "2")
	if _, err := r.ledger.Debit(ctx, points.Op{ID: "spend", Viewer: v2.ID, Amount: 30}); err != nil {
		t.Fatal(err)
	}
	res, err := r.s.Undo(ctx, g.ID)
	if err != nil || res.Undone != 1 || fmt.Sprint(res.Short) != "[U2]" || res.Give.UndoneAt != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if a, b := r.balance(t, "1"), r.balance(t, "2"); a != 0 || b != 20 {
		t.Fatal(a, b)
	}
	if _, err := r.ledger.Credit(ctx, points.Op{ID: "earned", Viewer: v2.ID, Amount: 40}); err != nil {
		t.Fatal(err)
	}
	res, err = r.s.Undo(ctx, g.ID)
	if err != nil || res.Undone != 2 || len(res.Short) != 0 || res.Give.UndoneAt == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if again, err := r.s.Undo(ctx, g.ID); err != nil || again.Undone != 2 {
		t.Fatal(again, err)
	}
	if _, err := r.s.Give(ctx, req); err != nil || r.balance(t, "1") != 0 {
		t.Fatal("a retry after undo paid again:", err, r.balance(t, "1"))
	}
	if _, err := r.s.Undo(ctx, "g_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if problems, _ := r.ledger.Audit(ctx); len(problems) != 0 {
		t.Fatal(problems)
	}
}
