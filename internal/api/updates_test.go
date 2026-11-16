package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockgithub"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/updates"
)

var (
	testGitHub     *mockgithub.Server
	testGitHubKey  ed25519.PrivateKey
	testUpdatesApp []string
)

func newTestUpdates(t *testing.T, set *settings.Store, dir string) *updates.Service {
	t.Helper()
	gh := mockgithub.New()
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	gh.Base = srv.URL
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	testGitHub, testGitHubKey, testUpdatesApp = gh, priv, nil
	s := updates.New(updates.Options{Settings: set, DataDir: dir, Version: "0.1.0-beta.5", API: srv.URL, TestServer: true,
		Repo: "shawnbutts/StreamGremlin", Keys: []ed25519.PublicKey{pub}, GOOS: "darwin", GOARCH: "arm64", FirstCheck: time.Hour,
		Apply: func(f string) error { testUpdatesApp = append(testUpdatesApp, f); return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Start(ctx)
	return s
}

func TestUpdatesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	var st updates.Status
	get := func() {
		t.Helper()
		rec := do(t, s, req{method: "GET", path: "/v1/updates", body: "", token: testToken})
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		st = updates.Status{}
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
	}
	get()
	if st.State != updates.StateNotChecked || !st.Settings.Check || st.Version != "0.1.0-beta.5" {
		t.Fatalf("%+v", st)
	}
	if rec := do(t, s, req{method: "POST", path: "/v1/updates/download", body: "", token: testToken}); rec.Code != http.StatusConflict {
		t.Fatalf("download before checking: %d %s", rec.Code, rec.Body)
	}

	testGitHub.Add(mockgithub.Release{Tag: "v0.1.0-beta.6", Prerelease: true, Notes: "Fixes.",
		Files: mockgithub.Signed(map[string][]byte{"StreamGremlin-0.1.0-beta.6-macos.dmg": []byte("dmg")}, testGitHubKey)})
	rec := do(t, s, req{method: "POST", path: "/v1/updates/check", body: "", token: testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.State != updates.StateAvailable || !st.Latest.Installable {
		t.Fatalf("%+v %v", st, err)
	}
	if rec := do(t, s, req{method: "POST", path: "/v1/updates/download", body: "", token: testToken}); rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for get(); st.Download.State == updates.DownloadRunning && time.Now().Before(deadline); get() {
		time.Sleep(10 * time.Millisecond)
	}
	if st.Download.State != updates.DownloadReady {
		t.Fatalf("%+v", st.Download)
	}

	if rec := do(t, s, req{method: "PUT", path: "/v1/settings/updates", body: `{}`, token: testToken}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no answer: %d", rec.Code)
	}
	if rec := do(t, s, req{method: "PUT", path: "/v1/settings/updates", body: `{"check":false}`, token: testToken}); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if get(); st.Settings.Check {
		t.Fatal("still checking")
	}
	if rec := do(t, s, req{method: "PUT", path: "/v1/settings/updates", body: `{"check":"no"}`, token: testToken}); rec.Code != http.StatusBadRequest {
		t.Fatalf("%d", rec.Code)
	}
}
