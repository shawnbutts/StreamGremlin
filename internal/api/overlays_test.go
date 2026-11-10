package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

func TestOverlaysThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	code, out := call("POST", "/v1/overlays", `{"name":"alerts"}`)
	var created struct {
		Overlay OverlayView
		URL     string
	}
	_ = json.Unmarshal([]byte(out), &created)
	if code != 201 || created.Overlay.Kind != "alert" || !strings.HasPrefix(created.URL, testOverlays.URL+"/o/") {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/overlays", `{"name":"alerts"}`); code != 409 {
		t.Fatal("a taken name:", code)
	}
	for _, body := range []string{`{"name":"Bad"}`, `{"name":"x","kind":"scoreboard"}`, `{"name":"y","look":{"position":"left"}}`} {
		if code, out := call("POST", "/v1/overlays", body); code != 400 {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	// The list never carries links.
	code, out = call("GET", "/v1/overlays", "")
	if code != 200 || strings.Contains(out, "/o/") || !strings.Contains(out, `"server":"`+testOverlays.URL) || !strings.Contains(out, `"pages":0`) {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out = call("GET", "/v1/overlays/alerts/link", ""); code != 200 || !strings.Contains(out, created.URL) {
		t.Fatalf("link: %d %s", code, out)
	}
	// A sample alert needs the overlay open in OBS.
	if code, out = call("POST", "/v1/overlays/alerts/test", ""); code != 503 || !strings.Contains(out, "isn't open in OBS") {
		t.Fatalf("test, closed: %d %s", code, out)
	}
	// An unsaved look can be tried in the preview; a bad one is refused,
	// and neither is saved.
	if code, out = call("POST", "/v1/overlays/alerts/test?preview=1", `{"look":{"font":"custom","font_name":"Comic Neue","background2":"#ff0080"}}`); code != 200 || !strings.Contains(out, `"test":true`) {
		t.Fatalf("preview a look: %d %s", code, out)
	}
	if code, _ = call("POST", "/v1/overlays/alerts/test?preview=1", `{"look":{"font":"custom","font_name":"x\"; }"}}`); code != 400 {
		t.Fatal("a bad look in a preview:", code)
	}
	if _, out = call("GET", "/v1/overlays", ""); strings.Contains(out, "Comic Neue") {
		t.Fatal("a previewed look was saved")
	}
	// A preview-only sample needs no OBS, and reaches no OBS.
	if code, out = call("POST", "/v1/overlays/alerts/test?preview=1", ""); code != 200 || !strings.Contains(out, `"test":true`) || strings.Contains(out, `"shown":true`) {
		t.Fatalf("preview: %d %s", code, out)
	}
	// Open it as OBS would; the sample shows once the page says it started.
	ctx := context.Background()
	wsURL := "ws" + strings.TrimPrefix(created.URL, "http") + "ws"
	c, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Subprotocols: []string{overlays.Subprotocol}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	go func() {
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m struct {
				Type string
				Body struct{ Alert overlays.Alert }
			}
			_ = json.Unmarshal(b, &m)
			if m.Type == "alert" {
				ack, _ := json.Marshal(map[string]string{"type": "ack", "id": m.Body.Alert.ID, "status": "started"})
				_ = c.Write(ctx, websocket.MessageText, ack)
			}
		}
	}()
	waitFor(t, func() bool { _, out := call("GET", "/v1/overlays", ""); return strings.Contains(out, `"pages":1`) })
	if code, out = call("POST", "/v1/overlays/alerts/test", ""); code != 200 || !strings.Contains(out, `"shown":true`) {
		t.Fatalf("test: %d %s", code, out)
	}
	// A look given in part keeps the rest.
	if code, out = call("PUT", "/v1/overlays/alerts", `{"look":{"animation":"pop"}}`); code != 200 || !strings.Contains(out, `"animation":"pop"`) || !strings.Contains(out, `"position":"top"`) {
		t.Fatalf("part of a look: %d %s", code, out)
	}
	if code, out = call("POST", "/v1/overlays", `{"name":"corner","look":{"position":"top-right"}}`); code != 201 || !strings.Contains(out, `"position":"top-right"`) || !strings.Contains(out, `"seconds":6`) {
		t.Fatalf("add with part of a look: %d %s", code, out)
	}
	if code, _ = call("PUT", "/v1/overlays/nope", `{"look":{}}`); code != 404 {
		t.Fatal(code)
	}
	// A new look reaches the page; a new link stops the old one.
	if code, out = call("PUT", "/v1/overlays/alerts", `{"look":{"position":"bottom-left","animation":"pop","text_color":"#000000","background":"#ffffff","opacity":80,"seconds":8}}`); code != 200 || !strings.Contains(out, "bottom-left") {
		t.Fatalf("look: %d %s", code, out)
	}
	code, out = call("POST", "/v1/overlays/alerts/link", "")
	if code != 200 || strings.Contains(out, created.URL) {
		t.Fatalf("new link: %d %s", code, out)
	}
	if res, err := http.Get(created.URL); err != nil || res.StatusCode != 404 {
		t.Fatal("the old link still works", err)
	} else {
		_ = res.Body.Close()
	}
	if code, out = call("DELETE", "/v1/overlays/alerts", ""); code != 200 || !strings.Contains(out, `"deleted":true`) {
		t.Fatalf("delete: %d %s", code, out)
	}
	if code, _ = call("GET", "/v1/overlays/alerts/link", ""); code != 404 {
		t.Fatal(code)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// Giveaway and wheel overlays can play a sound from the library as a roll
// or spin starts and lands; an alert overlay can't (its step chooses), and
// a sound not in the library is refused.
func TestOverlaySounds(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	if code, out := call("POST", "/v1/sounds/tada", string(testWAV)); code != 201 {
		t.Fatal(code, out)
	}
	for _, body := range []string{`{"name":"wheel","kind":"wheel"}`, `{"name":"alerts","kind":"alert"}`} {
		if code, out := call("POST", "/v1/overlays", body); code != 201 {
			t.Fatal(code, out)
		}
	}
	if code, out := call("PUT", "/v1/overlays/wheel", `{"look":{"end_sound":"tada","end_volume":70,"cheer_sound":"tada"}}`); code != 200 ||
		!strings.Contains(out, `"end_sound":"tada"`) || !strings.Contains(out, `"cheer_sound":"tada"`) || !strings.Contains(out, `"start_volume":80`) {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/overlays/wheel", `{"look":{"start_sound":"nope"}}`); code != 400 || !strings.Contains(out, "no sound") {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/overlays/wheel", `{"look":{"end_volume":101}}`); code != 400 {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/overlays/wheel", `{"look":{"cheer_sound":"nope"}}`); code != 400 {
		t.Fatal(code, out)
	}
	if code, out := call("PUT", "/v1/overlays/alerts", `{"look":{"cheer_sound":"tada"}}`); code != 400 || !strings.Contains(out, "step chooses") {
		t.Fatal(code, out)
	}
}

// Wheel looks (docs/adr/0036-packages-plugins-and-themes.md): listed with
// their settings; a wheel overlay takes one, settings stay with their
// look and are replaced with another; other overlays can't have one.
func TestWheelLooksThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	code, out := call("GET", "/v1/overlays", "")
	var list OverlayList
	_ = json.Unmarshal([]byte(out), &list)
	if code != 200 || len(list.Themes) != 3 || list.Themes[0].ID != "builtin:workshop" || len(list.Themes[0].Settings) != 3 || !list.Themes[0].OddsChecked {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := call("POST", "/v1/overlays", `{"name":"alerts","look":{"theme":"builtin:workshop"}}`); code != 400 || !strings.Contains(out, "wheel overlays") {
		t.Fatalf("an alert overlay with a look: %d %s", code, out)
	}
	if code, out := call("POST", "/v1/overlays", `{"name":"wheel","kind":"wheel"}`); code != 201 {
		t.Fatal(code, out)
	}
	look := func(body string) overlays.Look {
		t.Helper()
		code, out := call("PUT", "/v1/overlays/wheel", body)
		var v OverlayView
		_ = json.Unmarshal([]byte(out), &v)
		if code != 200 {
			t.Fatalf("%s: %d %s", body, code, out)
		}
		return v.Look
	}
	l := look(`{"look":{"theme":"builtin:workshop","theme_settings":{"title":"Pick a game","show_title":false}}}`)
	if l.Theme != "builtin:workshop" || l.ThemeSettings["title"] != "Pick a game" {
		t.Fatal(l)
	}
	// A change that doesn't mention them keeps the settings.
	if l := look(`{"look":{"start_volume":50}}`); l.ThemeSettings["title"] != "Pick a game" || l.StartVolume != 50 {
		t.Fatal(l)
	}
	// Another look's settings replace them; Classic has none.
	if l := look(`{"look":{"theme":"builtin:neon-arcade","theme_settings":{"accent":"#ff00ff"}}}`); l.ThemeSettings["title"] != nil || l.ThemeSettings["accent"] != "#ff00ff" {
		t.Fatal(l)
	}
	if l := look(`{"look":{"theme":""}}`); l.Theme != "" || len(l.ThemeSettings) != 0 {
		t.Fatal(l)
	}
	for _, body := range []string{
		`{"look":{"theme":"builtin:disco"}}`,
		`{"look":{"theme":"builtin:workshop","theme_settings":{"script":"alert(1)"}}}`,
		`{"look":{"theme":"builtin:workshop","theme_settings":{"accent":"red"}}}`,
	} {
		if code, out := call("PUT", "/v1/overlays/wheel", body); code != 400 {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
}
