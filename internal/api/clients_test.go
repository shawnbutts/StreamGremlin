package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pairing a Stream Deck through the API: the app starts it and sees the
// code; the plugin swaps the code (no token needed) for its own token,
// which can read and press buttons and nothing else, and presses as the
// Stream Deck. Forgetting it refuses the token at once.
func TestPairingAStreamDeck(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, token, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: token, body: body})
		return w.Code, w.Body.String()
	}
	if code, out := call("GET", "/v1/clients/pairing", testToken, ""); code != 200 || !strings.Contains(out, `"state":"none"`) {
		t.Fatal(code, out)
	}
	// With nothing started, a code gets nowhere.
	if code, out := call("POST", "/v1/pair", "", `{"code":"ABCD-EFGH","name":"Deck"}`); code != 409 || !strings.Contains(out, "start pairing") {
		t.Fatal(code, out)
	}
	code, out := call("POST", "/v1/clients/pairing", testToken, `{"kind":"stream_deck"}`)
	var started struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(out), &started); err != nil || code != 201 || len(started.Code) != 9 {
		t.Fatal(code, out)
	}
	if code, out := call("GET", "/v1/clients/pairing", testToken, ""); !strings.Contains(out, `"state":"waiting"`) || !strings.Contains(out, started.Code) {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/pair", "", `{"code":"2222-2222","name":"Deck"}`); code != 403 || !strings.Contains(out, "isn't the one") {
		t.Fatal(code, out)
	}
	// An admin can't press as the Stream Deck.
	call("PUT", "/v1/buttons/brb", testToken, `{"label":"Be right back"}`)
	if code, _ := call("POST", "/v1/buttons/brb/press", testToken, `{"from":"stream_deck"}`); code != 400 {
		t.Fatal("admin claimed the stream deck:", code)
	}

	code, out = call("POST", "/v1/pair", "", `{"code":"`+started.Code+`","name":"Desk deck","request_id":"r1"}`)
	var paired PairResult
	if err := json.Unmarshal([]byte(out), &paired); err != nil || code != 201 || paired.Token == "" || paired.Client.Kind != "stream_deck" {
		t.Fatal(code, out)
	}
	if strings.Join(paired.Permissions, ",") != "buttons.press,buttons.read,deck.read,deck.use,self.read" {
		t.Fatal(paired.Permissions)
	}
	if code, out := call("GET", "/v1/clients/pairing", testToken, ""); !strings.Contains(out, `"state":"paired"`) || strings.Contains(out, paired.Token) {
		t.Fatal(code, out)
	}
	deck := paired.Token
	if code, out := call("GET", "/v1/whoami", deck, ""); code != 200 || !strings.Contains(out, `"kind":"stream_deck"`) || !strings.Contains(out, "Desk deck") {
		t.Fatal(code, out)
	}
	if code, out := call("GET", "/v1/whoami", testToken, ""); code != 200 || !strings.Contains(out, `"kind":"admin"`) {
		t.Fatal(code, out)
	}
	if code, out := call("GET", "/v1/buttons", deck, ""); code != 200 || !strings.Contains(out, "Be right back") {
		t.Fatal(code, out)
	}
	code, out = call("POST", "/v1/buttons/brb/press", deck, `{"request_id":"d1"}`)
	if code != 200 || !strings.Contains(out, `\"from\":\"stream_deck\"`) && !strings.Contains(out, `"from":"stream_deck"`) {
		t.Fatal(code, out)
	}
	// Whatever it says, it's the Stream Deck.
	if code, out := call("POST", "/v1/buttons/brb/press", deck, `{"request_id":"d2","from":"live"}`); code != 200 || !strings.Contains(out, "stream_deck") {
		t.Fatal(code, out)
	}
	// Nothing else.
	for _, c := range [][2]string{
		{"PUT", "/v1/buttons/brb"}, {"DELETE", "/v1/buttons/brb"}, {"GET", "/v1/workflows"}, {"GET", "/v1/secrets"},
		{"GET", "/v1/clients"}, {"POST", "/v1/clients/pairing"}, {"POST", "/v1/moderation"}, {"POST", "/v1/chat/messages"},
		{"GET", "/v1/status"}, {"GET", "/v1/settings/ui"},
	} {
		w := do(t, s, req{method: c[0], path: c[1], token: deck, body: `{}`})
		if w.Code != 403 || errCode(t, w) != CodeForbidden {
			t.Errorf("%s %s: %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
	// A web page can't use it either.
	if w := do(t, s, req{method: "GET", path: "/v1/buttons", token: deck, header: map[string]string{"Origin": "https://evil.example"}}); w.Code != 403 {
		t.Fatal("origin:", w.Code)
	}

	var list struct{ Clients []struct{ ID, Name string } }
	_, out = call("GET", "/v1/clients", testToken, "")
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Clients) != 1 || list.Clients[0].Name != "Desk deck" || strings.Contains(out, deck) {
		t.Fatal(out)
	}
	if code, _ := call("DELETE", "/v1/clients/"+list.Clients[0].ID, testToken, ""); code != 204 {
		t.Fatal("forget:", code)
	}
	if code, _ := call("DELETE", "/v1/clients/"+list.Clients[0].ID, testToken, ""); code != 404 {
		t.Fatal("forget twice:", code)
	}
	if w := do(t, s, req{method: "GET", path: "/v1/buttons", token: deck}); w.Code != 401 {
		t.Fatal("forgotten token:", w.Code)
	}

	// Cancelling, and bad requests.
	call("POST", "/v1/clients/pairing", testToken, `{"kind":"stream_deck"}`)
	if code, _ := call("DELETE", "/v1/clients/pairing", testToken, ""); code != 204 {
		t.Fatal(code)
	}
	for _, body := range []string{`{"kind":"phone"}`, `{"kind":"stream_deck","extra":1}`} {
		if code, out := call("POST", "/v1/clients/pairing", testToken, body); code != 400 {
			t.Fatal(body, code, out)
		}
	}
	if code, _ := call("POST", "/v1/pair", "", `{"code":"x","name":""}`); code != 400 {
		t.Fatal("empty name:", code)
	}
}
