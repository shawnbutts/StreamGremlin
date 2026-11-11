package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/variables"
)

// recorder is a fake outside service: it records every call it gets.
type recorder struct {
	mu    sync.Mutex
	calls []map[string]any
	gate  chan struct{} // when set, test.slow waits for it
}

func (r *recorder) add(args map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
}

func (r *recorder) got() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.calls...)
}

func testRegistry(rec *recorder) *Registry {
	reg := NewRegistry()
	reg.Add(Action{Name: "test.send", Label: "Send", Group: "test", External: true,
		Params: []Param{{Name: "message", Label: "Message", Type: "text", Required: true, Editor: "message"}, {Name: "password", Label: "Password", Type: "text"},
			{Name: "sound", Label: "Sound", Type: "text", Editor: "sound", Pattern: `^[a-z0-9][a-z0-9_-]{0,63}$`, PatternHint: "a sound's name"}},
		Run: func(ctx context.Context, c Call) (any, error) {
			rec.add(c.Args)
			return map[string]any{"sent": c.Args["message"], "key": c.IdempotencyKey}, nil
		}})
	reg.Add(Action{Name: "test.local", Label: "Local", Group: "test",
		Params: []Param{{Name: "value", Label: "Value", Type: "text"}},
		Run:    func(ctx context.Context, c Call) (any, error) { return map[string]any{"value": c.Args["value"]}, nil }})
	reg.Add(Action{Name: "test.fail", Label: "Fail", Group: "test",
		Run: func(ctx context.Context, c Call) (any, error) { return nil, errors.New("it broke") }})
	reg.Add(Action{Name: "test.slow", Label: "Slow", Group: "test", External: true,
		Params: []Param{{Name: "tag", Label: "Tag", Type: "text"}},
		Run: func(ctx context.Context, c Call) (any, error) {
			rec.add(c.Args)
			rec.mu.Lock()
			gate := rec.gate
			rec.mu.Unlock()
			select {
			case <-gate:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}})
	reg.Add(Action{Name: "test.preview", Label: "Preview", Group: "test", External: true, Preview: true,
		Run: func(ctx context.Context, c Call) (any, error) {
			login := ""
			if c.Actor != nil {
				login = c.Actor.Login
			}
			rec.add(map[string]any{"dry_run": c.DryRun, "actor": login})
			return map[string]any{"dry_run": c.DryRun}, nil
		}})
	return reg
}

type rig struct {
	t     *testing.T
	db    *storage.DB
	inbox *events.Inbox
	rec   *recorder
	reg   *Registry
	vars  *variables.Store
	eng   *Engine
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, inbox: events.NewInbox(db, nil), rec: &recorder{}, vars: variables.New(db)}
	r.reg = testRegistry(r.rec)
	r.start()
	return r
}

func (r *rig) start() {
	r.t.Helper()
	r.eng = New(Options{DB: r.db, Inbox: r.inbox, Actions: r.reg, Variables: r.vars})
	if err := r.eng.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	eng := r.eng
	r.t.Cleanup(eng.Close)
}

// def builds a definition from parts as JSON.
func def(id string, extra string) []byte {
	return []byte(fmt.Sprintf(`{"schema":%q,"id":%q,"name":"Test %s",%s}`, Schema, id, id, extra))
}

func (r *rig) save(raw []byte, on bool) Summary {
	r.t.Helper()
	s, _, err := r.eng.Save(context.Background(), raw)
	if err != nil {
		r.t.Fatal(err)
	}
	if s.Enabled != on {
		if err := r.eng.SetEnabled(context.Background(), s.ID, on); err != nil {
			r.t.Fatal(err)
		}
		s.Enabled = on
	}
	return s
}

// chat stores a chat command event; quality "synthetic" makes a test event.
func (r *rig) chat(id, command string, q events.Quality) events.Record {
	r.t.Helper()
	p, _ := json.Marshal(map[string]any{"command": command, "text": "!" + command, "count": 3})
	res, err := r.inbox.Accept(context.Background(), events.Envelope{Type: "chat.command.received", Source: "twitch",
		SourceEventID: id, Payload: p, DeliveryQuality: q, Actor: &events.Actor{Platform: "twitch", ID: "1", Login: "fan", DisplayName: "Fan"}})
	if err != nil {
		r.t.Fatal(err)
	}
	return res.Record
}

