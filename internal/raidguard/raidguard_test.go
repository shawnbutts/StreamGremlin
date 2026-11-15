package raidguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

type rig struct {
	t      *testing.T
	inbox  *events.Inbox
	set    *settings.Store
	g      *Service
	mu     sync.Mutex
	known  map[string]time.Time
	made   map[string]time.Time
	locks  int
	lookup int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, inbox: events.NewInbox(db, slog.New(slog.DiscardHandler)), set: settings.NewStore(db), known: map[string]time.Time{},
		made: map[string]time.Time{}}
	r.start()
	return r
}

func (r *rig) start() {
	r.g = New(Options{Inbox: r.inbox, Settings: r.set,
		Known: func(_ context.Context, platform, id string) (time.Time, bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			t, ok := r.known[platform+":"+id]
			return t, ok, nil
		},
		Users: func(_ context.Context, _, ids []string) ([]twitch.User, twitch.RateInfo, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.lookup++
			var out []twitch.User
			for _, id := range ids {
				if m, ok := r.made[id]; ok {
					out = append(out, twitch.User{ID: id, CreatedAt: m})
				}
			}
			return out, twitch.RateInfo{}, nil
		},
		Lock: func(context.Context) (string, string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.locks++
			return "done", "Locked."
		}})
	if err := r.g.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(r.g.Close)
}

