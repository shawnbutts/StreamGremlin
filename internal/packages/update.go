package packages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Updating and rolling back (docs/adr/0036-packages-plugins-and-themes.md,
// D-068). A package keeps its current version and up to KeptVersions
// others: the highest below the current one first, so Roll back always
// has the one before, and then the lowest above it.

// KeptVersions is how many versions besides the current one are kept.
const KeptVersions = 2

// CompareVersions orders two versions the way semver does: -1, 0 or 1.
// A pre-release ("1.0.0-beta") comes before its release.
func CompareVersions(a, b string) int {
	ac, apre, _ := strings.Cut(a, "-")
	bc, bpre, _ := strings.Cut(b, "-")
	an, bn := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < 3 && i < len(an) && i < len(bn); i++ {
		if c := compareNumbers(an[i], bn[i]); c != 0 {
			return c
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	ap, bp := strings.Split(apre, "."), strings.Split(bpre, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		_, aErr := strconv.Atoi(ap[i])
		_, bErr := strconv.Atoi(bp[i])
		var c int
		switch {
		case aErr == nil && bErr == nil:
			c = compareNumbers(ap[i], bp[i])
		case aErr == nil: // numbers come before words
			c = -1
		case bErr == nil:
			c = 1
		default:
			c = strings.Compare(ap[i], bp[i])
		}
		if c != 0 {
			return c
		}
	}
	return compareInts(len(ap), len(bp))
}

func compareNumbers(a, b string) int {
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	return compareInts(x, y)
}

func compareInts(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// versionDigest is a stored version's digest, or "" when it isn't stored.
func (s *Store) versionDigest(ctx context.Context, id, version string) (string, error) {
	var d string
	err := s.db.QueryRowContext(ctx, "SELECT digest FROM package_versions WHERE package_id = ? AND version = ?", id, version).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return d, err
}

func versionsTx(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT version FROM package_versions WHERE package_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// below is the highest of versions under current, or "".
func below(versions []string, current string) string {
	best := ""
	for _, v := range versions {
		if CompareVersions(v, current) < 0 && (best == "" || CompareVersions(v, best) > 0) {
			best = v
		}
	}
	return best
}

// keep chooses the versions besides current to keep: the highest below it
// first, then the lowest above it, up to KeptVersions.
func keep(versions []string, current string) []string {
	var lower, higher []string
	for _, v := range versions {
		switch c := CompareVersions(v, current); {
		case c < 0:
			lower = append(lower, v)
		case c > 0:
			higher = append(higher, v)
		}
	}
	sort.Slice(lower, func(i, j int) bool { return CompareVersions(lower[i], lower[j]) > 0 })
	sort.Slice(higher, func(i, j int) bool { return CompareVersions(higher[i], higher[j]) < 0 })
	out := append(lower, higher...)
	if len(out) > KeptVersions {
		out = out[:KeptVersions]
	}
	sort.Slice(out, func(i, j int) bool { return CompareVersions(out[i], out[j]) > 0 })
	return out
}

func (s *Store) fillKept(ctx context.Context, p *Package) error {
	all, err := versionsTx(ctx, s.db, p.ID)
	if err != nil {
		return err
	}
	p.Kept = []string{}
	for _, v := range all {
		if v != p.Version {
			p.Kept = append(p.Kept, v)
		}
	}
	sort.Slice(p.Kept, func(i, j int) bool { return CompareVersions(p.Kept[i], p.Kept[j]) > 0 })
	p.RollbackTo = below(p.Kept, p.Version)
	return nil
}

// update makes r the installed version of an installed id: stored if it
// isn't already (a version kept from before has the same files), current,
// with its publisher; then the versions beyond what's kept are deleted.
func update(ctx context.Context, tx *sql.Tx, pv Preview, r *Read, ts string) error {
	cur := pv.Installed
	var stored string
	err := tx.QueryRowContext(ctx, "SELECT digest FROM package_versions WHERE package_id = ? AND version = ?", r.Manifest.ID, r.Manifest.Version).
		Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case stored != r.Digest:
		return fmt.Errorf("%w: %s %s was kept with other files just now", ErrConflict, r.Manifest.ID, r.Manifest.Version)
	}
	if stored == "" {
		if err := storeVersion(ctx, tx, r, ts); err != nil {
			return err
		}
	}
	if err := setCurrent(ctx, tx, r.Manifest.ID, cur.Version, r.Manifest.Version, r.Manifest.Publisher, ts); err != nil {
		return err
	}
	detail := fmt.Sprintf("%s %s to %s, published by %s (not verified), digest %s", r.Manifest.Name, cur.Version, r.Manifest.Version,
		r.Manifest.Publisher, r.Digest)
	if pv.PublisherChange {
		detail += fmt.Sprintf("; the publisher changed from %s, confirmed by name", cur.Publisher)
	}
	if err := auditTx(ctx, tx, r.Manifest.ID, r.Manifest.Version, "update", detail, ts); err != nil {
		return err
	}
	return prune(ctx, tx, r.Manifest.ID, r.Manifest.Version)
}

// setCurrent switches the current version, if it's still from: an update
// or roll back made at the same moment loses.
func setCurrent(ctx context.Context, tx *sql.Tx, id, from, to, publisher, ts string) error {
	res, err := tx.ExecContext(ctx, `UPDATE packages SET current_version = ?, publisher = ?, updated_at = ?
		WHERE id = ? AND current_version = ?`, to, publisher, ts, id, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s changed just now: check it again", ErrConflict, id)
	}
	return nil
}

// prune deletes the versions beyond the current one and those kept.
func prune(ctx context.Context, tx *sql.Tx, id, current string) error {
	all, err := versionsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	kept := map[string]bool{current: true}
	for _, v := range keep(all, current) {
		kept[v] = true
	}
	for _, v := range all {
		if kept[v] {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM package_files WHERE package_id = ? AND version = ?", id, v); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM package_versions WHERE package_id = ? AND version = ?", id, v); err != nil {
			return err
		}
	}
	return nil
}

// Rollback makes the version before the installed one (its RollbackTo)
// current again, with that version's publisher. The version rolled back
// from is kept. Returns the package as it is now and the version it was.
func (s *Store) Rollback(ctx context.Context, id string, now time.Time) (Package, string, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Package{}, "", err
	}
	if cur.RollbackTo == "" {
		return Package{}, "", fmt.Errorf("%w: there's no version of %s before %s to roll back to", ErrConflict, cur.Name, cur.Version)
	}
	to := cur.RollbackTo
	ts := storage.Timestamp(now)
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT manifest FROM package_versions WHERE package_id = ? AND version = ?", id, to).Scan(&raw); err != nil {
			return err
		}
		var m Manifest
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return err
		}
		if err := setCurrent(ctx, tx, id, cur.Version, to, m.Publisher, ts); err != nil {
			return err
		}
		return auditTx(ctx, tx, id, to, "rollback", fmt.Sprintf("%s %s back to %s, published by %s (not verified)", m.Name, cur.Version, to, m.Publisher), ts)
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Package{}, "", err
		}
		return Package{}, "", fmt.Errorf("packages: roll back: %w", err)
	}
	p, err := s.Get(ctx, id)
	return p, cur.Version, err
}
