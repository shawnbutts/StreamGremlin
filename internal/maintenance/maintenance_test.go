package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/backup"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func rig(t *testing.T) (*Service, *storage.DB, string, *clock) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	if err := datadir.Ensure(dir); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, datadir.DatabasePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, datadir.BackupDir(dir)); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	s := New(Options{DB: db, Settings: settings.NewStore(db), DataDir: dir, AppVersion: "test", Now: c.now})
	return s, db, dir, c
}

func TestSettingsAreChecked(t *testing.T) {
	s, _, _, _ := rig(t)
	ctx := context.Background()
	if st, _ := s.Settings(ctx); st != Defaults {
		t.Fatal(st)
	}
	for _, bad := range []Settings{
		{BackupEveryHours: -1, BackupKeep: 7}, {BackupEveryHours: 200, BackupKeep: 7}, {BackupEveryHours: 24, BackupKeep: 0},
		{BackupEveryHours: 24, BackupKeep: 7, EventsDays: -1}, {BackupEveryHours: 24, BackupKeep: 7, RunsDays: 4000},
	} {
		if _, err := s.SetSettings(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	want := Settings{BackupEveryHours: 0, BackupKeep: 3, EventsDays: 0, RunsDays: 7}
	if _, err := s.SetSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Settings(ctx); st != want {
		t.Fatal(st)
	}
}

func TestBackupsRunOnScheduleAndArePruned(t *testing.T) {
	s, _, dir, c := rig(t)
	ctx := context.Background()
	_, _ = s.SetSettings(ctx, Settings{BackupEveryHours: 24, BackupKeep: 2, EventsDays: 30, RunsDays: 90})
	s.started = c.now()
	s.runDue(ctx) // just started: waits a minute
	if list, _ := backup.List(dir); len(list) != 0 {
		t.Fatal("backed up during the start delay")
	}
	c.add(time.Minute)
	s.runDue(ctx)
	if list, _ := backup.List(dir); len(list) != 1 || list[0].Kind != backup.Scheduled {
		t.Fatalf("%+v", list)
	}
	c.add(time.Hour)
	s.runDue(ctx) // not due yet
	if list, _ := backup.List(dir); len(list) != 1 {
		t.Fatal("backed up again within a day")
	}
	for range 3 {
		c.add(24 * time.Hour)
		s.runDue(ctx)
	}
	if list, _ := backup.List(dir); len(list) != 2 {
		t.Fatalf("kept %d, want 2", len(list))
	}
	st := s.Status(ctx)
	if st.LastBackupAt == nil || st.NextBackupAt == nil || !st.NextBackupAt.Equal(st.LastBackupAt.Add(24*time.Hour)) {
		t.Fatalf("%+v", st)
	}
	// Turned off: none due.
	_, _ = s.SetSettings(ctx, Settings{BackupEveryHours: 0, BackupKeep: 2})
	c.add(48 * time.Hour)
	s.runDue(ctx)
	if list, _ := backup.List(dir); len(list) != 2 || s.Status(ctx).NextBackupAt != nil {
		t.Fatal("backed up while turned off")
	}
}

func TestAFailedBackupIsShownAndRetriedLater(t *testing.T) {
	s, _, dir, c := rig(t)
	ctx := context.Background()
	s.started = c.now().Add(-time.Hour)
	// A file where the backups folder goes: backups now fail, as on a full
	// or read-only disk.
	_ = os.RemoveAll(backup.Dir(dir))
	if err := os.WriteFile(backup.Dir(dir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.runDue(ctx)
	st := s.Status(ctx)
	if st.LastBackupError == "" || st.NextBackupAt == nil || !st.NextBackupAt.Equal(c.now().Add(time.Hour)) {
		t.Fatalf("%+v", st)
	}
	_ = os.Remove(backup.Dir(dir))
	s.runDue(ctx) // fixed, but not retried until the hour is up
	if list, _ := backup.List(dir); len(list) != 0 {
		t.Fatal("retried at once")
	}
	c.add(time.Hour)
	s.runDue(ctx)
	if list, _ := backup.List(dir); len(list) != 1 || s.Status(ctx).LastBackupError != "" {
		t.Fatalf("%+v %+v", list, s.Status(ctx))
	}
}

func TestTheSweepKeepsWhatItShould(t *testing.T) {
	s, db, _, c := rig(t)
	ctx := context.Background()
	now := c.now()
	ts := func(d time.Duration) string { return storage.Timestamp(now.Add(-d)) }
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	event := func(seq int, age time.Duration) {
		exec(`INSERT INTO events (seq, event_id, type, schema_version, source, occurred_at, received_at, payload, correlation_id, delivery_quality, content_hash)
			VALUES (?, ?, 'chat.message.received', 1, 'twitch', ?, ?, '{}', 'c', 'confirmed', 'h')`, seq, "e"+strconv.Itoa(seq), ts(age), ts(age))
	}
	day := 24 * time.Hour
	event(1, 40*day) // old: swept
	event(2, 40*day) // old, but a run for it is still waiting: kept
	event(3, 2*day)  // recent: kept
	exec(`INSERT INTO workflows (id, name, enabled, current_version, created_at, updated_at) VALUES ('w', 'W', 1, 1, ?, ?)`, ts(0), ts(0))
	exec(`INSERT INTO workflow_versions (workflow_id, version, definition, checksum, created_at) VALUES ('w', 1, '{}', 'x', ?)`, ts(0))
	job := func(id string, seq int, status string, finishedAge time.Duration) {
		var fin any
		if finishedAge > 0 {
			fin = ts(finishedAge)
		}
		exec(`INSERT INTO jobs (id, workflow_id, version, event_id, event_seq, status, dry_run, created_at, finished_at) VALUES (?, 'w', 1, ?, ?, ?, 0, ?, ?)`,
			id, "e"+strconv.Itoa(seq), seq, status, ts(100*day), fin)
		exec(`INSERT INTO job_steps (job_id, path, kind, status, started_at, finished_at) VALUES (?, 'steps[0]', 'action', 'ok', ?, ?)`, id, ts(0), ts(0))
	}
	job("old", 1, "succeeded", 100*day) // swept with its trace
	job("recent", 3, "failed", 10*day)  // kept
	job("waiting", 2, "pending", 0)     // kept
	exec(`INSERT INTO variables (scope, owner, name, value, updated_at, updated_by) VALUES ('global', '', 'deaths', '3', ?, 'you')`, ts(400*day))

	res, err := s.Sweep(ctx)
	if err != nil || res.Events != 1 || res.Runs != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	count := func(q string) (n int) {
		_ = db.QueryRowContext(ctx, q).Scan(&n)
		return
	}
	if count("SELECT count(*) FROM events") != 2 || count("SELECT count(*) FROM jobs") != 2 ||
		count("SELECT count(*) FROM job_steps") != 2 || count("SELECT count(*) FROM variables") != 1 ||
		count("SELECT count(*) FROM workflow_versions") != 1 {
		t.Fatal("swept the wrong things")
	}
	if s.Status(ctx).LastSweep == nil {
		t.Fatal("no sweep recorded")
	}
	// 0 keeps forever.
	_, _ = s.SetSettings(ctx, Settings{BackupEveryHours: 24, BackupKeep: 7, EventsDays: 0, RunsDays: 0})
	if res, _ := s.Sweep(ctx); res.Events != 0 || res.Runs != 0 {
		t.Fatalf("%+v", res)
	}
}
