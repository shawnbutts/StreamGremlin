package filters

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// fakeTwitch answers moderating requests and remembers them.
type fakeTwitch struct {
	mu   sync.Mutex
	sent []twitch.Mod
}

func (f *fakeTwitch) Moderate(_ context.Context, m twitch.Mod) (twitch.ModOutcome, error) {
	if err := twitch.CheckMod(m); err != nil {
		return twitch.ModOutcome{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return twitch.ModOutcome{State: twitch.ModDone}, nil
}

func (f *fakeTwitch) ChangeChatSettings(context.Context, twitch.ChatSettingsChange) (twitch.ChatSettings, twitch.ModOutcome, error) {
	return twitch.ChatSettings{}, twitch.ModOutcome{}, errors.New("not used")
}

func (f *fakeTwitch) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

type rig struct {
	t     *testing.T
	inbox *events.Inbox
	tw    *fakeTwitch
	mods  *moderation.Service
	svc   *Service
	n     int
}

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
	r := &rig{t: t, inbox: events.NewInbox(db, nil), tw: &fakeTwitch{}}
	r.mods = moderation.New(db, r.tw, nil)
	r.svc = New(Options{Inbox: r.inbox, Settings: settings.NewStore(db), Moderation: r.mods, BotID: func() string { return "999" }})
	return r
}

// say stores a chat message and returns its record.
func (r *rig) say(userID, role, text string, q events.Quality, extra map[string]any) events.Record {
	r.t.Helper()
	r.n++
	id := "msg-" + strconv.Itoa(r.n)
	p := map[string]any{"message_id": id, "text": text, "emotes": []string{},
		"chatter": map[string]any{"id": userID, "login": "u" + userID, "role": role, "is_moderator": role == "moderator"}}
	for k, v := range extra {
		p[k] = v
	}
	raw, _ := json.Marshal(p)
	env := events.Envelope{Type: "chat.message.received", Source: "twitch", SourceEventID: id, OccurredAt: time.Now(),
		ChannelID: "100", Actor: &events.Actor{Platform: "twitch", ID: userID, Login: "u" + userID}, Payload: raw, DeliveryQuality: q}
	res, err := r.inbox.Accept(context.Background(), env)
	if err != nil {
		r.t.Fatal(err)
	}
	return res.Record
}

func (r *rig) caught() []map[string]any {
	r.t.Helper()
	recs, err := r.svc.Recent(context.Background(), 50)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []map[string]any
	for _, rec := range recs {
		var p map[string]any
		_ = json.Unmarshal(rec.Payload, &p)
		p["_quality"] = string(rec.DeliveryQuality)
		out = append(out, p)
	}
	return out
}

func (r *rig) set(f func(*Settings)) {
	r.t.Helper()
	s := Defaults()
	s.On = true
	f(&s)
	if _, err := r.svc.SetSettings(context.Background(), s); err != nil {
		r.t.Fatal(err)
	}
}

// A caught message is deleted once, as the streamer, recorded from
// "filter", and makes one caught event; handling it again sends nothing.
func TestCatchDeletesOnceAndRecords(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.set(func(s *Settings) { s.Links.On = true })
	rec := r.say("42", "viewer", "free followers at bit.ly/x", events.Confirmed, nil)
	r.svc.handle(ctx, rec)
	r.svc.handle(ctx, rec) // delivered twice
	if r.tw.count() != 1 {
		t.Fatalf("sent %d times", r.tw.count())
	}
	m := r.tw.sent[0]
	if m.Action != twitch.ModDelete || m.MessageID != rec.SourceEventID || m.Reason != "" {
		t.Fatalf("%+v", m)
	}
	list, err := r.mods.List(ctx, "twitch", "42", 10)
	if err != nil || len(list) != 1 || list[0].From != moderation.FromFilter || list[0].State != moderation.Done ||
		list[0].RequestID != "filter-"+rec.SourceEventID {
		t.Fatalf("%+v %v", list, err)
	}
	c := r.caught()
	if len(c) != 1 || c[0]["rule"] != RuleLinks || c[0]["action"] != ActionDelete || c[0]["state"] != "done" ||
		c[0]["text"] != "free followers at bit.ly/x" || c[0]["_quality"] != string(events.Confirmed) {
		t.Fatalf("%+v", c)
	}
	if ch, _ := c[0]["chatter"].(map[string]any); ch["login"] != "u42" {
		t.Fatalf("chatter %v", c[0]["chatter"])
	}
}

func TestTimeoutAndWarnCarryTheReason(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.set(func(s *Settings) {
		s.Caps.On, s.Caps.Action, s.Caps.Seconds = true, ActionTimeout, 300
		s.Words.On, s.Words.Action, s.Words.List = true, ActionWarn, []string{"darn"}
	})
	r.svc.handle(ctx, r.say("42", "viewer", "STOP SHOUTING AT ME PLEASE", events.Confirmed, nil))
	r.svc.handle(ctx, r.say("43", "viewer", "well darn", events.Confirmed, nil))
	if r.tw.count() != 2 {
		t.Fatal(r.tw.sent)
	}
	if m := r.tw.sent[0]; m.Action != twitch.ModTimeout || m.Seconds != 300 || m.UserID != "42" || m.Reason != Reason(RuleCaps) {
		t.Fatalf("%+v", m)
	}
	if m := r.tw.sent[1]; m.Action != twitch.ModWarn || m.UserID != "43" || m.Reason != Reason(RuleWords) {
		t.Fatalf("%+v", m)
	}
}

func TestLeftAlone(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.set(func(s *Settings) { s.Links.On = true })
	r.svc.handle(ctx, r.say("43", "moderator", "bit.ly/x", events.Confirmed, nil))
	r.svc.handle(ctx, r.say("999", "viewer", "bit.ly/x", events.Confirmed, nil))                                         // the bot
	r.svc.handle(ctx, r.say("100", "viewer", "bit.ly/x", events.Confirmed, nil))                                         // you
	r.svc.handle(ctx, r.say("44", "viewer", "bit.ly/x", events.Confirmed, map[string]any{"source_broadcaster_id": "7"})) // shared chat
	r.svc.handle(ctx, r.say("45", "viewer", "a fine message", events.Confirmed, nil))
	if r.tw.count() != 0 || len(r.caught()) != 0 {
		t.Fatalf("%v %v", r.tw.sent, r.caught())
	}
	// All filters off.
	r.set(func(s *Settings) { s.On, s.Links.On = false, true })
	r.svc.handle(ctx, r.say("46", "viewer", "bit.ly/x", events.Confirmed, nil))
	if r.tw.count() != 0 {
		t.Fatal("acted while off")
	}
}

// A test message is judged, but nothing reaches Twitch: the caught event
// is a test event.
func TestTestMessagesNeverReachTwitch(t *testing.T) {
	r := newRig(t)
	r.set(func(s *Settings) { s.Links.On = true })
	r.svc.handle(context.Background(), r.say("42", "viewer", "bit.ly/x", events.Synthetic, nil))
	if r.tw.count() != 0 {
		t.Fatal("a test message reached Twitch")
	}
	c := r.caught()
	if len(c) != 1 || c[0]["state"] != "test" || c[0]["_quality"] != string(events.Synthetic) {
		t.Fatalf("%+v", c)
	}
}

// Started, the service judges new messages as they arrive, never older
// ones.
func TestFollowsChatFromStart(t *testing.T) {
	r := newRig(t)
	r.set(func(s *Settings) { s.Links.On = true })
	r.say("41", "viewer", "old bit.ly/x", events.Confirmed, nil)
	if err := r.svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.svc.Close)
	r.say("42", "viewer", "new bit.ly/x", events.Confirmed, nil)
	deadline := time.Now().Add(5 * time.Second)
	for r.tw.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if r.tw.count() != 1 || r.tw.sent[0].MessageID == "" {
		t.Fatalf("%+v", r.tw.sent)
	}
	if c := r.caught(); len(c) != 1 || c[0]["text"] != "new bit.ly/x" {
		t.Fatalf("%+v", c)
	}
}

