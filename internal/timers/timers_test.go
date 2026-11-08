package timers

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time         { return c.t }
func (c *clock) add(d time.Duration)    { c.t = c.t.Add(d) }
func (c *clock) addSec(n int)           { c.add(time.Duration(n) * time.Second) }
func at(c *clock) func() time.Time      { return c.now }
func noRandom(n int64) int64            { return 0 }
func maxRandom(n int64) int64           { return n - 1 }
func fixed(v int64) func(n int64) int64 { return func(int64) int64 { return v } }
func eventsOf(t *testing.T, in *events.Inbox) []events.Record {
	return latest(t, in, events.TimerFired)
}

type rig struct {
	t     *testing.T
	db    *storage.DB
	in    *events.Inbox
	clk   *clock
	svc   *Service
	chats int
}

func newRig(t *testing.T, random func(int64) int64) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, in: events.NewInbox(db, nil), clk: &clock{t: time.Date(2026, 11, 8, 10, 0, 0, 0, time.Local)}}
	r.svc = New(Options{DB: db, Inbox: r.in, Now: at(r.clk), Random: random})
	return r
}

// restart is a new service on the same database, as after a restart.
func (r *rig) restart() {
	r.svc = New(Options{DB: r.db, Inbox: r.in, Now: at(r.clk), Random: noRandom})
}

func (r *rig) save(tm Timer) Timer {
	r.t.Helper()
	saved, _, err := r.svc.Save(context.Background(), tm, r.clk.now())
	if err != nil {
		r.t.Fatal(err)
	}
	return saved
}

func (r *rig) tick() { r.svc.RunDue(context.Background()) }

func (r *rig) fired() []events.Record { return eventsOf(r.t, r.in) }

func (r *rig) event(typ, source string, q events.Quality) {
	r.eventWith(typ, source, q, `{"chatter":{"role":"viewer"}}`)
}

func (r *rig) eventWith(typ, source string, q events.Quality, payload string) {
	r.t.Helper()
	r.chats++
	_, err := r.in.Accept(context.Background(), events.Envelope{Type: typ, Source: source,
		SourceEventID: "e" + strconv.Itoa(r.chats), Payload: json.RawMessage(payload), DeliveryQuality: q})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) chat(n int, q events.Quality) {
	for range n {
		r.event("chat.message.received", "twitch", q)
	}
}

func latest(t *testing.T, in *events.Inbox, typ string) []events.Record {
	t.Helper()
	recs, err := in.After(context.Background(), 0, 1000, events.Filter{typ})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func payload(t *testing.T, rec events.Record) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(rec.Payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheck(t *testing.T) {
	ok := []Timer{
		{ID: "socials", Name: "Socials", EverySeconds: 600},
		{ID: "a_b-1", Name: "x", EverySeconds: 10, UpToSeconds: 86400},
		{ID: "nightly", Name: "Nightly", Cron: "0 20 * * *", ChatLines: 1000},
	}
	for _, tm := range ok {
		if err := tm.Check(); err != nil {
			t.Errorf("%+v: %v", tm, err)
		}
	}
	bad := []Timer{
		{ID: "", Name: "x", EverySeconds: 60},
		{ID: "Socials", Name: "x", EverySeconds: 60},
		{ID: "a.b", Name: "x", EverySeconds: 60},
		{ID: "x", Name: "  ", EverySeconds: 60},
		{ID: "x", Name: "x", EverySeconds: 9},
		{ID: "x", Name: "x", EverySeconds: 86401},
		{ID: "x", Name: "x"},
		{ID: "x", Name: "x", EverySeconds: 60, UpToSeconds: 60},
		{ID: "x", Name: "x", EverySeconds: 60, UpToSeconds: 86401},
		{ID: "x", Name: "x", Cron: "0 20 * * *", EverySeconds: 60},
		{ID: "x", Name: "x", Cron: "nope"},
		{ID: "x", Name: "x", EverySeconds: 60, ChatLines: -1},
		{ID: "x", Name: "x", EverySeconds: 60, ChatLines: 1001},
		{ID: "x", Name: "x", EverySeconds: 60, Counter: -1},
	}
	for _, tm := range bad {
		if err := tm.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted", tm)
		}
	}
}

func TestIntervalGoesOffAndCounts(t *testing.T) {
	r := newRig(t, noRandom)
	r.save(Timer{ID: "socials", Name: "Socials", Enabled: true, EverySeconds: 60})
	r.tick() // scheduled from now
	r.clk.addSec(59)
	r.tick()
	if n := len(r.fired()); n != 0 {
		t.Fatalf("went off early: %d", n)
	}
	r.clk.addSec(1)
	r.tick()
	r.tick() // once per turn
	r.clk.addSec(60)
	r.tick()
	f := r.fired()
	if len(f) != 2 {
		t.Fatalf("%d fires", len(f))
	}
	p := payload(t, f[1])
	if p["timer_id"] != "socials" || p["name"] != "Socials" || p["counter"] != float64(2) || p["manual"] != false ||
		p["every_seconds"] != float64(60) || f[1].Source != "timers" || f[1].DeliveryQuality != events.Confirmed {
		t.Fatalf("%+v %v", f[1].Envelope, p)
	}
	got, _ := r.svc.Get(context.Background(), "socials")
	if got.Counter != 2 || got.LastFiredAt == nil {
		t.Fatalf("%+v", got)
	}
}

func TestRandomRangeStaysWithinIt(t *testing.T) {
	r := newRig(t, maxRandom)
	r.save(Timer{ID: "r", Name: "R", Enabled: true, EverySeconds: 60, UpToSeconds: 90})
	r.tick()
	r.clk.addSec(89)
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("before the top of the range")
	}
	r.clk.addSec(1)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("not at the top of the range")
	}
	r2 := newRig(t, fixed(15))
	r2.save(Timer{ID: "r", Name: "R", Enabled: true, EverySeconds: 60, UpToSeconds: 90})
	r2.tick()
	st, _ := r2.svc.StatusOf(context.Background(), "r")
	if st.NextAt == nil || st.NextAt.Sub(r2.clk.now()) != 75*time.Second {
		t.Fatalf("%+v", st)
	}
}

