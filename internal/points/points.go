// Package points keeps the points ledger (docs/adr/0032-viewers-and-points.md):
// append-only and double-entry. Every transaction moves points between a
// viewer's account and a system account (the issuer points come from, the
// sink spent points go to), and its entries sum to zero. Balances are kept
// by the database in the same transaction, never below zero unless the
// currency allows it. Each operation has an id: repeating it does nothing
// more, so a retried step never pays twice. A mistake is undone by a
// reversal, once.
package points

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Limits.
const (
	MaxAmount = 1_000_000_000 // in one operation
	MaxMemo   = 200
	MaxName   = 40
	// Default is the currency every empty currency setting means.
	Default = "points"
	issuer  = "system:issuer"
	sink    = "system:sink"
)

// Kinds of transaction.
const (
	Credit  = "credit"  // earned: issuer → viewer
	Debit   = "debit"   // spent: viewer → sink
	Adjust  = "adjust"  // by the streamer, either way
	Reverse = "reverse" // undoes another, once
)

// Errors.
var (
	ErrInvalid = errors.New("points: invalid")
	// ErrInsufficient: the viewer hasn't enough. Nothing changed.
	ErrInsufficient = errors.New("points: not enough")
	// ErrConflict: an operation id was used for something else.
	ErrConflict = errors.New("points: that operation id was used for something else")
	ErrNotFound = errors.New("points: not found")
	// ErrReversed: the transaction was already undone.
	ErrReversed = errors.New("points: already undone")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// InsufficientError says how many a viewer has and needed.
type InsufficientError struct {
	Has, Needs int64
	Name       string // the currency's name
}

func (e *InsufficientError) Error() string {
	return fmt.Sprintf("not enough %s: has %d, needs %d", e.Name, e.Has, e.Needs)
}

func (e *InsufficientError) Unwrap() error { return ErrInsufficient }

// Currency is a kind of points.
type Currency struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	AllowNegative bool      `json:"allow_negative"`
	CreatedAt     time.Time `json:"created_at"`
}

