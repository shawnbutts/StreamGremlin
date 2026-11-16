package deck

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/timers"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

type fakeWorkflows struct {
	mu sync.Mutex
	on map[string]bool
}

func (f *fakeWorkflows) Get(_ context.Context, id string) (workflow.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	on, ok := f.on[id]
	if !ok {
		return workflow.Summary{}, workflow.ErrNotFound
	}
	return workflow.Summary{ID: id, Name: "Workflow " + id, Enabled: on}, nil
}

func (f *fakeWorkflows) SetEnabled(_ context.Context, id string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on[id] = on
	return nil
}

type fakeTimers struct {
	list    map[string]timers.Timer
	changed int
}

func (f *fakeTimers) Get(_ context.Context, id string) (timers.Timer, error) {
	t, ok := f.list[id]
	if !ok {
		return timers.Timer{}, timers.ErrNotFound
	}
	return t, nil
}

func (f *fakeTimers) Save(_ context.Context, t timers.Timer, _ time.Time) (timers.Timer, bool, error) {
	f.list[t.ID] = t
	return t, false, nil
}

func (f *fakeTimers) Changed() { f.changed++ }

type fakeLock struct {
	locked bool
	sent   []moderation.Request
	answer string // the state Twitch answers with
}

func (f *fakeLock) Locked(context.Context) (*moderation.Record, error) {
	if f.locked {
		return &moderation.Record{Action: moderation.Lock}, nil
	}
	return nil, nil
}

func (f *fakeLock) Do(_ context.Context, r moderation.Request) (moderation.Record, bool, error) {
	f.sent = append(f.sent, r)
	state := f.answer
	if state == "" {
		state = moderation.Done
	}
	if state == moderation.Done {
		f.locked = r.Action == moderation.Lock
	}
	return moderation.Record{Action: r.Action, State: state, Answer: "Twitch said so"}, false, nil
}

type world struct {
	s        *Service
	vars     *variables.Store
	wf       *fakeWorkflows
	tm       *fakeTimers
	lock     *fakeLock
	signedIn bool
	live     *bool
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	w := &world{
		vars:     variables.New(db),
		wf:       &fakeWorkflows{on: map[string]bool{"wf_a": true}},
		tm:       &fakeTimers{list: map[string]timers.Timer{"break": {ID: "break", Name: "Take a break", Enabled: false, EverySeconds: 1800}}},
		lock:     &fakeLock{},
		signedIn: true,
	}
	n := 0
	w.s = New(Options{
		Settings: settings.NewStore(db), Variables: w.vars, Workflows: w.wf, Timers: w.tm, Lock: w.lock,
		Live: func(context.Context) (bool, bool) {
			if w.live == nil {
				return false, false
			}
			return *w.live, true
		},
		Chat: func() string { return "connected" }, OBS: func() string { return "off" },
		SignedIn: func() bool { return w.signedIn },
		NewID:    func() string { n++; return "deck-" + strconv.Itoa(n) },
	})
	return w
}

