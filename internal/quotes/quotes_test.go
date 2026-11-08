package quotes

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return NewStore(db)
}

var now = time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)

func TestAddNumbersAndNeverReuses(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if next, _ := s.NextNumber(ctx); next != 1 {
		t.Fatalf("next %d", next)
	}
	a, created, err := s.Add(ctx, New{Text: "  I meant   to do that ", AddedBy: "Fan", Category: "Celeste"}, now)
	if err != nil || !created || a.ID != 1 || a.Text != "I meant to do that" || a.AddedBy != "Fan" || a.Category != "Celeste" {
		t.Fatalf("%+v %v %v", a, created, err)
	}
	b, _, _ := s.Add(ctx, New{Text: "two"}, now)
	c, _, _ := s.Add(ctx, New{Text: "three"}, now)
	if b.ID != 2 || c.ID != 3 {
		t.Fatal(b.ID, c.ID)
	}
	if ok, _ := s.Delete(ctx, 3); !ok {
		t.Fatal("delete")
	}
	if ok, _ := s.Delete(ctx, 3); ok {
		t.Fatal("deleted twice")
	}
	if next, _ := s.NextNumber(ctx); next != 4 {
		t.Fatalf("next after delete %d", next)
	}
	d, _, _ := s.Add(ctx, New{Text: "four"}, now)
	if d.ID != 4 {
		t.Fatalf("a deleted number was reused: %d", d.ID)
	}
	if n, _ := s.Count(ctx); n != 3 {
		t.Fatalf("count %d", n)
	}
	if _, err := s.Get(ctx, 3); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestARepeatedAddReturnsTheFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make([]int64, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, _, err := s.Add(ctx, New{Text: "once", Key: "job:1/steps[0]"}, now)
			if err != nil {
				t.Error(err)
			}
			ids[i] = q.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("%v", ids)
		}
	}
	if n, _ := s.Count(ctx); n != 1 {
		t.Fatalf("%d quotes", n)
	}
}

func TestChecks(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, n := range []New{{Text: "  "}, {Text: strings.Repeat("x", MaxText+1)}, {Text: "x", AddedBy: strings.Repeat("y", MaxName+1)},
		{Text: "x", Category: strings.Repeat("z", MaxCategory+1)}, {Text: "x", Key: strings.Repeat("k", 201)}} {
		if _, _, err := s.Add(ctx, n, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%.40v accepted: %v", n, err)
		}
	}
	for in, want := range map[string]int64{"5": 5, "#12": 12, " 3 ": 3} {
		if n, ok := ParseNumber(in); !ok || n != want {
			t.Errorf("%q: %d %v", in, n, ok)
		}
	}
	for _, in := range []string{"", "0", "-1", "five", "#", "1.5"} {
		if _, ok := ParseNumber(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestRandomListAndEdit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Random(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, text := range []string{"alpha 100%", "beta_x", "gamma"} {
		if _, _, err := s.Add(ctx, New{Text: text, AddedBy: "Mod"}, now); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	for range 200 {
		q, err := s.Random(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seen[q.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("random picked %v", seen)
	}
	for q, want := range map[string]int{"": 3, "100%": 1, "_": 1, "mod": 3, "#2": 1, "ALPHA": 1, "nothing": 0} {
		list, err := s.List(ctx, q, 10)
		if err != nil || len(list) != want {
			t.Errorf("%q: %d %v", q, len(list), err)
		}
	}
	list, _ := s.List(ctx, "", 2)
	if len(list) != 2 || list[0].ID != 3 {
		t.Fatalf("newest first, limited: %+v", list)
	}
	later := now.Add(time.Hour)
	same, err := s.Edit(ctx, 1, "alpha 100%", "Mod", "", later)
	if err != nil || !same.UpdatedAt.Equal(now) {
		t.Fatalf("an unchanged edit: %+v %v", same, err)
	}
	e, err := s.Edit(ctx, 1, "alpha, fixed", "Streamer", "Just Chatting", later)
	if err != nil || e.ID != 1 || e.Text != "alpha, fixed" || e.Category != "Just Chatting" || !e.CreatedAt.Equal(now) || !e.UpdatedAt.Equal(later) {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := s.Edit(ctx, 99, "x", "", "", later); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Edit(ctx, 1, "", "", "", later); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
