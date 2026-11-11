//go:build acceptance

package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const roundWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.quick-round","name":"Quick round",
	"triggers":[{"type":"chat.command.received","where":{"command":"quickround"}}],
	"steps":[{"id":"open","action":"dice-tray.open_round","with":{"seconds":"10"}}]}`

// A plugin's timer and events through the real engine (D-073): a round
// opened from chat closes on its timer, says who won, and its event starts
// the champion workflow from Dice tray's own template.
func TestLuaPluginEventsAndTimers(t *testing.T) {
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
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes", "-allow", "chat.send,events.publish,points.rounds"); code != 0 {
		t.Fatal(out, errOut)
	}
	files := t.TempDir()
	defs := []string{roundWorkflow}
	for _, id := range []string{"dice-tray/roll", "dice-tray/champion"} {
		w, errOut, code := cli(t, dir, "templates", "show", id)
		if code != 0 {
			t.Fatal(errOut)
		}
		defs = append(defs, w)
	}
	for _, def := range defs {
		if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "w.json", def)); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	for _, id := range []string{"chat.quick-round", "chat.dice-tray-roll", "plugin.dice-tray-champion"} {
		if out, errOut, code := cli(t, dir, "workflows", "on", id); code != 0 {
			t.Fatal(id, out, errOut)
		}
	}
	cli(t, dir, "packages", "on", "dice-tray")

	opened := time.Now()
	tw.Say("streamer", "Streamer", "!quickround")
	eventually(t, "the round opening", said("A dice round is open for 10 seconds"))
	tw.Say("streamer", "Fan", "!roll 3")
	eventually(t, "Fan's roll", said("Fan rolled "))
	eventually(t, "the round closing", said("The dice round is over: Fan wins with "))
	if took := time.Since(opened); took < 9*time.Second {
		t.Errorf("the round closed after %v", took)
	}
	// Its event started the champion workflow.
	eventually(t, "the champion", said("Congratulations Fan, dice champion with "))
	out, _, _ := cli(t, dir, "events", "-types", "plugin.dice_tray.round_ended")
	if !strings.Contains(out, "plugin.dice_tray.round_ended") || !strings.Contains(out, "plugin:dice-tray") {
		t.Errorf("events:\n%s", out)
	}
	out, _, _ = cli(t, dir, "packages", "activity", "dice-tray")
	if !strings.Contains(out, "events.publish ok      round_ended") {
		t.Errorf("activity:\n%s", out)
	}
	// Removing it turns off the workflow its event starts.
	if out, errOut, code := cli(t, dir, "packages", "remove", "dice-tray", "-yes"); code != 0 || !strings.Contains(out, "Dice round winner (Dice tray)") {
		t.Fatal(out, errOut)
	}
}
