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

// ErrGroupNotFound: no workflow is in a group by that name.
var ErrGroupNotFound = errors.New("workflow: no group by that name")

// ErrGroupExists: renaming a group to another group's name.
var ErrGroupExists = errors.New("workflow: another group has that name; move its workflows one by one, or choose another name")

// ErrDeleted: the id belongs to a deleted workflow; restore it to use it.
var ErrDeleted = errors.New("workflow: that id belongs to a deleted workflow; restore it, or use another id")

// Summary describes a workflow for lists.
type Summary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Group is its group ("" for none); GroupEnabled is that group's
	// switch. It runs only when both switches are on (Running).
	Group        string     `json:"group"`
	GroupEnabled bool       `json:"group_enabled"`
	Running      bool       `json:"running"`
	Version      int        `json:"version"`
	Triggers     []Trigger  `json:"triggers"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	LastJob      *Job       `json:"last_job,omitempty"`
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

// save stores a new version when the content changed. A new workflow
// starts on or off as given (D-077); an existing one keeps its switch.
func (s *store) save(ctx context.Context, d *Definition, on bool) (version int, changed bool, err error) {
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflows (id, name, enabled, current_version, created_at, updated_at, grp)
			VALUES (?, ?, ?, 1, ?, ?, ?)`, d.ID, d.Name, on, now, now, d.Group); err != nil {
			return 0, false, err
		}
		version = 1
	case err != nil:
		return 0, false, err
	case curSum == sum:
		return cur, false, nil // the same content: no new version
	default:
		version = cur + 1
		if _, err := tx.ExecContext(ctx, "UPDATE workflows SET name = ?, current_version = ?, updated_at = ?, grp = ? WHERE id = ?",
			d.Name, version, now, d.Group, d.ID); err != nil {
			return 0, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_versions (workflow_id, version, definition, checksum, created_at)
		VALUES (?, ?, ?, ?, ?)`, d.ID, version, string(canonical), sum, now); err != nil {
		return 0, false, err
	}
	if err := tidyGroups(ctx, tx, d.Group, now); err != nil {
		return 0, false, err
	}
	return version, true, tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// tidyGroups makes sure a group in use has its switch (on, when new), and
// forgets the switches of groups no workflow is in any more.
func tidyGroups(ctx context.Context, x execer, inUse, now string) error {
	if inUse != "" {
		if _, err := x.ExecContext(ctx, "INSERT OR IGNORE INTO workflow_groups (name, enabled, updated_at) VALUES (?, 1, ?)", inUse, now); err != nil {
			return err
		}
	}
	_, err := x.ExecContext(ctx, `DELETE FROM workflow_groups WHERE name NOT IN
		(SELECT grp FROM workflows WHERE deleted_at IS NULL AND grp != '')`)
	return err
}

// GroupSummary is one group, for the list.
type GroupSummary struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Workflows int    `json:"workflows"` // in it
	On        int    `json:"on"`        // in it and switched on
}

// groups lists the groups in use, by name.
func (s *store) groups(ctx context.Context) ([]GroupSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.name, g.enabled, count(w.id), coalesce(sum(w.enabled), 0)
		FROM workflow_groups g JOIN workflows w ON w.grp = g.name AND w.deleted_at IS NULL
		GROUP BY g.name ORDER BY g.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupSummary{}
	for rows.Next() {
		var g GroupSummary
		var en int
		if err := rows.Scan(&g.Name, &en, &g.Workflows, &g.On); err != nil {
			return nil, err
		}
		g.Enabled = en == 1
		out = append(out, g)
	}
	return out, rows.Err()
}

// groupName finds a group's name as it's spelled, in any case.
func (s *store) groupName(ctx context.Context, name string) (string, bool, error) {
	var got string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM workflow_groups WHERE name = ?", name).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return got, err == nil, err
}

func (s *store) setGroupEnabled(ctx context.Context, name string, on bool) error {
	res, err := s.db.ExecContext(ctx, "UPDATE workflow_groups SET enabled = ?, updated_at = ? WHERE name = ?", b2i(on), ts(s.now()), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// groupMembers lists the workflows in a group (not deleted).
func (s *store) groupMembers(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM workflows WHERE grp = ? COLLATE NOCASE AND deleted_at IS NULL ORDER BY id", name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
	var grp string
	_ = s.db.QueryRowContext(ctx, "SELECT grp FROM workflows WHERE id = ?", id).Scan(&grp)
	if deleted {
		grp = ""
	}
	return tidyGroups(ctx, s.db, grp, ts(s.now()))
}

// list returns the workflows, or (deleted) only the deleted ones.
func (s *store) list(ctx context.Context, deleted bool) ([]Summary, error) {
	cond := "w.deleted_at IS NULL"
	if deleted {
		cond = "w.deleted_at IS NOT NULL"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id, w.name, w.enabled, w.current_version, w.updated_at, w.deleted_at, v.definition, w.grp, coalesce(g.enabled, 1)
		FROM workflows w JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.current_version
		LEFT JOIN workflow_groups g ON g.name = w.grp
		WHERE `+cond+` ORDER BY w.name, w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var sm Summary
		var en, groupOn int
		var updated, raw string
		var del sql.NullString
		if err := rows.Scan(&sm.ID, &sm.Name, &en, &sm.Version, &updated, &del, &raw, &sm.Group, &groupOn); err != nil {
			return nil, err
		}
		sm.Enabled = en == 1
		sm.GroupEnabled = groupOn == 1
		sm.Running = sm.Enabled && sm.GroupEnabled && !del.Valid
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

// Stats counts workflows and runs for Home.
type Stats struct {
	Total     int  `json:"total"`
	On        int  `json:"on"`
	Tested    bool `json:"tested"`   // a test event has run one
	Answered  bool `json:"answered"` // a real event has run one to the end
	Failed24h int  `json:"failed_24h"`
	// FailedSinceSeen: Failed24h counts only since the streamer marked
	// failures as seen, less than 24 hours ago.
	FailedSinceSeen bool `json:"failed_since_seen,omitempty"`
}

func (s *store) stats(ctx context.Context, failedSince time.Time) (Stats, error) {
	var st Stats
	var on sql.NullInt64
	// On counts the ones that run: switched on, with their group on.
	if err := s.db.QueryRowContext(ctx, `SELECT count(*), sum(w.enabled * coalesce(g.enabled, 1)) FROM workflows w
		LEFT JOIN workflow_groups g ON g.name = w.grp WHERE w.deleted_at IS NULL`).Scan(&st.Total, &on); err != nil {
		return st, err
	}
	st.On = int(on.Int64)
	from := s.now().Add(-24 * time.Hour)
	if failedSince.After(from) {
		from = failedSince
		st.FailedSinceSeen = true
	}
	since := ts(from)
	err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM jobs WHERE dry_run = 1 AND status NOT IN ('pending', 'running')),
		EXISTS (SELECT 1 FROM jobs WHERE dry_run = 0 AND status = 'succeeded'),
		(SELECT count(*) FROM jobs WHERE status = 'failed' AND created_at >= ?)`, since).Scan(&st.Tested, &st.Answered, &st.Failed24h)
	return st, err
}
