//go:build acceptance

package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// A Lua plugin through the real engine, CLI and a stand-in Twitch
// (docs/adr/0038-lua-plugins.md): installed only with its permissions,
// off until turned on, answering chat through its own steps and sg.chat,
// keeping its state, every call in its activity, settings restarting it,
// on again after a restart, and removed with its workflows turned off.
func TestLuaPluginThroughChatAndTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
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
	out, _, code := cli(t, dir, "packages", "install", zipPath)
	if code != 0 || !strings.Contains(out, "a Lua plugin, published by StreamGremlin examples (not verified)") ||
		!strings.Contains(out, "dice-tray.roll_and_say") ||
		!strings.Contains(out, "Templates it adds to the gallery: !best: the session's best roll, Cheer a dice round's winner, !gamble: gamble points on a roll, !roll: roll dice in chat, !diceround: a timed dice round.") ||
		!strings.Contains(out, "chat.send  Send messages in your chat") || !strings.Contains(out, "plugin.dice_tray.round_ended  A dice round ends:") ||
		!strings.Contains(out, "-yes -allow chat.send,events.publish,points.rounds") {
		t.Fatalf("check:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes"); code != 1 || !strings.Contains(errOut, "-allow chat.send,events.publish,points.rounds") {
		t.Fatalf("without -allow: %d %s", code, errOut)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes", "-allow", "chat.send,events.publish,points.rounds"); code != 0 || !strings.Contains(out, "Installed Dice tray 1.4.0, off") {
		t.Fatalf("install: %d %s%s", code, out, errOut)
	}

	// Its templates are in the gallery: the workflows come from them.
	out, _, _ = cli(t, dir, "templates")
	if !strings.Contains(out, "dice-tray/roll Dice tray (add-on) !roll: roll dice in chat") || !strings.Contains(out, "[Dice tray is off") {
		t.Fatalf("templates:\n%s", out)
	}
	files := t.TempDir()
	for _, id := range []string{"dice-tray/roll", "dice-tray/best"} {
		w, errOut, code := cli(t, dir, "templates", "show", id)
		if code != 0 {
			t.Fatal(errOut)
		}
		if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "w.json", w)); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	cli(t, dir, "workflows", "on", "chat.dice-tray-roll")
	cli(t, dir, "workflows", "on", "chat.dice-tray-best")

	// Off: the step fails, saying so.
	tw.Say("streamer", "Fan", "!roll 2")
	eventually(t, "the failed run", func() string {
		out, _, _ := cli(t, dir, "runs", "-workflow", "chat.dice-tray-roll")
		if !strings.Contains(out, "failed") {
			return out
		}
		return ""
	})
	out, _, _ = cli(t, dir, "runs", "-workflow", "chat.dice-tray-roll")
	if trace, _, _ := cli(t, dir, "runs", "show", strings.Fields(out)[0]); !strings.Contains(trace, "Dice tray is off") {
		t.Fatalf("off trace:\n%s", trace)
	}

	// On: it answers chat itself and keeps the session's rolls.
	if out, errOut, code := cli(t, dir, "packages", "on", "dice-tray"); code != 0 || !strings.Contains(out, "Dice tray is on") {
		t.Fatal(out, errOut)
	}
	tw.Say("streamer", "Fan", "!roll")
	eventually(t, "Fan's roll", said("Fan rolled a "))
	tw.Say("streamer", "Pal", "!roll 3")
	eventually(t, "Pal's roll", said("Pal rolled "))
	tw.Say("streamer", "Pal", "!best")
	eventually(t, "the best", said("(2 rolls so far)"))

	out, _, _ = cli(t, dir, "packages", "activity", "dice-tray")
	if !strings.Contains(out, "log       ok      Dice tray ready: d6") || strings.Count(out, "chat.send ok      ") != 2 {
		t.Fatalf("activity:\n%s", out)
	}
	if out, _, _ := cli(t, dir, "packages"); !strings.Contains(out, "Lua plugin, published by StreamGremlin examples (not verified); on; permissions: chat.send, events.publish, points.rounds; used by Best roll (Dice tray), Roll dice (Dice tray)") {
		t.Fatalf("list:\n%s", out)
	}

	// Settings start it again, fresh.
	if out, errOut, code := cli(t, dir, "packages", "set", "dice-tray", "sides=20"); code != 0 || !strings.Contains(out, "started again, fresh") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "packages", "set", "dice-tray", "sides=200"); code == 0 || !strings.Contains(errOut, "from 2 to 100") {
		t.Fatalf("200 sides: %s", errOut)
	}
	tw.Say("streamer", "Pal", "!best")
	eventually(t, "the session started over", said("No rolls yet this session."))

	// A restart: it's on again, with its new settings.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	eventually(t, "chat again", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	})
	tw.Say("streamer", "Fan", "!roll 1")
	eventually(t, "a d20 roll", said("(d20)"))

	// Removing turns its workflows off.
	out, _, _ = cli(t, dir, "packages", "remove", "dice-tray")
	if !strings.Contains(out, "These workflows use its steps and are turned off (not deleted): Best roll (Dice tray), Roll dice (Dice tray)") {
		t.Fatalf("remove check:\n%s", out)
	}
	if out, errOut, code := cli(t, dir, "packages", "remove", "dice-tray", "-yes"); code != 0 || !strings.Contains(out, "Workflows that used it (now off): Best roll (Dice tray), Roll dice (Dice tray)") {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "workflows"); strings.Contains(out, "chat.dice-tray-roll  on") {
		t.Fatalf("still on:\n%s", out)
	}
	out, _, _ = cli(t, dir, "packages", "history")
	for _, want := range []string{"install  dice-tray", "on       dice-tray", "settings dice-tray", "remove   dice-tray"} {
		if !strings.Contains(out, want) {
			t.Errorf("history has no %q:\n%s", want, out)
		}
	}
}
