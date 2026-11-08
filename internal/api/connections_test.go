package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func connections(t *testing.T, s *Server) Connections {
	t.Helper()
	w := do(t, s, req{method: "GET", path: "/v1/connections", token: testToken})
	var c Connections
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return c
}

func TestTwitchSignInThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	if c := connections(t, s); c.Twitch.Streamer.State != "not_configured" || c.Twitch.TestServer == "" {
		t.Fatalf("%+v", c.Twitch)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-in", token: testToken}); w.Code != 409 {
		t.Fatalf("sign-in without a client id: %d", w.Code)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/connections/twitch/client-id", token: testToken, body: `{"client_id":"NOPE"}`}); w.Code != 400 {
		t.Fatalf("bad client id: %d", w.Code)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/connections/twitch/client-id", token: testToken, body: `{"client_id":"abcdefghij0123456789"}`}); w.Code != 200 {
		t.Fatalf("client id: %d %s", w.Code, w.Body)
	}
	w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-in", token: testToken})
	var p struct {
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.UserCode == "" || strings.Contains(w.Body.String(), "device_code") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if err := testMock.Approve(p.UserCode, "apistreamer"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for connections(t, s).Twitch.Streamer.State != "connected" {
		if time.Now().After(deadline) {
			t.Fatalf("never connected: %+v", connections(t, s).Twitch.Streamer)
		}
		time.Sleep(50 * time.Millisecond)
	}
	body := do(t, s, req{method: "GET", path: "/v1/connections", token: testToken}).Body.String()
	if strings.Contains(body, "access_token") || strings.Contains(body, "refresh_token") {
		t.Fatal("tokens in the answer")
	}
	for i := 0; i < 2; i++ {
		if w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-out", token: testToken}); w.Code != 204 {
			t.Fatalf("sign-out: %d", w.Code)
		}
	}
	if c := connections(t, s); c.Twitch.Streamer.State != "signed_out" {
		t.Fatal(c.Twitch.Streamer)
	}
}

func TestTwitchRoutesCheckAccountAndToken(t *testing.T) {
	s := newTestServer(t)
	if w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/admin/sign-in", token: testToken}); w.Code != 404 {
		t.Fatalf("unknown account: %d", w.Code)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/connections/twitch/bot/sign-in", token: testToken}); w.Code != 204 {
		t.Fatalf("cancel with nothing pending: %d", w.Code)
	}
	for _, rq := range []req{{method: "GET", path: "/v1/connections"}, {method: "PUT", path: "/v1/connections/twitch/client-id", body: "{}"},
		{method: "POST", path: "/v1/connections/twitch/streamer/sign-in"}, {method: "POST", path: "/v1/connections/twitch/bot/sign-out"}} {
		if w := do(t, s, rq); w.Code != 401 {
			t.Errorf("%s %s: %d", rq.method, rq.path, w.Code)
		}
	}
}

func TestChatSendThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	send := func(body string) (int, string) {
		w := do(t, s, req{method: "POST", path: "/v1/chat/messages", token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	if code, body := send(`{"text":"hi"}`); code != 409 || !strings.Contains(body, "Sign in") {
		t.Fatalf("signed out: %d %s", code, body)
	}
	do(t, s, req{method: "PUT", path: "/v1/connections/twitch/client-id", token: testToken, body: `{"client_id":"abcdefghij0123456789"}`})
	w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-in", token: testToken})
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	_ = testMock.Approve(p.UserCode, "chatstreamer")
	deadline := time.Now().Add(5 * time.Second)
	for connections(t, s).Twitch.Chat.State != "connected" {
		if time.Now().After(deadline) {
			t.Fatalf("chat never connected: %+v", connections(t, s).Twitch.Chat)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code, body := send(`{"text":"hello from the api"}`); code != 200 || !strings.Contains(body, `"sent_as":"streamer"`) {
		t.Fatalf("send: %d %s", code, body)
	}
	if log := testMock.ChatLog(); len(log) != 1 || log[0].Text != "hello from the api" {
		t.Fatalf("%+v", log)
	}
	if code, _ := send(`{"text":""}`); code != 400 {
		t.Fatalf("empty: %d", code)
	}
	testMock.DropNext("automod_held")
	if code, body := send(`{"text":"held"}`); code != 422 || !strings.Contains(body, "not_sent") {
		t.Fatalf("dropped: %d %s", code, body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/chat/messages", body: `{"text":"x"}`}); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}
