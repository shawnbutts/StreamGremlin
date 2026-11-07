// Package storage opens the SQLite database and applies its schema.
// Conventions are in docs/adr/0001-storage.md.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// TimeFormat is how every timestamp is stored: UTC, milliseconds, sortable as text.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Timestamp formats t for storage.
func Timestamp(t time.Time) string { return t.UTC().Format(TimeFormat) }

// ParseTimestamp reads a stored timestamp.
func ParseTimestamp(s string) (time.Time, error) { return time.Parse(TimeFormat, s) }

// DB is the application database.
type DB struct {
	*sql.DB
	path string
}

// Open opens (creating if needed) the database at path with the project's
// connection settings, and checks that they took effect. It does not migrate.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("storage: database path must not contain '?' or '#': %q", path)
	}
	// Create the file ourselves so it starts private; SQLite copies the mode
	// to the -wal and -shm files.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("storage: create %s: %w", path, err)
	}
	_ = f.Close()

	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	db := &DB{DB: sqldb, path: path}
	if err := db.setWAL(ctx); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	if err := db.verify(ctx); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	return db, nil
}

// Path is the database file's location.
func (db *DB) Path() string { return db.path }

// setWAL switches to WAL. The mode is stored in the file, so this only does
// work once. Switching can report SQLITE_BUSY without using the busy handler
// when several processes open a new file at once, so it retries briefly.
func (db *DB) setWAL(ctx context.Context) error {
	var lastErr error
	for i := 0; i < 50; i++ {
		var mode string
		lastErr = db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
		if lastErr == nil && strings.EqualFold(mode, "wal") {
			return nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("journal_mode is %q", mode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return fmt.Errorf("storage: enable WAL: %w", lastErr)
}

func (db *DB) verify(ctx context.Context) error {
	checks := []struct {
		pragma string
		want   int64
	}{
		{"busy_timeout", 5000},
		{"foreign_keys", 1},
		{"synchronous", 2}, // FULL
	}
	for _, c := range checks {
		var got int64
		if err := db.QueryRowContext(ctx, "PRAGMA "+c.pragma).Scan(&got); err != nil {
			return fmt.Errorf("storage: read %s: %w", c.pragma, err)
		}
		if got != c.want {
			return fmt.Errorf("storage: %s is %d, want %d", c.pragma, got, c.want)
		}
	}
	return nil
}

// ErrNotFound is returned by stores when a row doesn't exist.
var ErrNotFound = errors.New("not found")
