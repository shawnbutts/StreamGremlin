package workflow

import (
	"context"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/commands"
)

// Workflow groups (docs/adr/0020-workflow-groups.md): a workflow is in at
// most one group, named in its definition. A group's switch leaves each
// workflow's own alone; a workflow runs only when both are on.

// Groups lists the groups in use, with how many workflows each has and
// how many of those are switched on.
func (e *Engine) Groups(ctx context.Context) ([]GroupSummary, error) { return e.st.groups(ctx) }

// canonicalGroup spells a group as it already exists, in any case, so
// "alerts" joins "Alerts" instead of making a second group.
func (e *Engine) canonicalGroup(ctx context.Context, d *Definition) error {
	if d.Group == "" {
		return nil
	}
	name, ok, err := e.st.groupName(ctx, d.Group)
	if err == nil && ok {
		d.Group = name
	}
	return err
}

// groupOn reports whether a workflow's group lets it run.
func (e *Engine) groupOn(ctx context.Context, group string) bool {
	if group == "" {
		return true
	}
	var en int
	if err := e.st.db.QueryRowContext(ctx, "SELECT enabled FROM workflow_groups WHERE name = ?", group).Scan(&en); err != nil {
		return true // a new group starts on
	}
	return en == 1
}

// SetGroupEnabled switches a group on or off. Each workflow's own switch
// stays as it is. Turning a group on is refused when its workflows that
// are on would answer a chat command something running already answers,
// or each other's: one answer per command.
func (e *Engine) SetGroupEnabled(ctx context.Context, name string, on bool) error {
	e.cmdMu.Lock()
	defer e.cmdMu.Unlock()
	canon, ok, err := e.st.groupName(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGroupNotFound
	}
	members, err := e.st.groupMembers(ctx, canon)
	if err != nil {
		return err
	}
	if on {
		in := map[string]bool{}
		for _, id := range members {
			in[id] = true
		}
		e.mu.Lock()
		var held []commands.Claim
		for _, wf := range e.enabled {
			if !in[wf.id] {
				held = append(held, ClaimsOf(wf.id, wf.def)...)
			}
		}
		e.mu.Unlock()
		held = append(held, e.otherClaims()...)
		var cs []commands.Conflict
		for _, id := range members {
			d, _, wfOn, err := e.st.definition(ctx, id, 0)
			if err != nil || !wfOn {
				continue
			}
			mine := ClaimsOf(id, d)
			cs = append(cs, commands.Conflicts(mine, held)...)
			held = append(held, mine...)
		}
		if len(cs) > 0 {
			return &commands.TakenError{Conflicts: cs}
		}
	}
	if err := e.st.setGroupEnabled(ctx, canon, on); err != nil {
		return err
	}
	if !on {
		// As for a workflow turned off: its waiting runs are cancelled.
		for _, id := range members {
			e.cancelWaiting(ctx, id)
		}
	}
	e.log.Info("workflow group turned "+map[bool]string{true: "on", false: "off"}[on], "group", canon, "workflows", len(members))
	if err := e.reload(ctx); err != nil {
		return err
	}
	e.notify.Notify()
	return nil
}

// RenameGroup renames a group. Each workflow in it gets a new version
// naming the new group; the group's switch is kept.
func (e *Engine) RenameGroup(ctx context.Context, from, to string) (string, error) {
	to = strings.TrimSpace(to)
	if !GroupPattern.MatchString(to) {
		return "", &InvalidError{[]Problem{{"$.group", "a group's name is up to 40 letters, digits, spaces and - _ & ', like Alerts"}}}
	}
	e.cmdMu.Lock()
	defer e.cmdMu.Unlock()
	canon, ok, err := e.st.groupName(ctx, from)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrGroupNotFound
	}
	if other, exists, err := e.st.groupName(ctx, to); err != nil {
		return "", err
	} else if exists && !strings.EqualFold(other, canon) {
		return "", ErrGroupExists
	}
	members, err := e.st.groupMembers(ctx, canon)
	if err != nil {
		return "", err
	}
	var en int
	_ = e.st.db.QueryRowContext(ctx, "SELECT enabled FROM workflow_groups WHERE name = ?", canon).Scan(&en)
	now := ts(e.st.now())
	// The new name's switch first, as the old one was, so no workflow runs
	// or stops while they move.
	if _, err := e.st.db.ExecContext(ctx, "INSERT INTO workflow_groups (name, enabled, updated_at) VALUES (?, ?, ?) "+
		"ON CONFLICT (name) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at", to, en, now); err != nil {
		return "", err
	}
	for _, id := range members {
		d, _, _, err := e.st.definition(ctx, id, 0)
		if err != nil {
			return "", err
		}
		d.Group = to
		if _, _, err := e.st.save(ctx, d, false); err != nil { // existing: on stays as it was
			return "", fmt.Errorf("moving %s: %w", id, err)
		}
	}
	e.log.Info("workflow group renamed", "from", canon, "to", to, "workflows", len(members))
	if err := e.reload(ctx); err != nil {
		return "", err
	}
	e.notify.Notify()
	return to, nil
}

// cancelWaiting cancels a workflow's runs that haven't started.
func (e *Engine) cancelWaiting(ctx context.Context, id string) {
	e.mu.Lock()
	pending := append([]string(nil), e.queue(id).pending...)
	e.queue(id).pending = nil
	e.mu.Unlock()
	for _, j := range pending {
		_, _ = e.st.db.ExecContext(ctx, "UPDATE jobs SET status = 'cancelled', reason = ?, finished_at = ? WHERE id = ? AND status = 'pending'",
			errTurnedOff.Error(), ts(e.st.now()), j)
	}
}
