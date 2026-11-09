//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// First Words through the real engine, CLI and mocktwitch: the welcome
// template greets each viewer once a stream, never the streamer; going
// live on Twitch starts over, and so does a reset; after a crash nobody
// is greeted twice.
func TestFirstWordsThroughTheCLI(t *testing.T) {
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
	out, _, _ := cli(t, dir, "templates", "show", "welcome-first")
	f := writeFile(t, t.TempDir(), "welcome.json", out)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.welcome-first")
	// The template says it one of three ways, at random.
	welcomes := func(name string) int {
		n := 0
		for _, m := range tw.ChatLog() {
			switch m.Text {
			case "Welcome to the stream, " + name + "! 👋", "Hi there, " + name + ", glad you're here!", "Welcome in, " + name + "!":
				n++
			}
		}
		return n
	}
	waitWelcomes := func(name string, want int) {
		t.Helper()
		eventually(t, "welcomes for "+name, func() string {
			if n := welcomes(name); n != want {
				return strings.Repeat("x", n)
			}
			return ""
		})
	}

	tw.Say("streamer", "Fan", "hi!")
	tw.Say("streamer", "Fan", "how's it going")
	tw.Say("streamer", "Streamer", "hello chat")
	tw.Say("streamer", "Pal", "yo")
	waitWelcomes("Fan", 1)
	waitWelcomes("Pal", 1)
	if welcomes("Streamer") != 0 {
		t.Fatal("the streamer was welcomed")
	}
	if out, _, _ := cli(t, dir, "first-words"); !strings.Contains(out, "2 viewers have said their first words") {
		t.Fatalf("status:\n%s", out)
	}

	// A crash: nobody is greeted twice.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	eventually(t, "chat again", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	})
	tw.Say("streamer", "Fan", "still here")
	tw.Say("streamer", "Newbie", "first time!")
	waitWelcomes("Newbie", 1)
	if welcomes("Fan") != 1 {
		t.Fatal("Fan welcomed twice after a restart")
	}

	// Going live starts over; so does a reset.
	tw.GoLive("streamer")
	eventually(t, "the stream start", func() string {
		if out, _, _ := cli(t, dir, "first-words"); !strings.Contains(out, "you went live") {
			return out
		}
		return ""
	})
	tw.Say("streamer", "Fan", "back again")
	waitWelcomes("Fan", 2)
	if out, errOut, code := cli(t, dir, "first-words", "reset"); code != 0 || !strings.Contains(out, "Started over") {
		t.Fatal(out, errOut)
	}
	tw.Say("streamer", "Fan", "and again")
	waitWelcomes("Fan", 3)
}
