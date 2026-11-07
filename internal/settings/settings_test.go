package settings

import (
	"context"
	"errors"
	"path/filepath"
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
	if err != nil || ui != DefaultUI {
		t.Fatalf("default: %+v, %v", ui, err)
	}
	for i := 0; i < 2; i++ { // saving twice is harmless
		if _, err := s.SetUI(ctx, UI{Theme: ThemeMidnight}); err != nil {
			t.Fatal(err)
		}
	}
	ui, err = s.UI(ctx)
	if err != nil || ui.Theme != ThemeMidnight {
		t.Fatalf("after save: %+v, %v", ui, err)
	}
}

func TestUIRejectsUnknownTheme(t *testing.T) {
	s := newStore(t)
	_, err := s.SetUI(context.Background(), UI{Theme: "purple"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}
