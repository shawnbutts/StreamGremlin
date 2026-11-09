package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/variables"
)

// RunWorkflow is the action that runs another workflow's steps inside the
// current run (docs/adr/0010-editor-run-delete.md).
const RunWorkflow = "workflow.run"

// MaxCallDepth is how many workflows deep a chain of calls may go below the
// job's own.
const MaxCallDepth = 4

// registerCoreActions adds the actions the engine itself provides.
func registerCoreActions(reg *Registry) {
	if _, ok := reg.Get(RunWorkflow); ok {
		return // a restarted engine on the same registry
	}
	reg.Add(Action{
		Name: RunWorkflow, Label: "Run another workflow", Group: "Workflows",
		Description: "Runs another workflow's steps now, as part of this run, with the same event. Values passed in are args.<name> there; what it remembered comes back as steps.<id>.output.vars. It must be turned on.",
		Params: []Param{
			{Name: "workflow", Label: "Workflow", Type: "text", Required: true, Literal: true, Editor: "workflow",
				Pattern: idPattern.String(), PatternHint: "a workflow's id, like chat.say-hello"},
			{Name: "args", Label: "Values to pass", Type: "any"},
		},
		// Run is never called: the interpreter runs the steps itself, so
		// they share the run's budget, time limit, cancellation and trace.
		Run: func(context.Context, Call) (any, error) {
			return nil, errors.New("workflow.run only works inside a workflow run")
		},
	})
}

// call runs another workflow's steps as part of this run.
func (r *interp) call(ctx context.Context, path string, s Step, rec StepRecord, args map[string]any, fail func(error) error) error {
	id, _ := args["workflow"].(string)
	pass := map[string]any{}
	if a, ok := args["args"]; ok && a != nil {
		m, isMap := a.(map[string]any)
		if !isMap {
			return fail(fmt.Errorf("the values to pass must be a map of names to values, not %s", describe(a)))
		}
		pass = m
	}
	for _, c := range r.chain {
		if c == id {
			return fail(fmt.Errorf("a loop: %s → %s; a workflow can't run itself, even through others", strings.Join(r.chain, " → "), id))
		}
	}
	if len(r.chain) > MaxCallDepth {
		return fail(fmt.Errorf("workflows calling workflows more than %d deep: %s → %s", MaxCallDepth, strings.Join(r.chain, " → "), id))
	}
	if r.eng == nil {
		return fail(errors.New("workflow.run only works inside a workflow run"))
	}
	d, enabled, err := r.eng.st.callable(ctx, id)
	switch {
	case errors.Is(err, ErrNotFound):
		return fail(fmt.Errorf("there's no workflow %q", id))
	case err != nil:
		return fail(err)
	case !enabled:
		return fail(fmt.Errorf("“%s” is off; turn it on so other workflows can run it", d.Name))
	}

	// Global and viewer variables are shared with the caller; the workflow
	// scope is the called workflow's own.
	rv := &runVars{store: r.vars.store, viewer: r.vars.viewer, wfID: id, dryRun: r.dryRun,
		view: map[string]any{"global": r.vars.view["global"], "viewer": r.vars.view["viewer"], "workflow": map[string]any{}}}
	if rv.store != nil {
		own, err := rv.store.Load(ctx, variables.Workflow, id)
		if err != nil {
			return fail(fmt.Errorf("its saved variables couldn't be read: %w", err))
		}
		rv.view["workflow"] = own
	}
	sub := &interp{
		reg: r.reg, dryRun: r.dryRun, budget: r.budget, jobID: r.jobID, wfID: id, actor: r.actor, vars: rv, eng: r.eng,
		chain: append(append([]string(nil), r.chain...), id), now: r.now, trace: r.trace,
		sc: scope{event: r.sc.event, vars: map[string]any{}, steps: map[string]any{}, job: r.sc.job, saved: rv.view, args: pass},
	}
	if err := sub.run(ctx, path+">"+id+":steps", d.Steps); err != nil {
		if ctx.Err() != nil || errors.Is(err, errBudget) {
			return err // cancelled, timed out or over budget: the whole run stops
		}
		return fail(fmt.Errorf("“%s” failed: %w", d.Name, err))
	}
	out := map[string]any{"workflow": id, "vars": sub.sc.vars}
	rec.Status, rec.Output = StepOK, capSize(out, 8<<10)
	r.record(rec)
	r.result(s.ID, StepOK, out, "")
	return nil
}
