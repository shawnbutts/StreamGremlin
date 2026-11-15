//go:build acceptance

package acceptance

import (
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
)

// Twitch channel events through the real engine with mock Twitch: each kind
// asked for and listed, the templates answering follows, gifts (anonymous
// too) and raids, a redemption limited to one reward, a redelivered event
// answered once, and quotes taking the channel's category.
func TestTwitchChannelEvents(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	tw.UpdateChannel("streamer", "Speedrun night", "Celeste")
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat and events", func() string {
		out, _, _ := cli(t, dir, "twitch")
		if !strings.Contains(out, "Chat:       connected") || !strings.Contains(out, "✓ Channel point redemptions") ||
			!strings.Contains(out, `Channel:    "Speedrun night", in Celeste`) {
			return "not yet: " + out
		}
		return ""
	})
	for _, id := range []string{"thank-follow", "thank-gift", "welcome-raid", "quoteadd"} {
		out, _, _ := cli(t, dir, "templates", "show", id)
		f := writeFile(t, t.TempDir(), id+".json", out)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	hydrate := writeFile(t, t.TempDir(), "hydrate.json", `{"schema":"streamgremlin.workflow/v1","id":"points.hydrate","name":"Hydrate",
		"triggers":[{"type":"rewards.channel_points.redeemed","where":{"reward.title":"Hydrate"}}],
		"steps":[{"action":"chat.send","with":{"message":"${event.actor.display_name} says drink water! (${event.payload.reward.cost} points)"}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", hydrate); code != 0 {
		t.Fatal(out, errOut)
	}
	for _, id := range []string{"twitch.thank-follow", "twitch.thank-gift", "twitch.welcome-raid", "chat.quoteadd", "points.hydrate"} {
		if out, errOut, code := cli(t, dir, "workflows", "on", id); code != 0 {
			t.Fatal(out, errOut)
		}
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
	count := func(text string) int {
		n := 0
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, text) {
				n++
			}
		}
		return n
	}

	id := tw.Follow("streamer", "NewFan")
	eventually(t, "thanks for the follow", said("Thanks for the follow, NewFan!"))
	tw.Notify("streamer", "channel.follow", map[string]any{"user_id": tw.UserID("newfan"), "user_login": "newfan", "user_name": "NewFan",
		"followed_at": "2026-11-08T20:00:00Z"}, id) // Twitch delivers it again
	tw.Gift("streamer", "Giver", "1000", 5)
	tw.Gift("streamer", "", "1000", 3)
	eventually(t, "a gift", said("Giver just gifted 5 subs!"))
	eventually(t, "an anonymous gift", said("Anonymous just gifted 3 subs!"))
	tw.Raid("streamer", "FriendlyStreamer", 42)
	eventually(t, "the raid", said("FriendlyStreamer brought 42 friends. Go give them a follow: twitch.tv/friendlystreamer"))
	tw.Redeem("streamer", "Thirsty", "Something else", 50, "")
	tw.Redeem("streamer", "Thirsty", "Hydrate", 300, "")
	eventually(t, "the redemption", said("Thirsty says drink water! (300 points)"))
	if count("Thanks for the follow") != 1 || count("drink water") != 1 {
		t.Fatalf("answered twice, or for the wrong reward: %+v", tw.ChatLog())
	}
	out, _, _ := cli(t, dir, "events", "-types", "audience.*")
	if strings.Count(out, "audience.follow.created") != 1 || !strings.Contains(out, "audience.raid.received") {
		t.Fatalf("events:\n%s", out)
	}

	// A quote takes what you're streaming.
	tw.Say("streamer", "streamer", "!quoteadd that jump was easy")
	eventually(t, "the quote", said("Added quote #1"))
	if out, _, _ := cli(t, dir, "quotes"); !strings.Contains(out, "#1 that jump was easy (added by streamer, Celeste,") {
		t.Fatalf("quotes:\n%s", out)
	}
}

// Hype Trains through the real engine and mocktwitch: started, moving
// and ended reach Events in words, and the template thanks chat at the end.
func TestHypeTrainsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "the Hype Train feed", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "✓ Hype Train ends") {
			return "not yet: " + out
		}
		return ""
	})
	out, _, _ := cli(t, dir, "templates", "show", "thank-hype-train")
	f := writeFile(t, t.TempDir(), "hype.json", out)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "twitch.thank-hype-train")
	top := mocktwitch.HypeContribution{Login: "BitsFan", Type: "bits", Total: 900}
	tw.HypeTrain("streamer", "begin", "ht1", 1, 900, 900, 1800, top)
	tw.HypeTrain("streamer", "progress", "ht1", 2, 2100, 300, 2000, top)
	tw.HypeTrain("streamer", "end", "ht1", 2, 2400, 0, 0, top)
	eventually(t, "the thanks", func() string {
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, "level 2") {
				return ""
			}
		}
		return "no thanks yet"
	})
	if out, _, _ := cli(t, dir, "events", "-types", "monetization.hype_train.*"); !strings.Contains(out, "monetization.hype_train.started") ||
		!strings.Contains(out, "monetization.hype_train.updated") || !strings.Contains(out, "monetization.hype_train.ended") {
		t.Fatalf("events:\n%s", out)
	}
}
