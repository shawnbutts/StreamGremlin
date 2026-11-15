package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// The raid guard through the API: its settings read, refused when wrong,
// saved; a test alert shown and dismissed; and no token.
func TestRaidGuardThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	var v RaidGuardView
	w := do(t, s, req{method: "GET", path: "/v1/raid-guard", token: testToken})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Settings.Mode != "alert" || v.Alert != nil {
		t.Fatal(w.Code, w.Body)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/raid-guard", token: testToken, body: `{"mode":"panic","first_time":8,"new_accounts":5,"days":7,"window_seconds":30}`}); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	w = do(t, s, req{method: "PUT", path: "/v1/raid-guard", token: testToken, body: `{"mode":"lock","first_time":3,"new_accounts":3,"days":14,"window_seconds":60,"after_raid_minutes":5}`})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"lock"`) || !strings.Contains(w.Body.String(), `"days":14`) {
		t.Fatal(w.Code, w.Body)
	}
	w = do(t, s, req{method: "POST", path: "/v1/raid-guard/test", token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.Alert == nil || !v.Alert.Test || v.Alert.Locked || len(v.Alert.Accounts) != 3 {
		t.Fatal(w.Body.String())
	}
	w = do(t, s, req{method: "POST", path: "/v1/raid-guard/dismiss", token: testToken})
	if !strings.Contains(w.Body.String(), `"alert":null`) {
		t.Fatal(w.Body.String())
	}
	if w := do(t, s, req{method: "GET", path: "/v1/raid-guard"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
