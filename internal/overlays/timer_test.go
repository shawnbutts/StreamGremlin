package overlays

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/settings"
)

type timerRig struct {
	store *Store
	hub   *Hub
	set   *settings.Store
	inbox *events.Inbox
	t     *Timers
	mu    sync.Mutex
	now   time.Time
}

func newTimerRig(t *testing.T) *timerRig {
	t.Helper()
	s, db, _ := newStore(t)
	inbox := events.NewInbox(db, nil)
	r := &timerRig{store: s, hub: NewHub(nil), set: settings.NewStore(db), inbox: inbox, now: time.Date(2026, 11, 13, 18, 30, 0, 0, time.Local)}
	r.t = NewTimers(s, r.hub, r.set, inbox, nil)
	r.t.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
	t.Cleanup(r.t.Close)
	return r
}

func (r *timerRig) add(t *testing.T, name string, look Look) {
	t.Helper()
	if _, _, err := r.store.Create(context.Background(), name, KindTimer, look, now); err != nil {
		t.Fatal(err)
	}
}

func (r *timerRig) later(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
}

func timerLookOf(mode string, seconds int) Look {
	l := DefaultLook()
	l.TimerMode, l.TimerLength = mode, seconds
	return l
}

// A countdown: starts at its length, pauses where it is, takes more time,
// resets to its length; counts to a time of day; a stopwatch counts up.
// Every change reaches the page with the engine's time.
func TestCountdownsAndStopwatches(t *testing.T) {
	ctx := context.Background()
	r := newTimerRig(t)
	r.add(t, "soon", timerLookOf(TimerCountdown, 300))
	r.add(t, "clock", timerLookOf(TimerClock, 0))
	r.add(t, "watch", timerLookOf(TimerStopwatch, 0))
	page := r.hub.join(Overlay{Name: "soon", Kind: KindTimer}, false)
	drain(page)

	st, err := r.t.Do(ctx, "soon", TimerOp{Action: "start"})
	if err != nil || !st.Running || st.BaseMS != 300000 {
		t.Fatal(st, err)
	}
	types, bodies := drain(page)
	var v TimerView
	if count(types, "timer") != 1 || json.Unmarshal(bodies[0], &v) != nil || !v.Running || v.BaseMS != 300000 || v.NowMS != r.now.UnixMilli() {
		t.Fatal(types, string(bodies[0]))
	}
	r.later(100 * time.Second)
	if st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "pause"}); st.Running || st.BaseMS != 200000 {
		t.Fatal("paused:", st)
	}
	r.later(time.Hour) // paused: nothing passes
	if st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "add", Seconds: 60}); st.Running || st.BaseMS != 260000 {
		t.Fatal("added:", st)
	}
	if st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "start"}); !st.Running || st.BaseMS != 260000 {
		t.Fatal("resumed:", st)
	}
	if st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "start", Seconds: 90}); st.BaseMS != 90000 {
		t.Fatal("a new length:", st)
	}
	if st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "start", Until: "20:00"}); st.BaseMS != (28*60+20)*1000 { // at 19:31:40
		t.Fatal("until 20:00:", st, r.now)
	}
	if st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "start", Until: "7:00"}); st.BaseMS != (11*3600+28*60+20)*1000 {
		t.Fatal("until 7:00 is tomorrow morning:", st)
	}
	st, _ = r.t.Do(ctx, "soon", TimerOp{Action: "reset"})
	if st.Running || st.BaseMS != 300000 {
		t.Fatal("reset:", st)
	}
	if saved, _ := r.t.State(ctx, "soon"); saved != nil {
		t.Fatal("reset kept a state:", saved)
	}
	types, bodies = drain(page)
	if string(bodies[len(bodies)-1]) != "null" {
		t.Fatal("reset sent:", types, string(bodies[len(bodies)-1]))
	}

	if st, _ = r.t.Do(ctx, "watch", TimerOp{Action: "start"}); !st.Running || st.BaseMS != 0 {
		t.Fatal(st)
	}
	r.later(42 * time.Second)
	if st, _ = r.t.Do(ctx, "watch", TimerOp{Action: "pause"}); st.BaseMS != 42000 {
		t.Fatal("stopwatch:", st)
	}

	for name, op := range map[string]TimerOp{
		"clock": {Action: "start"}, // a clock has nothing to start
	} {
		if _, err := r.t.Do(ctx, name, op); !errors.Is(err, ErrNotTimer) {
			t.Error(name, err)
		}
	}
	for _, op := range []TimerOp{{Action: "add", Seconds: 0}, {Action: "add", Seconds: MaxTimerSeconds}, {Action: "start", Until: "7pm"},
		{Action: "start", Seconds: -1}, {Action: "jump"}} {
		if _, err := r.t.Do(ctx, "soon", op); err == nil {
			t.Errorf("%+v was done", op)
		}
	}
	for _, op := range []TimerOp{{Action: "add", Seconds: 10}, {Action: "start", Seconds: 10}, {Action: "start", Until: "19:00"}} {
		if _, err := r.t.Do(ctx, "watch", op); err == nil {
			t.Errorf("a stopwatch took %+v", op)
		}
	}
}

