package earning

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

type fixture struct {
	s      *Service
	in     *events.Inbox
	db     *storage.DB
	ledger *points.Ledger
	vs     *viewers.Service
	n      int
	t0     time.Time
	list   *fakeChatters
	now    time.Time
}

type fakeChatters struct {
	list  []events.Actor
	err   error
	calls int
}

func (c *fakeChatters) Chatters(context.Context) ([]events.Actor, error) {
	c.calls++
	return c.list, c.err
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	in := events.NewInbox(db, nil)
	f := &fixture{in: in, db: db, ledger: points.New(db, nil), vs: viewers.New(db, in, nil), t0: time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)}
	f.s = f.newService()
	return f
}

func (f *fixture) newService() *Service {
	if f.list == nil {
		f.list = &fakeChatters{}
	}
	return New(Options{DB: f.db, Inbox: f.in, Settings: settings.NewStore(f.db), Viewers: f.vs, Points: f.ledger, Chatters: f.list,
		Now: func() time.Time { return f.now }})
}

// event adds an event from viewer id (login "u<id>") at t0+sec seconds.
func (f *fixture) event(t *testing.T, typ, id string, sec int, payload string, q events.Quality) {
	t.Helper()
	f.n++
	env := events.Envelope{Type: typ, Source: "twitch", SourceEventID: fmt.Sprintf("m%d", f.n), Payload: []byte(payload), DeliveryQuality: q,
		ChannelID: "100", ReceivedAt: f.t0.Add(time.Duration(sec) * time.Second)}
	if id != "" {
		env.Actor = &events.Actor{Platform: "twitch", ID: id, Login: "u" + id, DisplayName: "U" + id}
	}
	if _, err := f.in.Accept(context.Background(), env); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) balance(t *testing.T, id string) int64 {
	t.Helper()
	v, err := f.vs.ByAccount(context.Background(), "twitch", id)
	if errors.Is(err, viewers.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.ledger.Balance(context.Background(), v.ID, "")
	return b
}

func (f *fixture) on(t *testing.T, change func(*Rules)) {
	t.Helper()
	r := Defaults()
	r.On = true
	if change != nil {
		change(&r)
	}
	if _, err := f.s.SetRules(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) catchUp(t *testing.T) {
	t.Helper()
	if err := f.s.CatchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// What happened before earning first started never pays, even once turned on.
func TestStartsAtTheNewestEvent(t *testing.T) {
	f := newFixture(t)
	f.event(t, "audience.follow.created", "1", 0, `{}`, events.Confirmed)
	f.catchUp(t) // off: the cursor starts here
	f.on(t, nil)
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 0 {
		t.Fatalf("paid for a follow before earning started: %d", b)
	}
	f.event(t, "audience.follow.created", "2", 1, `{}`, events.Confirmed)
	f.catchUp(t)
	if b := f.balance(t, "2"); b != 50 {
		t.Fatal(b)
	}
}

// While earning is off, events pass by and are never paid later.
func TestOffPaysNothingEvenLater(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.event(t, "audience.follow.created", "1", 0, `{}`, events.Confirmed)
	f.catchUp(t)
	f.on(t, nil)
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 0 {
		t.Fatal(b)
	}
}

// Each rule pays what it says; gift recipients, anonymous gifts, test
// events, the streamer and core events never earn.
func TestEachRule(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, func(r *Rules) { r.Chat.LiveOnly = false })
	f.event(t, "audience.follow.created", "1", 0, `{}`, events.Confirmed)
	f.event(t, "monetization.subscription.started", "2", 0, `{"tier":1,"is_gift":false}`, events.Confirmed)
	f.event(t, "monetization.subscription.started", "3", 0, `{"tier":3,"is_gift":true}`, events.Confirmed) // a recipient
	f.event(t, "monetization.subscription.renewed", "4", 0, `{"tier":2,"months":5}`, events.Confirmed)
	f.event(t, "monetization.subscription.gifted", "5", 0, `{"tier":1,"total":5}`, events.Confirmed)
	f.event(t, "monetization.subscription.gifted", "", 0, `{"tier":1,"total":5,"is_anonymous":true}`, events.Confirmed)
	f.event(t, "monetization.bits.cheered", "6", 0, `{"bits":250}`, events.Confirmed)
	f.event(t, "monetization.bits.cheered", "7", 0, `{"bits":99}`, events.Confirmed)
	f.event(t, "chat.message.received", "8", 0, `{}`, events.Confirmed)
	f.event(t, "audience.follow.created", "9", 0, `{}`, events.Synthetic)
	f.event(t, "audience.follow.created", "100", 0, `{}`, events.Confirmed) // the streamer
	f.event(t, "chat.message.received", "10", 0, `{"chatter":{"role":"broadcaster"}}`, events.Confirmed)
	f.catchUp(t)
	for id, want := range map[string]int64{"1": 50, "2": 500, "3": 0, "4": 1000, "5": 1250, "6": 20, "7": 0, "8": 1, "9": 0, "100": 0, "10": 0} {
		if b := f.balance(t, id); b != want {
			t.Errorf("viewer %s has %d, want %d", id, b, want)
		}
	}
	v, _ := f.vs.ByAccount(context.Background(), "twitch", "5")
	h, _ := f.ledger.History(context.Background(), v.ID, 5)
	if len(h) != 1 || h[0].Memo != "gifting 5 subs" || h[0].By != By || h[0].Kind != points.Credit {
		t.Fatalf("%+v", h)
	}
}

// A follow pays once per viewer, ever: unfollowing and following again
// pays nothing, and neither does a later change to the amount.
func TestFollowOnceEver(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, nil)
	f.event(t, "audience.follow.created", "1", 0, `{}`, events.Confirmed)
	f.event(t, "audience.follow.created", "1", 60, `{}`, events.Confirmed)
	f.catchUp(t)
	f.on(t, func(r *Rules) { r.Follow = 80 })
	f.event(t, "audience.follow.created", "1", 120, `{}`, events.Confirmed)
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 50 {
		t.Fatal(b)
	}
}

// Chat pays at most once per wait for each viewer, and the wait survives a
// restart.
func TestChatWait(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, func(r *Rules) { r.Chat.LiveOnly = false })
	for _, sec := range []int{0, 10, 59} {
		f.event(t, "chat.message.received", "1", sec, `{}`, events.Confirmed)
	}
	f.event(t, "chat.message.received", "2", 30, `{}`, events.Confirmed)
	f.catchUp(t)
	if a, b := f.balance(t, "1"), f.balance(t, "2"); a != 1 || b != 1 {
		t.Fatal(a, b)
	}
	// A restart: a new service, the same database.
	f.s = f.newService()
	f.event(t, "chat.message.received", "1", 61, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "1", 90, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "1", 125, `{}`, events.Confirmed)
	f.catchUp(t)
	if a := f.balance(t, "1"); a != 3 {
		t.Fatal(a)
	}
	// No wait: every message pays.
	f.on(t, func(r *Rules) { r.Chat.LiveOnly = false; r.Chat.EverySeconds = 0 })
	f.event(t, "chat.message.received", "2", 31, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "2", 31, `{}`, events.Confirmed)
	f.catchUp(t)
	if b := f.balance(t, "2"); b != 3 {
		t.Fatal(b)
	}
}

