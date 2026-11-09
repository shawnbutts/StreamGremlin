//go:build acceptance

package acceptance

import (
	"regexp"
	"strings"
	"testing"
)

// A giveaway through the real engine, CLI and mocktwitch chat: opened from
// the CLI and announced; viewers enter in chat and hear they're in; a
// second !join and the streamer's own don't count; drawn from the CLI; the
// winner claims in chat; ended with congratulations; and the record checks
// out with "giveaways verify". A practice giveaway takes test chat and
// sends nothing.
func TestGiveawaysThroughChatAndTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not reading chat"
		}
		return ""
	})
	said := func(text string) func() string {
		return func() string {
			for _, m := range tw.ChatLog() {
				if strings.Contains(m.Text, text) {
					return ""
				}
			}
			return "nothing says " + text
		}
	}

	if out, errOut, code := cli(t, dir, "giveaways", "open", "-winners", "1", "-claim", "60", "a", "Steam", "key"); code != 0 ||
		!strings.Contains(out, "Viewers type !join") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the opening", said("Giveaway: a Steam key! Type !join in chat to enter"))
	for _, v := range []string{"ann", "bob", "cat"} {
		tw.Say("streamer", v, "!join")
	}
	tw.Say("streamer", "ann", "!join")
	tw.Say("streamer", "streamer", "!join")
	eventually(t, "the entries", func() string {
		out, _, _ := cli(t, dir, "giveaways")
		if !strings.Contains(out, "3 entries") {
			return out
		}
		return ""
	})
	eventually(t, "you're in", said("you're in a Steam key! (3 entries so far)"))

	out, errOut, code := cli(t, dir, "giveaways", "draw")
	if code != 0 || !strings.Contains(out, "number") || !strings.Contains(out, "of the 3 not yet drawn") {
		t.Fatal(out, errOut)
	}
	winner := strings.ToLower(regexp.MustCompile(`Draw 1: (\w+)`).FindStringSubmatch(out)[1])
	eventually(t, "the winner", said("@"+winner+" wins a Steam key! Type !claim within 60 seconds"))
	if _, errOut, code := cli(t, dir, "giveaways", "draw"); code == 0 || !strings.Contains(errOut, "all 1 winner are drawn") {
		t.Fatalf("a second winner: %d %s", code, errOut)
	}
	if _, errOut, code := cli(t, dir, "giveaways", "end"); code == 0 || !strings.Contains(errOut, "still has time to claim") {
		t.Fatalf("end during the claim: %d %s", code, errOut)
	}
	tw.Say("streamer", winner, "!claim")
	eventually(t, "the claim", said("@"+winner+" claimed a Steam key!"))
	if out, errOut, code := cli(t, dir, "giveaways", "end"); code != 0 || !strings.Contains(out, "ended") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the congratulations", said("a Steam key is over. Congratulations to @"+winner))
	list, _, _ := cli(t, dir, "giveaways")
	id := regexp.MustCompile(`a Steam key \(ended, 3 entries\)\s+(\S+)`).FindStringSubmatch(list)
	if id == nil {
		t.Fatal(list)
	}
	if out, _, code := cli(t, dir, "giveaways", "verify", id[1]); code != 0 || !strings.Contains(out, "✓ The entry list matches its fingerprint") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "giveaways", "show", id[1], "-entries"); !strings.Contains(out, "1\ttwitch:") || strings.Contains(out, "Streamer") {
		t.Fatalf("show:\n%s", out)
	}

	// Practice: test chat enters, nothing reaches Twitch chat.
	before := len(tw.ChatLog())
	if out, errOut, code := cli(t, dir, "giveaways", "open", "-practice", "practice", "run"); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "events", "simulate", "chat.command.received", "user=Tester", "command=!join")
	eventually(t, "the test entry", func() string {
		out, _, _ := cli(t, dir, "giveaways")
		if !strings.Contains(out, "1 entry") {
			return out
		}
		return ""
	})
	if out, errOut, code := cli(t, dir, "giveaways", "draw"); code != 0 || !strings.Contains(out, "Tester") {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "giveaways", "cancel")
	if n := len(tw.ChatLog()); n != before {
		t.Fatalf("practice sent %d messages", n-before)
	}
}
