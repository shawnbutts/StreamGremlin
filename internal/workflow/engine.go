package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// maxRunning caps jobs running at once across every workflow.
const maxRunning = 32

var (
	errCancelled = errors.New("cancelled")
	errReplaced  = errors.New("a newer event replaced it (replace queue)")
	errTurnedOff = errors.New("the workflow was turned off")
	errStopping  = errors.New("StreamGremlin is stopping")
)

// Options configure an Engine.
type Options struct {
	DB      *storage.DB
	Inbox   *events.Inbox
	Actions *Registry
	// Variables, when set, gives workflows saved variables and the
	// vars.* actions.
	Variables VarStore
	Logger    *slog.Logger
}

type enabledWF struct {
	id      string
	version int
	def     *Definition
}

type wq struct {
	pending []string                           // job ids, oldest first
	running map[string]context.CancelCauseFunc // job id -> cancel
}

// Engine dispatches events to workflows and runs their jobs.
type Engine struct {
	st     store
	inbox  *events.Inbox
	reg    *Registry
	vars   VarStore
	log    *slog.Logger
	notify *events.Notifier

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	cmdMu   sync.Mutex // one answer per command: checks and the changes they allow
	mu      sync.Mutex
	enabled []enabledWF
	queues  map[string]*wq
	running int
	cursor  int64

	traceErrors atomic.Int64
}

// New returns an Engine. Start begins dispatching.
func New(opts Options) *Engine {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, stop := context.WithCancel(context.Background())
	registerCoreActions(opts.Actions)
	if opts.Variables != nil {
		registerVariableActions(opts.Actions)
	}
	return &Engine{
		st: store{db: opts.DB, now: time.Now}, inbox: opts.Inbox, reg: opts.Actions, vars: opts.Variables,
		log: opts.Logger.With("component", "workflows"), notify: events.NewNotifier(),
		ctx: ctx, stop: stop, queues: map[string]*wq{},
	}
}

// Actions is the registry workflows use.
func (e *Engine) Actions() *Registry { return e.reg }

// Changes is closed at the next job change.
func (e *Engine) Changes() <-chan struct{} { return e.notify.C() }

// Start recovers from the last run and begins dispatching. Jobs that were
// running when StreamGremlin stopped are marked interrupted, never re-run:
// they may already have done things outside. On the very first start the
// dispatcher begins at the newest event, so old events don't run.
func (e *Engine) Start(ctx context.Context) error {
	db := e.st.db
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET status = 'interrupted', reason = 'StreamGremlin stopped while it ran',
		finished_at = ? WHERE status = 'running'`, ts(e.st.now())); err != nil {
		return fmt.Errorf("workflow: mark interrupted jobs: %w", err)
	}
	err := db.QueryRowContext(ctx, "SELECT cursor FROM workflow_dispatch WHERE id = 1").Scan(&e.cursor)
	if errors.Is(err, sql.ErrNoRows) {
		head, herr := e.inbox.Head(ctx)
		if herr != nil {
			return herr
		}
		e.cursor = head
		_, err = db.ExecContext(ctx, "INSERT INTO workflow_dispatch (id, cursor) VALUES (1, ?)", head)
	}
	if err != nil {
		return fmt.Errorf("workflow: dispatch cursor: %w", err)
	}
	if err := e.reload(ctx); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, "SELECT id, workflow_id FROM jobs WHERE status = 'pending' ORDER BY created_at, rowid")
	if err != nil {
		return err
	}
	e.mu.Lock()
	for rows.Next() {
		var id, wf string
		if err := rows.Scan(&id, &wf); err == nil {
			e.queue(wf).pending = append(e.queue(wf).pending, id)
		}
	}
	_ = rows.Close()
	e.schedule()
	e.mu.Unlock()
	e.wg.Add(1)
	go e.dispatchLoop()
	return nil
}

// Close stops dispatching and cancels running jobs, which run their finally
// steps and stop as interrupted at the next start if they can't finish.
func (e *Engine) Close() {
	e.mu.Lock()
	for _, q := range e.queues {
		for _, cancel := range q.running {
			cancel(errStopping)
		}
	}
	e.mu.Unlock()
	e.stop()
	e.wg.Wait()
}

func (e *Engine) queue(id string) *wq {
	q := e.queues[id]
	if q == nil {
		q = &wq{running: map[string]context.CancelCauseFunc{}}
		e.queues[id] = q
	}
	return q
}

// reload reads the current definitions of the workflows that run: on,
// with their group on (docs/adr/0020-workflow-groups.md). This is the one
// place that decides; command checks use the same set.
func (e *Engine) reload(ctx context.Context) error {
	rows, err := e.st.db.QueryContext(ctx, `SELECT w.id FROM workflows w LEFT JOIN workflow_groups g ON g.name = w.grp
		WHERE w.enabled = 1 AND w.deleted_at IS NULL AND coalesce(g.enabled, 1) = 1 ORDER BY w.id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	_ = rows.Close()
	var list []enabledWF
	for _, id := range ids {
		d, v, _, err := e.st.definition(ctx, id, 0)
		if err != nil {
			e.log.Warn("workflow can't be loaded; it won't run", "workflow", id, "err", err)
			continue
		}
		list = append(list, enabledWF{id: id, version: v, def: d})
	}
	e.mu.Lock()
	e.enabled = list
	e.mu.Unlock()
	return nil
}

