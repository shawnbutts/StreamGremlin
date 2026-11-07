package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dir
}

func mig(v int, name, sql string) Migration {
	return Migration{Version: v, Name: name, SQL: sql, Checksum: name + sql}
}

var fixedNow = func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) }

func TestOpenSettingsAndFileMode(t *testing.T) {
	db, _ := openTemp(t)
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode %q, %v", mode, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestEmbeddedMigrationsApply(t *testing.T) {
	db, dir := openTemp(t)
	res, err := db.Migrate(context.Background(), filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if res.From != 0 || res.To < 1 || res.Backup != "" {
		t.Fatalf("unexpected result %+v", res)
	}
	// Running again is a no-op.
	res2, err := db.Migrate(context.Background(), filepath.Join(dir, "backups"))
	if err != nil || res2.From != res.To || res2.To != res.To {
		t.Fatalf("second run %+v, %v", res2, err)
	}
	v, err := db.SchemaVersion(context.Background())
	if err != nil || v != res.To {
		t.Fatalf("SchemaVersion %d, %v", v, err)
	}
}

func TestBackupBeforeChangingExistingSchema(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	backups := filepath.Join(dir, "backups")
	one := []Migration{mig(1, "a", "CREATE TABLE a (x INTEGER) STRICT;")}
	if _, err := db.migrate(ctx, one, backups, fixedNow); err != nil {
		t.Fatal(err)
	}
	two := append(one, mig(2, "b", "CREATE TABLE b (x INTEGER) STRICT;"))
	res, err := db.migrate(ctx, two, backups, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != 1 || res.To != 2 || res.Backup == "" {
		t.Fatalf("unexpected result %+v", res)
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestRefusesEditedMigration(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	if _, err := db.migrate(ctx, []Migration{mig(1, "a", "CREATE TABLE a (x INTEGER) STRICT;")}, dir, fixedNow); err != nil {
		t.Fatal(err)
	}
	_, err := db.migrate(ctx, []Migration{mig(1, "a", "CREATE TABLE a (y INTEGER) STRICT;")}, dir, fixedNow)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	two := []Migration{
		mig(1, "a", "CREATE TABLE a (x INTEGER) STRICT;"),
		mig(2, "b", "CREATE TABLE b (x INTEGER) STRICT;"),
	}
	if _, err := db.migrate(ctx, two, dir, fixedNow); err != nil {
		t.Fatal(err)
	}
	_, err := db.migrate(ctx, two[:1], dir, fixedNow)
	if !errors.Is(err, ErrDatabaseNewer) {
		t.Fatalf("got %v, want ErrDatabaseNewer", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	db, dir := openTemp(t)
	ctx := context.Background()
	bad := []Migration{mig(1, "a", "CREATE TABLE a (x INTEGER) STRICT; CREATE TABLE a (x INTEGER) STRICT;")}
	if _, err := db.migrate(ctx, bad, dir, fixedNow); err == nil {
		t.Fatal("expected failure")
	}
	if v, _ := db.SchemaVersion(ctx); v != 0 {
		t.Fatalf("version %d after failed migration, want 0", v)
	}
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name = 'a'").Scan(&n)
	if n != 0 {
		t.Fatal("table from failed migration was left behind")
	}
}

func TestConcurrentMigratorsApplyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "race.db")
	ctx := context.Background()
	migs := []Migration{
		mig(1, "a", "CREATE TABLE a (x INTEGER) STRICT;"),
		mig(2, "b", "CREATE TABLE b (x INTEGER) STRICT;"),
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := Open(ctx, path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			if _, err := db.migrate(ctx, migs, dir, fixedNow); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil || n != 2 {
		t.Fatalf("schema_migrations rows %d, %v; want 2", n, err)
	}
}

func TestMigrationLogIsAppendOnly(t *testing.T) {
	db, dir := openTemp(t)
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations"); err == nil {
		t.Fatal("delete from schema_migrations succeeded")
	}
	if _, err := db.Exec("UPDATE schema_migrations SET name = 'x'"); err == nil {
		t.Fatal("update of schema_migrations succeeded")
	}
}

func TestMigrationNumbering(t *testing.T) {
	migs, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 1 {
		t.Errorf("%d migrations; before the first public release there is one baseline", len(migs))
	}
}

func TestTimestampFormat(t *testing.T) {
	ts := Timestamp(time.Date(2026, 11, 7, 1, 2, 3, 4_000_000, time.FixedZone("x", 3600)))
	if ts != "2026-11-07T00:02:03.004Z" {
		t.Fatalf("got %s", ts)
	}
	if _, err := ParseTimestamp(ts); err != nil {
		t.Fatal(err)
	}
}
