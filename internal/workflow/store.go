package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// ErrNotFound: no workflow or job with that id.
var ErrNotFound = errors.New("workflow: not found")

// ErrDeleted: the id belongs to a deleted workflow; restore it to use it.
var ErrDeleted = errors.New("workflow: that id belongs to a deleted workflow; restore it, or use another id")

// Summary describes a workflow for lists.
type Summary struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	Version   int        `json:"version"`
	Triggers  []Trigger  `json:"triggers"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	LastJob   *Job       `json:"last_job,omitempty"`
}

// Job is one run of a workflow, or the record of why it didn't run.
type Job struct {
	ID         string     `json:"id"`
	WorkflowID string     `json:"workflow_id"`
	Version    int        `json:"version"`
	EventID    string     `json:"event_id"`
	EventSeq   int64      `json:"event_seq"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	DryRun     bool       `json:"dry_run"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Job statuses.
const (
	JobPending     = "pending"
	JobRunning     = "running"
	JobSucceeded   = "succeeded"
	JobFailed      = "failed"
	JobCancelled   = "cancelled"
	JobInterrupted = "interrupted" // StreamGremlin stopped while it ran; never re-run on its own
	JobRejected    = "rejected"    // the queue was full
	JobDropped     = "dropped"     // the drop policy, or drop_oldest
	JobSuperseded  = "superseded"  // the replace policy
)

type store struct {
	db  *storage.DB
	now func() time.Time
}

func ts(t time.Time) string { return storage.Timestamp(t) }

// save stores a new version when the content changed. New workflows start
// off, so a pasted or imported workflow never runs before it's checked.
func (s *store) save(ctx context.Context, d *Definition) (version int, changed bool, err error) {
	canonical, sum := d.Canonical()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var cur int
	var curSum string
	var deleted sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT w.current_version, v.checksum, w.deleted_at FROM workflows w
		JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.current_version
		WHERE w.id = ?`, d.ID).Scan(&cur, &curSum, &deleted)
	if err == nil && deleted.Valid {
		return 0, false, ErrDeleted
	}
	now := ts(s.now())
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflows (id, name, enabled, current_version, created_at, updated_at)
			VALUES (?, ?, 0, 1, ?, ?)`, d.ID, d.Name, now, now); err != nil {
			return 0, false, err
		}
		version = 1
	case err != nil:
		return 0, false, err
	case curSum == sum:
		return cur, false, nil // the same content: no new version
	default:
		version = cur + 1
		if _, err := tx.ExecContext(ctx, "UPDATE workflows SET name = ?, current_version = ?, updated_at = ? WHERE id = ?",
			d.Name, version, now, d.ID); err != nil {
			return 0, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_versions (workflow_id, version, definition, checksum, created_at)
		VALUES (?, ?, ?, ?, ?)`, d.ID, version, string(canonical), sum, now); err != nil {
		return 0, false, err
	}
	return version, true, tx.Commit()
}

func (s *store) setEnabled(ctx context.Context, id string, on bool) error {
	res, err := s.db.ExecContext(ctx, "UPDATE workflows SET enabled = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL", b2i(on), ts(s.now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// definition loads a workflow's version (0 = current).
func (s *store) definition(ctx context.Context, id string, version int) (*Definition, int, bool, error) {
	q := `SELECT v.definition, v.version, w.enabled FROM workflows w JOIN workflow_versions v ON v.workflow_id = w.id
		WHERE w.id = ? AND v.version = CASE WHEN ? = 0 THEN w.current_version ELSE ? END`
	var raw string
	var v, en int
	err := s.db.QueryRowContext(ctx, q, id, version, version).Scan(&raw, &v, &en)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, ErrNotFound
	}
	if err != nil {
		return nil, 0, false, err
	}
	var d Definition
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, 0, false, fmt.Errorf("workflow: stored definition of %s: %w", id, err)
	}
	return &d, v, en == 1, nil
}

// callable loads a workflow for workflow.run: its current definition and
// whether it's on. Deleted workflows aren't found.
func (s *store) callable(ctx context.Context, id string) (*Definition, bool, error) {
	var deleted sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT deleted_at FROM workflows WHERE id = ?", id).Scan(&deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	if deleted.Valid {
		return nil, false, ErrNotFound
	}
	d, _, on, err := s.definition(ctx, id, 0)
	return d, on, err
}

