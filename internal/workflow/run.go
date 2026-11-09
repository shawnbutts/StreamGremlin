package workflow

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Step statuses in traces.
const (
	StepOK      = "ok"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// StepRecord is one line of a run's trace.
type StepRecord struct {
	Path     string    `json:"path"`
	StepID   string    `json:"step_id,omitempty"`
	Kind     string    `json:"kind"`
	Action   string    `json:"action,omitempty"`
	Status   string    `json:"status"`
	Reason   string    `json:"reason,omitempty"`
	Error    string    `json:"error,omitempty"`
	Input    any       `json:"input,omitempty"`
	Output   any       `json:"output,omitempty"`
	Started  time.Time `json:"started_at"`
	Finished time.Time `json:"finished_at"`
}

var (
	errBudget = errors.New("the workflow ran more steps than its max_steps allows")
)

// interp runs one job's steps.
type interp struct {
	reg    *Registry
	sc     scope
	dryRun bool
	budget *int // shared with workflows this run calls
	jobID  string
	wfID   string
	actor  *events.Actor // who the event is about, or nil
	vars   *runVars
	eng    *Engine  // for workflow.run
	chain  []string // workflow ids from the job's own down to this one
	now    func() time.Time
	trace  func(StepRecord)
}

func (r *interp) record(rec StepRecord) {
	rec.Finished = r.now()
	if r.trace != nil {
		r.trace(rec)
	}
}

// result stores a step's outcome where later expressions can read it.
func (r *interp) result(id, status string, output any, err string) {
	if id == "" {
		return
	}
	m := map[string]any{"status": status, "output": output}
	if err != "" {
		m["error"] = err
	}
	r.sc.steps[id] = m
}

func (r *interp) run(ctx context.Context, path string, steps []Step) error {
	for i, s := range steps {
		if err := r.step(ctx, fmt.Sprintf("%s[%d]", path, i), s); err != nil {
			return err
		}
	}
	return nil
}

func (r *interp) step(ctx context.Context, path string, s Step) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*r.budget--
	if *r.budget < 0 {
		return errBudget
	}
	rec := StepRecord{Path: path, StepID: s.ID, Kind: s.Kind(), Action: s.Action, Started: r.now()}
	fail := func(err error) error {
		rec.Status, rec.Error = StepFailed, err.Error()
		r.record(rec)
		r.result(s.ID, StepFailed, nil, err.Error())
		if s.OnError == "continue" && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errBudget) {
			return nil
		}
		return err
	}
	if s.If != "" {
		ok, err := evalBool(ctx, s.If, r.sc)
		if err != nil {
			return fail(err)
		}
		if !ok {
			if s.Kind() == "do" && s.Else != nil {
				rec.Status, rec.Reason = StepOK, "if was false: ran else"
				r.record(rec)
				return r.run(ctx, path+".else", s.Else)
			}
			rec.Status, rec.Reason = StepSkipped, "if was false"
			r.record(rec)
			r.result(s.ID, StepSkipped, nil, "")
			return nil
		}
	}
	switch s.Kind() {
	case "action":
		return r.action(ctx, path, s, rec, fail)
	case "set":
		names := make([]string, 0, len(s.Set))
		for n := range s.Set {
			names = append(names, n)
		}
		sort.Strings(names)
		out := map[string]any{}
		for _, n := range names {
			v, err := eval(ctx, s.Set[n], r.sc)
			if err != nil {
				return fail(err)
			}
			r.sc.vars[n] = v
			out[n] = v
		}
		rec.Status, rec.Output = StepOK, capSize(out, 8<<10)
		r.record(rec)
		r.result(s.ID, StepOK, out, "")
		return nil
	case "do":
		rec.Status = StepOK
		r.record(rec)
		if err := r.run(ctx, path+".do", s.Do); err != nil {
			return err
		}
		r.result(s.ID, StepOK, nil, "")
		return nil
	case "foreach":
		v, err := eval(ctx, s.Foreach, r.sc)
		if err != nil {
			return fail(err)
		}
		list, ok := v.([]any)
		if !ok {
			return fail(fmt.Errorf("foreach needs a list, and got %s", describe(v)))
		}
		max := s.Max
		if max == 0 {
			max = DefaultForeachMax
		}
		if len(list) > max {
			return fail(fmt.Errorf("the list has %d items; this foreach allows %d", len(list), max))
		}
		rec.Status, rec.Output = StepOK, map[string]any{"count": len(list)}
		r.record(rec)
		saved := r.sc.each
		defer func() { r.sc.each = saved }()
		for i, item := range list {
			r.sc.each = map[string]any{"item": item, "index": int64(i)}
			if err := r.run(ctx, fmt.Sprintf("%s.steps#%d", path, i), s.Steps); err != nil {
				return err
			}
		}
		r.result(s.ID, StepOK, map[string]any{"count": len(list)}, "")
		return nil
	case "choose":
		total := 0
		for _, b := range s.Choose {
			total += b.Weight
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
		if err != nil {
			return fail(err)
		}
		roll, pick := int(n.Int64()), 0
		for acc := 0; pick < len(s.Choose); pick++ {
			acc += s.Choose[pick].Weight
			if roll < acc {
				break
			}
		}
		out := map[string]any{"branch": int64(pick), "roll": int64(roll), "total": int64(total)}
		rec.Status, rec.Output = StepOK, out
		r.record(rec)
		r.result(s.ID, StepOK, out, "")
		return r.run(ctx, fmt.Sprintf("%s.choose[%d].steps", path, pick), s.Choose[pick].Steps)
	case "wait":
		d, _ := time.ParseDuration(s.Wait)
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-t.C:
		}
		rec.Status = StepOK
		r.record(rec)
		r.result(s.ID, StepOK, nil, "")
		return nil
	}
	return fail(fmt.Errorf("an unknown kind of step"))
}

