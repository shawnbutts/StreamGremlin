package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/variables"
)

// chatAs stores a chat command from a chatter with a role.
func (r *rig) chatAs(id, command, role string) {
	r.t.Helper()
	p, _ := json.Marshal(map[string]any{"command": command, "chatter": map[string]any{"role": role}})
	if _, err := r.inbox.Accept(context.Background(), events.Envelope{Type: "chat.command.received", Source: "twitch",
		SourceEventID: id, Payload: p, DeliveryQuality: events.Confirmed,
		Actor: &events.Actor{Platform: "twitch", ID: "1", Login: "fan", DisplayName: "Fan"}}); err != nil {
		r.t.Fatal(err)
	}
}

func TestAWhereListMatchesAnyOfItsValues(t *testing.T) {
	r := newRig(t)
	r.save(def("mods", `"triggers":[{"type":"chat.command.received","where":{"command":"x","chatter.role":["moderator","broadcaster"]}}],
		"steps":[{"action":"test.send","with":{"message":"${event.payload.chatter.role}"}}]`), true)
	r.chatAs("1", "x", "viewer")
	r.chatAs("2", "x", "moderator")
	r.chatAs("3", "x", "vip")
	r.chatAs("4", "x", "broadcaster")
	r.jobs("mods", 2)
	if got := strings.Join(r.messages(), " "); got != "moderator broadcaster" {
		t.Fatalf("ran for %q", got)
	}
	for _, bad := range []string{`[]`, `[{"a":1}]`, `[` + strings.Repeat(`"x",`, MaxWhereAny) + `"x"]`} {
		_, err := Parse(def("bad", `"triggers":[{"type":"chat.command.received","where":{"chatter.role":`+bad+`}}],"steps":[{"action":"test.local"}]`), r.reg)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "where.chatter.role") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

const helper = `"triggers":[{"type":"never.happens.at_all"}],
	"steps":[{"id":"n","action":"vars.add","with":{"scope":"workflow","name":"calls"}},
	         {"set":{"greeting":"'Hi ' + args.who + ' (' + string(saved.workflow.calls) + ')'"}},
	         {"action":"test.send","with":{"message":"${vars.greeting} for ${event.actor.login}"}}]`

func TestRunAnotherWorkflow(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.save(def("greet", helper), true)
	r.save(def("caller", `"triggers":[{"type":"chat.command.received","where":{"command":"hi"}}],
		"steps":[{"set":{"greeting":"'mine'"}},
		         {"id":"call","action":"workflow.run","with":{"workflow":"greet","args":{"who":"${event.actor.display_name}"}}},
		         {"action":"test.send","with":{"message":"${vars.greeting} / ${steps.call.output.vars.greeting} / ${args.size()}"}}]`), true)
	r.chat("c1", "hi", events.Confirmed)
	js := r.jobs("caller", 1)
	if js[0].Status != JobSucceeded {
		t.Fatalf("%+v", js[0])
	}
	// The helper sees the same event and its args; its vars stay its own.
	if got := strings.Join(r.messages(), " | "); got != "Hi Fan (1) for fan | mine / Hi Fan (1) / 0" {
		t.Fatalf("messages %q", got)
	}
	// Its workflow variables are the helper's own.
	if v := r.saved(variables.Key{Scope: variables.Workflow, Owner: "greet", Name: "calls"}); v != int64(1) {
		t.Fatalf("helper's calls = %v", v)
	}
	// Its steps are in the caller's trace, under the calling step, with
	// their own idempotency keys.
	_, steps, _ := r.eng.Trace(ctx, js[0].ID)
	var paths []string
	for _, s := range steps {
		paths = append(paths, s.Path)
	}
	want := "steps[0] steps[1]>greet:steps[0] steps[1]>greet:steps[1] steps[1]>greet:steps[2] steps[1] steps[2]"
	if strings.Join(paths, " ") != want {
		t.Fatalf("trace paths %v", paths)
	}
	if key := steps[3].Output.(map[string]any)["key"]; key != js[0].ID+"/steps[1]>greet:steps[2]" {
		t.Fatalf("idempotency key %v", key)
	}
	// The helper isn't run by its own trigger: it has none that fire.
	if j, _ := r.eng.Jobs(ctx, "greet", 10); len(j) != 0 {
		t.Fatal(j)
	}
}

func TestRunAnotherWorkflowRefusesLoopsDepthOffAndComputedNames(t *testing.T) {
	r := newRig(t)
	call := func(id, target string) {
		r.save(def(id, `"triggers":[{"type":"chat.command.received","where":{"command":"`+id+`"}}],
			"steps":[{"action":"workflow.run","with":{"workflow":"`+target+`"}}]`), true)
	}
	call("a", "b")
	call("b", "a")
	r.chat("l1", "a", events.Confirmed)
	if js := r.jobs("a", 1); js[0].Status != JobFailed || !strings.Contains(js[0].Error, "a loop: a → b → a") {
		t.Fatalf("%+v", js[0])
	}
	for i := 1; i <= 5; i++ {
		call(fmt.Sprint("d", i), fmt.Sprint("d", i+1))
	}
	r.save(def("d6", `"triggers":[{"type":"never.happens.at_all"}],"steps":[{"action":"test.local"}]`), true)
	r.chat("deep", "d1", events.Confirmed)
	if js := r.jobs("d1", 1); js[0].Status != JobFailed || !strings.Contains(js[0].Error, "more than 4 deep") {
		t.Fatalf("%+v", js[0])
	}
	r.chat("ok", "d2", events.Confirmed) // four below: allowed
	if js := r.jobs("d2", 1); js[0].Status != JobSucceeded {
		t.Fatalf("%+v", js[0])
	}
	r.save(def("off", `"triggers":[{"type":"never.happens.at_all"}],"steps":[{"action":"test.local"}]`), false)
	call("calls.off", "off")
	call("calls.nothing", "nothing.here")
	r.chat("o1", "calls.off", events.Confirmed)
	r.chat("o2", "calls.nothing", events.Confirmed)
	if js := r.jobs("calls.off", 1); !strings.Contains(js[0].Error, "is off") {
		t.Fatalf("%+v", js[0])
	}
	if js := r.jobs("calls.nothing", 1); !strings.Contains(js[0].Error, `no workflow "nothing.here"`) {
		t.Fatalf("%+v", js[0])
	}
	_, err := Parse(def("chosen", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"workflow.run","with":{"workflow":"${event.payload.args}"}}]`), r.reg)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not computed") {
		t.Fatalf("chat chose the workflow: %v", err)
	}
}

func TestACalledWorkflowFollowsTheDryRunRule(t *testing.T) {
	r := newRig(t)
	r.save(def("greet", helper), true)
	r.save(def("caller", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"workflow.run","with":{"workflow":"greet","args":{"who":"T"}}}]`), true)
	r.chat("t1", "hi", events.Synthetic)
	if js := r.jobs("caller", 1); js[0].Status != JobSucceeded || !js[0].DryRun {
		t.Fatalf("%+v", js[0])
	}
	if len(r.rec.got()) != 0 {
		t.Fatal("a test run's called workflow reached outside")
	}
	if v := r.saved(variables.Key{Scope: variables.Workflow, Owner: "greet", Name: "calls"}); v != nil {
		t.Fatalf("a test run saved %v", v)
	}
}

