package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/actions"
	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/maintenance"
	"code.streamgremlin.com/streamgremlin/internal/mockobs"
	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

const testToken = "test-token-not-secret"

// testMock is the mock Twitch behind the latest newTestServer. Tests using
// it don't run in parallel.
var testMock *mocktwitch.Server

// testOBS is the mock OBS behind the latest newTestServer, at testOBSURL.
var (
	testOBS    *mockobs.Server
	testOBSURL string
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newTestServerWithSecrets(t)
	return s
}

func newTestServerWithSecrets(t *testing.T) (*Server, *secrets.Store) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	sec, err := secrets.Open(context.Background(), db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	mock := mocktwitch.New()
	mockSrv := httptest.NewServer(mock)
	t.Cleanup(mockSrv.Close)
	mock.SetBase(mockSrv.URL)
	set := settings.NewStore(db)
	inbox := events.NewInbox(db, nil)
	tw := twitch.New(twitch.Options{Secrets: sec, Settings: set, AuthURL: mockSrv.URL, Inbox: inbox})
	tw.Start(context.Background())
	t.Cleanup(tw.Close)
	testMock = mock
	obsMock := mockobs.New()
	obsSrv := httptest.NewServer(obsMock)
	t.Cleanup(obsSrv.Close)
	testOBS, testOBSURL = obsMock, "ws"+strings.TrimPrefix(obsSrv.URL, "http")
	ob := obs.New(obs.Options{DB: db, Settings: set, Secrets: sec, Inbox: inbox})
	if err := ob.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ob.Close)
	reg := workflow.NewRegistry()
	actions.Register(reg, tw, ob)
	vars := variables.New(db)
	wf := workflow.New(workflow.Options{DB: db, Inbox: inbox, Actions: reg, Variables: vars})
	if err := wf.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wf.Close)
	started := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	return NewServer(Options{Token: testToken}, Routes(Deps{
		DataDir: dir, StartedAt: started, DB: db, Settings: set, Secrets: sec,
		Events: inbox, Twitch: tw, OBS: ob, Workflows: wf, Variables: vars,
		Maintenance: maintenance.New(maintenance.Options{DB: db, Settings: set, DataDir: dir}),
		Now:         func() time.Time { return started.Add(90 * time.Second) },
	})), sec
}

type req struct {
	method, path, body string
	token              string
	host               string
	header             map[string]string
}

func do(t *testing.T, s *Server, rq req) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(rq.method, rq.path, strings.NewReader(rq.body))
	r.Host = "127.0.0.1:7430"
	if rq.host != "" {
		r.Host = rq.host
	}
	if rq.token != "" {
		r.Header.Set("Authorization", "Bearer "+rq.token)
	}
	for k, v := range rq.header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b ErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	return b.Error.Code
}

func TestHealthIsPublicAndSaysOnlyOK(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/health"})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestStatusNeedsTheToken(t *testing.T) {
	s := newTestServer(t)
	for _, tok := range []string{"", "wrong"} {
		w := do(t, s, req{method: "GET", path: "/v1/status", token: tok})
		if w.Code != 401 || errCode(t, w) != CodeUnauthorized {
			t.Fatalf("token %q: %d %s", tok, w.Code, w.Body)
		}
	}
	w := do(t, s, req{method: "GET", path: "/v1/status", token: testToken})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var st Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Status != "running" || st.UptimeSeconds != 90 || st.SchemaVersion < 1 || st.StartedAt != "2026-11-07T12:00:00.000Z" {
		t.Fatalf("unexpected status %+v", st)
	}
}

func TestEveryResponseCarriesHeaders(t *testing.T) {
	s := newTestServer(t)
	for _, rq := range []req{
		{method: "GET", path: "/v1/health"},
		{method: "GET", path: "/v1/status"},
		{method: "GET", path: "/nope"},
		{method: "GET", path: "/v1/health", host: "evil.example"},
	} {
		w := do(t, s, rq)
		if w.Header().Get(brand.APIVersionHeader) != "1" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s %s: missing headers %v", rq.method, rq.path, w.Header())
		}
	}
}

