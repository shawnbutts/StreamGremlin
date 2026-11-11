package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/packages"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/rounds"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// TestMain lets this test binary be the Lua worker, as the app is.
func TestMain(m *testing.M) {
	lua.RunWorkerIfAsked()
	os.Exit(m.Run())
}

type fakeChat struct {
	mu   sync.Mutex
	sent []twitch.SendRequest
	err  error
}

func (f *fakeChat) SendChat(_ context.Context, req twitch.SendRequest) (twitch.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return twitch.SendResult{}, f.err
	}
	f.sent = append(f.sent, req)
	return twitch.SendResult{MessageID: "m1"}, nil
}

func (f *fakeChat) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.sent {
		out = append(out, r.Text)
	}
	return out
}

type fakeWorkflows struct {
	list []workflow.Summary
	// uses, when set, is each workflow's step, by its id.
	uses map[string]string
	off  []string
}

func (f *fakeWorkflows) UsingActions(_ context.Context, prefix string) ([]workflow.Summary, error) {
	if f.uses == nil {
		return f.list, nil
	}
	out := []workflow.Summary{}
	for _, w := range f.list {
		if a := f.uses[w.ID]; a == prefix || strings.HasSuffix(prefix, ".") && strings.HasPrefix(a, prefix) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeWorkflows) UsingTriggers(_ context.Context, prefix string) ([]workflow.Summary, error) {
	out := []workflow.Summary{}
	for _, w := range f.list {
		if strings.HasPrefix(f.uses[w.ID], prefix) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeWorkflows) SetEnabled(_ context.Context, id string, on bool) error {
	f.off = append(f.off, id)
	return nil
}

// zipFolder zips a folder as a package.
func zipFolder(t *testing.T, dir string, change func(name string, b []byte) []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p) //nolint:gosec // the repository's own example
		if err != nil {
			return err
		}
		if change != nil {
			b = change(filepath.ToSlash(rel), b)
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var example = filepath.Join("..", "..", "examples", "plugins", "dice-tray")

type rig struct {
	store *packages.Store
	reg   *workflow.Registry
	chat  *fakeChat
	inbox *fakeInbox
	m     *Manager
	now   time.Time
	// Points: the ledger, viewers and rounds, with rolls set by the test.
	ledger *points.Ledger
	vs     *viewers.Service
	rounds *rounds.Service
	rolls  []int
}

type fakeInbox struct {
	mu   sync.Mutex
	got  []events.Envelope
	seen map[string]bool
}

func (f *fakeInbox) Accept(_ context.Context, env events.Envelope) (events.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if env.SourceEventID != "" {
		if f.seen[env.SourceEventID] {
			return events.Result{}, events.ErrConflict
		}
		f.seen[env.SourceEventID] = true
	}
	f.got = append(f.got, env)
	return events.Result{}, nil
}

func (f *fakeInbox) types() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.got {
		out = append(out, e.Type)
	}
	return out
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{store: packages.NewStore(db), reg: workflow.NewRegistry(), chat: &fakeChat{}, inbox: &fakeInbox{seen: map[string]bool{}},
		now: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
	r.ledger, r.vs = points.New(db, nil), viewers.New(db, nil, nil)
	r.rounds = rounds.New(rounds.Options{DB: db, Ledger: r.ledger, Viewers: r.vs, Now: func() time.Time { return r.now }, Roll: func(int) int {
		v := r.rolls[0]
		r.rolls = r.rolls[1:]
		return v
	}})
	r.m = New(Options{Store: r.store, Actions: r.reg, Chat: r.chat, Inbox: r.inbox, Rounds: r.rounds, Now: func() time.Time { return r.now }})
	t.Cleanup(r.m.Close)
	return r
}

func (r *rig) install(t *testing.T, data []byte, perms ...string) {
	t.Helper()
	pv, _, err := r.store.Check(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.InstallWith(context.Background(), data, packages.Approval{Digest: pv.Digest, Permissions: perms}, r.now); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Installed(context.Background(), pv.ID); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) step(t *testing.T, name string, args map[string]any) (any, error) {
	t.Helper()
	a, ok := r.reg.Get(name)
	if !ok {
		t.Fatalf("no step %s", name)
	}
	return a.Run(context.Background(), workflow.Call{Args: args, IdempotencyKey: "j1/steps[0]", JobID: "j1", WorkflowID: "dice",
		Event: map[string]any{"actor": map[string]any{"display_name": "Fan"}}})
}

func activity(t *testing.T, r *rig, id string) []string {
	t.Helper()
	list, err := r.store.ActivityOf(context.Background(), id, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i].Call+" "+list[i].Outcome+": "+list[i].Detail)
	}
	return out
}

// The example plugin, from install to removal (docs/adr/0038-lua-plugins.md).
func TestTheExamplePlugin(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	data := zipFolder(t, example, nil)

	// Its permissions must be approved exactly.
	pv, _, err := r.store.Check(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Kind != "lua" || pv.Plugin == nil || len(pv.Plugin.Permissions) != 3 || pv.Plugin.Permissions[0].Name != "chat.send" ||
		!strings.Contains(pv.Plugin.Permissions[0].What, "Send messages in your chat") || len(pv.Plugin.Actions) != 5 {
		t.Fatalf("preview: %+v", pv.Plugin)
	}
	for _, perms := range [][]string{nil, {"chat.send"}, {"chat.send", "chat.send"}, {"chat.send", "points.give"}} {
		if _, err := r.store.InstallWith(ctx, data, packages.Approval{Digest: pv.Digest, Permissions: perms}, r.now); !errors.Is(err, packages.ErrConfirmPermissions) {
			t.Errorf("%v: %v", perms, err)
		}
	}
	r.install(t, data, "chat.send", "events.publish", "points.rounds")

	// Installed off: its steps are there, and fail saying so.
	a, ok := r.reg.Get("dice-tray.roll")
	if !ok || a.Group != "Dice tray" || a.AddOn != "dice-tray" || !a.External || !strings.Contains(a.Description, "published by StreamGremlin examples (not verified)") ||
		a.Param("who").Editor != "message" || a.Param("dice").Type != "text" || a.Param("dice").DefaultHint != "A number from 1 to 10" {
		t.Fatalf("step: %+v", a)
	}
	if _, err := r.step(t, "dice-tray.roll", nil); err == nil || !strings.Contains(err.Error(), "Dice tray is off: turn it on on Add-ons") {
		t.Fatalf("off: %v", err)
	}

	// On: it loads, rolls, keeps its state, and talks in chat.
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	out, err := r.step(t, "dice-tray.roll", map[string]any{"dice": "3"})
	res, _ := out.(map[string]any)
	if err != nil || res["who"] != "Fan" || len(res["each"].([]any)) != 3 || !strings.Contains(res["text"].(string), "(3d6)") {
		t.Fatalf("roll: %v %v", err, out)
	}
	if _, err := r.step(t, "dice-tray.roll", map[string]any{"dice": int64(11)}); err == nil || !strings.Contains(err.Error(), "from 1 to 10") {
		t.Errorf("11 dice: %v", err)
	}
	out, err = r.step(t, "dice-tray.roll_and_say", map[string]any{"who": "Pal"})
	if err != nil || out.(map[string]any)["said"] != true {
		t.Fatalf("roll and say: %v %v", err, out)
	}
	if sent := r.chat.texts(); len(sent) != 1 || !strings.HasPrefix(sent[0], "Pal rolled a ") {
		t.Errorf("chat: %q", sent)
	}
	if r.chat.sent[0].Workflow != "plugin:dice-tray" {
		t.Errorf("sent as %q: its echo would count as you chatting", r.chat.sent[0].Workflow)
	}
	out, _ = r.step(t, "dice-tray.best", nil)
	if out.(map[string]any)["rolls"] != int64(2) {
		t.Errorf("best: %v", out)
	}
	act := strings.Join(activity(t, r, "dice-tray"), "\n")
	if !strings.Contains(act, "log ok: Dice tray ready: d6") || !strings.Contains(act, "chat.send ok: Pal rolled a ") {
		t.Errorf("activity:\n%s", act)
	}

	// Settings restart it: the session starts over with 20 sides.
	if _, err := r.store.SetSettings(ctx, "dice-tray", map[string]any{"sides": float64(20)}, r.now); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Restart(ctx, "dice-tray"); err != nil {
		t.Fatal(err)
	}
	out, _ = r.step(t, "dice-tray.best", nil)
	if out.(map[string]any)["rolls"] != int64(0) {
		t.Errorf("after settings: %v", out)
	}
	out, _ = r.step(t, "dice-tray.roll", nil)
	if !strings.Contains(out.(map[string]any)["text"].(string), "(d20)") {
		t.Errorf("20 sides: %v", out)
	}

	// Off: its steps fail at once; the store says so.
	if err := r.m.SetEnabled(ctx, "dice-tray", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.step(t, "dice-tray.roll", nil); err == nil || !strings.Contains(err.Error(), "is off") {
		t.Errorf("off again: %v", err)
	}
	if p, _ := r.store.Get(ctx, "dice-tray"); p.Plugin.Enabled {
		t.Error("still on in the store")
	}

	// Removing turns off the workflows that use it, and takes its steps out.
	wf := &fakeWorkflows{list: []workflow.Summary{{ID: "chat.roll", Name: "Roll", Enabled: true}, {ID: "chat.best", Name: "Best"}}}
	names, err := r.m.Removing(ctx, "dice-tray", wf)
	if err != nil || strings.Join(names, ",") != "Roll,Best" || strings.Join(wf.off, ",") != "chat.roll" {
		t.Fatalf("removing: %v %v %v", names, wf.off, err)
	}
	if _, ok := r.reg.Get("dice-tray.roll"); ok {
		t.Error("its step is still there")
	}
}

// What a plugin may not do is refused, and recorded.
func TestPluginRefusals(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// The example without chat.send.
	noChat := zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "streamgremlin.json" {
			return bytes.Replace(b, []byte(`"chat.send", "events.publish", "points.rounds"`), []byte(`"events.publish", "points.rounds"`), 1)
		}
		return b
	})
	r.install(t, noChat, "events.publish", "points.rounds")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	out, err := r.step(t, "dice-tray.roll_and_say", nil)
	if err != nil || out.(map[string]any)["said"] != false {
		t.Fatalf("%v %v", err, out)
	}
	if len(r.chat.texts()) != 0 {
		t.Fatal("sent without permission")
	}
	act := strings.Join(activity(t, r, "dice-tray"), "\n")
	if !strings.Contains(act, "chat.send refused: Fan rolled") || !strings.Contains(act, "it doesn't have permission to send chat") ||
		!strings.Contains(act, "log ok: couldn't say it: it doesn't have permission") {
		t.Errorf("activity:\n%s", act)
	}
}

func TestChatLimits(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.install(t, zipFolder(t, example, nil), "chat.send", "events.publish", "points.rounds")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	for range ChatPerMinute + 2 {
		_, _ = r.step(t, "dice-tray.roll_and_say", nil)
	}
	if n := len(r.chat.texts()); n != ChatPerMinute {
		t.Errorf("%d sent in a minute", n)
	}
	r.now = r.now.Add(time.Minute)
	if out, _ := r.step(t, "dice-tray.roll_and_say", nil); out.(map[string]any)["said"] != true {
		t.Errorf("a minute later: %v", out)
	}
	// Twitch refusing is a failure, not a refusal.
	r.chat.err = twitch.ErrNotSignedIn
	r.now = r.now.Add(time.Minute)
	_, _ = r.step(t, "dice-tray.roll_and_say", nil)
	if act := strings.Join(activity(t, r, "dice-tray"), "\n"); !strings.Contains(act, "chat.send failed: ") || !strings.Contains(act, "at most 20 messages a minute") {
		t.Errorf("activity:\n%s", act)
	}
}

// A plugin that won't load is left off, saying why; one whose worker keeps
// dying is turned off as failed.
func TestPluginFailures(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	broken := zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "plugin.lua" {
			return append(b, []byte("\nlocal x = = 1\n")...)
		}
		return b
	})
	r.install(t, broken, "chat.send", "events.publish", "points.rounds")
	err := r.m.SetEnabled(ctx, "dice-tray", true)
	if !errors.Is(err, ErrLoad) || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("broken: %v", err)
	}
	p, _ := r.store.Get(ctx, "dice-tray")
	if p.Plugin.Enabled || !strings.Contains(p.Plugin.Error, "it didn't load") {
		t.Fatalf("state: %+v", p.Plugin)
	}
	if _, err := r.step(t, "dice-tray.roll", nil); err == nil || !strings.Contains(err.Error(), "it didn't load") {
		t.Errorf("step: %v", err)
	}
}

