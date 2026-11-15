//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Workflow groups through the real engine, CLI and mocktwitch: a group
// switched off stops its workflows answering chat and on restores them;
// a group can't come on to answer a command something else answers;
// renaming keeps the switch; the switch survives a crash; templates
// carry their group.
func TestWorkflowGroupsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
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
	save := func(id, group, command, answer string) {
		t.Helper()
		g := ""
		if group != "" {
			g = `"group":"` + group + `",`
		}
		f := writeFile(t, t.TempDir(), id+".json", `{"schema":"streamgremlin.workflow/v1","id":"`+id+`","name":"`+id+`",`+g+`
			"triggers":[{"type":"chat.command.received","where":{"command":"`+command+`"}}],
			"steps":[{"action":"chat.send","with":{"message":"`+answer+`"}}]}`)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
		if out, errOut, code := cli(t, dir, "workflows", "on", id); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	said := func(text string) int {
		n := 0
		for _, m := range tw.ChatLog() {
			if m.Text == text {
				n++
			}
		}
		return n
	}
	save("fun.dice", "Fun", "roll", "rolled")
	save("fun.flip", "Fun", "flip", "flipped")
	save("chat.hi", "", "hi", "hello there")
	if out, _, _ := cli(t, dir, "workflows", "groups"); !strings.Contains(out, "Fun") || !strings.Contains(out, "2 workflows, 2 switched on") {
		t.Fatalf("groups:\n%s", out)
	}

	if out, errOut, code := cli(t, dir, "workflows", "group", "fun", "off"); code != 0 || !strings.Contains(out, "Each keeps its own switch") {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "workflows"); !strings.Contains(out, "on, group off  [Fun]") {
		t.Fatalf("list:\n%s", out)
	}
	tw.Say("streamer", "Fan", "!roll")
	tw.Say("streamer", "Fan", "!hi")
	eventually(t, "the answer outside the group", func() string {
		if said("hello there") != 1 {
			return "no hello"
		}
		return ""
	})
	if said("rolled") != 0 {
		t.Fatal("a workflow answered with its group off")
	}

	// While Fun is off, another workflow takes !roll: Fun can't come on.
	save("games.roll", "", "roll", "other roll")
	if _, errOut, code := cli(t, dir, "workflows", "group", "Fun", "on"); code == 0 || !strings.Contains(errOut, "roll") {
		t.Fatalf("a taken command: %d %s", code, errOut)
	}
	cli(t, dir, "workflows", "delete", "games.roll")
	if out, errOut, code := cli(t, dir, "workflows", "group", "Fun", "on"); code != 0 {
		t.Fatal(out, errOut)
	}
	tw.Say("streamer", "Fan", "!roll")
	eventually(t, "the group back on", func() string {
		if said("rolled") != 1 {
			return "no roll"
		}
		return ""
	})

	if out, errOut, code := cli(t, dir, "workflows", "group", "Fun", "rename", "Games"); code != 0 || !strings.Contains(out, "Renamed Fun to Games") {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "group", "Games", "off")
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	if out, _, _ := cli(t, dir, "workflows", "groups"); !strings.Contains(out, "off  Games") {
		t.Fatalf("after a crash:\n%s", out)
	}

	// Templates name their group.
	if out, _, _ := cli(t, dir, "templates", "show", "thank-follow"); !strings.Contains(out, `"group": "Thanks"`) {
		t.Fatalf("template:\n%s", out)
	}
}
