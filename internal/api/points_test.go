package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
)

// Viewers and points through the API: a viewer recorded from real chat,
// found by name; points given and taken by hand (409 below zero), a repeat
// doing nothing more; undo once; renamed; the top list; the audit; the
// export with history; forgetting keeps the ledger whole.
func TestViewersAndPointsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	ctx := context.Background()
	if _, err := testEvents.Accept(ctx, events.Envelope{Type: "chat.message.received", Source: "twitch", Payload: []byte(`{}`),
		DeliveryQuality: events.Confirmed, Actor: &events.Actor{Platform: "twitch", ID: "1", Login: "ann", DisplayName: "Ann"}}); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Viewers []ViewerView `json:"viewers"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(list.Viewers) == 0 && time.Now().Before(deadline) {
		_, out := call("GET", "/v1/viewers?q=AN", "")
		_ = json.Unmarshal([]byte(out), &list)
		time.Sleep(20 * time.Millisecond)
	}
	if len(list.Viewers) != 1 || list.Viewers[0].DisplayName != "Ann" || list.Viewers[0].Messages != 1 {
		t.Fatalf("%+v", list)
	}
	id := list.Viewers[0].ID
	if code, _ := call("POST", "/v1/viewers/"+id+"/points", `{"amount":50,"reason":"welcome"}`); code != 400 {
		t.Fatal("no request id:", code)
	}
	code, out := call("POST", "/v1/viewers/"+id+"/points", `{"amount":50,"reason":"welcome","request_id":"r1"}`)
	if code != 200 || !strings.Contains(out, `"balance":50`) {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/viewers/"+id+"/points", `{"amount":50,"reason":"welcome","request_id":"r1"}`); code != 200 ||
		!strings.Contains(out, `"applied":false`) {
		t.Fatal("a repeat:", code, out)
	}
	if code, out := call("POST", "/v1/viewers/"+id+"/points", `{"amount":-80,"request_id":"r2"}`); code != 409 ||
		!strings.Contains(out, "not enough points: has 50, needs 80") {
		t.Fatal(code, out)
	}
	_, out = call("POST", "/v1/viewers/"+id+"/points", `{"amount":-20,"reason":"oops","request_id":"r3"}`)
	var took points.Result
	_ = json.Unmarshal([]byte(out), &took)
	if code, out := call("POST", "/v1/points/transactions/"+took.Transaction.ID+"/undo", `{"request_id":"u1"}`); code != 200 ||
		!strings.Contains(out, `"balance":50`) {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/points/transactions/"+took.Transaction.ID+"/undo", `{"request_id":"u2"}`); code != 409 {
		t.Fatal("undone twice:", code)
	}
	if code, out := call("PUT", "/v1/points/currency", `{"name":"Gold"}`); code != 200 || !strings.Contains(out, `"name":"Gold"`) {
		t.Fatal(code, out)
	}
	_, out = call("GET", "/v1/points", "")
	var pv PointsView
	_ = json.Unmarshal([]byte(out), &pv)
	if pv.Currency.Name != "Gold" || pv.Summary.Issued != 50 || len(pv.Top) != 1 || pv.Top[0].DisplayName != "Ann" {
		t.Fatalf("%+v", pv)
	}
	if code, out := call("GET", "/v1/points/audit", ""); code != 200 || !strings.Contains(out, `"ok":true`) {
		t.Fatal(code, out)
	}
	_, out = call("GET", "/v1/viewers/"+id+"/export", "")
	var ex ViewerExport
	_ = json.Unmarshal([]byte(out), &ex)
	if ex.Schema != "streamgremlin.viewer-export/v1" || ex.Viewer.Login != "ann" || ex.Points.Balance != 50 || len(ex.Points.History) != 3 ||
		ex.Points.Currency != "Gold" {
		t.Fatalf("%+v", ex)
	}
	if code, out := call("POST", "/v1/viewers/"+id+"/forget", ""); code != 200 || !strings.Contains(out, `"forgotten_at"`) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/viewers?q=ann", ""); strings.Contains(out, id) {
		t.Fatal("a forgotten viewer is listed:", out)
	}
	if code, out := call("GET", "/v1/points/audit", ""); !strings.Contains(out, `"ok":true`) {
		t.Fatal("forgetting broke the ledger:", code, out)
	}
	if code, _ := call("GET", "/v1/viewers/v_nope", ""); code != 404 {
		t.Fatal(code)
	}
}

// Earning rules through the API: off by default, refused when they don't
// make sense, and once on, a real follow pays and a test follow doesn't.
func TestEarningThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	code, out := call("GET", "/v1/points/earning", "")
	var got EarningView
	if err := json.Unmarshal([]byte(out), &got); code != 200 || err != nil || got.Rules.On || got.Rules.Follow != 50 || got.Live != "unknown" {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/points/earning", `{"on":true,"follow":-1}`); code != 400 || !strings.Contains(out, "a follow earns 0 to") {
		t.Fatal(code, out)
	}
	got.Rules.On, got.Rules.Follow = true, 75
	body, _ := json.Marshal(got.Rules)
	if code, out := call("PUT", "/v1/points/earning", string(body)); code != 200 || !strings.Contains(out, `"follow":75`) {
		t.Fatal(code, out)
	}
	ctx := context.Background()
	for i, q := range []events.Quality{events.Synthetic, events.Confirmed} {
		if _, err := testEvents.Accept(ctx, events.Envelope{Type: "audience.follow.created", Source: "twitch", Payload: []byte(`{}`),
			DeliveryQuality: q, Actor: &events.Actor{Platform: "twitch", ID: strings.Repeat("9", i+1), Login: "fan", DisplayName: "Fan"}}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, out := call("GET", "/v1/points", ""); strings.Contains(out, `"issued":75`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, out := call("GET", "/v1/points", ""); !strings.Contains(out, `"issued":75`) || !strings.Contains(out, `"viewers":1`) {
		t.Fatal(out)
	}
}

// Giving to a group through the API: Twitch's list unreadable is 502 and
// gives nothing; recent chatters previewed and given to, once; a repeat
// answers the same give; the list of gives; undo; and exporting everyone,
// forgotten viewers unnamed.
func TestGivesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	if code, out := call("POST", "/v1/points/gives", `{"audience":"chat","amount":5,"request_id":"r0"}`); code != 502 ||
		!strings.Contains(out, "nothing was given") {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/points/gives/preview", `{"audience":"everyone"}`); code != 400 {
		t.Fatal(code)
	}
	ctx := context.Background()
	for _, who := range []events.Actor{{Platform: "twitch", ID: "1", Login: "ann", DisplayName: "Ann"}, {Platform: "twitch", ID: "2", Login: "bob", DisplayName: "Bob"}} {
		a := who
		if _, err := testEvents.Accept(ctx, events.Envelope{Type: "chat.message.received", Source: "twitch", Payload: []byte(`{}`),
			DeliveryQuality: events.Confirmed, Actor: &a}); err != nil {
			t.Fatal(err)
		}
	}
	var g struct {
		Viewers int      `json:"viewers"`
		Sample  []string `json:"sample"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for g.Viewers < 2 && time.Now().Before(deadline) {
		_, out := call("POST", "/v1/points/gives/preview", `{"audience":"recent","minutes":5}`)
		_ = json.Unmarshal([]byte(out), &g)
		time.Sleep(20 * time.Millisecond)
	}
	if g.Viewers != 2 || len(g.Sample) != 2 {
		t.Fatalf("%+v", g)
	}
	body := `{"audience":"recent","minutes":5,"amount":40,"reason":"stream end","request_id":"r1"}`
	code, out := call("POST", "/v1/points/gives", body)
	if code != 200 || !strings.Contains(out, `"total":80`) || !strings.Contains(out, `"applied":true`) {
		t.Fatal(code, out)
	}
	var give struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &give)
	if code, out := call("POST", "/v1/points/gives", body); code != 200 || !strings.Contains(out, `"applied":false`) || !strings.Contains(out, give.ID) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/points", ""); !strings.Contains(out, `"issued":80`) {
		t.Fatal("paid twice:", out)
	}
	if _, out := call("GET", "/v1/points/gives", ""); !strings.Contains(out, "everyone who chatted in the last 5 minutes") {
		t.Fatal(out)
	}
	if code, out := call("POST", "/v1/points/gives/"+give.ID+"/undo", ""); code != 200 || !strings.Contains(out, `"undone":2`) {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/points/gives/g_nope/undo", ""); code != 404 {
		t.Fatal(code)
	}
	bob, _ := testViewers.ByLogin(ctx, "bob")
	if code, _ := call("POST", "/v1/viewers/"+bob.ID+"/forget", ""); code != 200 {
		t.Fatal(code)
	}
	code, out = call("GET", "/v1/exports/viewers", "")
	var all ViewersExport
	if err := json.Unmarshal([]byte(out), &all); code != 200 || err != nil || all.Schema != "streamgremlin.viewers-export/v1" || len(all.Viewers) != 2 {
		t.Fatal(code, out)
	}
	if strings.Contains(out, `"bob"`) || !strings.Contains(out, "Forgotten viewer") || len(all.Viewers[0].History) != 2 {
		t.Fatal("forgotten viewers are unnamed, and history is all there:", out)
	}
}