func TestARestartNeverFiresMissedTurns(t *testing.T) {
	r := newRig(t, noRandom)
	r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60})
	r.tick()
	r.clk.add(time.Hour) // StreamGremlin was closed for an hour
	r.restart()
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("a missed turn fired on start")
	}
	r.clk.addSec(60)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("didn't go off a full interval after start")
	}
}

func TestChatLinesWaitInsteadOfSkipping(t *testing.T) {
	r := newRig(t, noRandom)
	r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60, ChatLines: 3})
	r.tick()
	r.chat(2, events.Confirmed)
	r.chat(5, events.Synthetic) // test events never count
	for _, role := range []string{"broadcaster", "broadcaster"} {
		// Your own messages, and what StreamGremlin sends as you, don't count.
		r.eventWith("chat.message.received", "twitch", events.Confirmed, `{"chatter":{"role":"`+role+`"}}`)
	}
	r.eventWith("chat.command.received", "twitch", events.Confirmed, `{"chatter":{"role":"viewer"}}`) // commands are also messages
	r.clk.addSec(60)
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("went off short of chat lines")
	}
	st, _ := r.svc.StatusOf(context.Background(), "x")
	if st.Waiting != "chat" || st.Lines != 2 {
		t.Fatalf("%+v", st)
	}
	r.clk.add(10 * time.Minute) // still waiting, long after its turn
	r.chat(1, events.Confirmed)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("didn't go off once chat caught up")
	}
	// Lines count again from the fire, and the next interval from then.
	r.chat(2, events.Confirmed)
	r.clk.addSec(60)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("old lines counted twice")
	}
	r.chat(1, events.Confirmed)
	r.tick()
	if len(r.fired()) != 2 {
		t.Fatal("second turn")
	}
}

func TestLiveOnlySkipsTurnsWhileNotLive(t *testing.T) {
	r := newRig(t, noRandom)
	r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60, LiveOnly: true})
	r.tick()
	r.clk.addSec(60)
	r.tick()                                                     // OBS never said: unknown is not live
	r.event("broadcast.stream.started", "obs", events.Synthetic) // a test event isn't going live
	r.clk.addSec(60)
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("went off while not live")
	}
	r.event("broadcast.stream.started", "obs", events.Confirmed)
	r.clk.addSec(30) // the skipped turn isn't made up at once
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("made up a skipped turn")
	}
	r.clk.addSec(30)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("didn't go off while live")
	}
	r.event("broadcast.stream.stopped", "obs", events.Confirmed)
	r.clk.addSec(60)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("went off after the stream stopped")
	}
	if live, ok := r.svc.Live(context.Background()); live || !ok {
		t.Fatalf("live %v %v", live, ok)
	}
}

func TestRunOnceTurnsItselfOffButRunNowDoesnt(t *testing.T) {
	r := newRig(t, noRandom)
	ctx := context.Background()
	r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60, Once: true})
	r.tick()
	if _, _, err := r.svc.Fire(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.Get(ctx, "x"); !got.Enabled || got.Counter != 1 {
		t.Fatalf("run now: %+v", got)
	}
	r.clk.addSec(60)
	r.tick()
	r.clk.addSec(60)
	r.tick()
	got, _ := r.svc.Get(ctx, "x")
	if got.Enabled || got.Counter != 2 || len(r.fired()) != 2 {
		t.Fatalf("%+v, %d fires", got, len(r.fired()))
	}
	p := payload(t, r.fired()[0])
	if p["manual"] != true {
		t.Fatalf("%v", p)
	}
	if st, _ := r.svc.StatusOf(ctx, "x"); st.Waiting != "off" || st.NextAt != nil {
		t.Fatalf("%+v", st)
	}
}

