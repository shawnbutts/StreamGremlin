//go:build acceptance

package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A workflow that answers any chat, sending as you with no bot signed in,
// answers each viewer once: its own messages come back from Twitch as your
// chat, and must never start it again.
func TestAWorkflowNeverAnswersItself(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return "not yet: " + out
		}
		return ""
	})
	f := filepath.Join(t.TempDir(), "echo.json")
	if err := os.WriteFile(f, []byte(`{"schema":"streamgremlin.workflow/v1","id":"chat.echo","name":"Echo",
		"triggers":[{"type":"chat.message.received"}],
		"steps":[{"action":"chat.send","with":{"message":"I heard: ${event.payload.text}"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.echo")
	tw.Say("streamer", "Fan", "hello")
	eventually(t, "the answer", func() string {
		for _, m := range tw.ChatLog() {
			if m.Text == "I heard: hello" {
				return ""
			}
		}
		return "no answer"
	})
	time.Sleep(2 * time.Second) // time for a loop to show itself
	n := 0
	for _, m := range tw.ChatLog() {
		if strings.HasPrefix(m.Text, "I heard:") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d answers: %+v", n, tw.ChatLog())
	}
	// You typing in chat is still you chatting, and is answered.
	tw.Say("streamer", "streamer", "me too")
	eventually(t, "answering you", func() string {
		for _, m := range tw.ChatLog() {
			if m.Text == "I heard: me too" {
				return ""
			}
		}
		return "no answer to you"
	})
}
