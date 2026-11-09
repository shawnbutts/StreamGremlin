package actions

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/sounds"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// "Show an alert": a test reaches previews only and plays nothing; a real
// one with no page open fails and says so; text is clipped, not refused.
func TestShowAnAlert(t *testing.T) {
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
	sec, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	store, hub, lib := overlays.NewStore(db, sec), overlays.NewHub(nil), sounds.NewStore(db)
	if _, _, err := store.Create(ctx, "alerts", overlays.KindAlert, overlays.DefaultLook(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.Add(ctx, "chime", []byte("RIFF\x24\x00\x00\x00WAVEfmt "+strings.Repeat("\x00", 64)), 70, time.Now()); err != nil {
		t.Fatal(err)
	}
	reg := workflow.NewRegistry()
	Register(reg, Services{Overlays: store, Hub: hub, Sounds: lib, Player: sounds.NewPlayer(nil)})
	a, _ := reg.Get("overlays.alert")
	if !a.External || !a.Preview {
		t.Fatal("Show an alert must be a Preview action")
	}

	out := run(t, reg, "overlays.alert", workflow.Call{DryRun: true, IdempotencyKey: "j/1",
		Args: map[string]any{"overlay": "alerts", "text": "Fan followed", "picture": true, "sound": "chime"}})
	res := out["result"].(overlays.Result)
	if !res.Test || res.Shown || out["sound"] != "would play chime" || out["picture"] != "not looked up for a test" ||
		!strings.HasPrefix(out["preview"].(string), "nobody saw it: no preview of alerts was open") {
		t.Fatalf("%+v", out)
	}

	_, err = a.Run(ctx, workflow.Call{IdempotencyKey: "j/2", Args: map[string]any{"overlay": "alerts", "text": strings.Repeat("x", 900)}})
	if !errors.Is(err, overlays.ErrNotOpen) || !strings.Contains(err.Error(), "isn't open in OBS") {
		t.Fatal(err)
	}
	for args, want := range map[*map[string]any]string{
		{"overlay": "nope", "text": "x"}:                   `no overlay "nope"`,
		{"overlay": "alerts", "text": "x", "sound": "boo"}: `no sound "boo"`,
		{"overlay": "alerts", "text": "x", "seconds": 1.0}: "2 to 60 seconds",
		{"overlay": "alerts", "text": "  "}:                "text is empty",
	} {
		if _, err := a.Run(ctx, workflow.Call{Args: *args}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: %v", *args, err)
		}
	}
	if got := clip(strings.Repeat("é", 600), overlays.MaxText); len([]rune(got)) != overlays.MaxText || !strings.HasSuffix(got, "…") {
		t.Fatal(len([]rune(got)))
	}
}
