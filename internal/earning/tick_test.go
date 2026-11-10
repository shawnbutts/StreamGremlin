package earning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

func actor(id string) events.Actor {
	return events.Actor{Platform: "twitch", ID: id, Login: "u" + id, DisplayName: "U" + id}
}

func (f *fixture) tick(t *testing.T, sec int) Tick {
	t.Helper()
	got, err := f.s.Tick(context.Background(), f.t0.Add(time.Duration(sec)*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func timeOn(r *Rules) { r.Time.On = true }

// While live: viewers who chatted in the window earn the chatting amount,
// once, even when also on the chatter list; quiet viewers on the list earn
// less; the streamer's chat, test chat and chat before the window don't
// count. The same tick again pays nothing more.
func TestTimeInChat(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, func(r *Rules) { r.Time.On = true; r.Chat.Amount = 0 })
	f.event(t, "broadcast.channel.online", "", 0, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "1", 0, `{}`, events.Confirmed)     // 15 minutes before the tick: too long ago
	f.event(t, "chat.message.received", "2", 600, `{}`, events.Confirmed)   // in the window
	f.event(t, "chat.message.received", "3", 650, `{}`, events.Synthetic)   // a test
	f.event(t, "chat.message.received", "100", 700, `{}`, events.Confirmed) // the streamer
	f.list.list = []events.Actor{actor("1"), actor("2"), actor("4")}
	got := f.tick(t, 900)
	if got.Active != 1 || got.Quiet != 2 || got.Note != "" {
		t.Fatalf("%+v", got)
	}
	for id, want := range map[string]int64{"1": 3, "2": 10, "3": 0, "4": 3, "100": 0} {
		if b := f.balance(t, id); b != want {
			t.Errorf("viewer %s has %d, want %d", id, b, want)
		}
	}
	again := f.tick(t, 900)
	if !again.At.Equal(got.At) || again.Active != got.Active || again.Quiet != got.Quiet || f.balance(t, "2") != 10 {
		t.Fatalf("a repeat: %+v", again)
	}
	if last, err := f.s.LastTick(context.Background()); err != nil || last == nil || last.Active != 1 {
		t.Fatal(last, err)
	}
	// The next interval pays again; a chat exactly the window's length ago
	// no longer counts as chatting.
	if got := f.tick(t, 1200); got.Active != 0 || got.Quiet != 3 || f.balance(t, "2") != 13 {
		t.Fatalf("%+v", got)
	}
	if problems, _ := f.ledger.Audit(context.Background()); len(problems) != 0 {
		t.Fatal(problems)
	}
}

// Nothing is paid offline or when it's unknown whether you're live, and
// the tick says why; with it off, nothing is asked of Twitch.
func TestTimeInChatOnlyLive(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.list.list = []events.Actor{actor("4")}
	f.on(t, nil) // earning on, time in chat off
	if got := f.tick(t, 300); got.Note != NoteOff || f.list.calls != 0 {
		t.Fatalf("%+v", got)
	}
	f.on(t, timeOn)
	if got := f.tick(t, 600); got.Note != NoteUnknownLive || got.Quiet != 0 {
		t.Fatalf("%+v", got)
	}
	f.event(t, "broadcast.stream.stopped", "", 650, `{}`, events.Confirmed)
	if got := f.tick(t, 900); got.Note != NoteNotLive {
		t.Fatalf("%+v", got)
	}
	if b := f.balance(t, "4"); b != 0 || f.list.calls != 0 {
		t.Fatal(b, f.list.calls)
	}
}

// When Twitch's list can't be read, chatters are still paid and the tick
// says why the quiet ones weren't. A quiet amount of 0 never asks Twitch.
func TestTimeInChatWithoutTheList(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, timeOn)
	f.event(t, "broadcast.stream.started", "", 0, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "2", 100, `{}`, events.Confirmed)
	f.list.err = errors.New("needs moderator:read:chatters")
	got := f.tick(t, 300)
	if got.Active != 1 || got.Quiet != 0 || !strings.Contains(got.Note, "only viewers who chatted earned") ||
		!strings.Contains(got.Note, "moderator:read:chatters") {
		t.Fatalf("%+v", got)
	}
	f.on(t, func(r *Rules) { r.Time.On = true; r.Time.Quiet = 0 })
	calls := f.list.calls
	if got := f.tick(t, 600); got.Active != 1 || got.Note != "" || f.list.calls != calls {
		t.Fatalf("%+v", got)
	}
}

// The loop pays the interval that just ended, once; never one that ended
// before StreamGremlin started, or one that's too late (the computer
// slept).
func TestTickLoop(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, timeOn)
	f.event(t, "broadcast.channel.online", "", 0, `{}`, events.Confirmed)
	f.list.list = []events.Actor{actor("4")}
	ctx := context.Background()
	f.now = f.t0.Add(301 * time.Second) // 1 second after an interval ended
	last := f.now.Truncate(5 * time.Minute)
	if got := f.s.maybeTick(ctx, last); !got.Equal(last) || f.balance(t, "4") != 0 {
		t.Fatal("paid an interval that ended before the start")
	}
	f.now = f.t0.Add(601 * time.Second)
	last = f.s.maybeTick(ctx, last)
	if f.balance(t, "4") != 3 {
		t.Fatal("not paid")
	}
	if again := f.s.maybeTick(ctx, last); !again.Equal(last) || f.balance(t, "4") != 3 {
		t.Fatal("paid twice")
	}
	f.now = f.t0.Add(1200*time.Second + 2*time.Minute) // woke late
	last = f.s.maybeTick(ctx, last)
	if f.balance(t, "4") != 3 || !last.Equal(f.t0.Add(1200*time.Second)) {
		t.Fatal("paid a late interval")
	}
}

func TestTimeChecks(t *testing.T) {
	for _, change := range []func(*Rules){
		func(r *Rules) { r.Time.EveryMinutes = 0 },
		func(r *Rules) { r.Time.EveryMinutes = 61 },
		func(r *Rules) { r.Time.ActiveMinutes = 0 },
		func(r *Rules) { r.Time.Quiet = -1 },
	} {
		r := Defaults()
		change(&r)
		if err := r.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", r.Time, err)
		}
	}
}
