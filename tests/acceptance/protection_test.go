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

// The raid guard (M8 slice 3, D-107) through the real engine, CLI and
// mocktwitch: regulars and old accounts don't count; a burst of brand-new
// accounts right after a raid warns, starts "A raid looks suspicious" and,
// set to lock, locks chat; the cooldown holds back a second warning; and
// the raiders are banned as a ban list from names.
func TestRaidGuardThroughTheCLI(t *testing.T) {
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
	eventually(t, "chat and raids", func() string {
		if tw.Subscribed("streamer") == 0 || tw.SubscribedTo("streamer", "channel.raid") == 0 {
			return "not yet"
		}
		return ""
	})
	ok("warns on Live.", "raidguard")
	ok("Saved.", "raidguard", "set", "mode=lock", "first=50", "new=6", "days=7", "window=60", "after-raid=5")
	// A regular, and an old account: never count.
	tw.Say("streamer", "regular", "hello")
	eventually(t, "the regular recorded", func() string {
		if out, _, _ := cli(t, dir, "viewers", "regular"); !strings.Contains(out, "regular") {
			return out
		}
		return ""
	})
	time.Sleep(4 * time.Second) // well past the grace for "just seen"
	tw.Say("streamer", "regular", "still here")
	tw.SetAccountCreated("oldtimer", time.Now().Add(-3*365*24*time.Hour))
	tw.Say("streamer", "oldtimer", "hi")
	// A raid, then brand-new accounts: half of 6 is a burst.
	tw.Raid("streamer", "SomeChannel", 50)
	for i := range 3 {
		name := fmt.Sprintf("freshbot%d", i)
		tw.SetAccountCreated(name, time.Now().Add(-2*time.Hour))
		tw.Say("streamer", name, "hate")
	}
	var out string
	eventually(t, "the warning", func() string {
		out, _, _ = cli(t, dir, "raidguard")
		if !strings.Contains(out, "Latest warning") {
			return out
		}
		return ""
	})
	if ev, _, _ := cli(t, dir, "events", "-types", "moderation.raid.suspected"); !strings.Contains(ev, "#") {
		t.Fatal("no event:", ev)
	}
	if !strings.Contains(out, "3 brand-new accounts in 60 seconds; chat was locked.") || strings.Contains(out, "regular") ||
		strings.Contains(out, "oldtimer") {
		t.Fatal(out)
	}
	eventually(t, "chat locked", func() string {
		if !tw.ShieldActive("streamer") {
			return "Shield Mode off"
		}
		return ""
	})
	ok("Locked from StreamGremlin since", "mod", "modes")
	ok("lock", "mod", "history")
	// The raiders banned as a ban list from names (Live's "Ban these
	// accounts…"), at once though you're live.
	code, body := call(t, e, "POST", "/v1/ban-lists", e.token, `{"name":"Raid","names":["freshbot0","freshbot1","freshbot2"],"start":true}`, nil)
	if code != 200 {
		t.Fatal(code, body)
	}
	eventually(t, "raiders banned", func() string {
		for i := range 3 {
			if !tw.IsBanned("streamer", fmt.Sprintf("freshbot%d", i)) {
				return "not yet"
			}
		}
		return ""
	})
	// Within the cooldown, more new accounts don't warn again.
	for i := range 4 {
		name := fmt.Sprintf("laterbot%d", i)
		tw.SetAccountCreated(name, time.Now().Add(-time.Hour))
		tw.Say("streamer", name, "more")
	}
	time.Sleep(time.Second)
	if out, _, _ := cli(t, dir, "events", "-types", "moderation.raid.suspected"); strings.Count(out, "#") != 1 {
		t.Fatal("warned again within the cooldown:", out)
	}
}

