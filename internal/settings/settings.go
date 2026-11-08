// Package settings stores small, typed settings documents in the settings table.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Theme is the desktop app's colour theme.
type Theme string

const (
	ThemeSystem   Theme = "system" // follow the OS: dark or light
	ThemeDark     Theme = "dark"
	ThemeLight    Theme = "light"
	ThemeMidnight Theme = "midnight"
)

// Valid reports whether t is a known theme.
func (t Theme) Valid() bool {
	switch t {
	case ThemeSystem, ThemeDark, ThemeLight, ThemeMidnight:
		return true
	}
	return false
}

// UI holds the desktop app's appearance settings.
type UI struct {
	Theme Theme `json:"theme"`
}

// DefaultUI is used until the streamer changes anything.
var DefaultUI = UI{Theme: ThemeSystem}

// ErrInvalid wraps validation failures, so the API can answer 400.
var ErrInvalid = errors.New("invalid setting")

// Store reads and writes settings.
type Store struct {
	db  *storage.DB
	now func() time.Time
}

// NewStore returns a Store on db.
func NewStore(db *storage.DB) *Store { return &Store{db: db, now: time.Now} }

const keyUI = "ui"

// UI returns the appearance settings, or the defaults if none are saved.
func (s *Store) UI(ctx context.Context) (UI, error) {
	ui := DefaultUI
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", keyUI).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ui, nil
	}
	if err != nil {
		return ui, fmt.Errorf("settings: read ui: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &ui); err != nil {
		return DefaultUI, fmt.Errorf("settings: decode ui: %w", err)
	}
	if !ui.Theme.Valid() {
		ui.Theme = DefaultUI.Theme
	}
	return ui, nil
}

// SetUI validates and saves the appearance settings. Saving the same value
// twice is harmless.
func (s *Store) SetUI(ctx context.Context, ui UI) (UI, error) {
	if !ui.Theme.Valid() {
		return UI{}, fmt.Errorf("%w: theme must be system, dark, light or midnight", ErrInvalid)
	}
	raw, err := json.Marshal(ui)
	if err != nil {
		return UI{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		keyUI, string(raw), storage.Timestamp(s.now()))
	if err != nil {
		return UI{}, fmt.Errorf("settings: save ui: %w", err)
	}
	return ui, nil
}

// Load reads the settings document stored under key into v. It reports
// false, leaving v alone, when nothing is saved.
func (s *Store) Load(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settings: read %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("settings: decode %s: %w", key, err)
	}
	return true, nil
}

// Save stores v as the settings document under key. Never put secrets here:
// settings go into backups and exports.
func (s *Store) Save(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(raw), storage.Timestamp(s.now()))
	if err != nil {
		return fmt.Errorf("settings: save %s: %w", key, err)
	}
	return nil
}
