package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/variables"
)

func (r *rig) saved(k variables.Key) any {
	r.t.Helper()
	v, err := r.vars.Get(context.Background(), k)
	if errors.Is(err, variables.ErrNotFound) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return v.Value
}

func (r *rig) messages() []string {
	var out []string
	for _, c := range r.rec.got() {
		out = append(out, fmt.Sprint(c["message"]))
	}
	return out
}

const counter = `"triggers":[{"type":"chat.command.received","where":{"command":"death"}}],
	"steps":[{"id":"n","action":"vars.add","with":{"scope":"global","name":"deaths"}},
	         {"action":"test.send","with":{"message":"${steps.n.output.value}/${saved.global.deaths}"}}]`

func TestACounterSurvivesRunsAndRestarts(t *testing.T) {
	r := newRig(t)
	r.save(def("deaths", counter), true)
	r.chat("d1", "death", events.Confirmed)
	r.chat("d2", "death", events.Confirmed)
	// The same event delivered again is never counted twice.
	p, _ := json.Marshal(map[string]any{"command": "death"})
	_, _ = r.inbox.Accept(context.Background(), events.Envelope{Type: "chat.command.received", Source: "twitch", SourceEventID: "d2",
		Payload: p, DeliveryQuality: events.Confirmed, Actor: &events.Actor{Platform: "twitch", ID: "1"}})
	r.jobs("deaths", 2)
	r.eng.Close()
	r.start()
	r.chat("d3", "death", events.Confirmed)
	r.jobs("deaths", 3)
	if got := strings.Join(r.messages(), " "); got != "1/1 2/2 3/3" {
		t.Fatalf("messages %q", got)
	}
	if v := r.saved(variables.Key{Scope: variables.Global, Name: "deaths"}); v != int64(3) {
		t.Fatalf("deaths = %v", v)
	}
}

func TestRunsCountingAtOnceNeverLoseACount(t *testing.T) {
	r := newRig(t)
	r.save(def("deaths", `"queue":{"policy":"parallel","concurrency":16},`+strings.NewReplacer(`{"action":"test.send"`, `{"action":"test.local"`, `"message"`, `"value"`).Replace(counter)), true)
	for i := range 30 {
		r.chat(fmt.Sprint("p", i), "death", events.Confirmed)
	}
	r.jobs("deaths", 30)
	if v := r.saved(variables.Key{Scope: variables.Global, Name: "deaths"}); v != int64(30) {
		t.Fatalf("30 runs at once counted %v", v)
	}
}

func TestTestRunsNeverSaveVariables(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	key := variables.Key{Scope: variables.Global, Name: "deaths"}
	_, _ = r.vars.Set(ctx, key, int64(10), variables.Change{By: "you"})
	r.save(def("deaths", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"id":"n","action":"vars.add","with":{"scope":"global","name":"deaths"}},
		         {"action":"vars.set","with":{"scope":"global","name":"title","value":"new"}},
		         {"action":"vars.delete","with":{"scope":"global","name":"deaths"}},
		         {"action":"test.local","with":{"value":"${steps.n.output.value}/${has(saved.global.deaths)}/${saved.global.title}"}}]`), true)
	r.chat("t1", "death", events.Synthetic)
	js := r.jobs("deaths", 1)
	if js[0].Status != JobSucceeded {
		t.Fatalf("%+v", js[0])
	}
	_, steps, _ := r.eng.Trace(ctx, js[0].ID)
	// Within the run it behaves as if saved...
	if out := steps[3].Output.(map[string]any)["value"]; out != "11/false/new" {
		t.Fatalf("the run saw %v", out)
	}
	if steps[0].Status != StepOK || !strings.Contains(steps[0].Reason, "not saved") || steps[0].Output.(map[string]any)["saved"] != false {
		t.Fatalf("%+v", steps[0])
	}
	// ...but nothing was saved.
	if v := r.saved(key); v != int64(10) {
		t.Fatalf("a test run changed deaths to %v", v)
	}
	if v := r.saved(variables.Key{Scope: variables.Global, Name: "title"}); v != nil {
		t.Fatalf("a test run saved title %v", v)
	}
}

func TestViewerAndWorkflowScopes(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.save(def("lurk", `"triggers":[{"type":"chat.command.received","where":{"command":"lurk"}}],
		"steps":[{"id":"n","action":"vars.add","with":{"scope":"viewer","name":"lurks"}},
		         {"action":"vars.set","with":{"scope":"workflow","name":"last","value":"${event.actor.login}"}},
		         {"action":"test.send","with":{"message":"${saved.viewer.lurks} ${saved.workflow.last}"}}]`), true)
	r.save(def("peek", `"triggers":[{"type":"chat.command.received","where":{"command":"peek"}}],
		"steps":[{"action":"test.send","with":{"message":"${has(saved.workflow.last)} ${saved.viewer.lurks}"}}]`), true)
	r.chat("l1", "lurk", events.Confirmed)
	r.chat("l2", "lurk", events.Confirmed)
	r.jobs("lurk", 2)
	r.chat("p1", "peek", events.Confirmed)
	r.jobs("peek", 1)
	if got := strings.Join(r.messages(), " | "); got != "1 fan | 2 fan | false 2" {
		t.Fatalf("messages %q", got)
	}
	v, err := r.vars.Get(ctx, variables.Key{Scope: variables.Viewer, Owner: "twitch:1", Name: "lurks"})
	if err != nil || v.OwnerLabel != "Fan" || v.UpdatedBy != "workflow:lurk" {
		t.Fatalf("%+v %v", v, err)
	}
	if v := r.saved(variables.Key{Scope: variables.Workflow, Owner: "lurk", Name: "last"}); v != "fan" {
		t.Fatalf("workflow variable %v", v)
	}

	// An event nobody caused has no viewer variables.
	p, _ := json.Marshal(map[string]any{"command": "lurk"})
	if _, err := r.inbox.Accept(ctx, events.Envelope{Type: "chat.command.received", Source: "twitch", SourceEventID: "x",
		Payload: p, DeliveryQuality: events.Confirmed}); err != nil {
		t.Fatal(err)
	}
	js := r.jobs("lurk", 3)
	if js[2].Status != JobFailed || !strings.Contains(js[2].Error, "no viewer") {
		t.Fatalf("%+v", js[2])
	}
}

func TestVariableMistakesShowWhenSaving(t *testing.T) {
	r := newRig(t)
	_, err := Parse(def("bad", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"vars.set","with":{"scope":"everyone","name":"Deaths","value":1}},
		         {"action":"vars.add","with":{"scope":"global","name":"${vars.n}"}}]`), r.reg)
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatal(err)
	}
	got := fmt.Sprint(inv.Problems)
	for _, want := range []string{"$.steps[0].with.scope", "$.steps[0].with.name", "lowercase"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "steps[1]") {
		t.Errorf("a name from an expression is checked when it runs: %s", got)
	}
	// ...and when it runs.
	r.save(def("late", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"vars.set","with":{"scope":"global","name":"${'Bad Name'}","value":1}}]`), true)
	r.chat("b1", "x", events.Confirmed)
	if js := r.jobs("late", 1); js[0].Status != JobFailed || !strings.Contains(js[0].Error, "lowercase") {
		t.Fatalf("%+v", js[0])
	}
}
