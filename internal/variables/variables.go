// Package variables keeps the values workflows save between runs and
// restarts (docs/adr/0009-variables.md). Each variable lives in one scope:
// global (every workflow), viewer (one per viewer, by platform user id) or
// workflow (private to one workflow).
package variables

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Scope says who can see a variable.
type Scope string

// Scopes.
const (
	Global   Scope = "global"
	Viewer   Scope = "viewer"
	Workflow Scope = "workflow"
)

// Scopes lists every scope, in the order screens show them.
var Scopes = []Scope{Global, Viewer, Workflow}

// Limits.
const (
	MaxValueBytes    = 16 << 10 // one value, as JSON
	MaxNamesPerOwner = 1000     // names in one global, viewer or workflow set
	MaxNameLen       = 64
)

// NamePattern is what a variable name looks like: lowercase letters, digits
// and _, starting with a letter. No dots, so `saved.global.deaths` reads
// one variable; group related ones with a prefix (giveaway_open).
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// NameHint explains NamePattern in words.
const NameHint = "lowercase letters, digits and _, starting with a letter, up to 64 characters"

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("variables: invalid")
	ErrNotFound = errors.New("variables: no such variable")
	ErrFull     = errors.New("variables: too many variables")
)

// Key names one variable.
type Key struct {
	Scope Scope  `json:"scope"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (k Key) String() string {
	if k.Owner == "" {
		return string(k.Scope) + "." + k.Name
	}
	return string(k.Scope) + "[" + k.Owner + "]." + k.Name
}

// Check reports whether the key is well formed.
func (k Key) Check() error {
	switch k.Scope {
	case Global:
		if k.Owner != "" {
			return fmt.Errorf("%w: a global variable has no owner", ErrInvalid)
		}
	case Viewer:
		if !strings.Contains(k.Owner, ":") || len(k.Owner) > 160 {
			return fmt.Errorf("%w: a viewer is named <platform>:<user id>, like twitch:12345", ErrInvalid)
		}
	case Workflow:
		if k.Owner == "" || len(k.Owner) > 128 {
			return fmt.Errorf("%w: a workflow variable needs the workflow's id", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: the scope is global, viewer or workflow", ErrInvalid)
	}
	if !NamePattern.MatchString(k.Name) {
		return fmt.Errorf("%w: %q isn't a variable name: %s", ErrInvalid, k.Name, NameHint)
	}
	return nil
}

// Variable is one saved value.
type Variable struct {
	Key
	OwnerLabel string    `json:"owner_label,omitempty"`
	Value      any       `json:"value"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}

// Change describes who changes a variable: "you" or "workflow:<id>", and
// for viewer variables, the viewer's name as last seen.
type Change struct {
	By         string
	OwnerLabel string
}

// Store reads and writes variables.
type Store struct {
	db  *storage.DB
	now func() time.Time
}

// New returns a Store.
func New(db *storage.DB) *Store { return &Store{db: db, now: time.Now} }

// encode checks a value and returns its JSON.
func encode(v any) (string, error) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("%w: not a number", ErrInvalid)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: the value can't be saved: %w", ErrInvalid, err)
	}
	if len(b) > MaxValueBytes {
		return "", fmt.Errorf("%w: the value is %d bytes; the limit is %d", ErrInvalid, len(b), MaxValueBytes)
	}
	return string(b), nil
}

// decode reads a stored value. Whole numbers come back as int64, so
// counters stay integers.
func decode(s string) any {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return Normalize(v)
}

// Normalize turns decoded JSON into plain values: whole numbers become
// int64, others float64.
func Normalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
		return x
	case []any:
		for i := range x {
			x[i] = Normalize(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = Normalize(x[k])
		}
		return x
	}
	return v
}

func ts(t time.Time) string { return storage.Timestamp(t) }

// Get returns one variable.
func (s *Store) Get(ctx context.Context, k Key) (Variable, error) {
	if err := k.Check(); err != nil {
		return Variable{}, err
	}
	var raw, at string
	v := Variable{Key: k}
	err := s.db.QueryRowContext(ctx, "SELECT value, owner_label, updated_at, updated_by FROM variables WHERE scope = ? AND owner = ? AND name = ?",
		k.Scope, k.Owner, k.Name).Scan(&raw, &v.OwnerLabel, &at, &v.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.Value = decode(raw)
	v.UpdatedAt, _ = storage.ParseTimestamp(at)
	return v, nil
}

// Load returns every value of one owner's set (a scope's whole set for
// global), by name. Workflows read these when a run starts.
func (s *Store) Load(ctx context.Context, scope Scope, owner string) (map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, value FROM variables WHERE scope = ? AND owner = ?", scope, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var n, raw string
		if err := rows.Scan(&n, &raw); err != nil {
			return nil, err
		}
		out[n] = decode(raw)
	}
	return out, rows.Err()
}

// Filter narrows List.
type Filter struct {
	Scope Scope  // "" for every scope
	Owner string // "" for every owner
	Query string // part of a name or viewer name, any case
	Limit int    // 1 to 1000; 0 means 500
}

