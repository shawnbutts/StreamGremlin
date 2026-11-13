//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Twitch's Blocked Terms through the real engine, CLI and mocktwitch
// (D-088): one added on Twitch is listed with AutoMod's end, terms are
// added (one twice, one too short for Twitch), listed, and removed.
func TestBlockedTermsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	tw.BlockTerm("streamer", "automodded", time.Now().Add(time.Hour))

	out, errOut, code := cli(t, dir, "filters", "blocked")
	if code != 0 || !strings.Contains(out, "automodded") || !strings.Contains(out, "(AutoMod, until ") {
		t.Fatal(out, errOut)
	}
	out, errOut, code = cli(t, dir, "filters", "block", "spoiler*", "darn", "spoiler*", "a*")
	if code != 1 || strings.Count(out, "Blocked: ") != 3 || !strings.Contains(out, "Refused: a*: ") || !strings.Contains(out, "too short") {
		t.Fatal(code, out, errOut)
	}
	terms := tw.BlockedTerms("streamer")
	if len(terms) != 3 || terms[0].Text != "darn" || terms[1].Text != "spoiler*" {
		t.Fatalf("%+v", terms)
	}
	if out, errOut, code := cli(t, dir, "filters", "unblock", terms[0].ID); code != 0 || !strings.Contains(out, "Removed") {
		t.Fatal(out, errOut)
	}
	out, _, _ = cli(t, dir, "filters", "blocked")
	if strings.Contains(out, "darn") || !strings.Contains(out, "Twitch's Blocked Terms (2)") {
		t.Fatal(out)
	}
	if _, _, code := cli(t, dir, "filters", "block"); code != 2 {
		t.Fatal("no term:", code)
	}
}
