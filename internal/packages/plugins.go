package packages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/templates"
)

// MaxActivity is how many activity entries a plugin keeps.
const MaxActivity = 1000

func samePermissions(asked, approved []string) bool {
	a, b := slices.Clone(asked), slices.Clone(approved)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func permissionList(perms []string) string {
	if len(perms) == 0 {
		return "no permissions"
	}
	return strings.Join(perms, ", ")
}

// fillPlugin adds a plugin's saved settings.
func (s *Store) fillPlugin(ctx context.Context, p *Package) error {
	if p.Plugin == nil {
		return nil
	}
	saved, err := s.savedSettings(ctx, p.ID)
	if err != nil {
		return err
	}
	p.Plugin.Values = overlays.FillSettings(p.Settings, saved)
	ts, err := s.templatesOf(ctx, p.ID, p.Version)
	if err != nil {
		return err
	}
	p.Plugin.Templates = templateInfo(ts)
	return nil
}

// templatesOf reads an installed version's templates, as they were checked.
func (s *Store) templatesOf(ctx context.Context, id, version string) ([]templates.Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, data FROM package_files WHERE package_id = ? AND version = ?
		AND path LIKE 'templates/%.json' ORDER BY path`, id, version)
	if err != nil {
		return nil, fmt.Errorf("packages: templates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []templates.Template{}
	for rows.Next() {
		var p string
		var b []byte
		if err := rows.Scan(&p, &b); err != nil {
			return nil, err
		}
		if !isTemplate(p) {
			continue
		}
		t, err := templates.ParseAddOn(b)
		if err != nil {
			return nil, fmt.Errorf("packages: %s's %s: %w", id, p, err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddOnTemplates are every installed plugin's templates, for the gallery:
// each id is "<plugin>/<template>", its category the plugin's name.
func (s *Store) AddOnTemplates(ctx context.Context) ([]templates.Template, error) {
	ps, _, err := s.Plugins(ctx)
	if err != nil {
		return nil, err
	}
	out := []templates.Template{}
	for _, p := range ps {
		ts, err := s.templatesOf(ctx, p.ID, p.Version)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			t.ID = p.ID + "/" + t.ID
			t.Category = p.Name
			t.AddOn = &templates.AddOn{ID: p.ID, Name: p.Name, Publisher: p.Publisher, Enabled: p.Plugin.Enabled}
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Store) savedSettings(ctx context.Context, id string) (map[string]any, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT settings FROM package_settings WHERE package_id = ?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("packages: settings: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("packages: settings: %w", err)
	}
	return m, nil
}

// Plugin returns an installed Lua plugin, with its manifest.
func (s *Store) Plugin(ctx context.Context, id string) (Package, Manifest, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Package{}, Manifest{}, err
	}
	if p.Plugin == nil {
		return Package{}, Manifest{}, fmt.Errorf("%w: %s is a %s, not a plugin", ErrInvalid, p.Name, p.Kind)
	}
	m, err := s.manifest(ctx, id, p.Version)
	return p, m, err
}

func (s *Store) manifest(ctx context.Context, id, version string) (Manifest, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT manifest FROM package_versions WHERE package_id = ? AND version = ?", id, version).Scan(&raw); err != nil {
		return Manifest{}, fmt.Errorf("packages: %s's manifest: %w", id, err)
	}
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Manifest{}, fmt.Errorf("packages: %s's manifest: %w", id, err)
	}
	return m, nil
}

// Plugins are the installed Lua plugins, with their manifests.
func (s *Store) Plugins(ctx context.Context) ([]Package, []Manifest, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	var ps []Package
	var ms []Manifest
	for _, p := range all {
		if p.Plugin == nil {
			continue
		}
		m, err := s.manifest(ctx, p.ID, p.Version)
		if err != nil {
			return nil, nil, err
		}
		ps, ms = append(ps, p), append(ms, m)
	}
	return ps, ms, nil
}

// Approved are the permissions the streamer approved for a plugin.
func (s *Store) Approved(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT permission FROM package_permissions WHERE package_id = ? ORDER BY permission", id)
	if err != nil {
		return nil, fmt.Errorf("packages: permissions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetEnabled turns a plugin on or off, clearing why it failed.
func (s *Store) SetEnabled(ctx context.Context, id string, on bool, now time.Time) error {
	p, _, err := s.Plugin(ctx, id)
	if err != nil {
		return err
	}
	action := "off"
	if on {
		action = "on"
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE packages SET enabled = ?, state_error = '' WHERE id = ?", on, id); err != nil {
			return err
		}
		return auditTx(ctx, tx, id, p.Version, action, "", storage.Timestamp(now))
	})
}

// Fail turns a plugin off, saying why: it wouldn't load, or its worker
// failed too often.
func (s *Store) Fail(ctx context.Context, id, why string, now time.Time) error {
	p, _, err := s.Plugin(ctx, id)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE packages SET enabled = 0, state_error = ? WHERE id = ?", why, id); err != nil {
			return err
		}
		return auditTx(ctx, tx, id, p.Version, "failed", why, storage.Timestamp(now))
	})
}

// SetSettings saves a plugin's settings, each checked against its
// manifest; ones not given keep their value. It returns them all.
func (s *Store) SetSettings(ctx context.Context, id string, values map[string]any, now time.Time) (map[string]any, error) {
	p, _, err := s.Plugin(ctx, id)
	if err != nil {
		return nil, err
	}
	saved, err := s.savedSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	next := overlays.FillSettings(p.Settings, saved)
	var changed []string
	for k, v := range values {
		i := slices.IndexFunc(p.Settings, func(st overlays.Setting) bool { return st.Key == k })
		if i < 0 {
			return nil, invalid("%s has no setting %q", p.Name, k)
		}
		if err := p.Settings[i].Check(v); err != nil {
			return nil, invalid("%s", strings.TrimPrefix(err.Error(), overlays.ErrInvalid.Error()+": "))
		}
		if fmt.Sprint(next[k]) != fmt.Sprint(v) {
			changed = append(changed, k)
		}
		next[k] = v
	}
	slices.Sort(changed)
	raw, _ := json.Marshal(next)
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO package_settings (package_id, settings) VALUES (?, ?)
			ON CONFLICT (package_id) DO UPDATE SET settings = excluded.settings`, id, string(raw)); err != nil {
			return err
		}
		if len(changed) == 0 {
			return nil
		}
		return auditTx(ctx, tx, id, p.Version, "settings", "changed "+strings.Join(changed, ", "), storage.Timestamp(now))
	})
	if err != nil {
		return nil, fmt.Errorf("packages: settings: %w", err)
	}
	return next, nil
}

