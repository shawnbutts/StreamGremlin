package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// "Give points" to the event's viewer, recorded if new; the same step again
// gives nothing more; "Take points" refuses, changing nothing, when they
// haven't enough; "Check points" reads, by @name too; giving and taking
// are External, so a test never changes a real balance.
func TestPointsSteps(t *testing.T) {
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
	vs, ledger := viewers.New(db, events.NewInbox(db, nil), nil), points.New(db, nil)
	reg := workflow.NewRegistry()
	Register(reg, Services{Viewers: vs, Points: ledger})
	for _, name := range []string{"points.give", "points.take"} {
		if a, _ := reg.Get(name); !a.External {
			t.Fatal(name, "must be External")
		}
	}
	ann := &events.Actor{Platform: "twitch", ID: "1", Login: "ann", DisplayName: "Ann"}
	call := func(key string, args map[string]any) workflow.Call {
		return workflow.Call{IdempotencyKey: key, WorkflowID: "chat.thanks", Actor: ann, Args: args}
	}
	out := run(t, reg, "points.give", call("j/1", map[string]any{"amount": 50.0, "reason": "thanks"}))
	if out["balance"] != int64(50) || out["viewer"] != "Ann" || out["currency"] != "points" || out["repeated"] != false {
		t.Fatal(out)
	}
	if again := run(t, reg, "points.give", call("j/1", map[string]any{"amount": 50.0})); again["balance"] != int64(50) || again["repeated"] != true {
		t.Fatal("a retried step paid twice:", again)
	}
	take, _ := reg.Get("points.take")
	if _, err := take.Run(ctx, call("j/2", map[string]any{"amount": 80.0})); err == nil || !strings.Contains(err.Error(), "not enough points: has 50, needs 80") {
		t.Fatal(err)
	}
	if out := run(t, reg, "points.take", call("j/3", map[string]any{"amount": 20.0})); out["balance"] != int64(30) {
		t.Fatal(out)
	}
	if out := run(t, reg, "points.balance", workflow.Call{Args: map[string]any{"viewer": "@Ann"}}); out["balance"] != int64(30) {
		t.Fatal(out)
	}
	stranger := workflow.Call{Actor: &events.Actor{Platform: "twitch", ID: "2", Login: "bob"}, Args: map[string]any{}}
	if out := run(t, reg, "points.balance", stranger); out["balance"] != int64(0) {
		t.Fatal(out)
	}
	give, _ := reg.Get("points.give")
	for args, want := range map[*map[string]any]string{
		{"amount": 0.0}:                      "above 0",
		{"amount": 5.0, "viewer": "@nobody"}: "hasn't seen @nobody",
	} {
		if _, err := give.Run(ctx, call("j/9", *args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: %v", *args, err)
		}
	}
	if _, err := give.Run(ctx, workflow.Call{IdempotencyKey: "j/10", Args: map[string]any{"amount": 5.0}}); err == nil ||
		!strings.Contains(err.Error(), "no viewer") {
		t.Fatal(err)
	}
}