func TestSettingsAndTry(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	got, err := r.svc.Settings(ctx)
	if err != nil || got.On || got.Caps.Percent != 70 {
		t.Fatalf("%+v %v", got, err)
	}
	bad := Defaults()
	bad.Links.Action = "ban"
	if _, err := r.svc.SetSettings(ctx, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	s := Defaults()
	s.Words.List = []string{"  Darn  "}
	saved, err := r.svc.SetSettings(ctx, s)
	if err != nil || saved.Words.List[0] != "darn" {
		t.Fatalf("%+v %v", saved, err)
	}
	if again, _ := r.svc.Settings(ctx); again.Words.List[0] != "darn" {
		t.Fatalf("%+v", again)
	}
	// Try judges as if the filters and the rules were on, but only rules
	// that are on count.
	if rule, err := r.svc.Try(ctx, nil, Message{Platform: "twitch", Role: "viewer", Text: "darn it"}); err != nil || rule != "" {
		t.Fatalf("%q %v", rule, err)
	}
	s.Words.On = true
	if rule, _ := r.svc.Try(ctx, &s, Message{Platform: "twitch", Role: "viewer", Text: "darn it"}); rule != RuleWords {
		t.Fatalf("%q", rule)
	}
	if rule, _ := r.svc.Try(ctx, &s, Message{Platform: "twitch", Role: "moderator", Text: "darn it"}); rule != "" {
		t.Fatalf("a moderator: %q", rule)
	}
}

func TestPermitLetsALinkThrough(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.set(func(s *Settings) { s.Links.On = true })
	r.svc.Permit("twitch", "@U42", time.Minute)
	r.svc.handle(ctx, r.say("42", "viewer", "my art: example.com/art", events.Confirmed, nil))
	r.svc.handle(ctx, r.say("43", "viewer", "example.com/spam", events.Confirmed, nil))
	if r.tw.count() != 1 || r.tw.sent[0].MessageID == "" {
		t.Fatalf("%+v", r.tw.sent)
	}
	if c := r.caught(); len(c) != 1 || c[0]["text"] != "example.com/spam" {
		t.Fatalf("%+v", c)
	}
}
