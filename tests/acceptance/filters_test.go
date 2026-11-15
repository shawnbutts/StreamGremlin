//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Chat filters through the real engine, CLI and mocktwitch (D-085): a
// viewer's link is deleted once, a moderator's isn't, a banned word gets a
// warning, !permit lets a link through, and the CLI shows what was caught
// and tries a message.
func TestChatFiltersThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "filters"); !strings.Contains(out, "Chat filters: off") || !strings.Contains(out, "Nothing caught lately.") {
		t.Fatalf("filters:\n%s", out)
	}
	body := `{"on":true,
		"words":{"on":true,"action":"warn","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"list":["darn"]},
		"links":{"on":true,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"allowed":["twitch.tv"]},
		"emotes":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"max":10},
		"caps":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"min_letters":10,"percent":70},
		"symbols":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"min_length":15,"percent":50},
		"repeats":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"times":3}}`
	if code, out := call(t, e, "PUT", "/v1/filters", e.token, body, nil); code != 200 {
		t.Fatal(code, out)
	}
	out, _, _ := cli(t, dir, "templates", "show", "permit")
	f := writeFile(t, t.TempDir(), "permit.json", out)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}

	// The chat connection is up once a message reaches the engine.
	tw.Say("streamer", "warmup", "hello")
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "viewers", "warmup"); !strings.Contains(out, "warmup") {
			return "not yet: " + out
		}
		return ""
	})

	spam := tw.Say("streamer", "spammer", "cheap followers at bit.ly/x")
	eventually(t, "the link deleted", func() string {
		if !tw.Deleted(spam) {
			return "not deleted"
		}
		return ""
	})
	tw.SetModerator("streamer", "helper", true)
	modLink := tw.Say("streamer", "helper", "rules are at example.com/rules")
	tw.Say("streamer", "ann", "well darn")
	eventually(t, "the warning", func() string {
		if tw.Warned("streamer", "ann") == "" {
			return "not warned"
		}
		return ""
	})
	if tw.Deleted(modLink) {
		t.Fatal("a moderator's link was deleted")
	}

	tw.Say("streamer", "helper", "!permit @artist")
	eventually(t, "the permit", func() string {
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, "you can post a link") {
				return ""
			}
		}
		return "no reply"
	})
	art := tw.Say("streamer", "artist", "my art: example.com/art")
	time.Sleep(time.Second)
	if tw.Deleted(art) {
		t.Fatal("a permitted link was deleted")
	}

	out, _, _ = cli(t, dir, "filters")
	if !strings.Contains(out, "Chat filters: on") || !strings.Contains(out, "spammer: cheap followers at bit.ly/x") ||
		!strings.Contains(out, "ann: well darn") || strings.Contains(out, "artist") {
		t.Fatalf("filters:\n%s", out)
	}
	if out, _, _ := cli(t, dir, "filters", "check", "go", "to", "bit.ly/y"); !strings.Contains(out, "Links would catch it") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "filters", "check", "-role", "moderator", "bit.ly/y"); !strings.Contains(out, "No filter") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "mod", "history"); !strings.Contains(out, "spammer") {
		t.Fatalf("history:\n%s", out)
	}
}
