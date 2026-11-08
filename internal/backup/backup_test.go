package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// folder is a data folder with a space in its path, like macOS's.
func folder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Application Support")
	if err := datadir.Ensure(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func openDB(t *testing.T, dir string) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), datadir.DatabasePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background(), Dir(dir)); err != nil {
		t.Fatal(err)
	}
	return db
}

func setTitle(t *testing.T, db *storage.DB, v string) {
	t.Helper()
	if err := settings.NewStore(db).Save(context.Background(), "test", map[string]string{"title": v}); err != nil {
		t.Fatal(err)
	}
}

func title(t *testing.T, db *storage.DB) string {
	t.Helper()
	var v map[string]string
	if _, err := settings.NewStore(db).Load(context.Background(), "test", &v); err != nil {
		t.Fatal(err)
	}
	return v["title"]
}

var t0 = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func TestABackupHoldsTheDatabaseAndNeverASecret(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	defer db.Close()
	sec, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "oauth-hunter2-do-not-leak"
	if err := sec.Set(ctx, "twitch/streamer/oauth", secrets.NewValue(secret)); err != nil {
		t.Fatal(err)
	}
	setTitle(t, db, "first")
	info, err := Create(ctx, db, dir, Manual, "test", t0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "streamgremlin-manual-20261108T120000.000Z.zip" || info.SchemaVersion < 7 {
		t.Fatalf("%+v", info)
	}
	p := filepath.Join(Dir(dir), info.Name)
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("the secret is in %s", f.Name)
		}
	}
	_ = zr.Close()
	if strings.Join(names, ",") != "manifest.json,streamgremlin.db" {
		t.Fatalf("archive holds %v", names)
	}
	if _, err := os.Stat(filepath.Join(dir, secrets.FileName)); err != nil {
		t.Fatal("the test should have a secrets file to leave out")
	}
	tmp, err := Verify(ctx, dir, info.Name)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(tmp)
	list, _ := List(dir)
	if len(list) != 1 || list[0].Kind != Manual || list[0].Problem != "" {
		t.Fatalf("%+v", list)
	}
}

// rewrite copies an archive, changing it on the way.
func rewrite(t *testing.T, src, dst string, change func(name string, b []byte) (string, []byte)) {
	t.Helper()
	zr, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	out, _ := os.Create(dst)
	zw := zip.NewWriter(out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		name, nb := change(f.Name, b)
		if name == "" {
			continue
		}
		w, _ := zw.Create(name)
		_, _ = w.Write(nb)
	}
	_ = zw.Close()
	_ = out.Close()
}

func TestDamagedOrForeignBackupsAreRefused(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	defer db.Close()
	good, err := Create(ctx, db, dir, Manual, "test", t0)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(Dir(dir), good.Name)
	n := 0
	bad := func(change func(name string, b []byte) (string, []byte)) string {
		n++
		name := strings.Replace(good.Name, "120000.000Z", "12000"+strconv.Itoa(n)+".000Z", 1)
		rewrite(t, src, filepath.Join(Dir(dir), name), change)
		return name
	}
	cases := map[string]string{
		"changed": bad(func(name string, b []byte) (string, []byte) {
			if name == "streamgremlin.db" {
				b = append([]byte(nil), b...)
				b[len(b)-1] ^= 0xff
			}
			return name, b
		}),
		"extra file": bad(func(name string, b []byte) (string, []byte) {
			if name == "manifest.json" {
				return "secrets.enc", []byte("x") // and no manifest at all
			}
			return name, b
		}),
		"another format": bad(func(name string, b []byte) (string, []byte) {
			if name == "manifest.json" {
				return name, bytes.Replace(b, []byte("streamgremlin.backup/v1"), []byte("streamwisp.backup/v1"), 1)
			}
			return name, b
		}),
	}
	for what, name := range cases {
		if tmp, err := Verify(ctx, dir, name); !errors.Is(err, ErrInvalid) {
			_ = os.Remove(tmp)
			t.Errorf("%s: %v", what, err)
		}
	}
	// Not a backup's name, or one outside the folder.
	for _, name := range []string{"../streamgremlin.db", "streamgremlin-manual-x.zip", "nope.zip"} {
		if _, err := Verify(ctx, dir, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
		if err := Delete(dir, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete %s: %v", name, err)
		}
	}
	// A database from a newer StreamGremlin is refused.
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (999, 'future', 'x', '2030-01-01T00:00:00.000Z')"); err != nil {
		t.Skip("schema_migrations is append-only in a way this test can't use:", err)
	}
	newer, err := Create(ctx, db, dir, Manual, "test", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(ctx, dir, newer.Name); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("%v", err)
	}
}

