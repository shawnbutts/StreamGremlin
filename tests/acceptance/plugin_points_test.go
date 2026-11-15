//go:build acceptance

package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// Points for plugins through the real engine (ADR-0039): a viewer gambles
// from chat; Dice tray's round takes the stake, StreamGremlin rolls and
// records it, pays at most twice, and the ledger balances; a stake beyond
// their points is refused in chat.
func TestLuaPluginGamblesPoints(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return "not yet: " + out
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
			return "no " + text
		}
	}
	zipPath := filepath.Join(t.TempDir(), "dice-tray.zip")
	zipFolder(t, filepath.Join("..", "..", "examples", "plugins", "dice-tray"), zipPath)
	out, _, _ := cli(t, dir, "packages", "install", zipPath)
	if !strings.Contains(out, "points.rounds  Hold viewers' points in its rounds and pay them out: up to 1,000 points from a viewer a round, "+
		"paying back at most 2× their stake, for up to 30 seconds. Its odds are its own: StreamGremlin can't check them") {
		t.Fatalf("check:\n%s", out)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes", "-allow", "chat.send,events.publish,points.rounds"); code != 0 {
		t.Fatal(out, errOut)
	}
	w, _, _ := cli(t, dir, "templates", "show", "dice-tray/gamble")
	if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, t.TempDir(), "w.json", w)); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.dice-tray-gamble")
	cli(t, dir, "packages", "on", "dice-tray")

	// Fan is seen in chat, then given 100 points.
	tw.Say("streamer", "Fan", "hello")
	eventually(t, "Fan's points", func() string {
		if out, errOut, code := cli(t, dir, "points", "give", "@fan", "100"); code != 0 {
			return out + errOut
		}
		return ""
	})
	tw.Say("streamer", "Fan", "!gamble 50")
	eventually(t, "the gamble", func() string {
		for _, m := range tw.ChatLog() {
			if strings.HasPrefix(m.Text, "Fan rolled ") && (strings.Contains(m.Text, "wins 100 points! (now 150)") ||
				strings.Contains(m.Text, "loses 50 points. (now 50;")) {
				return ""
			}
		}
		return "no result"
	})
	tw.Say("streamer", "Fan", "!gamble 1000")
	eventually(t, "the refusal", said("@Fan no gamble: Fan has "))

	out, _, _ = cli(t, dir, "packages", "rounds", "dice-tray")
	for _, want := range []string{"Gamble  settled", "Gamble  refunded (no stake)",
		"Rules, as the plugin states them: Roll 1 to 100: 60 or more pays back twice the stake; less loses it.",
		"Stakes: Fan 50", "Rolls, made and recorded by StreamGremlin: Fan's roll: "} {
		if !strings.Contains(out, want) {
			t.Errorf("rounds have no %q:\n%s", want, out)
		}
	}
	if out, errOut, code := cli(t, dir, "points", "audit"); code != 0 {
		t.Errorf("audit: %s%s", out, errOut)
	}
}
