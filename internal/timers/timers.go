// Package timers runs Streamer.bot-style timed actions
// (docs/adr/0013-timers.md): a timer goes off every so often, or on a cron
// schedule, and records a timers.timer.fired event that workflows match by
// timer_id. A timer can wait for chat lines, run only while live, and run
// once; each counts how often it has gone off.
package timers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Limits.
const (
	MinEvery     = 10           // seconds
	MaxEvery     = 24 * 60 * 60 // seconds
	MaxChatLines = 1000
	MaxName      = 100
	MaxTimers    = 200
)

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("timers: invalid")
	ErrNotFound = errors.New("timers: no such timer")
	ErrFull     = errors.New("timers: too many timers")
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// IDPattern is what a timer's id looks like, for the workflow step.
const IDPattern = `^[a-z0-9][a-z0-9_-]*$`

// Timer is one timer, as saved.
type Timer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// EverySeconds goes off every so often; with UpToSeconds, at random
	// between the two. Zero when Cron is set.
	EverySeconds int `json:"every_seconds,omitempty"`
	UpToSeconds  int `json:"up_to_seconds,omitempty"`
	// Cron is a five-field schedule in the computer's local time.
	Cron string `json:"cron,omitempty"`
	// ChatLines: when its time comes, it waits for this many chat messages
	// from viewers (not you or the bot) since it last went off.
	ChatLines int  `json:"chat_lines"`
	LiveOnly  bool `json:"live_only"`
	// Once turns it off after it goes off on schedule (Run now doesn't).
	Once bool `json:"once"`
	// Counter is how many times it has gone off, Run now included. Saving
	// keeps it; only a new timer takes it from the request.
	Counter     int64      `json:"counter"`
	LastFiredAt *time.Time `json:"last_fired_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// CheckID reports what's wrong with a timer's id.
func CheckID(id string) error {
	if len(id) > 64 || !idPattern.MatchString(id) {
		return invalid("a timer's id is up to 64 lowercase letters, digits, - and _, like socials")
	}
	return nil
}

// Check reports what's wrong with t, in words.
func (t *Timer) Check() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Cron = strings.TrimSpace(t.Cron)
	if err := CheckID(t.ID); err != nil {
		return err
	}
	switch {
	case t.Name == "" || utf8.RuneCountInString(t.Name) > MaxName:
		return invalid("a timer needs a name of up to %d characters", MaxName)
	case t.ChatLines < 0 || t.ChatLines > MaxChatLines:
		return invalid("chat lines are 0 to %d", MaxChatLines)
	case t.Counter < 0:
		return invalid("the count can't be negative")
	}
	if t.Cron != "" {
		if t.EverySeconds != 0 || t.UpToSeconds != 0 {
			return invalid("a timer runs every so often or on a schedule, not both")
		}
		if _, err := ParseCron(t.Cron); err != nil {
			return invalid("%s", err.Error())
		}
		return nil
	}
	switch {
	case t.EverySeconds < MinEvery || t.EverySeconds > MaxEvery:
		return invalid("a timer runs every 10 seconds to 24 hours")
	case t.UpToSeconds != 0 && (t.UpToSeconds <= t.EverySeconds || t.UpToSeconds > MaxEvery):
		return invalid("the random range ends after it starts, and within 24 hours")
	}
	return nil
}

// Store keeps timers in the database.
type Store struct{ db *storage.DB }

const columns = `id, name, enabled, every_seconds, up_to_seconds, cron, chat_lines, live_only, run_once,
	fire_count, last_fired_at, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanTimer(s scanner) (Timer, error) {
	var t Timer
	var last sql.NullString
	var created, updated string
	err := s.Scan(&t.ID, &t.Name, &t.Enabled, &t.EverySeconds, &t.UpToSeconds, &t.Cron, &t.ChatLines, &t.LiveOnly, &t.Once,
		&t.Counter, &last, &created, &updated)
	if err != nil {
		return Timer{}, err
	}
	if last.Valid {
		if at, err := storage.ParseTimestamp(last.String); err == nil {
			t.LastFiredAt = &at
		}
	}
	t.CreatedAt, _ = storage.ParseTimestamp(created)
	t.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return t, nil
}

