package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// publicRoutes are the only routes that answer without the token: a
// liveness probe, and swapping a pairing code for a client's token
// (ADR-0042). Adding one is a security decision, not a test fix.
var publicRoutes = []string{"GET /v1/health", "POST /v1/pair"}

// readOnlyWrites are routes that use a method other than GET but change
// nothing, so a read permission is enough. Each says why.
var readOnlyWrites = map[string]string{
	"POST /v1/backups/{name}/verify": "only reads the backup and reports what's wrong",
	"POST /v1/filters/check":         "judges a sample message; nothing is sent or saved",
	"POST /v1/points/gives/preview":  "counts who's in a group; gives nothing",
	"POST /v1/player/reports":        "the app's window says whether a sound it was asked to play started; it changes only that play's result",
}

// TestEveryRouteIsGuarded sweeps the whole route table (M9 slice 1,
// D-110): every route except the public ones refuses a missing or wrong
// token, a foreign Host and a foreign Origin before its handler runs; a
// paired Stream Deck reaches only its kind's routes; and a route that
// changes something never needs only a read permission. A new route can't
// skip these checks unnoticed.
func TestEveryRouteIsGuarded(t *testing.T) {
	s := newTestServer(t)
	deck := pairDeck(t, s)
	param := regexp.MustCompile(`\{[^}]+\}`)

	var public []string
	for _, rt := range s.Routes() {
		name := rt.Method + " " + rt.Path
		path := param.ReplaceAllString(rt.Path, "x")
		if rt.Public {
			public = append(public, name)
		}

		// Host and Origin are checked for every route, public or not.
		if w := do(t, s, req{method: rt.Method, path: path, token: testToken, host: "evil.example"}); w.Code != http.StatusForbidden {
			t.Errorf("%s with a foreign Host: %d, want 403", name, w.Code)
		}
		if w := do(t, s, req{method: rt.Method, path: path, token: testToken, header: map[string]string{"Origin": "https://evil.example"}}); w.Code != http.StatusForbidden {
			t.Errorf("%s from a foreign Origin: %d, want 403", name, w.Code)
		}
		if rt.Public {
			continue
		}

		for _, tok := range []string{"", "wrong", testToken + "x", strings.ToUpper(testToken)} {
			w := do(t, s, req{method: rt.Method, path: path, token: tok, body: `{}`})
			if w.Code != http.StatusUnauthorized || errCode(t, w) != CodeUnauthorized {
				t.Errorf("%s with token %q: %d %s, want 401", name, tok, w.Code, w.Body.String())
			}
		}

		if !slices.Contains(clientPerms["stream_deck"], rt.Permission) {
			w := do(t, s, req{method: rt.Method, path: path, token: deck, body: `{}`})
			if w.Code != http.StatusForbidden || errCode(t, w) != CodeForbidden {
				t.Errorf("%s with a Stream Deck's token: %d %s, want 403", name, w.Code, w.Body.String())
			}
		}

		if rt.Method != "GET" && strings.HasSuffix(string(rt.Permission), ".read") {
			if _, ok := readOnlyWrites[name]; !ok {
				t.Errorf("%s changes something but needs only %s; give it a write permission, or list it in readOnlyWrites with why", name, rt.Permission)
			}
		}
	}
	slices.Sort(public)
	want := slices.Clone(publicRoutes)
	slices.Sort(want)
	if !slices.Equal(public, want) {
		t.Errorf("public routes are %v, want only %v", public, want)
	}
	for name := range readOnlyWrites {
		found := false
		for _, rt := range s.Routes() {
			found = found || rt.Method+" "+rt.Path == name
		}
		if !found {
			t.Errorf("readOnlyWrites lists %s, which isn't a route", name)
		}
	}
}

// TestUnknownRoutesAreRefusedPlainly: a path that isn't a route is
// a plain 404, and a foreign Host is still refused first.
func TestUnknownRoutesAreRefusedPlainly(t *testing.T) {
	s := newTestServer(t)
	if w := do(t, s, req{method: "GET", path: "/v1/nope"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown route: %d", w.Code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/nope", host: "evil.example"}); w.Code != http.StatusForbidden {
		t.Errorf("unknown route, foreign Host: %d", w.Code)
	}
	for _, p := range []string{"/v1/../v1/status", "/v1//status", "/v1/status/"} {
		if w := do(t, s, req{method: "GET", path: p}); w.Code == http.StatusOK {
			t.Errorf("%s answered without the token", p)
		}
	}
}

// pairDeck pairs a Stream Deck and returns its token.
func pairDeck(t *testing.T, s *Server) string {
	t.Helper()
	w := do(t, s, req{method: "POST", path: "/v1/clients/pairing", token: testToken, body: `{"kind":"stream_deck"}`})
	var started struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil || started.Code == "" {
		t.Fatalf("start pairing: %d %s", w.Code, w.Body.String())
	}
	w = do(t, s, req{method: "POST", path: "/v1/pair", body: `{"code":"` + started.Code + `","name":"Deck"}`})
	var paired PairResult
	if err := json.Unmarshal(w.Body.Bytes(), &paired); err != nil || paired.Token == "" {
		t.Fatalf("pair: %d %s", w.Code, w.Body.String())
	}
	return paired.Token
}
