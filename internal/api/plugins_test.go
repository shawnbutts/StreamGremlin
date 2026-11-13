package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
)

// examplePlugin zips examples/plugins/dice-tray, each file changed by
// change when given.
func examplePlugin(t *testing.T, change ...func(name string, b []byte) []byte) string {
	t.Helper()
	dir := filepath.Join("..", "..", "examples", "plugins", "dice-tray")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"streamgremlin.json", "plugin.lua", "README.md", "templates/roll.json", "templates/best.json",
		"templates/round.json", "templates/champion.json", "templates/gamble.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the repository's own example
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range change {
			b = c(name, b)
		}
		w, _ := zw.Create(name)
		_, _ = w.Write(b)
	}
	_ = zw.Close()
	return buf.String()
}

const diceWorkflowAPI = `{"schema":"streamgremlin.workflow/v1","id":"chat.roll","name":"Roll",
	"triggers":[{"type":"chat.command.received","where":{"command":"roll"}}],
	"steps":[{"id":"roll","action":"dice-tray.roll","with":{"dice":2}}]}`

// A Lua plugin through the API (docs/adr/0038-lua-plugins.md): installed
// only with its permissions approved, off; turned on; its settings; its
// activity; removed with its workflows turned off.
func TestPluginsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	data := examplePlugin(t)
	code, out := call("POST", "/v1/packages/check", data)
	var pv struct {
		Digest string
		Plugin struct {
			Permissions []struct{ Name, What string }
			Actions     []struct{ Name string }
		}
	}
	_ = json.Unmarshal([]byte(out), &pv)
	if code != 200 || len(pv.Plugin.Permissions) != 3 || pv.Plugin.Permissions[0].Name != "chat.send" || len(pv.Plugin.Actions) != 5 {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest, data); code != 409 || !strings.Contains(out, "confirm_permissions") ||
		!strings.Contains(out, "asks for chat.send, events.publish, points.rounds") {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest+"&permissions=chat.send,events.publish,points.rounds", data); code != 201 {
		t.Fatal(code, out)
	}
	// Its steps are there for workflows, while it's off.
	if code, out := call("PUT", "/v1/workflows/chat.roll", diceWorkflowAPI); code != 200 && code != 201 {
		t.Fatal(code, out)
	}
	_, out = call("GET", "/v1/packages", "")
	if !strings.Contains(out, `"enabled":false`) || !strings.Contains(out, `"workflows":["Roll"]`) || !strings.Contains(out, `"values":{"cheer":true,"sides":6,"win_at":60}`) ||
		!strings.Contains(out, `"type":"plugin.dice_tray.new_best"`) {
		t.Fatal(out)
	}

	// Its events are triggers in the editor, marked as the add-on's.
	_, out = call("GET", "/v1/palette", "")
	if !strings.Contains(out, `"type":"plugin.dice_tray.round_ended","label":"A dice round ends","category":"Dice tray"`) ||
		!strings.Contains(out, `"add_on":"dice-tray"`) || !strings.Contains(out, `{"token":"winner","label":"The winner's name (empty with no rolls)","expr":"event.payload.winner"}`) {
		t.Fatal(out)
	}
	if code, out := call("PUT", "/v1/workflows/plugin.champion", `{"schema":"streamgremlin.workflow/v1","id":"plugin.champion","name":"Champion",
		"triggers":[{"type":"plugin.dice_tray.round_ended"}],"steps":[{"id":"s","action":"chat.send","with":{"message":"GG ${event.payload.winner}"}}]}`); code != 200 && code != 201 {
		t.Fatal(code, out)
	}
	call("PUT", "/v1/workflows/plugin.champion/enabled", `{"enabled": true}`)

	// Its templates are in the gallery, in its own section.
	_, out = call("GET", "/v1/templates", "")
	if !strings.Contains(out, `"id":"dice-tray/roll"`) || !strings.Contains(out, `"category":"Dice tray"`) ||
		!strings.Contains(out, `"add_on":{"id":"dice-tray","name":"Dice tray","publisher":"StreamGremlin examples","enabled":false}`) ||
		!strings.Contains(out, `"Discord","Dice tray"]`) {
		t.Fatal(out)
	}

	// On, settings, activity.
	if code, out := call("PUT", "/v1/packages/dice-tray/enabled", `{"enabled": true}`); code != 200 || !strings.Contains(out, `"enabled":true`) {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/packages/dice-tray/settings", `{"settings": {"sides": 200}}`); code != 400 || !strings.Contains(out, "from 2 to 100") {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/packages/dice-tray/settings", `{"settings": {"token": "x"}}`); code != 400 || !strings.Contains(out, "no setting") {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/packages/dice-tray/settings", `{"settings": {"sides": 20}}`); code != 200 || !strings.Contains(out, `"sides":20`) {
		t.Fatal(code, out)
	}
	if code, out := call("GET", "/v1/packages/dice-tray/activity", ""); code != 200 || !strings.Contains(out, "Dice tray ready: d20") {
		t.Fatal(code, out)
	}
	if code, _ := call("PUT", "/v1/packages/dice-tray/enabled", `{}`); code != 400 {
		t.Fatal("no enabled:", code)
	}
	if code, _ := call("PUT", "/v1/packages/nope/enabled", `{"enabled": true}`); code != 404 {
		t.Fatal("unknown:", code)
	}

	// Its rounds: a gamble from a viewer with points.
	if code, out := call("PUT", "/v1/workflows/chat.gamble", `{"schema":"streamgremlin.workflow/v1","id":"chat.gamble","name":"Gamble",
		"triggers":[{"type":"chat.command.received","where":{"command":"gamble"}}],"steps":[{"id":"g","action":"dice-tray.gamble","with":{"amount":"20"}}]}`); code != 200 && code != 201 {
		t.Fatal(code, out)
	}
	v, err := testViewers.Ensure(context.Background(), events.Actor{Platform: "twitch", ID: "fan-1", Login: "fan", DisplayName: "Fan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testLedger.Credit(context.Background(), points.Op{ID: "seed-fan", Viewer: v.ID, Amount: 100, By: "test"}); err != nil {
		t.Fatal(err)
	}
	if res, err := testPlugins.Call(context.Background(), "dice-tray", "gamble", map[string]any{"amount": "20"},
		map[string]any{"id": "e1", "source": "twitch", "actor": map[string]any{"platform": "twitch", "id": "fan-1", "login": "fan", "display_name": "Fan"}}, "j1"); err != nil {
		t.Fatal(res, err)
	}
	code, out = call("GET", "/v1/packages/dice-tray/rounds", "")
	if code != 200 || !strings.Contains(out, `"title":"Gamble"`) || !strings.Contains(out, `"state":"settled"`) ||
		!strings.Contains(out, `"rules":"Roll 1 to 100: 60 or more pays back twice the stake; less loses it."`) ||
		!strings.Contains(out, `"name":"Fan","amount":20`) || !strings.Contains(out, `"sides":100`) {
		t.Fatal(code, out)
	}
	if code, _ := call("GET", "/v1/packages/dice-tray/rounds?limit=0", ""); code != 400 {
		t.Fatal("limit 0:", code)
	}

	// Removed: its workflow is turned off, not deleted.
	call("PUT", "/v1/workflows/chat.roll/enabled", `{"enabled": true}`)
	code, out = call("DELETE", "/v1/packages/dice-tray", "")
	if code != 200 || !strings.Contains(out, `"workflows":["Gamble","Roll","Champion"]`) {
		t.Fatal(code, out)
	}
	for _, id := range []string{"chat.roll", "plugin.champion"} {
		if _, out := call("GET", "/v1/workflows/"+id, ""); !strings.Contains(out, `"enabled":false`) {
			t.Fatal("still on:", out)
		}
	}
	if _, out := call("GET", "/v1/palette", ""); strings.Contains(out, "plugin.dice_tray") {
		t.Fatal("its triggers outlived it:", out)
	}
	if _, out := call("GET", "/v1/templates", ""); strings.Contains(out, "dice-tray/") || strings.Contains(out, `"Dice tray"`) {
		t.Fatal("its templates outlived it:", out)
	}
	if _, out := call("GET", "/v1/packages/audit", ""); !strings.Contains(out, `"action":"on"`) || !strings.Contains(out, `"action":"settings"`) {
		t.Fatal(out)
	}
}

