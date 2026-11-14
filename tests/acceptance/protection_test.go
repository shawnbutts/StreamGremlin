//go:build acceptance

package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// M8 slice 1 (D-105) through the real engine, CLI and mocktwitch: Shield
// Mode on and off, a lock and an unlock that puts back only what it
// changed, Shield Mode turned on from Twitch's site arriving as an event,
// and the "Shield Mode on when I go offline" template.
func TestShieldModeAndLockThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "listening for Shield Mode", func() string {
		if tw.SubscribedTo("streamer", "channel.shield_mode.begin") == 0 {
			return "not yet"
		}
		return ""
	})
	ok := func(want string, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
		return out
	}
	ok("Shield Mode: off.", "mod", "modes")
	ok("Shield Mode is on.", "mod", "shield", "on")
	if !tw.ShieldActive("streamer") {
		t.Fatal("not on")
	}
	ok("Shield Mode is off.", "mod", "shield", "off")

	// Slow mode was on before: the lock leaves it, the unlock keeps it.
	ok("slow (10 seconds)", "mod", "modes", "slow=10")
	ok("Locked: Shield Mode, emote-only and followers-only (followed 10 minutes) on.", "mod", "lock")
	out := ok("Locked from StreamGremlin since", "mod", "modes")
	if !strings.Contains(out, "emote-only") || !strings.Contains(out, "followers-only (followed 10 minutes)") || !strings.Contains(out, "Shield Mode: on.") {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "mod", "lock"); code == 0 || !strings.Contains(errOut, "locked already") {
		t.Fatal("locked twice:", code, errOut)
	}
	ok("Unlocked: chat is back as it was before the lock.", "mod", "unlock")
	out = ok("Chat modes on: slow (10 seconds).", "mod", "modes")
	if !strings.Contains(out, "Shield Mode: off.") || strings.Contains(out, "Locked") {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "mod", "unlock"); code == 0 || !strings.Contains(errOut, "isn't locked") {
		t.Fatal("unlocked twice:", code, errOut)
	}
	ok("lock", "mod", "history")

	// From Twitch's site: an event.
	tw.SetShieldMode("streamer", "AMod", true)
	eventually(t, "the Shield Mode event", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "moderation.shield_mode.changed")
		if !strings.Contains(out, "moderation.shield_mode.changed") {
			return out
		}
		return ""
	})
	tw.SetShieldMode("streamer", "AMod", false)

	// The template: Shield Mode on when you go offline.
	tmpl, _, _ := cli(t, dir, "templates", "show", "shield-offline")
	if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, t.TempDir(), "w.json", tmpl)); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "moderation.shield-offline")
	tw.GoOffline("streamer")
	eventually(t, "Shield Mode on after going offline", func() string {
		if !tw.ShieldActive("streamer") {
			return "still off"
		}
		return ""
	})
}

