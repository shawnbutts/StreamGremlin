package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAdsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	view := func() AdsView {
		_, out := call("GET", "/v1/ads", "")
		var v AdsView
		_ = json.Unmarshal([]byte(out), &v)
		return v
	}
	if v := view(); v.State != RewardsNotSignedIn || v.Schedule != nil {
		t.Fatalf("%+v", v)
	}
	call("PUT", "/v1/connections/twitch/client-id", `{"client_id":"abcdefghij0123456789"}`)
	_, out := call("POST", "/v1/connections/twitch/streamer/sign-in", "")
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal([]byte(out), &p)
	_ = testMock.Approve(p.UserCode, "adstreamer")
	deadline := time.Now().Add(5 * time.Second)
	for view().State != RewardsOK {
		if time.Now().After(deadline) {
			t.Fatalf("%+v", view())
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Not live: Twitch refuses, and nothing is announced.
	if code, out := call("POST", "/v1/ads/commercial", `{"length":60,"op_key":"a"}`); code != 409 || !strings.Contains(out, "live") {
		t.Fatalf("offline: %d %s", code, out)
	}
	deadline = time.Now().Add(5 * time.Second)
	for testMock.SubscribedTo("adstreamer", "channel.ad_break.begin") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ad breaks never followed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	testMock.SetLive("adstreamer", true)
	if code, out := call("POST", "/v1/ads/commercial", `{"length":60,"op_key":"b"}`); code != 200 || !strings.Contains(out, `"status":"accepted"`) {
		t.Fatalf("start: %d %s", code, out)
	}
	// The same key again: the first answer, not a second ad.
	if code, _ := call("POST", "/v1/ads/commercial", `{"length":60,"op_key":"b"}`); code != 200 || testMock.Commercials() != 2 {
		t.Fatal("again:", code, testMock.Commercials())
	}
	// Inside Twitch's gap: refused here, never sent.
	if code, out := call("POST", "/v1/ads/commercial", `{"length":30}`); code != 409 || !strings.Contains(out, "break between ads") ||
		testMock.Commercials() != 2 {
		t.Fatalf("cooldown: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/ads/commercial", `{"length":181}`); code != 400 {
		t.Fatal("too long:", code)
	}
	// Twitch's event says the break began: then, and only then, it's listed.
	deadline = time.Now().Add(5 * time.Second)
	for v := view(); len(v.Breaks) != 1 || v.Breaks[0].RequestID == ""; v = view() {
		if time.Now().After(deadline) {
			t.Fatalf("%+v", v)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code, out := call("POST", "/v1/ads/snooze", ""); code != 200 || !strings.Contains(out, `"snooze_count":2`) {
		t.Fatalf("snooze: %d %s", code, out)
	}
	v := view()
	if v.Schedule == nil || v.Schedule.SnoozeCount != 2 || v.Schedule.LastAdAt == nil || len(v.Requests) != 3 {
		t.Fatalf("%+v", v)
	}
	testMock.SetAffiliate("adstreamer", false)
	if v := view(); v.State != RewardsNoChannelPoint {
		t.Fatalf("%+v", v)
	}
}
