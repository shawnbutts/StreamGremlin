package settings

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return NewStore(db)
}

func TestUIDefaultsThenSaves(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ui, err := s.UI(ctx)
	if err != nil || !reflect.DeepEqual(ui, DefaultUI()) || !reflect.DeepEqual(ui.More, []string{"timers", "quotes", "rewards"}) {
		t.Fatalf("default: %+v, %v", ui, err)
	}
	want := UI{Theme: ThemeMidnight, TextSize: "large", Density: "compact", More: []string{"rewards", "connections", "rewards"}}
	for i := 0; i < 2; i++ { // saving twice is harmless
		if _, err := s.SetUI(ctx, want); err != nil {
			t.Fatal(err)
		}
	}
	ui, err = s.UI(ctx)
	// More comes back in sidebar order, once each.
	if err != nil || ui.Theme != ThemeMidnight || ui.TextSize != "large" || ui.Density != "compact" ||
		!reflect.DeepEqual(ui.More, []string{"connections", "rewards"}) {
		t.Fatalf("after save: %+v, %v", ui, err)
	}
	// Nothing under More is a choice too.
	empty := ui
	empty.More = []string{}
	if _, err := s.SetUI(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if ui, _ = s.UI(ctx); len(ui.More) != 0 || ui.More == nil {
		t.Fatalf("%+v", ui)
	}
}

// Settings saved before text size, spacing and More existed get their
// defaults; so does anything unreadable.
func TestOlderUISettingsGetDefaults(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value, updated_at) VALUES ('ui', ?, '2026-11-01T00:00:00Z')",
		`{"theme":"light","text_size":"huge","more":["home","quotes"]}`); err != nil {
		t.Fatal(err)
	}
	ui, err := s.UI(ctx)
	if err != nil || ui.Theme != ThemeLight || ui.TextSize != "standard" || ui.Density != "comfortable" ||
		!reflect.DeepEqual(ui.More, []string{"quotes"}) {
		t.Fatalf("%+v %v", ui, err)
	}
}

func TestUIRejectsWhatItCantUse(t *testing.T) {
	s := newStore(t)
	for _, bad := range []func(*UI){
		func(u *UI) { u.Theme = "purple" },
		func(u *UI) { u.TextSize = "huge" },
		func(u *UI) { u.Density = "airy" },
		func(u *UI) { u.More = []string{"home"} },      // always shown
		func(u *UI) { u.More = []string{"workflows"} }, // always shown
		func(u *UI) { u.More = []string{"nope"} },
	} {
		ui := DefaultUI()
		bad(&ui)
		if _, err := s.SetUI(context.Background(), ui); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: got %v, want ErrInvalid", ui, err)
		}
	}
}
