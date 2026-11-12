//go:build acceptance

package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pairing a Stream Deck through the real engine and CLI (M7 slice 4,
// ADR-0042), with the plugin's side done as plain HTTP, as the plugin
// will: the CLI shows a code; the code, sent without a token, gets the
// deck its own token; the token presses a button (recorded as the Stream
// Deck) and can do nothing else; it survives a restart; forgetting it
// refuses it. Neither the code nor the token reaches the logs.
func TestPairingAStreamDeckThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir, "-debug")
	if out, errOut, code := cli(t, dir, "buttons", "set", "brb", "-label", "Be right back"); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "streamdeck"); !strings.Contains(out, "No Stream Deck is paired") {
		t.Fatal(out)
	}
	out, errOut, code := cli(t, dir, "streamdeck", "pair", "-wait=false", "-json")
	var p struct{ Code string }
	if err := json.Unmarshal([]byte(out), &p); err != nil || code != 0 || len(p.Code) != 9 {
		t.Fatal(out, errOut, code)
	}
	if code, body := call(t, e, "POST", "/v1/pair", "", `{"code":"2222-2222","name":"Desk deck"}`, nil); code != 403 {
		t.Fatal("wrong code:", code, body)
	}
	code, body := call(t, e, "POST", "/v1/pair", "", `{"code":"`+p.Code+`","name":"Desk deck","request_id":"r1"}`, nil)
	var paired struct {
		Token  string
		Client struct{ ID string }
	}
	if err := json.Unmarshal([]byte(body), &paired); err != nil || code != 201 || paired.Token == "" {
		t.Fatal(code, body)
	}
	deck := paired.Token
	if code, body := call(t, e, "POST", "/v1/buttons/brb/press", deck, `{"request_id":"k1"}`, nil); code != 200 || !strings.Contains(body, "stream_deck") {
		t.Fatal("press:", code, body)
	}
	for _, c := range [][2]string{{"GET", "/v1/status"}, {"GET", "/v1/workflows"}, {"PUT", "/v1/buttons/brb"}, {"GET", "/v1/clients"}} {
		if code, _ := call(t, e, c[0], c[1], deck, `{"label":"x"}`, nil); code != 403 {
			t.Errorf("%s %s: %d", c[0], c[1], code)
		}
	}
	if out, _, _ := cli(t, dir, "streamdeck"); !strings.Contains(out, "Desk deck") || !strings.Contains(out, "last used") {
		t.Fatal(out)
	}

	// The pairing is kept across a restart; the code isn't.
	e.kill()
	e = startEngine(t, dir, "-debug")
	if code, _ := call(t, e, "GET", "/v1/buttons", deck, "", nil); code != 200 {
		t.Fatal("after restart:", code)
	}
	if code, _ := call(t, e, "POST", "/v1/pair", "", `{"code":"`+p.Code+`","name":"Again"}`, nil); code != 409 {
		t.Fatal("old code after restart:", code)
	}

	if out, errOut, code := cli(t, dir, "streamdeck", "forget", paired.Client.ID); code != 0 || !strings.Contains(out, "Forgotten") {
		t.Fatal(out, errOut)
	}
	if code, _ := call(t, e, "GET", "/v1/buttons", deck, "", nil); code != 401 {
		t.Fatal("forgotten token:", code)
	}
	if _, errOut, code := cli(t, dir, "streamdeck", "forget", paired.Client.ID); code == 0 {
		t.Fatal("forgot twice:", errOut)
	}

	time.Sleep(100 * time.Millisecond)
	logFile, err := os.ReadFile(filepath.Join(dir, "logs", "engine.log"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"engine.log": string(logFile), "stderr": e.stderr.String()} {
		if strings.Contains(text, deck) || strings.Contains(text, p.Code) || strings.Contains(text, strings.ReplaceAll(p.Code, "-", "")) {
			t.Errorf("the deck's token or the code appears in %s", name)
		}
	}
	if !strings.Contains(string(logFile), "client paired") {
		t.Errorf("pairing isn't logged:\n%s", logFile)
	}
}
