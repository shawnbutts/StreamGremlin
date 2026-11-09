package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/giveaways"
)

func TestGiveawaysThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	view := func() GiveawaysView {
		_, out := call("GET", "/v1/giveaways", "")
		var v GiveawaysView
		_ = json.Unmarshal([]byte(out), &v)
		return v
	}
	if v := view(); v.Current != nil || len(v.Recent) != 0 {
		t.Fatalf("%+v", v)
	}
	if code, _ := call("POST", "/v1/giveaways", `{"title":""}`); code != 400 {
		t.Fatal("no title:", code)
	}
	code, out := call("POST", "/v1/giveaways", `{"title":"a Steam key","winners":2,"practice":true}`)
	if code != 201 {
		t.Fatal(code, out)
	}
	var g giveaways.Giveaway
	_ = json.Unmarshal([]byte(out), &g)
	if code, _ := call("POST", "/v1/giveaways", `{"title":"another"}`); code != 409 {
		t.Fatal("a second:", code)
	}
	for i, who := range []string{"Ann", "Bob", "Cat"} {
		body := fmt.Sprintf(`{"type":"chat.command.received","request_id":"g%d","fields":{"user":%q,"command":"!join"}}`, i, who)
		if code, out := call("POST", "/v1/events/simulate", body); code != 201 && code != 200 {
			t.Fatal(code, out)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for v := view(); v.Current == nil || len(v.Current.Entries) != 3; v = view() {
		if time.Now().After(deadline) {
			t.Fatalf("%+v", v.Current)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, out := call("POST", "/v1/giveaways/"+g.ID+"/draw", `{"count":1}`); code != 200 || !strings.Contains(out, `"candidates":3`) {
		t.Fatal(code, out)
	}
	var drawn struct {
		Draws []giveaways.Draw `json:"draws"`
	}
	_, out = call("POST", "/v1/giveaways/"+g.ID+"/draw", "")
	_ = json.Unmarshal([]byte(out), &drawn)
	if len(drawn.Draws) != 1 || drawn.Draws[0].Candidates != 2 {
		t.Fatal(out)
	}
	if code, _ := call("POST", "/v1/giveaways/"+g.ID+"/draw", ""); code != 409 {
		t.Fatal("a third winner:", code)
	}
	if code, out := call("POST", "/v1/giveaways/"+g.ID+"/set-aside", fmt.Sprintf(`{"draw_id":%q}`, drawn.Draws[0].ID)); code != 200 ||
		!strings.Contains(out, `"candidates":1`) {
		t.Fatal(code, out)
	}
	_, out = call("GET", "/v1/giveaways/"+g.ID, "")
	var d giveaways.Detail
	_ = json.Unmarshal([]byte(out), &d)
	if d.Fingerprint == "" || len(giveaways.Verify(d)) != 0 || len(d.Draws) != 3 || d.Messages[0].State != "practice" {
		t.Fatalf("%+v %v", d.Giveaway, giveaways.Verify(d))
	}
	if code, _ := call("POST", "/v1/giveaways/"+g.ID+"/end", ""); code != 200 {
		t.Fatal("end:", code)
	}
	if code, _ := call("POST", "/v1/giveaways/"+g.ID+"/cancel", ""); code != 409 {
		t.Fatal("cancel after end:", code)
	}
	if code, _ := call("GET", "/v1/giveaways/nope", ""); code != 404 {
		t.Fatal("missing:", code)
	}

	// A live giveaway holds its command: a workflow answering it can't be
	// turned on meanwhile, and a giveaway can't take one a workflow holds.
	code, out = call("POST", "/v1/giveaways", `{"title":"live one","command":"enter"}`)
	if code != 201 {
		t.Fatal(code, out)
	}
	_ = json.Unmarshal([]byte(out), &g)
	wf := `{"schema":"streamgremlin.workflow/v1","id":"chat.enter","name":"Enter",
		"triggers":[{"type":"chat.command.received","where":{"command":"enter"}}],"steps":[{"action":"note","with":{"text":"hi"}}]}`
	if code, out := call("PUT", "/v1/workflows/chat.enter", wf); code >= 300 {
		t.Fatal(code, out)
	}
	on := `{"enabled":true}`
	if code, out := call("PUT", "/v1/workflows/chat.enter/enabled", on); code != 409 || !strings.Contains(out, "the giveaway “live one”") {
		t.Fatalf("a workflow on a giveaway's command: %d %s", code, out)
	}
	if _, out := call("GET", "/v1/commands", ""); !strings.Contains(out, `"giveaway:`+g.ID) {
		t.Fatal(out)
	}
	call("POST", "/v1/giveaways/"+g.ID+"/cancel", "")
	if code, out := call("PUT", "/v1/workflows/chat.enter/enabled", on); code >= 300 {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/giveaways", `{"title":"x","command":"enter"}`); code != 409 || !strings.Contains(out, "command_taken") {
		t.Fatal(code, out)
	}
}