// jobs waits until the workflow has n finished jobs, and returns them oldest first.
func (r *rig) jobs(wf string, n int) []Job {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		js, _ := r.eng.Jobs(context.Background(), wf, 100)
		done := 0
		for _, j := range js {
			if j.Status != JobPending && j.Status != JobRunning {
				done++
			}
		}
		if done >= n || time.Now().After(deadline) {
			if done != n {
				r.t.Fatalf("%d finished jobs, want %d: %+v", done, n, js)
			}
			for i, k := 0, len(js)-1; i < k; i, k = i+1, k-1 {
				js[i], js[k] = js[k], js[i]
			}
			return js
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const hello = `"triggers":[{"type":"chat.command.received","where":{"command":"hello"}}],
	"steps":[{"id":"reply","action":"test.send","with":{"message":"Hi ${event.actor.display_name}, count ${event.payload.count + 1}","password":"hunter2"}}]`

func TestParseRejectsWithEveryProblem(t *testing.T) {
	reg := testRegistry(&recorder{})
	_, err := Parse(def("Bad ID", `"triggers":[{"type":"twitch"}],"queue":{"policy":"sometimes"},"timeout":"2h",
		"steps":[{"action":"nope"},{"action":"test.send"},{"set":{"Bad":"1 +"}},{"wait":"5s","set":{"a":"1"}},
		{"do":[],"else":[]},{"foreach":"[1]","steps":[{"wait":"1s"}],"max":5000},{"choose":[{"weight":0,"steps":[]}]},
		{"id":"x","wait":"1s"},{"id":"x","wait":"1s"},{"action":"test.send","with":{"message":"${unclosed","extra":1}}]`), reg)
	var inv *InvalidError
	if !errors.As(err, &inv) || !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	text := err.Error()
	for _, want := range []string{"$.id", "$.triggers[0].type", "$.queue.policy", "$.timeout", `no action "nope"`,
		"$.steps[1].with.message: required", "$.steps[2].set.Bad", "exactly one of", "$.steps[4].else: needs an if",
		"$.steps[5].max", "$.steps[6].choose[0].weight", "also used at", "without its closing", "no parameter \"extra\""} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if _, err := Parse([]byte(`{"schema":"`+Schema+`","id":"a.b","name":"x","triggers":[],"steps":[],"surprise":1}`), reg); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestExpressionsAndTemplates(t *testing.T) {
	ctx := context.Background()
	sc := scope{event: map[string]any{"payload": map[string]any{"count": int64(3), "name": "Fan", "items": []any{"a", "b"}}},
		vars: map[string]any{"x": int64(2)}, steps: map[string]any{}, job: map[string]any{}}
	cases := map[string]any{
		`${event.payload.count}`:                    int64(3), // whole template keeps its type
		`Hi ${event.payload.name}!`:                 "Hi Fan!",
		`${event.payload.count * vars.x}`:           int64(6),
		`$${not an expression}`:                     "${not an expression}",
		`${event.payload.items}`:                    []any{"a", "b"},
		`${event.payload.?missing.orValue("none")}`: "none",
		`${event.payload.name.lowerAscii()}`:        "fan",
		`n=${1.5}`:                                  "n=1.5",
	}
	for tmpl, want := range cases {
		raw, _ := json.Marshal(tmpl)
		got, err := render(ctx, raw, sc)
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: %#v %v, want %#v", tmpl, got, err, want)
		}
	}
	if _, err := evalBool(ctx, `event.payload.count`, sc); err == nil || !strings.Contains(err.Error(), "not true or false") {
		t.Fatal(err)
	}
	big := make([]any, 300)
	for i := range big {
		big[i] = int64(i)
	}
	sc.event["payload"].(map[string]any)["items"] = big
	if _, err := eval(ctx, `event.payload.items.all(a, event.payload.items.all(b, event.payload.items.all(c, true)))`, sc); err == nil {
		t.Fatal("an expensive expression ran to the end")
	}
}

func TestOffUntilTurnedOnAndOneJobPerEvent(t *testing.T) {
	r := newRig(t)
	r.save(def("chat.hello", hello), false)
	r.chat("m1", "hello", events.Confirmed)
	time.Sleep(100 * time.Millisecond)
	if js, _ := r.eng.Jobs(context.Background(), "chat.hello", 10); len(js) != 0 {
		t.Fatal("a new workflow ran before it was turned on")
	}
	if err := r.eng.SetEnabled(context.Background(), "chat.hello", true); err != nil {
		t.Fatal(err)
	}
	r.chat("m2", "hello", events.Confirmed)
	r.chat("m3", "goodbye", events.Confirmed) // no match
	js := r.jobs("chat.hello", 1)
	if js[0].Status != JobSucceeded || js[0].DryRun {
		t.Fatalf("%+v", js[0])
	}
	calls := r.rec.got()
	if len(calls) != 1 || calls[0]["message"] != "Hi Fan, count 4" {
		t.Fatalf("%+v", calls)
	}
	_, steps, _ := r.eng.Trace(context.Background(), js[0].ID)
	if len(steps) != 1 || steps[0].Status != StepOK || steps[0].Input.(map[string]any)["password"] != "[redacted]" ||
		steps[0].Output.(map[string]any)["key"] != js[0].ID+"/steps[0]" {
		t.Fatalf("%+v", steps)
	}

	// A restart with the cursor wound back redispatches, but never runs twice.
	r.eng.Close()
	if _, err := r.db.Exec("UPDATE workflow_dispatch SET cursor = 0"); err != nil {
		t.Fatal(err)
	}
	r.start()
	time.Sleep(200 * time.Millisecond)
	if js, _ := r.eng.Jobs(context.Background(), "chat.hello", 10); len(js) != 2 || len(r.rec.got()) != 2 {
		// m1 now matches too (the workflow is on), m2 must not repeat.
		t.Fatalf("%d jobs, %d calls", len(js), len(r.rec.got()))
	}
}

func TestOldEventsDontRunOnTheFirstStart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, _ := storage.Open(ctx, filepath.Join(dir, "w.db"))
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.Migrate(ctx, dir)
	r := &rig{t: t, db: db, inbox: events.NewInbox(db, nil), rec: &recorder{}, vars: variables.New(db)}
	r.reg = testRegistry(r.rec)
	r.chat("before", "hello", events.Confirmed) // stored before the engine ever ran
	r.start()
	r.save(def("chat.hello", hello), true)
	r.chat("after", "hello", events.Confirmed)
	js := r.jobs("chat.hello", 1)
	if len(js) != 1 || len(r.rec.got()) != 1 {
		t.Fatalf("%+v", js)
	}
}

func TestTestEventsAreDryRuns(t *testing.T) {
	r := newRig(t)
	r.save(def("chat.hello", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"id":"sent","action":"test.send","with":{"message":"to ${event.actor.login}"}},
		         {"id":"local","action":"test.local","with":{"value":"${steps.sent.status}"}}]`), true)
	r.chat("t1", "hello", events.Synthetic)
	js := r.jobs("chat.hello", 1)
	if !js[0].DryRun || js[0].Status != JobSucceeded || len(r.rec.got()) != 0 {
		t.Fatalf("%+v, %d outside calls", js[0], len(r.rec.got()))
	}
	_, steps, _ := r.eng.Trace(context.Background(), js[0].ID)
	if steps[0].Status != StepSkipped || !strings.Contains(steps[0].Reason, "dry run") ||
		steps[0].Input.(map[string]any)["message"] != "to fan" || steps[1].Status != StepOK ||
		steps[1].Output.(map[string]any)["value"] != "skipped" {
		t.Fatalf("%+v", steps)
	}
	// A trigger can refuse test events.
	r.save(def("confirmed.only", `"triggers":[{"type":"chat.**","delivery_quality":["confirmed"]}],"steps":[{"action":"test.local"}]`), true)
	r.chat("t2", "hi", events.Synthetic)
	time.Sleep(100 * time.Millisecond)
	if js, _ := r.eng.Jobs(context.Background(), "confirmed.only", 10); len(js) != 0 {
		t.Fatal(js)
	}
}

// A Preview step runs for a test event too, told it's a dry run, and knows
// who the event is about; the trace says it reached previews only.
func TestPreviewStepsRunInTestsAsDryRuns(t *testing.T) {
	r := newRig(t)
	r.save(def("alerts", `"triggers":[{"type":"chat.command.received"}],"steps":[{"action":"test.preview"}]`), true)
	r.chat("p1", "hi", events.Synthetic)
	js := r.jobs("alerts", 1)
	_, steps, _ := r.eng.Trace(context.Background(), js[0].ID)
	got := r.rec.got()
	if len(got) != 1 || got[0]["dry_run"] != true || got[0]["actor"] != "fan" ||
		steps[0].Status != StepOK || !strings.Contains(steps[0].Reason, "never reaches OBS") {
		t.Fatalf("%v %+v", got, steps)
	}
	r.chat("p2", "hi", events.Confirmed)
	r.jobs("alerts", 2)
	if got := r.rec.got(); len(got) != 2 || got[1]["dry_run"] != false || got[1]["actor"] != "fan" {
		t.Fatalf("%v", got)
	}
}

// A message with several ways to say it sends one of them, picked at
// random each run, with values filled in; the trace shows which.
func TestOneOfSeveralVersions(t *testing.T) {
	r := newRig(t)
	r.save(def("chat.hi", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"test.send","with":{"message":{"one_of":["Hi there, ${event.actor.login}!","Welcome in!","Good day to you sir and/or madam!"]}}}]`), true)
	for i := range 40 {
		r.chat(fmt.Sprint("v", i), "hi", events.Confirmed)
	}
	r.jobs("chat.hi", 40)
	seen := map[string]bool{}
	for _, c := range r.rec.got() {
		seen[c["message"].(string)] = true
	}
	for m := range seen {
		if m != "Hi there, fan!" && m != "Welcome in!" && m != "Good day to you sir and/or madam!" {
			t.Fatalf("not one of the versions: %q", m)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("always the same: %v", seen)
	}
	js := r.jobs("chat.hi", 40)
	_, steps, _ := r.eng.Trace(context.Background(), js[0].ID)
	if m, _ := steps[0].Input.(map[string]any)["message"].(string); !seen[m] {
		t.Fatalf("the trace doesn't show the version sent: %+v", steps[0].Input)
	}
}

