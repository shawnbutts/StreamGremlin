package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

const apiSecret = "oauth:never-in-an-answer"

func TestSecretsListChecksAndForgets(t *testing.T) {
	s, store := newTestServerWithSecrets(t)
	ctx := context.Background()
	if err := store.Set(ctx, "obs/password", secrets.NewValue(apiSecret)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "twitch/42/oauth", secrets.NewValue(apiSecret)); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, req{method: "GET", path: "/v1/secrets", token: testToken})
	var list SecretList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if list.Store.Kind != secrets.KindFile || list.Store.Label == "" || list.Checked || len(list.Secrets) != 2 || list.Secrets[0].State != "" {
		t.Fatalf("list %+v", list)
	}

	w = do(t, s, req{method: "GET", path: "/v1/secrets?check=true", token: testToken})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || !list.Checked {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, it := range list.Secrets {
		if it.State != secrets.CheckOK {
			t.Fatalf("check %+v", it)
		}
	}
	if strings.Contains(w.Body.String(), "never-in-an-answer") {
		t.Fatal("an answer holds a secret value")
	}

	for i := 0; i < 2; i++ { // a retried forget is harmless
		w = do(t, s, req{method: "DELETE", path: "/v1/secrets/twitch/42/oauth", token: testToken})
		if w.Code != 204 {
			t.Fatalf("delete #%d: %d %s", i, w.Code, w.Body)
		}
	}
	if _, err := store.Get(ctx, "twitch/42/oauth"); err == nil {
		t.Fatal("still saved")
	}
	w = do(t, s, req{method: "GET", path: "/v1/secrets", token: testToken})
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Secrets) != 1 || list.Secrets[0].Key != "obs/password" {
		t.Fatalf("after forget %+v", list)
	}
}

func TestSecretsRoutesNeedTheToken(t *testing.T) {
	s := newTestServer(t)
	for _, rq := range []req{
		{method: "GET", path: "/v1/secrets"},
		{method: "GET", path: "/v1/secrets?check=true"},
		{method: "DELETE", path: "/v1/secrets/obs/password"},
	} {
		if w := do(t, s, rq); w.Code != 401 {
			t.Errorf("%s %s: %d", rq.method, rq.path, w.Code)
		}
	}
}

func TestForgetRejectsBadNames(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/v1/secrets/Bad%20Name", "/v1/secrets/a//b", "/v1/secrets/a/..%2fb"} {
		w := do(t, s, req{method: "DELETE", path: path, token: testToken})
		if w.Code != 400 && w.Code != 404 && w.Code != 301 && w.Code != 307 {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
		if w.Code == 204 {
			t.Errorf("%s was accepted", path)
		}
	}
	if w := do(t, s, req{method: "PUT", path: "/v1/secrets/obs/password", token: testToken, body: "{}"}); w.Code != 405 {
		t.Errorf("PUT: %d; secrets are saved by the features that use them, not through this route", w.Code)
	}
}
