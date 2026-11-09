package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPollsAndPredictionsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	view := func() PollsView {
		_, out := call("GET", "/v1/polls", "")
		var v PollsView
		_ = json.Unmarshal([]byte(out), &v)
		return v
	}
	if v := view(); v.State != RewardsNotSignedIn {
		t.Fatalf("%+v", v)
	}
	call("PUT", "/v1/connections/twitch/client-id", `{"client_id":"abcdefghij0123456789"}`)
	_, out := call("POST", "/v1/connections/twitch/streamer/sign-in", "")
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal([]byte(out), &p)
	_ = testMock.Approve(p.UserCode, "pollstreamer")
	deadline := time.Now().Add(5 * time.Second)
	for view().State != RewardsOK {
		if time.Now().After(deadline) {
			t.Fatalf("%+v", view())
		}
		time.Sleep(50 * time.Millisecond)
	}
	poll := `{"title":"What next?","choices":["Celeste","Hades"],"seconds":60}`
	if code, out := call("POST", "/v1/polls", poll); code != 201 || !strings.Contains(out, `"status":"active"`) {
		t.Fatalf("start: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/polls", poll); code != 200 {
		t.Fatal("again:", code)
	}
	if code, out := call("POST", "/v1/polls", `{"title":"Other?","choices":["a","b"],"seconds":60}`); code != 409 || !strings.Contains(out, "already running") {
		t.Fatalf("a second: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/polls", `{"title":"q","choices":["a"],"seconds":60}`); code != 400 {
		t.Fatal("one choice:", code)
	}
	if v := view(); v.Poll == nil || v.Poll.Title != "What next?" {
		t.Fatalf("%+v", v)
	}
	if code, out := call("POST", "/v1/polls/end", `{"archive":true}`); code != 200 || !strings.Contains(out, `"status":"archived"`) {
		t.Fatalf("end: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/polls/end", ""); code != 409 {
		t.Fatal("end again:", code)
	}

	if code, out := call("POST", "/v1/predictions", `{"title":"Win?","outcomes":["Yes","No"],"seconds":60}`); code != 201 {
		t.Fatalf("prediction: %d %s", code, out)
	}
	if code, out := call("POST", "/v1/predictions/end", `{"how":"resolve","winner":"yes"}`); code != 200 || !strings.Contains(out, `"status":"resolved"`) {
		t.Fatalf("resolve: %d %s", code, out)
	}
	if code, out := call("POST", "/v1/predictions/end", `{"how":"cancel"}`); code != 409 {
		t.Fatalf("cancel after: %d %s", code, out)
	}

	testMock.SetAffiliate("pollstreamer", false)
	if v := view(); v.State != RewardsNoChannelPoint || !strings.Contains(v.Detail, "Affiliate or Partner") {
		t.Fatalf("%+v", v)
	}
}
