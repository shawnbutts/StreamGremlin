//go:build acceptance

package acceptance

import (
	"regexp"
	"strings"
	"testing"
)

// Viewers and points through the real engine, CLI and mocktwitch: a viewer
// recorded from chat; points given and taken from the CLI, never below
// zero; !points answered in chat; undone once; renamed; the audit; the
// export; and forgetting, with the ledger still adding up.
func TestPointsThroughTheCLIAndChat(t *testing.T) {
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
	tw.Say("streamer", "ann", "hello!")
	eventually(t, "ann recorded", func() string {
		out, _, _ := cli(t, dir, "viewers", "an")
		if !strings.Contains(out, "ann") {
			return out
		}
		return ""
	})
	out, errOut, code := cli(t, dir, "points", "give", "@ann", "50", "-reason", "welcome")
	if code != 0 || !strings.Contains(out, "now has 50") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "points", "take", "@ann", "80"); code == 0 || !strings.Contains(errOut, "not enough points: has 50, needs 80") {
		t.Fatal("took too many:", code, errOut)
	}
	out, _, _ = cli(t, dir, "points", "take", "@ann", "20", "-reason", "oops")
	tx := regexp.MustCompile(`\((tx_\w+)\)`).FindStringSubmatch(out)[1]
	if out, errOut, code := cli(t, dir, "points", "undo", tx); code != 0 || !strings.Contains(out, "now have 50") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "points", "undo", tx); code == 0 || !strings.Contains(errOut, "already undone") {
		t.Fatal("undone twice:", code, errOut)
	}

	// !points in chat.
	wf := writeFile(t, t.TempDir(), "points.json", `{"schema":"streamgremlin.workflow/v1","id":"chat.points","name":"Check your points",
		"triggers":[{"type":"chat.command.received","where":{"command":"points"}}],
		"steps":[{"id":"p","action":"points.balance","with":{}},
			{"action":"chat.send","with":{"message":"@${event.actor.display_name}, you have ${steps.p.output.balance} ${steps.p.output.currency}."}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", wf); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.points")
	if out, errOut, code := cli(t, dir, "points", "rename", "Gold"); code != 0 || !strings.Contains(out, "now called Gold") {
		t.Fatal(out, errOut)
	}
	tw.Say("streamer", "ann", "!points")
	eventually(t, "the reply", func() string {
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, "you have 50 Gold") {
				return ""
			}
		}
		return "no reply yet"
	})
	if out, _, code := cli(t, dir, "points", "audit"); code != 0 || !strings.Contains(out, "✓") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "points"); !strings.Contains(out, "1 viewer holds 50 Gold") {
		t.Fatal(out)
	}
	if out, _, code := cli(t, dir, "viewers", "export", "@ann"); code != 0 || !strings.Contains(out, `"schema": "streamgremlin.viewer-export/v1"`) ||
		!strings.Contains(out, `"memo": "welcome"`) {
		t.Fatal(out)
	}
	if out, errOut, code := cli(t, dir, "viewers", "forget", "@ann"); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "viewers", "an"); strings.Contains(out, "ann") {
		t.Fatal("a forgotten viewer is listed:", out)
	}
	if out, _, code := cli(t, dir, "points", "audit"); code != 0 {
		t.Fatal("forgetting broke the ledger:", out)
	}
}
