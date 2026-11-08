package api

import (
	"encoding/json"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/variables"
)

func TestVariablesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	put := func(body string) (int, string) {
		w := do(t, s, req{method: "PUT", path: "/v1/variables", token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	if code, body := put(`{"scope":"global","owner":"","name":"deaths","value":3}`); code != 200 || !strings.Contains(body, `"value":3`) || !strings.Contains(body, `"updated_by":"you"`) {
		t.Fatalf("save: %d %s", code, body)
	}
	// Repeating it is harmless.
	if code, _ := put(`{"scope":"global","owner":"","name":"deaths","value":3}`); code != 200 {
		t.Fatal(code)
	}
	if code, _ := put(`{"scope":"workflow","owner":"chat.hello","name":"last","value":{"who":"fan"}}`); code != 200 {
		t.Fatal(code)
	}
	for _, bad := range []string{
		`{"scope":"global","owner":"","name":"Deaths","value":1}`,
		`{"scope":"global","owner":"x","name":"deaths","value":1}`,
		`{"scope":"viewer","owner":"123","name":"lurks","value":1}`,
		`{"scope":"global","owner":"","name":"deaths"}`,
		`{"scope":"global","owner":"","name":"deaths","value":null}`,
		`{"scope":"global","owner":"","name":"deaths","value":1,"extra":true}`,
		`{"scope":"global","owner":"","name":"big","value":"` + strings.Repeat("x", variables.MaxValueBytes) + `"}`,
	} {
		if code, body := put(bad); code != 400 {
			t.Errorf("%.80s: %d %s", bad, code, body)
		}
	}
	var list struct{ Variables []variables.Variable }
	w := do(t, s, req{method: "GET", path: "/v1/variables?scope=global", token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Variables) != 1 || list.Variables[0].Value != float64(3) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/variables?q=LAS", token: testToken}); !strings.Contains(w.Body.String(), `"name":"last"`) {
		t.Fatalf("search: %s", w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/variables?scope=everyone", token: testToken}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	del := "/v1/variables?scope=workflow&owner=chat.hello&name=last"
	if w := do(t, s, req{method: "DELETE", path: del, token: testToken}); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "DELETE", path: del, token: testToken}); w.Code != 404 {
		t.Fatalf("delete again: %d", w.Code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/variables"}); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}
