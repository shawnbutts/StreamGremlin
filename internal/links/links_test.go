package links

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
	vs     *viewers.Service
	ledger *points.Ledger
	now    time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	vs := viewers.New(db, events.NewInbox(db, nil), nil)
	ledger := points.New(db, nil)
	r := &rig{vs: vs, ledger: ledger, now: time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)}
	r.s = New(db, vs, ledger, nil)
	r.s.now = func() time.Time { return r.now }
	return r
}

var (
	twAnn  = events.Actor{Platform: Twitch, ID: "100", Login: "ann", DisplayName: "Ann"}
	ytAnn  = events.Actor{Platform: YouTube, ID: "UCann", Login: "ann.yt", DisplayName: "@Ann.YT"}
	twBob  = events.Actor{Platform: Twitch, ID: "200", Login: "bob", DisplayName: "Bob"}
	ytNoHa = events.Actor{Platform: YouTube, ID: "UCplain", DisplayName: "Plain Name"}
)

func (r *rig) give(t *testing.T, a events.Actor, n int64, op string) viewers.Viewer {
	t.Helper()
	v, err := r.vs.Ensure(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ledger.Credit(context.Background(), points.Op{ID: op, Viewer: v.ID, Amount: n}); err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *rig) balance(t *testing.T, v viewers.Viewer) int64 {
	t.Helper()
	b, err := r.ledger.Balance(context.Background(), v.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// !link in chat: the code works once, only for the account named, on its
// platform; the balances join; points go to one balance from then on;
// unlinking leaves them with Twitch; the ledger still adds up.
func TestLinkInChatSharesPoints(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tw := r.give(t, twAnn, 30, "a")
	yt := r.give(t, ytAnn, 12, "b")
	asked, err := r.s.Ask(ctx, ytAnn, "@Ann")
	if err != nil || asked.Platform != Twitch || asked.Login != "ann" || !IsCode(asked.Code) {
		t.Fatalf("%+v %v", asked, err)
	}
	// Someone else typing it, or typing it on YouTube, does nothing.
	if _, err := r.s.Confirm(ctx, twBob, asked.Code); !errors.Is(err, ErrCode) {
		t.Fatal(err)
	}
	if _, err := r.s.Confirm(ctx, ytAnn, asked.Code); !errors.Is(err, ErrCode) || !strings.Contains(err.Error(), "Twitch") {
		t.Fatal(err)
	}
	l, err := r.s.Confirm(ctx, twAnn, asked.Code)
	if err != nil || l.Twitch.ID != tw.ID || l.YouTube.ID != yt.ID || l.LinkedBy != "chat" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := r.s.Confirm(ctx, twAnn, asked.Code); !errors.Is(err, ErrCode) {
		t.Fatal("a code worked twice")
	}
	if a, b := r.balance(t, tw), r.balance(t, yt); a != 42 || b != 42 {
		t.Fatalf("balances %d %d, want one shared 42", a, b)
	}
	// Points to either account go to the shared balance; spending too.
	if _, err := r.ledger.Credit(ctx, points.Op{ID: "c", Viewer: yt.ID, Amount: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ledger.Debit(ctx, points.Op{ID: "d", Viewer: tw.ID, Amount: 45}); err != nil {
		t.Fatal(err)
	}
	if b := r.balance(t, yt); b != 5 {
		t.Fatalf("shared balance %d", b)
	}
	if _, err := r.ledger.Debit(ctx, points.Op{ID: "e", Viewer: yt.ID, Amount: 6}); !errors.Is(err, points.ErrInsufficient) {
		t.Fatal("spent more than the shared balance:", err)
	}
	hist, _ := r.ledger.History(ctx, yt.ID, 100)
	if len(hist) != 6 { // two earned, the move out and in, one earned, one spent
		t.Fatalf("history %d: %+v", len(hist), hist)
	}
	if o, _ := r.ledger.Owner(ctx, yt.ID); o != tw.ID {
		t.Fatal("owner", o)
	}
	// Already linked: asked again, it says so.
	if _, err := r.s.Ask(ctx, twAnn, "@ann.yt"); !errors.Is(err, ErrLinked) || !strings.Contains(err.Error(), "@Ann.YT") {
		t.Fatal(err)
	}
	// Unlinked: Twitch keeps the shared points; YouTube starts from 0.
	if _, err := r.s.Unlink(ctx, yt.ID, "the app"); err != nil {
		t.Fatal(err)
	}
	if a, b := r.balance(t, tw), r.balance(t, yt); a != 5 || b != 0 {
		t.Fatalf("after unlinking %d %d", a, b)
	}
	if _, err := r.s.Unlink(ctx, yt.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if bad, err := r.ledger.Audit(ctx); err != nil || len(bad) != 0 {
		t.Fatalf("%v %v", bad, err)
	}
	// Linked again later: a new link, with nothing lost.
	if _, err := r.s.Link(ctx, yt.ID, tw.ID, "the app"); err != nil {
		t.Fatal(err)
	}
}

func TestCodesRunOutAndWrongTriesAreLimited(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	asked, err := r.s.Ask(ctx, twAnn, "@ann.yt")
	if err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(CodeTTL)
	if _, err := r.s.Confirm(ctx, ytAnn, asked.Code); !errors.Is(err, ErrCode) {
		t.Fatal("a code worked after it ran out")
	}
	asked, _ = r.s.Ask(ctx, twAnn, "@ann.yt")
	for range MaxTries {
		_, _ = r.s.Confirm(ctx, ytAnn, "000000"+"")
	}
	if _, err := r.s.Confirm(ctx, ytAnn, asked.Code); !errors.Is(err, ErrCode) || !strings.Contains(err.Error(), "too many") {
		t.Fatal("tries weren't limited:", err)
	}
	r.now = r.now.Add(CodeTTL)
	asked, _ = r.s.Ask(ctx, twAnn, "@ann.yt")
	if _, err := r.s.Confirm(ctx, ytAnn, asked.Code); err != nil {
		t.Fatal(err)
	}
	// A new ask replaces the old code.
	first, _ := r.s.Ask(ctx, twBob, "@someone")
	second, _ := r.s.Ask(ctx, twBob, "@someone")
	if first.Code != second.Code && r.s.pending[first.Code] != nil {
		t.Fatal("the old code still works")
	}
}

func TestLinkRefusals(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tw := r.give(t, twAnn, 1, "a")
	bob := r.give(t, twBob, 1, "b")
	yt := r.give(t, ytAnn, 1, "c")
	if _, err := r.s.Link(ctx, tw.ID, bob.ID, "the app"); !errors.Is(err, ErrInvalid) {
		t.Fatal("two Twitch viewers linked:", err)
	}
	if _, err := r.s.Ask(ctx, twAnn, "not a name!"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.s.Confirm(ctx, ytNoHa, "123456"); !errors.Is(err, ErrCode) || !strings.Contains(err.Error(), "handle") {
		t.Fatal(err)
	}
	if _, err := r.s.Link(ctx, tw.ID, yt.ID, "the app"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Link(ctx, bob.ID, yt.ID, "the app"); !errors.Is(err, ErrLinked) {
		t.Fatal("linked twice:", err)
	}
	if p, ok, err := r.s.Partner(ctx, YouTube, "UCann"); err != nil || !ok || p.ID != "100" {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	if _, ok, _ := r.s.Partner(ctx, Twitch, "200"); ok {
		t.Fatal("bob has a partner")
	}
}