func TestOnlyScheduledBackupsArePruned(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	defer db.Close()
	for i := range 5 {
		if _, err := Create(ctx, db, dir, Scheduled, "test", t0.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(ctx, db, dir, Manual, "test", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, err := Prune(dir, 3); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	list, _ := List(dir)
	kinds := map[string]int{}
	for _, b := range list {
		kinds[b.Kind]++
	}
	if kinds[Scheduled] != 3 || kinds[Manual] != 1 || !LastOf(dir, Scheduled).Equal(t0.Add(4*time.Hour)) {
		t.Fatalf("%+v", list)
	}
}

func TestRestoreIsStagedAndAppliedAtStart(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	setTitle(t, db, "before")
	b, err := Create(ctx, db, dir, Manual, "test", t0)
	if err != nil {
		t.Fatal(err)
	}
	setTitle(t, db, "after")
	if err := Stage(ctx, dir, b.Name); err != nil {
		t.Fatal(err)
	}
	if Pending(dir) != b.Name || title(t, db) != "after" {
		t.Fatal("staging changed something")
	}
	_ = db.Close() // the engine stops

	res := ApplyPending(ctx, dir, "test", t0.Add(time.Minute))
	if res == nil || !res.OK || res.PreRestore == "" || res.KeptAside != "" || Pending(dir) != "" {
		t.Fatalf("%+v", res)
	}
	db = openDB(t, dir)
	if title(t, db) != "before" {
		t.Fatalf("restored %q", title(t, db))
	}
	_ = db.Close()
	if r := LastResult(dir); r == nil || r.From != b.Name || !r.OK {
		t.Fatalf("%+v", r)
	}
	// What it replaced was kept: restoring that brings "after" back.
	if err := Stage(ctx, dir, res.PreRestore); err != nil {
		t.Fatal(err)
	}
	if res := ApplyPending(ctx, dir, "test", t0.Add(2*time.Minute)); !res.OK {
		t.Fatalf("%+v", res)
	}
	db = openDB(t, dir)
	defer db.Close()
	if title(t, db) != "after" {
		t.Fatal("the pre-restore backup didn't hold the replaced state")
	}
	if ApplyPending(ctx, dir, "test", t0) != nil {
		t.Fatal("nothing was staged")
	}
}

func TestARestoreThatFailsChangesNothing(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	setTitle(t, db, "kept")
	b, _ := Create(ctx, db, dir, Manual, "test", t0)
	if err := Stage(ctx, dir, b.Name); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	// The backup is damaged after it was staged.
	p := filepath.Join(Dir(dir), b.Name)
	rewrite(t, p, p+".x", func(name string, raw []byte) (string, []byte) {
		if name == "streamgremlin.db" {
			raw = append([]byte(nil), raw...)
			raw[100] ^= 0xff
		}
		return name, raw
	})
	_ = os.Rename(p+".x", p)
	res := ApplyPending(ctx, dir, "test", t0)
	if res == nil || res.OK || !strings.Contains(res.Error, "doesn't match") || Pending(dir) != "" {
		t.Fatalf("%+v", res)
	}
	db = openDB(t, dir)
	defer db.Close()
	if title(t, db) != "kept" {
		t.Fatal("a failed restore changed the database")
	}
}

func TestRestoringOverADamagedDatabaseKeepsItsFiles(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	setTitle(t, db, "good")
	b, _ := Create(ctx, db, dir, Manual, "test", t0)
	if err := Stage(ctx, dir, b.Name); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	dbPath := datadir.DatabasePath(dir)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.WriteFile(dbPath, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := ApplyPending(ctx, dir, "test", t0)
	if !res.OK || res.KeptAside == "" {
		t.Fatalf("%+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(Dir(dir), res.KeptAside, filepath.Base(dbPath))); err != nil || string(b) != "this is not a database" {
		t.Fatal("the damaged database wasn't kept", err)
	}
	db = openDB(t, dir)
	defer db.Close()
	if title(t, db) != "good" {
		t.Fatal(title(t, db))
	}
}

func TestPreMigrationCopiesAreListedAndRestorable(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	db := openDB(t, dir)
	setTitle(t, db, "old")
	if _, err := db.Backup(ctx, Dir(dir), "before-migration-0008", t0); err != nil {
		t.Fatal(err)
	}
	setTitle(t, db, "new")
	list, _ := List(dir)
	if len(list) != 1 || list[0].Kind != PreMigration {
		t.Fatalf("%+v", list)
	}
	if err := Stage(ctx, dir, list[0].Name); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if res := ApplyPending(ctx, dir, "test", t0); !res.OK {
		t.Fatalf("%+v", res)
	}
	db = openDB(t, dir)
	defer db.Close()
	if title(t, db) != "old" {
		t.Fatal(title(t, db))
	}
	m, _ := json.Marshal(list[0])
	if !strings.Contains(string(m), `"kind":"pre-migration"`) {
		t.Fatal(string(m))
	}
}
