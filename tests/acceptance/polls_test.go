//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// Polls and predictions through the real engine, CLI and mocktwitch: a poll
// started from the CLI, voted on, completed, and announced by the template;
// a second refused while one runs; a prediction locked and resolved from
// the CLI and announced; a non-Affiliate told plainly.
func TestPollsAndPredictionsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "the poll feeds", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "✓ Polls end") || !strings.Contains(out, "✓ Predictions end") {
			return out
		}
		return ""
	})
	for _, id := range []string{"poll-results", "prediction-results"} {
		out, _, _ := cli(t, dir, "templates", "show", id)
		f := writeFile(t, t.TempDir(), id+".json", out)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
		cli(t, dir, "workflows", "on", "twitch."+id)
	}
	said := func(text string) func() string {
		return func() string {
			for _, m := range tw.ChatLog() {
				if strings.Contains(m.Text, text) {
					return ""
				}
			}
			return "nothing says " + text
		}
	}

	if out, errOut, code := cli(t, dir, "polls", "start", "-seconds", "60", "What next?", "Celeste", "Hades"); code != 0 || !strings.Contains(out, "Poll started") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "polls", "start", "Other?", "a", "b"); code == 0 || !strings.Contains(errOut, "already running") {
		t.Fatalf("a second poll: %d %s", code, errOut)
	}
	tw.Vote("streamer", 1, 5)
	tw.Vote("streamer", 0, 2)
	tw.CompletePoll("streamer")
	eventually(t, "the poll results", said("Hades wins with 7 votes"))
	if out, _, _ := cli(t, dir, "polls"); !strings.Contains(out, "Poll (completed): What next?") || !strings.Contains(out, "5 votes") {
		t.Fatalf("polls:\n%s", out)
	}

	if out, errOut, code := cli(t, dir, "polls", "predict", "Will I win?", "Yes", "No"); code != 0 {
		t.Fatal(out, errOut)
	}
	tw.Bet("streamer", "Fan", 0, 500)
	if out, errOut, code := cli(t, dir, "polls", "lock"); code != 0 || !strings.Contains(out, "locked") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "polls", "resolve", "1"); code != 0 || !strings.Contains(out, "resolved") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the prediction result", said("“Yes” it is!"))
	if _, errOut, code := cli(t, dir, "polls", "cancel"); code == 0 || !strings.Contains(errOut, "already ended") {
		t.Fatalf("cancel after resolve: %d %s", code, errOut)
	}

	tw.SetAffiliate("streamer", false)
	if out, _, _ := cli(t, dir, "polls"); !strings.Contains(out, "Affiliate or Partner") {
		t.Fatalf("not affiliate:\n%s", out)
	}
}
