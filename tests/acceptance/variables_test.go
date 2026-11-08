//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Saved variables through the real engine and CLI: counting from chat, per
// viewer, surviving a crash, edited by hand, and never changed by tests.
func TestSavedVariablesThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
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
	for _, f := range []string{"deaths", "lurk"} {
		if out, errOut, code := cli(t, dir, "workflows", "save", "../../examples/workflows/"+f+".json"); code != 0 {
			t.Fatal(out, errOut)
		}
		cli(t, dir, "workflows", "on", "chat."+f)
	}
	said := func(text string) func() string {
		return func() string {
			for _, m := range tw.ChatLog() {
				if m.Text == text {
					return ""
				}
			}
			return "no " + text
		}
	}

	tw.Say("streamer", "streamer", "!death")
	eventually(t, "one death", said("💀 Deaths so far: 1"))
	tw.Say("streamer", "viewer", "!death") // not a mod: only says the count
	tw.Say("streamer", "streamer", "!death")
	eventually(t, "two deaths", said("💀 Deaths so far: 2"))
	tw.Say("streamer", "Fan", "!lurk")
	eventually(t, "a lurk", said("Enjoy the lurk, Fan! That's lurk number 1 👀"))
	out, _, _ := cli(t, dir, "vars")
	if !strings.Contains(out, "deaths") || !strings.Contains(out, "2") || !strings.Contains(out, "(Fan)") || !strings.Contains(out, "lurks") {
		t.Fatalf("vars:\n%s", out)
	}

	// A crash: the values stay.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	eventually(t, "chat again", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	})
	tw.Say("streamer", "Fan", "!lurk")
	eventually(t, "the second lurk", said("Enjoy the lurk, Fan! That's lurk number 2 👀"))

	// Changed by hand, then used by the next run.
	if out, errOut, code := cli(t, dir, "vars", "set", "global", "deaths", "10"); code != 0 || !strings.Contains(out, "deaths = 10") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "vars", "set", "global", "Deaths", "1"); code == 0 || !strings.Contains(errOut, "lowercase") {
		t.Fatalf("a bad name: %d %s", code, errOut)
	}
	tw.Say("streamer", "streamer", "!death")
	eventually(t, "eleven deaths", said("💀 Deaths so far: 11"))

	// A test event runs, but never saves.
	cli(t, dir, "events", "simulate", "chat.command.received", "command=!lurk", "user=Tester")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.lurk"); !strings.Contains(out, "succeeded (test)") {
			return out
		}
		return ""
	})
	if out, _, _ := cli(t, dir, "vars", "viewer"); strings.Contains(out, "Tester") || !strings.Contains(out, "Fan") {
		t.Fatalf("a test run saved a variable:\n%s", out)
	}
	if out, _, code := cli(t, dir, "vars", "delete", "global", "deaths"); code != 0 || !strings.Contains(out, "Deleted") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "vars", "global"); strings.Contains(out, "deaths") {
		t.Fatalf("deleted, but:\n%s", out)
	}
}
