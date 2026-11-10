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

// Earning rules through the real engine, CLI and mocktwitch: off until
// turned on (and nothing before pays); a first follow once, ever; subs by
// tier, gifted subs to the gifter (never the recipient), bits per 100, a
// redelivered cheer paid once; chat only once live, at most once a minute;
// and the ledger still adding up.
func TestEarningThroughTheCLIAndTwitch(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat and events", func() string {
		out, _, _ := cli(t, dir, "twitch")
		if !strings.Contains(out, "Chat:       connected") || !strings.Contains(out, "✓ Channel point redemptions") {
			return out
		}
		return ""
	})
	if out, _, _ := cli(t, dir, "points", "earning"); !strings.Contains(out, "Earning is off") || !strings.Contains(out, "unknown") {
		t.Fatal(out)
	}
	tw.Follow("streamer", "early") // before earning is on: never pays
	tw.Say("streamer", "early", "hi")
	eventually(t, "early recorded", func() string {
		if out, _, _ := cli(t, dir, "viewers", "early"); !strings.Contains(out, "early") {
			return out
		}
		return ""
	})
	if out, errOut, code := cli(t, dir, "points", "earning", "set", "follow=40", "tier1=100", "tier2=200", "gift=30", "bits=5", "every=60"); code != 0 ||
		!strings.Contains(out, "a first follow 40") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "points", "earning", "set", "follow=-1"); code == 0 || !strings.Contains(errOut, "a follow earns 0 to") {
		t.Fatal("a negative rule:", code, errOut)
	}
	if out, _, code := cli(t, dir, "points", "earning", "on"); code != 0 || !strings.Contains(out, "Earning is on") {
		t.Fatal(out)
	}

	tw.Follow("streamer", "fan")
	tw.Follow("streamer", "fan") // again: once ever
	tw.Subscribe("streamer", "subber", "2000", false)
	tw.Subscribe("streamer", "lucky", "1000", true) // a gift recipient
	tw.Gift("streamer", "gifter", "1000", 3)
	cheer := map[string]any{"bits": 250, "message": "Cheer250", "is_anonymous": false, "user_id": tw.UserID("cheery"), "user_login": "cheery", "user_name": "Cheery"}
	id := tw.Notify("streamer", "channel.cheer", cheer, "")
	tw.Notify("streamer", "channel.cheer", cheer, id) // redelivered
	tw.Say("streamer", "chatty", "before going live")
	tw.GoLive("streamer")
	eventually(t, "live", func() string {
		if out, _, _ := cli(t, dir, "points", "earning"); !strings.Contains(out, "You're live") {
			return out
		}
		return ""
	})
	tw.Say("streamer", "chatty", "one")
	tw.Say("streamer", "chatty", "two") // within the minute
	tw.Say("streamer", "streamer", "the streamer never earns")
	want := map[string]string{"fan": " 40", "subber": " 200", "gifter": " 90", "cheery": " 10", "chatty": " 1"}
	eventually(t, "earned", func() string {
		out, _, _ := cli(t, dir, "viewers")
		for login, n := range want {
			found := false
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(strings.ToLower(line), login) && strings.Contains(line, n+" ") {
					found = true
				}
			}
			if !found {
				return out
			}
		}
		return ""
	})
	out, _, _ := cli(t, dir, "points")
	if !strings.Contains(out, "5 viewers hold 341 points") {
		t.Fatal("early, lucky or the streamer earned, or someone earned twice:", out)
	}
	if out, _, code := cli(t, dir, "points", "audit"); code != 0 || !strings.Contains(out, "✓") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "viewers", "show", "@gifter"); !strings.Contains(out, "gifting 3 subs") {
		t.Fatal(out)
	}
}