// The New accounts and Text art filters (M8 slice 4, D-108) through the
// real engine and mocktwitch: a day-old account's message deleted, an old
// account's left; text art deleted; a client that doesn't send the new
// rules leaves them as they were.
func TestNewAccountAndArtFiltersThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not yet"
		}
		return ""
	})
	code, body := call(t, e, "PUT", "/v1/filters", e.token, `{"on":true,"new_accounts":{"on":true,"days":7},"art":{"on":true,"min_chars":10}}`, nil)
	if code != 200 {
		t.Fatal(code, body)
	}
	// An older client's save, without the new rules: they stay on.
	code, body = call(t, e, "PUT", "/v1/filters", e.token, `{"on":true,"caps":{"on":false,"action":"delete","min_letters":10,"percent":70}}`, nil)
	if code != 200 || !strings.Contains(body, `"new_accounts":{"on":true`) {
		t.Fatal(code, body)
	}
	out, _, _ := cli(t, dir, "filters")
	if !strings.Contains(out, "New accounts") || !strings.Contains(out, "younger than 7 days") || !strings.Contains(out, "Text art") {
		t.Fatal(out)
	}
	tw.SetAccountCreated("freshie", time.Now().Add(-24*time.Hour))
	tw.SetAccountCreated("veteran", time.Now().Add(-5*365*24*time.Hour))
	tw.Say("streamer", "veteran", "hello everyone")
	tw.Say("streamer", "freshie", "hello everyone")
	tw.Say("streamer", "veteran", "⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿ look")
	eventually(t, "two caught", func() string {
		out, _, _ := cli(t, dir, "filters")
		if !strings.Contains(out, "New accounts delete") || !strings.Contains(out, "Text art") || strings.Count(out, "done") < 2 {
			return out
		}
		return ""
	})
	out, _, _ = cli(t, dir, "filters")
	if strings.Contains(out, "veteran: hello everyone") {
		t.Fatal("an old account's message was caught:", out)
	}
}

// The follow-bot review (M8 slice 5, D-109) through the real engine, CLI
// and mocktwitch: a burst of day-old accounts that never chatted found and
// marked; an old follower and a young one who chatted not; blocked as a
// list, you and your mods left alone; undone.
func TestFollowBotReviewThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "follows", func() string {
		if tw.SubscribedTo("streamer", "channel.follow") == 0 {
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
	tw.SetAccountCreated("oldfan", time.Now().Add(-4*365*24*time.Hour))
	tw.Follow("streamer", "oldfan")
	tw.SetAccountCreated("chattynew", time.Now().Add(-24*time.Hour))
	tw.Say("streamer", "chattynew", "hi, new here!")
	tw.Follow("streamer", "chattynew")
	for i := range 5 {
		name := fmt.Sprintf("followbot%d", i)
		tw.SetAccountCreated(name, time.Now().Add(-20*time.Hour))
		tw.Follow("streamer", name)
	}
	tw.SetModerator("streamer", "followbot4", true)
	eventually(t, "the follows recorded", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "audience.follow.created")
		if strings.Count(out, "#") < 7 {
			return out
		}
		return ""
	})
	eventually(t, "chattynew recorded as chatting", func() string {
		out, _, _ := cli(t, dir, "viewers", "chattynew")
		if !strings.Contains(out, "chattynew") {
			return out
		}
		return ""
	})
	out := ok("5 of 7 follows look like bots", "followbots")
	if strings.Contains(out, "oldfan") || strings.Contains(out, "chattynew") || strings.Count(out, "in a burst") != 5 {
		t.Fatal(out)
	}
	ok("Add -yes", "followbots", "block")
	ok("Blocking 5", "followbots", "block", "-yes")
	eventually(t, "blocked", func() string {
		if got := tw.Blocked("streamer"); len(got) != 4 {
			return fmt.Sprint(got)
		}
		return ""
	})
	out = ok("Done.", "bans")
	if !strings.Contains(out, "kept 1") {
		t.Fatal("the mod wasn't left alone:", out)
	}
	if len(tw.Bans("streamer")) != 0 {
		t.Fatal("banned instead of blocked")
	}
	var list string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Follow bots") {
			list = l[:strings.Index(l, ": ")]
		}
	}
	ok("Unblocking", "bans", "undo", list, "-yes")
	eventually(t, "unblocked", func() string {
		if got := tw.Blocked("streamer"); len(got) != 0 {
			return fmt.Sprint(got)
		}
		return ""
	})
}
