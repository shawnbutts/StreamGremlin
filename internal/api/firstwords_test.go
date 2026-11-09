package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A test chat message makes test first words, once per viewer; the
// status counts real chat only; a reset starts over.
func TestFirstWordsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	for _, id := range []string{"f1", "f2"} {
		simulate(t, s, `{"type":"chat.message.received","request_id":"`+id+`","fields":{"user":"Fan","text":"hi"}}`)
	}
	var page EventPage
	for range 100 {
		w := do(t, s, req{method: "GET", path: "/v1/events?types=chat.first_words.received", token: testToken})
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if len(page.Events) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	w := do(t, s, req{method: "GET", path: "/v1/events?types=chat.first_words.received", token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Events) != 1 || page.Events[0].DeliveryQuality != "synthetic" || page.Events[0].Actor.Login != "fan" {
		t.Fatalf("%s", w.Body)
	}
	w = do(t, s, req{method: "GET", path: "/v1/first-words", token: testToken})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"viewers":0`) || !strings.Contains(w.Body.String(), `"why":"12 hours"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = do(t, s, req{method: "POST", path: "/v1/first-words/reset", token: testToken})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"why":"reset"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/first-words/reset"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
