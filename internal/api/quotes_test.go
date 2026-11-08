package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQuotesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	code, out := call("POST", "/v1/quotes", `{"text":"I meant to do that","category":"Celeste","request_id":"r1"}`)
	if code != 201 || !strings.Contains(out, `"id":1`) || !strings.Contains(out, `"added_by":"you"`) {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, out = call("POST", "/v1/quotes", `{"text":"I meant to do that","request_id":"r1"}`); code != 200 || !strings.Contains(out, `"id":1`) {
		t.Fatalf("repeat: %d %s", code, out)
	}
	call("POST", "/v1/quotes", `{"text":"second","added_by":"Fan"}`)
	for _, bad := range []string{`{"text":""}`, `{"text":"x","added_by_id":"twitch:1"}`, `{"text":"x","platform":"twitch"}`, `not json`} {
		if code, out := call("POST", "/v1/quotes", bad); code != 400 {
			t.Errorf("%s: %d %s", bad, code, out)
		}
	}
	code, out = call("GET", "/v1/quotes?q=fan", "")
	var list QuoteList
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != 200 || len(list.Quotes) != 1 || list.Count != 2 || list.Next != 3 {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out = call("PUT", "/v1/quotes/1", `{"text":"I totally meant to do that","added_by":"you","category":"Celeste"}`); code != 200 ||
		!strings.Contains(out, "totally") {
		t.Fatalf("edit: %d %s", code, out)
	}
	if code, _ = call("PUT", "/v1/quotes/%231", `{"text":"by #1"}`); code != 200 {
		t.Fatalf("edit #1: %d", code)
	}
	if code, _ = call("PUT", "/v1/quotes/9", `{"text":"x"}`); code != 404 {
		t.Fatal(code)
	}
	if code, _ = call("PUT", "/v1/quotes/abc", `{"text":"x"}`); code != 404 {
		t.Fatal(code)
	}
	if code, _ = call("DELETE", "/v1/quotes/1", ""); code != 204 {
		t.Fatal(code)
	}
	if code, _ = call("DELETE", "/v1/quotes/1", ""); code != 404 {
		t.Fatal(code)
	}
	if code, out = call("POST", "/v1/quotes", `{"text":"third"}`); !strings.Contains(out, `"id":3`) {
		t.Fatalf("numbers aren't reused: %d %s", code, out)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/quotes"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
