//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Channel point rewards through the real engine, CLI and mocktwitch:
// made, listed with who made them, paused; a workflow refunds a
// redemption of a StreamGremlin reward, once, and never for a test event;
// a reward made on Twitch can't be refunded, and the run says why; the
// queue and done from the CLI; a channel without channel points is told.
func TestChannelPointRewardsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	if out, _, _ := cli(t, dir, "rewards"); !strings.Contains(out, "sign in with your Twitch account to use channel point rewards") {
		t.Fatalf("signed out:\n%s", out)
	}
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat and events", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "✓ Channel point redemptions") {
			return out
		}
		return ""
	})
	tw.AddDashboardReward("streamer", "Hydrate", 100)
	if out, errOut, code := cli(t, dir, "rewards", "add", "-cost", "500", "-input", "-prompt", "Who gets it?", "-cooldown", "60", "Group hug"); code != 0 ||
		!strings.Contains(out, `Made "Group hug" (500 points)`) {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "rewards", "add", "-cost", "5", "Hydrate"); code == 0 || !strings.Contains(errOut, "has that title") {
		t.Fatalf("a taken title: %d %s", code, errOut)
	}
	out, _, _ := cli(t, dir, "rewards")
	if !strings.Contains(out, "Group hug") || !strings.Contains(out, "made in StreamGremlin") || !strings.Contains(out, "made on Twitch: trigger only") {
		t.Fatalf("list:\n%s", out)
	}
	if out, errOut, code := cli(t, dir, "rewards", "set", "Hydrate", "-state", "pause"); code == 0 || !strings.Contains(errOut, "made in StreamGremlin") {
		t.Fatalf("pausing Twitch's reward: %d %s %s", code, out, errOut)
	}
	if out, errOut, code := cli(t, dir, "rewards", "set", "group hug", "-state", "pause"); code != 0 || !strings.Contains(out, "paused") {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "rewards", "set", "Group hug", "-state", "resume")

	for _, wf := range []struct{ id, reward string }{{"points.hug", "Group hug"}, {"points.hydrate", "Hydrate"}} {
		f := writeFile(t, t.TempDir(), wf.id+".json", `{"schema":"streamgremlin.workflow/v1","id":"`+wf.id+`","name":"`+wf.reward+` refund",
			"triggers":[{"type":"rewards.channel_points.redeemed","where":{"reward.title":"`+wf.reward+`"}}],
			"steps":[{"action":"rewards.redemption.set","with":{"action":"refund"}}]}`)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
		cli(t, dir, "workflows", "on", wf.id)
	}
	// A test event reaches nothing on Twitch.
	cli(t, dir, "events", "simulate", "rewards.channel_points.redeemed", "reward=Group hug")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "points.hug"); !strings.Contains(out, "succeeded (test)") {
			return out
		}
		return ""
	})

	hug := tw.Redeem("streamer", "Fan", "Group hug", 0, "everyone")
	eventually(t, "the refund", func() string {
		if st := tw.RedemptionStatus(hug); st != "CANCELED" {
			return "status " + st
		}
		return ""
	})
	tw.Redeem("streamer", "Fan", "Hydrate", 0, "")
	eventually(t, "the refused refund", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "points.hydrate"); !strings.Contains(out, "failed") || !strings.Contains(out, "made in StreamGremlin") {
			return out
		}
		return ""
	})

	// By hand: the queue, and done.
	cli(t, dir, "workflows", "off", "points.hug")
	waiting := tw.Redeem("streamer", "Pal", "Group hug", 0, "me please")
	eventually(t, "the queue", func() string {
		if out, _, _ := cli(t, dir, "rewards", "queue", "Group hug"); !strings.Contains(out, waiting) || !strings.Contains(out, "me please") {
			return out
		}
		return ""
	})
	if out, errOut, code := cli(t, dir, "rewards", "done", "Group hug", waiting); code != 0 || !strings.Contains(out, "Marked Pal's") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "rewards", "refund", "Group hug", waiting); code == 0 || !strings.Contains(errOut, "already handled") {
		t.Fatalf("refund after done: %d %s", code, errOut)
	}
	if st := tw.RedemptionStatus(waiting); st != "FULFILLED" {
		t.Fatal(st)
	}
	if out, errOut, code := cli(t, dir, "rewards", "delete", "Group hug"); code != 0 {
		t.Fatal(out, errOut)
	}

	tw.SetAffiliate("streamer", false)
	if out, _, _ := cli(t, dir, "rewards"); !strings.Contains(out, "Affiliate or Partner") {
		t.Fatalf("not affiliate:\n%s", out)
	}
}