func (e *Engine) dispatchLoop() {
	defer e.wg.Done()
	for e.ctx.Err() == nil {
		e.mu.Lock()
		cursor := e.cursor
		e.mu.Unlock()
		recs, err := e.inbox.Wait(e.ctx, cursor, 100, nil, 30*time.Second)
		if err != nil {
			if e.ctx.Err() != nil {
				return
			}
			e.log.Warn("workflow dispatch: couldn't read events", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for _, rec := range recs {
			if err := e.dispatch(e.ctx, rec); err != nil {
				if e.ctx.Err() != nil {
					return
				}
				e.log.Warn("workflow dispatch failed; trying again", "seq", rec.Seq, "err", err)
				time.Sleep(time.Second)
				break
			}
		}
	}
}

type decision struct {
	wf       enabledWF
	status   string // pending, dropped or rejected for the new job
	reason   string
	jobID    string
	drop     []string // older pending jobs to mark dropped
	supersed []string // older pending jobs to mark superseded
	cancel   bool     // replace: cancel the running job
}

// dispatch decides, in one transaction with the cursor's move, what every
// enabled workflow does with one event. Every decision is a job row, so
// "why didn't it run?" always has an answer.
func (e *Engine) dispatch(ctx context.Context, rec events.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	payload := map[string]any{}
	_ = json.Unmarshal(rec.Payload, &payload)
	normalize(payload)
	var ds []decision
	for _, wf := range e.enabled {
		if !wf.def.matches(rec, payload) {
			continue
		}
		ds = append(ds, e.decide(wf))
	}
	tx, err := e.st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := ts(e.st.now())
	dry := rec.DeliveryQuality == events.Synthetic
	inserted := map[string]bool{}
	for i := range ds {
		d := &ds[i]
		res, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, workflow_id, version, event_id, event_seq, status, reason, dry_run, created_at, finished_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CASE WHEN ? = 'pending' THEN NULL ELSE ? END)
			ON CONFLICT (workflow_id, event_id) DO NOTHING`,
			d.jobID, d.wf.id, d.wf.version, rec.EventID, rec.Seq, d.status, d.reason, b2i(dry), now, d.status, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			inserted[d.jobID] = true
		}
		for _, id := range d.drop {
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET status = 'dropped', reason = ?, finished_at = ? WHERE id = ? AND status = 'pending'",
				"the queue was full; a newer event took its place", now, id); err != nil {
				return err
			}
		}
		for _, id := range d.supersed {
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET status = 'superseded', reason = ?, finished_at = ? WHERE id = ? AND status = 'pending'",
				"a newer event replaced it (replace queue)", now, id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE workflow_dispatch SET cursor = ? WHERE id = 1", rec.Seq); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Committed: now change the queues to match.
	e.cursor = rec.Seq
	for _, d := range ds {
		q := e.queue(d.wf.id)
		q.pending = remove(q.pending, d.drop...)
		q.pending = remove(q.pending, d.supersed...)
		if d.cancel {
			for _, cancel := range q.running {
				cancel(errReplaced)
			}
		}
		if d.status == JobPending && inserted[d.jobID] {
			q.pending = append(q.pending, d.jobID)
		}
	}
	if len(ds) > 0 {
		e.schedule()
		e.notify.Notify()
	}
	return nil
}

// decide applies the workflow's queue policy to a new event. Call with mu held.
func (e *Engine) decide(wf enabledWF) decision {
	q := e.queue(wf.id)
	d := decision{wf: wf, status: JobPending, jobID: events.NewID()}
	pol := wf.def.Queue
	max := pol.MaxPending
	if max == 0 {
		max = 100
	}
	switch pol.Policy {
	case QueueDrop:
		if len(q.running) > 0 || len(q.pending) > 0 {
			d.status, d.reason = JobDropped, "the workflow was already running (drop queue)"
		}
		return d
	case QueueReplace:
		d.supersed = append([]string(nil), q.pending...)
		d.cancel = len(q.running) > 0
		return d
	}
	if len(q.pending) >= max {
		if pol.OnOverflow == "drop_oldest" {
			d.drop = []string{q.pending[0]}
		} else {
			d.status, d.reason = JobRejected, fmt.Sprintf("%d runs were already waiting (max_pending)", max)
		}
	}
	return d
}

func remove(list []string, ids ...string) []string {
	if len(ids) == 0 {
		return list
	}
	out := list[:0]
	for _, x := range list {
		keep := true
		for _, id := range ids {
			if x == id {
				keep = false
			}
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}

// schedule starts waiting jobs where their queue and the global cap allow.
// Call with mu held.
func (e *Engine) schedule() {
	for wfID, q := range e.queues {
		limit := 1
		for _, wf := range e.enabled {
			if wf.id == wfID && wf.def.Queue.Policy == QueueParallel {
				limit = wf.def.Queue.Concurrency
			}
		}
		for len(q.running) < limit && len(q.pending) > 0 && e.running < maxRunning {
			id := q.pending[0]
			q.pending = q.pending[1:]
			ctx, cancel := context.WithCancelCause(e.ctx)
			q.running[id] = cancel
			e.running++
			e.wg.Add(1)
			go e.runJob(ctx, wfID, id)
		}
	}
}

// runJob runs one job and records how it ended.
func (e *Engine) runJob(ctx context.Context, wfID, id string) {
	defer e.wg.Done()
	status, errText := e.execute(ctx, id)
	if status != "" {
		if err := e.st.finish(context.WithoutCancel(ctx), id, status, errText); err != nil {
			e.log.Warn("couldn't record a job's end", "job", id, "err", err)
		}
	}
	e.mu.Lock()
	q := e.queue(wfID)
	if cancel, ok := q.running[id]; ok {
		cancel(nil)
		delete(q.running, id)
		e.running--
	}
	e.schedule()
	e.mu.Unlock()
	e.notify.Notify()
	e.log.Info("workflow run finished", "workflow", wfID, "job", id, "status", status)
}

func (e *Engine) execute(ctx context.Context, id string) (status, errText string) {
	bg := context.WithoutCancel(ctx)
	res, err := e.st.db.ExecContext(bg, "UPDATE jobs SET status = 'running', started_at = ? WHERE id = ? AND status = 'pending'",
		ts(e.st.now()), id)
	if err != nil {
		return JobFailed, err.Error()
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", "" // cancelled while waiting
	}
	e.notify.Notify()
	job, err := e.st.job(bg, id)
	if err != nil {
		return JobFailed, err.Error()
	}
	def, _, _, err := e.st.definition(bg, job.WorkflowID, job.Version)
	if err != nil {
		return JobFailed, err.Error()
	}
	rec, err := e.inbox.Get(bg, job.EventID)
	if err != nil {
		return JobFailed, "its event couldn't be read: " + err.Error()
	}
	rv, err := loadRunVars(bg, e.vars, job.WorkflowID, rec.Actor, job.DryRun)
	if err != nil {
		return JobFailed, "its saved variables couldn't be read: " + err.Error()
	}
	sc := scope{
		event: eventMap(rec), vars: map[string]any{}, steps: map[string]any{}, saved: rv.view,
		job: map[string]any{"id": job.ID, "workflow_id": job.WorkflowID, "workflow_version": int64(job.Version), "dry_run": job.DryRun},
	}
	trace := func(r StepRecord) {
		if err := e.st.addStep(bg, id, r); err != nil {
			// Tracing must never change what a workflow does.
			e.traceErrors.Add(1)
			e.log.Warn("couldn't record a trace step", "job", id, "err", err)
		}
	}
	budget := def.StepBudget()
	in := &interp{reg: e.reg, sc: sc, dryRun: job.DryRun, budget: &budget, jobID: id, wfID: job.WorkflowID, actor: rec.Actor, vars: rv, now: e.st.now,
		trace: trace, eng: e, chain: []string{job.WorkflowID}}
	rctx, cancel := context.WithTimeout(ctx, def.TimeoutDuration())
	runErr := in.run(rctx, "steps", def.Steps)
	timedOut := errors.Is(rctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()
	if len(def.Finally) > 0 {
		// Finally always runs, with its own short budget, so a cancelled or
		// failed run can still clean up (switch a scene back, say).
		fctx, fcancel := context.WithTimeout(bg, FinallyBudget)
		fin := *in
		finBudget := DefaultMaxSteps
		fin.budget = &finBudget
		if ferr := fin.run(fctx, "finally", def.Finally); ferr != nil && runErr == nil {
			runErr = fmt.Errorf("finally: %w", ferr)
		}
		fcancel()
	}
	switch {
	case runErr == nil:
		return JobSucceeded, ""
	case timedOut:
		return JobFailed, fmt.Sprintf("took longer than the workflow's %s limit", def.TimeoutDuration())
	case ctx.Err() != nil:
		cause := context.Cause(ctx)
		if errors.Is(cause, errStopping) {
			return JobInterrupted, cause.Error()
		}
		return JobCancelled, cause.Error()
	}
	return JobFailed, runErr.Error()
}

// eventMap is what expressions see as event.
func eventMap(r events.Record) map[string]any {
	var payload any = map[string]any{}
	_ = json.Unmarshal(r.Payload, &payload)
	m := map[string]any{
		"id": r.EventID, "seq": r.Seq, "type": r.Type, "source": r.Source, "source_event_id": r.SourceEventID,
		"channel_id": r.ChannelID, "delivery_quality": string(r.DeliveryQuality),
		"occurred_at": r.OccurredAt.UTC().Format(time.RFC3339Nano), "payload": normalize(payload),
	}
	if r.Actor != nil {
		m["actor"] = map[string]any{"platform": r.Actor.Platform, "id": r.Actor.ID, "login": r.Actor.Login, "display_name": r.Actor.DisplayName}
	} else {
		m["actor"] = nil
	}
	return m
}

// matches reports whether any trigger accepts the event.
func (d *Definition) matches(r events.Record, payload map[string]any) bool {
	for _, t := range d.Triggers {
		if t.matches(r, payload) {
			return true
		}
	}
	return false
}

func (t Trigger) matches(r events.Record, payload map[string]any) bool {
	if !typeMatches(t.Type, r.Type) {
		return false
	}
	if len(t.Sources) > 0 && !contains(t.Sources, r.Source) {
		return false
	}
	if len(t.DeliveryQuality) > 0 {
		ok := false
		for _, q := range t.DeliveryQuality {
			if q == r.DeliveryQuality {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	for path, raw := range t.Where {
		var want any
		if json.Unmarshal(raw, &want) != nil {
			return false
		}
		got, ok := lookup(payload, path)
		if !ok || !whereMatches(normalize(want), got) {
			return false
		}
	}
	return true
}

// whereMatches compares one where value: exactly, or any value of a list.
func whereMatches(want, got any) bool {
	if list, ok := want.([]any); ok {
		for _, w := range list {
			if reflect.DeepEqual(w, got) {
				return true
			}
		}
		return false
	}
	return reflect.DeepEqual(want, got)
}

func typeMatches(pattern, typ string) bool {
	if pattern == typ {
		return true
	}
	p, t := strings.Split(pattern, "."), strings.Split(typ, ".")
	for i, seg := range p {
		if seg == "**" && i == len(p)-1 {
			return len(t) > i
		}
		if i >= len(t) || (seg != "*" && seg != t[i]) {
			return false
		}
	}
	return len(p) == len(t)
}

func lookup(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, k := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = mm[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
