package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/gives"
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

type fakeLists struct{ chat []events.Actor }

func (f fakeLists) Chatters(context.Context) ([]events.Actor, error)    { return f.chat, nil }
func (f fakeLists) Subscribers(context.Context) ([]events.Actor, error) { return nil, nil }

// "Give points to everyone": External; gives the group once each, a retried
// run giving nothing more; nobody in the group isn't a failure.
func TestGiveAllStep(t *testing.T) {
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
	lists := fakeLists{chat: []events.Actor{{Platform: "twitch", ID: "1", Login: "ann"}, {Platform: "twitch", ID: "2", Login: "bob"}}}
	gv := gives.New(gives.Options{DB: db, Viewers: vs, Points: ledger, Twitch: lists})
	reg := workflow.NewRegistry()
	Register(reg, Services{Viewers: vs, Points: ledger, Gives: gv})
	if a, _ := reg.Get("points.give_all"); !a.External {
		t.Fatal("must be External")
	}
	call := workflow.Call{IdempotencyKey: "j/1", WorkflowID: "twitch.raid-rain", Args: map[string]any{"group": "chat", "amount": 25.0, "reason": "raid!"}}
	out := run(t, reg, "points.give_all", call)
	if out["viewers"] != 2 || out["total"] != int64(50) || out["group"] != "everyone in chat" || out["repeated"] != false {
		t.Fatal(out)
	}
	if again := run(t, reg, "points.give_all", call); again["repeated"] != true || again["total"] != int64(50) {
		t.Fatal(again)
	}
	ann, _ := vs.ByLogin(ctx, "ann")
	if b, _ := ledger.Balance(ctx, ann.ID, ""); b != 25 {
		t.Fatal("paid twice:", b)
	}
	h, _ := ledger.History(ctx, ann.ID, 1)
	if h[0].By != "workflow twitch.raid-rain" || h[0].Memo != "raid!" {
		t.Fatal(h)
	}
	empty := workflow.Call{IdempotencyKey: "j/2", Args: map[string]any{"group": "subs", "amount": 5.0}}
	if out := run(t, reg, "points.give_all", empty); out["viewers"] != 0 || !strings.Contains(out["note"].(string), "no subscribers") {
		t.Fatal(out)
	}
}
