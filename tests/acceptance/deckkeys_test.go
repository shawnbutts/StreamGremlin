//go:build acceptance

package acceptance

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Stream Deck counter, status and switch keys through the real engine and
// the stand-in Twitch (M10 slice 1, D-123, ADR-0053), with the plugin's
// side as plain HTTP: nothing is usable until the streamer chooses it; a
// counter counts once per request id, also across a restart; a workflow
// switch turns it off; the chat lock locks Twitch chat once, recorded as
// from the Stream Deck, and unlocks; the deck still can't read or choose
// anything else.
func TestStreamDeckKeys(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	must := func(args ...string) string {
		t.Helper()
		o, errOut, code := cli(t, dir, args...)
		if code != 0 {
			t.Fatalf("%v: %s %s", args, o, errOut)
		}
		return o
	}
	must("twitch", "client-id", fixtureClientID)
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	must("vars", "set", "global", "deaths", "7")
	must("workflows", "save", filepath.Join("..", "..", "examples", "workflows", "hello.json"))
	hello := workflowID(t, "hello.json")
	must("workflows", "on", hello)

	var p struct{ Code string }
	if err := json.Unmarshal([]byte(must("streamdeck", "pair", "-wait=false", "-json")), &p); err != nil {
		t.Fatal(err)
	}
	code, body := call(t, e, "POST", "/v1/pair", "", `{"code":"`+p.Code+`","name":"Desk deck","request_id":"r1"}`, nil)
	var paired struct{ Token string }
	if err := json.Unmarshal([]byte(body), &paired); err != nil || code != 201 {
		t.Fatal(code, body)
	}
	deck := paired.Token
	expect := func(method, path, token, in string, want int, has ...string) string {
		t.Helper()
		code, body := call(t, e, method, path, token, in, nil)
		if code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, code, body, want)
		}
		for _, h := range has {
			if !strings.Contains(body, h) {
				t.Errorf("%s %s doesn't say %s: %s", method, path, h, body)
			}
		}
		return body
	}

	// Nothing until chosen; the deck can't choose, or read variables.
	expect("GET", "/v1/deck", deck, "", 200, `"counters":[]`, `"switches":[]`)
	expect("POST", "/v1/deck/counters/deaths", deck, `{"change":"add","request_id":"c1"}`, 403)
	choice := `{"counters":["deaths"],"switches":[{"kind":"workflow","id":"` + hello + `"},{"kind":"chat_lock"}]}`
	expect("PUT", "/v1/settings/streamdeck", deck, choice, 403)
	expect("GET", "/v1/variables", deck, "", 403)
	expect("PUT", "/v1/settings/streamdeck", e.token, choice, 200)

	// Signed in, the lock is known: not locked.
	expect("GET", "/v1/deck", deck, "", 200, `"chat_locked":false`, `{"name":"deaths","value":7}`, `"label":"Chat lock","on":false`)

	// A counter counts once per request id.
	expect("POST", "/v1/deck/counters/deaths", deck, `{"change":"add","request_id":"c1"}`, 200, `"value":8`)
	expect("POST", "/v1/deck/counters/deaths", deck, `{"change":"add","request_id":"c1"}`, 200, `"value":8`)
	if out := must("vars", "global"); !strings.Contains(out, "8") {
		t.Fatal(out)
	}

	// A workflow switch.
	expect("PUT", "/v1/deck/switches/workflow/"+hello, deck, `{"on":false}`, 200, `"on":false`)
	expect("GET", "/v1/workflows/"+hello, e.token, "", 200, `"enabled":false`)

	// The chat lock: once, recorded as from the deck; again is harmless.
	expect("PUT", "/v1/deck/switches/chat_lock/-", deck, `{"on":true}`, 200, `"on":true`)
	if !tw.ShieldActive("streamer") {
		t.Fatal("Shield Mode isn't on")
	}
	expect("PUT", "/v1/deck/switches/chat_lock/-", deck, `{"on":true}`, 200, `"on":true`)
	hist := expect("GET", "/v1/moderation", e.token, "", 200)
	if n := strings.Count(hist, `"action":"lock"`); n != 1 || !strings.Contains(hist, `"from":"stream_deck"`) {
		t.Fatalf("locks %d: %s", n, hist)
	}
	expect("GET", "/v1/deck", deck, "", 200, `"chat_locked":true`)
	expect("PUT", "/v1/deck/switches/chat_lock/-", deck, `{"on":false}`, 200, `"on":false`)
	if tw.ShieldActive("streamer") {
		t.Fatal("Shield Mode is still on")
	}

	// Run a workflow (D-124): only the one named starts, once per request
	// id, whatever its trigger; a command workflow is said to need a viewer.
	must("workflows", "save", filepath.Join("..", "..", "examples", "workflows", "lurk.json"))
	lurk := workflowID(t, "lurk.json")
	must("workflows", "on", lurk)
	expect("PUT", "/v1/deck/switches/workflow/"+hello, deck, `{"on":true}`, 200, `"on":true`)
	expect("PUT", "/v1/settings/streamdeck", e.token, `{"counters":["deaths"],"switches":[{"kind":"chat_lock"}],"runs":["`+hello+`"],"scenes":true}`, 200)
	expect("GET", "/v1/deck", deck, "", 200, `"needs_viewer":true`, `"scenes":{"known":false`)
	expect("POST", "/v1/deck/runs/"+hello, deck, `{"request_id":"run1"}`, 200, `"duplicate":false`)
	expect("POST", "/v1/deck/runs/"+hello, deck, `{"request_id":"run1"}`, 200, `"duplicate":true`)
	expect("POST", "/v1/deck/runs/"+lurk, deck, `{"request_id":"run2"}`, 403)
	eventually(t, "the run", func() string {
		_, body := call(t, e, "GET", "/v1/jobs?workflow="+hello, e.token, "", nil)
		if strings.Count(body, `"workflow_id":"`+hello+`"`) != 1 {
			return "not yet: " + body
		}
		return ""
	})
	if _, body := call(t, e, "GET", "/v1/jobs?workflow="+lurk, e.token, "", nil); strings.Contains(body, `"workflow_id"`) {
		t.Errorf("lurk ran too: %s", body)
	}
	// OBS isn't connected: a scene switch is refused, not pretended.
	if code, body := call(t, e, "POST", "/v1/deck/scene", deck, `{"scene":"BRB"}`, nil); code < 400 {
		t.Errorf("scene with no OBS: %d %s", code, body)
	}

	// After a restart: the choice and the value are kept.
	stopEngine(t, e)
	e = startEngine(t, dir, "-twitch-auth-url", twURL)
	expect("GET", "/v1/deck", deck, "", 200, `{"name":"deaths","value":8}`)
	expect("POST", "/v1/deck/counters/deaths", deck, `{"change":"reset","request_id":"c2"}`, 200, `"value":0`)

	// Un-chosen: refused at once.
	expect("PUT", "/v1/settings/streamdeck", e.token, `{"counters":[],"switches":[]}`, 200)
	expect("POST", "/v1/deck/counters/deaths", deck, `{"change":"add","request_id":"c3"}`, 403)
	expect("PUT", "/v1/deck/switches/chat_lock/-", deck, `{"on":true}`, 403)
}
