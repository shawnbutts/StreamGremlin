package filters

import (
	"slices"
	"testing"
	"time"
)

// FuzzJudge: every chat message is judged. With every rule on, judging
// never panics, names a known rule or none, and leaves the streamer and
// mods alone.
func FuzzJudge(f *testing.F) {
	for _, s := range []string{"hello", "BUY FOLLOWERS at example.com", "⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿", "!!!!!!!!!!!!", "Kappa Kappa Kappa", "\x00\xff", "a.b"} {
		f.Add(s, "viewer", 3)
	}
	s := Defaults()
	s.On = true
	for _, r := range s.rules() {
		r.On = true
	}
	s.Words.List = []string{"badword", "spam*"}
	c, err := Compile(s)
	if err != nil {
		f.Fatal(err)
	}
	known := []string{"", RuleWords, RuleNewAccounts, RuleLinks, RuleEmotes, RuleCaps, RuleSymbols, RuleArt, RuleRepeats}
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, text, role string, age int) {
		reps := NewRepeats()
		m := Message{Platform: "twitch", UserID: "1", Login: "a", Role: role, Text: text, AccountAgeDays: &age, At: at, Emotes: []string{"Kappa"}}
		for range 3 {
			if got := c.Judge(m, reps); !slices.Contains(known, got) {
				t.Fatalf("rule %q", got)
			}
		}
		m.IsBroadcaster, m.Role = true, "broadcaster"
		if got := c.Judge(m, nil); got != "" {
			t.Fatalf("the streamer was caught by %s", got)
		}
	})
}
