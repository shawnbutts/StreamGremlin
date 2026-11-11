package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

const brbWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"live.brb","name":"Be right back",
	"triggers":[{"type":"buttons.button.pressed","where":{"button_id":"brb"}}],
	"steps":[{"action":"note","with":{"text":"brb"}}]}`

func TestButtonsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	if code, out := call("PUT", "/v1/buttons/brb", `{"label":"Be right back","color":"blue"}`); code != 201 || !strings.Contains(out, `"position":0`) {
		t.Fatalf("create: %d %s", code, out)
	}
	if code, out := call("PUT", "/v1/buttons/brb", `{"label":"Be right back","color":"blue"}`); code != 200 {
		t.Fatalf("repeat: %d %s", code, out)
	}
	if code, _ := call("PUT", "/v1/buttons/hype", `{"label":"Hype!"}`); code != 201 {
		t.Fatal(code)
	}
	for path, bad := range map[string]string{
		"/v1/buttons/Bad": `{"label":"x"}`,
		"/v1/buttons/x":   `{"label":""}`,
		"/v1/buttons/y":   `{"label":"y","color":"plaid"}`,
		"/v1/buttons/z":   `{"id":"other","label":"z"}`,
	} {
		if code, out := call("PUT", path, bad); code != 400 || !strings.Contains(out, CodeInvalidRequest) {
			t.Errorf("%s %s: %d %s", path, bad, code, out)
		}
	}

	// A workflow that starts from the button, on.
	if code, out := call("PUT", "/v1/workflows/live.brb", brbWorkflow); code != 200 {
		t.Fatalf("workflow: %d %s", code, out)
	}
	call("PUT", "/v1/workflows/live.brb/enabled", `{"enabled":true}`)

	var list ButtonList
	code, out := call("GET", "/v1/buttons", "")
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != 200 || len(list.Buttons) != 2 {
		t.Fatalf("list: %d %s", code, out)
	}
	if b := list.Buttons[0]; b.ID != "brb" || len(b.Starts) != 1 || b.Starts[0].ID != "live.brb" || len(list.Buttons[1].Starts) != 0 {
		t.Fatalf("starts: %+v", list.Buttons)
	}

	// Pressing it runs the workflow, once per request id.
	var pressed PressResult
	code, out = call("POST", "/v1/buttons/brb/press", `{"request_id":"p1"}`)
	if err := json.Unmarshal([]byte(out), &pressed); err != nil || code != 200 || pressed.Duplicate ||
		pressed.Event.Type != "buttons.button.pressed" || len(pressed.Started) != 1 {
		t.Fatalf("press: %d %s", code, out)
	}
	if !strings.Contains(string(pressed.Event.Payload), `"from":"live"`) {
		t.Fatalf("from: %s", pressed.Event.Payload)
	}
	code, out = call("POST", "/v1/buttons/brb/press", `{"request_id":"p1"}`)
	if code != 200 || !strings.Contains(out, `"duplicate":true`) {
		t.Fatalf("repeat press: %d %s", code, out)
	}
	var jobs struct{ Jobs []workflow.Job }
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out = call("GET", "/v1/jobs?workflow=live.brb", "")
		_ = json.Unmarshal([]byte(out), &jobs)
		if len(jobs.Jobs) == 1 && jobs.Jobs[0].Status == workflow.JobSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs: %s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The CLI says it's the CLI; nothing else can be claimed.
	if code, out = call("POST", "/v1/buttons/hype/press", `{"from":"cli"}`); code != 200 || !strings.Contains(out, `"started":[]`) {
		t.Fatalf("cli press: %d %s", code, out)
	}
	if code, _ = call("POST", "/v1/buttons/hype/press", `{"from":"stream_deck"}`); code != 400 {
		t.Fatalf("from stream_deck with the app's token: %d", code)
	}
	if code, _ = call("POST", "/v1/buttons/nope/press", ``); code != 404 {
		t.Fatal(code)
	}

	// The order.
	if code, out = call("PUT", "/v1/button-order", `{"order":["hype","brb"]}`); code != 200 || !strings.Contains(out, `"id":"hype"`) {
		t.Fatalf("order: %d %s", code, out)
	}
	if code, _ = call("PUT", "/v1/button-order", `{"order":["hype"]}`); code != 400 {
		t.Fatal(code)
	}
	_, out = call("GET", "/v1/buttons", "")
	_ = json.Unmarshal([]byte(out), &list)
	if list.Buttons[0].ID != "hype" {
		t.Fatalf("%+v", list.Buttons)
	}

	if code, _ = call("DELETE", "/v1/buttons/brb", ""); code != 204 {
		t.Fatal(code)
	}
	if code, _ = call("DELETE", "/v1/buttons/brb", ""); code != 404 {
		t.Fatal(code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/buttons"}); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}
