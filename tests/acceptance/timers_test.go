//go:build acceptance

package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Timers through the real engine and CLI: Run now, an interval that waits
// for chat lines, the count surviving a crash, a workflow step turning a
// timer off, and test events that never turn one off.
func TestTimersThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	chatUp := func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	}
	eventually(t, "chat", chatUp)
	if out, errOut, code := cli(t, dir, "workflows", "save", "../../examples/workflows/socials.json"); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "timer.socials")
	stop := filepath.Join(t.TempDir(), "stop.json")
	if err := os.WriteFile(stop, []byte(`{"schema":"streamgremlin.workflow/v1","id":"chat.socials-off","name":"Stop the socials",
		"triggers":[{"type":"chat.command.received","where":{"command":"nosocials","chatter.role":["moderator","broadcaster"]}}],
		"steps":[{"action":"timers.set_state","with":{"timer":"socials","state":"off"}},{"action":"chat.send","with":{"message":"Socials off."}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := cli(t, dir, "workflows", "save", stop); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.socials-off")
	reminders := func() int {
		n := 0
		for _, m := range tw.ChatLog() {
			if strings.HasPrefix(m.Text, "📣 Follow along") {
				n++
			}
		}
		return n
	}
	said := func(text string) func() string {
		return func() string {
			for _, m := range tw.ChatLog() {
				if strings.Contains(m.Text, text) {
					return ""
				}
			}
			return "no " + text
		}
	}

	if out, errOut, code := cli(t, dir, "timers", "set", "socials", "-name", "Socials", "-every", "10s", "-lines", "2"); code != 0 ||
		!strings.Contains(out, "Saved Socials (socials): every 10s, on") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "timers", "set", "bad", "-name", "Bad", "-every", "5s"); code == 0 || !strings.Contains(errOut, "10 seconds") {
		t.Fatalf("too often: %d %s", code, errOut)
	}

	// Run now: the workflow runs for real, and the count goes up.
	if out, errOut, code := cli(t, dir, "timers", "run", "socials"); code != 0 || !strings.Contains(out, "1 times") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the first reminder", said("(reminder #1)"))

	// Its turn comes after 10 seconds, but it waits for two chat lines.
	time.Sleep(11 * time.Second)
	if out, _, _ := cli(t, dir, "timers"); !strings.Contains(out, "waiting for chat: 0 of 2 lines") {
		t.Fatalf("waiting:\n%s", out)
	}
	if reminders() != 1 {
		t.Fatal("went off without chat")
	}
	tw.Say("streamer", "Fan", "hello")
	tw.Say("streamer", "Fan", "still here")
	eventually(t, "the second reminder", said("(reminder #2)"))

	// A crash: the timer and its count stay, and nothing fires in a burst.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	eventually(t, "chat again", chatUp)
	out, _, _ := cli(t, dir, "timers")
	if !strings.Contains(out, "socials") || !strings.Contains(out, "gone off 2 times") || !strings.Contains(out, "after 2 chat lines") {
		t.Fatalf("after a crash:\n%s", out)
	}

	// A test event of the stop command runs, but never turns the timer off.
	cli(t, dir, "events", "simulate", "chat.command.received", "command=!nosocials", "role=moderator")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.socials-off"); !strings.Contains(out, "succeeded (test)") {
			return out
		}
		return ""
	})
	if out, _, _ := cli(t, dir, "timers"); strings.Contains(out, "; off;") {
		t.Fatalf("a test turned it off:\n%s", out)
	}
	// Your real command does.
	tw.Say("streamer", "streamer", "!nosocials")
	eventually(t, "socials off", said("Socials off."))
	if out, _, _ := cli(t, dir, "timers"); !strings.Contains(out, "; off;") {
		t.Fatalf("still on:\n%s", out)
	}
	if out, errOut, code := cli(t, dir, "timers", "on", "socials"); code != 0 || !strings.Contains(out, "Socials is on") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "timers", "delete", "socials"); code != 0 {
		t.Fatal(out, errOut)
	}
	if _, _, code := cli(t, dir, "timers", "run", "socials"); code == 0 {
		t.Fatal("ran a deleted timer")
	}
}
