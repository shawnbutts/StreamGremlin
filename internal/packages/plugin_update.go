package packages

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

// Updating and rolling back a Lua plugin (D-072,
// docs/adr/0038-lua-plugins.md): it asks again only for the permissions
// that aren't approved now, names the steps the other version doesn't
// have, and keeps its settings where they still fit.

// PluginChange is what moving a plugin to another version does.
type PluginChange struct {
	// NewPermissions are those the other version asks for that aren't
	// approved now: moving needs every permission it asks for approved.
	NewPermissions []PermissionInfo `json:"new_permissions"`
	// Permissions are all those the other version asks for.
	Permissions []string `json:"permissions"`
	// RemovedSteps are the installed version's steps the other doesn't
	// have: workflows using them are turned off.
	RemovedSteps []Action `json:"removed_steps"`
	// Reset are the settings, by label, that go back to their default: the
	// other version doesn't have them, or doesn't take their value.
	Reset []string `json:"reset"`
	// Workflows are those using the removed steps, by name (the API fills
	// them in, from the workflow engine).
	Workflows []string `json:"workflows"`
}

// Installed is what installing, updating or rolling back did.
type Installed struct {
	Package
	// From is the version installed before, for an update or roll back.
	From string
	// Changed: false when exactly this was installed already.
	Changed bool
	// Reset are a plugin's settings that went back to their default.
	Reset []string
}

// pluginChange says what moving id from one manifest to another does.
func (s *Store) pluginChange(ctx context.Context, id string, from, to Manifest) (*PluginChange, error) {
	approved, err := s.Approved(ctx, id)
	if err != nil {
		return nil, err
	}
	saved, err := s.savedSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	ch := &PluginChange{NewPermissions: []PermissionInfo{}, Permissions: append([]string{}, to.Lua.Permissions...), RemovedSteps: []Action{},
		Workflows: []string{}}
	for _, p := range to.Lua.Permissions {
		// Higher limits on its rounds ask again too (ADR-0039).
		if !slices.Contains(approved, p) || p == PermRounds && from.Lua.Points.more(to.Lua.Points) {
			ch.NewPermissions = append(ch.NewPermissions, PermissionInfo{Name: p, What: PermissionWhat(to.Lua, p)})
		}
	}
	ch.RemovedSteps = removedSteps(from, to)
	_, ch.Reset = refitSettings(saved, from.Lua.Settings, to.Lua.Settings)
	return ch, nil
}

// removedSteps are from's actions that to doesn't have.
func removedSteps(from, to Manifest) []Action {
	out := []Action{}
	for _, a := range from.Lua.Actions {
		if !slices.ContainsFunc(to.Lua.Actions, func(b Action) bool { return b.Name == a.Name }) {
			out = append(out, a)
		}
	}
	return out
}

// refitSettings keeps the saved values the new settings still take. A
// value left at the old default is dropped, so the new default applies;
// any other value that doesn't fit is reset, and named by its label.
func refitSettings(saved map[string]any, old, next []overlays.Setting) (map[string]any, []string) {
	kept := map[string]any{}
	reset := []string{}
	for _, o := range old {
		v, ok := saved[o.Key]
		if !ok || fmt.Sprint(v) == fmt.Sprint(o.Default) {
			continue
		}
		i := slices.IndexFunc(next, func(n overlays.Setting) bool { return n.Key == o.Key })
		if i < 0 || next[i].Kind != o.Kind || next[i].Check(v) != nil {
			reset = append(reset, o.Label)
			continue
		}
		kept[o.Key] = v
	}
	return kept, reset
}

// switchPlugin moves a plugin's permissions and settings to another
// version, inside the transaction that makes it current. A failure from
// the version before is cleared: this is another version. It returns the
// settings reset and a note for the audit.
func switchPlugin(ctx context.Context, tx *sql.Tx, id string, from, to Manifest, ch *PluginChange) ([]string, string, error) {
	if _, err := tx.ExecContext(ctx, "DELETE FROM package_permissions WHERE package_id = ?", id); err != nil {
		return nil, "", err
	}
	for _, perm := range to.Lua.Permissions {
		if _, err := tx.ExecContext(ctx, "INSERT INTO package_permissions (package_id, permission) VALUES (?, ?)", id, perm); err != nil {
			return nil, "", err
		}
	}
	var raw string
	saved := map[string]any{}
	err := tx.QueryRowContext(ctx, "SELECT settings FROM package_settings WHERE package_id = ?", id).Scan(&raw)
	switch {
	case err == nil:
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return nil, "", err
		}
	case err != sql.ErrNoRows:
		return nil, "", err
	}
	kept, reset := refitSettings(saved, from.Lua.Settings, to.Lua.Settings)
	b, _ := json.Marshal(kept)
	if _, err := tx.ExecContext(ctx, `INSERT INTO package_settings (package_id, settings) VALUES (?, ?)
		ON CONFLICT (package_id) DO UPDATE SET settings = excluded.settings`, id, string(b)); err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE packages SET state_error = '' WHERE id = ?", id); err != nil {
		return nil, "", err
	}
	note := "; permissions: " + permissionList(to.Lua.Permissions)
	if len(ch.NewPermissions) > 0 {
		var names []string
		for _, p := range ch.NewPermissions {
			names = append(names, p.Name)
		}
		note += "; permissions approved: " + strings.Join(names, ", ")
	}
	if len(ch.RemovedSteps) > 0 {
		var names []string
		for _, a := range ch.RemovedSteps {
			names = append(names, a.Name)
		}
		note += "; steps removed: " + strings.Join(names, ", ")
	}
	if len(reset) > 0 {
		note += "; settings reset: " + strings.Join(reset, ", ")
	}
	return reset, note, nil
}

// needsApproval refuses moving to a version that asks for permissions not
// approved now, unless exactly those it asks for are approved.
func needsApproval(name, verb string, to Manifest, ch *PluginChange, approved []string) error {
	if len(ch.NewPermissions) == 0 || samePermissions(to.Lua.Permissions, approved) {
		return nil
	}
	var names []string
	for _, p := range ch.NewPermissions {
		names = append(names, p.Name)
	}
	return fmt.Errorf("%w: %s %s asks for more: %s. Approve exactly what it asks for (%s) to %s",
		ErrConfirmPermissions, name, to.Version, strings.Join(names, ", "), permissionList(to.Lua.Permissions), verb)
}
