//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Quotes through the real engine, chat and CLI, with the !quote, !quoteadd
// and !quotedel templates: who may add, numbers never reused, a test event
// never adding, and quotes surviving a crash.
func TestQuotesThroughChatAndTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	chatUp := func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return "not yet: " + out
		}
		return ""
	}
	eventually(t, "chat", chatUp)
	for _, id := range []string{"quote", "quoteadd", "quotedel"} {
		out, _, _ := cli(t, dir, "templates", "show", id)
		f := writeFile(t, t.TempDir(), id+".json", out)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
		if out, errOut, code := cli(t, dir, "workflows", "on", "chat."+id); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	said := func(text string) func() string {
		return func() string {
			for _, m := range tw.ChatLog() {
				if strings.HasPrefix(m.Text, text) {
					return ""
				}
			}
			return "no " + text
		}
	}

	tw.Say("streamer", "Fan", "!quoteadd not allowed") // not a mod: no answer
	tw.Say("streamer", "streamer", "!quoteadd I meant to do that")
	eventually(t, "the first quote", said("Added quote #1: I meant to do that"))
	tw.SetModerator("streamer", "modfriend", true)
	tw.Say("streamer", "ModFriend", "!quoteadd   chat is   always right ")
	eventually(t, "the mod's quote", said("Added quote #2: chat is always right"))
	out, _, _ := cli(t, dir, "quotes")
	if !strings.Contains(out, "#2 chat is always right (added by ModFriend") || strings.Contains(out, "not allowed") || !strings.Contains(out, "2 quotes.") {
		t.Fatalf("quotes:\n%s", out)
	}

	tw.Say("streamer", "Fan", "!quote 1")
	eventually(t, "quote one", said("#1: I meant to do that ("))
	tw.Say("streamer", "Fan", "!quote 9")
	eventually(t, "no quote nine", said("No such quote. There are 2 quotes"))
	tw.Say("streamer", "Fan", "!quotedel 2") // not a mod
	tw.Say("streamer", "streamer", "!quotedel #1")
	eventually(t, "deleted", said("Deleted quote #1."))
	if out, _, _ := cli(t, dir, "quotes"); strings.Contains(out, "#1 ") || !strings.Contains(out, "#2 ") {
		t.Fatalf("after deleting:\n%s", out)
	}

	// A test event runs the workflow as a test: nothing is added.
	cli(t, dir, "events", "simulate", "chat.command.received", "command=!quoteadd", "args=just a test", "role=moderator")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.quoteadd"); !strings.Contains(out, "succeeded (test)") {
			return "not yet: " + out
		}
		return ""
	})
	if out, _, _ := cli(t, dir, "quotes"); strings.Contains(out, "just a test") {
		t.Fatalf("a test added a quote:\n%s", out)
	}

	// A crash: the quotes stay, and numbers keep counting up.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	if out, errOut, code := cli(t, dir, "quotes", "add", "-by", "Streamer", "back", "again"); code != 0 || !strings.Contains(out, "Added quote #3.") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "quotes", "edit", "3", "back once more"); code != 0 || !strings.Contains(out, "#3 back once more (added by Streamer") {
		t.Fatal(out, errOut)
	}
	if out, _, code := cli(t, dir, "quotes", "show", "#2"); code != 0 || !strings.Contains(out, "chat is always right") {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "quotes", "delete", "1"); code == 0 || !strings.Contains(errOut, "no such quote") {
		t.Fatalf("deleted twice: %d %s", code, errOut)
	}
}
