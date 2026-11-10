package giveaways

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

func (r *rig) refusal(id, login string) string {
	for _, x := range r.detail(id).Refusals {
		if x.Login == login {
			return x.Reason
		}
	}
	return ""
}

func (r *rig) refused(id, login, reason string) {
	r.t.Helper()
	r.eventually(login+" turned away for "+reason, func() bool { return r.refusal(id, login) == reason })
}

func (r *rig) entered(id, login string) *Entry {
	for _, e := range r.detail(id).Entries {
		if e.Login == login {
			return &e
		}
	}
	return nil
}

func (r *rig) in(id, login string) Entry {
	r.t.Helper()
	r.eventually(login+" in", func() bool { return r.entered(id, login) != nil })
	return *r.entered(id, login)
}

func (r *rig) count(text string) int { return strings.Count(r.chat.all(), text) }

// Follow and account-age rules ask Twitch, record what each entry was
// checked against, turn others away once with the reason in chat, let
// someone who follows afterwards in, and turn everyone away with "try
// again" when Twitch can't be asked.
func TestFollowAndAccountRules(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	now := time.Now()
	r.chat.follows = map[string]time.Time{"id-ann": now.AddDate(0, 0, -10), "id-cat": now.AddDate(0, 0, -2), "id-dan": now.AddDate(0, 0, -30)}
	r.chat.made = map[string]time.Time{"id-dan": now.AddDate(0, 0, -5)}
	g, err := r.svc.Open(ctx, Settings{Title: "a key", Rules: Rules{FollowDays: 7, AccountDays: 30}}, "app")
	if err != nil || !g.Rules.Followers {
		t.Fatal("follow days imply following:", g.Rules, err)
	}
	r.eventually("the opening", func() bool {
		return strings.Contains(r.chat.all(), "Open to followers of 7 days or more; Twitch accounts at least 30 days old. One entry each")
	})
	for _, v := range []string{"ann", "bob", "cat", "dan"} {
		r.say(v, "join", false)
	}
	ann := r.in(g.ID, "ann")
	if ann.Qualified == nil || ann.Qualified.FollowedAt == nil || ann.Qualified.AccountCreatedAt == nil {
		t.Fatal("what ann was checked against:", ann.Qualified)
	}
	r.refused(g.ID, "bob", ReasonFollowers)
	r.refused(g.ID, "cat", ReasonFollowDays)
	r.refused(g.ID, "dan", ReasonAccountDays)
	r.eventually("reasons in chat", func() bool {
		all := r.chat.all()
		return strings.Contains(all, "@bob: a key is for followers. Follow, then type !join.") &&
			strings.Contains(all, "@cat: a key is for followers of 7 days or more.") &&
			strings.Contains(all, "@dan: a key is for Twitch accounts at least 30 days old.")
	})
	// Asking again with the same answer isn't said again.
	r.say("bob", "join", false)
	r.eventually("bob checked again", func() bool { return r.chat.calls() >= 6 })
	time.Sleep(150 * time.Millisecond)
	if n := r.count("@bob"); n != 1 {
		t.Fatalf("bob was told %d times:\n%s", n, r.chat.all())
	}
	// Following afterwards lets them in, and the refusal goes.
	r.chat.mu.Lock()
	r.chat.follows["id-bob"] = now.AddDate(-1, 0, 0)
	r.chat.mu.Unlock()
	r.say("bob", "join", false)
	r.in(g.ID, "bob")
	if r.refusal(g.ID, "bob") != "" {
		t.Fatal("bob is in but still refused")
	}
	// Twitch can't be asked: turned away with "try again", never guessed.
	r.chat.mu.Lock()
	r.chat.factsErr = errors.New("twitch down")
	r.chat.mu.Unlock()
	r.say("eve", "join", false)
	r.refused(g.ID, "eve", ReasonUnchecked)
	r.eventually("try again", func() bool {
		return strings.Contains(r.chat.all(), "@eve: Twitch couldn't be asked just now, so you're not in a key yet. Type !join to try again.")
	})
	r.chat.mu.Lock()
	r.chat.factsErr = nil
	r.chat.follows["id-eve"] = now.AddDate(-1, 0, 0)
	r.chat.mu.Unlock()
	r.say("eve", "join", false)
	r.in(g.ID, "eve")
	// The entry list's fingerprint is the same shape as without rules.
	closed, err := r.svc.CloseEntries(ctx, g.ID)
	if err != nil || closed.EntryCount != 3 {
		t.Fatal(closed, err)
	}
	d := r.detail(g.ID)
	if fp := Fingerprint(d.Entries, false); fp != closed.Fingerprint {
		t.Fatal("fingerprint:", fp, closed.Fingerprint)
	}
	// Rules are fixed once open.
	if _, err := r.db.ExecContext(ctx, "UPDATE giveaways SET rules = '{}' WHERE id = ?", g.ID); err == nil {
		t.Fatal("rules changed")
	}
	if _, err := r.db.ExecContext(ctx, "UPDATE giveaway_entries SET qualified = '{}' WHERE giveaway_id = ?", g.ID); err == nil {
		t.Fatal("what an entry was checked against changed")
	}
}

