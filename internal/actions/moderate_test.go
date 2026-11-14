package actions

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

type modFakes struct {
	mu sync.Mutex
	tw []twitch.Mod
	yt []youtube.Mod
}

func (f *modFakes) Moderate(_ context.Context, m twitch.Mod) (twitch.ModOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tw = append(f.tw, m)
	return twitch.ModOutcome{State: twitch.ModDone}, twitch.CheckMod(m)
}

func (f *modFakes) ChangeChatSettings(context.Context, twitch.ChatSettingsChange) (twitch.ChatSettings, twitch.ModOutcome, error) {
	return twitch.ChatSettings{}, twitch.ModOutcome{}, nil
}

type ytFake struct{ f *modFakes }

func (y ytFake) Moderate(_ context.Context, m youtube.Mod) (youtube.ModOutcome, error) {
	y.f.mu.Lock()
	defer y.f.mu.Unlock()
	y.f.yt = append(y.f.yt, m)
	if m.Action == youtube.ModBan {
		return youtube.ModOutcome{State: youtube.ModRefused, Detail: "YouTube doesn't let you ban a moderator."}, nil
	}
	return youtube.ModOutcome{State: youtube.ModDone, ID: "ban1"}, nil
}

func TestModerateTheViewerStep(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	f := &modFakes{}
	mods := moderation.New(db, f, nil)
	mods.UseYouTube(ytFake{f})
	reg := workflow.NewRegistry()
	Register(reg, Services{Moderation: mods})
	a, ok := reg.Get("chat.moderate")
	if !ok || !a.External {
		t.Fatal("no chat.moderate, or not external")
	}
	call := func(source, id, key string, args map[string]any) workflow.Call {
		return workflow.Call{Args: args, IdempotencyKey: key, WorkflowID: "w",
			Actor: &events.Actor{Platform: source, ID: id, Login: "ann"},
			Event: map[string]any{"source": source, "payload": map[string]any{"message_id": "m-1"}}}
	}
	ctx := context.Background()

	// Twitch: a timeout, 10 minutes by default; the same step again sends nothing.
	for range 2 {
		if _, err := a.Run(ctx, call("twitch", "42", "job1/s1", map[string]any{"what": "timeout"})); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.tw) != 1 || f.tw[0].Action != twitch.ModTimeout || f.tw[0].Seconds != 600 || f.tw[0].UserID != "42" {
		t.Fatalf("%+v", f.tw)
	}
	// YouTube: delete the message being answered.
	if _, err := a.Run(ctx, call("youtube", "UCfan", "job2/s1", map[string]any{"what": "delete"})); err != nil {
		t.Fatal(err)
	}
	if len(f.yt) != 1 || f.yt[0].Action != youtube.ModDelete || f.yt[0].MessageID != "m-1" {
		t.Fatalf("%+v", f.yt)
	}
	// Refused: the step fails with why.
	if _, err := a.Run(ctx, call("youtube", "UCmod", "job3/s1", map[string]any{"what": "ban"})); err == nil || !strings.Contains(err.Error(), "moderator") {
		t.Fatal(err)
	}
	// No viewer, or not from chat.
	if _, err := a.Run(ctx, workflow.Call{Args: map[string]any{"what": "ban"}, IdempotencyKey: "j4", Event: map[string]any{"source": "obs"}}); err == nil {
		t.Fatal("moderated an OBS event")
	}
	list, _ := mods.List(ctx, "", "", 10)
	if len(list) != 3 || list[0].From != moderation.FromWorkflow {
		t.Fatalf("%+v", list)
	}
}
