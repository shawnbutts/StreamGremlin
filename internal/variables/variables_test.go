package variables

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func open(t *testing.T, dir string) (*Store, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, db := open(t, t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	return s
}

var you = Change{By: "you"}

func g(name string) Key { return Key{Scope: Global, Name: name} }

func TestKeysAreChecked(t *testing.T) {
	bad := []Key{
		{Scope: "everyone", Name: "x"},
		{Scope: Global, Owner: "me", Name: "x"},
		{Scope: Viewer, Owner: "12345", Name: "x"}, // no platform
		{Scope: Workflow, Name: "x"},               // no workflow
		g("Deaths"), g("giveaway.open"), g("1st"), g(""), g(strings.Repeat("a", 65)),
	}
	for _, k := range bad {
		if err := k.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", k, err)
		}
	}
	for _, k := range []Key{g("deaths"), g("giveaway_open"), {Scope: Viewer, Owner: "twitch:1", Name: "lurks"}, {Scope: Workflow, Owner: "chat.hello", Name: "n2"}} {
		if err := k.Check(); err != nil {
			t.Errorf("%+v: %v", k, err)
		}
	}
}

func TestValuesKeepTheirTypesAndSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	s, db := open(t, dir)
	ctx := context.Background()
	values := map[string]any{
		"count": int64(3), "ratio": 0.5, "title": "Speedrun", "open": true,
		"list": []any{int64(1), "two"}, "map": map[string]any{"a": int64(1)},
	}
	for n, v := range values {
		if _, err := s.Set(ctx, g(n), v, you); err != nil {
			t.Fatal(n, err)
		}
	}
	_ = db.Close()
	s, db = open(t, dir) // a restart
	defer db.Close()
	got, err := s.Load(ctx, Global, "")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(values) {
		t.Fatalf("got %v, want %v", got, values)
	}
	if _, ok := got["count"].(int64); !ok {
		t.Fatalf("a whole number came back as %T", got["count"])
	}
	v, err := s.Get(ctx, g("title"))
	if err != nil || v.UpdatedBy != "you" || v.UpdatedAt.IsZero() {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := s.Get(ctx, g("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestValueLimits(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Set(ctx, g("big"), strings.Repeat("x", MaxValueBytes), you); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too big: %v", err)
	}
	for i := range MaxNamesPerOwner {
		if _, err := s.Set(ctx, g(fmt.Sprintf("v%d", i)), int64(i), you); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Set(ctx, g("one_more"), int64(1), you); !errors.Is(err, ErrFull) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := s.Set(ctx, g("v1"), int64(9), you); err != nil {
		t.Fatalf("changing one at the limit: %v", err)
	}
	// Other sets have their own limit.
	if _, err := s.Set(ctx, Key{Scope: Workflow, Owner: "w", Name: "one_more"}, int64(1), you); err != nil {
		t.Fatal(err)
	}
}

func TestAddCountsExactly(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if prev, v, err := s.Add(ctx, g("deaths"), int64(1), you); err != nil || prev != nil || v != int64(1) {
		t.Fatalf("%v %v %v", prev, v, err)
	}
	if _, v, _ := s.Add(ctx, g("deaths"), int64(-3), you); v != int64(-2) {
		t.Fatal(v)
	}
	if _, v, _ := s.Add(ctx, g("deaths"), 0.5, you); v != -1.5 {
		t.Fatal(v)
	}
	_, _ = s.Set(ctx, g("title"), "hi", you)
	if _, _, err := s.Add(ctx, g("title"), int64(1), you); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "text") {
		t.Fatalf("adding to text: %v", err)
	}
	_, _ = s.Set(ctx, g("huge"), int64(1<<62), you)
	if _, _, err := s.Add(ctx, g("huge"), int64(1<<62), you); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overflow: %v", err)
	}
	// Many runs counting at once never lose a count.
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Add(ctx, g("hits"), int64(1), you); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if v, _ := s.Get(ctx, g("hits")); v.Value != int64(40) {
		t.Fatalf("40 adds at once: %v", v.Value)
	}
}

func TestListDeleteAndViewerNames(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	fan := Key{Scope: Viewer, Owner: "twitch:1", Name: "lurks"}
	_, _ = s.Set(ctx, g("deaths"), int64(1), you)
	_, _ = s.Set(ctx, g("giveaway_open"), true, you)
	_, _ = s.Set(ctx, fan, int64(1), Change{By: "workflow:lurk", OwnerLabel: "Fan"})
	_, _ = s.Set(ctx, fan, int64(2), you) // no name given: the last one seen stays
	_, _ = s.Set(ctx, Key{Scope: Workflow, Owner: "quote", Name: "last"}, int64(4), you)

	all, err := s.List(ctx, Filter{})
	if err != nil || len(all) != 4 || all[0].Name != "deaths" || all[2].Scope != Viewer || all[2].OwnerLabel != "Fan" || all[3].Scope != Workflow {
		t.Fatalf("%+v %v", all, err)
	}
	for f, want := range map[Filter]int{
		{Scope: Global}: 2, {Scope: Viewer}: 1, {Owner: "quote"}: 1,
		{Query: "fan"}: 1, {Query: "GIVE"}: 1, {Query: "_"}: 1, {Query: "%"}: 0,
	} {
		if got, _ := s.List(ctx, f); len(got) != want {
			t.Errorf("%+v: %d, want %d", f, len(got), want)
		}
	}
	if ok, err := s.Delete(ctx, g("deaths")); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.Delete(ctx, g("deaths")); ok {
		t.Fatal("deleted twice")
	}
}
