package giveaways

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

type fakeChat struct {
	mu   sync.Mutex
	said []string
	err  error
}

func (f *fakeChat) SendChat(_ context.Context, req twitch.SendRequest) (twitch.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return twitch.SendResult{}, f.err
	}
	f.said = append(f.said, req.Text)
	return twitch.SendResult{MessageID: "m"}, nil
}

func (f *fakeChat) all() string { f.mu.Lock(); defer f.mu.Unlock(); return strings.Join(f.said, "\n") }

type rig struct {
	t     *testing.T
	db    *storage.DB
	inbox *events.Inbox
	chat  *fakeChat
	svc   *Service
	held  []commands.Claim
	mu    sync.Mutex
	shift time.Duration // the service's clock runs this far ahead
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, inbox: events.NewInbox(db, nil), chat: &fakeChat{}}
	r.start()
	return r
}

func (r *rig) now() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return time.Now().Add(r.shift) }

func (r *rig) wait(d time.Duration) { r.mu.Lock(); r.shift += d; r.mu.Unlock() }

func (r *rig) start() {
	r.t.Helper()
	if r.svc != nil {
		r.svc.Close()
	}
	r.svc = New(Options{DB: r.db, Inbox: r.inbox, Twitch: r.chat, Now: r.now, AckEvery: 50 * time.Millisecond,
		Commands: func(cs []commands.Claim) error {
			if c := commands.Conflicts(cs, r.held); len(c) > 0 {
				return &commands.TakenError{Conflicts: c}
			}
			return nil
		}})
	if err := r.svc.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	svc := r.svc
	r.t.Cleanup(svc.Close)
}

// say stores a chat command from a viewer, as Twitch chat (or test chat).
func (r *rig) say(login, command string, test bool, roles ...string) {
	r.t.Helper()
	q := events.Confirmed
	if test {
		q = events.Synthetic
	}
	c := map[string]any{"id": "id-" + login, "login": login, "display_name": strings.ToUpper(login[:1]) + login[1:], "is_broadcaster": false,
		"is_subscriber": false}
	for _, role := range roles {
		c["is_"+role] = true
	}
	p, _ := json.Marshal(map[string]any{"command": command, "chatter": c, "text": "!" + command})
	_, err := r.inbox.Accept(context.Background(), events.Envelope{Type: "chat.command.received", Source: "twitch",
		Actor: &events.Actor{Platform: "twitch", ID: "id-" + login, Login: login}, DeliveryQuality: q, Payload: p, ReceivedAt: r.now()})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) eventually(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	r.t.Fatal("timed out waiting for", what)
}