func TestPluginCrashesTurnItOff(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	bomb := zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "plugin.lua" {
			return []byte(`sg.action("roll", function() local t = {} for i = 1, 1e9 do t[i] = string.rep("x", 1000) .. i end end)
sg.action("best", function() return 1 end)`)
		}
		return b
	})
	r.install(t, bomb, "chat.send", "events.publish", "points.rounds")
	r.m.opts.Lua.Memory = 32 << 20
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	for i := range MaxCrashes {
		if _, err := r.step(t, "dice-tray.roll", nil); err == nil || !strings.Contains(err.Error(), "memory") {
			t.Fatalf("%d: %v", i, err)
		}
		if i < MaxCrashes-1 {
			if out, err := r.step(t, "dice-tray.best", nil); err != nil || out != int64(1) {
				t.Fatalf("started again after %d: %v %v", i, out, err)
			}
		}
	}
	p, _ := r.store.Get(ctx, "dice-tray")
	if p.Plugin.Enabled || !strings.Contains(p.Plugin.Error, "stopped 5 times in 10 minutes") {
		t.Fatalf("state: %+v", p.Plugin)
	}
}

// Plugins left on start with StreamGremlin.
func TestStartLoadsPluginsLeftOn(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.install(t, zipFolder(t, example, nil), "chat.send", "events.publish", "points.rounds")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	r.m.Close()
	reg := workflow.NewRegistry()
	m := New(Options{Store: r.store, Actions: reg, Chat: r.chat})
	t.Cleanup(m.Close)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a, ok := reg.Get("dice-tray.best")
	if !ok {
		t.Fatal("steps not registered at start")
	}
	if out, err := a.Run(ctx, workflow.Call{Event: map[string]any{}}); err != nil || out.(map[string]any)["rolls"] != int64(0) {
		t.Fatalf("after start: %v %v", out, err)
	}
}

