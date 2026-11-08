package workflow

import (
	"context"
	"errors"
	"fmt"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/variables"
)

// VarStore is what workflows need of saved variables
// (docs/adr/0009-variables.md).
type VarStore interface {
	Load(ctx context.Context, scope variables.Scope, owner string) (map[string]any, error)
	Set(ctx context.Context, k variables.Key, value any, c variables.Change) (any, error)
	Add(ctx context.Context, k variables.Key, amount any, c variables.Change) (any, any, error)
	Delete(ctx context.Context, k variables.Key) (bool, error)
}

// runVars is one run's view of its saved variables: read when the run
// starts, and changed by the run's own saves, so `saved.*` reflects them.
// Saves by other runs meanwhile show up at the next run; vars.add always
// adds to the stored value, so counting stays exact.
type runVars struct {
	store  VarStore
	view   map[string]any // "global", "viewer", "workflow" -> map[string]any
	viewer *events.Actor
	wfID   string
	dryRun bool
}

func viewerOwner(a *events.Actor) (owner, label string, ok bool) {
	if a == nil || a.Platform == "" || a.ID == "" {
		return "", "", false
	}
	label = a.DisplayName
	if label == "" {
		label = a.Login
	}
	return a.Platform + ":" + a.ID, label, true
}

// loadRunVars reads the three sets a run can see.
func loadRunVars(ctx context.Context, st VarStore, wfID string, actor *events.Actor, dryRun bool) (*runVars, error) {
	rv := &runVars{store: st, viewer: actor, wfID: wfID, dryRun: dryRun,
		view: map[string]any{"global": map[string]any{}, "viewer": map[string]any{}, "workflow": map[string]any{}}}
	if st == nil {
		return rv, nil
	}
	var err error
	if rv.view["global"], err = st.Load(ctx, variables.Global, ""); err != nil {
		return nil, err
	}
	if rv.view["workflow"], err = st.Load(ctx, variables.Workflow, wfID); err != nil {
		return nil, err
	}
	if owner, _, ok := viewerOwner(actor); ok {
		if rv.view["viewer"], err = st.Load(ctx, variables.Viewer, owner); err != nil {
			return nil, err
		}
	}
	return rv, nil
}

var errNoViewer = errors.New("this event has no viewer, so there are no viewer variables to use")

// key resolves a call's scope and name to a stored variable.
func (rv *runVars) key(c Call) (variables.Key, variables.Change, map[string]any, error) {
	scope, _ := c.String("scope", "")
	name, _ := c.String("name", "")
	k := variables.Key{Scope: variables.Scope(scope), Name: name}
	ch := variables.Change{By: "workflow:" + rv.wfID}
	switch k.Scope {
	case variables.Viewer:
		owner, label, ok := viewerOwner(rv.viewer)
		if !ok {
			return k, ch, nil, errNoViewer
		}
		k.Owner, ch.OwnerLabel = owner, label
	case variables.Workflow:
		k.Owner = rv.wfID
	}
	if err := k.Check(); err != nil {
		return k, ch, nil, errors.Unwrap(err)
	}
	view, _ := rv.view[scope].(map[string]any)
	return k, ch, view, nil
}

func (c Call) vars() (*runVars, error) {
	if c.run == nil || c.run.store == nil {
		return nil, errors.New("saved variables are only available in a workflow run")
	}
	return c.run, nil
}

// clean turns a variables error into words for the trace.
func clean(err error) error {
	if errors.Is(err, variables.ErrInvalid) || errors.Is(err, variables.ErrFull) {
		msg := err.Error()
		for _, p := range []string{"variables: invalid: ", "variables: too many variables: "} {
			if len(msg) > len(p) && msg[:len(p)] == p {
				msg = msg[len(p):]
			}
		}
		return errors.New(msg)
	}
	return err
}

// registerVariableActions adds vars.set, vars.add and vars.delete.
func registerVariableActions(reg *Registry) {
	if _, ok := reg.Get("vars.set"); ok {
		return // a restarted engine on the same registry
	}
	scope := Param{Name: "scope", Label: "Kept for", Type: "choice", Required: true,
		Choices: []string{string(variables.Global), string(variables.Viewer), string(variables.Workflow)}}
	name := Param{Name: "name", Label: "Name", Type: "text", Required: true, Editor: "variable",
		Pattern: variables.NamePattern.String(), PatternHint: variables.NameHint}
	const group = "Variables"
	reg.Add(Action{
		Name: "vars.set", Label: "Save a variable", Group: group, SavesState: true,
		Description: "Keeps a value between runs and restarts: for every workflow (global), for the viewer who caused the event (viewer), or for this workflow only (workflow). Read it as saved.global.<name>, saved.viewer.<name> or saved.workflow.<name>.",
		Params:      []Param{scope, name, {Name: "value", Label: "Value", Type: "any", Required: true, Editor: "value"}},
		Run: func(ctx context.Context, c Call) (any, error) {
			rv, err := c.vars()
			if err != nil {
				return nil, err
			}
			k, ch, view, err := rv.key(c)
			if err != nil {
				return nil, err
			}
			value := c.Args["value"]
			prev := view[k.Name]
			if !rv.dryRun {
				if prev, err = rv.store.Set(ctx, k, value, ch); err != nil {
					return nil, clean(err)
				}
			}
			view[k.Name] = value
			return map[string]any{"value": value, "previous": prev, "saved": !rv.dryRun}, nil
		},
	})
	reg.Add(Action{
		Name: "vars.add", Label: "Add to a saved number", Group: group, SavesState: true,
		Description: "Adds to a saved number (a missing one counts as 0), all at once, so runs counting at the same time never lose a count. Use a negative amount to subtract. The new value is steps.<id>.output.value.",
		Params:      []Param{scope, name, {Name: "amount", Label: "Amount", Type: "number", Default: int64(1)}},
		Run: func(ctx context.Context, c Call) (any, error) {
			rv, err := c.vars()
			if err != nil {
				return nil, err
			}
			k, ch, view, err := rv.key(c)
			if err != nil {
				return nil, err
			}
			amount, ok := c.Args["amount"]
			if !ok || amount == nil {
				amount = int64(1)
			}
			var prev, value any
			if rv.dryRun {
				prev = view[k.Name]
				if value, err = variables.Sum(prev, amount); err != nil {
					return nil, fmt.Errorf("%s %w", k.Name, err)
				}
			} else if prev, value, err = rv.store.Add(ctx, k, amount, ch); err != nil {
				return nil, clean(err)
			}
			view[k.Name] = value
			return map[string]any{"value": value, "previous": prev, "saved": !rv.dryRun}, nil
		},
	})
	reg.Add(Action{
		Name: "vars.delete", Label: "Forget a saved variable", Group: group, SavesState: true, More: true,
		Description: "Removes a saved variable. Forgetting one that isn't there is fine.",
		Params:      []Param{scope, name},
		Run: func(ctx context.Context, c Call) (any, error) {
			rv, err := c.vars()
			if err != nil {
				return nil, err
			}
			k, _, view, err := rv.key(c)
			if err != nil {
				return nil, err
			}
			_, existed := view[k.Name]
			if !rv.dryRun {
				if existed, err = rv.store.Delete(ctx, k); err != nil {
					return nil, clean(err)
				}
			}
			delete(view, k.Name)
			return map[string]any{"existed": existed, "saved": !rv.dryRun}, nil
		},
	})
}
