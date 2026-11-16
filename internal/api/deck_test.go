package api

import (
	"strings"
	"testing"
)

// The Stream Deck's keys beyond buttons (D-123, ADR-0053): nothing is
// usable until chosen, a chosen counter changes once per request id and is
// recorded as from the deck, and a deck can't choose for itself.
func TestDeckKeys(t *testing.T) {
	s := newTestServer(t)
	deck := pairDeck(t, s)

	w := do(t, s, req{method: "GET", path: "/v1/deck", token: deck})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"counters":[]`) || !strings.Contains(w.Body.String(), `"live":null`) {
		t.Fatalf("nothing chosen: %d %s", w.Code, w.Body.String())
	}
	w = do(t, s, req{method: "POST", path: "/v1/deck/counters/deaths", token: deck, body: `{"change":"add","request_id":"r1"}`})
	if w.Code != 403 || errCode(t, w) != CodeForbidden {
		t.Fatalf("not chosen: %d %s", w.Code, w.Body.String())
	}
	// The deck can't choose for itself; the streamer can.
	choice := `{"counters":["deaths"],"switches":[{"kind":"chat_lock"}]}`
	if w := do(t, s, req{method: "PUT", path: "/v1/settings/streamdeck", token: deck, body: choice}); w.Code != 403 {
		t.Fatalf("the deck chose: %d", w.Code)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/settings/streamdeck", token: testToken, body: `{"counters":["Deaths"]}`}); w.Code != 400 {
		t.Fatalf("a bad name: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/settings/streamdeck", token: testToken, body: choice}); w.Code != 200 {
		t.Fatalf("choose: %d %s", w.Code, w.Body.String())
	}
	for i, id := range []string{"r1", "r1", "r2"} {
		w := do(t, s, req{method: "POST", path: "/v1/deck/counters/deaths", token: deck, body: `{"change":"add","request_id":"` + id + `"}`})
		want := []string{`"value":1`, `"value":1`, `"value":2`}[i]
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("press %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	w = do(t, s, req{method: "GET", path: "/v1/variables?scope=global", token: testToken})
	if !strings.Contains(w.Body.String(), `"updated_by":"stream_deck:cl_`) {
		t.Errorf("not recorded as from the deck: %s", w.Body.String())
	}
	if w := do(t, s, req{method: "POST", path: "/v1/deck/counters/deaths", token: deck, body: `{"change":"double","request_id":"r3"}`}); w.Code != 400 {
		t.Errorf("a bad change: %d", w.Code)
	}
	// Signed out of Twitch, the chat lock is unknown: shown so, and refused.
	w = do(t, s, req{method: "GET", path: "/v1/deck", token: deck})
	if !strings.Contains(w.Body.String(), `"chat_locked":null`) || !strings.Contains(w.Body.String(), `"label":"Chat lock","on":null`) {
		t.Errorf("lock unknown: %s", w.Body.String())
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/deck/switches/chat_lock/-", token: deck, body: `{"on":true}`}); w.Code != 409 || errCode(t, w) != CodeUnknownOutcome {
		t.Errorf("lock while unknown: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/deck/switches/workflow/wf_x", token: deck, body: `{"on":true}`}); w.Code != 403 {
		t.Errorf("an unchosen workflow: %d", w.Code)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/deck/switches/chat_lock/-", token: deck, body: `{}`}); w.Code != 400 {
		t.Errorf("no on: %d", w.Code)
	}
}
