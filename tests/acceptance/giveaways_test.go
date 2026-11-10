//go:build acceptance

package acceptance

import (
	"regexp"
	"strings"
	"testing"
	"time"
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

// Entry rules through the real engine, CLI and mocktwitch: entering by
// chatting, followers of 7 days with accounts 30 days old, checked with
// Twitch's Get Channel Followers and Get Users; people turned away hear why
// in chat, and again only when the reason changes. A practice giveaway's
// rules read the test chatter's facts.
func TestGiveawayEntryRules(t *testing.T) {
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
	now := time.Now()
	tw.SetFollower("streamer", "ann", now.AddDate(0, 0, -30))
	tw.SetFollower("streamer", "cat", now.AddDate(0, 0, -2))
	tw.SetFollower("streamer", "dan", now.AddDate(0, 0, -30))
	tw.SetAccountCreated("dan", now.AddDate(0, 0, -5))

	if out, errOut, code := cli(t, dir, "giveaways", "open", "-chat", "-follow-days", "7", "-account-days", "30", "a", "mug"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the opening", said("Say anything in chat (or type !join) to enter. Open to followers of 7 days or more; "+
		"Twitch accounts at least 30 days old."))
	for _, v := range []string{"ann", "bob", "cat", "dan"} {
		tw.Say("streamer", v, "hello!")
	}
	eventually(t, "ann in", said("@ann: you're in a mug! (1 entry so far)"))
	eventually(t, "bob", said("@bob: a mug is for followers. Follow, then type !join."))
	eventually(t, "cat", said("@cat: a mug is for followers of 7 days or more."))
	eventually(t, "dan", said("@dan: a mug is for Twitch accounts at least 30 days old."))
	// bob follows now: the reason changes, and chat hears the new one.
	tw.Follow("streamer", "bob")
	tw.Say("streamer", "bob", "!join")
	eventually(t, "bob again", said("@bob: a mug is for followers of 7 days or more."))
	out, _, _ := cli(t, dir, "giveaways")
	if !strings.Contains(out, "Open to followers of 7 days or more") || !strings.Contains(out, "Turned away: 3 viewers") {
		t.Fatalf("giveaways:\n%s", out)
	}
	cli(t, dir, "giveaways", "cancel")

	// Practice: the test chatter's facts, never Twitch.
	calls := tw.FollowerCalls()
	if out, errOut, code := cli(t, dir, "giveaways", "open", "-practice", "-followers", "practice"); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "events", "simulate", "chat.command.received", "user=Fan", "command=!join", "follow_days=10")
	cli(t, dir, "events", "simulate", "chat.command.received", "user=Stranger", "command=!join", "follow_days=-1")
	eventually(t, "the practice entries", func() string {
		out, _, _ := cli(t, dir, "giveaways")
		if !strings.Contains(out, "1 entry") || !strings.Contains(out, "Turned away: 1 viewer") {
			return out
		}
		return ""
	})
	if tw.FollowerCalls() != calls {
		t.Fatal("a practice giveaway asked Twitch")
	}
	cli(t, dir, "giveaways", "cancel")
}

// Extra chances through the real engine, CLI and mocktwitch: subscribers
// get 3 chances, chat says so (never "the same chance"), a subscriber's
// "you're in" names their chances, draws pick tickets, and the record
// checks out with "giveaways verify".
func TestGiveawayChances(t *testing.T) {
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
	tw.SetSubMonths("streamer", "ann", 4)
	if out, errOut, code := cli(t, dir, "giveaways", "open", "-sub-chances", "3", "-winners", "2", "a", "hoodie"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the opening", said("One entry each, and subscribers get 3 chances to everyone else's 1; 2 winners will be drawn."))
	for _, v := range []string{"ann", "bob", "cat"} {
		tw.Say("streamer", v, "!join")
	}
	eventually(t, "ann's chances", said("@ann (3 chances)"))
	out, errOut, code := cli(t, dir, "giveaways", "draw")
	if code != 0 || !strings.Contains(out, "of the 5 held by those not yet drawn") {
		t.Fatal(out, errOut)
	}
	list, _, _ := cli(t, dir, "giveaways")
	if !strings.Contains(list, "Chances: subscribers get 3 chances to everyone else's 1.") {
		t.Fatal(list)
	}
	cli(t, dir, "giveaways", "end")
	list, _, _ = cli(t, dir, "giveaways")
	id := regexp.MustCompile(`a hoodie \(ended, 3 entries\)\s+(\S+)`).FindStringSubmatch(list)
	if id == nil {
		t.Fatal(list)
	}
	if out, _, code := cli(t, dir, "giveaways", "verify", id[1]); code != 0 || !strings.Contains(out, "picked a ticket") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "giveaways", "show", id[1], "-entries"); !strings.Contains(out, "\t3\tann") {
		t.Fatalf("show:\n%s", out)
	}
}

// Drawing from recent chat through the real engine, CLI and mocktwitch:
// everyone who chatted enters at once (not the streamer, once each), a
// follower rule turns someone away without telling chat, winners are
// drawn, and the record checks out.
func TestGiveawayFromRecentChat(t *testing.T) {
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
	tw.SetFollower("streamer", "ann", time.Now().AddDate(-1, 0, 0))
	tw.SetFollower("streamer", "cat", time.Now().AddDate(-1, 0, 0))
	tw.Say("streamer", "ann", "hi!")
	tw.Say("streamer", "bob", "hello")
	tw.Say("streamer", "cat", "!lurk")
	tw.Say("streamer", "ann", "again")
	tw.Say("streamer", "streamer", "thanks for coming")
	eventually(t, "chat stored", func() string {
		out, _, _ := cli(t, dir, "events")
		if strings.Count(out, "chat.message.received") < 5 {
			return out
		}
		return ""
	})
	out, errOut, code := cli(t, dir, "giveaways", "open", "-from-chat", "10", "-followers", "-winners", "2", "a", "badge")
	if code != 0 || !strings.Contains(out, "Drawn from chat of the last 10 minutes.") || !strings.Contains(out, "(closed): from chat of the last 10 minutes, 2 winners, 2 entries") ||
		!strings.Contains(out, "Turned away: 1 viewer") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the announcement", said("🎲 Drawing a badge now from everyone who chatted in the last 10 minutes. Open to followers."))
	eventually(t, "the winners", said("2 entries from chat for a badge. Drawing!"))
	for _, m := range tw.ChatLog() {
		if strings.Contains(m.Text, "@bob") || strings.Contains(m.Text, "you're in") {
			t.Fatal("told someone who didn't ask:", m.Text)
		}
	}
	cli(t, dir, "giveaways", "end")
	list, _, _ := cli(t, dir, "giveaways")
	id := regexp.MustCompile(`a badge \(ended, 2 entries\)\s+(\S+)`).FindStringSubmatch(list)
	if id == nil {
		t.Fatal(list)
	}
	if out, _, code := cli(t, dir, "giveaways", "verify", id[1]); code != 0 || !strings.Contains(out, "✓") {
		t.Fatal(out)
	}
}
