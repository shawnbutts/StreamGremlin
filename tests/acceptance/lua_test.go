//go:build acceptance

package acceptance

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

const diceWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.dice","name":"Dice",
	"triggers":[{"type":"chat.command.received","where":{"command":"roll"}}],
	"steps":[
		{"id":"roll","action":"lua.run","with":{
			"script":"local n = tonumber(sg.args.sides) or 6\nlocal r = math.random(n)\nsg.log('rolled ' .. r .. ' of ' .. n)\nreturn {roll = r, who = sg.event.actor.display_name}",
			"args":{"sides":"${event.payload.args}"}}},
		{"action":"note","with":{"text":"lua said ${steps.roll.output.result.who} rolled ${steps.roll.output.result.roll}"}}
	]}`

// Lua scripts through the real engine and CLI (docs/adr/0037-lua-scripts.md):
// tried from the CLI, stopped at their time and memory limits without
// harming the engine, run as a workflow step for a test event, refused when
// computed, and their workers gone with the engine.
func TestLuaScriptsThroughTheEngineAndCLI(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir)
	files := t.TempDir()

	script := writeFile(t, files, "dice.lua", "local r = math.random(tonumber(sg.args.sides))\nprint('hello ' .. sg.event.actor.display_name)\nreturn {roll = r, sides = sg.args.sides}\n")
	out, errOut, code := cli(t, dir, "lua", "try", script, "-arg", "sides=6")
	if code != 0 || !strings.Contains(out, "log: hello TestViewer") || !strings.Contains(out, `"sides": 6`) || !strings.Contains(out, `"roll": `) {
		t.Fatalf("try: %d\n%s%s", code, out, errOut)
	}

	// A loop is stopped at its limit, and the engine carries on.
	loop := writeFile(t, files, "loop.lua", "sg.log('spinning')\nwhile true do end\n")
	start := time.Now()
	out, errOut, code = cli(t, dir, "lua", "try", loop, "-time-limit", "0.3")
	if code != 1 || !strings.Contains(errOut, "The script failed (timeout): the script ran longer than its 300ms time limit") || !strings.Contains(out, "log: spinning") {
		t.Fatalf("loop: %d\n%s%s", code, out, errOut)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the loop took %s to stop", took)
	}
	// A memory bomb ends its worker, not the engine.
	bomb := writeFile(t, files, "bomb.lua", "local t = {}\nfor i = 1, 1e9 do t[i] = string.rep('x', 1000) .. i end\n")
	if out, errOut, code := cli(t, dir, "lua", "try", bomb, "-time-limit", "25"); code != 1 || !strings.Contains(errOut, "(memory): the script used more than its 64 MiB of memory") {
		t.Fatalf("bomb: %d\n%s%s", code, out, errOut)
	}
	if out, _, code := cli(t, dir, "status"); code != 0 || !strings.Contains(out, "running") {
		t.Fatalf("after the bomb: %s", out)
	}
	// What the sandbox leaves out isn't there.
	probe := writeFile(t, files, "probe.lua", "return {io = type(io), exec = type(os.execute), req = type(require), load = type(loadstring)}\n")
	if out, _, _ := cli(t, dir, "lua", "try", probe); strings.Contains(out, "function") || strings.Contains(out, "table") {
		t.Fatalf("sandbox: %s", out)
	}

	// As a workflow step: a test event runs it (it changes nothing outside
	// itself), and the next step reads its result.
	if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "dice.json", diceWorkflow)); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.dice")
	cli(t, dir, "events", "simulate", "chat.command.received", "command=!roll", "args=20")
	eventually(t, "the dice run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.dice"); !strings.Contains(out, "succeeded (test)") {
			return "not yet: " + out
		}
		return ""
	})
	out, _, _ = cli(t, dir, "runs", "-workflow", "chat.dice")
	trace, _, _ := cli(t, dir, "runs", "show", strings.Fields(out)[0])
	if !strings.Contains(trace, "lua.run") || !strings.Contains(trace, "rolled ") || !strings.Contains(trace, " of 20") || !strings.Contains(trace, "lua said ") {
		t.Fatalf("trace:\n%s", trace)
	}

	// The script is written out: one computed from chat is refused on save.
	computed := strings.Replace(diceWorkflow, `local n = tonumber`, `local n = ${event.payload.args} + tonumber`, 1)
	if _, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "computed.json", computed)); code != 2 || !strings.Contains(errOut, "written out, not computed") {
		t.Fatalf("computed: %d %s", code, errOut)
	}

	// Every run is logged; the engine's log never has the script's values
	// beyond its log lines.
	if !strings.Contains(e.stderr.String(), "lua script ran") || !strings.Contains(e.stderr.String(), "lua worker killed") {
		t.Errorf("engine log:\n%s", e.stderr.String())
	}

	// Workers go when the engine does.
	e.kill()
	if runtime.GOOS != "windows" {
		eventually(t, "no workers left", func() string {
			out, _ := exec.Command("pgrep", "-f", bin+" __lua-worker").Output()
			return strings.TrimSpace(string(out))
		})
	}
}
