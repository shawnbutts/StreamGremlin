package deck

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/ads"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

type fakeOBS struct {
	connected bool
	current   string
	by        string
}

func (f *fakeOBS) Scenes(context.Context) (string, []string, error) {
	if !f.connected {
		return "", nil, errors.New("not connected")
	}
	return f.current, []string{"Game", "BRB"}, nil
}

func (f *fakeOBS) SwitchScene(_ context.Context, scene, by string) (string, error) {
	prev := f.current
	f.current, f.by = scene, by
	return prev, nil
}

type fakeOverlays map[string]overlays.Overlay

func (f fakeOverlays) Get(_ context.Context, name string) (overlays.Overlay, error) {
	o, ok := f[name]
	if !ok {
		return overlays.Overlay{}, overlays.ErrNotFound
	}
	return o, nil
}

type fakeClocks struct {
	st  map[string]*overlays.TimerState
	ops []string
	now func() time.Time
}

func (f *fakeClocks) State(_ context.Context, name string) (*overlays.TimerState, error) {
	return f.st[name], nil
}

func (f *fakeClocks) Do(_ context.Context, name string, op overlays.TimerOp) (overlays.TimerState, error) {
	f.ops = append(f.ops, op.Action)
	cur := f.st[name]
	if cur == nil {
		cur = &overlays.TimerState{BaseMS: 60_000}
	}
	switch op.Action {
	case "start":
		cur.Running, cur.Since = true, f.now()
	case "pause": // a stopwatch keeps what it counted
		cur.BaseMS += f.now().Sub(cur.Since).Milliseconds()
		cur.Running = false
	case "reset":
		cur = nil
	}
	f.st[name] = cur
	if cur == nil {
		return overlays.TimerState{}, nil
	}
	return *cur, nil
}

type fakeAds struct {
	reads, snoozes int
	next           time.Time
}

func (f *fakeAds) Schedule(context.Context) (twitch.AdSchedule, error) {
	f.reads++
	n := f.next
	return twitch.AdSchedule{NextAdAt: &n, SnoozeCount: 3 - f.snoozes}, nil
}

