package lua

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type hostLog struct {
	mu    sync.Mutex
	calls []string
	allow bool
}

func (h *hostLog) fn(ctx context.Context, op, msg string) (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, op+":"+msg)
	if !h.allow {
		return false, "it doesn't have permission to send chat"
	}
	return true, ""
}

func startPlugin(t *testing.T, script string, opts Options, h *hostLog) (*Plugin, Result, error) {
	t.Helper()
	if h == nil {
		h = &hostLog{allow: true}
	}
	p, res, err := StartPlugin(opts, PluginSpec{ID: "dice", Name: "Dice", Version: "1.0.0", Script: script,
		Settings: map[string]any{"sides": float64(6)}, Permissions: []string{"chat.send"}, Actions: []string{"roll", "count", "say", "slow", "nothing"},
		Timeout: 2 * time.Second}, h.fn)
	if p != nil {
		t.Cleanup(p.Close)
	}
	return p, res, err
}

const dicePlugin = `
local rolls = 0
sg.log("loading " .. sg.plugin.name .. " " .. sg.plugin.version)
sg.action("roll", function(call)
  rolls = rolls + 1
  local n = call.args.sides or sg.settings.sides
  return {roll = math.random(n), sides = n, who = call.event.actor and call.event.actor.display_name, key = call.key}
end)
sg.action("count", function() return rolls end)
sg.action("say", function(call)
  local ok, why = sg.chat("rolled for " .. call.args.who)
  return {ok = ok, why = why}
end)
sg.action("slow", function() while true do end end)
`

func call(t *testing.T, p *Plugin, action string, args map[string]any) (Result, *Error) {
	t.Helper()
	res, err := p.Call(context.Background(), action, args, map[string]any{"actor": map[string]any{"display_name": "Fan"}}, "job/1")
	if err == nil {
		return res, nil
	}
	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("%s: %v", action, err)
	}
	return res, le
}

// A plugin keeps its state between calls, and gets its settings, values,
// event and key.
func TestPluginCalls(t *testing.T) {
	p, res, err := startPlugin(t, dicePlugin, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Log) != 1 || res.Log[0] != "loading Dice 1.0.0" {
		t.Errorf("load log: %q", res.Log)
	}
	r, le := call(t, p, "roll", nil)
	m, _ := r.Value.(map[string]any)
	if le != nil || m["sides"] != int64(6) || m["who"] != "Fan" || m["key"] != "job/1" {
		t.Fatalf("roll: %+v %v", le, r.Value)
	}
	if r, _ := call(t, p, "roll", map[string]any{"sides": float64(20)}); r.Value.(map[string]any)["sides"] != int64(20) {
		t.Errorf("sides 20: %v", r.Value)
	}
	if r, _ := call(t, p, "count", nil); r.Value != int64(2) {
		t.Errorf("state not kept: %v", r.Value)
	}
	// A declared action it doesn't handle.
	if _, le := call(t, p, "nothing", nil); le == nil || le.Kind != KindScript || !strings.Contains(le.Message, `doesn't handle "nothing"`) {
		t.Errorf("unhandled: %+v", le)
	}
}

func TestPluginHostCalls(t *testing.T) {
	h := &hostLog{allow: true}
	p, _, err := startPlugin(t, dicePlugin, Options{}, h)
	if err != nil {
		t.Fatal(err)
	}
	if r, le := call(t, p, "say", map[string]any{"who": "Fan"}); le != nil || r.Value.(map[string]any)["ok"] != true {
		t.Fatalf("say: %+v %v", le, r.Value)
	}
	h.allow = false
	if r, _ := call(t, p, "say", map[string]any{"who": "Pal"}); r.Value.(map[string]any)["why"] != "it doesn't have permission to send chat" {
		t.Errorf("refused: %v", r.Value)
	}
	if strings.Join(h.calls, "|") != "chat:rolled for Fan|chat:rolled for Pal" {
		t.Errorf("host calls: %q", h.calls)
	}
}

func TestPluginLoadFailures(t *testing.T) {
	for _, c := range []struct{ script, kind, want string }{
		{"sg.action('roll', function() end\n", KindScript, "syntax error"},
		{"sg.action('teleport', function() end)", KindScript, `"teleport" isn't one of the actions`},
		{"sg.action('roll', function() end) sg.action('roll', function() end)", KindScript, "handled twice"},
		{"while true do end", KindTimeout, "longer than 5 seconds"},
		{"local ok, why = sg.chat('hi') error(why)", KindScript, "while the script loads"},
	} {
		h := &hostLog{allow: true}
		_, _, err := startPlugin(t, c.script, Options{}, h)
		var le *Error
		if !errors.As(err, &le) || le.Kind != c.kind || !strings.Contains(le.Message, c.want) {
			t.Errorf("%q: %v, want %s %q", c.script, err, c.kind, c.want)
		}
		if len(h.calls) != 0 {
			t.Errorf("%q: host called while loading", c.script)
		}
	}
	// sg.action after loading.
	p, _, err := startPlugin(t, `sg.action("roll", function() sg.action("count", function() end) end)`, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, le := call(t, p, "roll", nil); le == nil || !strings.Contains(le.Message, "only while the script loads") {
		t.Errorf("late sg.action: %+v", le)
	}
}

// A call past its time is stopped by the worker, which keeps its state; one
// whose worker never answers is killed.
func TestPluginTimeLimits(t *testing.T) {
	p, _, err := startPlugin(t, dicePlugin, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = call(t, p, "roll", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = p.Call(ctx, "slow", nil, nil, "")
	var le *Error
	if !errors.As(err, &le) || le.Kind != KindTimeout || !strings.Contains(le.Message, "time limit") {
		t.Fatalf("slow: %v", err)
	}
	if p.Dead() {
		t.Fatal("the worker died, though it stopped the script itself")
	}
	if r, _ := call(t, p, "count", nil); r.Value != int64(1) {
		t.Errorf("state after a timeout: %v", r.Value)
	}
}

func TestPluginWorkerDies(t *testing.T) {
	p, _, err := startPlugin(t, `
sg.action("roll", function() local t = {} for i = 1, 1e9 do t[i] = string.rep("x", 1000) .. i end end)
sg.action("count", function() return 1 end)`, Options{Memory: 32 << 20}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, le := call(t, p, "roll", nil); le == nil || le.Kind != KindMemory {
		t.Fatalf("bomb: %+v", le)
	}
	if !p.Dead() {
		t.Fatal("not dead after its memory cap")
	}
	if _, le := call(t, p, "count", nil); le == nil || le.Kind != KindCrashed {
		t.Errorf("after: %+v", le)
	}
}

// Calls wait their turn: a script never runs two at once.
func TestPluginOneCallAtATime(t *testing.T) {
	p, _, err := startPlugin(t, `
local busy = false
sg.action("roll", function()
  if busy then error("two at once") end
  busy = true
  local t = os.clock() while os.clock() - t < 0.02 do end
  busy = false
  return 1
end)`, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Call(context.Background(), "roll", nil, nil, ""); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
