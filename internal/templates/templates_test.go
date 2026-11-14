package templates_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/actions"
	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/quotes"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/templates"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// fill puts something in every blank, the way the streamer would: an
// overlay's name, or "Be Right Back" for a scene.
func fill(t *testing.T, tp templates.Template) []byte {
	t.Helper()
	var d any
	if err := json.Unmarshal(tp.Workflow, &d); err != nil {
		t.Fatal(err)
	}
	for _, b := range tp.Blanks {
		parent, key := walk(t, d, b.Path)
		v, ok := parent[key].(string)
		if !ok || v != "" {
			t.Fatalf("%s: the blank %s isn't an empty string: %#v", tp.ID, b.Path, parent[key])
		}
		parent[key] = "Be Right Back"
		if key == "overlay" {
			parent[key] = "alerts"
		}
	}
	out, _ := json.Marshal(d)
	return out
}

// walk finds the map holding the last part of a $.a[0].b path.
func walk(t *testing.T, d any, path string) (map[string]any, string) {
	t.Helper()
	parts := strings.Split(strings.ReplaceAll(strings.TrimPrefix(path, "$."), "[", ".["), ".")
	cur := d
	for i, p := range parts {
		if strings.HasPrefix(p, "[") {
			var n int
			_, _ = fmt.Sscanf(p, "[%d]", &n)
			cur = cur.([]any)[n]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: no %s", path, p)
		}
		if i == len(parts)-1 {
			return m, p
		}
		cur = m[p]
	}
	t.Fatalf("bad path %s", path)
	return nil, ""
}

// Every template is valid for the real engine, can be on together with the
// others, and runs to the end for a test event.
func TestEveryTemplateWorks(t *testing.T) {
	all, err := templates.All()
	if err != nil || len(all) < 8 {
		t.Fatalf("%d templates, %v", len(all), err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	reg := workflow.NewRegistry()
	// Twitch and OBS are nil: test events never reach them. Quotes are
	// StreamGremlin's own, read even by test runs, and alerts show in
	// previews.
	sec, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	ovs := overlays.NewStore(db, sec)
	if _, _, err := ovs.Create(ctx, "alerts", overlays.KindAlert, overlays.DefaultLook(), time.Now()); err != nil {
		t.Fatal(err)
	}
	actions.Register(reg, actions.Services{Quotes: quotes.NewStore(db), Overlays: ovs, Hub: overlays.NewHub(nil),
		Viewers: viewers.New(db, events.NewInbox(db, nil), nil), Points: points.New(db, nil)})
	inbox := events.NewInbox(db, nil)
	eng := workflow.New(workflow.Options{DB: db, Inbox: inbox, Actions: reg, Variables: variables.New(db)})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	ids, wfIDs := map[string]bool{}, map[string]bool{}
	var claims []commands.Claim
	var sims []events.Simulation
	for _, tp := range all {
		if ids[tp.ID] || tp.Title == "" || tp.Description == "" || !contains(templates.Categories, tp.Category) {
			t.Errorf("%s: %+v", tp.ID, tp)
		}
		ids[tp.ID] = true
		raw := fill(t, tp)
		d, err := workflow.Parse(raw, reg)
		if err != nil {
			t.Fatalf("%s: %v", tp.ID, err)
		}
		if wfIDs[d.ID] {
			t.Errorf("two templates make workflow %s", d.ID)
		}
		wfIDs[d.ID] = true
		// Templates can all be on at once: no two answer one command.
		mine := workflow.ClaimsOf(d.ID, d)
		if cs := commands.Conflicts(mine, claims); len(cs) > 0 {
			t.Errorf("%s: %+v", tp.ID, cs)
		}
		claims = append(claims, mine...)
		if _, _, err := eng.Save(ctx, raw); err != nil {
			t.Fatal(err)
		}
		if err := eng.SetEnabled(ctx, d.ID, true); err != nil {
			t.Fatal(err)
		}
		// A test event that matches its first trigger.
		tr := d.Triggers[0]
		fields := map[string]string{}
		var where map[string]any
		b, _ := json.Marshal(tr.Where)
		_ = json.Unmarshal(b, &where)
		if c, ok := where["command"].(string); ok {
			fields["command"], fields["args"] = "!"+c, "@friend extra"
		}
		if r, ok := where["chatter.role"].([]any); ok {
			fields["role"] = r[0].(string)
		}
		sim := events.Simulation{Type: tr.Type, Fields: fields, RequestID: tp.ID}
		if len(tr.Sources) > 0 {
			sim.Source = tr.Sources[0] // a YouTube-only template hears a YouTube test event
		}
		sims = append(sims, sim)
	}
	for _, s := range sims {
		env, err := s.Build()
		if err != nil {
			t.Fatal(s.Type, err)
		}
		if _, err := inbox.Accept(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for id := range wfIDs {
		for {
			js, _ := eng.Jobs(ctx, id, 5)
			if len(js) > 0 && js[0].Status != workflow.JobPending && js[0].Status != workflow.JobRunning {
				if js[0].Status != workflow.JobSucceeded || !js[0].DryRun {
					_, steps, _ := eng.Trace(ctx, js[0].ID)
					t.Errorf("%s: %+v %+v", id, js[0], steps)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s didn't run: %+v", id, js)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