func (r *interp) action(ctx context.Context, path string, s Step, rec StepRecord, fail func(error) error) error {
	a, ok := r.reg.Get(s.Action)
	if !ok {
		return fail(fmt.Errorf("there's no action %q in this version of StreamGremlin", s.Action))
	}
	args := map[string]any{}
	names := make([]string, 0, len(s.With))
	for n := range s.With {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		raw := s.With[n]
		if versions, ok := oneOf(raw); ok {
			// Several ways to say it: one, at random (docs/adr/0008-workflows.md).
			raw = versions[pickIndex(len(versions))]
		}
		v, err := render(ctx, raw, r.sc)
		if err != nil {
			return fail(fmt.Errorf("%s: %w", n, err))
		}
		args[n] = v
	}
	rec.Input = redact(args)
	if err := checkArgs(a, args); err != nil {
		return fail(err)
	}
	if a.Name == RunWorkflow {
		return r.call(ctx, path, s, rec, args, fail)
	}
	if r.dryRun && a.External && !a.Preview {
		out := map[string]any{"dry_run": true}
		rec.Status, rec.Reason, rec.Output = StepSkipped, "dry run: a test event never reaches "+outside(a), out
		r.record(rec)
		r.result(s.ID, StepSkipped, out, "")
		return nil
	}
	actx := ctx
	if s.Timeout != "" {
		d, _ := time.ParseDuration(s.Timeout)
		var cancel context.CancelFunc
		actx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	out, err := a.Run(actx, Call{Args: args, IdempotencyKey: r.jobID + "/" + path, JobID: r.jobID, WorkflowID: r.wfID, DryRun: r.dryRun, Actor: r.actor, Event: r.sc.event, run: r.vars})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("the step took longer than its %s timeout", s.Timeout)
		}
		return fail(err)
	}
	out = normalizeOutput(out)
	rec.Status, rec.Output = StepOK, capSize(out, 8<<10)
	switch {
	case r.dryRun && a.SavesState:
		rec.Reason = "test run: changed for this run only, not saved"
	case r.dryRun && a.Preview:
		rec.Reason = "test run: never reaches OBS; at most the app's previews (the output says whether one showed it)"
	}
	r.record(rec)
	r.result(s.ID, StepOK, out, "")
	return nil
}

func outside(a *Action) string {
	switch {
	case strings.HasPrefix(a.Name, "obs."):
		return "OBS"
	case strings.HasPrefix(a.Name, "twitch.") || strings.HasPrefix(a.Name, "chat."):
		return "Twitch"
	}
	return "outside services"
}

// normalizeOutput makes an action's output look like expression values.
func normalizeOutput(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return normalize(out)
}

var secretWords = []string{"token", "secret", "password", "passwd", "authorization", "api_key", "apikey", "cookie", "credential", "private_key"}

// redact makes an input safe to keep in a trace: values under secret-sounding
// keys are hidden, long strings cut, and the whole kept small.
func redact(v any) any {
	return capSize(redactValue("", v), 4<<10)
}

func redactValue(key string, v any) any {
	k := strings.ToLower(key)
	for _, w := range secretWords {
		if strings.Contains(k, w) {
			return "[redacted]"
		}
	}
	switch x := v.(type) {
	case string:
		if len(x) > 256 {
			return x[:256] + "…"
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = redactValue("", e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for kk, e := range x {
			out[kk] = redactValue(kk, e)
		}
		return out
	}
	return v
}

// capSize replaces a value too big to keep with a note of its size.
func capSize(v any, limit int) any {
	b, err := json.Marshal(v)
	if err != nil || len(b) <= limit {
		return v
	}
	return map[string]any{"truncated": true, "bytes": len(b)}
}

// MaxVersions is how many ways a message can be said.
const MaxVersions = 50

// oneOf reads a message given as several versions, {"one_of": [...]}, each
// as its raw JSON string.
func oneOf(raw json.RawMessage) ([]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != 1 {
		return nil, false
	}
	list, ok := m["one_of"]
	if !ok {
		return nil, false
	}
	var versions []json.RawMessage
	if json.Unmarshal(list, &versions) != nil || len(versions) == 0 {
		return nil, false
	}
	return versions, true
}

// pickIndex picks 0 to n-1 with a secure random source. It's for variety,
// not prizes: nothing is recorded but the version in the trace.
func pickIndex(n int) int {
	if n <= 1 {
		return 0
	}
	i, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(i.Int64())
}
