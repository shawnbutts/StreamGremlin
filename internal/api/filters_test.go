package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The chat filters through the API: the defaults, off; bad settings
// refused; saved settings tidied; trying a message; a test chat message
// caught as a test, never reaching Twitch; and moderating requests can't
// claim to come from a filter.
func TestFiltersThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	get := func() FiltersView {
		t.Helper()
		w := do(t, s, req{method: "GET", path: "/v1/filters", token: testToken})
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var v FiltersView
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	v := get()
	if v.Settings.On || v.Settings.Links.On || len(v.Recent) != 0 || v.Settings.Links.Allowed[0] != "twitch.tv" {
		t.Fatalf("%+v", v)
	}

	st := v.Settings
	st.Links.Action = "ban"
	b, _ := json.Marshal(st)
	if w := do(t, s, req{method: "PUT", path: "/v1/filters", token: testToken, body: string(b)}); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "deletes, times out or warns") {
		t.Fatalf("ban: %d %s", w.Code, w.Body.String())
	}

	st = v.Settings
	st.On, st.Links.On, st.Words.On, st.Words.List = true, true, true, []string{"  Spoil* "}
	b, _ = json.Marshal(st)
	w := do(t, s, req{method: "PUT", path: "/v1/filters", token: testToken, body: string(b)})
	var saved FiltersView
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if w.Code != 200 || saved.Settings.Words.List[0] != "spoil*" || !get().Settings.Links.On {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}

	check := func(body string) (int, FilterVerdict) {
		w := do(t, s, req{method: "POST", path: "/v1/filters/check", token: testToken, body: body})
		var fv FilterVerdict
		_ = json.Unmarshal(w.Body.Bytes(), &fv)
		return w.Code, fv
	}
	if code, fv := check(`{"text":"no spoilers please"}`); code != 200 || fv.Rule != "words" || fv.Reason == "" {
		t.Fatalf("%d %+v", code, fv)
	}
	if code, fv := check(`{"text":"bit.ly/x","role":"moderator"}`); code != 200 || fv.Rule != "" {
		t.Fatalf("moderator: %d %+v", code, fv)
	}
	if code, fv := check(`{"text":"WHY SO LOUD IN HERE","settings":` + strings.Replace(string(b), `"caps":{"on":false`, `"caps":{"on":true`, 1) + `}`); code != 200 || fv.Rule != "caps" {
		t.Fatalf("with settings: %d %+v", code, fv)
	}
	if code, _ := check(`{"text":"x","role":"king"}`); code != 400 {
		t.Fatal("a made-up role")
	}

	calls := len(testMock.ModCalls())
	simulate(t, s, `{"type":"chat.message.received","request_id":"f1","fields":{"user":"Spammer","text":"cheap followers at bit.ly/x"}}`)
	deadline := time.Now().Add(5 * time.Second)
	for len(get().Recent) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the test message wasn't caught")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c := get().Recent[0]
	if c.Rule != "links" || c.State != "test" || c.Login != "spammer" || c.Text != "cheap followers at bit.ly/x" {
		t.Fatalf("%+v", c)
	}
	if len(testMock.ModCalls()) != calls {
		t.Fatal("a test message reached Twitch")
	}

	if w := do(t, s, req{method: "POST", path: "/v1/moderation", token: testToken,
		body: `{"request_id":"x1","action":"unban","platform":"twitch","user_id":"1","from":"filter"}`}); w.Code != 400 {
		t.Fatalf("from filter: %d", w.Code)
	}
}