func TestSavingKeepsTheCountAndUnchangedKeepsTheSchedule(t *testing.T) {
	r := newRig(t, noRandom)
	ctx := context.Background()
	first := r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60, Counter: 41}) // an import's count
	if first.Counter != 41 {
		t.Fatalf("%+v", first)
	}
	r.tick()
	r.clk.addSec(30)
	same := r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60, Counter: 0})
	if same.Counter != 41 || !same.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("unchanged save: %+v", same)
	}
	r.tick()
	r.clk.addSec(30)
	r.tick()
	if len(r.fired()) != 1 || payload(t, r.fired()[0])["counter"] != float64(42) {
		t.Fatal("an unchanged save restarted the schedule, or the count was lost")
	}
	// A change starts the schedule again, and keeps the count.
	r.clk.addSec(30)
	changed := r.save(Timer{ID: "x", Name: "X again", Enabled: true, EverySeconds: 60, Counter: 7})
	if changed.Counter != 42 {
		t.Fatalf("%+v", changed)
	}
	r.tick()
	r.clk.addSec(30)
	r.tick()
	if len(r.fired()) != 1 {
		t.Fatal("a change didn't restart the schedule")
	}
	if _, err := r.svc.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSetState(t *testing.T) {
	r := newRig(t, noRandom)
	ctx := context.Background()
	r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 60})
	r.tick()
	for _, c := range []struct {
		st   State
		want bool
	}{{Off, false}, {Off, false}, {Toggle, true}, {Toggle, false}, {On, true}, {On, true}} {
		on, err := r.svc.SetState(ctx, "x", c.st, r.clk.now())
		if err != nil || on != c.want {
			t.Fatalf("%s: %v %v", c.st, on, err)
		}
	}
	if _, err := r.svc.SetState(ctx, "nope", On, r.clk.now()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.svc.SetState(ctx, "x", "maybe", r.clk.now()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// Turning it on again (off then on, a second later) starts its count again.
	r.clk.addSec(50)
	r.tick()
	_, _ = r.svc.SetState(ctx, "x", Off, r.clk.now())
	r.clk.addSec(1)
	_, _ = r.svc.SetState(ctx, "x", On, r.clk.now())
	r.tick()
	r.clk.addSec(30)
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("turning it on didn't restart the count")
	}
}

func TestCronSchedule(t *testing.T) {
	r := newRig(t, noRandom)
	r.clk.t = time.Date(2026, 11, 8, 19, 59, 30, 0, time.Local)
	r.save(Timer{ID: "eight", Name: "Eight", Enabled: true, Cron: "0 20 * * *"})
	r.tick()
	r.clk.addSec(29)
	r.tick()
	if len(r.fired()) != 0 {
		t.Fatal("early")
	}
	r.clk.addSec(1)
	r.tick()
	f := r.fired()
	if len(f) != 1 || payload(t, f[0])["cron"] != "0 20 * * *" {
		t.Fatalf("%v", f)
	}
	st, _ := r.svc.StatusOf(context.Background(), "eight")
	if st.NextAt == nil || !st.NextAt.Equal(time.Date(2026, 11, 9, 20, 0, 0, 0, time.Local)) {
		t.Fatalf("%+v", st)
	}
	r.save(Timer{ID: "never", Name: "Never", Enabled: true, Cron: "0 0 31 2 *"})
	r.tick()
	if st, _ := r.svc.StatusOf(context.Background(), "never"); st.Waiting != "never" || st.NextAt != nil {
		t.Fatalf("%+v", st)
	}
}

func TestDeletedAndMadeAgainStartsANewSeries(t *testing.T) {
	r := newRig(t, noRandom)
	ctx := context.Background()
	r.save(Timer{ID: "x", Name: "X", EverySeconds: 60})
	if _, _, err := r.svc.Fire(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.svc.Delete(ctx, "x"); !ok {
		t.Fatal("delete")
	}
	if ok, _ := r.svc.Delete(ctx, "x"); ok {
		t.Fatal("deleted twice")
	}
	if _, _, err := r.svc.Fire(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	r.clk.add(time.Millisecond)
	r.save(Timer{ID: "x", Name: "X", EverySeconds: 60})
	tm, _, err := r.svc.Fire(ctx, "x")
	if err != nil || tm.Counter != 1 || len(r.fired()) != 2 {
		t.Fatalf("%+v %v", tm, err)
	}
}

func TestDisabledTimersNeverGoOff(t *testing.T) {
	r := newRig(t, noRandom)
	r.save(Timer{ID: "x", Name: "X", EverySeconds: 10})
	for range 5 {
		r.tick()
		r.clk.addSec(10)
	}
	if len(r.fired()) != 0 {
		t.Fatal("an off timer went off")
	}
	if st, _ := r.svc.StatusOf(context.Background(), "x"); st.Waiting != "off" {
		t.Fatalf("%+v", st)
	}
}

func TestTheLoopRunsAndStops(t *testing.T) {
	r := newRig(t, noRandom)
	r.svc = New(Options{DB: r.db, Inbox: r.in, Tick: time.Millisecond, Now: at(r.clk), Random: noRandom})
	r.save(Timer{ID: "x", Name: "X", Enabled: true, EverySeconds: 10})
	r.svc.Start(context.Background())
	r.svc.Changed()
	r.svc.Close()
	r.svc.Close() // twice is harmless
}
