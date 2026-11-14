package audience

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newInbox(t *testing.T) *events.Inbox {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	return events.NewInbox(db, slog.New(slog.DiscardHandler))
}

func chat(t *testing.T, in *events.Inbox, platform, id, name string, at time.Time, extra map[string]any) {
	t.Helper()
	p := map[string]any{"text": "hi", "chatter": map[string]any{"is_broadcaster": false}}
	for k, v := range extra {
		p[k] = v
	}
	raw, _ := json.Marshal(p)
	q := events.Confirmed
	if extra["synthetic"] == true {
		q = events.Synthetic
	}
	env := events.Envelope{EventID: events.NewID(), Type: "chat.message.received", Source: platform, SourceEventID: events.NewID(),
		OccurredAt: at, Actor: &events.Actor{Platform: platform, ID: id, Login: name, DisplayName: name}, Payload: raw, DeliveryQuality: q}
	env.CorrelationID = env.EventID
	if _, err := in.Accept(context.Background(), env); err != nil {
		t.Fatal(err)
	}
}

func waitType(t *testing.T, in *events.Inbox, typ string, n int) []events.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs, err := in.After(context.Background(), 0, 1000, events.Filter{typ})
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) >= n || time.Now().After(deadline) {
			if len(recs) != n {
				t.Fatalf("%s: %d, want %d", typ, len(recs), n)
			}
			return recs
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestViewersChanged(t *testing.T) {
	if ViewersChanged(100, 105) || !ViewersChanged(100, 110) || !ViewersChanged(0, 1) || ViewersChanged(2, 2) {
		t.Fatal("thresholds")
	}
}

func TestActiveChatters(t *testing.T) {
	in := newInbox(t)
	now := time.Now()
	// Spoke before StreamGremlin started, within the window: remembered.
	chat(t, in, "twitch", "1", "ann", now.Add(-3*time.Minute), nil)
	// Too long ago, history, a test, and you: never listed.
	chat(t, in, "twitch", "2", "old", now.Add(-20*time.Minute), nil)
	chat(t, in, events.SourceYouTube, "UCh", "hist", now, map[string]any{"backlog": true})
	chat(t, in, "twitch", "3", "test", now, map[string]any{"synthetic": true})
	chat(t, in, "twitch", "9", "me", now, map[string]any{"chatter": map[string]any{"is_broadcaster": true}})
	var mu sync.Mutex
	ytLive := false
	s := New(Options{Inbox: in, YouTubeLive: func() bool { mu.Lock(); defer mu.Unlock(); return ytLive },
		ActiveEvery: 20 * time.Millisecond, ViewersEvery: time.Hour})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	chat(t, in, events.SourceYouTube, "UCbo", "bo", time.Now(), nil)
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Active()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := s.Active()
	if len(got) != 2 || got[0].DisplayName != "bo" || got[1].DisplayName != "ann" {
		t.Fatalf("%+v", got)
	}
	// Not live: nothing is announced.
	time.Sleep(60 * time.Millisecond)
	waitType(t, in, TypeActive, 0)
	mu.Lock()
	ytLive = true
	mu.Unlock()
	recs := waitType(t, in, TypeActive, 1)
	var p struct {
		Count, TwitchCount, YouTubeCount int
		Names                            string
		Minutes                          int
	}
	var raw map[string]any
	_ = json.Unmarshal(recs[0].Payload, &raw)
	p.Count, p.Names = int(raw["count"].(float64)), raw["names"].(string)
	p.TwitchCount, p.YouTubeCount = int(raw["twitch_count"].(float64)), int(raw["youtube_count"].(float64))
	if p.Count != 2 || p.TwitchCount != 1 || p.YouTubeCount != 1 || p.Names != "bo, ann" || raw["minutes"] != 10.0 {
		t.Fatalf("%v", raw)
	}
	if recs[0].Source != Source {
		t.Fatal(recs[0].Source)
	}
}

func TestTwitchViewers(t *testing.T) {
	in := newInbox(t)
	var mu sync.Mutex
	live, viewers, fail := true, 50, false
	s := New(Options{Inbox: in, ActiveEvery: time.Hour, ViewersEvery: 10 * time.Millisecond,
		TwitchStream: func(context.Context) (bool, int, error) {
			mu.Lock()
			defer mu.Unlock()
			if fail {
				return false, 0, errors.New("signed out")
			}
			return live, viewers, nil
		}})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	set := func(f func()) { mu.Lock(); f(); mu.Unlock(); time.Sleep(60 * time.Millisecond) }
	set(func() {})
	set(func() { viewers = 53 }) // under a tenth
	waitType(t, in, TypeViewers, 0)
	set(func() { viewers = 60 })
	recs := waitType(t, in, TypeViewers, 1)
	var p map[string]any
	_ = json.Unmarshal(recs[0].Payload, &p)
	if p["viewers"] != 60.0 || p["previous"] != 50.0 || recs[0].Source != "twitch" {
		t.Fatalf("%v %s", p, recs[0].Source)
	}
	// Unknown changes nothing; offline forgets the count.
	set(func() { fail = true; viewers = 10 })
	waitType(t, in, TypeViewers, 1)
	set(func() { fail = false; live = false })
	set(func() { live = true; viewers = 10 })
	waitType(t, in, TypeViewers, 1)
}