func TestHostCheckBlocksRebinding(t *testing.T) {
	s := newTestServer(t)
	for _, host := range []string{"evil.example", "evil.example:7430", "192.168.1.5:7430"} {
		w := do(t, s, req{method: "GET", path: "/v1/status", token: testToken, host: host})
		if w.Code != 403 || errCode(t, w) != CodeForbidden {
			t.Errorf("host %q: %d", host, w.Code)
		}
	}
	for _, host := range []string{"localhost:1", "127.0.0.1:2", "[::1]:3", "LOCALHOST"} {
		w := do(t, s, req{method: "GET", path: "/v1/status", token: testToken, host: host})
		if w.Code != 200 {
			t.Errorf("host %q: %d", host, w.Code)
		}
	}
}

func TestOriginIsRefusedUnlessAllowed(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/status", token: testToken,
		header: map[string]string{"Origin": "https://evil.example"}})
	if w.Code != 403 {
		t.Fatalf("%d", w.Code)
	}
}

func TestWrongAPIVersionIsRefused(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/health", header: map[string]string{brand.APIVersionHeader: "2"}})
	if w.Code != 400 || errCode(t, w) != CodeUnsupportedAPIVersion {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/nothing", token: testToken})
	if w.Code != 404 || errCode(t, w) != CodeNotFound {
		t.Fatalf("404: %d %s", w.Code, w.Body)
	}
	w = do(t, s, req{method: "DELETE", path: "/v1/settings/ui", token: testToken})
	if w.Code != 405 || errCode(t, w) != CodeMethodNotAllowed || w.Header().Get("Allow") != "GET, PUT" {
		t.Fatalf("405: %d %s %v", w.Code, w.Body, w.Header())
	}
}

func TestSettingsRoundTripAndValidation(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, req{method: "GET", path: "/v1/settings/ui", token: testToken})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"theme":"system"`) {
		t.Fatalf("default: %d %s", w.Code, w.Body)
	}
	for i := 0; i < 2; i++ { // a retried PUT is harmless
		w = do(t, s, req{method: "PUT", path: "/v1/settings/ui", token: testToken, body: `{"theme":"midnight"}`})
		if w.Code != 200 {
			t.Fatalf("put: %d %s", w.Code, w.Body)
		}
	}
	w = do(t, s, req{method: "GET", path: "/v1/settings/ui", token: testToken})
	if !strings.Contains(w.Body.String(), `"theme":"midnight"`) {
		t.Fatalf("after put: %s", w.Body)
	}
	for _, body := range []string{`{"theme":"purple"}`, `{"theme":"dark","extra":1}`, `not json`, `{"theme":"dark"}{}`} {
		w = do(t, s, req{method: "PUT", path: "/v1/settings/ui", token: testToken, body: body})
		if w.Code != 400 || errCode(t, w) != CodeInvalidRequest {
			t.Errorf("body %q: %d %s", body, w.Code, w.Body)
		}
	}
	big := `{"theme":"` + strings.Repeat("x", maxBody) + `"}`
	w = do(t, s, req{method: "PUT", path: "/v1/settings/ui", token: testToken, body: big})
	if w.Code != 413 || errCode(t, w) != CodePayloadTooLarge {
		t.Errorf("big body: %d", w.Code)
	}
}

func TestOnlyHealthIsPublic(t *testing.T) {
	for _, rt := range newTestServer(t).Routes() {
		if rt.Permission == "" && rt.Path != "/v1/health" {
			t.Errorf("%s %s has no permission", rt.Method, rt.Path)
		}
	}
}

func TestRequireLoopback(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:0", "[::1]:7430", "127.0.0.2:1"} {
		if err := RequireLoopback(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:7430", ":7430", "localhost:7430", "192.168.1.2:80", "nonsense"} {
		if err := RequireLoopback(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-token")
	tok, err := LoadOrCreateToken(path)
	if err != nil || len(tok) < 40 {
		t.Fatalf("%q %v", tok, err)
	}
	again, err := LoadOrCreateToken(path)
	if err != nil || again != tok {
		t.Fatalf("reload: %q %v", again, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(path); err == nil {
		t.Fatal("damaged token accepted")
	}
}
