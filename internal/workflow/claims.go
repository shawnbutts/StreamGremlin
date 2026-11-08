package workflow

import (
	"context"
	"encoding/json"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/commands"
)

// commandEvent is the event a !command arrives as.
const commandEvent = "chat.command.received"

// ClaimsOf lists the chat commands a workflow answers while it's on: those
// its command triggers name in where.command (docs/adr/0011-command-ownership.md).
// A trigger that names no command sees every command and answers none.
func ClaimsOf(id string, d *Definition) []commands.Claim {
	var out []commands.Claim
	for _, t := range d.Triggers {
		if !typeMatches(t.Type, commandEvent) {
			continue
		}
		cmds := strings1(t.Where["command"])
		if len(cmds) == 0 {
			continue
		}
		var roles []string
		if raw, ok := t.Where["chatter.role"]; ok {
			roles = strings1(raw)
			if roles == nil {
				roles = []string{} // a role match that isn't text: it meets no other
			}
		}
		var sources []string
		if len(t.Sources) > 0 {
			sources = t.Sources
		}
		for _, c := range cmds {
			out = append(out, commands.Claim{Command: strings.ToLower(c), Owner: id, Name: d.Name, Sources: sources, Roles: roles})
		}
	}
	return out
}

// strings1 reads a where value as text values: one, or a list.
func strings1(raw json.RawMessage) []string {
	if raw == nil {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []any
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	var out []string
	for _, v := range many {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// heldExcept is every claim of the enabled workflows but id.
func (e *Engine) heldExcept(id string) []commands.Claim {
	e.mu.Lock()
	defer e.mu.Unlock()
	var held []commands.Claim
	for _, wf := range e.enabled {
		if wf.id != id {
			held = append(held, ClaimsOf(wf.id, wf.def)...)
		}
	}
	return held
}

// checkCommands refuses d's claims that something else on already answers.
// With prev (a change to a workflow that's on), only overlaps the change
// creates are refused: a new command, or a command opened to more chatters
// or chats. An overlap that was already there doesn't block an unrelated
// edit.
func (e *Engine) checkCommands(id string, d, prev *Definition) error {
	held := e.heldExcept(id)
	cs := commands.Conflicts(ClaimsOf(id, d), held)
	if prev != nil {
		had := map[commands.Conflict]bool{}
		for _, c := range commands.Conflicts(ClaimsOf(id, prev), held) {
			had[c] = true
		}
		kept := cs[:0]
		for _, c := range cs {
			if !had[c] {
				kept = append(kept, c)
			}
		}
		cs = kept
	}
	if len(cs) > 0 {
		return &commands.TakenError{Conflicts: cs}
	}
	return nil
}

// Commands lists every chat command a workflow answers, with whether each
// owner is on and whether owners that are on overlap.
func (e *Engine) Commands(ctx context.Context) ([]commands.Entry, error) {
	list, err := e.st.list(ctx, false)
	if err != nil {
		return nil, err
	}
	var claims []commands.Claim
	on := map[string]bool{}
	for _, s := range list {
		d, _, _, err := e.st.definition(ctx, s.ID, 0)
		if err != nil {
			continue
		}
		on[s.ID] = s.Enabled
		claims = append(claims, ClaimsOf(s.ID, d)...)
	}
	return commands.Table(claims, on), nil
}
