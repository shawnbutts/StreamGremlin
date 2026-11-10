package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/wheels"
)

// Wheels through the API: add (400, 201, 409), the odds, change (a new
// version), spin (202, then 200 for the same request id), the spin settles
// and checks, one spin read back, delete keeps the spins.
func TestWheelsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	if code, _ := call("POST", "/v1/wheels", `{"name":"next-game","title":"Next game","choices":[{"label":"Mario"}]}`); code != 400 {
		t.Fatal("one choice:", code)
	}
	body := `{"name":"next-game","title":"Next game","choices":[{"label":"Mario","weight":2},{"label":"Zelda"},{"label":"Tetris"}]}`
	code, out := call("POST", "/v1/wheels", body)
	if code != 201 || !strings.Contains(out, `"percent":50`) {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/wheels", body); code != 409 {
		t.Fatal("a taken name:", code)
	}
	code, out = call("PUT", "/v1/wheels/next-game", `{"title":"Next game","choices":[{"label":"Mario"},{"label":"Zelda"}]}`)
	if code != 200 || !strings.Contains(out, `"version":2`) {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/wheels/next-game/spin", `{}`); code != 400 {
		t.Fatal("no request id:", code)
	}
	code, out = call("POST", "/v1/wheels/next-game/spin", `{"request_id":"r1"}`)
	if code != 202 {
		t.Fatal(code, out)
	}
	var sp wheels.Spin
	_ = json.Unmarshal([]byte(out), &sp)
	if code, again := call("POST", "/v1/wheels/next-game/spin", `{"request_id":"r1"}`); code != 200 || !strings.Contains(again, sp.ID) {
		t.Fatal("a repeat:", code, again)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out = call("GET", "/v1/spins/"+sp.ID, "")
		_ = json.Unmarshal([]byte(out), &sp)
		if sp.SettledAt != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sp.SettledAt == nil || sp.Display != "no_audience" || len(wheels.Verify(sp)) != 0 || sp.WheelVersion != 2 {
		t.Fatalf("%+v", sp)
	}
	_, out = call("GET", "/v1/wheels", "")
	var v WheelsView
	_ = json.Unmarshal([]byte(out), &v)
	if len(v.Wheels) != 1 || len(v.Spins) != 1 || v.Pages != 0 {
		t.Fatalf("%+v", v)
	}
	if code, _ := call("DELETE", "/v1/wheels/next-game", ""); code != 200 {
		t.Fatal(code)
	}
	if code, _ := call("DELETE", "/v1/wheels/next-game", ""); code != 404 {
		t.Fatal(code)
	}
	if code, out := call("GET", "/v1/spins/"+sp.ID, ""); code != 200 {
		t.Fatal("the spin went with the wheel:", code, out)
	}
	if code, _ := call("POST", "/v1/wheels/next-game/spin", `{"request_id":"r2"}`); code != 404 {
		t.Fatal("a deleted wheel spun:", code)
	}
}