// diceVersion is the example at another version: without chat.send when
// !perms, and without the "roll" step when dropRoll.
func diceVersion(t *testing.T, version string, perms bool, dropRoll bool) string {
	return examplePlugin(t, func(name string, b []byte) []byte {
		s := string(b)
		switch name {
		case "streamgremlin.json":
			s = strings.Replace(s, `"version": "1.3.0"`, `"version": "`+version+`"`, 1)
			if !perms {
				s = strings.Replace(s, `"chat.send", "events.publish", "points.rounds"`, `"events.publish", "points.rounds"`, 1)
			}
			if dropRoll {
				i := strings.Index(s, "{\n        \"name\": \"roll\",")
				j := strings.Index(s, "{\n        \"name\": \"roll_and_say\",")
				s = s[:i] + s[j:]
			}
		case "plugin.lua":
			if dropRoll {
				s = strings.Replace(s, `sg.action("roll", function`, `local _ = (function`, 1)
			}
		}
		return []byte(s)
	})
}

// Updating and rolling back a plugin through the API (D-072): the check
// names new permissions, dropped steps and the workflows using them;
// updating asks only for new permissions; workflows using a dropped step
// are turned off.
func TestPluginUpdatesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	digest := func(data string) string {
		var pv struct{ Digest string }
		code, out := call("POST", "/v1/packages/check", data)
		if code != 200 {
			t.Fatal(code, out)
		}
		_ = json.Unmarshal([]byte(out), &pv)
		return pv.Digest
	}
	// 1.0.0 asks only to send events.
	v1 := diceVersion(t, "1.0.0", false, false)
	if code, out := call("POST", "/v1/packages?digest="+digest(v1)+"&permissions=events.publish,points.rounds", v1); code != 201 {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/workflows/chat.roll", diceWorkflowAPI); code != 200 && code != 201 {
		t.Fatal(code, out)
	}
	call("PUT", "/v1/workflows/chat.roll/enabled", `{"enabled": true}`)
	call("PUT", "/v1/packages/dice-tray/enabled", `{"enabled": true}`)

	// 1.2.0 asks for chat.send and drops "Roll dice", which chat.roll uses.
	v2 := diceVersion(t, "1.2.0", true, true)
	code, out := call("POST", "/v1/packages/check", v2)
	if code != 200 || !strings.Contains(out, `"plugin_update":{"new_permissions":[{"name":"chat.send"`) ||
		!strings.Contains(out, `"removed_steps":[{"name":"roll"`) || !strings.Contains(out, `"workflows":["Roll"]`) {
		t.Fatal(code, out)
	}
	d2 := digest(v2)
	if code, out := call("POST", "/v1/packages?digest="+d2, v2); code != 409 || !strings.Contains(out, "confirm_permissions") ||
		!strings.Contains(out, "asks for more: chat.send") {
		t.Fatal(code, out)
	}
	code, out = call("POST", "/v1/packages?digest="+d2+"&permissions=chat.send,events.publish,points.rounds", v2)
	if code != 201 || !strings.Contains(out, `"from":"1.0.0"`) || !strings.Contains(out, `"workflows":["Roll"]`) || !strings.Contains(out, `"enabled":true`) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/workflows/chat.roll", ""); !strings.Contains(out, `"enabled":false`) {
		t.Fatal("still on:", out)
	}

	// Its roll back is described, with nothing to approve; rolling back
	// brings the step back and turns nothing off.
	_, out = call("GET", "/v1/packages", "")
	if !strings.Contains(out, `"rollback_to":"1.0.0"`) || !strings.Contains(out, `"rollback":{"new_permissions":[],"permissions":["events.publish","points.rounds"],"removed_steps":[],"reset":[],"workflows":[]}`) {
		t.Fatal(out)
	}
	code, out = call("POST", "/v1/packages/dice-tray/rollback", "")
	if code != 200 || !strings.Contains(out, `"from":"1.2.0"`) || !strings.Contains(out, `"version":"1.0.0"`) || !strings.Contains(out, `"enabled":true`) {
		t.Fatal(code, out)
	}
	// Forward again, then to a version without permissions; rolling back to
	// 1.2.0 then needs chat.send approved.
	call("POST", "/v1/packages?digest="+d2+"&permissions=chat.send,events.publish,points.rounds", v2)
	v3 := diceVersion(t, "1.3.0", false, true)
	if code, out := call("POST", "/v1/packages?digest="+digest(v3)+"&permissions=events.publish,points.rounds", v3); code != 201 {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages/dice-tray/rollback", ""); code != 409 || !strings.Contains(out, "confirm_permissions") {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages/dice-tray/rollback?permissions=chat.send,events.publish,points.rounds", ""); code != 200 || !strings.Contains(out, `"version":"1.2.0"`) {
		t.Fatal(code, out)
	}
	_, out = call("GET", "/v1/packages/audit", "")
	if !strings.Contains(out, "permissions approved: chat.send") || !strings.Contains(out, "steps removed: roll") {
		t.Fatal(out)
	}
}
