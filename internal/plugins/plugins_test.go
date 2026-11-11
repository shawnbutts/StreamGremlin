package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/packages"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
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
	off  []string
}

func (f *fakeWorkflows) UsingActions(_ context.Context, prefix string) ([]workflow.Summary, error) {
	return f.list, nil
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
	m     *Manager
	now   time.Time
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
	r := &rig{store: packages.NewStore(db), reg: workflow.NewRegistry(), chat: &fakeChat{}, now: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
	r.m = New(Options{Store: r.store, Actions: r.reg, Chat: r.chat, Now: func() time.Time { return r.now }})
	t.Cleanup(r.m.Close)
	return r
}

func (r *rig) install(t *testing.T, data []byte, perms ...string) {
	t.Helper()
	pv, _, err := r.store.Check(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.store.InstallWith(context.Background(), data, packages.Approval{Digest: pv.Digest, Permissions: perms}, r.now); err != nil {
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
	if pv.Kind != "lua" || pv.Plugin == nil || len(pv.Plugin.Permissions) != 1 || pv.Plugin.Permissions[0].Name != "chat.send" ||
		!strings.Contains(pv.Plugin.Permissions[0].What, "Send messages in your chat") || len(pv.Plugin.Actions) != 3 {
		t.Fatalf("preview: %+v", pv.Plugin)
	}
	for _, perms := range [][]string{nil, {"chat.send", "chat.send"}, {"chat.send", "points.give"}} {
		if _, _, _, err := r.store.InstallWith(ctx, data, packages.Approval{Digest: pv.Digest, Permissions: perms}, r.now); !errors.Is(err, packages.ErrConfirmPermissions) {
			t.Errorf("%v: %v", perms, err)
		}
	}
	r.install(t, data, "chat.send")

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
			return bytes.Replace(b, []byte(`"permissions": ["chat.send"],`), nil, 1)
		}
		return b
	})
	r.install(t, noChat)
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
	r.install(t, zipFolder(t, example, nil), "chat.send")
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
	r.install(t, broken, "chat.send")
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
	r.install(t, bomb, "chat.send")
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
	r.install(t, zipFolder(t, example, nil), "chat.send")
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
	if len(pv.Plugin.Templates) != 2 || pv.Plugin.Templates[0].Title != "!best: the session's best roll" {
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
	r.install(t, zipFolder(t, example, nil), "chat.send")
	ts, err := r.store.AddOnTemplates(ctx)
	if err != nil || len(ts) != 2 || ts[1].ID != "dice-tray/roll" || ts[1].Category != "Dice tray" || ts[1].AddOn == nil || ts[1].AddOn.Enabled {
		t.Fatalf("gallery: %+v %v", ts, err)
	}
}

// "!roll" with nothing after it rolls the default, one die.
func TestEmptyNumberIsTheDefault(t *testing.T) {
	r := newRig(t)
	r.install(t, zipFolder(t, example, nil), "chat.send")
	if err := r.m.SetEnabled(context.Background(), "dice-tray", true); err != nil {
		t.Fatal(err)
	}
	out, err := r.step(t, "dice-tray.roll", map[string]any{"dice": " "})
	if err != nil || len(out.(map[string]any)["each"].([]any)) != 1 {
		t.Fatalf("%v %v", out, err)
	}
}