// setDeleted marks a workflow deleted (turning it off) or restores it (off).
func (s *store) setDeleted(ctx context.Context, id string, deleted bool) error {
	q := "UPDATE workflows SET enabled = 0, deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL"
	var at any = ts(s.now())
	if !deleted {
		q = "UPDATE workflows SET enabled = 0, deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL"
		at = nil
	}
	res, err := s.db.ExecContext(ctx, q, at, ts(s.now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// list returns the workflows, or (deleted) only the deleted ones.
func (s *store) list(ctx context.Context, deleted bool) ([]Summary, error) {
	cond := "w.deleted_at IS NULL"
	if deleted {
		cond = "w.deleted_at IS NOT NULL"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id, w.name, w.enabled, w.current_version, w.updated_at, w.deleted_at, v.definition
		FROM workflows w JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.current_version
		WHERE `+cond+` ORDER BY w.name, w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var sm Summary
		var en int
		var updated, raw string
		var del sql.NullString
		if err := rows.Scan(&sm.ID, &sm.Name, &en, &sm.Version, &updated, &del, &raw); err != nil {
			return nil, err
		}
		sm.Enabled = en == 1
		sm.UpdatedAt, _ = storage.ParseTimestamp(updated)
		if del.Valid {
			t, _ := storage.ParseTimestamp(del.String)
			sm.DeletedAt = &t
		}
		var d Definition
		_ = json.Unmarshal([]byte(raw), &d)
		sm.Triggers = d.Triggers
		out = append(out, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if jobs, err := s.jobs(ctx, out[i].ID, 1); err == nil && len(jobs) > 0 {
			out[i].LastJob = &jobs[0]
		}
	}
	return out, nil
}

const selectJob = `SELECT id, workflow_id, version, event_id, event_seq, status, reason, dry_run, error, created_at, started_at, finished_at FROM jobs `

func scanJob(r interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var dry int
	var created string
	var started, finished sql.NullString
	if err := r.Scan(&j.ID, &j.WorkflowID, &j.Version, &j.EventID, &j.EventSeq, &j.Status, &j.Reason, &dry, &j.Error,
		&created, &started, &finished); err != nil {
		return j, err
	}
	j.DryRun = dry == 1
	j.CreatedAt, _ = storage.ParseTimestamp(created)
	for _, p := range []struct {
		src sql.NullString
		dst **time.Time
	}{{started, &j.StartedAt}, {finished, &j.FinishedAt}} {
		if p.src.Valid {
			t, _ := storage.ParseTimestamp(p.src.String)
			*p.dst = &t
		}
	}
	return j, nil
}

// jobs lists the newest jobs, of one workflow or ("") all.
func (s *store) jobs(ctx context.Context, workflowID string, limit int) ([]Job, error) {
	q, args := selectJob+"ORDER BY created_at DESC, rowid DESC LIMIT ?", []any{limit}
	if workflowID != "" {
		q, args = selectJob+"WHERE workflow_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?", []any{workflowID, limit}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *store) job(ctx context.Context, id string) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, selectJob+"WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

func (s *store) steps(ctx context.Context, jobID string) ([]StepRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, step_id, kind, action, status, reason, error, input, output, started_at, finished_at
		FROM job_steps WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StepRecord{}
	for rows.Next() {
		var r StepRecord
		var in, outv sql.NullString
		var st, fin string
		if err := rows.Scan(&r.Path, &r.StepID, &r.Kind, &r.Action, &r.Status, &r.Reason, &r.Error, &in, &outv, &st, &fin); err != nil {
			return nil, err
		}
		if in.Valid {
			_ = json.Unmarshal([]byte(in.String), &r.Input)
		}
		if outv.Valid {
			_ = json.Unmarshal([]byte(outv.String), &r.Output)
		}
		r.Started, _ = storage.ParseTimestamp(st)
		r.Finished, _ = storage.ParseTimestamp(fin)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) addStep(ctx context.Context, jobID string, r StepRecord) error {
	jsonOrNil := func(v any) any {
		if v == nil {
			return nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(b)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_steps (job_id, path, step_id, kind, action, status, reason, error, input, output, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobID, r.Path, r.StepID, r.Kind, r.Action, r.Status, r.Reason, r.Error,
		jsonOrNil(r.Input), jsonOrNil(r.Output), ts(r.Started), ts(r.Finished))
	return err
}

func (s *store) finish(ctx context.Context, id, status, errText string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ? AND status IN ('pending', 'running')",
		status, errText, ts(s.now()), id)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
