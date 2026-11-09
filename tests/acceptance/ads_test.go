//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Ads through the real engine, CLI and mocktwitch: a break run from the CLI
// is announced in chat only once Twitch says it began, and its estimated
// end is announced once; "!ad" inside Twitch's gap is refused, fails its
// run, and announces nothing; a viewer's "!ad" asks Twitch for nothing.
func TestAdsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "the ad feed", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "✓ Ad breaks") {
			return out
		}
		return ""
	})
	for _, id := range []string{"ad-break-chat", "ad"} {
		out, _, _ := cli(t, dir, "templates", "show", id)
		f := writeFile(t, t.TempDir(), id+".json", out)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	cli(t, dir, "workflows", "on", "twitch.ad-break-chat")
	cli(t, dir, "workflows", "on", "chat.ad")
	said := func(text string) int {
		n := 0
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, text) {
				n++
			}
		}
		return n
	}

	// Offline: Twitch refuses, and chat hears nothing.
	if _, errOut, code := cli(t, dir, "ads", "run", "2"); code == 0 || !strings.Contains(errOut, "live") {
		t.Fatalf("offline: %d %s", code, errOut)
	}
	tw.GoLive("streamer")
	if out, errOut, code := cli(t, dir, "ads", "run", "2"); code != 0 || !strings.Contains(out, "accepted") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the break announced", func() string {
		if said("2-second ad break is starting") != 1 {
			return "not announced"
		}
		return ""
	})
	eventually(t, "the estimated end", func() string {
		if said("should be over now") != 1 {
			return "no end"
		}
		return ""
	})
	if out, _, _ := cli(t, dir, "ads"); !strings.Contains(out, "(you ran it)") || !strings.Contains(out, "(estimated)") ||
		!strings.Contains(out, "Snoozes left: 3") {
		t.Fatalf("ads:\n%s", out)
	}

	// Inside Twitch's 8-minute gap: refused here, the run fails, nothing said.
	tw.Say("streamer", "streamer", "!ad")
	eventually(t, "the refused !ad", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.ad"); !strings.Contains(out, "failed") {
			return out
		}
		return ""
	})
	tw.Say("streamer", "Viewer", "!ad")
	if _, errOut, code := cli(t, dir, "ads", "run", "30"); code == 0 || !strings.Contains(errOut, "break between ads") {
		t.Fatalf("cooldown: %d %s", code, errOut)
	}
	if n := tw.Commercials(); n != 2 {
		t.Fatal("asked Twitch", n, "times; want 2 (the offline one and the accepted one)")
	}
	if said("ad break is starting") != 1 {
		t.Fatal("a refused request was announced")
	}
	if out, errOut, code := cli(t, dir, "ads", "snooze"); code != 0 || !strings.Contains(out, "Snoozes left: 2") {
		t.Fatal(out, errOut)
	}
}
