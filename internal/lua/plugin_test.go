package lua

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type hostLog struct {
	mu    sync.Mutex
	calls []string
	got   []HostCall
	allow bool
}

func (h *hostLog) fn(ctx context.Context, c HostCall) HostReply {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.got = append(h.got, c)
	switch c.Op {
	case "emit":
		h.calls = append(h.calls, c.Op+":"+c.Name)
	case "chat":
		h.calls = append(h.calls, c.Op+":"+c.Message)
	default:
		h.calls = append(h.calls, c.Op)
		if c.Op == "round.open" {
			return HostReply{OK: true, Value: "r_1"}
		}
		if c.Op == "round.stake" {
			return HostReply{OK: true, Value: map[string]any{"viewer": "v_1", "stake": c.Payload["amount"]}}
		}
		if c.Op == "round.settle" {
			return HostReply{OK: true}
		}
		return HostReply{Reason: "no such thing"}
	}
	if !h.allow {
		return HostReply{Reason: "it doesn't have permission to send chat"}
	}
	return HostReply{OK: true}
}

func startPlugin(t *testing.T, script string, opts Options, h *hostLog) (*Plugin, Result, error) {
	t.Helper()
	if h == nil {
		h = &hostLog{allow: true}
	}
	p, res, err := StartPlugin(opts, PluginSpec{ID: "dice", Name: "Dice", Version: "1.0.0", Script: script,
		Settings: map[string]any{"sides": float64(6)}, Permissions: []string{"chat.send"}, Actions: []string{"roll", "count", "say", "slow", "nothing", "later", "cancel", "emit"},
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

// The caller's deadline passing just before the worker stops the script
// itself (a busy computer) waits for the worker's answer instead of killing
// it, so the plugin keeps its state. One that never answers is still killed.
func TestPluginDeadlineBeforeWorker(t *testing.T) {
	p, _, err := startPlugin(t, dicePlugin, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = call(t, p, "roll", nil)
	log := slog.New(slog.DiscardHandler)
	exchange := func(deadline, workerLimit time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		f := frame{Type: "call", Action: "slow", ID: p.id(), TimeoutMS: workerLimit.Milliseconds()}
		_, err := p.proc.exchange(ctx, f, workerLimit, log, func(g frame) frame { return frame{Type: "reply", ID: g.ID} })
		return err
	}
	var le *Error
	if err := exchange(100*time.Millisecond, 150*time.Millisecond); !errors.As(err, &le) || le.Kind != KindTimeout {
		t.Fatalf("the worker's own stop: %v", err)
	}
	if p.Dead() {
		t.Fatal("killed a worker that stopped the script itself")
	}
	if r, _ := call(t, p, "count", nil); r.Value != int64(1) {
		t.Errorf("state after the deadline: %v", r.Value)
	}
	if err := exchange(50*time.Millisecond, 10*time.Second); !errors.Is(err, context.DeadlineExceeded) || !p.Dead() {
		t.Fatalf("a worker past grace: %v, dead %v", err, p.Dead())
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

const timerPlugin = `
local fired = {}
local pending = nil
sg.after(1, function() fired[#fired + 1] = "loaded" end)
sg.action("later", function(call)
  pending = sg.after(call.args.seconds, function()
    fired[#fired + 1] = call.args.name
    sg.chat("timer " .. call.args.name)
  end)
  return pending
end)
sg.action("cancel", function() return sg.cancel(pending) end)
sg.action("count", function() return table.concat(fired, ",") end)
sg.action("emit", function(call)
  local ok, why = sg.emit("won", {winner = "Fan", total = 12}, call.key)
  return {ok = ok, why = why}
end)
sg.action("slow", function()
  local ok, err = pcall(sg.after, 0.5, function() end)
  local ok2, err2 = pcall(sg.after, 1, "not a function")
  return {err, err2}
end)
`

// Timers (D-073): set while loading or in a call, reported to the engine,
// fired by it one at a time; cancelled ones never fire.
func TestPluginTimers(t *testing.T) {
	h := &hostLog{allow: true}
	p, _, err := startPlugin(t, timerPlugin, Options{}, h)
	if err != nil {
		t.Fatal(err)
	}
	ids := p.Timers()
	if len(ids) != 1 {
		t.Fatalf("after loading: %v", ids)
	}
	r, le := call(t, p, "later", map[string]any{"seconds": float64(2), "name": "a"})
	if le != nil {
		t.Fatal(le)
	}
	a := int(r.Value.(int64))
	r, _ = call(t, p, "later", map[string]any{"seconds": float64(3), "name": "b"})
	b := int(r.Value.(int64))
	if _, le := call(t, p, "cancel", nil); le != nil {
		t.Fatal(le)
	}
	due := p.Timers()
	if len(due) != 2 || due[a].IsZero() || !due[b].IsZero() {
		t.Fatalf("waiting: %v (a %d, b %d)", due, a, b)
	}
	if at := due[a]; time.Until(at) < time.Second || time.Until(at) > 2*time.Second {
		t.Errorf("a is due in %v", time.Until(at))
	}
	id, _, ok := p.NextTimer()
	if !ok || id == a {
		t.Fatalf("next: %d %v", id, ok)
	}
	for _, id := range []int{id, a, b} {
		if _, err := p.Fire(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if r, _ := call(t, p, "count", nil); r.Value != "loaded,a" {
		t.Errorf("fired: %v", r.Value)
	}
	if len(p.Timers()) != 0 {
		t.Errorf("still waiting: %v", p.Timers())
	}
	if strings.Join(h.calls, "|") != "chat:timer a" || h.got[0].Event != nil {
		t.Errorf("host calls: %q %+v", h.calls, h.got)
	}
	r, _ = call(t, p, "slow", nil)
	errs := r.Value.([]any)
	if !strings.Contains(errs[0].(string), "sg.after: seconds is 1 to 86400") || !strings.Contains(errs[1].(string), "function") {
		t.Errorf("bad timers: %v", errs)
	}
}

func TestPluginTimerLimit(t *testing.T) {
	p, _, err := startPlugin(t, `for i = 1, 100 do sg.after(60, function() end) end
sg.action("later", function() local ok, err = pcall(sg.after, 60, function() end); return err end)`, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := call(t, p, "later", nil); !strings.Contains(r.Value.(string), "100 timers") {
		t.Errorf("101st: %v", r.Value)
	}
}

// sg.emit asks the engine, with the event name, payload, id and the call's
// event.
func TestPluginEmits(t *testing.T) {
	h := &hostLog{allow: true}
	p, _, err := startPlugin(t, timerPlugin, Options{}, h)
	if err != nil {
		t.Fatal(err)
	}
	r, le := call(t, p, "emit", nil)
	if le != nil || r.Value.(map[string]any)["ok"] != true {
		t.Fatalf("emit: %v %v", le, r.Value)
	}
	g := h.got[0]
	if g.Op != "emit" || g.Name != "won" || g.Payload["winner"] != "Fan" || g.Payload["total"] != int64(12) || g.Key != "job/1" ||
		g.Event["actor"] == nil {
		t.Errorf("host call: %+v", g)
	}
}

// Points host calls give back values (ADR-0039).
func TestPluginPointsCalls(t *testing.T) {
	h := &hostLog{allow: true}
	p, _, err := startPlugin(t, `
sg.action("roll", function(call)
  local round = sg.round.open("Gamble", "Roll 1-100: 60 or more doubles.", call.key)
  local st = sg.round.stake(round, 50)
  local ok = sg.round.settle(round, {[st.viewer] = 100})
  local b, why = sg.points.balance()
  return {round = round, viewer = st.viewer, stake = st.stake, settled = ok, why = why}
end)`, Options{}, h)
	if err != nil {
		t.Fatal(err)
	}
	r, le := call(t, p, "roll", nil)
	if le != nil {
		t.Fatal(le)
	}
	m := r.Value.(map[string]any)
	if m["round"] != "r_1" || m["viewer"] != "v_1" || m["stake"] != int64(50) || m["settled"] != true || m["why"] != "no such thing" {
		t.Errorf("values: %v", m)
	}
	if strings.Join(h.calls, "|") != "round.open|round.stake|round.settle|points.balance" {
		t.Errorf("calls: %q", h.calls)
	}
	if g := h.got[0]; g.Payload["title"] != "Gamble" || g.Payload["id"] != "job/1" {
		t.Errorf("open: %+v", g.Payload)
	}
	if g := h.got[2]; g.Payload["payouts"].(map[string]any)["v_1"] != int64(100) {
		t.Errorf("settle: %+v", g.Payload)
	}
}
