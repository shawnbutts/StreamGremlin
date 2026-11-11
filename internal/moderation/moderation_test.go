package moderation

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

type fakeTwitch struct {
	calls   atomic.Int32
	outcome twitch.ModOutcome
	gate    chan struct{} // when set, each call waits for it
	last    twitch.Mod
	mu      sync.Mutex
}

func (f *fakeTwitch) Moderate(_ context.Context, m twitch.Mod) (twitch.ModOutcome, error) {
	if err := twitch.CheckMod(m); err != nil {
		return twitch.ModOutcome{}, err
	}
	f.calls.Add(1)
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	f.last = m
	f.mu.Unlock()
	return f.outcome, nil
}

func (f *fakeTwitch) ChangeChatSettings(_ context.Context, c twitch.ChatSettingsChange) (twitch.ChatSettings, twitch.ModOutcome, error) {
	f.calls.Add(1)
	cs := twitch.ChatSettings{}
	if c.Slow != nil {
		cs.Slow = *c.Slow
		cs.SlowSeconds = c.SlowSeconds
	}
	return cs, f.outcome, nil
}

func newService(t *testing.T) (*Service, *fakeTwitch, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	f := &fakeTwitch{outcome: twitch.ModOutcome{State: Done}}
	return New(db, f, nil), f, db
}

func timeout(id string) Request {
	return Request{RequestID: id, Action: twitch.ModTimeout, Platform: "twitch", UserID: "42", Login: "ann", Seconds: 600, Reason: "calm", From: FromLive}
}

// A request is sent once and recorded; the same request id returns the
// record and sends nothing, however late; a different request under the
// same id is refused; refused and unknown answers are recorded as such.
func TestDoOnceAndRecord(t *testing.T) {
	s, f, _ := newService(t)
	ctx := context.Background()
	rec, dup, err := s.Do(ctx, timeout("r1"))
	if err != nil || dup || rec.State != Done || rec.AnsweredAt == nil || f.calls.Load() != 1 {
		t.Fatal(rec, dup, err)
	}
	again, dup, err := s.Do(ctx, timeout("r1"))
	if err != nil || !dup || again.ID != rec.ID || f.calls.Load() != 1 {
		t.Fatal("repeat:", again, dup, err)
	}
	other := timeout("r1")
	other.Seconds = 60
	if _, _, err := s.Do(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatal("reused id:", err)
	}

	f.outcome = twitch.ModOutcome{State: Refused, Detail: "Twitch refused: already banned (400)"}
	if rec, _, _ := s.Do(ctx, timeout("r2")); rec.State != Refused || rec.Answer == "" {
		t.Fatal(rec)
	}
	f.outcome = twitch.ModOutcome{State: Unknown, Detail: "Twitch didn't answer"}
	if rec, _, _ := s.Do(ctx, timeout("r3")); rec.State != Unknown {
		t.Fatal(rec)
	}
	// A repeat of an unknown one isn't sent again either.
	if rec, dup, _ := s.Do(ctx, timeout("r3")); !dup || rec.State != Unknown || f.calls.Load() != 3 {
		t.Fatal("unknown repeated:", rec, dup)
	}

	list, err := s.List(ctx, "twitch", "42", 10)
	if err != nil || len(list) != 3 || list[0].RequestID != "r3" {
		t.Fatalf("%+v %v", list, err)
	}
	if list, _ := s.List(ctx, "twitch", "99", 10); len(list) != 0 {
		t.Fatal(list)
	}
}

// The same request twice at once is sent once.
func TestConcurrentRepeatSentOnce(t *testing.T) {
	s, f, _ := newService(t)
	f.gate = make(chan struct{})
	var wg sync.WaitGroup
	dups := atomic.Int32{}
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, dup, err := s.Do(context.Background(), timeout("same"))
			if err != nil {
				t.Error(err)
			}
			if dup {
				dups.Add(1)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(f.gate)
	wg.Wait()
	if f.calls.Load() != 1 || dups.Load() != 4 {
		t.Fatal(f.calls.Load(), dups.Load())
	}
}

// A request a crash left pending is unknown after Recover.
func TestRecoverPending(t *testing.T) {
	s, _, db := newService(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO moderation (id, request_id, action, platform, user_id, from_where, state, created_at)
		VALUES ('mod_x', 'rx', 'ban', 'twitch', '42', 'live', 'pending', ?)`, storage.Timestamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	rec, dup, err := s.Do(ctx, Request{RequestID: "rx", Action: "ban", Platform: "twitch", UserID: "42", From: FromLive})
	if err != nil || !dup || rec.State != Unknown || rec.Answer == "" {
		t.Fatal(rec, dup, err)
	}
}

// Chat modes are recorded with the change and Twitch's settings after.
func TestChatModes(t *testing.T) {
	s, _, _ := newService(t)
	on, wait := true, 10
	rec, _, err := s.Do(context.Background(), Request{RequestID: "c1", Action: ChatModes, Platform: "twitch", From: FromCLI,
		Chat: &twitch.ChatSettingsChange{Slow: &on, SlowSeconds: &wait}})
	if err != nil || rec.Settings == nil || !rec.Settings.Slow || rec.Chat == nil {
		t.Fatal(rec, err)
	}
	list, _ := s.List(context.Background(), "", "", 5)
	if len(list) != 1 || list[0].Chat == nil || *list[0].Chat.SlowSeconds != 10 {
		t.Fatalf("%+v", list)
	}
}

// Bad requests are refused before anything is recorded or sent.
func TestInvalid(t *testing.T) {
	s, f, _ := newService(t)
	on := true
	for _, r := range []Request{
		{RequestID: "a", Action: "ban", Platform: "youtube", UserID: "42", From: FromLive},
		{RequestID: "a b", Action: "ban", Platform: "twitch", UserID: "42", From: FromLive},
		{RequestID: "", Action: "ban", Platform: "twitch", UserID: "42", From: FromLive},
		{RequestID: "a", Action: "ban", Platform: "twitch", UserID: "42", From: "stream_deck"},
		{RequestID: "a", Action: "kick", Platform: "twitch", UserID: "42", From: FromLive},
		{RequestID: "a", Action: "timeout", Platform: "twitch", UserID: "42", From: FromLive},
		{RequestID: "a", Action: ChatModes, Platform: "twitch", From: FromLive},
		{RequestID: "a", Action: ChatModes, Platform: "twitch", UserID: "42", From: FromLive, Chat: &twitch.ChatSettingsChange{EmoteOnly: &on}},
		{RequestID: "a", Action: "ban", Platform: "twitch", UserID: "42", From: FromLive, Chat: &twitch.ChatSettingsChange{EmoteOnly: &on}},
	} {
		if _, _, err := s.Do(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", r, err)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("sent")
	}
	if list, _ := s.List(context.Background(), "", "", 10); len(list) != 0 {
		t.Fatal("recorded:", list)
	}
}
