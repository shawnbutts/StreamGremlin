//go:build acceptance

package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

const rollOnlyWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.throw","name":"Throw",
	"triggers":[{"type":"chat.command.received","where":{"command":"throw"}}],
	"steps":[{"id":"roll","action":"dice-tray.roll","with":{"dice":"2"}}]}`

// Updating and rolling back a plugin through the CLI (D-072): the check
// names the step it drops and the workflow using it; the update needs no
// -allow when it asks for nothing new; the workflow using the dropped step
// is turned off, the plugin starts again as the new version and answers
// chat; rolling back brings the step back.
func TestLuaPluginUpdatesThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	})
	v1 := filepath.Join(t.TempDir(), "dice-tray.zip")
	zipFolder(t, filepath.Join("..", "..", "examples", "plugins", "dice-tray"), v1)
	if out, errOut, code := cli(t, dir, "packages", "install", v1, "-yes", "-allow", "chat.send,events.publish,points.rounds"); code != 0 {
		t.Fatal(out, errOut)
	}
	files := t.TempDir()
	w, _, _ := cli(t, dir, "templates", "show", "dice-tray/roll")
	for _, def := range []string{w, rollOnlyWorkflow} {
		if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "w.json", def)); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	cli(t, dir, "workflows", "on", "chat.dice-tray-roll")
	cli(t, dir, "workflows", "on", "chat.throw")
	cli(t, dir, "packages", "on", "dice-tray")

	// The test version drops "Roll dice" and allows at most 12 sides.
	v2 := filepath.Join(t.TempDir(), "dice-tray-2.0.0.zip")
	zipFolder(t, filepath.Join("..", "testdata", "dice-tray-update"), v2)
	if out, errOut, code := cli(t, dir, "packages", "set", "dice-tray", "sides=20"); code != 0 {
		t.Fatal(out, errOut)
	}
	out, _, _ := cli(t, dir, "packages", "install", v2)
	for _, want := range []string{"An update: 1.4.0 is installed", "It asks for the permissions you allowed already: chat.send, events.publish, points.rounds.",
		"Steps it doesn't have: Roll dice (dice-tray.roll). These workflows use them and are turned off (not deleted): Throw.",
		"Settings that go back to their default: Sides on each die.",
		"Run again with -yes to install it."} {
		if !strings.Contains(out, want) {
			t.Fatalf("check has no %q:\n%s", want, out)
		}
	}
	out, errOut, code := cli(t, dir, "packages", "install", v2, "-yes")
	if code != 0 || !strings.Contains(out, "Updated Dice tray from 1.4.0 to 2.0.0. These workflows used a step it doesn't have and are turned off (not deleted): Throw.") ||
		!strings.Contains(out, "Back to the default: Sides on each die.") || !strings.Contains(out, "It started again, fresh, as 2.0.0.") {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "workflows"); !strings.Contains(out, "chat.throw") || strings.Contains(out, "chat.throw  on") {
		t.Fatalf("workflows:\n%s", out)
	}
	tw.Say("streamer", "Fan", "!roll")
	eventually(t, "a roll from 2.0.0", func() string {
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, "Fan rolled a ") && strings.Contains(m.Text, "(d6)") {
				return ""
			}
		}
		return "no roll"
	})
	if out, _, _ := cli(t, dir, "packages"); !strings.Contains(out, "Dice tray 2.0.0") || !strings.Contains(out, "can roll back to 1.4.0") {
		t.Fatalf("list:\n%s", out)
	}

	// Roll back: "Roll dice" is there again; nothing else is turned off.
	out, _, _ = cli(t, dir, "packages", "rollback", "dice-tray")
	if !strings.Contains(out, "Rolling Dice tray back from 2.0.0 to 1.4.0") || !strings.Contains(out, "Run again with -yes to roll back.") {
		t.Fatalf("rollback check:\n%s", out)
	}
	if out, errOut, code := cli(t, dir, "packages", "rollback", "dice-tray", "-yes"); code != 0 ||
		!strings.Contains(out, "Rolled Dice tray back from 2.0.0 to 1.4.0. It started again, fresh, as 1.4.0.") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "workflows", "on", "chat.throw"); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "packages", "activity", "dice-tray"); !strings.Contains(out, "Dice tray 2.0.0 ready: d6") {
		t.Errorf("activity:\n%s", out)
	}
	out, _, _ = cli(t, dir, "packages", "history")
	for _, want := range []string{"update   dice-tray", "rollback dice-tray"} {
		if !strings.Contains(out, want) {
			t.Errorf("history has no %q:\n%s", want, out)
		}
	}
}
