package packages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Store keeps installed packages in the database.
type Store struct {
	db *storage.DB
}

// NewStore returns a Store.
func NewStore(db *storage.DB) *Store { return &Store{db: db} }

// Package is an installed package as its current version describes it.
type Package struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Publisher   string    `json:"publisher"` // as stated: not verified
	Description string    `json:"description"`
	License     string    `json:"license"`
	Homepage    string    `json:"homepage"`
	Digest      string    `json:"digest"`
	Size        int64     `json:"size"`
	Files       int       `json:"files"`
	InstalledAt time.Time `json:"installed_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Settings are a theme's settings, as each overlay using it may set them.
	Settings []overlays.Setting `json:"settings,omitempty"`
	// Kept are the other versions kept for rolling back, newest first.
	Kept []string `json:"kept"`
	// RollbackTo is the version Roll back returns to, if any: the highest
	// kept version below this one.
	RollbackTo string `json:"rollback_to,omitempty"`
}

// Preview is what a package is, before installing it: what the app shows
// when it asks.
type Preview struct {
	Package
	// Installed is the version installed now under the same id, if any.
	Installed *Package `json:"installed,omitempty"`
	// Same: exactly this version is installed already; installing it again
	// changes nothing.
	Same bool `json:"same"`
	// Sounds: the theme has its own sounds (D-065).
	Sounds bool `json:"sounds"`
	// Update: another version is installed, and installing this one
	// replaces it (the installed one is kept, to roll back to).
	Update bool `json:"update"`
	// Older: this version is below the installed one.
	Older bool `json:"older"`
	// PublisherChange: the installed one says another publisher. Installing
	// needs the new publisher confirmed by name.
	PublisherChange bool `json:"publisher_change"`
}

func describe(r *Read, now time.Time) Package {
	m := r.Manifest
	p := Package{ID: m.ID, Kind: m.Kind, Name: m.Name, Version: m.Version, Publisher: m.Publisher, Description: m.Description,
		License: m.License, Homepage: m.Homepage, Digest: r.Digest, Size: r.Size, Files: len(r.Files), InstalledAt: now, UpdatedAt: now}
	if m.Theme != nil {
		p.Settings = m.Theme.Settings
	}
	return p
}

// Check reads a package and says what installing it would do. Nothing is
// stored. A package that can't be installed is the error.
func (s *Store) Check(ctx context.Context, data []byte) (Preview, *Read, error) {
	r, err := ReadZip(data)
	if err != nil {
		return Preview{}, nil, err
	}
	pv := Preview{Package: describe(r, time.Time{})}
	th := overlays.InstalledTheme(r.Manifest.ID, "", "", r.Manifest.Version, "", r.Digest, r.Manifest.Theme.Entry, nil, r.Paths())
	pv.Sounds = len(th.Settings) > 0
	cur, err := s.Get(ctx, r.Manifest.ID)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return Preview{}, nil, err
	default:
		pv.Installed = &cur
		if cur.Kind != r.Manifest.Kind {
			return pv, r, fmt.Errorf("%w: %s is installed as a %s: this is a %s with the same id", ErrConflict, cur.Name, cur.Kind, r.Manifest.Kind)
		}
		stored, err := s.versionDigest(ctx, cur.ID, r.Manifest.Version)
		if err != nil {
			return Preview{}, nil, err
		}
		switch {
		case cur.Version == r.Manifest.Version && cur.Digest == r.Digest:
			pv.Same = true
		case stored != "" && stored != r.Digest:
			return pv, r, fmt.Errorf("%w: %s %s is installed or kept, and this is a different package with the same version: ask its publisher for a new version",
				ErrConflict, cur.Name, r.Manifest.Version)
		default:
			pv.Update = true
			pv.Older = CompareVersions(r.Manifest.Version, cur.Version) < 0
			pv.PublisherChange = cur.Publisher != r.Manifest.Publisher
		}
	}
	return pv, r, nil
}

// Install stores a package the streamer was shown and confirmed: digest
// is the one they were shown, so what's installed is what they saw.
// Another version of an installed id is an update: the installed version
// is kept, to roll back to. One from another publisher needs publisher,
// the new publisher's name exactly as the package states it. Installing
// exactly what's installed changes nothing (false). from is the version
// an update replaced.
func (s *Store) Install(ctx context.Context, data []byte, digest, publisher string, now time.Time) (p Package, from string, changed bool, err error) {
	pv, r, err := s.Check(ctx, data)
	if err != nil {
		if r != nil {
			s.audit(ctx, r.Manifest.ID, r.Manifest.Version, "refuse", err.Error(), now)
		}
		return Package{}, "", false, err
	}
	if digest != r.Digest {
		return Package{}, "", false, fmt.Errorf("%w: this isn't the package that was checked: check it again", ErrInvalid)
	}
	if pv.Same {
		return *pv.Installed, "", false, nil
	}
	if pv.PublisherChange && publisher != r.Manifest.Publisher {
		return Package{}, "", false, fmt.Errorf("%w: %s is installed from %s (not verified), and this says %s (not verified): confirm the new publisher by name to replace it",
			ErrConfirm, pv.Installed.Name, pv.Installed.Publisher, r.Manifest.Publisher)
	}
	ts := storage.Timestamp(now)
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if pv.Update {
			return update(ctx, tx, pv, r, ts)
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM packages").Scan(&n); err != nil {
			return err
		}
		if n >= MaxPackages {
			return fmt.Errorf("%w: up to %d packages", ErrFull, MaxPackages)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO packages (id, kind, publisher, current_version, installed_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`, r.Manifest.ID, r.Manifest.Kind, r.Manifest.Publisher, r.Manifest.Version, ts, ts)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return fmt.Errorf("%w: %s was installed just now", ErrConflict, r.Manifest.ID)
		}
		if err := storeVersion(ctx, tx, r, ts); err != nil {
			return err
		}
		return auditTx(ctx, tx, r.Manifest.ID, r.Manifest.Version, "install",
			fmt.Sprintf("%s %s, published by %s (not verified), digest %s", r.Manifest.Name, r.Manifest.Version, r.Manifest.Publisher, r.Digest), ts)
	})
	if err != nil {
		if !errors.Is(err, ErrFull) && !errors.Is(err, ErrConflict) {
			return Package{}, "", false, fmt.Errorf("packages: install: %w", err)
		}
		return Package{}, "", false, err
	}
	if pv.Update {
		from = pv.Installed.Version
	}
	p, err = s.Get(ctx, r.Manifest.ID)
	return p, from, true, err
}

