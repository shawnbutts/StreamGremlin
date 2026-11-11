package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

const helloWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.hello","name":"Say hello",
	"triggers":[{"type":"chat.command.received","where":{"command":"hello"}}],
	"steps":[{"action":"chat.send","with":{"message":"Hi ${event.actor.display_name}!"}},{"action":"note","with":{"text":"done"}}]}`

func TestWorkflowsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello", token: testToken,
		body: strings.Replace(helloWorkflow, `"chat.send"`, `"chat.shout"`, 1)})
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"problems"`) || !strings.Contains(w.Body.String(), "chat.shout") {
		t.Fatalf("invalid: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/workflows/other.id", token: testToken, body: helloWorkflow}); w.Code != 400 {
		t.Fatalf("mismatched id: %d", w.Code)
	}
	w = do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello", token: testToken, body: helloWorkflow})
	// New workflows start on (D-077).
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"changed":true`) || !strings.Contains(w.Body.String(), `"enabled":true`) ||
		strings.Contains(w.Body.String(), "off_because") {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	// Another answer to !hello starts off, saying why.
	if w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello2", token: testToken,
		body: strings.Replace(helloWorkflow, `"chat.hello"`, `"chat.hello2"`, 1)}); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) ||
		!strings.Contains(w.Body.String(), `"off_because":"!hello is already answered by`) {
		t.Fatalf("taken: %d %s", w.Code, w.Body)
	}
	do(t, s, req{method: "DELETE", path: "/v1/workflows/chat.hello2", token: testToken})
	if w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello", token: testToken, body: helloWorkflow}); !strings.Contains(w.Body.String(), `"changed":false`) {
		t.Fatalf("resave: %s", w.Body)
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello/enabled", token: testToken, body: `{"enabled":true}`}); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	// A test event: the workflow runs, but nothing reaches Twitch.
	simulate(t, s, `{"type":"chat.command.received","request_id":"wf1","fields":{"command":"!hello","user":"Gremlin"}}`)
	var jobs struct{ Jobs []workflow.Job }
	deadline := time.Now().Add(5 * time.Second)
	for {
		w = do(t, s, req{method: "GET", path: "/v1/jobs?workflow=chat.hello", token: testToken})
		_ = json.Unmarshal(w.Body.Bytes(), &jobs)
		if len(jobs.Jobs) == 1 && jobs.Jobs[0].Status == workflow.JobSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs: %s", w.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !jobs.Jobs[0].DryRun || len(testMock.ChatLog()) != 0 {
		t.Fatal("a test event reached Twitch")
	}
	var detail JobDetail
	w = do(t, s, req{method: "GET", path: "/v1/jobs/" + jobs.Jobs[0].ID, token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Steps) != 2 || detail.Steps[0].Status != "skipped" || detail.Steps[0].Input.(map[string]any)["message"] != "Hi Gremlin!" {
		t.Fatalf("trace: %s", w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/jobs/" + jobs.Jobs[0].ID + "/cancel", token: testToken}); w.Code != 409 {
		t.Fatalf("cancel a finished run: %d", w.Code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/actions", token: testToken}); !strings.Contains(w.Body.String(), `"obs.scene.restore"`) {
		t.Fatalf("actions: %s", w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/workflows/chat.hello", token: testToken}); !strings.Contains(w.Body.String(), `"definition"`) {
		t.Fatalf("detail: %s", w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/workflows/nope", token: testToken}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, rq := range []req{{method: "GET", path: "/v1/workflows"}, {method: "PUT", path: "/v1/workflows/a.b", body: "{}"}, {method: "GET", path: "/v1/jobs"}} {
		if w := do(t, s, rq); w.Code != 401 {
			t.Errorf("%s %s: %d", rq.method, rq.path, w.Code)
		}
	}
}

func TestDeleteRestoreAndPaletteThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	if w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello", token: testToken, body: helloWorkflow}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/workflows/chat.hello", token: testToken}); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "DELETE", path: "/v1/workflows/chat.hello", token: testToken}); w.Code != 404 {
		t.Fatalf("delete again: %d", w.Code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/workflows", token: testToken}); strings.Contains(w.Body.String(), "chat.hello") {
		t.Fatalf("still listed: %s", w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/workflows?deleted=true", token: testToken}); !strings.Contains(w.Body.String(), `"deleted_at"`) {
		t.Fatalf("deleted list: %s", w.Body)
	}
	w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello", token: testToken, body: helloWorkflow})
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"deleted"`) {
		t.Fatalf("save over deleted: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/workflows/chat.hello/restore", token: testToken}); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "POST", path: "/v1/workflows/chat.hello/restore", token: testToken}); w.Code != 404 {
		t.Fatalf("restore again: %d", w.Code)
	}
	w = do(t, s, req{method: "GET", path: "/v1/palette", token: testToken})
	var p workflow.Palette
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || len(p.Triggers) == 0 || !strings.Contains(w.Body.String(), `"workflow.run"`) || !strings.Contains(w.Body.String(), `"editor":"message"`) {
		t.Fatalf("palette: %d %.300s", w.Code, w.Body)
	}
}

func TestCommandTakenThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	for _, id := range []string{"chat.hello", "chat.hello2"} {
		body := strings.Replace(helloWorkflow, `"id":"chat.hello"`, `"id":"`+id+`"`, 1)
		if w := do(t, s, req{method: "PUT", path: "/v1/workflows/" + id, token: testToken, body: body}); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello/enabled", token: testToken, body: `{"enabled":true}`}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w := do(t, s, req{method: "PUT", path: "/v1/workflows/chat.hello2/enabled", token: testToken, body: `{"enabled":true}`})
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"command_taken"`) ||
		!strings.Contains(w.Body.String(), `"conflicts":[{"command":"hello","owner":"chat.hello","name":"Say hello"}]`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = do(t, s, req{method: "GET", path: "/v1/commands", token: testToken})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"command":"hello"`) || !strings.Contains(w.Body.String(), `"conflict":false`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestOverviewThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/overview", token: testToken})
	var o struct {
		Checklist []struct {
			ID   string
			Done bool
		}
		SetUp     bool `json:"set_up"`
		Attention []any
	}
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil || w.Code != 200 || len(o.Checklist) != 6 || o.SetUp || o.Checklist[0].ID != "twitch" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/overview"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestTemplatesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/templates", token: testToken})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"brb"`) || !strings.Contains(w.Body.String(), `"blanks":[{"path":"$.steps[0].with.scene"`) {
		t.Fatalf("%d %.400s", w.Code, w.Body)
	}
}

func TestWorkflowGroupsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	for _, id := range []string{"chat.hello", "chat.hello2"} {
		body := strings.Replace(helloWorkflow, `"id":"chat.hello"`, `"id":"`+id+`","group":"Commands"`, 1)
		if code, out := call("PUT", "/v1/workflows/"+id, body); code != 200 || !strings.Contains(out, `"group":"Commands"`) {
			t.Fatal(code, out)
		}
	}
	call("PUT", "/v1/workflows/chat.hello/enabled", `{"enabled":true}`)
	if code, out := call("GET", "/v1/workflow-groups", ""); code != 200 ||
		!strings.Contains(out, `{"name":"Commands","enabled":true,"workflows":2,"on":1}`) {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/workflow-groups/commands/enabled", `{"enabled":false}`); code != 200 || !strings.Contains(out, `"enabled":false`) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/workflows", ""); !strings.Contains(out, `"enabled":true,"group":"Commands","group_enabled":false,"running":false`) {
		t.Fatal(out)
	}
	// While the group is off, chat.hello2 can be switched on; the group
	// then can't come on: both would answer !hello.
	call("PUT", "/v1/workflows/chat.hello2/enabled", `{"enabled":true}`)
	if code, out := call("PUT", "/v1/workflow-groups/Commands/enabled", `{"enabled":true}`); code != 409 || !strings.Contains(out, "command_taken") {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/workflow-groups/Commands", `{"name":"Chat"}`); code != 200 || !strings.Contains(out, `"name":"Chat"`) {
		t.Fatal(code, out)
	}
	if code, _ := call("PUT", "/v1/workflow-groups/Nope/enabled", `{"enabled":true}`); code != 404 {
		t.Fatal(code)
	}
	if code, _ := call("PUT", "/v1/workflow-groups/Chat", `{"name":"bad/name"}`); code != 400 {
		t.Fatal(code)
	}
}

// Failed runs can be marked as seen: Home's notice then counts only
// failures after that, and says so. The runs are kept.
func TestMarkingFailuresSeen(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	broken := `{"schema":"streamgremlin.workflow/v1","id":"chat.broken","name":"Broken",
		"triggers":[{"type":"chat.command.received","where":{"command":"oops"}}],"steps":[{"set":{"x":"1 / 0"}}]}`
	if code, out := call("PUT", "/v1/workflows/chat.broken", broken); code != 200 {
		t.Fatal(code, out)
	}
	call("PUT", "/v1/workflows/chat.broken/enabled", `{"enabled":true}`)
	fail := func(id string) {
		t.Helper()
		call("POST", "/v1/events/simulate", `{"type":"chat.command.received","request_id":"`+id+`","fields":{"command":"!oops"}}`)
	}
	failed := func(want int) string {
		t.Helper()
		var out string
		for range 100 {
			_, out = call("GET", "/v1/overview", "")
			if strings.Contains(out, fmt.Sprintf(`"failed_24h":%d`, want)) {
				return out
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("want %d failed: %s", want, out)
		return ""
	}
	fail("o1")
	if out := failed(1); !strings.Contains(out, `"action":"mark_failures_seen"`) || !strings.Contains(out, "in the last 24 hours") {
		t.Fatal(out)
	}
	if code, out := call("POST", "/v1/overview/failures-seen", ""); code != 200 || !strings.Contains(out, "seen_at") {
		t.Fatal(code, out)
	}
	if out := failed(0); strings.Contains(out, "failed in") {
		t.Fatal("the notice stayed:", out)
	}
	if _, out := call("GET", "/v1/jobs?workflow=chat.broken", ""); !strings.Contains(out, `"status":"failed"`) {
		t.Fatal("the failed run wasn't kept:", out)
	}
	time.Sleep(10 * time.Millisecond)
	fail("o2")
	if out := failed(1); !strings.Contains(out, "since you marked them as seen") {
		t.Fatal(out)
	}
}
