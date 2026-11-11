package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Save parses and stores a definition. Unchanged content keeps its version.
// A new workflow starts turned on (D-077), unless a chat command it answers
// is already answered by a workflow that's on: then it starts off, and
// Taken says why.
func (e *Engine) Save(ctx context.Context, raw []byte) (Summary, bool, error) {
	d, err := Parse(raw, e.reg)
	if err != nil {
		return Summary{}, false, err
	}
	// The check and the save happen together, so two changes can't both
	// take one command.
	e.cmdMu.Lock()
	defer e.cmdMu.Unlock()
	if err := e.canonicalGroup(ctx, d); err != nil {
		return Summary{}, false, err
	}
	// Only a workflow that will run must keep its commands free: on, and
	// in a group that's on (a move into another group counts).
	prev, _, on, err := e.st.definition(ctx, d.ID, 0)
	fresh := errors.Is(err, ErrNotFound)
	if err != nil && !fresh {
		return Summary{}, false, err
	}
	// A new workflow starts on when nothing that's on answers its commands.
	startOn := fresh && (!e.groupOn(ctx, d.Group) || e.checkCommands(d.ID, d, nil) == nil)
	if err == nil && on && e.groupOn(ctx, d.Group) {
		if !e.groupOn(ctx, prev.Group) {
			prev = nil // it wasn't running: everything it answers is new
		}
		if err := e.checkCommands(d.ID, d, prev); err != nil {
			return Summary{}, false, err
		}
	}
	version, changed, err := e.st.save(ctx, d, startOn)
	if err != nil {
		return Summary{}, false, err
	}
	if changed {
		e.log.Info("workflow saved", "workflow", d.ID, "version", version, "new", fresh, "on", fresh && startOn)
		if err := e.reload(ctx); err != nil {
			return Summary{}, false, err
		}
	}
	s, err := e.Get(ctx, d.ID)
	return s, changed, err
}

// Taken reports why a workflow can't be turned on: a *commands.TakenError
// when a chat command it answers is answered by a workflow that's on, or
// nil. A new workflow that started off says why with it.
func (e *Engine) Taken(ctx context.Context, id string) error {
	e.cmdMu.Lock()
	defer e.cmdMu.Unlock()
	d, _, _, err := e.st.definition(ctx, id, 0)
	if err != nil {
		return err
	}
	if !e.groupOn(ctx, d.Group) {
		return nil
	}
	return e.checkCommands(id, d, nil)
}

// Get returns one workflow's summary. Deleted workflows aren't found.
func (e *Engine) Get(ctx context.Context, id string) (Summary, error) {
	list, err := e.st.list(ctx, false)
	if err != nil {
		return Summary{}, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
	}
	return Summary{}, ErrNotFound
}

// List returns every workflow, or (deleted) the deleted ones.
func (e *Engine) List(ctx context.Context, deleted bool) ([]Summary, error) {
	return e.st.list(ctx, deleted)
}

// Definition returns a workflow's current definition, deleted or not.
func (e *Engine) Definition(ctx context.Context, id string) (*Definition, int, error) {
	d, v, _, err := e.st.definition(ctx, id, 0)
	return d, v, err
}

var errDeleted = errors.New("the workflow was deleted")

// Delete turns a workflow off and takes it out of the list and every
// lookup. Its waiting runs are cancelled and running ones stopped (their
// finally steps run). Versions, runs and its own saved variables are kept,
// for Restore.
func (e *Engine) Delete(ctx context.Context, id string) error {
	if err := e.st.setDeleted(ctx, id, true); err != nil {
		return err
	}
	e.mu.Lock()
	q := e.queue(id)
	pending := append([]string(nil), q.pending...)
	q.pending = nil
	for _, cancel := range q.running {
		cancel(errDeleted)
	}
	e.mu.Unlock()
	for _, j := range pending {
		_, _ = e.st.db.ExecContext(ctx, "UPDATE jobs SET status = 'cancelled', reason = ?, finished_at = ? WHERE id = ? AND status = 'pending'",
			errDeleted.Error(), ts(e.st.now()), j)
	}
	e.log.Info("workflow deleted", "workflow", id)
	if err := e.reload(ctx); err != nil {
		return err
	}
	e.notify.Notify()
	return nil
}

// Restore brings a deleted workflow back, turned off.
func (e *Engine) Restore(ctx context.Context, id string) error {
	if err := e.st.setDeleted(ctx, id, false); err != nil {
		return err
	}
	e.log.Info("workflow restored", "workflow", id)
	e.notify.Notify()
	return nil
}

