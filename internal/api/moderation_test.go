package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Moderating through the API: refused when signed out, and recorded so;
// signed in, a timeout done once however often it's sent; Twitch's
// refusal recorded with its words; a reused request id refused; the
// history per viewer and on their card; the chat modes read and changed;
// a suspicious-user flag on the card; bad input and no token.
func TestModerationThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	post := func(body string) (int, ModerationResult, string) {
		w := do(t, s, req{method: "POST", path: "/v1/moderation", token: testToken, body: body})
		var res ModerationResult
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return w.Code, res, w.Body.String()
	}
	fan := testMock.UserID("modfan")
	timeout := `{"request_id":"t1","action":"timeout","platform":"twitch","user_id":"` + fan + `","login":"modfan","seconds":600,"from":"live"}`

	if code, res, out := post(`{"request_id":"s0","action":"ban","platform":"twitch","user_id":"` + fan + `","from":"live"}`); code != 200 ||
		res.Record.State != "refused" || !strings.Contains(res.Record.Answer, "Sign in") {
		t.Fatalf("signed out: %d %s", code, out)
	}

	do(t, s, req{method: "PUT", path: "/v1/connections/twitch/client-id", token: testToken, body: `{"client_id":"abcdefghij0123456789"}`})
	w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-in", token: testToken})
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	_ = testMock.Approve(p.UserCode, "modstreamer")
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := do(t, s, req{method: "GET", path: "/v1/chat/settings", token: testToken})
		var v ChatModesView
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		if v.Settings != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat modes: %s", w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	code, res, out := post(timeout)
	if code != 200 || res.Duplicate || res.Record.State != "done" || !testMock.IsBanned("modstreamer", "modfan") {
		t.Fatalf("timeout: %d %s", code, out)
	}
	calls := len(testMock.ModCalls())
	if code, res, _ := post(timeout); code != 200 || !res.Duplicate || res.Record.ID == "" || len(testMock.ModCalls()) != calls {
		t.Fatal("repeat sent again:", code, res)
	}
	if code, _, _ := post(strings.Replace(timeout, "600", "60", 1)); code != 409 {
		t.Fatal("reused id:", code)
	}
	if _, res, _ := post(`{"request_id":"u1","action":"unvip","platform":"twitch","user_id":"` + fan + `","from":"live"}`); res.Record.State != "refused" ||
		!strings.Contains(res.Record.Answer, "not a VIP") {
		t.Fatalf("refused: %+v", res.Record)
	}
	for _, bad := range []string{
		`{"request_id":"b1","action":"timeout","platform":"twitch","user_id":"` + fan + `","from":"live"}`,
		`{"request_id":"b2","action":"kick","platform":"twitch","user_id":"` + fan + `","from":"live"}`,
		`{"request_id":"b3","action":"ban","platform":"twitch","user_id":"` + fan + `","from":"stream_deck"}`,
	} {
		if code, _, _ := post(bad); code != 400 {
			t.Fatal(bad, code)
		}
	}

	// Chat modes.
	code, res, out = post(`{"request_id":"c1","action":"chat_modes","platform":"twitch","chat_modes":{"slow_mode":true,"slow_mode_wait_time":10},"from":"live"}`)
	if code != 200 || res.Record.State != "done" || res.Record.Settings == nil || !res.Record.Settings.Slow {
		t.Fatalf("chat modes: %d %s", code, out)
	}
	w = do(t, s, req{method: "GET", path: "/v1/chat/settings", token: testToken})
	if !strings.Contains(w.Body.String(), `"slow_mode":true`) {
		t.Fatal(w.Body.String())
	}

	// History, and on the card with a suspicious flag.
	w = do(t, s, req{method: "GET", path: "/v1/moderation?platform=twitch&user_id=" + fan, token: testToken})
	var hist struct {
		Moderation []struct{ Action, State string } `json:"moderation"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &hist)
	if len(hist.Moderation) != 3 || hist.Moderation[0].Action != "unvip" {
		t.Fatalf("%s", w.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for testMock.SubscribedTo("modstreamer", "channel.suspicious_user.update") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("not listening for suspicious users")
		}
		time.Sleep(20 * time.Millisecond)
	}
	testMock.SuspiciousSay("modstreamer", "modfan", "back again", "active_monitoring", "likely", "ban_evader")
	deadline = time.Now().Add(5 * time.Second)
	for {
		w = do(t, s, req{method: "GET", path: "/v1/chatters/twitch/" + fan, token: testToken})
		var c ChatterCard
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		if c.Suspicious != nil {
			if c.Suspicious.BanEvasion != "likely" || c.Suspicious.Treatment != "active_monitoring" || len(c.Moderation) != 3 {
				t.Fatalf("%s", w.Body.String())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no flag: %s", w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	testMock.SuspiciousUpdate("modstreamer", "modfan", "modstreamer", "restricted")
	deadline = time.Now().Add(5 * time.Second)
	for {
		w = do(t, s, req{method: "GET", path: "/v1/chatters/twitch/" + fan, token: testToken})
		var c ChatterCard
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		if c.Suspicious != nil && c.Suspicious.Treatment == "restricted" {
			if c.Suspicious.BanEvasion != "likely" {
				t.Fatalf("evasion lost: %+v", c.Suspicious)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no update: %s", w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if w := do(t, s, req{method: "POST", path: "/v1/moderation", body: timeout}); w.Code != 401 {
		t.Fatal("no token:", w.Code)
	}
}