func TestDeleteAndRestore(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.rec.gate = make(chan struct{})
	r.save(def("slow", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"vars.set","with":{"scope":"workflow","name":"kept","value":1}},{"action":"test.slow"}],
		"finally":[{"action":"test.local","with":{"value":"cleaned up"}}]`), true)
	r.chat("s1", "x", events.Confirmed)
	r.chat("s2", "x", events.Confirmed) // waits behind s1
	deadline := time.Now().Add(5 * time.Second)
	for len(r.rec.got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := r.eng.Delete(ctx, "slow"); err != nil {
		t.Fatal(err)
	}
	js := r.jobs("slow", 2)
	for _, j := range js {
		if j.Status != JobCancelled || !strings.Contains(j.Error+j.Reason, "deleted") {
			t.Fatalf("%+v", j)
		}
	}
	_, steps, _ := r.eng.Trace(ctx, js[0].ID)
	if last := steps[len(steps)-1]; last.Path != "finally[0]" {
		t.Fatalf("finally didn't run: %+v", steps)
	}
	if list, _ := r.eng.List(ctx, false); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
	if list, _ := r.eng.List(ctx, true); len(list) != 1 || list[0].DeletedAt == nil || list[0].Enabled {
		t.Fatalf("deleted list: %+v", list)
	}
	if _, err := r.eng.Get(ctx, "slow"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := r.eng.SetEnabled(ctx, "slow", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("turned on while deleted: %v", err)
	}
	if _, _, err := r.eng.Save(ctx, def("slow", `"triggers":[{"type":"chat.command.received"}],"steps":[{"action":"test.local"}]`)); !errors.Is(err, ErrDeleted) {
		t.Fatalf("saved over a deleted workflow: %v", err)
	}
	if err := r.eng.Delete(ctx, "slow"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
	r.chat("s3", "x", events.Confirmed) // never runs while deleted
	time.Sleep(100 * time.Millisecond)
	if js, _ := r.eng.Jobs(ctx, "slow", 10); len(js) != 2 {
		t.Fatalf("ran while deleted: %+v", js)
	}

	if err := r.eng.Restore(ctx, "slow"); err != nil {
		t.Fatal(err)
	}
	s, err := r.eng.Get(ctx, "slow")
	if err != nil || s.Enabled || s.Version != 1 {
		t.Fatalf("restored: %+v %v", s, err)
	}
	if err := r.eng.Restore(ctx, "slow"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restored twice: %v", err)
	}
	// Its own variables came back with it.
	if v := r.saved(variables.Key{Scope: variables.Workflow, Owner: "slow", Name: "kept"}); v != int64(1) {
		t.Fatalf("kept = %v", v)
	}
	close(r.rec.gate)
}

// The editor writes conditions in these forms (cmd/desktop/frontend/src/lib/editor.ts).
func TestEditorConditions(t *testing.T) {
	ctx := context.Background()
	sc := scope{event: map[string]any{"payload": map[string]any{"text": "Hello THERE", "count": int64(5), "price": 2.5, "word": "10"}},
		vars: map[string]any{}, steps: map[string]any{}, job: map[string]any{},
		saved: map[string]any{"global": map[string]any{}, "viewer": map[string]any{}, "workflow": map[string]any{}}}
	cases := map[string]bool{
		`string(event.payload.text) == "Hello THERE"`:                    true,
		`string(event.payload.count) == "5"`:                             true,
		`string(event.payload.text) != "Hello THERE"`:                    false,
		`string(event.payload.text).lowerAscii().contains("there")`:      true,
		`string(event.payload.count).lowerAscii().contains("6")`:         false,
		`double(event.payload.count) > 4`:                                true,
		`double(event.payload.price) < 2`:                                false,
		`double(event.payload.word) > 9.5`:                               true,
		`double(saved.global.?deaths.orValue(0)) > 0`:                    false,
		`string(saved.global.?title.orValue("")) == ""`:                  true,
		`string(event.payload.text).lowerAscii().contains("say \"hi\"")`: false,
	}
	for src, want := range cases {
		got, err := evalBool(ctx, src, sc)
		if err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", src, got, err, want)
		}
	}
	// "Remember a value" stores text with values as a string expression.
	got, err := eval(ctx, `"Hi " + string(event.payload.count) + " \"you\""`, sc)
	if err != nil || got != `Hi 5 "you"` {
		t.Errorf("remember a value: %v %v", got, err)
	}
}

func TestThePaletteMatchesTheEventCatalog(t *testing.T) {
	ctx := context.Background()
	inPalette := map[string]bool{}
	for _, tr := range Triggers() {
		inPalette[tr.Type] = true
		k, ok := events.KindOf(tr.Type)
		if !ok {
			t.Errorf("%s isn't in the event catalog", tr.Type)
			continue
		}
		if tr.Label == "" || tr.Category == "" || tr.Help == "" || tr.Match == nil || tr.Values == nil {
			t.Errorf("%s: incomplete %+v", tr.Type, tr)
		}
		env, err := (events.Simulation{Type: k.Type, RequestID: "p"}).Build()
		if err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{}
		_ = json.Unmarshal(env.Payload, &payload)
		for _, m := range tr.Match {
			if _, ok := lookup(payload, m.Path); !ok {
				t.Errorf("%s: no %s in its payload", tr.Type, m.Path)
			}
			switch m.Kind {
			case "command", "who", "text", "scene", "source", "on_off", "timer", "button":
			default:
				t.Errorf("%s: kind %q", m.Path, m.Kind)
			}
		}
		sc := scope{event: eventMap(events.Record{Seq: 1, Envelope: env}), vars: map[string]any{}, steps: map[string]any{}, job: map[string]any{}}
		for _, v := range tr.Values {
			if _, err := eval(ctx, v.Expr, sc); err != nil || !varPattern.MatchString(v.Token) || v.Label == "" {
				t.Errorf("%s {%s}: %v", tr.Type, v.Token, err)
			}
		}
	}
	for _, k := range events.Catalog() {
		if !inPalette[k.Type] {
			t.Errorf("%s has no palette entry: the editor can't offer it", k.Type)
		}
	}
}
