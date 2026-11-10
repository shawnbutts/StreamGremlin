package points

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newLedger(t *testing.T) (*Ledger, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return New(db, nil), db
}

func bal(t *testing.T, l *Ledger, v string) int64 {
	t.Helper()
	b, err := l.Balance(context.Background(), v, "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Credits, debits and adjustments: the same operation id again does
// nothing more; the same id for something else is refused; nobody goes
// below zero, and a refused debit changes nothing.
func TestTheLedger(t *testing.T) {
	l, _ := newLedger(t)
	ctx := context.Background()
	res, err := l.Credit(ctx, Op{ID: "c1", Viewer: "v_ann", Amount: 100, Memo: "chatting"})
	if err != nil || !res.Applied || res.Balance != 100 || res.Transaction.Kind != Credit || res.Transaction.Currency != "points" {
		t.Fatal(res, err)
	}
	again, err := l.Credit(ctx, Op{ID: "c1", Viewer: "v_ann", Amount: 100})
	if err != nil || again.Applied || again.Transaction.ID != res.Transaction.ID || bal(t, l, "v_ann") != 100 {
		t.Fatal("a repeat paid twice:", again, err)
	}
	if _, err := l.Credit(ctx, Op{ID: "c1", Viewer: "v_ann", Amount: 5}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var short *InsufficientError
	if _, err := l.Debit(ctx, Op{ID: "d1", Viewer: "v_ann", Amount: 150}); !errors.As(err, &short) || short.Has != 100 || short.Needs != 150 ||
		short.Error() != "not enough points: has 100, needs 150" {
		t.Fatal(err)
	}
	if bal(t, l, "v_ann") != 100 {
		t.Fatal("a refused debit changed the balance")
	}
	if res, err := l.Debit(ctx, Op{ID: "d2", Viewer: "v_ann", Amount: 40}); err != nil || res.Balance != 60 {
		t.Fatal(res, err)
	}
	if res, err := l.Adjust(ctx, Op{ID: "a1", Viewer: "v_ann", Amount: -60, Memo: "oops"}); err != nil || res.Balance != 0 {
		t.Fatal(res, err)
	}
	if _, err := l.Adjust(ctx, Op{ID: "a2", Viewer: "v_ann", Amount: -1}); !errors.Is(err, ErrInsufficient) {
		t.Fatal(err)
	}
	for _, op := range []Op{{ID: "", Viewer: "v", Amount: 1}, {ID: "x", Viewer: "", Amount: 1}, {ID: "x", Viewer: "system:issuer", Amount: 1},
		{ID: "x", Viewer: "v", Amount: MaxAmount + 1}, {ID: "x", Viewer: "v", Amount: 1, Memo: strings.Repeat("m", MaxMemo+1)},
		{ID: "x", Viewer: "v", Amount: 1, Currency: "gold"}} {
		if _, err := l.Credit(ctx, op); err == nil {
			t.Error("allowed:", op)
		}
	}
	if _, err := l.Credit(ctx, Op{ID: "x", Viewer: "v", Amount: 0}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if bad, err := l.Audit(ctx); err != nil || len(bad) != 0 {
		t.Fatal(bad, err)
	}
}

// Undo reverses a transaction once; undoing a gift can't take a viewer
// below zero; an undo can't itself be undone.
func TestUndo(t *testing.T) {
	l, _ := newLedger(t)
	ctx := context.Background()
	gift, _ := l.Credit(ctx, Op{ID: "c1", Viewer: "v_ann", Amount: 50})
	spend, _ := l.Debit(ctx, Op{ID: "d1", Viewer: "v_ann", Amount: 30})
	res, err := l.Undo(ctx, spend.Transaction.ID, Op{ID: "u1", By: "the app"})
	if err != nil || res.Balance != 50 || res.Transaction.Reverses != spend.Transaction.ID || res.Transaction.Amount != 30 {
		t.Fatal(res, err)
	}
	if again, err := l.Undo(ctx, spend.Transaction.ID, Op{ID: "u1"}); err != nil || again.Applied {
		t.Fatal("a repeated undo:", again, err)
	}
	if _, err := l.Undo(ctx, spend.Transaction.ID, Op{ID: "u2"}); !errors.Is(err, ErrReversed) {
		t.Fatal("undone twice:", err)
	}
	if _, err := l.Undo(ctx, res.Transaction.ID, Op{ID: "u3"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("an undo undone:", err)
	}
	_, _ = l.Debit(ctx, Op{ID: "d2", Viewer: "v_ann", Amount: 40})
	if _, err := l.Undo(ctx, gift.Transaction.ID, Op{ID: "u4"}); !errors.Is(err, ErrInsufficient) {
		t.Fatal("undoing a gift went below zero:", err)
	}
	tx, _ := l.Transaction(ctx, spend.Transaction.ID)
	if tx.ReversedBy != res.Transaction.ID {
		t.Fatal(tx)
	}
	if hist, _ := l.History(ctx, "v_ann", 10); len(hist) != 4 || hist[0].Kind != Debit {
		t.Fatal(hist)
	}
}

// The record can't be changed or deleted, a balance can't be set below
// zero by hand, and a balance changed by hand is caught by the audit.
func TestTheRecordHolds(t *testing.T) {
	l, db := newLedger(t)
	ctx := context.Background()
	_, _ = l.Credit(ctx, Op{ID: "c1", Viewer: "v_ann", Amount: 10})
	for _, q := range []string{"UPDATE ledger_transactions SET amount = 99", "DELETE FROM ledger_transactions", "UPDATE ledger_entries SET amount = 1",
		"DELETE FROM ledger_entries", "UPDATE ledger_accounts SET balance = -5 WHERE owner = 'v_ann'"} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("allowed: %s", q)
		}
	}
	if _, err := db.ExecContext(ctx, "UPDATE ledger_accounts SET balance = 500 WHERE owner = 'v_ann'"); err != nil {
		t.Fatal(err)
	}
	if bad, _ := l.Audit(ctx); len(bad) != 1 || !strings.Contains(bad[0], "v_ann's points balance is 500, but its history adds up to 10") {
		t.Fatal(bad)
	}
}

// Many at once: 40 credits over 4 operation ids pay exactly 4 times.
func TestConcurrentRepeatsPayOnce(t *testing.T) {
	l, _ := newLedger(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = l.Credit(ctx, Op{ID: fmt.Sprintf("op%d", i%4), Viewer: "v_ann", Amount: 10})
		}(i)
	}
	wg.Wait()
	if b := bal(t, l, "v_ann"); b != 40 {
		t.Fatal(b)
	}
	if bad, _ := l.Audit(ctx); len(bad) != 0 {
		t.Fatal(bad)
	}
}

// A currency's name changes; its id and balances don't; the top list and
// summary count viewers only.
func TestRenameTopAndSummary(t *testing.T) {
	l, _ := newLedger(t)
	ctx := context.Background()
	_, _ = l.Credit(ctx, Op{ID: "1", Viewer: "v_ann", Amount: 30})
	_, _ = l.Credit(ctx, Op{ID: "2", Viewer: "v_bob", Amount: 70})
	c, err := l.Rename(ctx, "", "Gold")
	if err != nil || c.ID != "points" || c.Name != "Gold" {
		t.Fatal(c, err)
	}
	if _, err := l.Rename(ctx, "", " "); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var short *InsufficientError
	if _, err := l.Debit(ctx, Op{ID: "3", Viewer: "v_ann", Amount: 31}); !errors.As(err, &short) || !strings.Contains(err.Error(), "not enough Gold") {
		t.Fatal(err)
	}
	top, _ := l.Top(ctx, "", 10)
	if len(top) != 2 || top[0].Viewer != "v_bob" {
		t.Fatal(top)
	}
	if s, _ := l.Summarize(ctx, ""); s.Transactions != 2 || s.Viewers != 2 || s.Issued != 100 {
		t.Fatal(s)
	}
}
