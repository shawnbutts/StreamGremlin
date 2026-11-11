//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// The Live screen's viewer card through the real engine, CLI and
// mocktwitch (D-078): a chatter who follows, subscribes by gift, is a VIP
// and is timed out; a refused fact shows as unknown with why, never "no";
// and their recent chat.
func TestViewerCardThroughTheCLI(t *testing.T) {
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
	tw.SetFollower("streamer", "ann", time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC))
	tw.SetSubMonths("streamer", "ann", 2)
	tw.SetSubGift("streamer", "ann", "bob")
	tw.SetVIP("streamer", "ann", true)
	tw.SetBanned("streamer", "ann", true, time.Now().Add(time.Hour), "calm down", "streamer")
	tw.Say("streamer", "ann", "hello from ann")
	eventually(t, "ann recorded", func() string {
		out, _, _ := cli(t, dir, "viewers", "ann")
		if !strings.Contains(out, "ann") {
			return out
		}
		return ""
	})
	out, errOut, code := cli(t, dir, "viewers", "card", "@ann")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	for _, want := range []string{"Follows:    since Mar 1 2024", "Subscribed: tier 1, a gift from bob", "VIP:        yes",
		"Moderator:  no", "Banned:     timed out until", "calm down", "hello from ann"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in:\n%s", want, out)
		}
	}
	tw.FailCard("/helix/channels/vips", 401)
	out, _, _ = cli(t, dir, "viewers", "card", "@ann")
	if !strings.Contains(out, "VIP:        unknown: Sign in to Twitch again") {
		t.Fatalf("a refused fact:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "viewers", "card", "@nobody"); code == 0 || !strings.Contains(errOut, "hasn't seen") {
		t.Fatal(code, errOut)
	}
}
