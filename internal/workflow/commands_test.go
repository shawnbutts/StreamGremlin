package workflow

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/events"
)

func cmdDef(id, command, extra string) []byte {
	return def(id, `"triggers":[{"type":"chat.command.received","where":{"command":"`+command+`"`+extra+`}}],"steps":[{"action":"test.local"}]`)
}

func TestOneAnswerPerCommand(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.save(cmdDef("hello.one", "hello", ""), true)
	r.save(cmdDef("hello.two", "hello", ""), false)

	// Turning on a second answer is refused, naming the first.
	err := r.eng.SetEnabled(ctx, "hello.two", true)
	var taken *commands.TakenError
	if !errors.As(err, &taken) || len(taken.Conflicts) != 1 || taken.Conflicts[0].Owner != "hello.one" ||
		!strings.Contains(err.Error(), "!hello is already answered by “Test hello.one”") {
		t.Fatalf("%v", err)
	}
	if s, _ := r.eng.Get(ctx, "hello.two"); s.Enabled {
		t.Fatal("turned on anyway")
	}
	// A workflow that's off can be saved with any command.
	if _, _, err := r.eng.Save(ctx, cmdDef("hello.two", "hello", `,"chatter.role":"vip"`)); err != nil {
		t.Fatal(err)
	}

	// Chatters that never meet: mods and viewers can each have !so.
	r.save(cmdDef("so.mods", "so", `,"chatter.role":["moderator","broadcaster"]`), true)
	r.save(cmdDef("so.viewers", "so", `,"chatter.role":["viewer","subscriber","vip"]`), true)
	r.save(cmdDef("so.vip", "so", `,"chatter.role":"vip"`), false)
	if err := r.eng.SetEnabled(ctx, "so.vip", true); !errors.As(err, &taken) {
		t.Fatalf("vips overlap viewers-and-vips: %v", err)
	}
	// Different chats never meet either; no limit means every chat.
	r.save(def("raid.tw", `"triggers":[{"type":"chat.command.received","sources":["twitch"],"where":{"command":"raid"}}],"steps":[{"action":"test.local"}]`), true)
	r.save(def("raid.yt", `"triggers":[{"type":"chat.command.received","sources":["youtube"],"where":{"command":"raid"}}],"steps":[{"action":"test.local"}]`), true)
	r.save(def("raid.any", `"triggers":[{"type":"chat.command.received","where":{"command":"raid"}}],"steps":[{"action":"test.local"}]`), false)
	if err := r.eng.SetEnabled(ctx, "raid.any", true); !errors.As(err, &taken) || len(taken.Conflicts) != 2 {
		t.Fatalf("every chat overlaps both: %v", err)
	}

	// A change to a workflow that's on is checked for the commands it adds.
	if _, _, err := r.eng.Save(ctx, def("hello.one", `"triggers":[{"type":"chat.command.received","where":{"command":["hello","so"]}}],"steps":[{"action":"test.local"}]`)); !errors.As(err, &taken) || taken.Conflicts[0].Command != "so" {
		t.Fatalf("adding !so: %v", err)
	}
	// So is opening a command to chatters it overlaps with.
	if _, _, err := r.eng.Save(ctx, cmdDef("so.viewers", "so", "")); !errors.As(err, &taken) || taken.Conflicts[0].Owner != "so.mods" {
		t.Fatalf("widening !so to everyone: %v", err)
	}
	// A pattern that reaches commands counts; a trigger with no command claims none.
	r.save(def("logger", `"triggers":[{"type":"chat.command.received"}],"steps":[{"action":"test.local"}]`), true)
	r.save(def("star", `"triggers":[{"type":"chat.**","where":{"command":"hello"}}],"steps":[{"action":"test.local"}]`), false)
	if err := r.eng.SetEnabled(ctx, "star", true); !errors.As(err, &taken) {
		t.Fatalf("chat.** with !hello: %v", err)
	}

	// An overlap from before the check (here, made with the check bypassed)
	// is listed, and doesn't block an unrelated edit.
	if _, err := r.db.ExecContext(ctx, "UPDATE workflows SET enabled = 1 WHERE id = 'hello.two'"); err != nil {
		t.Fatal(err)
	}
	if err := r.eng.reload(ctx); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(cmdDef("hello.two", "hello", "")), `"steps":[{"action":"test.local"}]`, `"steps":[{"action":"test.local"},{"action":"test.local"}]`, 1)
	if _, _, err := r.eng.Save(ctx, []byte(edited)); err != nil {
		t.Fatalf("an unrelated edit was blocked by an old overlap: %v", err)
	}
	table, err := r.eng.Commands(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]commands.Entry{}
	for _, e := range table {
		got[e.Command] = e
	}
	if !got["hello"].Conflict || got["so"].Conflict || len(got["so"].Owners) != 3 || got["raid"].Conflict {
		t.Fatalf("%+v", table)
	}
	// Turning one off resolves it.
	_ = r.eng.SetEnabled(ctx, "hello.two", false)
	if table, _ := r.eng.Commands(ctx); table[0].Command != "hello" || table[0].Conflict {
		t.Fatalf("%+v", table)
	}
}

func TestTwoTurnedOnAtOnceCantBothTakeACommand(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for i := range 6 {
		r.save(cmdDef("race."+strconv.Itoa(i), "race", ""), false)
	}
	errs := make(chan error, 6)
	for i := range 6 {
		go func() { errs <- r.eng.SetEnabled(ctx, "race."+strconv.Itoa(i), true) }()
	}
	ok := 0
	for range 6 {
		if <-errs == nil {
			ok++
		}
	}
	list, _ := r.eng.List(ctx, false)
	on := 0
	for _, s := range list {
		if s.Enabled {
			on++
		}
	}
	if ok != 1 || on != 1 {
		t.Fatalf("%d turned on (%d said ok); want 1", on, ok)
	}
}

func TestStatsForHome(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	st, err := r.eng.Stats(ctx)
	if err != nil || st != (Stats{}) {
		t.Fatalf("%+v %v", st, err)
	}
	r.save(cmdDef("a", "a", ""), true)
	r.save(def("b", `"triggers":[{"type":"chat.command.received","where":{"command":"b"}}],"steps":[{"action":"test.fail"}]`), true)
	r.save(cmdDef("c", "c", ""), false)
	r.chat("t1", "a", events.Synthetic)
	r.jobs("a", 1)
	if st, _ := r.eng.Stats(ctx); st.Total != 3 || st.On != 2 || !st.Tested || st.Answered || st.Failed24h != 0 {
		t.Fatalf("after a test: %+v", st)
	}
	r.chat("b1", "b", events.Confirmed) // fails
	r.jobs("b", 1)
	if st, _ := r.eng.Stats(ctx); st.Answered || st.Failed24h != 1 {
		t.Fatalf("a failed real run isn't an answer: %+v", st)
	}
	r.chat("a1", "a", events.Confirmed)
	r.jobs("a", 2)
	_ = r.eng.Delete(ctx, "c")
	if st, _ := r.eng.Stats(ctx); !st.Answered || st.Total != 2 {
		t.Fatalf("%+v", st)
	}
}
