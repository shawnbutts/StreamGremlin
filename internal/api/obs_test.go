package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOBSThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	if w := do(t, s, req{method: "GET", path: "/v1/obs/scenes", token: testToken}); w.Code != 409 || errCode(t, w) != CodeNotConnected {
		t.Fatalf("not connected: %d %s", w.Code, w.Body)
	}
	testOBS.SetPassword("pw")
	w := do(t, s, req{method: "PUT", path: "/v1/connections/obs", token: testToken,
		body: `{"url":"` + testOBSURL + `","enabled":true,"password":"pw"}`})
	if w.Code != 200 || strings.Contains(w.Body.String(), `"pw"`) || !strings.Contains(w.Body.String(), `"has_password":true`) {
		t.Fatalf("configure: %d %s", w.Code, w.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for connections(t, s).OBS.State != "connected" {
		if time.Now().After(deadline) {
			t.Fatalf("never connected: %+v", connections(t, s).OBS)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var sc OBSScenes
	w = do(t, s, req{method: "GET", path: "/v1/obs/scenes", token: testToken})
	if json.Unmarshal(w.Body.Bytes(), &sc) != nil || sc.Current != "Starting Soon" || len(sc.Scenes) != 4 {
		t.Fatalf("%s", w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/obs/scene-items?scene=Just+Chatting", token: testToken}); w.Code != 200 || !strings.Contains(w.Body.String(), "Chat Box") {
		t.Fatalf("items: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/obs/scene", token: testToken, body: `{"scene":"Nope"}`}); w.Code != 404 {
		t.Fatalf("missing scene: %d", w.Code)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/obs/scene-items/visibility", token: testToken,
		body: `{"scene":"Gaming","source":"Webcam","visible":false}`}); w.Code != 200 || testOBS.Visible("Gaming", "Webcam") {
		t.Fatalf("visibility: %d %s", w.Code, w.Body)
	}

	// Take a snapshot, then the streamer changes the scene by hand: restoring leaves it.
	if w := do(t, s, req{method: "POST", path: "/v1/obs/snapshots", token: testToken, body: `{"key":"brb","scene":"Be Right Back"}`}); w.Code != 200 {
		t.Fatalf("take: %d %s", w.Code, w.Body)
	}
	time.Sleep(100 * time.Millisecond)
	testOBS.SwitchByHand("Gaming")
	// OBS's event for the change by hand arrives when it arrives.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		w = do(t, s, req{method: "GET", path: "/v1/obs/snapshots", token: testToken})
		if strings.Contains(w.Body.String(), `"would_restore":false`) && strings.Contains(w.Body.String(), "manual_change") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("open: %s", w.Body)
		}
	}
	w = do(t, s, req{method: "POST", path: "/v1/obs/snapshots/brb/restore", token: testToken})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"skipped"`) || testOBS.Current() != "Gaming" {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/obs/snapshots/brb/release", token: testToken}); w.Code != 404 {
		t.Fatalf("release a settled one: %d", w.Code)
	}
	for _, rq := range []req{{method: "GET", path: "/v1/obs/scenes"}, {method: "POST", path: "/v1/obs/scene", body: "{}"},
		{method: "PUT", path: "/v1/connections/obs", body: "{}"}, {method: "POST", path: "/v1/obs/snapshots/x/restore"}} {
		if w := do(t, s, rq); w.Code != 401 {
			t.Errorf("%s %s: %d", rq.method, rq.path, w.Code)
		}
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/connections/obs", token: testToken, body: `{"url":"http://x","enabled":true}`}); w.Code != 400 {
		t.Fatalf("bad url: %d", w.Code)
	}
}
