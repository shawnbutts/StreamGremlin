// Package settings stores small, typed settings documents in the settings table.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// UI holds the desktop app's appearance settings
// (docs/adr/0019-app-comfort.md).
type UI struct {
	Theme Theme `json:"theme"`
	// TextSize scales the whole app: small, standard, large or larger.
	TextSize string `json:"text_size"`
	// Density is comfortable or compact (less space around things).
	Density string `json:"density"`
	// More lists the screens shown under "More…" in the sidebar, by id.
	// Screens there work as usual; they're just out of the way.
	More []string `json:"more"`
}

// Text sizes and densities.
var (
	TextSizes = []string{"small", "standard", "large", "larger"}
	Densities = []string{"comfortable", "compact"}
	// MovableScreens can go under "More…". Home, Events, Workflows,
	// Settings and Help always stay in the sidebar.
	MovableScreens = []string{"connections", "timers", "quotes", "sounds", "overlays", "giveaways", "points", "viewers", "wheels", "rewards", "polls", "variables", "addons"}
)

// DefaultUI is used until the streamer changes anything. Settings saved
// before a field existed get its default.
func DefaultUI() UI {
	return UI{Theme: ThemeSystem, TextSize: "standard", Density: "comfortable", More: []string{"timers", "quotes", "wheels", "rewards", "polls"}}
}

// Check reports what's wrong with the settings, in words, and puts More in
// sidebar order without repeats.
func (ui *UI) Check() error {
	switch {
	case !ui.Theme.Valid():
		return fmt.Errorf("%w: theme must be system, dark, light or midnight", ErrInvalid)
	case !slices.Contains(TextSizes, ui.TextSize):
		return fmt.Errorf("%w: text size must be small, standard, large or larger", ErrInvalid)
	case !slices.Contains(Densities, ui.Density):
		return fmt.Errorf("%w: spacing must be comfortable or compact", ErrInvalid)
	}
	more := []string{}
	for _, id := range MovableScreens {
		if slices.Contains(ui.More, id) {
			more = append(more, id)
		}
	}
	for _, id := range ui.More {
		if !slices.Contains(MovableScreens, id) {
			return fmt.Errorf("%w: %q can't go under More; only %s can", ErrInvalid, id, strings.Join(MovableScreens, ", "))
		}
	}
	ui.More = more
	return nil
}

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
	ui := DefaultUI()
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", keyUI).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ui, nil
	}
	if err != nil {
		return ui, fmt.Errorf("settings: read ui: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &ui); err != nil {
		return DefaultUI(), fmt.Errorf("settings: decode ui: %w", err)
	}
	// Anything unreadable falls back to its default, one field at a time.
	def := DefaultUI()
	if !ui.Theme.Valid() {
		ui.Theme = def.Theme
	}
	if !slices.Contains(TextSizes, ui.TextSize) {
		ui.TextSize = def.TextSize
	}
	if !slices.Contains(Densities, ui.Density) {
		ui.Density = def.Density
	}
	if ui.More == nil {
		ui.More = def.More
	}
	ui.More = slices.DeleteFunc(slices.Clone(ui.More), func(id string) bool { return !slices.Contains(MovableScreens, id) })
	return ui, nil
}

// SetUI validates and saves the appearance settings. Saving the same value
// twice is harmless.
func (s *Store) SetUI(ctx context.Context, ui UI) (UI, error) {
	if err := ui.Check(); err != nil {
		return UI{}, err
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
