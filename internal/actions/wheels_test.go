package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/wheels"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// "Spin a wheel" waits until the wheel stops and gives its result; the same
// step again (a retried run) finds its spin; it's External, so a test
// event never spins. "Say a wheel's odds" gives them as text.
func TestWheelSteps(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	svc := wheels.New(wheels.Options{DB: db})
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if _, err := svc.Create(ctx, "coin", wheels.Settings{Title: "Coin", Choices: []wheels.Choice{{Label: "Heads"}, {Label: "Tails"}}}); err != nil {
		t.Fatal(err)
	}
	reg := workflow.NewRegistry()
	Register(reg, Services{Wheels: svc})
	if a, _ := reg.Get("wheels.spin"); !a.External {
		t.Fatal("spinning must be External")
	}
	out := run(t, reg, "wheels.spin", workflow.Call{IdempotencyKey: "j/1", WorkflowID: "chat.spin", Args: map[string]any{"wheel": "coin"}})
	if l := out["label"]; l != "Heads" && l != "Tails" || out["display"] != "no_audience" || out["repeated"] != false {
		t.Fatal(out)
	}
	again := run(t, reg, "wheels.spin", workflow.Call{IdempotencyKey: "j/1", Args: map[string]any{"wheel": "coin"}})
	if again["spin_id"] != out["spin_id"] || again["repeated"] != true {
		t.Fatal(again)
	}
	if spins, _ := svc.Spins(ctx, "coin", 10); len(spins) != 1 || spins[0].StartedBy != "workflow chat.spin" {
		t.Fatal(spins)
	}
	odds := run(t, reg, "wheels.odds", workflow.Call{Args: map[string]any{"wheel": "coin"}})
	if odds["text"] != "Coin: Heads 50%, Tails 50%" {
		t.Fatal(odds)
	}
	a, _ := reg.Get("wheels.spin")
	if _, err := a.Run(ctx, workflow.Call{Args: map[string]any{"wheel": "nope"}}); err == nil || !strings.Contains(err.Error(), "no such wheel") {
		t.Fatal(err)
	}
}