func (r *rig) detail(id string) Detail {
	r.t.Helper()
	d, err := r.svc.Get(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) entries(id string, n int) {
	r.t.Helper()
	r.eventually(fmt.Sprintf("%d entries", n), func() bool { return len(r.detail(id).Entries) == n })
}

func (r *rig) eventTypes() []string {
	recs, _ := r.inbox.Latest(context.Background(), 100, events.Filter{"giveaways.*"})
	var out []string
	for _, x := range recs {
		out = append(out, x.Type)
	}
	return out
}

func TestAGiveawayFromOpenToEnd(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.say("early", "join", false) // before it opened: not an entry
	g, err := r.svc.Open(ctx, Settings{Title: "a Steam key", Winners: 2}, "the app")
	if err != nil || g.Command != "join" || g.State != Open {
		t.Fatal(g, err)
	}
	r.eventually("the opening", func() bool { return strings.Contains(r.chat.all(), "Type !join in chat to enter") })
	if !strings.Contains(r.chat.all(), "every entry has the same chance; 2 winners will be drawn") {
		t.Fatal(r.chat.all())
	}
	for _, v := range []string{"ann", "bob", "cat", "dan"} {
		r.say(v, "join", false)
	}
	r.say("ann", "join", false)   // twice: one entry
	r.say("tester", "join", true) // test chat: not in a live giveaway
	r.say("me", "join", false, "broadcaster")
	r.say("eve", "JOIN", false) // any case
	r.entries(g.ID, 5)
	r.eventually("the acknowledgements", func() bool { return strings.Contains(r.chat.all(), "you're in a Steam key! (5 entries so far)") })
	d := r.detail(g.ID)
	if d.Entries[0].Login != "ann" || d.Entries[4].Login != "eve" {
		t.Fatalf("%+v", d.Entries)
	}
	if _, err := r.svc.Open(ctx, Settings{Title: "another"}, "app"); !errors.Is(err, ErrState) {
		t.Fatal("a second giveaway:", err)
	}
	// Drawing closes entries and fixes the list.
	draws, err := r.svc.Draw(ctx, g.ID, 0)
	if err != nil || len(draws) != 2 || draws[0].Candidates != 5 || draws[1].Candidates != 4 || draws[0].EntrySeq == draws[1].EntrySeq {
		t.Fatal(draws, err)
	}
	r.say("late", "join", false)
	d = r.detail(g.ID)
	if d.State != Closed || d.EntryCount != 5 || d.Fingerprint != Fingerprint(d.Entries) || len(Verify(d)) != 0 {
		t.Fatalf("%+v %v", d.Giveaway, Verify(d))
	}
	// Drawing again draws no one extra.
	if _, err := r.svc.Draw(ctx, g.ID, 0); !errors.Is(err, ErrState) {
		t.Fatal("a third winner:", err)
	}
	// Set one aside: the replacement is never someone drawn already.
	again, err := r.svc.SetAside(ctx, g.ID, draws[0].ID, "the app")
	if err != nil || len(again) != 1 || again[0].Candidates != 3 || again[0].EntrySeq == draws[0].EntrySeq || again[0].EntrySeq == draws[1].EntrySeq {
		t.Fatal(again, err)
	}
	if _, err := r.svc.SetAside(ctx, g.ID, draws[0].ID, "the app"); !errors.Is(err, ErrState) {
		t.Fatal("set aside twice:", err)
	}
	d = r.detail(g.ID)
	if problems := Verify(d); len(problems) != 0 {
		t.Fatal(problems)
	}
	end, err := r.svc.End(ctx, g.ID)
	if err != nil || end.State != Ended || r.svc.Current() != nil {
		t.Fatal(end, err)
	}
	r.eventually("the congratulations", func() bool {
		return strings.Contains(r.chat.all(), "a Steam key is over. Congratulations to @"+draws[1].Winner.Login)
	})
	if _, err := r.svc.Draw(ctx, g.ID, 1); !errors.Is(err, ErrState) {
		t.Fatal("draw after end:", err)
	}
	r.eventually("the events", func() bool {
		return strings.Join(r.eventTypes(), ",") == "giveaways.giveaway.opened,giveaways.giveaway.closed,giveaways.winner.drawn,"+
			"giveaways.winner.drawn,giveaways.winner.drawn,giveaways.giveaway.ended"
	})
}

func TestTheRecordCantBeChanged(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, _ := r.svc.Open(ctx, Settings{Title: "t"}, "app")
	r.say("ann", "join", false)
	r.say("bob", "join", false)
	r.entries(g.ID, 2)
	draws, _ := r.svc.Draw(ctx, g.ID, 1)
	for _, q := range []string{
		"UPDATE giveaway_entries SET user_id = 'x' WHERE giveaway_id = '" + g.ID + "'",
		"DELETE FROM giveaway_entries",
		"UPDATE giveaways SET fingerprint = 'x'",
		"UPDATE giveaway_draws SET pick_index = 0, candidates = 9",
		"UPDATE giveaway_draws SET outcome = 'pending_claim'",
		"DELETE FROM giveaway_draws",
		"DELETE FROM giveaways",
		"INSERT INTO giveaway_entries (giveaway_id, seq, platform, user_id, login, display_name, event_id, entered_at) " +
			"VALUES ('" + g.ID + "', 9, 'twitch', 'z', 'z', 'z', 'e', '2026-01-01T00:00:00.000Z')",
	} {
		if _, err := r.db.ExecContext(ctx, q); err == nil {
			t.Errorf("allowed: %s", q)
		}
	}
	// A record changed by hand is caught by the check.
	d := r.detail(g.ID)
	d.Entries[0].UserID = "someone-else"
	if len(Verify(d)) == 0 {
		t.Fatal("a changed list passed")
	}
	d = r.detail(g.ID)
	d.Draws[0].PickIndex = 1 - draws[0].PickIndex
	if len(Verify(d)) == 0 {
		t.Fatal("a changed pick passed")
	}
}

func TestClaimsAndForfeits(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, err := r.svc.Open(ctx, Settings{Title: "a hoodie", ClaimSeconds: 30, Winners: 1}, "app")
	if err != nil || g.ClaimCommand != "claim" {
		t.Fatal(g, err)
	}
	r.say("ann", "join", false)
	r.say("bob", "join", false)
	r.entries(g.ID, 2)
	draws, _ := r.svc.Draw(ctx, g.ID, 1)
	first := draws[0]
	if first.Outcome != PendingClaim || first.ClaimUntil == nil {
		t.Fatal(first)
	}
	other := "ann"
	if first.Winner.Login == "ann" {
		other = "bob"
	}
	r.say(other, "claim", false) // not theirs to claim
	if _, err := r.svc.End(ctx, g.ID); !errors.Is(err, ErrState) {
		t.Fatal("end during a claim:", err)
	}
	r.wait(33 * time.Second)
	r.eventually("the forfeit", func() bool { return r.detail(g.ID).Draws[0].Outcome == Forfeited })
	r.say(first.Winner.Login, "claim", false) // too late
	next, err := r.svc.Draw(ctx, g.ID, 0)
	if err != nil || len(next) != 1 || next[0].Winner.Login != other {
		t.Fatal(next, err)
	}
	r.say(other, "claim", false)
	r.eventually("the claim", func() bool { return r.detail(g.ID).Draws[1].Outcome == Claimed })
	r.eventually("chat", func() bool {
		all := r.chat.all()
		return strings.Contains(all, "didn't claim a hoodie in time") && strings.Contains(all, "@"+other+" claimed a hoodie!")
	})
	if r.detail(g.ID).Draws[0].Outcome != Forfeited {
		t.Fatal("a late claim counted")
	}
	if _, err := r.svc.End(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribersOnlyAndTimedEntries(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, _ := r.svc.Open(ctx, Settings{Title: "subs night", SubscribersOnly: true, Minutes: 5}, "app")
	r.say("sub", "join", false, "subscriber")
	r.say("viewer", "join", false)
	r.say("viewer", "join", false)
	r.entries(g.ID, 1)
	r.eventually("the refusal, once", func() bool { return strings.Count(r.chat.all(), "@viewer: subs night is for subscribers only.") == 1 })
	time.Sleep(150 * time.Millisecond)
	if strings.Count(r.chat.all(), "subscribers only.") != 1 || len(r.detail(g.ID).Refusals) != 1 {
		t.Fatal(r.chat.all())
	}
	r.wait(5 * time.Minute)
	r.eventually("entries closed on time", func() bool { return r.detail(g.ID).State == Closed })
	r.say("late", "join", false, "subscriber")
	r.eventually("the close in chat", func() bool { return strings.Contains(r.chat.all(), "Entries for subs night are closed: 1 entry.") })
	time.Sleep(100 * time.Millisecond)
	if r.detail(g.ID).EntryCount != 1 {
		t.Fatal(r.detail(g.ID).Entries)
	}
}

func TestPracticeTakesTestChatAndSendsNothing(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.held = []commands.Claim{{Command: "join", Owner: "wf", Name: "Join"}}
	if _, err := r.svc.Open(ctx, Settings{Title: "live"}, "app"); err == nil || !strings.Contains(err.Error(), "!join is already answered by “Join”") {
		t.Fatal("a held command:", err)
	}
	g, err := r.svc.Open(ctx, Settings{Title: "practice run", Practice: true}, "app")
	if err != nil {
		t.Fatal(err)
	}
	r.say("tester", "join", true)
	r.say("real", "join", false)
	r.entries(g.ID, 1)
	if _, err := r.svc.Draw(ctx, g.ID, 1); err != nil {
		t.Fatal(err)
	}
	d := r.detail(g.ID)
	if d.Entries[0].Login != "tester" || len(d.Messages) < 3 {
		t.Fatalf("%+v", d)
	}
	for _, m := range d.Messages {
		if m.State != "practice" {
			t.Fatal(m)
		}
	}
	if r.chat.all() != "" {
		t.Fatal("practice sent:", r.chat.all())
	}
	recs, _ := r.inbox.Latest(ctx, 10, events.Filter{"giveaways.*"})
	for _, x := range recs {
		if x.DeliveryQuality != events.Synthetic {
			t.Fatal("a practice event as real:", x.Type)
		}
	}
	if _, err := r.svc.Cancel(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
}

func TestARestartKeepsTheGiveawayAndNeverResends(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, _ := r.svc.Open(ctx, Settings{Title: "t"}, "app")
	r.say("ann", "join", false)
	r.entries(g.ID, 1)
	r.eventually("sent", func() bool { return strings.Contains(r.chat.all(), "you're in") })
	// A message mid-send when StreamGremlin stops.
	if _, err := r.db.ExecContext(ctx, `INSERT INTO giveaway_messages (giveaway_id, text, state, created_at)
		VALUES (?, 'half sent', 'sending', ?)`, g.ID, ts(r.now())); err != nil {
		t.Fatal(err)
	}
	r.start()
	if cur := r.svc.Current(); cur == nil || cur.ID != g.ID {
		t.Fatal("lost the giveaway")
	}
	// The same command read again enters no one twice.
	r.say("bob", "join", false)
	r.entries(g.ID, 2)
	d := r.detail(g.ID)
	var half Message
	for _, m := range d.Messages {
		if m.Text == "half sent" {
			half = m
		}
	}
	if half.State != "unknown" || strings.Contains(r.chat.all(), "half sent") {
		t.Fatal(half)
	}
	if n := strings.Count(r.chat.all(), "Giveaway: t!"); n != 1 {
		t.Fatal("opening sent", n, "times")
	}
}

func TestChatFailuresAreRecordedNotRetried(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.chat.err = twitch.ErrNotSignedIn
	g, _ := r.svc.Open(ctx, Settings{Title: "t"}, "app")
	r.eventually("the failure", func() bool {
		m := r.detail(g.ID).Messages
		return len(m) == 1 && m[0].State == "failed" && strings.Contains(m[0].Detail, "sign in")
	})
	r.chat.mu.Lock()
	r.chat.err = errors.New("connection reset")
	r.chat.mu.Unlock()
	if _, err := r.svc.Cancel(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	r.eventually("unknown", func() bool { m := r.detail(g.ID).Messages; return len(m) == 2 && m[1].State == "unknown" })
}

func TestSettingsAreChecked(t *testing.T) {
	r := newRig(t)
	for _, st := range []Settings{
		{Title: ""},
		{Title: strings.Repeat("x", 101)},
		{Title: "t", Command: "two words"},
		{Title: "t", Winners: 21},
		{Title: "t", ClaimSeconds: 10},
		{Title: "t", ClaimSeconds: 30, ClaimCommand: "join"},
		{Title: "t", Minutes: 1441},
	} {
		if _, err := r.svc.Open(context.Background(), st, "app"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", st, err)
		}
	}
	g, err := r.svc.Open(context.Background(), Settings{Title: "t", Command: "!Enter"}, "app")
	if err != nil || g.Command != "enter" {
		t.Fatal(g, err)
	}
	if cs := r.svc.Claims(); len(cs) != 1 || cs[0].Command != "enter" {
		t.Fatal(cs)
	}
}

func TestDrawsAreUniform(t *testing.T) {
	counts := make([]int, 4)
	for i := 0; i < 8000; i++ {
		n, err := pick(4)
		if err != nil {
			t.Fatal(err)
		}
		counts[n]++
	}
	for i, c := range counts {
		if c < 1700 || c > 2300 { // 2000 expected; far outside chance
			t.Fatalf("entry %d picked %d times of 8000: %v", i, c, counts)
		}
	}
}

func TestBatchesFitChat(t *testing.T) {
	var names []string
	for i := 0; i < 80; i++ {
		names = append(names, fmt.Sprintf("@viewer_number_%02d", i))
	}
	out := batches(names, ": you're in something! (80 entries so far)")
	if len(out) < 2 {
		t.Fatal(out)
	}
	seen := 0
	for _, b := range out {
		if len([]rune(b)) > twitch.MaxChatRunes {
			t.Fatal(len(b))
		}
		seen += strings.Count(b, "@viewer")
	}
	if seen != 80 {
		t.Fatal(seen)
	}
}

// The app and the CLI check the same fingerprint (cmd/desktop/frontend/src/lib/giveaways.test.ts).
func TestTheFingerprintFormat(t *testing.T) {
	got := Fingerprint([]Entry{{Seq: 1, Platform: "twitch", UserID: "u1"}, {Seq: 2, Platform: "twitch", UserID: "u2"}})
	if got != "104215ce233df7bab9166cd87d6aa326afac91317413b47e82f80ad131340060" { // printf '1\ttwitch:u1\n2\ttwitch:u2\n' | shasum -a 256
		t.Fatal(got)
	}
}