func (r *rig) chat(platform, id string, chatter map[string]any) {
	r.t.Helper()
	raw, _ := json.Marshal(map[string]any{"text": "hi", "chatter": chatter})
	env := events.Envelope{EventID: events.NewID(), Type: "chat.message.received", Source: platform, SourceEventID: events.NewID(),
		OccurredAt: time.Now(), Actor: &events.Actor{Platform: platform, ID: id, Login: id, DisplayName: id}, Payload: raw,
		DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	if _, err := r.inbox.Accept(context.Background(), env); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) alerts(n int) []events.Record {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs, _ := r.inbox.After(context.Background(), 0, 1000, events.Filter{TypeSuspected})
		if len(recs) >= n || time.Now().After(deadline) {
			if len(recs) != n {
				r.t.Fatalf("%d alerts, want %d", len(recs), n)
			}
			return recs
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestChecks(t *testing.T) {
	if Check(Defaults()) != nil {
		t.Fatal("defaults")
	}
	for _, bad := range []Settings{{Mode: "panic"}, {Mode: ModeAlert, FirstTime: 1, NewAccounts: 5, Days: 7, Window: 30},
		{Mode: ModeAlert, FirstTime: 5, NewAccounts: 5, Days: 0, Window: 30}, {Mode: ModeAlert, FirstTime: 5, NewAccounts: 5, Days: 7, Window: 5},
		{Mode: ModeAlert, FirstTime: 5, NewAccounts: 5, Days: 7, Window: 30, AfterRaid: 31}} {
		if err := Check(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

// First-time chatters: below the count nothing; at it, one alert, with
// the accounts; known viewers, mods, VIPs, subs and you never count; the
// cooldown holds back a second.
func TestFirstTimeBurst(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	st := Defaults()
	st.FirstTime = 4
	if _, err := r.g.SetSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	r.known["twitch:regular"] = time.Now().Add(-48 * time.Hour)
	r.chat("twitch", "regular", map[string]any{})
	r.chat("twitch", "mod", map[string]any{"is_moderator": true})
	r.chat("twitch", "sub", map[string]any{"is_subscriber": true})
	r.chat("youtube", "member", map[string]any{"is_subscriber": true})
	r.chat("twitch", "me", map[string]any{"is_broadcaster": true})
	for i := range 3 {
		r.chat("twitch", fmt.Sprintf("new%d", i), map[string]any{})
	}
	time.Sleep(80 * time.Millisecond)
	r.alerts(0)
	if r.g.Alert() != nil {
		t.Fatal("an alert below the count")
	}
	r.chat("youtube", "ytnew", map[string]any{}) // YouTube counts too
	recs := r.alerts(1)
	var p struct {
		Reason   string    `json:"reason"`
		Count    int       `json:"count"`
		Accounts []Account `json:"accounts"`
		Locked   bool      `json:"locked"`
	}
	_ = json.Unmarshal(recs[0].Payload, &p)
	if p.Reason != ReasonFirstTime || p.Count != 4 || len(p.Accounts) != 4 || p.Locked || recs[0].Source != Source {
		t.Fatalf("%+v", p)
	}
	if a := r.g.Alert(); a == nil || a.Count != 4 {
		t.Fatalf("%+v", a)
	}
	// Within the cooldown: no second alert; the same account never counts twice.
	for i := range 6 {
		r.chat("twitch", fmt.Sprintf("more%d", i), map[string]any{})
	}
	time.Sleep(80 * time.Millisecond)
	r.alerts(1)
	r.g.Dismiss()
	if r.g.Alert() != nil {
		t.Fatal("not dismissed")
	}
	if r.locks != 0 {
		t.Fatal("locked when only alerting")
	}
}

// Brand-new Twitch accounts, a closer watch after a raid, and locking when
// chosen; settings survive a restart; a test alert locks nothing.
func TestNewAccountsRaidAndLock(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	st := Defaults()
	st.Mode, st.FirstTime, st.NewAccounts, st.Days = ModeLock, 50, 4, 7
	if _, err := r.g.SetSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	r.g.Close()
	r.start()
	if got := r.g.Settings(); got.Mode != ModeLock || got.NewAccounts != 4 {
		t.Fatalf("%+v", got)
	}
	r.mu.Lock()
	r.made["old"] = time.Now().Add(-400 * 24 * time.Hour)
	r.made["b1"], r.made["b2"] = time.Now().Add(-time.Hour), time.Now().Add(-48*time.Hour)
	r.mu.Unlock()
	// A raid arrives: half the count (2) is enough for a few minutes.
	raw, _ := json.Marshal(map[string]any{"viewers": 40})
	env := events.Envelope{EventID: events.NewID(), Type: "audience.raid.received", Source: "twitch", SourceEventID: "raid1",
		OccurredAt: time.Now(), Actor: &events.Actor{Platform: "twitch", ID: "r", Login: "raider", DisplayName: "Raider"}, Payload: raw,
		DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	if _, err := r.inbox.Accept(ctx, env); err != nil {
		t.Fatal(err)
	}
	r.chat("twitch", "old", map[string]any{})
	r.chat("twitch", "b1", map[string]any{})
	r.chat("twitch", "b2", map[string]any{})
	recs := r.alerts(1)
	var p struct {
		Reason    string `json:"reason"`
		Count     int    `json:"count"`
		AfterRaid string `json:"after_raid"`
		Locked    bool   `json:"locked"`
	}
	_ = json.Unmarshal(recs[0].Payload, &p)
	if p.Reason != ReasonNewAccounts || p.Count != 2 || p.AfterRaid != "Raider" || !p.Locked || r.locks != 1 {
		t.Fatalf("%+v locks %d", p, r.locks)
	}
	// Ages are asked once each.
	r.chat("twitch", "b1", map[string]any{})
	time.Sleep(30 * time.Millisecond)
	if r.lookup != 3 {
		t.Fatalf("%d lookups", r.lookup)
	}
	a := r.g.Test(ctx)
	if !a.Test || r.locks != 1 {
		t.Fatal("a test locked")
	}
	if recs := r.alerts(2); recs[1].DeliveryQuality != events.Synthetic {
		t.Fatal("the test alert isn't a test event")
	}
	// Off: nothing counts.
	st.Mode = ModeOff
	_, _ = r.g.SetSettings(ctx, st)
	for i := range 10 {
		r.chat("twitch", fmt.Sprintf("off%d", i), map[string]any{})
	}
	time.Sleep(50 * time.Millisecond)
	r.alerts(2)
}