// "Only while live": chat pays only when the newest real stream event says
// live. Unknown pays nothing, and a test go-live changes nothing. The state
// is read again after a restart.
func TestChatOnlyWhileLive(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, nil)
	f.event(t, "chat.message.received", "1", 0, `{}`, events.Confirmed) // unknown
	f.event(t, "broadcast.channel.online", "", 1, `{}`, events.Synthetic)
	f.event(t, "chat.message.received", "1", 100, `{}`, events.Confirmed) // still unknown
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 0 || f.s.LiveState() != Unknown {
		t.Fatal(b, f.s.LiveState())
	}
	f.event(t, "broadcast.stream.started", "", 200, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "1", 201, `{}`, events.Confirmed)
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 1 || f.s.LiveState() != Live {
		t.Fatal(b, f.s.LiveState())
	}
	f.s = f.newService()
	f.event(t, "chat.message.received", "1", 400, `{}`, events.Confirmed)
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 2 || f.s.LiveState() != Live {
		t.Fatal("after a restart:", b, f.s.LiveState())
	}
	f.event(t, "broadcast.channel.offline", "", 500, `{}`, events.Confirmed)
	f.event(t, "chat.message.received", "1", 600, `{}`, events.Confirmed)
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 2 || f.s.LiveState() != Offline {
		t.Fatal(b, f.s.LiveState())
	}
}

// Reading the same events again (a lost cursor, say) pays nothing more.
func TestReadAgainPaysOnce(t *testing.T) {
	f := newFixture(t)
	f.catchUp(t)
	f.on(t, func(r *Rules) { r.Chat.LiveOnly = false; r.Chat.EverySeconds = 0 })
	f.event(t, "chat.message.received", "1", 0, `{}`, events.Confirmed)
	f.event(t, "monetization.bits.cheered", "1", 0, `{"bits":1000}`, events.Confirmed)
	f.event(t, "audience.follow.created", "1", 0, `{}`, events.Confirmed)
	f.catchUp(t)
	if _, err := f.db.Exec("UPDATE earning_cursor SET seq = 0"); err != nil {
		t.Fatal(err)
	}
	f.s = f.newService()
	f.catchUp(t)
	if b := f.balance(t, "1"); b != 1+100+50 {
		t.Fatal(b)
	}
	if problems, err := f.ledger.Audit(context.Background()); err != nil || len(problems) != 0 {
		t.Fatal(problems, err)
	}
}

func TestCheck(t *testing.T) {
	if err := Defaults().Check(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Rules{{Follow: -1}, {Bits: MaxAward + 1}, {Chat: Chat{EverySeconds: -1}}, {Chat: Chat{EverySeconds: MaxWait + 1}}} {
		if err := r.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	f := newFixture(t)
	if r, err := f.s.Rules(context.Background()); err != nil || r != Defaults() || r.On {
		t.Fatal("rules start as the defaults, off:", r, err)
	}
	if _, err := f.s.SetRules(context.Background(), Rules{Gift: -5}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
