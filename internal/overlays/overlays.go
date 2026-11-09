// Package overlays serves OBS browser sources (docs/adr/0017-alert-overlays.md).
// Each overlay is one browser source with its own private link. The link
// can only show that overlay and say how its alerts went; nothing else.
// The engine decides what to show; a page never decides anything.
package overlays

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Kinds of overlay.
const KindAlert = "alert"

// MaxOverlays is how many overlays there may be.
const MaxOverlays = 50

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("overlays: invalid")
	ErrNotFound = errors.New("overlays: no such overlay")
	ErrExists   = errors.New("overlays: an overlay with that name exists")
	ErrFull     = errors.New("overlays: too many overlays")
	// ErrNoLink: the link's token isn't in the secret store (forgotten, or
	// restored from a backup on another computer). A new link fixes it.
	ErrNoLink = errors.New("overlays: this overlay's link isn't saved on this computer; make a new link")
)

// NamePattern is what an overlay's name looks like.
const NamePattern = `^[a-z0-9][a-z0-9_-]{0,63}$`

var (
	namePattern  = regexp.MustCompile(NamePattern)
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Positions and animations an alert overlay offers.
var (
	Positions  = []string{"top-left", "top", "top-right", "center", "bottom-left", "bottom", "bottom-right"}
	Animations = []string{"slide", "pop"}
)

// Look is how an alert overlay looks.
type Look struct {
	Position   string `json:"position"`   // one of Positions
	Animation  string `json:"animation"`  // slide or pop
	TextColor  string `json:"text_color"` // #RRGGBB
	Background string `json:"background"` // #RRGGBB
	Opacity    int    `json:"opacity"`    // the background's, 0 to 100
	Seconds    int    `json:"seconds"`    // on screen, 2 to 60, unless a step says
}

// DefaultLook is a new alert overlay's look.
func DefaultLook() Look {
	return Look{Position: "top", Animation: "slide", TextColor: "#ffffff", Background: "#081923", Opacity: 90, Seconds: 6}
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if v == x {
			return true
		}
	}
	return false
}

// Check reports what's wrong with a look, in words.
func (l Look) Check() error {
	switch {
	case !oneOf(l.Position, Positions):
		return invalid("the position is a corner, top or bottom middle, or center")
	case !oneOf(l.Animation, Animations):
		return invalid("the animation is slide or pop")
	case !colorPattern.MatchString(l.TextColor), !colorPattern.MatchString(l.Background):
		return invalid("colors are like #ffffff")
	case l.Opacity < 0 || l.Opacity > 100:
		return invalid("the opacity is 0 to 100")
	case l.Seconds < MinSeconds || l.Seconds > MaxSeconds:
		return invalid("an alert stays %d to %d seconds", MinSeconds, MaxSeconds)
	}
	return nil
}

// How long an alert may stay on screen.
const (
	MinSeconds = 2
	MaxSeconds = 60
)

// CheckName reports what's wrong with an overlay's name.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return invalid("an overlay's name is up to 64 lowercase letters, digits, - and _, like alerts")
	}
	return nil
}

