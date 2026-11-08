// Package quotes keeps numbered quotes from the stream
// (docs/adr/0014-quotes.md), as Streamer.bot's Quotes: add, show one at
// random or by number, edit and delete. Numbers are never reused.
package quotes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Limits.
const (
	MaxText     = 500
	MaxName     = 100
	MaxCategory = 140
	MaxQuotes   = 100000
)

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("quotes: invalid")
	ErrNotFound = errors.New("quotes: no such quote")
	ErrFull     = errors.New("quotes: too many quotes")
)

// Quote is one quote.
type Quote struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	AddedBy   string    `json:"added_by"`
	AddedByID string    `json:"added_by_id,omitempty"`
	Platform  string    `json:"platform,omitempty"`
	Category  string    `json:"category,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// New is a quote to add. Key, when set, makes repeating the add return the
// first quote instead of adding another.
type New struct {
	Text      string `json:"text"`
	AddedBy   string `json:"added_by"`
	AddedByID string `json:"-"`
	Platform  string `json:"-"`
	Category  string `json:"category"`
	Key       string `json:"request_id"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func clean(text, by, category string) (string, string, string, error) {
	text = strings.Join(strings.Fields(text), " ")
	by, category = strings.TrimSpace(by), strings.TrimSpace(category)
	switch {
	case text == "":
		return "", "", "", invalid("a quote needs some text")
	case utf8.RuneCountInString(text) > MaxText:
		return "", "", "", invalid("a quote is up to %d characters", MaxText)
	case utf8.RuneCountInString(by) > MaxName:
		return "", "", "", invalid("who added it is up to %d characters", MaxName)
	case utf8.RuneCountInString(category) > MaxCategory:
		return "", "", "", invalid("the category is up to %d characters", MaxCategory)
	}
	return text, by, category, nil
}

// ParseNumber reads a quote number as chat types it: 5 or #5.
func ParseNumber(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(s), "#"), 10, 64)
	return n, err == nil && n > 0
}

// Store keeps quotes in the database.
type Store struct{ db *storage.DB }

// NewStore returns a Store.
func NewStore(db *storage.DB) *Store { return &Store{db: db} }

const columns = "id, text, added_by, added_by_id, platform, category, created_at, updated_at"

type scanner interface{ Scan(...any) error }

func scan(s scanner) (Quote, error) {
	var q Quote
	var created, updated string
	if err := s.Scan(&q.ID, &q.Text, &q.AddedBy, &q.AddedByID, &q.Platform, &q.Category, &created, &updated); err != nil {
		return Quote{}, err
	}
	q.CreatedAt, _ = storage.ParseTimestamp(created)
	q.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return q, nil
}

func (s *Store) one(ctx context.Context, q *sql.Row) (Quote, error) {
	out, err := scan(q)
	if errors.Is(err, sql.ErrNoRows) {
		return Quote{}, ErrNotFound
	}
	if err != nil {
		return Quote{}, fmt.Errorf("quotes: read: %w", err)
	}
	return out, nil
}

// Add saves a new quote. created reports false when the key was used
// before, and the first quote is returned.
func (s *Store) Add(ctx context.Context, n New, now time.Time) (q Quote, created bool, err error) {
	text, by, category, err := clean(n.Text, n.AddedBy, n.Category)
	if err != nil {
		return Quote{}, false, err
	}
	if len(n.Key) > 200 {
		return Quote{}, false, invalid("request_id is up to 200 characters")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Quote{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if n.Key != "" {
		prev, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM quotes WHERE request_key = ?", n.Key))
		if err == nil {
			return prev, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Quote{}, false, err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM quotes").Scan(&count); err != nil {
		return Quote{}, false, err
	}
	if count >= MaxQuotes {
		return Quote{}, false, fmt.Errorf("%w: up to %d quotes", ErrFull, MaxQuotes)
	}
	var key any
	if n.Key != "" {
		key = n.Key
	}
	ts := storage.Timestamp(now)
	q, err = scan(tx.QueryRowContext(ctx, `INSERT INTO quotes (text, added_by, added_by_id, platform, category, created_at, updated_at, request_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+columns, text, by, n.AddedByID, n.Platform, category, ts, ts, key))
	if err != nil {
		if n.Key != "" {
			// The same request raced in; the first one won.
			_ = tx.Rollback()
			if prev, perr := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM quotes WHERE request_key = ?", n.Key)); perr == nil {
				return prev, false, nil
			}
		}
		return Quote{}, false, fmt.Errorf("quotes: add: %w", err)
	}
	return q, true, tx.Commit()
}

// Get returns quote number id.
func (s *Store) Get(ctx context.Context, id int64) (Quote, error) {
	return s.one(ctx, s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM quotes WHERE id = ?", id))
}

// Random returns a quote chosen at random, or ErrNotFound when there are none.
func (s *Store) Random(ctx context.Context) (Quote, error) {
	n, err := s.Count(ctx)
	if err != nil {
		return Quote{}, err
	}
	if n == 0 {
		return Quote{}, ErrNotFound
	}
	i, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return Quote{}, err
	}
	return s.one(ctx, s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM quotes ORDER BY id LIMIT 1 OFFSET ?", i.Int64()))
}

// Count is how many quotes there are (not the highest number: deleted
// quotes leave gaps).
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM quotes").Scan(&n); err != nil {
		return 0, fmt.Errorf("quotes: count: %w", err)
	}
	return n, nil
}

// List returns quotes, newest first, whose text, adder or category contains
// query (any case), up to limit.
func (s *Store) List(ctx context.Context, query string, limit int) ([]Quote, error) {
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(query)) + "%"
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+` FROM quotes
		WHERE text LIKE ?1 ESCAPE '\' OR added_by LIKE ?1 ESCAPE '\' OR category LIKE ?1 ESCAPE '\' OR CAST(id AS TEXT) = ?2
		ORDER BY id DESC LIMIT ?3`, like, strings.TrimPrefix(strings.TrimSpace(query), "#"), limit)
	if err != nil {
		return nil, fmt.Errorf("quotes: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Quote{}
	for rows.Next() {
		q, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Edit changes a quote's text, who added it and its category. Its number
// and date stay.
func (s *Store) Edit(ctx context.Context, id int64, text, by, category string, now time.Time) (Quote, error) {
	text, by, category, err := clean(text, by, category)
	if err != nil {
		return Quote{}, err
	}
	return s.one(ctx, s.db.QueryRowContext(ctx, `UPDATE quotes SET text = ?, added_by = ?, category = ?,
		updated_at = CASE WHEN text = ? AND added_by = ? AND category = ? THEN updated_at ELSE ? END
		WHERE id = ? RETURNING `+columns, text, by, category, text, by, category, storage.Timestamp(now), id))
}

// Delete removes a quote. Later quotes keep their numbers.
func (s *Store) Delete(ctx context.Context, id int64) (bool, error) {
	r, err := s.db.ExecContext(ctx, "DELETE FROM quotes WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("quotes: delete: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// NextNumber is the number the next quote would get.
func (s *Store) NextNumber(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'quotes'), 0) + 1`).Scan(&n)
	return n, err
}