func (w *world) choose(t *testing.T, s Settings) {
	t.Helper()
	if _, err := w.s.SetSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAreChecked(t *testing.T) {
	for _, bad := range []Settings{
		{Counters: []string{"Deaths"}},
		{Counters: []string{"deaths", "deaths"}},
		{Switches: []Switch{{Kind: "scene", ID: "x"}}},
		{Switches: []Switch{{Kind: KindWorkflow}}},
		{Switches: []Switch{{Kind: KindChatLock, ID: "x"}}},
		{Switches: []Switch{{Kind: KindTimer, ID: "t"}, {Kind: KindTimer, ID: "t"}}},
		{Counters: make([]string, MaxCounters+1)},
	} {
		if err := bad.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	w := newWorld(t)
	got, err := w.s.Settings(context.Background())
	if err != nil || len(got.Counters) != 0 || len(got.Switches) != 0 {
		t.Fatalf("nothing is chosen at first: %+v %v", got, err)
	}
}

func TestStep(t *testing.T) {
	for _, c := range []struct {
		prev   any
		change string
		want   any
	}{
		{nil, Add, int64(1)}, {int64(7), Add, int64(8)}, {int64(7), Take, int64(6)}, {int64(0), Take, int64(0)},
		{int64(-3), Take, int64(0)}, {int64(7), Reset, int64(0)}, {2.5, Add, 3.5}, {0.5, Take, 0.0}, {nil, Take, int64(0)},
	} {
		got, err := Step(c.prev, c.change)
		if err != nil || got != c.want {
			t.Errorf("%v %s: %v %v, want %v", c.prev, c.change, got, err, c.want)
		}
	}
	if _, err := Step("text", Add); !errors.Is(err, ErrNotNumber) {
		t.Errorf("text: %v", err)
	}
	if _, err := Step(int64(1), "double"); !errors.Is(err, ErrInvalid) {
		t.Errorf("double: %v", err)
	}
}

// Only a chosen counter changes, once per request id, and is recorded as
// from the deck.
func TestCounters(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.s.Count(ctx, "deaths", Add, "r1", "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("not chosen: %v", err)
	}
	w.choose(t, Settings{Counters: []string{"deaths", "mood"}})
	for i, id := range []string{"r1", "r1", "r2"} {
		c, err := w.s.Count(ctx, "deaths", Add, id, "cl_1")
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{1, 1, 2}[i]
		if c.Value != want {
			t.Errorf("press %d (%s): %v, want %d", i, id, c.Value, want)
		}
	}
	// Another deck's request id is its own.
	if c, _ := w.s.Count(ctx, "deaths", Add, "r1", "cl_2"); c.Value != int64(3) {
		t.Errorf("another deck's r1: %v", c.Value)
	}
	v, err := w.vars.Get(ctx, variables.Key{Scope: variables.Global, Name: "deaths"})
	if err != nil || v.Value != int64(3) || v.UpdatedBy != "stream_deck:cl_2" {
		t.Errorf("saved: %+v %v", v, err)
	}
	if _, err := w.vars.Set(ctx, variables.Key{Scope: variables.Global, Name: "mood"}, "happy", variables.Change{By: "you"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.Count(ctx, "mood", Add, "r3", "cl_1"); !errors.Is(err, ErrNotNumber) {
		t.Errorf("text: %v", err)
	}
	// Un-chosen: refused at once.
	w.choose(t, Settings{})
	if _, err := w.s.Count(ctx, "deaths", Add, "r4", "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("un-chosen: %v", err)
	}
	if _, err := w.s.Count(ctx, "deaths", Add, "", "cl_1"); !errors.Is(err, ErrInvalid) {
		t.Errorf("no request id: %v", err)
	}
}

// Many presses at once all count.
func TestCountersAtOnce(t *testing.T) {
	w := newWorld(t)
	w.choose(t, Settings{Counters: []string{"hits"}})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := w.s.Count(context.Background(), "hits", Add, "p"+strconv.Itoa(i), "cl_1"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	v, _ := w.vars.Get(context.Background(), variables.Key{Scope: variables.Global, Name: "hits"})
	if v.Value != int64(20) {
		t.Errorf("hits %v, want 20", v.Value)
	}
}

func TestView(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.choose(t, Settings{Counters: []string{"deaths"}, Switches: []Switch{{Kind: KindWorkflow, ID: "wf_a"}, {Kind: KindTimer, ID: "break"},
		{Kind: KindWorkflow, ID: "wf_gone"}, {Kind: KindChatLock}}})
	v, err := w.s.View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status.Live != nil || v.Status.Chat != "connected" || v.Status.OBS != "off" || v.Status.ChatLocked == nil || *v.Status.ChatLocked {
		t.Errorf("status: %+v", v.Status)
	}
	if len(v.Counters) != 1 || v.Counters[0].Value != nil {
		t.Errorf("an unset counter: %+v", v.Counters)
	}
	sw := v.Switches
	if len(sw) != 4 || !*sw[0].On || sw[0].Label != "Workflow wf_a" || *sw[1].On || sw[1].Label != "Take a break" ||
		!sw[2].Gone || sw[2].On != nil || sw[3].On == nil || *sw[3].On {
		t.Errorf("switches: %+v", sw)
	}
	live := true
	w.live, w.signedIn = &live, false
	v, _ = w.s.View(ctx)
	if v.Status.Live == nil || !*v.Status.Live || v.Status.ChatLocked != nil || v.Switches[3].On != nil {
		t.Errorf("live, and the lock unknown signed out: %+v %+v", v.Status, v.Switches[3])
	}
}

func TestSwitches(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.s.Turn(ctx, KindWorkflow, "wf_a", false, "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("not chosen: %v", err)
	}
	w.choose(t, Settings{Switches: []Switch{{Kind: KindWorkflow, ID: "wf_a"}, {Kind: KindTimer, ID: "break"},
		{Kind: KindWorkflow, ID: "wf_gone"}, {Kind: KindChatLock}}})
	st, err := w.s.Turn(ctx, KindWorkflow, "wf_a", false, "cl_1")
	if err != nil || *st.On || w.wf.on["wf_a"] {
		t.Errorf("workflow off: %+v %v", st, err)
	}
	st, err = w.s.Turn(ctx, KindTimer, "break", true, "cl_1")
	if err != nil || !*st.On || !w.tm.list["break"].Enabled || w.tm.list["break"].EverySeconds != 1800 || w.tm.changed != 1 {
		t.Errorf("timer on, keeping the rest: %+v %v %+v", st, err, w.tm.list["break"])
	}
	// Already on: nothing saved again.
	if _, err := w.s.Turn(ctx, KindTimer, "break", true, "cl_1"); err != nil || w.tm.changed != 1 {
		t.Errorf("again: %v, changed %d", err, w.tm.changed)
	}
	if _, err := w.s.Turn(ctx, KindWorkflow, "wf_gone", true, "cl_1"); !errors.Is(err, ErrGone) {
		t.Errorf("gone: %v", err)
	}
	if _, err := w.s.Turn(ctx, KindTimer, "other", true, "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("another timer: %v", err)
	}
}

// The chat lock goes through moderation as from the deck, once; a retry
// finds it already locked, and Twitch's refusal is said.
func TestChatLock(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.choose(t, Settings{Switches: []Switch{{Kind: KindChatLock}}})
	for range 2 {
		st, err := w.s.Turn(ctx, KindChatLock, "", true, "cl_1")
		if err != nil || !*st.On {
			t.Fatalf("lock: %+v %v", st, err)
		}
	}
	if len(w.lock.sent) != 1 || w.lock.sent[0].From != moderation.FromDeck || w.lock.sent[0].Action != moderation.Lock {
		t.Errorf("sent: %+v", w.lock.sent)
	}
	w.lock.answer = moderation.Refused
	if _, err := w.s.Turn(ctx, KindChatLock, "", false, "cl_1"); !errors.Is(err, ErrRefused) {
		t.Errorf("refused: %v", err)
	}
	w.lock.answer = moderation.Unknown
	if _, err := w.s.Turn(ctx, KindChatLock, "", false, "cl_1"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
	w.signedIn = false
	if _, err := w.s.Turn(ctx, KindChatLock, "", false, "cl_1"); !errors.Is(err, ErrUnknown) {
		t.Errorf("signed out: %v", err)
	}
}
