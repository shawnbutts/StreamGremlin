package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackupsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "POST", path: "/v1/backups", token: testToken})
	var info struct{ Name, Kind string }
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || w.Code != 201 || info.Kind != "manual" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/backups/" + info.Name + "/verify", token: testToken}); w.Code != 200 {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/backups/" + info.Name + "/restore", token: testToken}); w.Code != 202 || !strings.Contains(w.Body.String(), `"restart_required":true`) {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	w = do(t, s, req{method: "GET", path: "/v1/backups", token: testToken})
	if !strings.Contains(w.Body.String(), `"pending":"`+info.Name+`"`) || !strings.Contains(w.Body.String(), `"backup_every_hours":24`) {
		t.Fatalf("list: %s", w.Body)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/backups/" + info.Name, token: testToken}); w.Code != 409 {
		t.Fatalf("deleting the pending one: %d", w.Code)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/backups/" + info.Name + "/restore", token: testToken}); w.Code != 204 {
		t.Fatalf("cancel: %d", w.Code)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/backups/" + info.Name, token: testToken}); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	for _, p := range []string{"/v1/backups/..%2Fstreamgremlin.db/verify", "/v1/backups/nope.zip/restore"} {
		if w := do(t, s, req{method: "POST", path: p, token: testToken}); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/settings/maintenance", token: testToken, body: `{"backup_every_hours":24,"backup_keep":0,"events_days":30,"runs_days":90}`}); w.Code != 400 {
		t.Fatalf("bad settings: %d", w.Code)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/settings/maintenance", token: testToken, body: `{"backup_every_hours":12,"backup_keep":3,"events_days":7,"runs_days":0}`}); w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/maintenance/sweep", token: testToken}); w.Code != 200 || !strings.Contains(w.Body.String(), `"events":0`) {
		t.Fatalf("sweep: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/backups"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