// Transaction is one move of points.
type Transaction struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Currency   string    `json:"currency"`
	Viewer     string    `json:"viewer"`
	Amount     int64     `json:"amount"` // to the viewer: + earned, - spent
	Memo       string    `json:"memo,omitempty"`
	By         string    `json:"by"`
	Reverses   string    `json:"reverses,omitempty"`
	ReversedBy string    `json:"reversed_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Op is an operation on a viewer's points.
type Op struct {
	// ID makes it safe to repeat: the same id does nothing more.
	ID       string
	Currency string // "" for points
	Viewer   string // the viewer's id
	Amount   int64  // credit and debit: above 0; adjust: either way, not 0
	Memo     string
	By       string // who: "the app", "workflow chat.points", …
}

// Result is how an operation went.
type Result struct {
	Transaction Transaction `json:"transaction"`
	Balance     int64       `json:"balance"`
	// Applied is false when the operation id was seen before: nothing more
	// happened.
	Applied bool `json:"applied"`
}

// Ledger keeps the points.
type Ledger struct {
	db  *storage.DB
	log *slog.Logger
	now func() time.Time
}

// New returns a Ledger.
func New(db *storage.DB, log *slog.Logger) *Ledger {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Ledger{db: db, log: log.With("component", "points"), now: time.Now}
}

func ts(t time.Time) string { return storage.Timestamp(t) }

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "tx_" + hex.EncodeToString(b)
}

// Currency returns a currency ("" for points).
func (l *Ledger) Currency(ctx context.Context, id string) (Currency, error) {
	if id == "" {
		id = Default
	}
	var c Currency
	var created string
	err := l.db.QueryRowContext(ctx, "SELECT id, name, allow_negative, created_at FROM currencies WHERE id = ?", id).
		Scan(&c.ID, &c.Name, &c.AllowNegative, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("%w: no currency %q", ErrNotFound, id)
	}
	c.CreatedAt, _ = storage.ParseTimestamp(created)
	return c, err
}

// Rename changes a currency's name. Its id, and everything that refers to
// it, stays the same.
func (l *Ledger) Rename(ctx context.Context, id, name string) (Currency, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return Currency{}, invalid("a currency's name is 1 to %d characters", MaxName)
	}
	c, err := l.Currency(ctx, id)
	if err != nil {
		return c, err
	}
	if _, err := l.db.ExecContext(ctx, "UPDATE currencies SET name = ? WHERE id = ?", name, c.ID); err != nil {
		return c, err
	}
	return l.Currency(ctx, c.ID)
}

// account returns the id of an account, made if new.
func account(ctx context.Context, tx *sql.Tx, currency, owner string) (int64, error) {
	if _, err := tx.ExecContext(ctx, "INSERT INTO ledger_accounts (currency, owner) VALUES (?, ?) ON CONFLICT DO NOTHING", currency, owner); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM ledger_accounts WHERE currency = ? AND owner = ?", currency, owner).Scan(&id)
	return id, err
}

// Credit gives a viewer points they earned.
func (l *Ledger) Credit(ctx context.Context, op Op) (Result, error) {
	if op.Amount <= 0 {
		return Result{}, invalid("an amount given is above 0")
	}
	return l.apply(ctx, Credit, op, "")
}

// Debit takes points a viewer spends. Not enough is ErrInsufficient, with
// nothing changed.
func (l *Ledger) Debit(ctx context.Context, op Op) (Result, error) {
	if op.Amount <= 0 {
		return Result{}, invalid("an amount taken is above 0")
	}
	op.Amount = -op.Amount
	return l.apply(ctx, Debit, op, "")
}

// Adjust gives (above 0) or takes (below 0) points by the streamer's hand.
func (l *Ledger) Adjust(ctx context.Context, op Op) (Result, error) {
	if op.Amount == 0 {
		return Result{}, invalid("an adjustment isn't 0")
	}
	return l.apply(ctx, Adjust, op, "")
}

// Undo reverses a transaction, once: the viewer gets back what was taken,
// or loses what was given (not below zero).
func (l *Ledger) Undo(ctx context.Context, txID string, op Op) (Result, error) {
	orig, err := l.Transaction(ctx, txID)
	if err != nil {
		return Result{}, err
	}
	if orig.Kind == Reverse {
		return Result{}, invalid("an undo can't be undone; make an adjustment instead")
	}
	op.Currency, op.Viewer, op.Amount = orig.Currency, orig.Viewer, -orig.Amount
	if op.Memo == "" {
		op.Memo = "undoes " + orig.Kind + ": " + orig.Memo
	}
	res, err := l.apply(ctx, Reverse, op, orig.ID)
	if err != nil && res.Transaction.ID == "" {
		var reversed string
		if l.db.QueryRowContext(ctx, "SELECT id FROM ledger_transactions WHERE reverses = ?", orig.ID).Scan(&reversed) == nil {
			return Result{}, fmt.Errorf("%w (by %s)", ErrReversed, reversed)
		}
	}
	return res, err
}

// apply records one transaction: the viewer's entry and the system's
// opposite one, in one database transaction.
func (l *Ledger) apply(ctx context.Context, kind string, op Op, reverses string) (Result, error) {
	op.Memo = strings.TrimSpace(op.Memo)
	switch {
	case op.ID == "" || len(op.ID) > 200:
		return Result{}, invalid("an operation id is 1 to 200 characters")
	case op.Viewer == "" || strings.HasPrefix(op.Viewer, "system:"):
		return Result{}, invalid("points go to a viewer")
	case op.Amount > MaxAmount || op.Amount < -MaxAmount:
		return Result{}, invalid("up to %d in one go", MaxAmount)
	case utf8.RuneCountInString(op.Memo) > MaxMemo:
		return Result{}, invalid("a reason is up to %d characters", MaxMemo)
	}
	if op.By == "" {
		op.By = "the app"
	}
	cur, err := l.Currency(ctx, op.Currency)
	if err != nil {
		return Result{}, err
	}
	op.Currency = cur.ID
	// A repeat: the same operation again does nothing more.
	if prev, err := l.byOp(ctx, op.ID); err == nil {
		if prev.Kind != kind || prev.Currency != op.Currency || prev.Viewer != op.Viewer || prev.Amount != op.Amount || prev.Reverses != reverses {
			return Result{}, ErrConflict
		}
		bal, _ := l.Balance(ctx, op.Viewer, op.Currency)
		return Result{Transaction: prev, Balance: bal}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	system := issuer
	if op.Amount < 0 {
		system = sink
	}
	viewerAcct, err := account(ctx, tx, op.Currency, op.Viewer)
	if err != nil {
		return Result{}, err
	}
	systemAcct, err := account(ctx, tx, op.Currency, system)
	if err != nil {
		return Result{}, err
	}
	t := Transaction{ID: newID(), Kind: kind, Currency: op.Currency, Viewer: op.Viewer, Amount: op.Amount, Memo: op.Memo, By: op.By,
		Reverses: reverses, CreatedAt: l.now()}
	var rev any
	if reverses != "" {
		rev = reverses
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions (id, op_id, kind, currency, viewer, amount, memo, by, reverses, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, op.ID, kind, t.Currency, t.Viewer, t.Amount, t.Memo, t.By, rev, ts(t.CreatedAt)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			if strings.Contains(err.Error(), "reverses") {
				return Result{}, ErrReversed
			}
			_ = tx.Rollback()
			return l.apply(ctx, kind, op, reverses) // raced with the same op: answered as a repeat
		}
		return Result{}, err
	}
	for _, e := range []struct {
		acct   int64
		amount int64
	}{{viewerAcct, op.Amount}, {systemAcct, -op.Amount}} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO ledger_entries (tx, account, amount) VALUES (?, ?, ?)", t.ID, e.acct, e.amount); err != nil {
			if strings.Contains(err.Error(), "not enough points") {
				_ = tx.Rollback()
				has, _ := l.Balance(ctx, op.Viewer, op.Currency)
				return Result{}, &InsufficientError{Has: has, Needs: -op.Amount, Name: cur.Name}
			}
			return Result{}, err
		}
	}
	var bal int64
	if err := tx.QueryRowContext(ctx, "SELECT balance FROM ledger_accounts WHERE id = ?", viewerAcct).Scan(&bal); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	l.log.Info("points moved", "kind", kind, "viewer", op.Viewer, "amount", op.Amount, "currency", op.Currency, "by", op.By)
	return Result{Transaction: t, Balance: bal, Applied: true}, nil
}

const txColumns = `t.id, t.kind, t.currency, t.viewer, t.amount, t.memo, t.by, COALESCE(t.reverses, ''),
	COALESCE((SELECT r.id FROM ledger_transactions r WHERE r.reverses = t.id), ''), t.created_at`

type scanner interface{ Scan(...any) error }

func scanTx(sc scanner) (Transaction, error) {
	var t Transaction
	var at string
	err := sc.Scan(&t.ID, &t.Kind, &t.Currency, &t.Viewer, &t.Amount, &t.Memo, &t.By, &t.Reverses, &t.ReversedBy, &at)
	t.CreatedAt, _ = storage.ParseTimestamp(at)
	return t, err
}

func (l *Ledger) byOp(ctx context.Context, opID string) (Transaction, error) {
	t, err := scanTx(l.db.QueryRowContext(ctx, "SELECT "+txColumns+" FROM ledger_transactions t WHERE t.op_id = ?", opID))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Transaction returns one transaction.
func (l *Ledger) Transaction(ctx context.Context, id string) (Transaction, error) {
	t, err := scanTx(l.db.QueryRowContext(ctx, "SELECT "+txColumns+" FROM ledger_transactions t WHERE t.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("%w: no transaction %q", ErrNotFound, id)
	}
	return t, err
}

// Balance is a viewer's balance (0 when they have none).
func (l *Ledger) Balance(ctx context.Context, viewer, currency string) (int64, error) {
	if currency == "" {
		currency = Default
	}
	var b int64
	err := l.db.QueryRowContext(ctx, "SELECT COALESCE((SELECT balance FROM ledger_accounts WHERE currency = ? AND owner = ?), 0)",
		currency, viewer).Scan(&b)
	return b, err
}

// History lists a viewer's transactions, newest first.
func (l *Ledger) History(ctx context.Context, viewer string, limit int) ([]Transaction, error) {
	rows, err := l.db.QueryContext(ctx, "SELECT "+txColumns+" FROM ledger_transactions t WHERE t.viewer = ? ORDER BY t.created_at DESC, t.rowid DESC LIMIT ?",
		viewer, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Transaction{}
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Holding is a viewer's balance in a top list.
type Holding struct {
	Viewer  string `json:"viewer"`
	Balance int64  `json:"balance"`
}

// Top lists the highest balances in a currency.
func (l *Ledger) Top(ctx context.Context, currency string, limit int) ([]Holding, error) {
	if currency == "" {
		currency = Default
	}
	rows, err := l.db.QueryContext(ctx, `SELECT owner, balance FROM ledger_accounts WHERE currency = ? AND owner NOT LIKE 'system:%' AND balance != 0
		ORDER BY balance DESC, owner LIMIT ?`, currency, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Holding{}
	for rows.Next() {
		var h Holding
		if err := rows.Scan(&h.Viewer, &h.Balance); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Audit checks the ledger: every transaction's entries sum to zero and
// every balance equals the sum of its entries. It returns what doesn't
// hold, or nothing.
func (l *Ledger) Audit(ctx context.Context) ([]string, error) {
	var bad []string
	rows, err := l.db.QueryContext(ctx, `SELECT tx, SUM(amount) FROM ledger_entries GROUP BY tx HAVING SUM(amount) != 0 LIMIT 20`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var sum int64
		_ = rows.Scan(&id, &sum)
		bad = append(bad, fmt.Sprintf("transaction %s doesn't balance: it's off by %d", id, sum))
	}
	_ = rows.Close()
	rows, err = l.db.QueryContext(ctx, `SELECT a.currency, a.owner, a.balance, COALESCE((SELECT SUM(e.amount) FROM ledger_entries e WHERE e.account = a.id), 0) AS s
		FROM ledger_accounts a WHERE a.balance != s LIMIT 20`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cur, owner string
		var bal, sum int64
		_ = rows.Scan(&cur, &owner, &bal, &sum)
		bad = append(bad, fmt.Sprintf("%s's %s balance is %d, but its history adds up to %d", owner, cur, bal, sum))
	}
	_ = rows.Close()
	var missing int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_transactions t WHERE (SELECT COUNT(*) FROM ledger_entries e WHERE e.tx = t.id) != 2`).
		Scan(&missing); err != nil {
		return nil, err
	}
	if missing > 0 {
		bad = append(bad, fmt.Sprintf("%d transactions don't have both their entries", missing))
	}
	return bad, nil
}

// Summary is the ledger in numbers.
type Summary struct {
	Transactions int   `json:"transactions"`
	Viewers      int   `json:"viewers"` // with a balance
	Issued       int64 `json:"issued"`  // in circulation: what viewers hold together
}

// Summarize counts the ledger for a currency.
func (l *Ledger) Summarize(ctx context.Context, currency string) (Summary, error) {
	if currency == "" {
		currency = Default
	}
	var s Summary
	err := l.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM ledger_transactions WHERE currency = ?1),
		(SELECT COUNT(*) FROM ledger_accounts WHERE currency = ?1 AND owner NOT LIKE 'system:%' AND balance != 0),
		(SELECT COALESCE(SUM(balance), 0) FROM ledger_accounts WHERE currency = ?1 AND owner NOT LIKE 'system:%')`, currency).
		Scan(&s.Transactions, &s.Viewers, &s.Issued)
	return s, err
}
