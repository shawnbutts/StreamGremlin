package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

const ytClientID = "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"

func TestYouTubeSignInThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	get := func() youtube.Status {
		t.Helper()
		w := do(t, s, req{method: "GET", path: "/v1/connections/youtube", token: testToken})
		var st youtube.Status
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &st) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return st
	}
	if st := get(); st.ClientSource != youtube.ClientNone || st.Streamer.State != youtube.StateNotConfigured || st.TestServer == "" ||
		st.Manifest.Provider != "youtube" || st.Quota.Limit != youtube.DailyQuota {
		t.Fatalf("%+v", st)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/connections/youtube/streamer/sign-in", token: testToken}); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/connections/youtube/client", token: testToken, body: `{"client_id":"nope"}`}); w.Code != 400 ||
		errCode(t, w) != CodeInvalidRequest {
		t.Fatal(w.Code, w.Body)
	}
	w := do(t, s, req{method: "PUT", path: "/v1/connections/youtube/client", token: testToken,
		body: `{"client_id":"` + ytClientID + `","client_secret":"GOCSPX-abcdefghijk"}`})
	if w.Code != 200 || strings.Contains(w.Body.String(), "GOCSPX") || !strings.Contains(w.Body.String(), `"has_secret":true`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/connections/youtube/nobody/sign-in", token: testToken}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = do(t, s, req{method: "POST", path: "/v1/connections/youtube/streamer/sign-in", token: testToken})
	var p youtube.PendingSignIn
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.AuthURL == "" {
		t.Fatal(w.Code, w.Body)
	}
	u, _ := url.Parse(p.AuthURL)
	q := u.Query()
	q.Set("mock_channel", "UCstreamer")
	u.RawQuery = q.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if st := get(); st.Streamer.State != youtube.StateConnected || st.Streamer.Channel == nil || st.Streamer.Channel.Title != "Streamer" ||
		st.Quota.Used < 1 {
		t.Fatalf("%+v", st)
	}
	// Chat: Find my stream reads the live broadcast's chat.
	b := testYouTube.GoLive("UCstreamer", "API stream")
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := do(t, s, req{method: "POST", path: "/v1/connections/youtube/chat/find", token: testToken})
		var st youtube.Status
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &st) != nil {
			t.Fatal(w.Code, w.Body)
		}
		if st.Chat.State == youtube.ChatConnected && st.Chat.Broadcast != nil && st.Chat.Broadcast.ID == b.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", st.Chat)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/connections/youtube/chat/find"}); w.Code != 401 {
		t.Fatal("find without a token:", w.Code)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/connections/youtube/bot/sign-in", token: testToken}); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/connections/youtube/streamer/sign-out", token: testToken}); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if st := get(); st.Streamer.State != youtube.StateSignedOut {
		t.Fatalf("%+v", st)
	}
	// The client's secret can't be read through the saved-secrets routes either.
	if w := do(t, s, req{method: "GET", path: "/v1/secrets", token: testToken}); strings.Contains(w.Body.String(), "GOCSPX") {
		t.Fatal(w.Body)
	}
}