func (f *fakeAds) Snooze(_ context.Context, opKey, _ string) (ads.Request, error) {
	f.snoozes++
	f.next = f.next.Add(5 * time.Minute)
	return ads.Request{Action: "snooze", Status: "accepted"}, nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func moreWorld(t *testing.T) (*world, *fakeOBS, *fakeClocks, *fakeAds, *clock) {
	t.Helper()
	w := newWorld(t)
	c := &clock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	ob := &fakeOBS{connected: true, current: "Game"}
	cl := &fakeClocks{st: map[string]*overlays.TimerState{}, now: c.now}
	ad := &fakeAds{next: c.t.Add(10 * time.Minute)}
	w.s.o.Now = c.now
	w.s.o.Inbox = events.NewInbox(w.db, nil)
	w.s.o.Scener = ob
	w.s.o.Overlays = fakeOverlays{
		"brb":   {Name: "brb", Kind: overlays.KindTimer, Look: overlays.Look{TimerMode: overlays.TimerCountdown, TimerLength: 300, TimerLabel: "Back soon"}},
		"watch": {Name: "watch", Kind: overlays.KindTimer, Look: overlays.Look{TimerMode: overlays.TimerStopwatch}},
		"clock": {Name: "clock", Kind: overlays.KindTimer},
	}
	w.s.o.Clocks = cl
	w.s.o.AdsSource = ad
	return w, ob, cl, ad, c
}

// A run records one event per request id, naming the workflow; a workflow
// whose trigger brings a viewer is said to.
func TestRuns(t *testing.T) {
	w, _, _, _, _ := moreWorld(t)
	ctx := context.Background()
	if _, _, err := w.s.Run(ctx, "wf_a", "r1", "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("not chosen: %v", err)
	}
	w.wf.on["wf_cmd"] = true
	w.choose(t, Settings{Runs: []string{"wf_a", "wf_cmd", "wf_gone"}})
	st, dup, err := w.s.Run(ctx, "wf_a", "r1", "cl_1")
	if err != nil || dup || !st.On || st.NeedsViewer {
		t.Fatalf("run: %+v %v %v", st, dup, err)
	}
	if _, dup, err := w.s.Run(ctx, "wf_a", "r1", "cl_1"); err != nil || !dup {
		t.Fatalf("again: %v %v", dup, err)
	}
	rec, err := w.s.o.Inbox.BySource(ctx, Source, "cl_1:r1")
	if err != nil || rec.Type != events.DeckRun {
		t.Fatalf("event: %+v %v", rec, err)
	}
	var p map[string]string
	_ = json.Unmarshal(rec.Payload, &p)
	if p["workflow_id"] != "wf_a" || p["client_id"] != "cl_1" {
		t.Errorf("payload %v", p)
	}
	if _, _, err := w.s.Run(ctx, "wf_gone", "r2", "cl_1"); !errors.Is(err, ErrGone) {
		t.Errorf("gone: %v", err)
	}
	v, err := w.s.View(ctx)
	if err != nil || len(v.Runs) != 3 || !v.Runs[1].NeedsViewer || !v.Runs[2].Gone {
		t.Errorf("view: %+v %v", v.Runs, err)
	}
}

func TestScenes(t *testing.T) {
	w, ob, _, _, _ := moreWorld(t)
	ctx := context.Background()
	if v, _ := w.s.View(ctx); v.Scenes != nil {
		t.Fatal("scenes shown unchosen")
	}
	if _, err := w.s.Scene(ctx, "BRB", "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("not chosen: %v", err)
	}
	w.choose(t, Settings{Scenes: true})
	sc, err := w.s.Scene(ctx, "BRB", "cl_1")
	if err != nil || sc.Current != "BRB" || ob.by != "stream_deck" {
		t.Fatalf("switch: %+v %v by %s", sc, err, ob.by)
	}
	ob.connected = false
	if v, _ := w.s.View(ctx); v.Scenes == nil || v.Scenes.Known {
		t.Errorf("OBS gone: %+v", v.Scenes)
	}
}

// Countdowns: start and pause are harmless to repeat; time is counted from
// the state; a clock isn't a countdown.
func TestCountdowns(t *testing.T) {
	w, _, cl, _, c := moreWorld(t)
	ctx := context.Background()
	w.choose(t, Settings{Countdowns: []string{"brb", "watch", "clock"}})
	v, _ := w.s.View(ctx)
	if len(v.Countdowns) != 3 || v.Countdowns[0].MS != 300_000 || v.Countdowns[0].Label != "Back soon" || v.Countdowns[1].MS != 0 || !v.Countdowns[2].Gone {
		t.Fatalf("view: %+v", v.Countdowns)
	}
	for range 2 {
		if _, err := w.s.Countdown(ctx, "watch", "start", "cl_1"); err != nil {
			t.Fatal(err)
		}
	}
	if len(cl.ops) != 1 {
		t.Errorf("started twice: %v", cl.ops)
	}
	c.t = c.t.Add(90 * time.Second)
	got, _ := w.s.Countdown(ctx, "watch", "pause", "cl_1")
	if got.Running || got.MS != 60_000+90_000 {
		t.Errorf("paused stopwatch: %+v", got)
	}
	if _, err := w.s.Countdown(ctx, "watch", "pause", "cl_1"); err != nil || len(cl.ops) != 2 {
		t.Errorf("paused twice: %v %v", err, cl.ops)
	}
	if _, err := w.s.Countdown(ctx, "clock", "start", "cl_1"); !errors.Is(err, ErrGone) {
		t.Errorf("a clock: %v", err)
	}
	if _, err := w.s.Countdown(ctx, "brb", "explode", "cl_1"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad action: %v", err)
	}
	if _, err := w.s.Countdown(ctx, "other", "start", "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("not chosen: %v", err)
	}
}

// The schedule is read from Twitch at most every 30 seconds, and again
// after a snooze.
func TestAds(t *testing.T) {
	w, _, _, ad, c := moreWorld(t)
	ctx := context.Background()
	if _, err := w.s.Snooze(ctx, "s1", "cl_1"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("not chosen: %v", err)
	}
	w.choose(t, Settings{Ads: true})
	for range 5 {
		if v, _ := w.s.View(ctx); v.Ads == nil || !v.Ads.Known || v.Ads.Snoozes != 3 {
			t.Fatalf("ads: %+v", v.Ads)
		}
	}
	if ad.reads != 1 {
		t.Errorf("read %d times in a moment", ad.reads)
	}
	c.t = c.t.Add(AdsEvery)
	_, _ = w.s.View(ctx)
	if ad.reads != 2 {
		t.Errorf("not read again after 30s: %d", ad.reads)
	}
	a, err := w.s.Snooze(ctx, "s1", "cl_1")
	if err != nil || a.Snoozes != 2 || ad.reads != 3 || !a.NextAdAt.Equal(c.t.Add(-AdsEvery).Add(15*time.Minute)) {
		t.Errorf("snoozed: %+v %v reads %d", a, err, ad.reads)
	}
}
