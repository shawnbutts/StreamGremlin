//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// eventually runs check until it returns "" or time runs out.
func eventually(t *testing.T, what string, check func() string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		problem := check()
		if problem == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s", what, problem)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestChatReadAndSendThroughTheCLI(t *testing.T) {
	m, url := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", url)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, m, "ChatStreamer"); code != 0 {
		t.Fatalf("sign-in: %s", out)
	}
	chatConnected := func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	}
	eventually(t, "chat connects", chatConnected)

	m.Say("chatstreamer", "SomeViewer", "!hello there")
	eventually(t, "the command arrives", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "chat.*")
		if !strings.Contains(out, "chat.message.received  (twitch)  SomeViewer: !hello there") ||
			!strings.Contains(out, "chat.command.received  (twitch)  SomeViewer: !hello there") {
			return out
		}
		return ""
	})

	if out, errOut, code := cli(t, dir, "chat", "send", "-as", "streamer", "hi", "chat"); code != 0 || !strings.Contains(out, "Sent to Twitch as chatstreamer") {
		t.Fatalf("send: %d %s%s", code, out, errOut)
	}
	if log := m.ChatLog(); log[len(log)-1].Text != "hi chat" {
		t.Fatalf("%+v", log)
	}
	if _, errOut, code := cli(t, dir, "chat", "send", strings.Repeat("x", 501)); code != 2 || !strings.Contains(errOut, "500") {
		t.Fatalf("too long: %d %s", code, errOut)
	}

	// While the engine is down, chat is missed (Twitch doesn't replay it);
	// after a restart, chat comes back by itself.
	e.kill()
	m.Say("chatstreamer", "SomeViewer", "while you were away")
	startEngine(t, dir, "-twitch-auth-url", url)
	eventually(t, "chat reconnects", chatConnected)
	m.Say("chatstreamer", "SomeViewer", "welcome back")
	eventually(t, "chat after the restart", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "chat.message.received")
		if !strings.Contains(out, "welcome back") || strings.Contains(out, "while you were away") {
			return out
		}
		return ""
	})
}