// Ban lists (M8 slice 2, D-106) through the real engine, CLI and
// mocktwitch with Twitch's rate limit on: a generated list read, checked
// (you and a mod left alone, unknown names found out), nothing banned
// until confirmed, then banned at Twitch's pace; the engine killed part way
// and started again, carrying on with nobody banned twice; a ban lifted by
// hand noticed and left alone; a newer file banning only its new names;
// saving; and undo lifting only the list's own bans.
func TestBanListsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	ok := func(want string, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
		return out
	}
	waitFor := func(what string, want ...string) string {
		t.Helper()
		var last string
		eventually(t, what, func() string {
			last, _, _ = cli(t, dir, "bans")
			for _, w := range want {
				if !strings.Contains(last, w) {
					return last
				}
			}
			return ""
		})
		return last
	}
	const n = 300
	var lines []string
	for i := range n {
		name := fmt.Sprintf("raidbot%03d", i)
		tw.UserID(name)
		lines = append(lines, name)
	}
	tw.SetModerator("streamer", "raidbot000", true)
	tw.BanViewer("streamer", "raidbot001") // banned before, by someone else
	files := t.TempDir()
	list := filepath.Join(files, "raid.txt")
	if err := os.WriteFile(list, []byte("# a raid\n"+strings.Join(lines, "\n")+"\nstreamer\nnot_a_real_account\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok("Nothing is banned until you confirm", "bans", "import", "Raid", list)
	waitFor("checked", "Checked: confirm to start.", fmt.Sprintf("waiting %d", n-2), "already 1", "not found 1", "kept 2")
	if len(tw.Bans("streamer")) != 1 {
		t.Fatal("banned before confirming")
	}
	ok("(a moderator)", "bans", "show", "Raid", "kept")

	// Twitch's pace: 400 requests every 2 seconds. Ban lists leave 200 of
	// them for everything else, so they stop at the spare each window.
	tw.SetRateLimit(400, 2*time.Second)
	ok("Banning 298 at Twitch's pace", "bans", "confirm", "Raid")
	eventually(t, "some banned", func() string {
		if len(tw.Bans("streamer")) < 40 {
			return "not yet"
		}
		return ""
	})
	// A crash, part way.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	waitFor("done after the restart", "Done.", fmt.Sprintf("banned %d", n-2))
	banned := map[string]int{}
	for _, c := range tw.ModCalls() {
		if c.Method == "POST" && c.Path == "/helix/moderation/bans" {
			banned[c.UserLogin]++
			if c.Status != 200 {
				t.Fatalf("a ban answered %d: %+v", c.Status, c)
			}
		}
	}
	for who, k := range banned {
		if k != 1 {
			t.Fatalf("%s banned %d times", who, k)
		}
	}
	if len(banned) != n-2 || tw.IsBanned("streamer", "raidbot000") {
		t.Fatalf("%d banned", len(banned))
	}
	if tw.RateDenied() != 0 {
		t.Fatalf("Twitch said too many %d times: the spare wasn't kept", tw.RateDenied())
	}
	tw.SetRateLimit(0, 0)

	// Unbanned by hand: noticed, and left alone.
	tw.UnbanViewer("streamer", "raidbot002")
	ok("Checking its bans against Twitch", "bans", "compare", "Raid")
	waitFor("compared", "Done.", "by hand 1")

	// A newer file: two new names; raidbot003 gone from it.
	tw.UserID("raidbot900")
	tw.UserID("raidbot901")
	newer := filepath.Join(files, "raid2.txt")
	kept := append(append([]string{}, lines[:3]...), lines[4:]...)
	if err := os.WriteFile(newer, []byte(strings.Join(append(kept, "raidbot900", "raidbot901"), "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(tw.ModCalls())
	ok("Reading the file", "bans", "import", "Raid", newer)
	waitFor("checked again", "Checked: confirm to start.", "waiting 2", "removed 1")
	ok("Banning 2", "bans", "confirm", "Raid")
	waitFor("done again", "Done.")
	var newBans []string
	for _, c := range tw.ModCalls()[before:] {
		if c.Method == "POST" && c.Path == "/helix/moderation/bans" {
			newBans = append(newBans, c.UserLogin)
		}
	}
	if strings.Join(newBans, ",") != "raidbot900,raidbot901" || tw.IsBanned("streamer", "raidbot002") || !tw.IsBanned("streamer", "raidbot003") {
		t.Fatalf("new bans %v", newBans)
	}

	// The saved file: a heading, then each one banned now (not the one
	// unbanned by hand, nor the one removed from the newer file).
	saved := filepath.Join(files, "saved.txt")
	ok("Saved Raid", "bans", "save", "Raid", saved, "banned")
	b, err := os.ReadFile(saved)
	if err != nil || strings.Count(string(b), "\n") != 1+(n-2)-1-1+2 || !strings.Contains(string(b), "raidbot900\n") {
		t.Fatalf("%d lines: %v", strings.Count(string(b), "\n"), err)
	}

	// Undo: only the list's own bans (raidbot003 too: the list banned it).
	ok("Add -yes", "bans", "undo", "Raid")
	ok("Unbanning", "bans", "undo", "Raid", "-yes")
	waitFor("undone", "Done.")
	if got := tw.Bans("streamer"); len(got) != 1 || got[0] != "raidbot001" {
		t.Fatalf("still banned: %v", got)
	}
}