// Overlay is one browser source. Its link isn't here: see Store.Link.
type Overlay struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Look      Look      `json:"look"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store keeps overlays: the rows in the database, the tokens in the secret
// store.
type Store struct {
	db      *storage.DB
	secrets *secrets.Store
}

// NewStore returns a Store.
func NewStore(db *storage.DB, sec *secrets.Store) *Store { return &Store{db: db, secrets: sec} }

func secretKey(name string) string { return "overlay/" + name }

// newToken returns a 256-bit random token and its hash.
func newToken() (token, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand never fails on supported systems
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const columns = "name, kind, look, created_at, updated_at"

type scanner interface{ Scan(...any) error }

func scan(s scanner) (Overlay, error) {
	var o Overlay
	var look, created, updated string
	if err := s.Scan(&o.Name, &o.Kind, &look, &created, &updated); err != nil {
		return Overlay{}, err
	}
	o.Look = DefaultLook()
	if err := json.Unmarshal([]byte(look), &o.Look); err != nil {
		return Overlay{}, fmt.Errorf("overlays: %s's look: %w", o.Name, err)
	}
	o.CreatedAt, _ = storage.ParseTimestamp(created)
	o.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return o, nil
}

// Create adds an overlay and returns its link's token. A taken name is
// refused: two sources never share a link by accident.
func (s *Store) Create(ctx context.Context, name, kind string, look Look, now time.Time) (Overlay, string, error) {
	if err := CheckName(name); err != nil {
		return Overlay{}, "", err
	}
	if kind != KindAlert {
		return Overlay{}, "", invalid("the kind of overlay is alert")
	}
	if err := look.Check(); err != nil {
		return Overlay{}, "", err
	}
	raw, _ := json.Marshal(look)
	token, hash := newToken()
	var count int
	var taken bool
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MAX(name = ?), 0) FROM overlays", name).Scan(&count, &taken); err != nil {
		return Overlay{}, "", err
	}
	if taken {
		return Overlay{}, "", fmt.Errorf("%w: %q", ErrExists, name)
	}
	if count >= MaxOverlays {
		return Overlay{}, "", fmt.Errorf("%w: up to %d", ErrFull, MaxOverlays)
	}
	// The link is saved first, so an overlay never exists with a link
	// nobody can see. (The secret store writes to the database too, so
	// this can't be inside the insert's transaction.)
	if err := s.secrets.Set(ctx, secretKey(name), secrets.NewValue(token)); err != nil {
		return Overlay{}, "", fmt.Errorf("overlays: saving the link: %w", err)
	}
	ts := storage.Timestamp(now)
	o, err := scan(s.db.QueryRowContext(ctx, `INSERT INTO overlays (name, kind, look, token_hash, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ? WHERE (SELECT COUNT(*) FROM overlays) < ?
		ON CONFLICT (name) DO NOTHING RETURNING `+columns, name, kind, string(raw), hash, ts, ts, MaxOverlays))
	if errors.Is(err, sql.ErrNoRows) {
		// Another request took the name or the last place just now.
		return Overlay{}, "", fmt.Errorf("%w: %q, or there are %d overlays already", ErrExists, name, MaxOverlays)
	}
	if err != nil {
		return Overlay{}, "", fmt.Errorf("overlays: create: %w", err)
	}
	return o, token, nil
}

// List returns every overlay, by name.
func (s *Store) List(ctx context.Context) ([]Overlay, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM overlays ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("overlays: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Overlay{}
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Get returns one overlay.
func (s *Store) Get(ctx context.Context, name string) (Overlay, error) {
	o, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM overlays WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return Overlay{}, ErrNotFound
	}
	return o, err
}

// ByToken returns the overlay a link's token opens.
func (s *Store) ByToken(ctx context.Context, token string) (Overlay, error) {
	if token == "" {
		return Overlay{}, ErrNotFound
	}
	o, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM overlays WHERE token_hash = ?", hashToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return Overlay{}, ErrNotFound
	}
	return o, err
}

// SetLook changes an overlay's look.
func (s *Store) SetLook(ctx context.Context, name string, look Look, now time.Time) (Overlay, error) {
	if err := look.Check(); err != nil {
		return Overlay{}, err
	}
	raw, _ := json.Marshal(look)
	o, err := scan(s.db.QueryRowContext(ctx, `UPDATE overlays SET look = ?,
		updated_at = CASE WHEN look = ? THEN updated_at ELSE ? END WHERE name = ? RETURNING `+columns,
		string(raw), string(raw), storage.Timestamp(now), name))
	if errors.Is(err, sql.ErrNoRows) {
		return Overlay{}, ErrNotFound
	}
	return o, err
}

// Link returns the token of an overlay's link. ErrNoLink means it isn't
// saved on this computer, or what's saved isn't the current link.
func (s *Store) Link(ctx context.Context, name string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT token_hash FROM overlays WHERE name = ?", name).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	v, err := s.secrets.Get(ctx, secretKey(name))
	if errors.Is(err, secrets.ErrNotFound) {
		return "", ErrNoLink
	}
	if err != nil {
		return "", err
	}
	if hashToken(v.Reveal()) != hash {
		return "", ErrNoLink
	}
	return v.Reveal(), nil
}

// NewLink replaces an overlay's link. The old one stops working at once,
// even if saving the new one fails.
func (s *Store) NewLink(ctx context.Context, name string, now time.Time) (string, error) {
	token, hash := newToken()
	r, err := s.db.ExecContext(ctx, "UPDATE overlays SET token_hash = ?, updated_at = ? WHERE name = ?", hash, storage.Timestamp(now), name)
	if err != nil {
		return "", fmt.Errorf("overlays: new link: %w", err)
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	if err := s.secrets.Set(ctx, secretKey(name), secrets.NewValue(token)); err != nil {
		return "", fmt.Errorf("overlays: the old link stopped working, but the new one couldn't be saved: %w", err)
	}
	return token, nil
}

// Delete removes an overlay; its link stops working.
func (s *Store) Delete(ctx context.Context, name string) (bool, error) {
	r, err := s.db.ExecContext(ctx, "DELETE FROM overlays WHERE name = ?", name)
	if err != nil {
		return false, fmt.Errorf("overlays: delete: %w", err)
	}
	n, _ := r.RowsAffected()
	if err := s.secrets.Delete(ctx, secretKey(name)); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return n > 0, fmt.Errorf("overlays: deleted, but its saved link couldn't be forgotten: %w", err)
	}
	return n > 0, nil
}