// storeVersion stores a version and its files.
func storeVersion(ctx context.Context, tx *sql.Tx, r *Read, ts string) error {
	manifest, _ := json.Marshal(r.Manifest)
	if _, err := tx.ExecContext(ctx, `INSERT INTO package_versions (package_id, version, digest, manifest, size, installed_at)
		VALUES (?, ?, ?, ?, ?, ?)`, r.Manifest.ID, r.Manifest.Version, r.Digest, string(manifest), r.Size, ts); err != nil {
		return err
	}
	for _, p := range r.Paths() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO package_files (package_id, version, path, sha256, data) VALUES (?, ?, ?, ?, ?)`,
			r.Manifest.ID, r.Manifest.Version, p, r.Hashes[p], r.Files[p]); err != nil {
			return err
		}
	}
	return nil
}

const listQuery = `SELECT p.id, p.kind, p.publisher, p.current_version, p.installed_at, p.updated_at, v.digest, v.manifest, v.size,
	(SELECT COUNT(*) FROM package_files f WHERE f.package_id = p.id AND f.version = p.current_version)
	FROM packages p JOIN package_versions v ON v.package_id = p.id AND v.version = p.current_version`

type scanner interface{ Scan(...any) error }

func scanPackage(row scanner) (Package, error) {
	var p Package
	var installed, updated, manifest string
	if err := row.Scan(&p.ID, &p.Kind, &p.Publisher, &p.Version, &installed, &updated, &p.Digest, &manifest, &p.Size, &p.Files); err != nil {
		return p, err
	}
	var m Manifest
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		return p, fmt.Errorf("packages: %s's manifest: %w", p.ID, err)
	}
	p.Name, p.Description, p.License, p.Homepage = m.Name, m.Description, m.License, m.Homepage
	if m.Theme != nil {
		p.Settings = m.Theme.Settings
	}
	p.InstalledAt, _ = storage.ParseTimestamp(installed)
	p.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return p, nil
}

// Get returns an installed package.
func (s *Store) Get(ctx context.Context, id string) (Package, error) {
	p, err := scanPackage(s.db.QueryRowContext(ctx, listQuery+" WHERE p.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Package{}, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	return p, s.fillKept(ctx, &p)
}

// List returns every installed package, by name.
func (s *Store) List(ctx context.Context) ([]Package, error) {
	rows, err := s.db.QueryContext(ctx, listQuery)
	if err != nil {
		return nil, fmt.Errorf("packages: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Package{}
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for i := range out {
		if err := s.fillKept(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name || out[i].Name == out[j].Name && out[i].ID < out[j].ID
	})
	return out, nil
}

// Remove deletes a package: its versions, files and settings. Its audit
// stays. Whoever calls this returns its overlays to Classic first.
func (s *Store) Remove(ctx context.Context, id string, now time.Time) (Package, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Package{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM package_files WHERE package_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM package_versions WHERE package_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM packages WHERE id = ?", id); err != nil {
			return err
		}
		return auditTx(ctx, tx, id, p.Version, "remove", p.Name+" "+p.Version, storage.Timestamp(now))
	})
	if err != nil {
		return Package{}, fmt.Errorf("packages: remove: %w", err)
	}
	return p, nil
}

// File returns one file of an installed version.
func (s *Store) File(ctx context.Context, id, version, path string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM package_files WHERE package_id = ? AND version = ? AND path = ?", id, version, path).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// Themes are the installed themes, as overlays use them.
func (s *Store) Themes(ctx context.Context) ([]overlays.Theme, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []overlays.Theme
	for _, p := range list {
		if p.Kind != KindTheme {
			continue
		}
		var manifest string
		if err := s.db.QueryRowContext(ctx, "SELECT manifest FROM package_versions WHERE package_id = ? AND version = ?", p.ID, p.Version).
			Scan(&manifest); err != nil {
			return nil, err
		}
		var m Manifest
		if err := json.Unmarshal([]byte(manifest), &m); err != nil || m.Theme == nil {
			return nil, fmt.Errorf("packages: %s's manifest: %w", p.ID, err)
		}
		paths, err := s.paths(ctx, p.ID, p.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, overlays.InstalledTheme(p.ID, p.Name, p.Description, p.Version, p.Publisher, p.Digest, m.Theme.Entry, m.Theme.Settings, paths))
	}
	return out, nil
}

func (s *Store) paths(ctx context.Context, id, version string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path FROM package_files WHERE package_id = ? AND version = ? ORDER BY path", id, version)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AuditEntry is one line of the package audit.
type AuditEntry struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	PackageID string    `json:"package_id"`
	Version   string    `json:"version"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
}

// Audit returns the newest audit entries first, up to limit.
func (s *Store) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT seq, at, package_id, version, action, detail FROM package_audit ORDER BY seq DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("packages: audit: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at string
		if err := rows.Scan(&e.Seq, &at, &e.PackageID, &e.Version, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		e.At, _ = storage.ParseTimestamp(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

func auditTx(ctx context.Context, tx *sql.Tx, id, version, action, detail, at string) error {
	if len(detail) > 300 {
		detail = detail[:300]
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO package_audit (at, package_id, version, action, detail) VALUES (?, ?, ?, ?, ?)",
		at, id, version, action, detail)
	return err
}

// audit records a refusal; failing to record it doesn't change the answer.
func (s *Store) audit(ctx context.Context, id, version, action, detail string, now time.Time) {
	_ = s.withTx(ctx, func(tx *sql.Tx) error {
		return auditTx(ctx, tx, id, version, action, detail, storage.Timestamp(now))
	})
}

// withTx runs fn in a transaction, committing if it returns nil.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
