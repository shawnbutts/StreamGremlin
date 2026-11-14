package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ban lists through the API: a file imported, checked (with you and a
// moderator left alone), confirmed and banned on Twitch; saved as a file;
// copied from Twitch; bad input; and no token.
func TestBanListsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	do(t, s, req{method: "PUT", path: "/v1/connections/twitch/client-id", token: testToken, body: `{"client_id":"abcdefghij0123456789"}`})
	w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-in", token: testToken})
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	_ = testMock.Approve(p.UserCode, "blstreamer")
	for deadline := time.Now().Add(5 * time.Second); ; {
		w := do(t, s, req{method: "GET", path: "/v1/connections", token: testToken})
		if strings.Contains(w.Body.String(), `"login":"blstreamer"`) && strings.Contains(w.Body.String(), `"state":"connected"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(w.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var names []string
	for i := range 12 {
		n := fmt.Sprintf("blbot%02d", i)
		testMock.UserID(n)
		names = append(names, n)
	}
	testMock.SetModerator("blstreamer", "blbot00", true)
	file := filepath.Join(t.TempDir(), "bots.txt")
	if err := os.WriteFile(file, []byte("# bots\n"+strings.Join(names, "\n")+"\nblstreamer\nnobody_at_all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	get := func(id string) BanListView {
		t.Helper()
		w := do(t, s, req{method: "GET", path: "/v1/ban-lists", token: testToken})
		var out struct {
			Lists []BanListView `json:"lists"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Code, w.Body)
		}
		for _, l := range out.Lists {
			if l.ID == id {
				return l
			}
		}
		t.Fatalf("no list %s: %s", id, w.Body)
		return BanListView{}
	}
	wait := func(id, phase string) BanListView {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			l := get(id)
			if l.Phase == phase && !l.Paused {
				return l
			}
			if time.Now().After(deadline) {
				t.Fatalf("%+v", l)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for _, bad := range []string{`{"name":"","path":"` + file + `"}`, `{"name":"x","path":"bots.txt"}`, `{"name":"x","path":"/no/such/file"}`} {
		if w := do(t, s, req{method: "POST", path: "/v1/ban-lists", token: testToken, body: bad}); w.Code != 400 {
			t.Fatal(bad, w.Code, w.Body)
		}
	}
	w = do(t, s, req{method: "POST", path: "/v1/ban-lists", token: testToken, body: `{"name":"Bots","path":` + fmt.Sprintf("%q", file) + `}`})
	var l BanListView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &l) != nil {
		t.Fatal(w.Code, w.Body)
	}
	l = wait(l.ID, "ready")
	if l.Counts["waiting"] != 11 || l.Counts["kept"] != 2 || l.Counts["not_found"] != 1 || testMock.IsBanned("blstreamer", "blbot01") {
		t.Fatalf("%+v", l)
	}
	w = do(t, s, req{method: "GET", path: "/v1/ban-lists/" + l.ID + "/entries?state=kept", token: testToken})
	if !strings.Contains(w.Body.String(), `"detail":"a moderator"`) || !strings.Contains(w.Body.String(), `"detail":"you"`) {
		t.Fatal(w.Body.String())
	}
	if w := do(t, s, req{method: "POST", path: "/v1/ban-lists/" + l.ID + "/confirm", token: testToken}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	l = wait(l.ID, "done")
	if l.Counts["banned"] != 11 || !testMock.IsBanned("blstreamer", "blbot01") || testMock.IsBanned("blstreamer", "blbot00") {
		t.Fatalf("%+v", l)
	}
	w = do(t, s, req{method: "GET", path: "/v1/ban-lists/" + l.ID + "/file?state=banned", token: testToken})
	if w.Code != 200 || strings.Count(w.Body.String(), "\n") != 12 || !strings.Contains(w.Body.String(), "blbot11\n") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do(t, s, req{method: "PATCH", path: "/v1/ban-lists/" + l.ID, token: testToken, body: `{"pause_live":false}`}); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"pause_live":false`) {
		t.Fatal(w.Code, w.Body)
	}
	// Twitch's own list, copied.
	w = do(t, s, req{method: "POST", path: "/v1/ban-lists", token: testToken, body: `{"name":"Twitch","copy":true}`})
	var cp BanListView
	_ = json.Unmarshal(w.Body.Bytes(), &cp)
	if cp = wait(cp.ID, "done"); cp.Counts["already"] != 11 {
		t.Fatalf("%+v", cp)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/ban-lists/" + l.ID + "/undo", token: testToken}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	wait(l.ID, "done")
	if testMock.IsBanned("blstreamer", "blbot01") {
		t.Fatal("not undone")
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/ban-lists/" + cp.ID, token: testToken}); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/ban-lists/" + cp.ID + "/pause", token: testToken}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/ban-lists"}); w.Code != 401 {
		t.Fatal("no token:", w.Code)
	}
}
