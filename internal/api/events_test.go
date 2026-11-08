package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func simulate(t *testing.T, s *Server, body string) (int, SimulateResult) {
	t.Helper()
	w := do(t, s, req{method: "POST", path: "/v1/events/simulate", token: testToken, body: body})
	var res SimulateResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	return w.Code, res
}

func TestSimulateThenRead(t *testing.T) {
	s := newTestServer(t)
	code, res := simulate(t, s, `{"type":"chat.message.received","request_id":"a1","fields":{"user":"Fan","text":"hi"}}`)
	if code != 201 || res.Duplicate || res.Event.Seq != 1 || res.Event.DeliveryQuality != "synthetic" {
		t.Fatalf("%d %+v", code, res)
	}
	code, again := simulate(t, s, `{"type":"chat.message.received","request_id":"a1","fields":{"user":"Fan","text":"hi"}}`)
	if code != 200 || !again.Duplicate || again.Event.EventID != res.Event.EventID {
		t.Fatalf("retry: %d %+v", code, again)
	}
	simulate(t, s, `{"type":"broadcast.scene.changed","request_id":"a2"}`)

	var page EventPage
	w := do(t, s, req{method: "GET", path: "/v1/events", token: testToken})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 2 || page.Head != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = do(t, s, req{method: "GET", path: "/v1/events?after=1", token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Events) != 1 || page.Events[0].Type != "broadcast.scene.changed" {
		t.Fatalf("after: %s", w.Body)
	}
	w = do(t, s, req{method: "GET", path: "/v1/events?types=chat.*", token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Events) != 1 || page.Events[0].Type != "chat.message.received" {
		t.Fatalf("filter: %s", w.Body)
	}
}

func TestEventsLongPollWakes(t *testing.T) {
	s := newTestServer(t)
	done := make(chan EventPage, 1)
	go func() {
		w := do(t, s, req{method: "GET", path: "/v1/events?after=0&wait=10", token: testToken})
		var page EventPage
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		done <- page
	}()
	time.Sleep(50 * time.Millisecond)
	simulate(t, s, `{"type":"chat.message.received","request_id":"w1"}`)
	select {
	case page := <-done:
		if len(page.Events) != 1 {
			t.Fatalf("%+v", page)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long poll didn't wake")
	}
}

func TestEventsRejectBadRequests(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/v1/events?limit=0", "/v1/events?limit=201", "/v1/events?wait=21",
		"/v1/events?after=-1", "/v1/events?after=x", "/v1/events?types=**", "/v1/events?types=twitch.chat.x"} {
		if w := do(t, s, req{method: "GET", path: path, token: testToken}); w.Code != 400 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	for _, body := range []string{`{"type":"chat.nope.happened","request_id":"x"}`, `{"type":"chat.message.received"}`,
		`{"type":"chat.message.received","request_id":"x","fields":{"bits":"1"}}`, `{"type":"chat.message.received","request_id":"x","extra":1}`} {
		if code, _ := simulate(t, s, body); code != 400 {
			t.Errorf("%s: %d", body, code)
		}
	}
	for _, rq := range []req{{method: "GET", path: "/v1/events"}, {method: "GET", path: "/v1/events/catalog"},
		{method: "POST", path: "/v1/events/simulate", body: "{}"}} {
		if w := do(t, s, rq); w.Code != 401 {
			t.Errorf("%s without a token: %d", rq.path, w.Code)
		}
	}
}

func TestCatalogRoute(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/events/catalog", token: testToken})
	var cat EventCatalog
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &cat) != nil || len(cat.Kinds) < 3 || !strings.Contains(w.Body.String(), `"fields"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
