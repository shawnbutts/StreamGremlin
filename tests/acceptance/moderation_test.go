//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Moderating through the real engine, CLI and mocktwitch (D-079): a
// timeout, a ban only with -yes, an unban, a warning, a VIP, Twitch's
// refusal in its words, the chat modes, a suspicious user's flag on their
// card, and the history of it all.
func TestModerationThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "listening for suspicious users", func() string {
		if tw.SubscribedTo("streamer", "channel.suspicious_user.message") == 0 {
			return "not yet"
		}
		return ""
	})
	tw.Say("streamer", "ann", "hello")
	eventually(t, "ann recorded", func() string {
		if out, _, _ := cli(t, dir, "viewers", "ann"); !strings.Contains(out, "ann") {
			return "not yet: " + out
		}
		return ""
	})
	ok := func(want string, args ...string) {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
	}
	ok("Timed out ann for 10m0s.", "mod", "timeout", "@ann", "10m", "-reason", "calm down")
	if !tw.IsBanned("streamer", "ann") {
		t.Fatal("not timed out")
	}
	ok("Add -yes to ban", "mod", "ban", "@ann")
	ok("Banned ann.", "mod", "ban", "@ann", "-yes")
	ok("Unbanned ann.", "mod", "unban", "@ann")
	if tw.IsBanned("streamer", "ann") {
		t.Fatal("still banned")
	}
	ok("Warned ann.", "mod", "warn", "@ann", "be", "nice")
	if tw.Warned("streamer", "ann") != "be nice" {
		t.Fatal("not warned")
	}
	ok("ann is a VIP.", "mod", "vip", "@ann")
	if _, errOut, code := cli(t, dir, "mod", "vip", "@ann"); code == 0 || !strings.Contains(errOut, "already a VIP") {
		t.Fatal("refusal:", code, errOut)
	}
	ok("Chat modes on: slow (10 seconds).", "mod", "modes", "slow=10")
	ok("slow (10 seconds)", "mod", "modes")

	tw.SuspiciousSay("streamer", "ann", "it's me again", "active_monitoring", "likely", "ban_evader")
	eventually(t, "the flag on the card", func() string {
		if out, _, _ := cli(t, dir, "viewers", "card", "@ann"); !strings.Contains(out, "Suspicious: likely ban evader, monitored") {
			return "not yet: " + out
		}
		return ""
	})
	ok("Twitch restricts ann", "mod", "restrict", "@ann")

	out, _, _ := cli(t, dir, "mod", "history", "@ann")
	for _, want := range []string{"timeout", "ban", "unban", "warn", "vip", "refused", "restrict"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %s in history:\n%s", want, out)
		}
	}
	if out, _, _ := cli(t, dir, "mod", "history"); !strings.Contains(out, "chat_modes") {
		t.Fatalf("no chat modes in history:\n%s", out)
	}
}