// A plugin's templates are checked as the editor would save them: its own
// steps and StreamGremlin's, not another add-on's (D-071).
func TestPluginTemplatesAreChecked(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.reg.Add(workflow.Action{Name: "chat.send", Label: "Send", Group: "Twitch", External: true,
		Params: []workflow.Param{{Name: "message", Label: "Message", Type: "text", Required: true}}})
	r.reg.Add(workflow.Action{Name: "other.thing", Label: "Other", Group: "Other", AddOn: "other"})
	r.store.SetCheck(r.m.CheckTemplates)
	pv, _, err := r.store.Check(ctx, zipFolder(t, example, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Plugin.Templates) != 5 || pv.Plugin.Templates[0].Title != "!best: the session's best roll" {
		t.Fatalf("templates: %+v", pv.Plugin.Templates)
	}
	for _, c := range []struct{ from, to, want string }{
		{`"action": "dice-tray.best"`, `"action": "dice-tray.teleport"`, `no action "dice-tray.teleport"`},
		{`"action": "dice-tray.best"`, `"action": "other.thing"`, `no action "other.thing"`},
		{`"triggers"`, `"trigger"`, `template "best": $: not a workflow definition`},
	} {
		bad := zipFolder(t, example, func(name string, b []byte) []byte {
			if name == "templates/best.json" {
				return bytes.Replace(b, []byte(c.from), []byte(c.to), 1)
			}
			return b
		})
		if _, _, err := r.store.Check(ctx, bad); !errors.Is(err, packages.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.to, err, c.want)
		}
	}
	// A blank left empty isn't a problem.
	blank := zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "templates/best.json" {
			b = bytes.Replace(b, []byte(`"blanks": []`), []byte(`"blanks": [{"path": "$.steps[1].with.message", "hint": "What to say"}]`), 1)
			return bytes.Replace(b, []byte(`"${steps.best.output.text}"`), []byte(`""`), 1)
		}
		return b
	})
	if _, _, err := r.store.Check(ctx, blank); err != nil {
		t.Errorf("blank: %v", err)
	}

	// Installed: in the gallery, under the plugin's name, saying it's off.
	r.install(t, zipFolder(t, example, nil), "chat.send", "events.publish", "points.rounds")
	ts, err := r.store.AddOnTemplates(ctx)
	if err != nil || len(ts) != 5 || ts[3].ID != "dice-tray/roll" || ts[3].Category != "Dice tray" || ts[3].AddOn == nil || ts[3].AddOn.Enabled {
		t.Fatalf("gallery: %+v %v", ts, err)
	}
}