// A countdown that reaches zero is saved as ended and says so once; a
// paused or restarted one doesn't; one that ended while StreamGremlin was
// off is ended without saying so late.
func TestACountdownSaysWhenItEnds(t *testing.T) {
	ctx := context.Background()
	r := newTimerRig(t)
	look := timerLookOf(TimerCountdown, 1)
	look.TimerLabel = "Starting soon"
	r.add(t, "soon", look)
	r.t.now = time.Now
	if _, err := r.t.Do(ctx, "soon", TimerOp{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var ended []events.Record
	for len(ended) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		ended, _ = r.inbox.After(ctx, 0, 10, events.Filter{TimerEnded})
	}
	if len(ended) != 1 || !strings.Contains(string(ended[0].Payload), `"overlay":"soon"`) || !strings.Contains(string(ended[0].Payload), "Starting soon") {
		t.Fatal("ended:", ended)
	}
	if st, _ := r.t.State(ctx, "soon"); st == nil || !st.Ended || st.Running || st.BaseMS != 0 {
		t.Fatal("saved:", st)
	}
	// Started again from zero: from the top.
	if st, _ := r.t.Do(ctx, "soon", TimerOp{Action: "start", Seconds: 600}); !st.Running || st.Ended {
		t.Fatal(st)
	}
	if _, err := r.t.Do(ctx, "soon", TimerOp{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if all, _ := r.inbox.After(ctx, 0, 10, events.Filter{TimerEnded}); len(all) != 1 {
		t.Fatal("a paused countdown said it ended:", all)
	}

	// Off while it ran out: ended at the next start, quietly.
	_ = r.set.Save(ctx, timerKey("soon"), TimerState{Running: true, BaseMS: 1000, Since: time.Now().Add(-time.Hour)})
	again := NewTimers(r.store, r.hub, r.set, r.inbox, nil)
	defer again.Close()
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := again.State(ctx, "soon"); st == nil || !st.Ended || st.Running {
		t.Fatal("after a restart:", st)
	}
	time.Sleep(50 * time.Millisecond)
	if all, _ := r.inbox.After(ctx, 0, 10, events.Filter{TimerEnded}); len(all) != 1 {
		t.Fatal("said it ended late:", len(all))
	}
}

func TestTimerLooksAreChecked(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	good := timerLookOf(TimerClock, 0)
	good.Clock24, good.ClockSeconds, good.ClockDate, good.TimerLabel = true, true, "long", "Local time"
	if _, _, err := s.Create(ctx, "clock", KindTimer, good, now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []func(l *Look){
		func(l *Look) { l.TimerMode = "egg-timer" },
		func(l *Look) { l.ClockDate = "full" },
		func(l *Look) { l.TimerLength = MaxTimerSeconds + 1 },
		func(l *Look) { l.TimerLabel = strings.Repeat("x", MaxTimerText+1) },
		func(l *Look) { l.TimerEndText = "two\nlines" },
	} {
		l := good
		bad(&l)
		if _, err := s.SetLook(ctx, "clock", l, now); err == nil {
			t.Errorf("%+v was saved", l)
		}
	}
	if _, _, err := s.Create(ctx, "alerts", KindAlert, good, now); err == nil {
		t.Fatal("an alert overlay took a clock")
	}
}

// Auto-start: OBS loading the page starts a countdown afresh from its
// length (a stopwatch from zero); the app's preview, a clock, a timer
// without it, and a second load moments later don't.
func TestTimersStartAsOBSLoadsThem(t *testing.T) {
	ctx := context.Background()
	r := newTimerRig(t)
	r.hub.OnTimerLoaded(func(name string) { r.t.Loaded(ctx, name) })
	auto := timerLookOf(TimerCountdown, 300)
	auto.TimerAutoStart = true
	r.add(t, "soon", auto)
	r.add(t, "manual", timerLookOf(TimerCountdown, 300))
	clock := timerLookOf(TimerClock, 0)
	clock.TimerAutoStart = true
	r.add(t, "clock", clock)
	watch := timerLookOf(TimerStopwatch, 0)
	watch.TimerAutoStart = true
	r.add(t, "watch", watch)

	r.hub.loaded(r.hub.join(Overlay{Name: "soon", Kind: KindTimer}, true)) // the app's preview
	if st, _ := r.t.State(ctx, "soon"); st != nil {
		t.Fatal("the preview started it:", st)
	}
	// Half-way through a countdown, OBS reloads the page: from the top again.
	if _, err := r.t.Do(ctx, "soon", TimerOp{Action: "start", Seconds: 60}); err != nil {
		t.Fatal(err)
	}
	r.later(time.Minute)
	r.hub.loaded(r.hub.join(Overlay{Name: "soon", Kind: KindTimer}, false))
	if st, _ := r.t.State(ctx, "soon"); st == nil || !st.Running || st.BaseMS != 300000 || !st.Since.Equal(r.now) {
		t.Fatal("not started afresh:", st)
	}
	r.later(time.Second)
	r.hub.loaded(r.hub.join(Overlay{Name: "soon", Kind: KindTimer}, false)) // a second source, a moment later
	if st, _ := r.t.State(ctx, "soon"); !st.Since.Equal(r.now.Add(-time.Second)) {
		t.Fatal("started twice:", st)
	}
	for _, name := range []string{"manual", "clock"} {
		r.hub.loaded(r.hub.join(Overlay{Name: name, Kind: KindTimer}, false))
		if st, _ := r.t.State(ctx, name); st != nil {
			t.Fatal(name, "started:", st)
		}
	}
	r.later(42 * time.Second)
	r.hub.loaded(r.hub.join(Overlay{Name: "watch", Kind: KindTimer}, false))
	if st, _ := r.t.State(ctx, "watch"); st == nil || !st.Running || st.BaseMS != 0 {
		t.Fatal("stopwatch:", st)
	}
	if _, _, err := r.store.Create(ctx, "alerts", KindAlert, auto, now); err == nil {
		t.Fatal("an alert overlay took auto-start")
	}
}
