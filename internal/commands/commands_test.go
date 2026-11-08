package commands

import (
	"strings"
	"testing"
)

func TestOverlapNeedsTheSameCommandChatsAndChatters(t *testing.T) {
	a := Claim{Command: "so", Owner: "a", Name: "A"}
	cases := []struct {
		b    Claim
		want bool
	}{
		{Claim{Command: "so", Owner: "b"}, true},
		{Claim{Command: "hug", Owner: "b"}, false},
		{Claim{Command: "so", Owner: "a"}, false},                              // a workflow never conflicts with itself
		{Claim{Command: "so", Owner: "b", Roles: []string{"moderator"}}, true}, // a has no limit
		{Claim{Command: "so", Owner: "b", Sources: []string{"youtube"}}, true}, // a has no limit
		{Claim{Command: "so", Owner: "b", Roles: []string{}}, true},            // a meets anything
	}
	for _, c := range cases {
		if got := Overlap(a, c.b); got != c.want {
			t.Errorf("%+v: %v", c.b, got)
		}
	}
	mods := Claim{Command: "so", Owner: "m", Roles: []string{"moderator", "broadcaster"}}
	viewers := Claim{Command: "so", Owner: "v", Roles: []string{"viewer", "subscriber"}}
	vips := Claim{Command: "so", Owner: "x", Roles: []string{"vip", "broadcaster"}}
	if Overlap(mods, viewers) || !Overlap(mods, vips) {
		t.Fatal("roles")
	}
	tw := Claim{Command: "so", Owner: "t", Sources: []string{"twitch"}}
	yt := Claim{Command: "so", Owner: "y", Sources: []string{"youtube"}}
	if Overlap(tw, yt) {
		t.Fatal("chats")
	}
}

func TestConflictsAndTheTable(t *testing.T) {
	held := []Claim{
		{Command: "hello", Owner: "w1", Name: "Hello"},
		{Command: "hello", Owner: "w1", Name: "Hello", Roles: []string{"vip"}}, // the same owner twice
		{Command: "so", Owner: "w2", Name: "Shoutout", Roles: []string{"moderator"}},
	}
	cs := Conflicts([]Claim{{Command: "hello", Owner: "new"}, {Command: "so", Owner: "new", Roles: []string{"viewer"}}}, held)
	if len(cs) != 1 || cs[0] != (Conflict{Command: "hello", Owner: "w1", Name: "Hello"}) {
		t.Fatalf("%+v", cs)
	}
	msg := (&TakenError{Conflicts: cs}).Error()
	if !strings.Contains(msg, "!hello is already answered by “Hello”") || !strings.Contains(msg, "Turn that off first") {
		t.Fatal(msg)
	}
	table := Table(append(held, Claim{Command: "hello", Owner: "w3", Name: "Also hello"}, Claim{Command: "so", Owner: "w4", Name: "Off one"}),
		map[string]bool{"w1": true, "w2": true, "w3": true})
	if len(table) != 2 || table[0].Command != "hello" || !table[0].Conflict || len(table[0].Owners) != 2 {
		t.Fatalf("%+v", table)
	}
	// An overlap with a workflow that's off isn't a conflict; those on are listed first.
	if table[1].Conflict || table[1].Owners[0].Owner != "w2" || table[1].Owners[1].Enabled {
		t.Fatalf("%+v", table[1])
	}
}
