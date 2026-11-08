package api

import (
	"encoding/json"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/timers"
)

func TestTimersThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	body := `{"name":"Socials","enabled":true,"every_seconds":600,"up_to_seconds":900,"chat_lines":5,"live_only":true,"counter":12}`
	code, out := call("PUT", "/v1/timers/socials", body)
	if code != 201 || !strings.Contains(out, `"counter":12`) || !strings.Contains(out, `"next_at"`) {
		t.Fatalf("create: %d %s", code, out)
	}
	// Repeating it is harmless, and the count can't be set on an existing timer.
	if code, out = call("PUT", "/v1/timers/socials", strings.Replace(body, `"counter":12`, `"counter":0`, 1)); code != 200 || !strings.Contains(out, `"counter":12`) {
		t.Fatalf("repeat: %d %s", code, out)
	}
	if code, out = call("PUT", "/v1/timers/nightly", `{"name":"Nightly","cron":"0 20 * * *"}`); code != 201 || !strings.Contains(out, `"waiting":"off"`) {
		t.Fatalf("cron: %d %s", code, out)
	}
	for path, bad := range map[string]string{
		"/v1/timers/socials": `{"name":"Socials","every_seconds":5}`,
		"/v1/timers/x":       `{"name":"X","cron":"0 20 * * *","every_seconds":60}`,
		"/v1/timers/y":       `{"name":"Y","cron":"whenever"}`,
		"/v1/timers/Bad":     `{"name":"Bad","every_seconds":60}`,
		"/v1/timers/z":       `{"id":"other","name":"Z","every_seconds":60}`,
		"/v1/timers/w":       `{"name":"W","every_seconds":60,"extra":1}`,
	} {
		if code, out := call("PUT", path, bad); code != 400 || !strings.Contains(out, CodeInvalidRequest) {
			t.Errorf("%s %s: %d %s", path, bad, code, out)
		}
	}
	code, out = call("GET", "/v1/timers", "")
	var list TimerList
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != 200 || len(list.Timers) != 2 || list.Live != nil {
		t.Fatalf("list: %d %s", code, out)
	}
	if list.Timers[0].ID != "nightly" || list.Timers[1].ID != "socials" || !list.Timers[1].LiveOnly || list.Timers[1].ChatLines != 5 {
		t.Fatalf("%+v", list.Timers)
	}

	// Run now works even while it's off, counts, and records the event.
	code, out = call("POST", "/v1/timers/nightly/fire", "")
	var fired struct {
		Timer timers.Timer
		Event struct {
			Type    string          `json:"type"`
			Source  string          `json:"source"`
			Payload json.RawMessage `json:"payload"`
		}
	}
	if err := json.Unmarshal([]byte(out), &fired); err != nil || code != 200 || fired.Timer.Counter != 1 || fired.Event.Type != "timers.timer.fired" ||
		fired.Event.Source != "timers" || !strings.Contains(string(fired.Event.Payload), `"manual":true`) {
		t.Fatalf("fire: %d %s", code, out)
	}
	if code, out = call("POST", "/v1/timers/nightly/fire", ""); code != 200 || !strings.Contains(out, `"counter":2`) {
		t.Fatalf("fire again: %d %s", code, out)
	}
	if code, _ = call("POST", "/v1/timers/nope/fire", ""); code != 404 {
		t.Fatal(code)
	}

	if code, _ = call("DELETE", "/v1/timers/nightly", ""); code != 204 {
		t.Fatal(code)
	}
	if code, _ = call("DELETE", "/v1/timers/nightly", ""); code != 404 {
		t.Fatal(code)
	}
	w := do(t, s, req{method: "GET", path: "/v1/timers"})
	if w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}
