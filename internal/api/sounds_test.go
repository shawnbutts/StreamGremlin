package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/sounds"
)

var testWAV = append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 64)...)

func TestSoundsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	code, out := call("POST", "/v1/sounds/ding?volume=60", string(testWAV))
	if code != 201 || !strings.Contains(out, `"format":"wav"`) || !strings.Contains(out, `"volume":60`) {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, _ = call("POST", "/v1/sounds/ding", string(testWAV)); code != 200 {
		t.Fatalf("repeat: %d", code)
	}
	if code, out = call("POST", "/v1/sounds/ding", "OggS"+strings.Repeat("x", 40)); code != 409 {
		t.Fatalf("other audio, same name: %d %s", code, out)
	}
	for path, body := range map[string]string{"/v1/sounds/Bad": string(testWAV), "/v1/sounds/x": "<html>", "/v1/sounds/y?volume=loud": string(testWAV),
		"/v1/sounds/z?volume=101": string(testWAV)} {
		if code, out := call("POST", path, body); code != 400 {
			t.Errorf("%s: %d %s", path, code, out)
		}
	}
	big := string(testWAV) + strings.Repeat("x", sounds.MaxBytes)
	if code, out = call("POST", "/v1/sounds/big", big); code != 413 {
		t.Fatalf("too big: %d %.100s", code, out)
	}
	code, out = call("GET", "/v1/sounds", "")
	var list SoundList
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != 200 || len(list.Sounds) != 1 || list.Player {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out = call("PUT", "/v1/sounds/ding", `{"volume":30}`); code != 200 || !strings.Contains(out, `"volume":30`) {
		t.Fatalf("volume: %d %s", code, out)
	}
	for _, bad := range []string{`{}`, `{"volume":-1}`, `{"volume":"loud"}`} {
		if code, _ := call("PUT", "/v1/sounds/ding", bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	w := do(t, s, req{method: "GET", path: "/v1/sounds/ding/audio", token: testToken})
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/wav" || !bytes.Equal(w.Body.Bytes(), testWAV) {
		t.Fatalf("audio: %d %v", w.Code, w.Header())
	}
	w = do(t, s, req{method: "GET", path: "/v1/sounds/ding/audio", token: testToken, header: map[string]string{"Range": "bytes=0-3"}})
	if w.Code != 206 || w.Body.String() != "RIFF" {
		t.Fatalf("range: %d %q", w.Code, w.Body.String())
	}

	// No window: playing says so.
	if code, out = call("POST", "/v1/sounds/ding/play", ""); code != 503 || !strings.Contains(out, "no StreamGremlin window") {
		t.Fatalf("no window: %d %s", code, out)
	}
	// A window polls through the API and reports.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		after := int64(-1)
		for ctx.Err() == nil {
			r := httptest.NewRequest("GET", "/v1/player/next?wait=1&after="+itoa(after), nil).WithContext(ctx)
			r.Host = "127.0.0.1:7430"
			r.Header.Set("Authorization", "Bearer "+testToken)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, r)
			var next struct {
				Commands []sounds.Command
				Head     int64
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &next)
			for _, c := range next.Commands {
				if c.Kind == "play" {
					rep, _ := json.Marshal(sounds.Report{ID: c.ID, OK: c.Volume == 30})
					do(t, s, req{method: "POST", path: "/v1/player/reports", token: testToken, body: string(rep)})
				}
			}
			after = next.Head
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !testPlayer.Connected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if code, out = call("POST", "/v1/sounds/ding/play", ""); code != 200 || !strings.Contains(out, `"played":true`) || !strings.Contains(out, `"volume":30`) {
		t.Fatalf("play: %d %s", code, out)
	}
	if code, out = call("POST", "/v1/sounds/ding/play", `{"volume":90}`); code != 500 && !strings.Contains(out, "couldn't play") {
		t.Fatalf("a refused play: %d %s", code, out)
	}
	if code, _ = call("POST", "/v1/player/stop", ""); code != 204 {
		t.Fatal(code)
	}
	if code, _ = call("POST", "/v1/player/reports", `{"id":"nothing","ok":true}`); code != 404 {
		t.Fatal(code)
	}
	if code, _ = call("POST", "/v1/sounds/nope/play", ""); code != 404 {
		t.Fatal(code)
	}
	if code, _ = call("DELETE", "/v1/sounds/ding", ""); code != 204 {
		t.Fatal(code)
	}
	if code, _ = call("DELETE", "/v1/sounds/ding", ""); code != 404 {
		t.Fatal(code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/sounds"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
