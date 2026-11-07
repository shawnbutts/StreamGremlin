package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeEngine struct{ got *http.Request }

func (f *fakeEngine) Token() string { return "tok" }
func (f *fakeEngine) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.got = r
		w.WriteHeader(204)
	})
}

var assets = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>app</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

func get(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://wails.localhost"+path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func info(t *testing.T, h http.Handler) Info {
	t.Helper()
	var i Info
	if err := json.Unmarshal(get(h, "GET", "/desktop/info", nil).Body.Bytes(), &i); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestStartingThenReady(t *testing.T) {
	st := NewState()
	h := Handler(assets, st)
	if i := info(t, h); i.State != PhaseStarting || i.Name != "StreamGremlin" {
		t.Fatalf("%+v", i)
	}
	w := get(h, "GET", "/v1/status", nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "engine_starting") {
		t.Fatalf("before ready: %d %s", w.Code, w.Body)
	}
	fe := &fakeEngine{}
	st.Ready(fe)
	if i := info(t, h); i.State != PhaseReady {
		t.Fatalf("%+v", i)
	}
	w = get(h, "GET", "/v1/status", map[string]string{
		"Origin": "wails://wails", "Authorization": "Bearer forged", "Cookie": "a=b",
	})
	if w.Code != 204 || fe.got == nil {
		t.Fatalf("bridge: %d", w.Code)
	}
	g := fe.got
	if g.Header.Get("Authorization") != "Bearer tok" || g.Header.Get("Origin") != "" ||
		g.Header.Get("Cookie") != "" || g.Host != "127.0.0.1" || g.Header.Get("StreamGremlin-API-Version") != "1" {
		t.Fatalf("bridged request not rewritten: host %q headers %v", g.Host, g.Header)
	}
}

func TestFailedStateExplainsWhy(t *testing.T) {
	st := NewState()
	st.Failed("StreamGremlin is already running")
	h := Handler(assets, st)
	if i := info(t, h); i.State != PhaseFailed || i.Message != "StreamGremlin is already running" {
		t.Fatalf("%+v", i)
	}
	w := get(h, "GET", "/v1/status", nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "already running") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAssetsHaveCSPAndScreensFallBackToIndex(t *testing.T) {
	h := Handler(assets, NewState())
	for _, p := range []string{"/", "/settings", "/assets/app.js"} {
		w := get(h, "GET", p, nil)
		if w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
		if w.Header().Get("Content-Security-Policy") != CSP {
			t.Errorf("%s: no CSP", p)
		}
	}
	if !strings.Contains(get(h, "GET", "/settings", nil).Body.String(), "<title>app</title>") {
		t.Error("screen path didn't serve index.html")
	}
	if get(h, "GET", "/missing.js", nil).Code != 404 {
		t.Error("missing file wasn't 404")
	}
}