// List returns every timer, by name.
func (s *Store) List(ctx context.Context) ([]Timer, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM timers ORDER BY name COLLATE NOCASE, id")
	if err != nil {
		return nil, fmt.Errorf("timers: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Timer{}
	for rows.Next() {
		t, err := scanTimer(rows)
		if err != nil {
			return nil, fmt.Errorf("timers: list: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns one timer.
func (s *Store) Get(ctx context.Context, id string) (Timer, error) {
	t, err := scanTimer(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM timers WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Timer{}, ErrNotFound
	}
	if err != nil {
		return Timer{}, fmt.Errorf("timers: get: %w", err)
	}
	return t, nil
}

// Save creates or replaces a timer. Saving an unchanged timer changes
// nothing, so its schedule keeps counting; any change restarts it.
func (s *Store) Save(ctx context.Context, t Timer, now time.Time) (Timer, bool, error) {
	if err := t.Check(); err != nil {
		return Timer{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Timer{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := scanTimer(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM timers WHERE id = ?", t.ID))
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return Timer{}, false, fmt.Errorf("timers: save: %w", err)
	}
	ts := storage.Timestamp(now)
	if created {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM timers").Scan(&n); err != nil {
			return Timer{}, false, err
		}
		if n >= MaxTimers {
			return Timer{}, false, fmt.Errorf("%w: up to %d timers; delete one first", ErrFull, MaxTimers)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO timers (id, name, enabled, every_seconds, up_to_seconds, cron, chat_lines,
			live_only, run_once, fire_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Name, t.Enabled, t.EverySeconds, t.UpToSeconds, t.Cron, t.ChatLines, t.LiveOnly, t.Once, t.Counter, ts, ts)
	} else {
		if old.Name == t.Name && old.Enabled == t.Enabled && old.EverySeconds == t.EverySeconds && old.UpToSeconds == t.UpToSeconds &&
			old.Cron == t.Cron && old.ChatLines == t.ChatLines && old.LiveOnly == t.LiveOnly && old.Once == t.Once {
			return old, false, nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE timers SET name = ?, enabled = ?, every_seconds = ?, up_to_seconds = ?, cron = ?,
			chat_lines = ?, live_only = ?, run_once = ?, updated_at = ? WHERE id = ?`,
			t.Name, t.Enabled, t.EverySeconds, t.UpToSeconds, t.Cron, t.ChatLines, t.LiveOnly, t.Once, ts, t.ID)
	}
	if err != nil {
		return Timer{}, false, fmt.Errorf("timers: save: %w", err)
	}
	saved, err := scanTimer(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM timers WHERE id = ?", t.ID))
	if err != nil {
		return Timer{}, false, err
	}
	return saved, created, tx.Commit()
}

// State is how a workflow step turns a timer on or off.
type State string

// States.
const (
	On     State = "on"
	Off    State = "off"
	Toggle State = "toggle"
)

// SetState turns a timer on, off, or the other way, and returns whether
// it's on. Turning one on starts its count again.
func (s *Store) SetState(ctx context.Context, id string, st State, now time.Time) (bool, error) {
	var expr string
	switch st {
	case On:
		expr = "1"
	case Off:
		expr = "0"
	case Toggle:
		expr = "1 - enabled"
	default:
		return false, invalid("the state is on, off or toggle")
	}
	var on bool
	err := s.db.QueryRowContext(ctx, `UPDATE timers SET updated_at = CASE WHEN enabled = `+expr+` THEN updated_at ELSE ? END,
		enabled = `+expr+` WHERE id = ? RETURNING enabled`, storage.Timestamp(now), id).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return on, err
}

// Delete removes a timer. Workflows that name it are kept; they just
// never start from it.
func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	r, err := s.db.ExecContext(ctx, "DELETE FROM timers WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("timers: delete: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// countFire adds one to the timer's count, and turns a run-once timer off
// when it went off on schedule.
func (s *Store) countFire(ctx context.Context, id string, manual bool, now time.Time) (Timer, error) {
	ts := storage.Timestamp(now)
	t, err := scanTimer(s.db.QueryRowContext(ctx, `UPDATE timers SET fire_count = fire_count + 1, last_fired_at = ?,
		enabled = CASE WHEN run_once AND ? THEN 0 ELSE enabled END,
		updated_at = CASE WHEN run_once AND ? AND enabled THEN ? ELSE updated_at END
		WHERE id = ? RETURNING `+columns, ts, !manual, !manual, ts, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Timer{}, ErrNotFound
	}
	return t, err
}
