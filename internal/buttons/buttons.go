// Package buttons keeps the streamer's buttons (M7, D-075): the Live
// screen shows them, and later the Stream Deck presses them. Pressing one
// records a buttons.button.pressed event, and workflows with the trigger
// "You press a button" match its button_id. A button holds no steps of its
// own, and has no viewer, so a step that needs one is refused.
package buttons

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Pressed is the event a press records.
const Pressed = events.ButtonPressed

// Limits.
const (
	MaxLabel   = 40
	MaxButtons = 100
	maxRequest = 64
)

// Colors a button may have; "" is the app's usual button colour.
var Colors = []string{"red", "orange", "yellow", "green", "teal", "blue", "purple", "pink"}

// Where a press came from.
const (
	FromLive       = "live"
	FromCLI        = "cli"
	FromStreamDeck = "stream_deck"
)

var froms = []string{FromLive, FromCLI, FromStreamDeck}

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("buttons: invalid")
	ErrNotFound = errors.New("buttons: no such button")
	ErrFull     = errors.New("buttons: too many buttons")
)

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	requestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	iconPattern    = regexp.MustCompile(`^(tabler:[a-z0-9]+(-[a-z0-9]+)*|nf:[a-z0-9]+(-[a-z0-9_]+)+)$`)
)

// MaxIcon is the longest icon name.
const MaxIcon = 72

// Button is one button, as saved.
type Button struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Color string `json:"color"`
	// Icon is an icon from a set the app bundles: tabler:NAME (Tabler
	// Icons) or nf:NAME (Nerd Fonts, like nf:md-microphone), or "" for none. The engine checks only the form of the
	// name; a screen that doesn't know it shows the label.
	Icon string `json:"icon"`
	// HideLabel shows only the icon. The label is still the button's name
	// for screen readers and its tooltip.
	HideLabel bool `json:"hide_label"`
	// Position is its place on the Live screen, from 0.
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// CheckID reports what's wrong with a button's id.
func CheckID(id string) error {
	if len(id) > 64 || !idPattern.MatchString(id) {
		return invalid("a button's id is up to 64 lowercase letters, digits, - and _, like brb")
	}
	return nil
}

// Check reports what's wrong with b, in words.
func (b *Button) Check() error {
	b.Label = strings.TrimSpace(b.Label)
	if err := CheckID(b.ID); err != nil {
		return err
	}
	if b.Label == "" || utf8.RuneCountInString(b.Label) > MaxLabel {
		return invalid("a button needs a label of up to %d characters", MaxLabel)
	}
	if b.Color != "" && !slices.Contains(Colors, b.Color) {
		return invalid("a button's colour is one of %s, or none", strings.Join(Colors, ", "))
	}
	if b.Icon != "" && (len(b.Icon) > MaxIcon || !iconPattern.MatchString(b.Icon)) {
		return invalid("an icon is tabler:NAME (tabler.io/icons) or nf:NAME (nerdfonts.com/cheat-sheet), like tabler:player-pause or nf:md-microphone")
	}
	if b.HideLabel && b.Icon == "" {
		return invalid("only a button with an icon can hide its label")
	}
	return nil
}

// Service keeps buttons and records their presses.
type Service struct {
	db    *storage.DB
	inbox *events.Inbox
	now   func() time.Time
}

// New returns the service. now is time.Now when nil.
func New(db *storage.DB, inbox *events.Inbox, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, inbox: inbox, now: now}
}