// SetEnabled turns a workflow on or off. Turning it off cancels its runs
// that are still waiting; ones already running finish.
func (e *Engine) SetEnabled(ctx context.Context, id string, on bool) error {
	e.cmdMu.Lock()
	defer e.cmdMu.Unlock()
	if on {
		// Turning on: each command it answers must be free, if it will
		// run. In a group that's off it won't, until the group comes on,
		// and that's checked then.
		d, _, _, err := e.st.definition(ctx, id, 0)
		if err != nil {
			return err
		}
		if e.groupOn(ctx, d.Group) {
			if err := e.checkCommands(id, d, nil); err != nil {
				return err
			}
		}
	}
	if err := e.st.setEnabled(ctx, id, on); err != nil {
		return err
	}
	if !on {
		e.cancelWaiting(ctx, id)
	}
	e.log.Info("workflow turned "+map[bool]string{true: "on", false: "off"}[on], "workflow", id)
	if err := e.reload(ctx); err != nil {
		return err
	}
	e.notify.Notify()
	return nil
}

// Jobs lists the newest runs, of one workflow or all.
func (e *Engine) Jobs(ctx context.Context, workflowID string, limit int) ([]Job, error) {
	return e.st.jobs(ctx, workflowID, limit)
}

// Trace returns a job and its steps.
func (e *Engine) Trace(ctx context.Context, id string) (Job, []StepRecord, error) {
	j, err := e.st.job(ctx, id)
	if err != nil {
		return j, nil, err
	}
	steps, err := e.st.steps(ctx, id)
	return j, steps, err
}

// ErrFinished: the job already ended.
var ErrFinished = errors.New("workflow: that run already finished")

// Cancel stops a waiting or running job. A running one runs its finally
// steps first.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	j, err := e.st.job(ctx, id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	q := e.queue(j.WorkflowID)
	if cancel, ok := q.running[id]; ok {
		cancel(errCancelled)
		e.mu.Unlock()
		return nil
	}
	waiting := false
	for _, p := range q.pending {
		if p == id {
			waiting = true
		}
	}
	q.pending = remove(q.pending, id)
	e.mu.Unlock()
	if !waiting {
		return fmt.Errorf("%w (%s)", ErrFinished, j.Status)
	}
	err = e.st.finish(ctx, id, JobCancelled, "")
	e.notify.Notify()
	return err
}

// TraceErrors counts trace rows that couldn't be written.
func (e *Engine) TraceErrors() int64 { return e.traceErrors.Load() }

// Stats counts workflows and runs for Home.
func (e *Engine) Stats(ctx context.Context) (Stats, error) { return e.st.stats(ctx, time.Time{}) }

// StatsSince is Stats, counting failed runs only since seen (when the
// streamer marked them as seen) if that's within the last 24 hours.
func (e *Engine) StatsSince(ctx context.Context, seen time.Time) (Stats, error) {
	return e.st.stats(ctx, seen)
}

// UsingActions are the workflows (not deleted) with a step whose action
// starts with prefix, when it ends in "." (a plugin's "<id>."), or is
// exactly prefix (one of its steps).
func (e *Engine) UsingActions(ctx context.Context, prefix string) ([]Summary, error) {
	all, err := e.st.list(ctx, false)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, w := range all {
		d, _, err := e.Definition(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		if usesAction(d.Steps, prefix) || usesAction(d.Finally, prefix) {
			out = append(out, w)
		}
	}
	return out, nil
}

// UsingTriggers are the workflows (not deleted) with a trigger whose type
// starts with prefix: a plugin's events, "plugin.<id>." (D-073).
func (e *Engine) UsingTriggers(ctx context.Context, prefix string) ([]Summary, error) {
	all, err := e.st.list(ctx, false)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, w := range all {
		d, _, err := e.Definition(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range d.Triggers {
			if strings.HasPrefix(t.Type, prefix) {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

// matchAction: a prefix ending in "." matches every action under it,
// anything else one action exactly.
func matchAction(action, prefix string) bool {
	if strings.HasSuffix(prefix, ".") {
		return strings.HasPrefix(action, prefix)
	}
	return action == prefix
}

func usesAction(steps []Step, prefix string) bool {
	for _, s := range steps {
		if matchAction(s.Action, prefix) || usesAction(s.Do, prefix) || usesAction(s.Else, prefix) || usesAction(s.Steps, prefix) {
			return true
		}
		for _, b := range s.Choose {
			if usesAction(b.Steps, prefix) {
				return true
			}
		}
	}
	return false
}

// Starting returns the running workflows an event starts: on, in a group
// that's on, with a trigger that matches it. The Live screen says which a
// button press started (D-075).
func (e *Engine) Starting(ctx context.Context, r events.Record) ([]Summary, error) {
	all, err := e.st.list(ctx, false)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	_ = json.Unmarshal(r.Payload, &payload)
	out := []Summary{}
	for _, w := range all {
		if !w.Running {
			continue
		}
		for _, t := range w.Triggers {
			if t.matches(r, payload) {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}
