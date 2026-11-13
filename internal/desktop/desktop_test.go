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
	h := Handler(assets, st, nil, nil)
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
	h := Handler(assets, st, nil, nil)
	if i := info(t, h); i.State != PhaseFailed || i.Message != "StreamGremlin is already running" {
		t.Fatalf("%+v", i)
	}
	w := get(h, "GET", "/v1/status", nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "already running") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAssetsHaveCSPAndScreensFallBackToIndex(t *testing.T) {
	h := Handler(assets, NewState(), nil, nil)
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

func TestAllowedURL(t *testing.T) {
	for _, ok := range []string{
		"https://www.twitch.tv/activate?public=true&device-code=ABCDEFGH",
		"https://dev.twitch.tv/console",
		"https://dev.twitch.tv/console/apps/create",
		"https://dev.twitch.tv/docs/authentication/register-app/",
		"http://127.0.0.1:7441/activate?device-code=ABC",
	} {
		if !AllowedURL(ok) {
			t.Errorf("%s should be allowed", ok)
		}
	}
	for _, bad := range []string{
		"https://twitch.tv.evil.example/activate", "http://www.twitch.tv/activate", "https://www.twitch.tv/somebody",
		"https://user@www.twitch.tv/activate", "javascript:alert(1)", "file:///etc/passwd", "http://192.168.1.2:7441/activate",
		"http://localhost:7441/activate", "http://127.0.0.1/activate", "https://dev.twitch.tv/consolex", "https://www.twitch.tv/activate#x",
	} {
		if AllowedURL(bad) {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestOpenURLOnlyOpensAllowedPages(t *testing.T) {
	var opened []string
	h := Handler(fstest.MapFS{}, NewState(), func(u string) error { opened = append(opened, u); return nil }, nil)
	post := func(body string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/open-url", strings.NewReader(body)))
		return w.Code
	}
	if c := post(`{"url":"https://www.twitch.tv/activate?device-code=X"}`); c != 204 {
		t.Fatalf("allowed: %d", c)
	}
	if c := post(`{"url":"https://evil.example/"}`); c != 400 {
		t.Fatalf("refused: %d", c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/desktop/open-url?url=https://www.twitch.tv/activate", nil))
	if w.Code != 405 || len(opened) != 1 {
		t.Fatalf("GET: %d, opened %v", w.Code, opened)
	}
}

func TestRestartEngine(t *testing.T) {
	restarts := 0
	st := NewState()
	st.Ready(&fakeEngine{})
	h := Handler(fstest.MapFS{}, st, nil, func() error { restarts++; st.Starting(); return nil })
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest("GET", "/desktop/restart-engine", nil))
	if get.Code != 405 || restarts != 0 {
		t.Fatalf("GET: %d", get.Code)
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest("POST", "/desktop/restart-engine", nil))
	if post.Code != 202 || restarts != 1 {
		t.Fatalf("POST: %d", post.Code)
	}
	info := httptest.NewRecorder()
	h.ServeHTTP(info, httptest.NewRequest("GET", "/desktop/info", nil))
	if !strings.Contains(info.Body.String(), `"state":"starting"`) {
		t.Fatal(info.Body.String())
	}
	none := httptest.NewRecorder()
	Handler(fstest.MapFS{}, NewState(), nil, nil).ServeHTTP(none, httptest.NewRequest("POST", "/desktop/restart-engine", nil))
	if none.Code != 503 {
		t.Fatal(none.Code)
	}
}

// Saving an export: the system's dialog decides where (the UI only
// suggests a name); a cancel saves nothing; bad names and other methods are
// refused; without a dialog it says so.
func TestSaveFile(t *testing.T) {
	var got struct {
		name string
		data []byte
	}
	answer := "/home/me/ann.json"
	h := Handler(fstest.MapFS{}, NewState(), nil, nil, Dialogs{Save: func(name string, data []byte) (string, error) {
		got.name, got.data = name, data
		return answer, nil
	}})
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/save-file", strings.NewReader(body)))
		return w
	}
	if w := post(`{"name":"ann.json","text":"{}"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"saved":"/home/me/ann.json"`) ||
		got.name != "ann.json" || string(got.data) != "{}" {
		t.Fatal(w.Code, w.Body.String(), got)
	}
	answer = ""
	if w := post(`{"name":"ann.json","text":"{}"}`); !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatal(w.Body.String())
	}
	for _, bad := range []string{`{"name":"../../etc/passwd","text":"x"}`, `{"name":"","text":"x"}`, `{"name":"a/b.json","text":"x"}`, `not json`} {
		if w := post(bad); w.Code != 400 {
			t.Error(bad, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/desktop/save-file", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	Handler(fstest.MapFS{}, NewState(), nil, nil).ServeHTTP(w, httptest.NewRequest("POST", "/desktop/save-file",
		strings.NewReader(`{"name":"a.json","text":"x"}`)))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

// The app can ask for a Now playing overlay's files by kind only; the
// dialog's title and filter come from the kind, and only the path comes
// back.
func TestChooseFile(t *testing.T) {
	var asked []string
	answer := "/Users/me/Pretzel/album.jpg"
	h := Handler(fstest.MapFS{}, NewState(), nil, nil, Dialogs{Choose: func(title, filter, pattern string) (string, error) {
		asked = append(asked, title+"|"+filter+"|"+pattern)
		return answer, nil
	}})
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/desktop/choose-file", strings.NewReader(body)))
		return w
	}
	if w := post(`{"kind":"song_art"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"path":"/Users/me/Pretzel/album.jpg"`) ||
		len(asked) != 1 || asked[0] != "Choose the album art|Pictures|*.jpg;*.jpeg;*.png" {
		t.Fatal(w.Code, w.Body.String(), asked)
	}
	answer = ""
	if w := post(`{"kind":"song_text"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"path":""`) || !strings.HasSuffix(asked[1], "*.txt") {
		t.Fatal("cancelled:", w.Body.String(), asked)
	}
	for _, bad := range []string{`{"kind":"anything"}`, `{"kind":"song_art","pattern":"*"}x`, `not json`} {
		if w := post(bad); w.Code != 400 {
			t.Error(bad, w.Code)
		}
	}
	if len(asked) != 2 {
		t.Fatal("a dialog opened for a bad request:", asked)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/desktop/choose-file", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	Handler(fstest.MapFS{}, NewState(), nil, nil).ServeHTTP(w, httptest.NewRequest("POST", "/desktop/choose-file", strings.NewReader(`{"kind":"song_art"}`)))
	if w.Code != 503 {
		t.Fatal("no dialog:", w.Code)
	}
}
