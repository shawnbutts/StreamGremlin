package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplePlugin zips examples/plugins/dice-tray.
func examplePlugin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "examples", "plugins", "dice-tray")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"streamgremlin.json", "plugin.lua", "README.md", "templates/roll.json", "templates/best.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the repository's own example
		if err != nil {
			t.Fatal(err)
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
	if code != 200 || len(pv.Plugin.Permissions) != 1 || pv.Plugin.Permissions[0].Name != "chat.send" || len(pv.Plugin.Actions) != 3 {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest, data); code != 409 || !strings.Contains(out, "confirm_permissions") ||
		!strings.Contains(out, "asks for chat.send") {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest+"&permissions=chat.send", data); code != 201 {
		t.Fatal(code, out)
	}
	// Its steps are there for workflows, while it's off.
	if code, out := call("PUT", "/v1/workflows/chat.roll", diceWorkflowAPI); code != 200 && code != 201 {
		t.Fatal(code, out)
	}
	_, out = call("GET", "/v1/packages", "")
	if !strings.Contains(out, `"enabled":false`) || !strings.Contains(out, `"workflows":["Roll"]`) || !strings.Contains(out, `"values":{"cheer":true,"sides":6}`) {
		t.Fatal(out)
	}

	// Its templates are in the gallery, in its own section.
	_, out = call("GET", "/v1/templates", "")
	if !strings.Contains(out, `"id":"dice-tray/roll"`) || !strings.Contains(out, `"category":"Dice tray"`) ||
		!strings.Contains(out, `"add_on":{"id":"dice-tray","name":"Dice tray","publisher":"StreamGremlin examples","enabled":false}`) ||
		!strings.Contains(out, `"Stream","Dice tray"]`) {
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

	// Removed: its workflow is turned off, not deleted.
	call("PUT", "/v1/workflows/chat.roll/enabled", `{"enabled": true}`)
	code, out = call("DELETE", "/v1/packages/dice-tray", "")
	if code != 200 || !strings.Contains(out, `"workflows":["Roll"]`) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/workflows/chat.roll", ""); !strings.Contains(out, `"enabled":false`) {
		t.Fatal("still on:", out)
	}
	if _, out := call("GET", "/v1/templates", ""); strings.Contains(out, "dice-tray/") || strings.Contains(out, `"Dice tray"`) {
		t.Fatal("its templates outlived it:", out)
	}
	if _, out := call("GET", "/v1/packages/audit", ""); !strings.Contains(out, `"action":"on"`) || !strings.Contains(out, `"action":"settings"`) {
		t.Fatal(out)
	}
}
