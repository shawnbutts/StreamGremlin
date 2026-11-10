package giveaways

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Drawing from recent chat: everyone who chatted in the window enters once,
// in chat order (not the streamer, not test chat, not chat from before the
// window); rules and chances apply; nobody hears "you're in" or why they
// were turned away; entries close and winners are drawn at once; the record
// checks; and no entry command is held.
func TestDrawFromRecentChat(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.talk("old", "hi from long ago", false, nil)
	r.wait(11 * time.Minute) // "old" is now outside a 10-minute window
	r.talk("ann", "hello", false, nil)
	r.say("bob", "lurk", false)
	r.talk("ann", "again", false, nil)
	r.send("chat.message.received", "cat", "hey", "", false, map[string]any{"is_subscriber": true})
	r.send("chat.message.received", "me", "my own chat", "", false, map[string]any{"is_broadcaster": true})
	r.talk("tester", "test chat", true, nil)
	r.chat.follows = map[string]time.Time{"id-ann": time.Now().AddDate(-1, 0, 0), "id-cat": time.Now().AddDate(-1, 0, 0)}

	g, err := r.svc.Open(ctx, Settings{Title: "a sticker", FromChatMinutes: 10, Winners: 2, SubscriberChances: 2,
		Rules: Rules{Followers: true}}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if g.State != Closed || g.EntryCount != 2 || g.Command != "" {
		t.Fatalf("entered, closed and drawn at once: %+v\n%+v\n%s", g, r.detail(g.ID).Refusals, r.chat.all())
	}
	d := r.detail(g.ID)
	if len(d.Entries) != 2 || d.Entries[0].Login != "ann" || d.Entries[1].Login != "cat" || d.Entries[1].Chances != 2 {
		t.Fatal(d.Entries)
	}
	if len(d.Refusals) != 1 || d.Refusals[0].Login != "bob" || d.Refusals[0].Reason != ReasonFollowers {
		t.Fatal(d.Refusals)
	}
	if len(d.Draws) != 2 || len(Verify(d)) != 0 {
		t.Fatal(d.Draws, Verify(d))
	}
	if r.svc.Claims() != nil && len(r.svc.Claims()) != 0 {
		t.Fatal("a draw from chat holds a command:", r.svc.Claims())
	}
	r.eventually("chat", func() bool {
		all := r.chat.all()
		return strings.Contains(all, "🎲 Drawing a sticker now from everyone who chatted in the last 10 minutes. Open to followers. "+
			"One entry each, and subscribers get 2 chances to everyone else's 1; 2 winners will be drawn.") &&
			strings.Contains(all, "2 entries from chat for a sticker. Drawing!") && strings.Count(all, " wins a sticker!") == 2
	})
	if all := r.chat.all(); strings.Contains(all, "you're in") || strings.Contains(all, "@bob") {
		t.Fatal("told people who didn't ask:\n" + all)
	}
	// Chat after it opened doesn't enter.
	r.talk("late", "am I in?", false, nil)
	r.say("late", "join", false)
	time.Sleep(150 * time.Millisecond)
	if n := len(r.detail(g.ID).Entries); n != 2 {
		t.Fatal(n)
	}
	if _, err := r.svc.End(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
}

// No one to draw: chat hears why, and it's cancelled.
func TestDrawFromQuietChat(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, err := r.svc.Open(ctx, Settings{Title: "a pin", FromChatMinutes: 5}, "app")
	if err != nil || g.State != Cancelled {
		t.Fatal(g, err)
	}
	r.eventually("why", func() bool {
		return strings.Contains(r.chat.all(), "a pin: no one chatted in the last 5 minutes, so it's cancelled.")
	})
	// Practice draws take test chat only.
	r.talk("tester", "hi", true, nil)
	r.talk("real", "hi", false, nil)
	g, err = r.svc.Open(ctx, Settings{Title: "p", FromChatMinutes: 5, Practice: true}, "app")
	if err != nil || g.EntryCount != 1 || r.detail(g.ID).Entries[0].Login != "tester" {
		t.Fatal(g, err)
	}
}

func TestFromChatLimits(t *testing.T) {
	r := newRig(t)
	for _, st := range []Settings{{Title: "x", FromChatMinutes: 61}, {Title: "x", FromChatMinutes: -1},
		{Title: "x", FromChatMinutes: 5, Minutes: 3}, {Title: "x", FromChatMinutes: 5, EnterByChat: true}} {
		if _, err := r.svc.Open(context.Background(), st, "app"); !errors.Is(err, ErrInvalid) {
			t.Error(st, err)
		}
	}
}