// Rules read from chat and the record: months subscribed (from the
// badge), messages this stream before entering, and recent wins.
func TestSubMessagesAndWinRules(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// frank won last week.
	prev, _ := r.svc.Open(ctx, Settings{Title: "before"}, "app")
	r.say("frank", "join", false)
	r.in(prev.ID, "frank")
	if _, err := r.svc.Draw(ctx, prev.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.End(ctx, prev.ID); err != nil {
		t.Fatal(err)
	}
	// A stream start, then chat.
	_, _ = r.inbox.Accept(ctx, events.Envelope{Type: "broadcast.channel.online", Source: "twitch", Payload: []byte(`{}`),
		DeliveryQuality: events.Confirmed, ReceivedAt: r.now()})
	for i := 0; i < 2; i++ {
		r.talk("ann", "hello", false, nil)
		r.talk("bob", "hi", false, nil)
	}
	r.talk("ann", "!lurk", false, nil) // commands aren't chatting
	sub := func(months string) map[string]any {
		return map[string]any{"badges": []map[string]string{{"set_id": "subscriber", "id": "0", "info": months}}}
	}
	g, err := r.svc.Open(ctx, Settings{Title: "now", Rules: Rules{SubMonths: 3, Messages: 2, WinCooldownDays: 7}}, "app")
	if err != nil || !g.SubscribersOnly {
		t.Fatal("months subscribed imply subscribers only:", g, err)
	}
	r.send("chat.command.received", "ann", "!join", "join", false, sub("6"), "subscriber")
	r.send("chat.command.received", "bob", "!join", "join", false, sub("2"), "subscriber")
	r.send("chat.command.received", "cat", "!join", "join", false, sub("12"), "subscriber")
	r.send("chat.command.received", "frank", "!join", "join", false, sub("12"), "subscriber")
	r.send("chat.command.received", "gus", "!join", "join", false, nil)
	ann := r.in(g.ID, "ann")
	if ann.Qualified == nil || ann.Qualified.SubMonths != 6 || ann.Qualified.Messages == nil || *ann.Qualified.Messages != 2 {
		t.Fatal(ann.Qualified)
	}
	r.refused(g.ID, "bob", ReasonSubMonths)
	r.refused(g.ID, "cat", ReasonMessages)
	r.refused(g.ID, "gus", ReasonSubscribers)
	// frank chatted 0 times too, but the win is checked first.
	r.refused(g.ID, "frank", ReasonRecentWin)
	if r.chat.calls() != 0 {
		t.Fatal("asked Twitch with no Twitch rule:", r.chat.calls())
	}
	r.eventually("reasons in chat", func() bool {
		all := r.chat.all()
		return strings.Contains(all, "@bob: now is for subscribers of 3 months or more.") &&
			strings.Contains(all, "@cat: now needs 2 messages in chat this stream first. Type !join to try again.") &&
			strings.Contains(all, "@frank: you won a giveaway in the last 7 days, so you can't enter now.")
	})
	// cat chats, then tries again.
	r.talk("cat", "hey all", false, nil)
	r.talk("cat", "good luck", false, nil)
	r.send("chat.command.received", "cat", "!join", "join", false, sub("12"), "subscriber")
	r.in(g.ID, "cat")
}

// Entering by chatting: any message enters; the command still works; a
// message from someone turned away isn't checked with Twitch again, but
// the messages rule is checked again as they chat.
func TestEnterByChat(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.chat.follows = map[string]time.Time{"id-ann": time.Now().AddDate(-1, 0, 0), "id-cat": time.Now().AddDate(-1, 0, 0)}
	g, err := r.svc.Open(ctx, Settings{Title: "chatty", EnterByChat: true, Rules: Rules{Followers: true}}, "app")
	if err != nil {
		t.Fatal(err)
	}
	r.eventually("the opening", func() bool { return strings.Contains(r.chat.all(), "Say anything in chat (or type !join) to enter") })
	r.talk("ann", "hi!", false, nil)
	r.in(g.ID, "ann")
	r.talk("bob", "hello", false, nil)
	r.refused(g.ID, "bob", ReasonFollowers)
	calls := r.chat.calls()
	for i := 0; i < 5; i++ {
		r.talk("bob", "still here", false, nil)
	}
	r.talk("cat", "!join", false, nil) // the command's own message: entered by the command event
	r.say("cat", "join", false)
	r.in(g.ID, "cat")
	time.Sleep(150 * time.Millisecond)
	if got := r.chat.calls(); got != calls+1 {
		t.Fatalf("Twitch asked %d times after bob was turned away; want once, for cat", got-calls)
	}
	if n := len(r.detail(g.ID).Entries); n != 2 {
		t.Fatal(n)
	}

	// The messages rule: checked again as they chat, without the command.
	if _, err := r.svc.Cancel(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = r.svc.Open(ctx, Settings{Title: "talkers", EnterByChat: true, Rules: Rules{Messages: 2}}, "app")
	r.talk("dan", "one", false, nil)
	r.refused(g.ID, "dan", ReasonMessages)
	r.talk("dan", "two", false, nil)
	r.talk("dan", "three", false, nil)
	r.in(g.ID, "dan")
}

// A practice giveaway's rules read the test chatter's facts and never ask
// Twitch.
func TestPracticeRules(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, err := r.svc.Open(ctx, Settings{Title: "p", Practice: true, Rules: Rules{FollowDays: 7, AccountDays: 30}}, "app")
	if err != nil {
		t.Fatal(err)
	}
	facts := func(follow, account int) map[string]any {
		return map[string]any{"practice": map[string]int{"follow_days": follow, "account_days": account}}
	}
	r.send("chat.command.received", "ann", "!join", "join", true, facts(30, 365))
	r.send("chat.command.received", "bob", "!join", "join", true, facts(-1, 365))
	r.send("chat.command.received", "cat", "!join", "join", true, facts(3, 365))
	r.send("chat.command.received", "dan", "!join", "join", true, facts(30, 2))
	r.send("chat.command.received", "eve", "!join", "join", true, nil) // no facts: not guessed
	r.in(g.ID, "ann")
	r.refused(g.ID, "bob", ReasonFollowers)
	r.refused(g.ID, "cat", ReasonFollowDays)
	r.refused(g.ID, "dan", ReasonAccountDays)
	r.refused(g.ID, "eve", ReasonUnchecked)
	if r.chat.calls() != 0 {
		t.Fatal("a practice giveaway asked Twitch")
	}
}

func TestRuleLimits(t *testing.T) {
	r := newRig(t)
	for _, rules := range []Rules{{FollowDays: -1}, {FollowDays: MaxRuleDays + 1}, {AccountDays: -1}, {SubMonths: MaxSubMonths + 1},
		{Messages: MaxMessages + 1}, {WinCooldownDays: MaxCooldownDays + 1}} {
		if _, err := r.svc.Open(context.Background(), Settings{Title: "x", Rules: rules}, "app"); !errors.Is(err, ErrInvalid) {
			t.Error(rules, err)
		}
	}
}
