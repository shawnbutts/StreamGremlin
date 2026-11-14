//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// M8 slice 1 (D-105) through the real engine, CLI and mocktwitch: Shield
// Mode on and off, a lock and an unlock that puts back only what it
// changed, Shield Mode turned on from Twitch's site arriving as an event,
// and the "Shield Mode on when I go offline" template.
func TestShieldModeAndLockThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "listening for Shield Mode", func() string {
		if tw.SubscribedTo("streamer", "channel.shield_mode.begin") == 0 {
			return "not yet"
		}
		return ""
	})
	ok := func(want string, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
		return out
	}
	ok("Shield Mode: off.", "mod", "modes")
	ok("Shield Mode is on.", "mod", "shield", "on")
	if !tw.ShieldActive("streamer") {
		t.Fatal("not on")
	}
	ok("Shield Mode is off.", "mod", "shield", "off")

	// Slow mode was on before: the lock leaves it, the unlock keeps it.
	ok("slow (10 seconds)", "mod", "modes", "slow=10")
	ok("Locked: Shield Mode, emote-only and followers-only (followed 10 minutes) on.", "mod", "lock")
	out := ok("Locked from StreamGremlin since", "mod", "modes")
	if !strings.Contains(out, "emote-only") || !strings.Contains(out, "followers-only (followed 10 minutes)") || !strings.Contains(out, "Shield Mode: on.") {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "mod", "lock"); code == 0 || !strings.Contains(errOut, "locked already") {
		t.Fatal("locked twice:", code, errOut)
	}
	ok("Unlocked: chat is back as it was before the lock.", "mod", "unlock")
	out = ok("Chat modes on: slow (10 seconds).", "mod", "modes")
	if !strings.Contains(out, "Shield Mode: off.") || strings.Contains(out, "Locked") {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "mod", "unlock"); code == 0 || !strings.Contains(errOut, "isn't locked") {
		t.Fatal("unlocked twice:", code, errOut)
	}
	ok("lock", "mod", "history")

	// From Twitch's site: an event.
	tw.SetShieldMode("streamer", "AMod", true)
	eventually(t, "the Shield Mode event", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "moderation.shield_mode.changed")
		if !strings.Contains(out, "moderation.shield_mode.changed") {
			return out
		}
		return ""
	})
	tw.SetShieldMode("streamer", "AMod", false)

	// The template: Shield Mode on when you go offline.
	tmpl, _, _ := cli(t, dir, "templates", "show", "shield-offline")
	if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, t.TempDir(), "w.json", tmpl)); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "moderation.shield-offline")
	tw.GoOffline("streamer")
	eventually(t, "Shield Mode on after going offline", func() string {
		if !tw.ShieldActive("streamer") {
			return "still off"
		}
		return ""
	})
}