// A sound can be several sounds too: one plays, at random.
func TestOneOfSeveralSounds(t *testing.T) {
	r := newRig(t)
	r.save(def("chat.horn", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"test.send","with":{"message":"x","sound":{"one_of":["airhorn","chime","boing"]}}}]`), true)
	for i := range 40 {
		r.chat(fmt.Sprint("s", i), "horn", events.Confirmed)
	}
	r.jobs("chat.horn", 40)
	seen := map[string]bool{}
	for _, c := range r.rec.got() {
		seen[c["sound"].(string)] = true
	}
	for snd := range seen {
		if snd != "airhorn" && snd != "chime" && snd != "boing" {
			t.Fatalf("not one of them: %q", snd)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("always the same: %v", seen)
	}
}

func TestVersionsAreChecked(t *testing.T) {
	r := newRig(t)
	for body, want := range map[string]string{
		`{"message":{"one_of":["ok",""]}}`:                       "$.steps[0].with.message.one_of[1]",
		`{"message":{"one_of":[]}}`:                              "$.steps[0].with.message.one_of",
		`{"message":{"one_of":["a"],"extra":1}}`:                 "$.steps[0].with.message",
		`{"message":"x","password":{"one_of":["a","b"]}}`:        "$.steps[0].with.password",
		`{"message":"x","sound":{"one_of":["ding","Bad Name"]}}`: "$.steps[0].with.sound.one_of[1]",
		`{"message":{"one_of":["${nope("]}}`:                     "$.steps[0].with.message.one_of[0]",
	} {
		_, _, err := r.eng.Save(context.Background(), def("chat.bad", `"triggers":[{"type":"chat.command.received"}],
			"steps":[{"action":"test.send","with":`+body+`}]`))
		var inv *InvalidError
		if !errors.As(err, &inv) || !strings.Contains(fmt.Sprint(inv.Problems), want) {
			t.Errorf("%s: %v", body, err)
		}
	}
	many := make([]string, MaxVersions+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprint("v", i))
	}
	if _, _, err := r.eng.Save(context.Background(), def("chat.bad", `"triggers":[{"type":"chat.command.received"}],
		"steps":[{"action":"test.send","with":{"message":{"one_of":[`+strings.Join(many, ",")+`]}}}]`)); err == nil {
		t.Error("51 versions accepted")
	}
}

func TestStepKinds(t *testing.T) {
	r := newRig(t)
	r.save(def("kinds", `"triggers":[{"type":"chat.command.received"}],"steps":[
		{"set":{"n":"event.payload.count","names":"['a','b','c']"}},
		{"if":"vars.n > 2","do":[{"action":"test.send","with":{"message":"big"}}],"else":[{"action":"test.send","with":{"message":"small"}}]},
		{"if":"vars.n > 10","do":[{"action":"test.send","with":{"message":"never"}}],"else":[{"action":"test.send","with":{"message":"else ran"}}]},
		{"foreach":"vars.names","steps":[{"action":"test.send","with":{"message":"${each.index}:${each.item}"}}]},
		{"id":"pick","choose":[{"weight":1,"steps":[{"action":"test.send","with":{"message":"heads"}}]},{"weight":1,"steps":[{"action":"test.send","with":{"message":"tails"}}]}]},
		{"wait":"10ms"},
		{"id":"broke","action":"test.fail","on_error":"continue"},
		{"action":"test.send","with":{"message":"after ${steps.broke.status}: ${steps.broke.error}"}},
		{"action":"test.send","if":"steps.pick.output.total == 2","with":{"message":"choose recorded"}}]`), true)
	r.chat("k", "go", events.Confirmed)
	js := r.jobs("kinds", 1)
	if js[0].Status != JobSucceeded {
		t.Fatalf("%+v", js[0])
	}
	var msgs []string
	for _, c := range r.rec.got() {
		msgs = append(msgs, c["message"].(string))
	}
	got := strings.Join(msgs, "|")
	for _, want := range []string{"big|else ran|0:a|1:b|2:c|", "after failed: it broke", "choose recorded"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if !strings.Contains(got, "heads") && !strings.Contains(got, "tails") {
		t.Fatal(got)
	}
}

func TestChooseUsesTheWeights(t *testing.T) {
	sc := scope{event: map[string]any{}, vars: map[string]any{}, steps: map[string]any{}, job: map[string]any{}}
	counts := map[int64]int{}
	for i := 0; i < 2000; i++ {
		budget := 10
		in := &interp{reg: NewRegistry(), sc: sc, budget: &budget, now: time.Now}
		_ = in.step(context.Background(), "s", Step{ID: "c", Choose: []Branch{{Weight: 1}, {Weight: 9}}})
		counts[in.sc.steps["c"].(map[string]any)["output"].(map[string]any)["branch"].(int64)]++
	}
	if counts[1] < 1600 || counts[0] < 100 {
		t.Fatalf("weights 1:9 gave %v", counts)
	}
}

func TestFailuresTimeoutsBudgetsAndFinally(t *testing.T) {
	r := newRig(t)
	r.save(def("fails", `"triggers":[{"type":"chat.command.received","where":{"command":"fail"}}],
		"steps":[{"action":"test.fail"},{"action":"test.send","with":{"message":"never"}}],
		"finally":[{"action":"test.send","with":{"message":"cleanup after ${job.id != '' ? 'run' : ''}"}}]`), true)
	r.save(def("slow", `"triggers":[{"type":"chat.command.received","where":{"command":"slow"}}],"timeout":"100ms",
		"steps":[{"action":"test.slow"}],"finally":[{"action":"test.send","with":{"message":"slow cleanup"}}]`), true)
	r.save(def("loop", `"triggers":[{"type":"chat.command.received","where":{"command":"loop"}}],"max_steps":5,
		"steps":[{"foreach":"[1,2,3,4,5,6]","steps":[{"action":"test.local"}]}]`), true)
	r.chat("f", "fail", events.Confirmed)
	r.chat("s", "slow", events.Confirmed)
	r.chat("l", "loop", events.Confirmed)
	if j := r.jobs("fails", 1)[0]; j.Status != JobFailed || !strings.Contains(j.Error, "it broke") {
		t.Fatalf("%+v", j)
	}
	if j := r.jobs("slow", 1)[0]; j.Status != JobFailed || !strings.Contains(j.Error, "100ms") {
		t.Fatalf("%+v", j)
	}
	if j := r.jobs("loop", 1)[0]; j.Status != JobFailed || !strings.Contains(j.Error, "max_steps") {
		t.Fatalf("%+v", j)
	}
	var msgs []string
	for _, c := range r.rec.got() {
		if m, ok := c["message"].(string); ok {
			msgs = append(msgs, m)
		}
	}
	got := strings.Join(msgs, "|")
	if !strings.Contains(got, "cleanup after run") || !strings.Contains(got, "slow cleanup") || strings.Contains(got, "never") {
		t.Fatal(got)
	}
}

func TestCancelRunsFinallyAndRestartInterrupts(t *testing.T) {
	r := newRig(t)
	r.rec.gate = make(chan struct{})
	r.save(def("long", `"triggers":[{"type":"chat.command.received"}],"steps":[{"action":"test.slow"}],
		"finally":[{"action":"test.send","with":{"message":"cleaned"}}]`), true)
	r.chat("a", "x", events.Confirmed)
	var running Job
	deadline := time.Now().Add(5 * time.Second)
	for running.Status != JobRunning {
		js, _ := r.eng.Jobs(context.Background(), "long", 5)
		if len(js) > 0 {
			running = js[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := r.eng.Cancel(context.Background(), running.ID); err != nil {
		t.Fatal(err)
	}
	if j := r.jobs("long", 1)[0]; j.Status != JobCancelled {
		t.Fatalf("%+v", j)
	}
	if err := r.eng.Cancel(context.Background(), running.ID); !errors.Is(err, ErrFinished) {
		t.Fatal(err)
	}
	if calls := r.rec.got(); calls[len(calls)-1]["message"] != "cleaned" {
		t.Fatal(calls)
	}

	// A crash while running: at the next start it's interrupted, not re-run.
	if _, err := r.db.Exec(`INSERT INTO jobs (id, workflow_id, version, event_id, event_seq, status, dry_run, created_at, started_at)
		VALUES ('crashed', 'long', 1, 'gone', 1, 'running', 0, '2026-11-08T00:00:00.000Z', '2026-11-08T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	r.eng.Close()
	before := len(r.rec.got())
	r.start()
	j, _, _ := r.eng.Trace(context.Background(), "crashed")
	if j.Status != JobInterrupted || len(r.rec.got()) != before {
		t.Fatalf("%+v", j)
	}
}

func TestQueuePolicies(t *testing.T) {
	r := newRig(t)
	r.rec.gate = make(chan struct{})
	r.save(def("serial", `"triggers":[{"type":"chat.command.received","where":{"command":"s"}}],"queue":{"max_pending":2},
		"steps":[{"action":"test.slow","with":{"tag":"s-${event.source_event_id}"}}]`), true)
	r.save(def("drop", `"triggers":[{"type":"chat.command.received","where":{"command":"d"}}],"queue":{"policy":"drop"},
		"steps":[{"action":"test.slow","with":{"tag":"d"}}]`), true)
	r.save(def("replace", `"triggers":[{"type":"chat.command.received","where":{"command":"r"}}],"queue":{"policy":"replace"},
		"steps":[{"action":"test.slow","with":{"tag":"r-${event.source_event_id}"}}]`), true)
	r.save(def("par", `"triggers":[{"type":"chat.command.received","where":{"command":"p"}}],"queue":{"policy":"parallel","concurrency":2},
		"steps":[{"action":"test.slow","with":{"tag":"p"}}]`), true)
	for i := 0; i < 4; i++ {
		r.chat(fmt.Sprint("s", i), "s", events.Confirmed) // 1 runs, 2 wait, 1 rejected
		r.chat(fmt.Sprint("d", i), "d", events.Confirmed) // 1 runs, 3 dropped
		r.chat(fmt.Sprint("p", i), "p", events.Confirmed) // 2 run, 2 wait
	}
	r.chat("r0", "r", events.Confirmed)
	time.Sleep(100 * time.Millisecond)
	r.chat("r1", "r", events.Confirmed) // cancels r0
	r.chat("r2", "r", events.Confirmed) // supersedes r1
	time.Sleep(200 * time.Millisecond)
	running := 0
	for _, c := range r.rec.got() {
		if c["tag"] == "p" {
			running++
		}
	}
	if running != 2 {
		t.Fatalf("parallel ran %d at once, want 2", running)
	}
	close(r.rec.gate)
	count := func(wf string) map[string]int {
		m := map[string]int{}
		for _, j := range r.jobs(wf, map[string]int{"serial": 4, "drop": 4, "replace": 3, "par": 4}[wf]) {
			m[j.Status]++
		}
		return m
	}
	if c := count("serial"); c[JobSucceeded] != 3 || c[JobRejected] != 1 {
		t.Fatalf("serial %v", c)
	}
	if c := count("drop"); c[JobSucceeded] != 1 || c[JobDropped] != 3 {
		t.Fatalf("drop %v", c)
	}
	if c := count("replace"); c[JobCancelled] != 1 || c[JobSuperseded] != 1 || c[JobSucceeded] != 1 {
		t.Fatalf("replace %v", c)
	}
	if c := count("par"); c[JobSucceeded] != 4 {
		t.Fatalf("parallel %v", c)
	}
	// Serial ran in order.
	var order []string
	for _, c := range r.rec.got() {
		if s, _ := c["tag"].(string); strings.HasPrefix(s, "s-") {
			order = append(order, s)
		}
	}
	if strings.Join(order, ",") != "s-s0,s-s1,s-s2" {
		t.Fatalf("serial order %v", order)
	}
}

func TestDropOldestAndTurningOff(t *testing.T) {
	r := newRig(t)
	r.rec.gate = make(chan struct{})
	r.save(def("q", `"triggers":[{"type":"chat.command.received"}],"queue":{"max_pending":1,"on_overflow":"drop_oldest"},
		"steps":[{"action":"test.slow","with":{"tag":"${event.source_event_id}"}}]`), true)
	r.chat("a", "x", events.Confirmed) // runs
	time.Sleep(50 * time.Millisecond)
	r.chat("b", "x", events.Confirmed) // waits
	r.chat("c", "x", events.Confirmed) // b dropped, c waits
	time.Sleep(50 * time.Millisecond)
	if err := r.eng.SetEnabled(context.Background(), "q", false); err != nil { // c cancelled
		t.Fatal(err)
	}
	close(r.rec.gate)
	m := map[string]string{}
	for _, j := range r.jobs("q", 3) {
		m[j.EventSeq2()] = j.Status
	}
	if m["1"] != JobSucceeded || m["2"] != JobDropped || m["3"] != JobCancelled {
		t.Fatalf("%v", m)
	}
}

func TestSavingTheSameContentKeepsTheVersion(t *testing.T) {
	r := newRig(t)
	s := r.save(def("v", hello), false)
	again, changed, err := r.eng.Save(context.Background(), def("v", hello))
	if err != nil || changed || again.Version != s.Version {
		t.Fatalf("%+v %v %v", again, changed, err)
	}
	next, changed, _ := r.eng.Save(context.Background(), def("v", strings.Replace(hello, "Hi", "Hello", 1)))
	if !changed || next.Version != 2 {
		t.Fatal(next)
	}
	if _, err := r.db.Exec("UPDATE workflow_versions SET definition = '{}'"); err == nil {
		t.Fatal("a version changed")
	}
}

func TestTriggerPatterns(t *testing.T) {
	for _, c := range []struct {
		pattern, typ string
		want         bool
	}{
		{"chat.command.received", "chat.command.received", true},
		{"chat.*.received", "chat.message.received", true},
		{"chat.*", "chat.message.received", false},
		{"chat.**", "chat.message.received", true},
		{"broadcast.**", "broadcast.scene.changed", true},
		{"broadcast.**", "chat.message.received", false},
		{"**", "anything.at.all", true},
	} {
		if got := typeMatches(c.pattern, c.typ); got != c.want {
			t.Errorf("%s ~ %s: %v", c.pattern, c.typ, got)
		}
	}
}

// EventSeq2 is the job's event seq as text, for compact test tables.
func (j Job) EventSeq2() string { return fmt.Sprint(j.EventSeq) }
