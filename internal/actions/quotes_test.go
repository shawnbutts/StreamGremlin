package actions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/quotes"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

func quoteRig(t *testing.T) (*workflow.Registry, *quotes.Store) {
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
	qs := quotes.NewStore(db)
	reg := workflow.NewRegistry()
	Register(reg, Services{Quotes: qs})
	return reg, qs
}

func run(t *testing.T, reg *workflow.Registry, name string, c workflow.Call) map[string]any {
	t.Helper()
	a, ok := reg.Get(name)
	if !ok {
		t.Fatal(name)
	}
	out, err := a.Run(context.Background(), c)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.(map[string]any)
}

func TestQuoteSteps(t *testing.T) {
	reg, qs := quoteRig(t)
	ctx := context.Background()
	call := func(args map[string]any, key string, dry bool) workflow.Call {
		return workflow.Call{Args: args, IdempotencyKey: key, WorkflowID: "chat.quoteadd", DryRun: dry}
	}

	// A test run says what it would add, and adds nothing.
	out := run(t, reg, "quotes.add", call(map[string]any{"text": "a test"}, "j0/s", true))
	if out["id"] != int64(1) || out["test"] != true || out["by"] != "you" {
		t.Fatalf("%v", out)
	}
	if n, _ := qs.Count(ctx); n != 0 {
		t.Fatal("a test added a quote")
	}
	// Added for real, once even when the step is retried.
	for range 2 {
		out = run(t, reg, "quotes.add", call(map[string]any{"text": "I meant to do that", "by": "Mod"}, "j1/s", false))
	}
	if out["id"] != int64(1) || out["text"] != "I meant to do that" || out["by"] != "Mod" || out["count"] != 1 {
		t.Fatalf("%v", out)
	}
	_, _, _ = qs.Add(ctx, quotes.New{Text: "second"}, time.Now())

	// By number, as chat types it, or at random.
	for _, n := range []any{"1", "#1", int64(1)} {
		if got := run(t, reg, "quotes.get", call(map[string]any{"number": n}, "", false)); got["found"] != true || got["id"] != int64(1) || got["date"] == "" {
			t.Fatalf("%v: %v", n, got)
		}
	}
	if got := run(t, reg, "quotes.get", call(map[string]any{}, "", false)); got["found"] != true || got["count"] != 2 {
		t.Fatalf("random: %v", got)
	}
	for _, n := range []any{"9", "@friend"} {
		if got := run(t, reg, "quotes.get", call(map[string]any{"number": n}, "", false)); got["found"] != false || got["count"] != 2 {
			t.Fatalf("%v: %v", n, got)
		}
	}

	// Deleting: a test only says; then for real; then nothing left to delete.
	if got := run(t, reg, "quotes.delete", call(map[string]any{"number": "#1"}, "", true)); got["deleted"] != true || got["test"] != true {
		t.Fatalf("%v", got)
	}
	if _, err := qs.Get(ctx, 1); err != nil {
		t.Fatal("a test deleted a quote")
	}
	if got := run(t, reg, "quotes.delete", call(map[string]any{"number": "1"}, "", false)); got["deleted"] != true {
		t.Fatalf("%v", got)
	}
	if got := run(t, reg, "quotes.delete", call(map[string]any{"number": "1"}, "", false)); got["deleted"] != false {
		t.Fatalf("%v", got)
	}
	if got := run(t, reg, "quotes.delete", call(map[string]any{"number": "x"}, "", false)); got["deleted"] != false {
		t.Fatalf("%v", got)
	}
}
