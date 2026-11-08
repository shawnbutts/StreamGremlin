// Package maintenance keeps the data folder tidy while the engine runs
// (docs/adr/0012-backups-and-retention.md): a backup every day, keeping the
// newest few, and a sweep of old events and finished runs. Workflows, their
// versions and saved variables are never swept.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/backup"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Settings say how often to back up and how long to keep history.
type Settings struct {
	BackupEveryHours int `json:"backup_every_hours"` // 0: no automatic backups
	BackupKeep       int `json:"backup_keep"`        // automatic backups kept
	EventsDays       int `json:"events_days"`        // 0: keep events forever
	RunsDays         int `json:"runs_days"`          // 0: keep finished runs forever
}

// Defaults apply until the streamer saves settings.
var Defaults = Settings{BackupEveryHours: 24, BackupKeep: 7, EventsDays: 30, RunsDays: 90}

// ErrInvalid wraps settings that can't be saved.
var ErrInvalid = errors.New("maintenance: invalid settings")

// Check reports what's wrong with s, in words.
func (s Settings) Check() error {
	switch {
	case s.BackupEveryHours < 0 || s.BackupEveryHours > 24*7:
		return fmt.Errorf("%w: back up every 1 to 168 hours, or 0 for never", ErrInvalid)
	case s.BackupKeep < 1 || s.BackupKeep > 60:
		return fmt.Errorf("%w: keep 1 to 60 automatic backups", ErrInvalid)
	case s.EventsDays < 0 || s.EventsDays > 3650:
		return fmt.Errorf("%w: keep events 1 to 3650 days, or 0 for always", ErrInvalid)
	case s.RunsDays < 0 || s.RunsDays > 3650:
		return fmt.Errorf("%w: keep runs 1 to 3650 days, or 0 for always", ErrInvalid)
	}
	return nil
}

const settingsKey = "maintenance"

// Sweep is what one sweep removed.
type Sweep struct {
	At     time.Time `json:"at"`
	Events int64     `json:"events"`
	Runs   int64     `json:"runs"`
	Error  string    `json:"error,omitempty"`
}

// Status is what the engine has done lately.
type Status struct {
	LastBackupAt    *time.Time `json:"last_backup_at,omitempty"`
	LastBackupError string     `json:"last_backup_error,omitempty"`
	NextBackupAt    *time.Time `json:"next_backup_at,omitempty"`
	LastSweep       *Sweep     `json:"last_sweep,omitempty"`
}

// Options configure a Service.
type Options struct {
	DB         *storage.DB
	Settings   *settings.Store
	DataDir    string
	AppVersion string
	Logger     *slog.Logger
	Now        func() time.Time
	// Tick is how often the service looks at what's due (default 1 minute).
	Tick time.Duration
	// StartDelay holds the first backup and sweep after start (default 1
	// minute), so starting a stream isn't slowed down.
	StartDelay time.Duration
}

// Service runs backups and sweeps on schedule.
type Service struct {
	opts    Options
	log     *slog.Logger
	started time.Time

	mu        sync.Mutex
	status    Status
	lastSweep time.Time
	retryAt   time.Time // after a failed backup
	stop      context.CancelFunc
	done      chan struct{}
}

// SweepEvery is how often old history is swept.
const SweepEvery = 6 * time.Hour

// New returns a Service. Start runs it.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Tick == 0 {
		opts.Tick = time.Minute
	}
	if opts.StartDelay == 0 {
		opts.StartDelay = time.Minute
	}
	return &Service{opts: opts, log: opts.Logger.With("component", "maintenance")}
}

// Settings returns the saved settings, or the defaults.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := Defaults
	if _, err := s.opts.Settings.Load(ctx, settingsKey, &st); err != nil {
		return Defaults, err
	}
	return st, nil
}

// SetSettings checks and saves settings.
func (s *Service) SetSettings(ctx context.Context, st Settings) (Settings, error) {
	if err := st.Check(); err != nil {
		return Settings{}, err
	}
	if err := s.opts.Settings.Save(ctx, settingsKey, st); err != nil {
		return Settings{}, err
	}
	s.log.Info("maintenance settings changed", "backup_every_hours", st.BackupEveryHours, "backup_keep", st.BackupKeep,
		"events_days", st.EventsDays, "runs_days", st.RunsDays)
	return st, nil
}

// Status returns what's been done and what's next.
func (s *Service) Status(ctx context.Context) Status {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	if last := backup.LastOf(s.opts.DataDir, backup.Scheduled); !last.IsZero() {
		st.LastBackupAt = &last
	}
	if cfg, err := s.Settings(ctx); err == nil && cfg.BackupEveryHours > 0 {
		next := s.nextBackup(cfg)
		st.NextBackupAt = &next
	}
	return st
}

