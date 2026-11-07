package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is one numbered schema change.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

var (
	// ErrChecksumMismatch means an applied migration's SQL has changed since
	// it ran. Before the first public release this happens when the baseline
	// is rewritten; the dev database then has to be moved aside.
	ErrChecksumMismatch = errors.New("storage: an applied migration has changed")
	// ErrDatabaseNewer means the database was migrated by a newer build.
	ErrDatabaseNewer = errors.New("storage: the database is newer than this build")
)

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migrations returns the embedded migrations in order. They must be numbered
// from 0001 with no gaps.
func Migrations() ([]Migration, error) { return loadMigrations(migrationFiles, "migrations") }

func loadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("storage: bad migration file name %q", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		b, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(b), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("storage: migrations must be numbered 0001 upwards with no gaps; found %04d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}

const bookkeeping = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	checksum   TEXT NOT NULL,
	applied_at TEXT NOT NULL
) STRICT;
CREATE TRIGGER IF NOT EXISTS schema_migrations_no_update
	BEFORE UPDATE ON schema_migrations
	BEGIN SELECT RAISE(ABORT, 'schema_migrations is append-only'); END;
CREATE TRIGGER IF NOT EXISTS schema_migrations_no_delete
	BEFORE DELETE ON schema_migrations
	BEGIN SELECT RAISE(ABORT, 'schema_migrations is append-only'); END;
`

// MigrateResult says what Migrate did.
type MigrateResult struct {
	From, To int
	Backup   string // path of the backup taken before changing, if any
}

// Migrate brings the schema up to date. Before changing a database that
// already has a schema it writes a backup to backupDir. Each migration runs
// in its own transaction that re-reads the version first, so two processes
// racing on a new file apply each migration exactly once.
func (db *DB) Migrate(ctx context.Context, backupDir string) (MigrateResult, error) {
	migs, err := Migrations()
	if err != nil {
		return MigrateResult{}, err
	}
	return db.migrate(ctx, migs, backupDir, time.Now)
}

func (db *DB) migrate(ctx context.Context, migs []Migration, backupDir string, now func() time.Time) (MigrateResult, error) {
	if _, err := db.ExecContext(ctx, bookkeeping); err != nil {
		return MigrateResult{}, fmt.Errorf("storage: create schema_migrations: %w", err)
	}
	applied, err := db.applied(ctx)
	if err != nil {
		return MigrateResult{}, err
	}
	res := MigrateResult{From: len(applied), To: len(applied)}
	for v, sum := range applied {
		if v > len(migs) {
			return res, fmt.Errorf("%w: it has schema version %d, this build knows up to %d", ErrDatabaseNewer, v, len(migs))
		}
		if migs[v-1].Checksum != sum {
			return res, fmt.Errorf("%w: migration %04d_%s; move %s aside to start a fresh development database",
				ErrChecksumMismatch, v, migs[v-1].Name, db.path)
		}
	}
	if len(applied) == len(migs) {
		return res, nil
	}
	if len(applied) > 0 {
		path, err := db.Backup(ctx, backupDir, fmt.Sprintf("before-migration-%04d", len(applied)+1), now())
		if err != nil {
			return res, err
		}
		res.Backup = path
	}
	for _, m := range migs[len(applied):] {
		if err := db.applyOne(ctx, m, now()); err != nil {
			return res, err
		}
		res.To = m.Version
	}
	return res, nil
}

func (db *DB) applied(ctx context.Context) (map[int]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("storage: read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, err
		}
		out[v] = sum
	}
	return out, rows.Err()
}

func (db *DB) applyOne(ctx context.Context, m Migration, at time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	if current >= m.Version {
		return nil // another process applied it first
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("storage: migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
		m.Version, m.Name, m.Checksum, Timestamp(at)); err != nil {
		return err
	}
	return tx.Commit()
}

// SchemaVersion is the highest applied migration, or 0.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Backup writes a consistent copy of the database to dir with VACUUM INTO.
func (db *DB) Backup(ctx context.Context, dir, label string, at time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("storage: backup folder: %w", err)
	}
	name := fmt.Sprintf("%s-%s.db", at.UTC().Format("20060102T150405.000Z"), label)
	path := filepath.Join(dir, name)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return "", fmt.Errorf("storage: backup: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return path, nil
}
