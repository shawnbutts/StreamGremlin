package actions

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/links"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// !link from chat: naming the other account gives a code, which that
// account types; every reply is words for chat, never a failed step.
func TestLinkStep(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	vs := viewers.New(db, events.NewInbox(db, nil), nil)
	ledger := points.New(db, nil)
	reg := workflow.NewRegistry()
	Register(reg, Services{Links: links.New(db, vs, ledger, nil), Viewers: vs, Points: ledger})
	a, ok := reg.Get("viewers.link")
	if !ok {
		t.Fatal("no viewers.link")
	}
	call := func(actor events.Actor, text, key string) map[string]any {
		t.Helper()
		out, err := a.Run(ctx, workflow.Call{Args: map[string]any{"text": text}, IdempotencyKey: key, Actor: &actor,
			Event: map[string]any{"source": actor.Platform}})
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)
	}
	tw := events.Actor{Platform: "twitch", ID: "1", Login: "ann", DisplayName: "Ann"}
	yt := events.Actor{Platform: "youtube", ID: "UCann", Login: "ann", DisplayName: "@Ann"}
	if m := call(tw, "", "k0")["message"].(string); !strings.Contains(m, "!link @yourhandle") {
		t.Fatal(m)
	}
	out := call(tw, "@ann", "k1")
	m := out["message"].(string)
	code := regexp.MustCompile(`!link (\d{6})`).FindStringSubmatch(m)
	if code == nil || !strings.Contains(m, "in YouTube chat as @ann") {
		t.Fatal(m)
	}
	if m := call(yt, "000000", "k2")["message"].(string); !strings.HasPrefix(m, "That code isn't right") {
		t.Fatal(m)
	}
	out = call(yt, code[1], "k3")
	if out["linked"] != true || !strings.Contains(out["message"].(string), "Ann on Twitch and @Ann on YouTube") {
		t.Fatal(out)
	}
	if m := call(yt, "@ann", "k4")["message"].(string); !strings.HasPrefix(m, "You're already linked to Ann on Twitch") {
		t.Fatal(m)
	}
	dry, _ := a.Run(ctx, workflow.Call{Args: map[string]any{"text": "@x"}, DryRun: true, Actor: &tw})
	if dry.(map[string]any)["test"] != true {
		t.Fatal(dry)
	}
}
