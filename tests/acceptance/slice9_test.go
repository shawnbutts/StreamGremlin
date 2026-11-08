//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

const shoutHelper = `{"schema":"streamgremlin.workflow/v1","id":"helper.shout","name":"Shout",
	"triggers":[{"type":"never.happens.at_all"}],
	"steps":[{"action":"chat.send","with":{"message":"📣 ${args.text}","as":"streamer"}}]}`

const modsOnly = `{"schema":"streamgremlin.workflow/v1","id":"chat.announce","name":"Announce",
	"triggers":[{"type":"chat.command.received","where":{"command":"announce","chatter.role":["moderator","broadcaster"]}}],
	"steps":[{"action":"workflow.run","with":{"workflow":"helper.shout","args":{"text":"${event.payload.args}"}}}]}`

// "Who can use it", running another workflow, and delete and restore,
// through the real engine and CLI.
func TestWhoRunAnotherAndDeleteThroughTheCLI(t *testing.T) {
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
	files := t.TempDir()
	for name, body := range map[string]string{"helper.json": shoutHelper, "announce.json": modsOnly} {
		if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, name, body)); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	cli(t, dir, "workflows", "on", "chat.announce")

	// The helper is off: the call fails and says so.
	tw.Say("streamer", "streamer", "!announce first")
	eventually(t, "the failed call", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.announce"); !strings.Contains(out, "is off") {
			return out
		}
		return ""
	})
	cli(t, dir, "workflows", "on", "helper.shout")
	tw.Say("streamer", "viewer", "!announce from a viewer") // not a mod: doesn't run
	tw.Say("streamer", "streamer", "!announce hello all")
	said := func(text string) func() string {
		return func() string {
			for _, m := range tw.ChatLog() {
				if m.Text == text {
					return ""
				}
			}
			return "no " + text
		}
	}
	eventually(t, "the announcement", said("📣 hello all"))
	for _, m := range tw.ChatLog() {
		if m.Text == "📣 from a viewer" {
			t.Fatal("a viewer ran a mods-only command")
		}
	}
	out, _, _ := cli(t, dir, "runs", "-workflow", "chat.announce")
	id := strings.Fields(out)[0]
	if trace, _, _ := cli(t, dir, "runs", "show", id); !strings.Contains(trace, "steps[0]>helper.shout:steps[0]") {
		t.Fatalf("trace:\n%s", trace)
	}

	// Delete: gone from the list, can't be saved over, restored off.
	if out, _, code := cli(t, dir, "workflows", "delete", "chat.announce"); code != 0 || !strings.Contains(out, "Deleted") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "workflows"); strings.Contains(out, "chat.announce") {
		t.Fatalf("still listed:\n%s", out)
	}
	if out, _, _ := cli(t, dir, "workflows", "deleted"); !strings.Contains(out, "chat.announce") {
		t.Fatalf("deleted list:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "again.json", modsOnly)); code == 0 || !strings.Contains(errOut, "deleted") {
		t.Fatalf("saved over a deleted workflow: %d %s", code, errOut)
	}
	if out, _, code := cli(t, dir, "workflows", "restore", "chat.announce"); code != 0 || !strings.Contains(out, "It's off") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "workflows"); !strings.Contains(out, "off  chat.announce") {
		t.Fatalf("restored:\n%s", out)
	}
}