const columns = `id, label, color, icon, hide_label, position, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scan(s scanner) (Button, error) {
	var b Button
	var created, updated string
	if err := s.Scan(&b.ID, &b.Label, &b.Color, &b.Icon, &b.HideLabel, &b.Position, &created, &updated); err != nil {
		return Button{}, err
	}
	b.CreatedAt, _ = storage.ParseTimestamp(created)
	b.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return b, nil
}

// List returns every button, in its order.
func (s *Service) List(ctx context.Context) ([]Button, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM buttons ORDER BY position, id")
	if err != nil {
		return nil, fmt.Errorf("buttons: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Button{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("buttons: list: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Get returns one button.
func (s *Service) Get(ctx context.Context, id string) (Button, error) {
	b, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM buttons WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Button{}, ErrNotFound
	}
	if err != nil {
		return Button{}, fmt.Errorf("buttons: get: %w", err)
	}
	return b, nil
}

// Save creates a button, last in the order, or changes one's label,
// colour and icon, keeping its place. It answers whether it was new.
func (s *Service) Save(ctx context.Context, b Button) (Button, bool, error) {
	if err := b.Check(); err != nil {
		return Button{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Button{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM buttons WHERE id = ?", b.ID))
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return Button{}, false, fmt.Errorf("buttons: save: %w", err)
	}
	ts := storage.Timestamp(s.now())
	switch {
	case created:
		var n, next int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MAX(position) + 1, 0) FROM buttons").Scan(&n, &next); err != nil {
			return Button{}, false, err
		}
		if n >= MaxButtons {
			return Button{}, false, fmt.Errorf("%w: up to %d buttons; delete one first", ErrFull, MaxButtons)
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO buttons ("+columns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)", b.ID, b.Label, b.Color, b.Icon, b.HideLabel, next, ts, ts)
	case old.Label == b.Label && old.Color == b.Color && old.Icon == b.Icon && old.HideLabel == b.HideLabel:
		return old, false, nil
	default:
		_, err = tx.ExecContext(ctx, "UPDATE buttons SET label = ?, color = ?, icon = ?, hide_label = ?, updated_at = ? WHERE id = ?",
			b.Label, b.Color, b.Icon, b.HideLabel, ts, b.ID)
	}
	if err != nil {
		return Button{}, false, fmt.Errorf("buttons: save: %w", err)
	}
	saved, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM buttons WHERE id = ?", b.ID))
	if err != nil {
		return Button{}, false, err
	}
	return saved, created, tx.Commit()
}

// Move puts the buttons in the order given, which names each one once.
func (s *Service) Move(ctx context.Context, order []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM buttons").Scan(&n); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range order {
		seen[id] = true
	}
	if len(order) != n || len(seen) != n {
		return invalid("the new order names every button once")
	}
	for i, id := range order {
		r, err := tx.ExecContext(ctx, "UPDATE buttons SET position = ? WHERE id = ?", i, id)
		if err != nil {
			return fmt.Errorf("buttons: move: %w", err)
		}
		if k, _ := r.RowsAffected(); k == 0 {
			return invalid("there's no button %q", id)
		}
	}
	return tx.Commit()
}

// Delete removes a button. Workflows that start from it are kept; they
// just never start from it again.
func (s *Service) Delete(ctx context.Context, id string) (bool, error) {
	r, err := s.db.ExecContext(ctx, "DELETE FROM buttons WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("buttons: delete: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// Press records a press of the button, from where it came. A request id
// names the press: the same one again is the same press, so a retried
// request never runs workflows twice. Without one, each call is a press.
// It answers the event and whether it was a repeat.
func (s *Service) Press(ctx context.Context, id, from, request string) (Button, events.Record, bool, error) {
	if !slices.Contains(froms, from) {
		return Button{}, events.Record{}, false, invalid("a press comes from %s", strings.Join(froms, ", "))
	}
	if request != "" && (len(request) > maxRequest || !requestPattern.MatchString(request)) {
		return Button{}, events.Record{}, false, invalid("a request id is up to %d letters, digits, - and _", maxRequest)
	}
	b, err := s.Get(ctx, id)
	if err != nil {
		return Button{}, events.Record{}, false, err
	}
	if request == "" {
		request = events.NewID()
	}
	raw, err := json.Marshal(map[string]any{"button_id": b.ID, "label": b.Label, "from": from})
	if err != nil {
		return Button{}, events.Record{}, false, err
	}
	// When the button was made names it: one deleted and made again under
	// the same id is a new button.
	sourceID := b.ID + ":" + strconv.FormatInt(b.CreatedAt.UnixMilli(), 36) + ":" + request
	// A repeat is the press already stored, whenever it comes.
	if rec, err := s.inbox.BySource(ctx, "buttons", sourceID); err == nil {
		return b, rec, true, nil
	} else if !errors.Is(err, events.ErrNotFound) {
		return Button{}, events.Record{}, false, fmt.Errorf("buttons: look for a press: %w", err)
	}
	res, err := s.inbox.Accept(ctx, events.Envelope{
		Type: Pressed, Source: "buttons",
		SourceEventID:   sourceID,
		OccurredAt:      s.now(),
		Payload:         raw,
		DeliveryQuality: events.Confirmed,
	})
	if errors.Is(err, events.ErrConflict) {
		// The same request at the same moment, stored first by the other.
		if rec, err := s.inbox.BySource(ctx, "buttons", sourceID); err == nil {
			return b, rec, true, nil
		}
	}
	if err != nil {
		return Button{}, events.Record{}, false, fmt.Errorf("buttons: record a press of %s: %w", b.ID, err)
	}
	return b, res.Record, res.Duplicate, nil
}

// Sample is the event a press of b from there would record, without
// recording it: to ask which workflows it would start.
func Sample(b Button, from string) (events.Record, error) {
	raw, err := json.Marshal(map[string]any{"button_id": b.ID, "label": b.Label, "from": from})
	if err != nil {
		return events.Record{}, err
	}
	return events.Record{Envelope: events.Envelope{Type: Pressed, Source: "buttons", Payload: raw, DeliveryQuality: events.Confirmed}}, nil
}
