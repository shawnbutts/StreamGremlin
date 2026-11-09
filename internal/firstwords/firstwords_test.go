package firstwords

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type rig struct {
	t     *testing.T
	inbox *events.Inbox
	set   *settings.Store
	svc   *Service
	mu    sync.Mutex
	now   time.Time
}

var t0 = time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, inbox: events.NewInbox(db, nil), set: settings.NewStore(db), now: t0}
	r.start()
	return r
}

func (r *rig) clock() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }

func (r *rig) at(t time.Time) { r.mu.Lock(); r.now = t; r.mu.Unlock() }

func (r *rig) start() {
	r.t.Helper()
	if r.svc != nil {
		r.svc.Close()
	}
	r.svc = New(Options{Inbox: r.inbox, Settings: r.set, Now: r.clock})
	if err := r.svc.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	svc := r.svc
	r.t.Cleanup(svc.Close)
}

// say stores a chat message, at the rig's time, from login; id names it.
func (r *rig) say(id, login, role string, q events.Quality) {
	r.t.Helper()
	p, _ := json.Marshal(map[string]any{"text": "hi from " + login, "chatter": map[string]any{"role": role, "login": login}})
	env := events.Envelope{Type: "chat.message.received", Source: "twitch", SourceEventID: id, OccurredAt: r.clock(),
		Actor: &events.Actor{Platform: "twitch", ID: "id-" + login, Login: login}, Payload: p, DeliveryQuality: q}
	if _, err := r.inbox.Accept(context.Background(), env); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) live(id string, q events.Quality) {
	r.t.Helper()
	env := events.Envelope{Type: "broadcast.stream.started", Source: "obs", SourceEventID: id, OccurredAt: r.clock(),
		Payload: json.RawMessage(`{}`), DeliveryQuality: q}
	if _, err := r.inbox.Accept(context.Background(), env); err != nil {
		r.t.Fatal(err)
	}
}

// firsts waits for want first-words events and returns who they're from,
// in order, with test ones marked.
func (r *rig) firsts(want int) []string {
	r.t.Helper()
	var got []string
	for range 200 {
		recs, _ := r.inbox.After(context.Background(), 0, 500, events.Filter{Type})
		got = got[:0]
		for _, x := range recs {
			name := x.Actor.Login
			if x.DeliveryQuality == events.Synthetic {
				name += " (test)"
			}
			got = append(got, name)
		}
		if len(got) >= want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond) // and no more after
	recs, _ := r.inbox.After(context.Background(), 0, 500, events.Filter{Type})
	if len(recs) != len(got) {
		var ids []string
		for _, x := range recs {
			ids = append(ids, x.SourceEventID)
		}
		r.t.Fatalf("more first words came: %d, then %d: %v", len(got), len(recs), ids)
	}
	return got
}

func eq(a, b []string) bool { return slices.Equal(a, b) }

// Each viewer once a stream; the streamer never; a redelivered message
// never twice; the event copies the message.
func TestFirstWordsOncePerViewer(t *testing.T) {
	r := newRig(t)
	r.say("m1", "fan", "viewer", events.Confirmed)
	r.say("m2", "fan", "viewer", events.Confirmed)
	r.say("m3", "pal", "moderator", events.Confirmed)
	r.say("m4", "streamer", "broadcaster", events.Confirmed)
	r.say("m1", "fan", "viewer", events.Confirmed) // redelivered
	if got := r.firsts(2); !eq(got, []string{"fan", "pal"}) {
		t.Fatal(got)
	}
	recs, _ := r.inbox.After(context.Background(), 0, 10, events.Filter{Type})
	if recs[0].SourceEventID != "first:m1" || recs[0].CausationID == "" || string(recs[0].Payload) == "" || recs[0].Source != "twitch" {
		t.Fatalf("%+v", recs[0])
	}
	if st := r.svc.Status(); st.Viewers != 2 || st.Why != "12 hours" {
		t.Fatalf("%+v", st)
	}
}

// Going live, a reset by hand, and 12 quiet hours each start over; a
// reset survives a restart.
func TestFirstWordsStartOver(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.say("a1", "fan", "viewer", events.Confirmed)
	r.at(t0.Add(time.Hour))
	r.live("go1", events.Confirmed)
	r.at(t0.Add(time.Hour + time.Minute))
	r.say("a2", "fan", "viewer", events.Confirmed)
	r.firsts(2)
	if st := r.svc.Status(); st.Why != "stream start" || st.Viewers != 1 {
		t.Fatalf("%+v", st)
	}
	r.at(t0.Add(2 * time.Hour))
	if st, err := r.svc.Reset(ctx); err != nil || st.Why != "reset" || st.Viewers != 0 {
		t.Fatal(st, err)
	}
	r.at(t0.Add(2*time.Hour + time.Minute))
	r.say("a3", "fan", "viewer", events.Confirmed)
	r.firsts(3)
	// 12 quiet hours later is a new stream too.
	r.at(t0.Add(15 * time.Hour))
	r.say("a4", "fan", "viewer", events.Confirmed)
	if got := r.firsts(4); !eq(got, []string{"fan", "fan", "fan", "fan"}) {
		t.Fatal(got)
	}
	// After a restart: the last 12 hours are read again, nobody is greeted
	// twice, and the reset is kept.
	r.at(t0.Add(15*time.Hour + time.Minute))
	r.start()
	r.say("a5", "fan", "viewer", events.Confirmed)
	r.firsts(4)
	if st := r.svc.Status(); st.Viewers != 1 {
		t.Fatalf("%+v", st)
	}
}

// Test chat is judged apart: a real viewer's test message is their test
// first words, and a test going-live starts only test chat over.
func TestTestChatIsSeparate(t *testing.T) {
	r := newRig(t)
	r.say("b1", "fan", "viewer", events.Confirmed)
	r.say("b2", "fan", "viewer", events.Synthetic)
	r.firsts(2)
	r.at(t0.Add(time.Minute))
	r.live("go-test", events.Synthetic)
	r.at(t0.Add(2 * time.Minute))
	r.say("b3", "fan", "viewer", events.Confirmed)
	r.say("b4", "fan", "viewer", events.Synthetic)
	got := r.firsts(3)
	sort.Strings(got)
	if !eq(got, []string{"fan", "fan (test)", "fan (test)"}) {
		t.Fatal(got)
	}
}

// A message stored while the service was down gets first words when it
// starts, if it's recent; never twice.
func TestCatchingUpAfterARestart(t *testing.T) {
	r := newRig(t)
	r.svc.Close()
	r.say("c1", "late", "viewer", events.Confirmed)
	r.start()
	if got := r.firsts(1); !eq(got, []string{"late"}) {
		t.Fatal(got)
	}
	r.start()
	r.firsts(1)
}

// Going live is placed by order, not time: a message stored just before it,
// in the same millisecond, still counts as before the stream.
func TestGoingLiveInTheSameMillisecond(t *testing.T) {
	r := newRig(t)
	r.say("d1", "fan", "viewer", events.Confirmed)
	r.live("go", events.Confirmed) // the same time as d1
	r.say("d2", "fan", "viewer", events.Confirmed)
	if got := r.firsts(2); !eq(got, []string{"fan", "fan"}) {
		t.Fatal(got)
	}
}