// List returns variables by scope, owner and name.
func (s *Store) List(ctx context.Context, f Filter) ([]Variable, error) {
	q := "SELECT scope, owner, name, value, owner_label, updated_at, updated_by FROM variables WHERE 1 = 1"
	var args []any
	if f.Scope != "" {
		q += " AND scope = ?"
		args = append(args, f.Scope)
	}
	if f.Owner != "" {
		q += " AND owner = ?"
		args = append(args, f.Owner)
	}
	if f.Query != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(f.Query)) + "%"
		q += ` AND (name LIKE ? ESCAPE '\' OR lower(owner_label) LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q += " ORDER BY CASE scope WHEN 'global' THEN 0 WHEN 'viewer' THEN 1 ELSE 2 END, owner_label, owner, name LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Variable{}
	for rows.Next() {
		var v Variable
		var raw, at string
		if err := rows.Scan(&v.Scope, &v.Owner, &v.Name, &raw, &v.OwnerLabel, &at, &v.UpdatedBy); err != nil {
			return nil, err
		}
		v.Value = decode(raw)
		v.UpdatedAt, _ = storage.ParseTimestamp(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

// write stores a value in an open transaction, checking the owner's limit.
func (s *Store) write(ctx context.Context, tx *sql.Tx, k Key, raw string, c Change, exists bool) error {
	if !exists {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM variables WHERE scope = ? AND owner = ?", k.Scope, k.Owner).Scan(&n); err != nil {
			return err
		}
		if n >= MaxNamesPerOwner {
			return fmt.Errorf("%w: %s already has %d variables; delete some first", ErrFull, describeOwner(k), n)
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO variables (scope, owner, name, value, owner_label, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, owner, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at,
			updated_by = excluded.updated_by,
			owner_label = CASE WHEN excluded.owner_label = '' THEN variables.owner_label ELSE excluded.owner_label END`,
		k.Scope, k.Owner, k.Name, raw, c.OwnerLabel, ts(s.now()), c.By)
	return err
}

func describeOwner(k Key) string {
	switch k.Scope {
	case Viewer:
		return "this viewer"
	case Workflow:
		return "this workflow"
	}
	return "the global set"
}

// current reads a value inside a transaction.
func current(ctx context.Context, tx *sql.Tx, k Key) (any, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT value FROM variables WHERE scope = ? AND owner = ? AND name = ?", k.Scope, k.Owner, k.Name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return decode(raw), true, nil
}

// Set saves a value, replacing any old one, and returns the old one.
func (s *Store) Set(ctx context.Context, k Key, value any, c Change) (previous any, err error) {
	if err := k.Check(); err != nil {
		return nil, err
	}
	raw, err := encode(value)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prev, exists, err := current(ctx, tx, k)
	if err != nil {
		return nil, err
	}
	if err := s.write(ctx, tx, k, raw, c, exists); err != nil {
		return nil, err
	}
	return prev, tx.Commit()
}

// Add adds amount to a number, atomically, so two runs counting at once
// never lose a count. A missing variable counts as 0. It returns the old
// and new values.
func (s *Store) Add(ctx context.Context, k Key, amount any, c Change) (previous, value any, err error) {
	return s.Update(ctx, k, func(prev any) (any, error) {
		v, err := Sum(prev, amount)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %w", ErrInvalid, k.Name, err)
		}
		return v, nil
	}, c)
}

// Update changes a value atomically: f gets the old value (nil when
// missing) and returns the new one, with no other write between. An error
// from f changes nothing and is returned as it is. It returns the old and
// new values.
func (s *Store) Update(ctx context.Context, k Key, f func(prev any) (any, error), c Change) (previous, value any, err error) {
	if err := k.Check(); err != nil {
		return nil, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil) // immediate: no other write between the read and the write
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prev, exists, err := current(ctx, tx, k)
	if err != nil {
		return nil, nil, err
	}
	value, err = f(prev)
	if err != nil {
		return nil, nil, err
	}
	raw, err := encode(value)
	if err != nil {
		return nil, nil, err
	}
	if err := s.write(ctx, tx, k, raw, c, exists); err != nil {
		return nil, nil, err
	}
	return prev, value, tx.Commit()
}

// Sum adds amount to prev (nil counts as 0). Integers stay integers.
func Sum(prev, amount any) (any, error) {
	if prev == nil {
		prev = int64(0)
	}
	switch a := amount.(type) {
	case int64:
		switch p := prev.(type) {
		case int64:
			if (a > 0 && p > math.MaxInt64-a) || (a < 0 && p < math.MinInt64-a) {
				return nil, errors.New("would be too big")
			}
			return p + a, nil
		case float64:
			return p + float64(a), nil
		}
	case float64:
		switch p := prev.(type) {
		case int64:
			return float64(p) + a, nil
		case float64:
			return p + a, nil
		}
	default:
		return nil, errors.New("can only add a number")
	}
	return nil, fmt.Errorf("holds %s, not a number, so it can't be added to", kind(prev))
}

func kind(v any) string {
	switch v.(type) {
	case string:
		return "text"
	case bool:
		return "true or false"
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	}
	return "something else"
}

// Delete removes a variable and reports whether it was there.
func (s *Store) Delete(ctx context.Context, k Key) (bool, error) {
	if err := k.Check(); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM variables WHERE scope = ? AND owner = ? AND name = ?", k.Scope, k.Owner, k.Name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
