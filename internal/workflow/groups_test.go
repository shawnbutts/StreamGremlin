package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/events"
)

func groupDef(id, group, command string) []byte {
	return def(id, `"group":`+jsonString(group)+`,"triggers":[{"type":"chat.command.received","where":{"command":"`+command+`"}}],
		"steps":[{"action":"test.send","with":{"message":"`+id+`"}}]`)
}

func jsonString(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + s + `"`
}

func (r *rig) summary(id string) Summary {
	r.t.Helper()
	s, err := r.eng.Get(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

// A group's switch leaves each workflow's alone: off stops them all, on
// restores exactly the ones that were on.
func TestAGroupSwitchKeepsEachWorkflowsSwitch(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.save(groupDef("alert.follow", "Alerts", "a1"), true)
	r.save(groupDef("alert.sub", "alerts", "a2"), false) // joins "Alerts", whatever the case
	r.save(groupDef("chat.hi", "", "hi"), true)
	gs, err := r.eng.Groups(ctx)
	if err != nil || len(gs) != 1 || gs[0].Name != "Alerts" || gs[0].Workflows != 2 || gs[0].On != 1 || !gs[0].Enabled {
		t.Fatalf("%+v %v", gs, err)
	}
	if s := r.summary("alert.sub"); s.Group != "Alerts" {
		t.Fatal("the group's spelling wasn't kept:", s.Group)
	}
	if err := r.eng.SetGroupEnabled(ctx, "ALERTS", false); err != nil {
		t.Fatal(err)
	}
	if s := r.summary("alert.follow"); !s.Enabled || s.GroupEnabled || s.Running {
		t.Fatalf("%+v", s)
	}
	r.chat("g1", "a1", events.Confirmed)
	r.chat("g2", "hi", events.Confirmed)
	r.jobs("chat.hi", 1)
	if js, _ := r.eng.Jobs(ctx, "alert.follow", 10); len(js) != 0 {
		t.Fatal("ran with its group off:", js)
	}
	if st, _ := r.eng.Stats(ctx); st.On != 1 {
		t.Fatalf("Home counts %d on", st.On)
	}
	// Turning one on inside a group that's off doesn't make it run.
	if err := r.eng.SetEnabled(ctx, "alert.sub", true); err != nil {
		t.Fatal(err)
	}
	if s := r.summary("alert.sub"); s.Running {
		t.Fatal("runs with its group off")
	}
	if err := r.eng.SetGroupEnabled(ctx, "Alerts", true); err != nil {
		t.Fatal(err)
	}
	r.chat("g3", "a1", events.Confirmed)
	r.jobs("alert.follow", 1)
	if gs, _ := r.eng.Groups(ctx); gs[0].On != 2 || !gs[0].Enabled {
		t.Fatalf("%+v", gs)
	}
	if err := r.eng.SetGroupEnabled(ctx, "nope", true); !errors.Is(err, ErrGroupNotFound) {
		t.Fatal(err)
	}
}

// One answer per command: a group that would make two workflows answer
// one command can't come on, and says which.
func TestAGroupCantComeOnToAnswerATakenCommand(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.save(groupDef("fun.dice", "Fun", "roll"), true)
	if err := r.eng.SetGroupEnabled(ctx, "Fun", false); err != nil {
		t.Fatal(err)
	}
	// While Fun is off, another workflow may take !roll.
	r.save(groupDef("games.roll", "", "roll"), true)
	err := r.eng.SetGroupEnabled(ctx, "Fun", true)
	var taken *commands.TakenError
	if !errors.As(err, &taken) || len(taken.Conflicts) != 1 || taken.Conflicts[0].Command != "roll" {
		t.Fatalf("%v", err)
	}
	if gs, _ := r.eng.Groups(ctx); gs[0].Enabled {
		t.Fatal("came on anyway")
	}
	// Two in the same group, both on while it's off, can't come on together.
	r.save(groupDef("fun.flip", "Fun", "flip"), true)
	r.save(groupDef("fun.flip2", "Fun", "flip"), true)
	if err := r.eng.SetEnabled(ctx, "games.roll", false); err != nil {
		t.Fatal(err)
	}
	if err := r.eng.SetGroupEnabled(ctx, "Fun", true); !errors.As(err, &taken) || taken.Conflicts[0].Command != "flip" {
		t.Fatalf("%v", err)
	}
	// Moving a workflow that's on into a group that's on is checked too.
	r.save(groupDef("solo.flip", "", "coin"), true)
	_, _, err = r.eng.Save(ctx, groupDef("games.roll", "Other", "roll"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.eng.SetEnabled(ctx, "fun.flip2", false); err != nil {
		t.Fatal(err)
	}
	if err := r.eng.SetGroupEnabled(ctx, "Fun", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.eng.Save(ctx, groupDef("solo.flip", "Fun", "flip")); !errors.As(err, &taken) {
		t.Fatalf("moved into a group to answer a taken command: %v", err)
	}
}

// Renaming moves every workflow (a new version each), keeps the switch,
// and refuses another group's name. Groups nobody's in are forgotten.
func TestRenamingAndForgettingGroups(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.save(groupDef("a.one", "Alerts", "x1"), true)
	r.save(groupDef("a.two", "Alerts", "x2"), true)
	r.save(groupDef("b.one", "Thanks", "x3"), true)
	if err := r.eng.SetGroupEnabled(ctx, "Alerts", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.eng.RenameGroup(ctx, "Alerts", "thanks"); !errors.Is(err, ErrGroupExists) {
		t.Fatal(err)
	}
	if _, err := r.eng.RenameGroup(ctx, "Alerts", "bad/name"); err == nil {
		t.Fatal("a bad name")
	}
	name, err := r.eng.RenameGroup(ctx, "alerts", "On screen")
	if err != nil || name != "On screen" {
		t.Fatal(name, err)
	}
	s := r.summary("a.one")
	if s.Group != "On screen" || s.GroupEnabled || s.Version != 2 {
		t.Fatalf("%+v", s)
	}
	gs, _ := r.eng.Groups(ctx)
	if len(gs) != 2 || gs[0].Name != "On screen" || gs[0].Enabled || gs[1].Name != "Thanks" {
		t.Fatalf("%+v", gs)
	}
	// A change of case only.
	if _, err := r.eng.RenameGroup(ctx, "On screen", "On Screen"); err != nil {
		t.Fatal(err)
	}
	// Moving the last one out forgets the group; deleting does too.
	r.save(groupDef("b.one", "", "x3"), true)
	if err := r.eng.Delete(ctx, "a.one"); err != nil {
		t.Fatal(err)
	}
	if err := r.eng.Delete(ctx, "a.two"); err != nil {
		t.Fatal(err)
	}
	if gs, _ := r.eng.Groups(ctx); len(gs) != 0 {
		t.Fatalf("%+v", gs)
	}
	// Restored, a workflow brings its group back.
	if err := r.eng.Restore(ctx, "a.one"); err != nil {
		t.Fatal(err)
	}
	if gs, _ := r.eng.Groups(ctx); len(gs) != 1 || gs[0].Name != "On Screen" {
		t.Fatalf("%+v", gs)
	}
}

func TestGroupNamesAreChecked(t *testing.T) {
	r := newRig(t)
	for _, g := range []string{" Alerts", "Alerts ", "a/b", strings.Repeat("x", 41), "<b>"} {
		if _, _, err := r.eng.Save(context.Background(), groupDef("x.y", g, "z")); err == nil {
			t.Errorf("%q accepted", g)
		}
	}
	for _, g := range []string{"Alerts", "Thanks & raids", "Café", "1st-timers"} {
		if _, _, err := r.eng.Save(context.Background(), groupDef("x.y", g, "z")); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}
