package actions

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// TestMain lets this test binary be the Lua worker, as the app is.
func TestMain(m *testing.M) {
	lua.RunWorkerIfAsked()
	os.Exit(m.Run())
}

func luaRig(t *testing.T) *workflow.Registry {
	t.Helper()
	r := lua.NewRunner(lua.Options{})
	t.Cleanup(r.Close)
	reg := workflow.NewRegistry()
	Register(reg, Services{Lua: r})
	return reg
}

// The step (docs/adr/0037-lua-scripts.md): values and the event in, the
// result and log out; failures as step errors.
func TestLuaStep(t *testing.T) {
	reg := luaRig(t)
	call := func(args map[string]any, dry bool) workflow.Call {
		return workflow.Call{Args: args, IdempotencyKey: "j1/steps[0]", JobID: "j1", WorkflowID: "dice", DryRun: dry,
			Event: map[string]any{"type": "chat.command", "actor": map[string]any{"display_name": "Fan"}}}
	}
	script := `
local sides = tonumber(sg.args.sides) or 6
local roll = math.random(sides)
sg.log("rolled " .. roll)
return {roll = roll, who = sg.event.actor.display_name}`
	// A test event runs it for real: it changes nothing outside itself.
	for _, dry := range []bool{false, true} {
		out := run(t, reg, "lua.run", call(map[string]any{"script": script, "args": map[string]any{"sides": "20"}}, dry))
		res, _ := out["result"].(map[string]any)
		roll, _ := res["roll"].(int64)
		if roll < 1 || roll > 20 || res["who"] != "Fan" {
			t.Fatalf("dry %v: %v", dry, out)
		}
		if l, _ := out["log"].([]string); len(l) != 1 || !strings.HasPrefix(l[0], "rolled ") {
			t.Fatalf("log: %#v", out["log"])
		}
		if _, ok := out["log_dropped"]; ok {
			t.Fatalf("log_dropped with nothing dropped")
		}
	}
	a, _ := reg.Get("lua.run")
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"script": "sg.log('working') error('nope')"}, `line 1: nope (its last line: "working")`},
		{map[string]any{"script": "while true do end", "time_limit": 0.2}, "the script ran longer than its 200ms time limit"},
		{map[string]any{"script": "return 1", "time_limit": 60}, "the time limit is 0.1 to 30 seconds"},
		{map[string]any{"script": "return 1", "time_limit": "soon"}, "the time limit is a number of seconds"},
		{map[string]any{"script": "return 1", "args": "x"}, "values to pass are names and values"},
	} {
		_, err := a.Run(context.Background(), call(c.args, false))
		if err == nil || err.Error() != c.want {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
	// Without a runner the step says so rather than panicking.
	bare := workflow.NewRegistry()
	Register(bare, Services{})
	b, _ := bare.Get("lua.run")
	if _, err := b.Run(context.Background(), call(map[string]any{"script": "return 1"}, false)); err == nil {
		t.Errorf("no runner: no error")
	}
}

// The script is written in the step, never computed, and at most 64 KiB:
// both show when the workflow is saved.
func TestLuaScriptIsLiteral(t *testing.T) {
	reg := luaRig(t)
	def := func(script string) []byte {
		b, _ := json.Marshal(map[string]any{
			"schema": workflow.Schema, "id": "dice", "name": "Dice", "triggers": []any{map[string]any{"type": "chat.message.received"}},
			"steps": []any{map[string]any{"id": "roll", "action": "lua.run", "with": map[string]any{"script": script}}},
		})
		return b
	}
	if _, err := workflow.Parse(def("return 1"), reg); err != nil {
		t.Fatalf("plain script: %v", err)
	}
	for _, c := range []struct{ script, want string }{
		{"return '${event.message}'", "written out, not computed"},
		{"return 1 --" + strings.Repeat("x", lua.MaxScriptBytes), "at most 64 KiB"},
	} {
		if _, err := workflow.Parse(def(c.script), reg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%.30q: %v, want %q", c.script, err, c.want)
		}
	}
}
