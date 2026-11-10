// Package wheels spins wheels of weighted choices (docs/adr/0030-wheels.md):
// the next game, a challenge, a mystery pick. The engine draws every spin
// with crypto/rand and records the wheel as it was, the number drawn and
// the result, so anyone can check it. Odds are exact (weight over total)
// and always shown as they are. A wheel overlay spins to the result the
// engine already drew; it never decides anything.
package wheels

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Limits.
const (
	MinChoices  = 2
	MaxChoices  = 50
	MaxLabel    = 60
	MaxTitle    = 100
	MaxWeight   = 1000
	MinSeconds  = 2
	MaxSeconds  = 20
	MaxWheels   = 100
	DefaultSpin = 6
)

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("wheels: invalid")
	ErrNotFound = errors.New("wheels: no such wheel")
	ErrExists   = errors.New("wheels: a wheel has that name")
	ErrFull     = errors.New("wheels: too many wheels")
	ErrBusy     = errors.New("wheels: too many spins are waiting")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// NamePattern is a wheel's name, used by workflows and the command line.
const NamePattern = `^[a-z0-9][a-z0-9_-]{0,63}$`

var (
	namePattern  = regexp.MustCompile(NamePattern)
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Choice is one wedge: a label, its weight (how many times as likely as a
// weight of 1), and a colour ("" for the overlay's own).
type Choice struct {
	Label  string `json:"label"`
	Weight int    `json:"weight"`
	Color  string `json:"color,omitempty"`
}

// Wheel is a wheel and its choices.
type Wheel struct {
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Choices     []Choice  `json:"choices"`
	SpinSeconds int       `json:"spin_seconds"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Settings make or change a wheel.
type Settings struct {
	Title       string   `json:"title"`
	Choices     []Choice `json:"choices"`
	SpinSeconds int      `json:"spin_seconds"`
}

func (st *Settings) normalise() error {
	st.Title = strings.TrimSpace(st.Title)
	if st.Title == "" || utf8.RuneCountInString(st.Title) > MaxTitle {
		return invalid("a wheel's title is 1 to %d characters", MaxTitle)
	}
	if st.SpinSeconds == 0 {
		st.SpinSeconds = DefaultSpin
	}
	if st.SpinSeconds < MinSeconds || st.SpinSeconds > MaxSeconds {
		return invalid("a spin takes %d to %d seconds", MinSeconds, MaxSeconds)
	}
	if len(st.Choices) < MinChoices || len(st.Choices) > MaxChoices {
		return invalid("a wheel has %d to %d choices", MinChoices, MaxChoices)
	}
	for i := range st.Choices {
		c := &st.Choices[i]
		c.Label = strings.TrimSpace(c.Label)
		c.Color = strings.ToLower(strings.TrimSpace(c.Color))
		if c.Weight == 0 {
			c.Weight = 1
		}
		switch {
		case c.Label == "" || utf8.RuneCountInString(c.Label) > MaxLabel:
			return invalid("choice %d: a label is 1 to %d characters", i+1, MaxLabel)
		case c.Weight < 1 || c.Weight > MaxWeight:
			return invalid("choice %d (%s): a weight is 1 to %d", i+1, c.Label, MaxWeight)
		case c.Color != "" && !colorPattern.MatchString(c.Color):
			return invalid("choice %d (%s): a colour is like #ff8800, or none", i+1, c.Label)
		}
	}
	return nil
}

// CheckName reports what's wrong with a wheel's name.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return invalid("a wheel's name is up to 64 lowercase letters, digits, - and _, like next-game")
	}
	return nil
}

// Total is the sum of the weights: a spin draws a number below it.
func Total(cs []Choice) int {
	n := 0
	for _, c := range cs {
		n += c.Weight
	}
	return n
}

// Winner is the choice a drawn number lands on: each choice holds its
// weight's worth of numbers, in order. -1 when draw is out of range.
func Winner(cs []Choice, draw int) int {
	if draw < 0 {
		return -1
	}
	for i, c := range cs {
		draw -= c.Weight
		if draw < 0 {
			return i
		}
	}
	return -1
}

// Odds is a choice's chance in words and as a fraction.
type Odds struct {
	Label   string  `json:"label"`
	Weight  int     `json:"weight"`
	Total   int     `json:"total"`
	Percent float64 `json:"percent"`
}

// OddsOf lists each choice's chance: weight in total.
func OddsOf(w Wheel) []Odds {
	total := Total(w.Choices)
	out := make([]Odds, 0, len(w.Choices))
	for _, c := range w.Choices {
		out = append(out, Odds{Label: c.Label, Weight: c.Weight, Total: total, Percent: 100 * float64(c.Weight) / float64(total)})
	}
	return out
}

// OddsWords says a wheel's odds for chat: "Next game: Mario 50%, Zelda 25%, …".
func OddsWords(w Wheel) string {
	var parts []string
	for _, o := range OddsOf(w) {
		parts = append(parts, fmt.Sprintf("%s %s", o.Label, percent(o.Percent)))
	}
	return w.Title + ": " + strings.Join(parts, ", ")
}

func percent(p float64) string {
	s := fmt.Sprintf("%.1f", p)
	s = strings.TrimSuffix(s, ".0")
	return s + "%"
}

// draw picks a number in [0, total) with crypto/rand.
func draw(total int) (int, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

func ts(t time.Time) string { return storage.Timestamp(t) }

const wheelColumns = "name, title, choices, spin_seconds, version, created_at, updated_at"

type scanner interface{ Scan(...any) error }

func scanWheel(s scanner) (Wheel, error) {
	var w Wheel
	var choices, created, updated string
	if err := s.Scan(&w.Name, &w.Title, &choices, &w.SpinSeconds, &w.Version, &created, &updated); err != nil {
		return w, err
	}
	if err := json.Unmarshal([]byte(choices), &w.Choices); err != nil {
		return w, fmt.Errorf("wheels: %s's choices: %w", w.Name, err)
	}
	w.CreatedAt, _ = storage.ParseTimestamp(created)
	w.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return w, nil
}

// List returns every wheel, by name.
func (s *Service) List(ctx context.Context) ([]Wheel, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+wheelColumns+" FROM wheels ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("wheels: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Wheel{}
	for rows.Next() {
		w, err := scanWheel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Get returns one wheel.
func (s *Service) Get(ctx context.Context, name string) (Wheel, error) {
	w, err := scanWheel(s.db.QueryRowContext(ctx, "SELECT "+wheelColumns+" FROM wheels WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return w, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return w, err
}

// Create adds a wheel.
func (s *Service) Create(ctx context.Context, name string, st Settings) (Wheel, error) {
	if err := CheckName(name); err != nil {
		return Wheel{}, err
	}
	if err := st.normalise(); err != nil {
		return Wheel{}, err
	}
	raw, _ := json.Marshal(st.Choices)
	now := ts(s.now())
	w, err := scanWheel(s.db.QueryRowContext(ctx, `INSERT INTO wheels (name, title, choices, spin_seconds, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ? WHERE (SELECT COUNT(*) FROM wheels) < ? ON CONFLICT (name) DO NOTHING RETURNING `+wheelColumns,
		name, st.Title, string(raw), st.SpinSeconds, now, now, MaxWheels))
	if errors.Is(err, sql.ErrNoRows) {
		var n int
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wheels").Scan(&n)
		if n >= MaxWheels {
			return Wheel{}, fmt.Errorf("%w: up to %d", ErrFull, MaxWheels)
		}
		return Wheel{}, fmt.Errorf("%w: %q", ErrExists, name)
	}
	return w, err
}

// Update changes a wheel. A change makes a new version; spins keep the
// version they were spun from.
func (s *Service) Update(ctx context.Context, name string, st Settings) (Wheel, error) {
	if err := st.normalise(); err != nil {
		return Wheel{}, err
	}
	raw, _ := json.Marshal(st.Choices)
	w, err := scanWheel(s.db.QueryRowContext(ctx, `UPDATE wheels SET title = ?, choices = ?, spin_seconds = ?, updated_at = ?,
		version = version + (title != ? OR choices != ? OR spin_seconds != ?) WHERE name = ? RETURNING `+wheelColumns,
		st.Title, string(raw), st.SpinSeconds, ts(s.now()), st.Title, string(raw), st.SpinSeconds, name))
	if errors.Is(err, sql.ErrNoRows) {
		return w, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return w, err
}

// Delete removes a wheel. Its spins are kept.
func (s *Service) Delete(ctx context.Context, name string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM wheels WHERE name = ?", name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
