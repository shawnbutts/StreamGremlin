package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/lua"
)

// TestMain lets this test binary be the Lua worker, as the app is.
func TestMain(m *testing.M) {
	lua.RunWorkerIfAsked()
	os.Exit(m.Run())
}

// Trying a script (docs/adr/0037-lua-scripts.md): its result and log, the
// made-up test event, and failures as answers, not errors.
func TestTryALuaScript(t *testing.T) {
	s := newTestServer(t)
	try := func(body string) (int, LuaTried, string) {
		w := do(t, s, req{method: "POST", path: "/v1/lua/try", token: testToken, body: body})
		var out LuaTried
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Body.String()
	}
	code, out, raw := try(`{"script": "sg.log('hello ' .. sg.event.actor.display_name) return {sum = sg.args.a + sg.args.b}", "args": {"a": 2, "b": 3}}`)
	if code != 200 || !out.OK || len(out.Log) != 1 || out.Log[0] != "hello TestViewer" {
		t.Fatal(code, raw)
	}
	if m, _ := out.Result.(map[string]any); m["sum"] != float64(5) {
		t.Fatal(raw)
	}
	// An event of its own.
	if _, out, raw := try(`{"script": "return sg.event.type", "event": {"type": "twitch.follow"}}`); out.Result != "twitch.follow" {
		t.Fatal(raw)
	}
	// Failures are answers.
	for _, c := range []struct{ body, kind, msg string }{
		{`{"script": "error('nope')"}`, lua.KindScript, "line 1: nope"},
		{`{"script": "while true do end", "time_limit": 0.2}`, lua.KindTimeout, "200ms time limit"},
		{`{"script": "return print"}`, lua.KindOutput, "function"},
	} {
		code, out, raw := try(c.body)
		if code != 200 || out.OK || out.Kind != c.kind || !strings.Contains(out.Error, c.msg) || out.Log == nil {
			t.Errorf("%s: %d %s", c.body, code, raw)
		}
	}
	// Bad requests.
	for _, c := range []struct{ body, msg string }{
		{`{"script": ""}`, "script is required"},
		{`{"script": "return 1", "time_limit": 31}`, "0.1 to 30"},
		{`{"script": "return 1", "nope": 1}`, "unknown field"},
		{`{"script": "` + strings.Repeat("-", lua.MaxScriptBytes+1) + `"}`, "64 KiB"},
	} {
		if code, _, raw := try(c.body); code != 400 || !strings.Contains(raw, c.msg) {
			t.Errorf("%.40s: %d %s", c.body, code, raw)
		}
	}
	// Without the token, nothing runs.
	if w := do(t, s, req{method: "POST", path: "/v1/lua/try", body: `{"script": "return 1"}`}); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
}