// "!roll" with nothing after it rolls the default, one die.
func TestEmptyNumberIsTheDefault(t *testing.T) {
	r := newRig(t)
	r.install(t, zipFolder(t, example, nil), "chat.send", "events.publish", "points.rounds")
	if err := r.m.SetEnabled(context.Background(), "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	out, err := r.step(t, "dice-tray.roll", map[string]any{"dice": " "})
	if err != nil || len(out.(map[string]any)["each"].([]any)) != 1 {
		t.Fatalf("%v %v", out, err)
	}
}

// Updating and rolling back (D-072): workflows using a step the new
// version drops are turned off; a plugin that's on is loaded again, fresh,
// as the new version.
func TestUpdatingAPlugin(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.install(t, zipFolder(t, example, nil), "chat.send", "events.publish", "points.rounds")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.step(t, "dice-tray.roll", nil); err != nil {
		t.Fatal(err)
	}
	// 1.4.0 drops "Roll dice", keeping "Roll dice and say it".
	v2 := zipFolder(t, example, func(name string, b []byte) []byte {
		switch name {
		case "streamgremlin.json":
			s := strings.Replace(string(b), `"version": "1.3.0"`, `"version": "1.4.0"`, 1)
			i := strings.Index(s, `{
        "name": "roll",`)
			j := strings.Index(s, `{
        "name": "roll_and_say",`)
			return []byte(s[:i] + s[j:])
		case "plugin.lua":
			return bytes.Replace(b, []byte(`sg.action("roll", function`), []byte(`local _ = (function`), 1)
		}
		return b
	})
	pv, _, err := r.store.Check(ctx, v2)
	if err != nil {
		t.Fatal(err)
	}
	if ch := pv.PluginUpdate; ch == nil || len(ch.RemovedSteps) != 1 || ch.RemovedSteps[0].Name != "roll" || len(ch.NewPermissions) != 0 {
		t.Fatalf("preview: %+v", pv.PluginUpdate)
	}
	if _, err := r.store.InstallWith(ctx, v2, packages.Approval{Digest: pv.Digest}, r.now); err != nil {
		t.Fatal(err)
	}
	wf := &fakeWorkflows{
		list: []workflow.Summary{{ID: "chat.roll", Name: "Roll", Enabled: true}, {ID: "chat.say", Name: "Say", Enabled: true}},
		uses: map[string]string{"chat.roll": "dice-tray.roll", "chat.say": "dice-tray.roll_and_say"},
	}
	off, err := r.m.Switched(ctx, "dice-tray", wf)
	if err != nil || strings.Join(off, ",") != "Roll" || strings.Join(wf.off, ",") != "chat.roll" {
		t.Fatalf("switched: %v %v %v", off, wf.off, err)
	}
	if _, ok := r.reg.Get("dice-tray.roll"); ok {
		t.Error("the dropped step is still there")
	}
	// Loaded again as 1.4.0: the session starts over.
	out, err := r.step(t, "dice-tray.best", nil)
	if err != nil || out.(map[string]any)["rolls"] != int64(0) {
		t.Fatalf("after the update: %v %v", out, err)
	}
	if list, _ := r.store.ActivityOf(ctx, "dice-tray", 1); len(list) == 0 || list[0].Version != "1.4.0" {
		t.Errorf("activity: %+v", list)
	}

	// Roll back: "Roll dice" is back, nothing is turned off.
	if _, err := r.store.RollbackWith(ctx, "dice-tray", nil, r.now); err != nil {
		t.Fatal(err)
	}
	wf.off = nil
	if off, err := r.m.Switched(ctx, "dice-tray", wf); err != nil || len(off) != 0 || len(wf.off) != 0 {
		t.Fatalf("rolled back: %v %v %v", off, wf.off, err)
	}
	if _, err := r.step(t, "dice-tray.roll", nil); err != nil {
		t.Fatalf("rolled back: %v", err)
	}

	// A version that won't load is left off, saying why.
	broken := zipFolder(t, example, func(name string, b []byte) []byte {
		switch name {
		case "streamgremlin.json":
			return bytes.Replace(b, []byte(`"version": "1.3.0"`), []byte(`"version": "1.5.0"`), 1)
		case "plugin.lua":
			return append(b, []byte("\nlocal x = = 1\n")...)
		}
		return b
	})
	pv, _, _ = r.store.Check(ctx, broken)
	if _, err := r.store.InstallWith(ctx, broken, packages.Approval{Digest: pv.Digest}, r.now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Switched(ctx, "dice-tray", wf); !errors.Is(err, ErrLoad) {
		t.Fatalf("broken: %v", err)
	}
	if p, _ := r.store.Get(ctx, "dice-tray"); p.Plugin.Enabled || !strings.Contains(p.Plugin.Error, "line") {
		t.Errorf("broken: %+v", p.Plugin)
	}
}

// eventually waits up to 5 seconds for check to say nothing.
func eventually(t *testing.T, what string, check func() string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		msg := check()
		if msg == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s", what, msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A plugin's events and timers (D-073): its events are triggers in the
// editor; a round's timer closes it, saying so in chat and sending its
// event; what it may not send is refused.
func TestPluginEventsAndTimers(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// Rounds of a second, for the test.
	r.install(t, zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "streamgremlin.json" {
			return bytes.Replace(b, []byte(`"default": 60, "min": 10`), []byte(`"default": 60, "min": 1`), 1)
		}
		return b
	}), "chat.send", "events.publish", "points.rounds")
	triggers := r.reg.AddOnTriggers()
	if len(triggers) != 2 || triggers[0].Type != "plugin.dice_tray.round_ended" || triggers[0].AddOn != "dice-tray" ||
		triggers[0].Category != "Dice tray" || triggers[0].Values[0].Expr != "event.payload.winner" ||
		!strings.Contains(triggers[0].Help, "published by StreamGremlin examples (not verified)") {
		t.Fatalf("triggers: %+v", triggers)
	}
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	out, err := r.step(t, "dice-tray.open_round", map[string]any{"seconds": "1"})
	if err != nil || out.(map[string]any)["open"] != true {
		t.Fatalf("open: %v %v", out, err)
	}
	if _, err := r.step(t, "dice-tray.roll", map[string]any{"who": "Pal", "dice": "3"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the round's end", func() string {
		for _, m := range r.chat.texts() {
			if strings.HasPrefix(m, "The dice round is over: Pal wins with ") {
				return ""
			}
		}
		return "not yet; said: " + strings.Join(r.chat.texts(), " | ")
	})
	eventually(t, "its event", func() string {
		if !slices.Contains(r.inbox.types(), "plugin.dice_tray.round_ended") {
			return "not yet; got: " + strings.Join(r.inbox.types(), ",")
		}
		return ""
	})
	r.inbox.mu.Lock()
	var ended events.Envelope
	for _, e := range r.inbox.got {
		if e.Type == "plugin.dice_tray.round_ended" {
			ended = e
		}
	}
	r.inbox.mu.Unlock()
	if ended.Source != "plugin:dice-tray" || ended.DeliveryQuality != events.Confirmed || !strings.Contains(string(ended.Payload), `"winner":"Pal"`) ||
		!strings.Contains(string(ended.Payload), `"rolls":1`) {
		t.Errorf("event: %+v %s", ended, ended.Payload)
	}
	act := strings.Join(activity(t, r, "dice-tray"), "\n")
	if !strings.Contains(act, `events.publish ok: round_ended {"rolls":1,"total":`) {
		t.Errorf("activity:\n%s", act)
	}

	// What it may not send.
	m := r.m
	m.mu.Lock()
	run := m.running["dice-tray"]
	m.mu.Unlock()
	for _, c := range []struct {
		call lua.HostCall
		want string
	}{
		{lua.HostCall{Op: "emit", Name: "teleported"}, `"teleported" isn't one of the events`},
		{lua.HostCall{Op: "emit", Name: "new_best", Event: map[string]any{"source": "plugin:dice-tray"}}, "could go round forever"},
		{lua.HostCall{Op: "emit", Name: "new_best", Key: "j1"}, ""},
		{lua.HostCall{Op: "emit", Name: "new_best", Key: "j1"}, "sent with that id already"},
	} {
		ok, why := m.emit(ctx, "dice-tray", run, c.call)
		if c.want == "" && !ok || c.want != "" && (ok || !strings.Contains(why, c.want)) {
			t.Errorf("%+v: %v %q, want %q", c.call, ok, why, c.want)
		}
	}
	r.now = r.now.Add(time.Second)
	sent := 0
	for range EventsPerSecond + 2 {
		if ok, _ := m.emit(ctx, "dice-tray", run, lua.HostCall{Op: "emit", Name: "new_best"}); ok {
			sent++
		}
	}
	if sent != EventsPerSecond {
		t.Errorf("%d sent in a second", sent)
	}

	// Removing turns off the workflows its events start.
	wf := &fakeWorkflows{list: []workflow.Summary{{ID: "plugin.champion", Name: "Champion", Enabled: true}, {ID: "chat.other", Name: "Other", Enabled: true}},
		uses: map[string]string{"plugin.champion": "plugin.dice_tray.round_ended", "chat.other": "chat.send"}}
	names, err := r.m.Removing(ctx, "dice-tray", wf)
	if err != nil || strings.Join(names, ",") != "Champion" || strings.Join(wf.off, ",") != "plugin.champion" {
		t.Fatalf("removing: %v %v %v", names, wf.off, err)
	}
	if len(r.reg.AddOnTriggers()) != 0 {
		t.Error("its triggers outlived it")
	}
}

// Turning a plugin off stops its timers.
func TestTimersStopWithThePlugin(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.install(t, zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "streamgremlin.json" {
			return bytes.Replace(b, []byte(`"default": 60, "min": 10`), []byte(`"default": 60, "min": 1`), 1)
		}
		return b
	}), "chat.send", "events.publish", "points.rounds")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.step(t, "dice-tray.open_round", map[string]any{"seconds": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetEnabled(ctx, "dice-tray", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	for _, m := range r.chat.texts() {
		if strings.Contains(m, "is over") {
			t.Fatalf("the round closed after the plugin was turned off: %q", m)
		}
	}
}

// stepBy runs a step for a viewer's event, with its own key.
func (r *rig) stepBy(t *testing.T, name, viewer, key string, args map[string]any) (map[string]any, error) {
	t.Helper()
	a, ok := r.reg.Get(name)
	if !ok {
		t.Fatalf("no step %s", name)
	}
	var actor any
	if viewer != "" {
		actor = map[string]any{"platform": "twitch", "id": "id-" + strings.ToLower(viewer), "login": strings.ToLower(viewer), "display_name": viewer}
	}
	out, err := a.Run(context.Background(), workflow.Call{Args: args, IdempotencyKey: key, JobID: key, WorkflowID: "gamble",
		Event: map[string]any{"id": "e-" + key, "source": "twitch", "actor": actor}})
	m, _ := out.(map[string]any)
	return m, err
}

// Gambling through the example (ADR-0039): the stake taken, the roll made
// by StreamGremlin, twice the stake back on a win, nothing more on a
// loss; refused without enough points or without a viewer; open rounds
// refunded when the plugin is turned off.
func TestGamblingThroughTheExample(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.install(t, zipFolder(t, example, nil), "chat.send", "events.publish", "points.rounds")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	fan, err := r.vs.Ensure(ctx, events.Actor{Platform: "twitch", ID: "id-fan", Login: "fan", DisplayName: "Fan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ledger.Credit(ctx, points.Op{ID: "seed", Viewer: fan.ID, Amount: 100, By: "test"}); err != nil {
		t.Fatal(err)
	}
	bal := func() int64 {
		b, _ := r.ledger.Balance(ctx, fan.ID, "")
		return b
	}
	r.rolls = []int{73}
	out, err := r.stepBy(t, "dice-tray.gamble", "Fan", "j1", map[string]any{"amount": "50"})
	if err != nil || out["won"] != true || out["balance"] != int64(150) || bal() != 150 {
		t.Fatalf("a win: %v %v (balance %d)", out, err, bal())
	}
	r.rolls = []int{12}
	if out, _ := r.stepBy(t, "dice-tray.gamble", "Fan", "j2", map[string]any{"amount": "all"}); out["won"] != false || out["stake"] != int64(150) || bal() != 0 {
		t.Fatalf("all, lost: %v (balance %d)", out, bal())
	}
	if out, _ := r.stepBy(t, "dice-tray.gamble", "Fan", "j3", map[string]any{"amount": "5"}); out["ok"] != false || !strings.Contains(out["why"].(string), "Fan has 0 points, not 5") {
		t.Errorf("not enough: %v", out)
	}
	if out, _ := r.stepBy(t, "dice-tray.gamble", "", "j4", map[string]any{"amount": "5"}); out["ok"] != false || !strings.Contains(out["why"].(string), "only a viewer's own command") {
		t.Errorf("no viewer: %v", out)
	}
	sent := strings.Join(r.chat.texts(), "\n")
	if !strings.Contains(sent, "Fan rolled 73 and wins 100 points! (now 150)") || !strings.Contains(sent, "Fan rolled 12 and loses 150 points. (now 0; 60 or more wins)") {
		t.Errorf("chat:\n%s", sent)
	}
	list, err := r.rounds.List(ctx, "dice-tray", 10)
	if err != nil || len(list) != 4 {
		t.Fatalf("rounds: %d %v", len(list), err)
	}
	states := map[string]int{}
	for _, rd := range list {
		states[rd.State]++
	}
	if states["settled"] != 2 || states["refunded"] != 2 || list[len(list)-1].Rules != "Roll 1 to 100: 60 or more pays back twice the stake; less loses it." {
		t.Errorf("rounds: %v %q", states, list[len(list)-1].Rules)
	}
	act := strings.Join(activity(t, r, "dice-tray"), "\n")
	if !strings.Contains(act, "round.stake ok: ") || !strings.Contains(act, "staked 50") || !strings.Contains(act, "round.stake refused: ") {
		t.Errorf("activity:\n%s", act)
	}

	// An open round is refunded when the plugin is turned off.
	if _, err := r.ledger.Credit(ctx, points.Op{ID: "seed2", Viewer: fan.ID, Amount: 30, By: "test"}); err != nil {
		t.Fatal(err)
	}
	lim := rounds.Limits{MaxStake: 1000, MaxMultiplier: 2, MaxRoundSeconds: 30}
	rd, err := r.rounds.Open(ctx, "dice-tray", "1.3.0", "Left open", "rules", "", lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.rounds.Stake(ctx, "dice-tray", rd.ID, &events.Actor{Platform: "twitch", ID: "id-fan", DisplayName: "Fan"}, 30, lim); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetEnabled(ctx, "dice-tray", false); err != nil {
		t.Fatal(err)
	}
	if bal() != 30 {
		t.Errorf("after turning off: %d", bal())
	}
	if problems, err := r.ledger.Audit(ctx); err != nil || len(problems) != 0 {
		t.Errorf("ledger: %v %v", problems, err)
	}
}

// Without points.rounds, its calls are refused.
func TestRoundsNeedThePermission(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.install(t, zipFolder(t, example, func(name string, b []byte) []byte {
		if name == "streamgremlin.json" {
			b = bytes.Replace(b, []byte(`, "points.rounds"]`), []byte(`]`), 1)
			i := bytes.Index(b, []byte(`"points": {`))
			j := bytes.Index(b, []byte(`"events": [`))
			return append(append([]byte{}, b[:i]...), b[j:]...)
		}
		return b
	}), "chat.send", "events.publish")
	if err := r.m.SetEnabled(ctx, "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	if out, _ := r.stepBy(t, "dice-tray.gamble", "Fan", "j1", map[string]any{"amount": "5"}); !strings.Contains(fmt.Sprint(out["why"]), "doesn't have permission to hold points") {
		t.Errorf("without permission: %v", out)
	}
}
