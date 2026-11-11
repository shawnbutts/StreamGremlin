//go:build acceptance

package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Buttons through the real engine and CLI (D-075): a button, a workflow
// that starts from it, a press that runs it for real, and the list saying
// what each button starts.
func TestButtonsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	})

	if out, errOut, code := cli(t, dir, "buttons", "set", "brb", "-label", "Be right back", "-color", "blue"); code != 0 || !strings.Contains(out, "Saved Be right back (brb)") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "buttons", "set", "bad", "-color", "plaid", "-label", "x"); code == 0 || !strings.Contains(errOut, "colour") {
		t.Fatalf("bad colour: %d %s", code, errOut)
	}
	wf := filepath.Join(t.TempDir(), "brb.json")
	if err := os.WriteFile(wf, []byte(`{"schema":"streamgremlin.workflow/v1","id":"live.brb","name":"Be right back",
		"triggers":[{"type":"buttons.button.pressed","where":{"button_id":"brb"}}],
		"steps":[{"action":"chat.send","with":{"message":"Back in a moment! (${event.payload.label})"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := cli(t, dir, "workflows", "save", wf); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "buttons", "press", "brb"); !strings.Contains(out, "No workflow that's on starts from it") {
		t.Fatalf("off: %s", out)
	}
	cli(t, dir, "workflows", "on", "live.brb")
	if out, _, _ := cli(t, dir, "buttons"); !strings.Contains(out, "starts Be right back") {
		t.Fatalf("list: %s", out)
	}
	if out, errOut, code := cli(t, dir, "buttons", "press", "brb"); code != 0 || !strings.Contains(out, "Started: Be right back") {
		t.Fatal(out, errOut)
	}
	said := func() int {
		n := 0
		for _, m := range tw.ChatLog() {
			if m.Text == "Back in a moment! (Be right back)" {
				n++
			}
		}
		return n
	}
	eventually(t, "the workflow's message", func() string {
		if said() != 1 {
			return "not said"
		}
		return ""
	})
	// The press is on the events list, from the CLI.
	if out, _, _ := cli(t, dir, "events"); !strings.Contains(out, "buttons.button.pressed") {
		t.Fatalf("events: %s", out)
	}
	if _, _, code := cli(t, dir, "buttons", "press", "nope"); code == 0 {
		t.Fatal("pressed a missing button")
	}
	cli(t, dir, "buttons", "set", "hype", "-label", "Hype!")
	if out, errOut, code := cli(t, dir, "buttons", "move", "hype", "brb"); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "buttons"); strings.Index(out, "hype") > strings.Index(out, "brb") {
		t.Fatalf("order: %s", out)
	}
	if out, _, code := cli(t, dir, "buttons", "delete", "brb"); code != 0 || !strings.Contains(out, "Deleted brb") {
		t.Fatal(out)
	}
}