// Activity is one call a plugin made to StreamGremlin.
type Activity struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Version string    `json:"version"`
	Call    string    `json:"call"`
	// Outcome is ok, refused (not allowed, too many) or failed (allowed,
	// but it didn't happen).
	Outcome string `json:"outcome"`
	Detail  string `json:"detail"`
}

// AddActivity records a call, keeping the last MaxActivity.
func (s *Store) AddActivity(ctx context.Context, id, version string, a Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO package_activity (at, package_id, version, call, outcome, detail)
			SELECT ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM packages WHERE id = ?)`,
			storage.Timestamp(a.At), id, version, a.Call, a.Outcome, cutText(a.Detail, 200), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // removed meanwhile
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM package_activity WHERE package_id = ? AND seq <= (
			SELECT seq FROM package_activity WHERE package_id = ? ORDER BY seq DESC LIMIT 1 OFFSET ?)`, id, id, MaxActivity)
		return err
	})
}

// ActivityOf is a plugin's newest calls first, at most limit.
func (s *Store) ActivityOf(ctx context.Context, id string, limit int) ([]Activity, error) {
	if _, _, err := s.Plugin(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, at, version, call, outcome, detail FROM package_activity
		WHERE package_id = ? ORDER BY seq DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("packages: activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var at string
		if err := rows.Scan(&a.Seq, &at, &a.Version, &a.Call, &a.Outcome, &a.Detail); err != nil {
			return nil, err
		}
		a.At, _ = storage.ParseTimestamp(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// cutText shortens s to at most n characters.
func cutText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