// nextBackup is when the next automatic backup is due.
func (s *Service) nextBackup(cfg Settings) time.Time {
	next := s.started.Add(s.opts.StartDelay)
	if last := backup.LastOf(s.opts.DataDir, backup.Scheduled); !last.IsZero() {
		if due := last.Add(time.Duration(cfg.BackupEveryHours) * time.Hour); due.After(next) {
			next = due
		}
	}
	s.mu.Lock()
	if s.retryAt.After(next) {
		next = s.retryAt
	}
	s.mu.Unlock()
	return next
}

// Start runs backups and sweeps until Close.
func (s *Service) Start(ctx context.Context) {
	ctx, s.stop = context.WithCancel(ctx)
	s.done = make(chan struct{})
	s.started = s.opts.Now()
	go func() {
		defer close(s.done)
		t := time.NewTicker(s.opts.Tick)
		defer t.Stop()
		for {
			s.runDue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// Close stops the service, waiting for a backup or sweep in progress.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
}

func (s *Service) runDue(ctx context.Context) {
	now := s.opts.Now()
	if now.Before(s.started.Add(s.opts.StartDelay)) {
		return
	}
	cfg, err := s.Settings(ctx)
	if err != nil {
		s.log.Warn("couldn't read maintenance settings", "err", err)
		return
	}
	if cfg.BackupEveryHours > 0 && !now.Before(s.nextBackup(cfg)) {
		s.scheduledBackup(ctx, cfg, now)
	}
	s.mu.Lock()
	sweepDue := s.lastSweep.IsZero() || now.Sub(s.lastSweep) >= SweepEvery
	s.mu.Unlock()
	if sweepDue {
		_, _ = s.Sweep(ctx)
	}
}

func (s *Service) scheduledBackup(ctx context.Context, cfg Settings, now time.Time) {
	info, err := backup.Create(ctx, s.opts.DB, s.opts.DataDir, backup.Scheduled, s.opts.AppVersion, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.status.LastBackupError = err.Error()
		s.retryAt = now.Add(time.Hour)
		s.log.Warn("automatic backup failed; trying again in an hour", "err", err)
		return
	}
	s.status.LastBackupError = ""
	s.retryAt = time.Time{}
	n, _ := backup.Prune(s.opts.DataDir, cfg.BackupKeep)
	s.log.Info("automatic backup made", "backup", info.Name, "pruned", n)
}

// Sweep removes events and finished runs older than the settings keep.
// Runs still waiting or running, and the events they belong to, are kept.
func (s *Service) Sweep(ctx context.Context) (Sweep, error) {
	now := s.opts.Now()
	res := Sweep{At: now.UTC()}
	cfg, err := s.Settings(ctx)
	if err == nil {
		res.Events, res.Runs, err = sweep(ctx, s.opts.DB, cfg, now)
	}
	if err != nil {
		res.Error = err.Error()
		s.log.Warn("sweeping old history failed", "err", err)
	} else if res.Events+res.Runs > 0 {
		s.log.Info("old history swept", "events", res.Events, "runs", res.Runs)
	}
	s.mu.Lock()
	s.lastSweep = now
	s.status.LastSweep = &res
	s.mu.Unlock()
	return res, err
}

func sweep(ctx context.Context, db *storage.DB, cfg Settings, now time.Time) (events, runs int64, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if cfg.RunsDays > 0 {
		cut := storage.Timestamp(now.Add(-time.Duration(cfg.RunsDays) * 24 * time.Hour))
		if _, err := tx.ExecContext(ctx, `DELETE FROM job_steps WHERE job_id IN
			(SELECT id FROM jobs WHERE finished_at IS NOT NULL AND finished_at < ?)`, cut); err != nil {
			return 0, 0, err
		}
		r, err := tx.ExecContext(ctx, "DELETE FROM jobs WHERE finished_at IS NOT NULL AND finished_at < ?", cut)
		if err != nil {
			return 0, 0, err
		}
		runs, _ = r.RowsAffected()
	}
	if cfg.EventsDays > 0 {
		cut := storage.Timestamp(now.Add(-time.Duration(cfg.EventsDays) * 24 * time.Hour))
		r, err := tx.ExecContext(ctx, `DELETE FROM events WHERE received_at < ?
			AND seq NOT IN (SELECT event_seq FROM jobs WHERE status IN ('pending', 'running'))`, cut)
		if err != nil {
			return 0, 0, err
		}
		events, _ = r.RowsAffected()
	}
	return events, runs, tx.Commit()
}
