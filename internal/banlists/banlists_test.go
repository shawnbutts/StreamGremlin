package banlists

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// fakeTwitch is a channel with accounts, bans, mods and VIPs, and a
// rate-limit bucket.
type fakeTwitch struct {
	mu       sync.Mutex
	users    map[string]string // login -> id
	banned   map[string]bool   // id
	mods     map[string]bool
	vips     map[string]bool
	bucket   int // requests left before 429; <0: unlimited
	reset    time.Time
	bans     []string // ids banned through ListBan, in order
	unbans   []string
	noAnswer map[string]bool // ids whose ban gets no answer (but happens)
	stop     bool            // every ban answers "sign in again"
	reasons  []string
	blocked  map[string]bool
	blocks   []string
}

func newFake(n int) *fakeTwitch {
	f := &fakeTwitch{users: map[string]string{"streamer": "1"}, banned: map[string]bool{}, mods: map[string]bool{}, vips: map[string]bool{},
		bucket: -1, noAnswer: map[string]bool{}}
	for i := range n {
		f.users[fmt.Sprintf("bot%04d", i)] = fmt.Sprint(1000 + i)
	}
	return f
}

func (f *fakeTwitch) take() (twitch.RateInfo, bool) {
	if f.bucket < 0 {
		return twitch.RateInfo{}, true
	}
	if f.bucket == 0 {
		return twitch.RateInfo{Known: true, Limit: 800, Remaining: 0, Reset: f.reset}, false
	}
	f.bucket--
	return twitch.RateInfo{Known: true, Limit: 800, Remaining: f.bucket, Reset: f.reset}, true
}

func (f *fakeTwitch) LookUpUsers(_ context.Context, logins, ids []string) ([]twitch.User, twitch.RateInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(logins)+len(ids) > twitch.MaxLookup {
		return nil, twitch.RateInfo{}, errors.New("too many")
	}
	ri, _ := f.take()
	var out []twitch.User
	for _, l := range logins {
		if id, ok := f.users[l]; ok {
			out = append(out, twitch.User{ID: id, Login: l})
		}
	}
	for _, id := range ids {
		for l, x := range f.users {
			if x == id {
				out = append(out, twitch.User{ID: id, Login: l})
			}
		}
	}
	return out, ri, nil
}

func (f *fakeTwitch) BannedAmong(_ context.Context, ids []string) (map[string]bool, twitch.RateInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ri, _ := f.take()
	out := map[string]bool{}
	for _, id := range ids {
		if f.banned[id] {
			out[id] = true
		}
	}
	return out, ri, nil
}

func (f *fakeTwitch) RolesAmong(_ context.Context, ids []string) (map[string]bool, map[string]bool, twitch.RateInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ri, _ := f.take()
	m, v := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		m[id], v[id] = f.mods[id], f.vips[id]
	}
	return m, v, ri, nil
}

func (f *fakeTwitch) BansPage(_ context.Context, after string) ([]twitch.BannedUser, string, twitch.RateInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []twitch.BannedUser
	for l, id := range f.users {
		if f.banned[id] {
			all = append(all, twitch.BannedUser{UserID: id, Login: l})
		}
	}
	// Two at a time, by id, to page.
	var page []twitch.BannedUser
	next := ""
	for _, b := range sortByID(all) {
		if b.UserID <= after {
			continue
		}
		if len(page) == 2 {
			next = page[1].UserID
			break
		}
		page = append(page, b)
	}
	return page, next, twitch.RateInfo{}, nil
}

func sortByID(b []twitch.BannedUser) []twitch.BannedUser {
	for i := range b {
		for j := i + 1; j < len(b); j++ {
			if b[j].UserID < b[i].UserID {
				b[i], b[j] = b[j], b[i]
			}
		}
	}
	return b
}

func (f *fakeTwitch) ListBan(_ context.Context, id, reason string) (twitch.ListOutcome, string, twitch.RateInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stop {
		return twitch.ListStop, "Sign in to Twitch again", twitch.RateInfo{}
	}
	ri, ok := f.take()
	if !ok {
		return twitch.ListWait, "wait", ri
	}
	if f.banned[id] {
		return twitch.ListAlready, "", ri
	}
	f.banned[id] = true
	f.bans = append(f.bans, id)
	f.reasons = append(f.reasons, reason)
	if f.noAnswer[id] {
		delete(f.noAnswer, id)
		return twitch.ListUnknown, "no answer", ri
	}
	return twitch.ListDone, "", ri
}

func (f *fakeTwitch) ListUnban(_ context.Context, id string) (twitch.ListOutcome, string, twitch.RateInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ri, _ := f.take()
	if !f.banned[id] {
		return twitch.ListNotBanned, "", ri
	}
	delete(f.banned, id)
	f.unbans = append(f.unbans, id)
	return twitch.ListDone, "", ri
}

func (f *fakeTwitch) StreamerID() string { return "1" }

func (f *fakeTwitch) ListBlock(_ context.Context, id string) (twitch.ListOutcome, string, twitch.RateInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocked == nil {
		f.blocked = map[string]bool{}
	}
	f.blocked[id] = true
	f.blocks = append(f.blocks, id)
	if f.noAnswer[id] {
		delete(f.noAnswer, id)
		return twitch.ListUnknown, "no answer", twitch.RateInfo{}
	}
	return twitch.ListDone, "", twitch.RateInfo{}
}

func (f *fakeTwitch) ListUnblock(_ context.Context, id string) (twitch.ListOutcome, string, twitch.RateInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.blocked, id)
	return twitch.ListDone, "", twitch.RateInfo{}
}

type rig struct {
	t    *testing.T
	db   *storage.DB
	tw   *fakeTwitch
	s    *Service
	dir  string
	live bool
	mu   sync.Mutex
}

func newRig(t *testing.T, tw *fakeTwitch) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, tw: tw, dir: dir}
	r.start()
	return r
}

func (r *rig) start() {
	r.s = New(Options{DB: r.db, Twitch: r.tw, Idle: 10 * time.Millisecond,
		Holders: func(_ context.Context, ids []string) (map[string]bool, error) {
			out := map[string]bool{}
			for _, id := range ids {
				out[id] = id == "1005"
			}
			return out, nil
		},
		Live: func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.live }})
	r.s.Start(context.Background())
	r.t.Cleanup(r.s.Close)
}

func (r *rig) file(name string, lines ...string) string {
	p := filepath.Join(r.dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *rig) phase(id, want string) List {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		l, err := r.s.Get(context.Background(), id)
		if err == nil && l.Phase == want && !l.Paused {
			return l
		}
		if time.Now().After(deadline) {
			c, _ := r.s.Counts(context.Background(), id)
			r.t.Fatalf("phase %s (paused %v, %q), want %s: %v", l.Phase, l.Paused, l.Problem, want, c)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rig) counts(id string) map[string]int {
	r.t.Helper()
	c, err := r.s.Counts(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func TestImportCheckConfirmBan(t *testing.T) {
	tw := newFake(10)
	tw.banned["1001"] = true
	tw.mods["1002"] = true
	tw.vips["1003"] = true
	r := newRig(t, tw)
	ctx := context.Background()
	path := r.file("list.txt", "# bots", "bot0000", "@BOT0001", "bot0002", "bot0003", "bot0004", "bot0005", "id:1006", "1007", "nobody_here",
		"streamer", "not a name", "bot0000")
	if _, err := r.s.Import(ctx, "Hate raid", "relative.txt", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("a relative path:", err)
	}
	l, err := r.s.Import(ctx, "Hate raid", path, "")
	if err != nil || l.Reason != "Ban list: Hate raid" {
		t.Fatalf("%+v %v", l, err)
	}
	l = r.phase(l.ID, Ready)
	c := r.counts(l.ID)
	// bot0000, bot0004, 1007: waiting; id:1006 isn't a name ("id:" isn't
	// a login), so it's skipped like "not a name".
	if c[StateWaiting] != 3 || c[StateAlready] != 1 || c[StateKept] != 4 || c[StateNotFound] != 1 || l.Skipped != 2 {
		t.Fatalf("%v skipped %d", c, l.Skipped)
	}
	if len(tw.bans) != 0 {
		t.Fatal("banned before confirming")
	}
	kept, _ := r.s.Entries(ctx, l.ID, []string{StateKept}, "", 10)
	details := []string{}
	for _, e := range kept {
		details = append(details, e.Detail)
	}
	if strings.Join(details, ",") != "a moderator,a VIP,has points,you" {
		t.Fatalf("%v", details)
	}
	if _, err := r.s.Confirm(ctx, l.ID, ConfirmOptions{}); err != nil {
		t.Fatal(err)
	}
	r.phase(l.ID, Done)
	c = r.counts(l.ID)
	if c[StateBanned] != 3 || len(tw.bans) != 3 || tw.reasons[0] != "Ban list: Hate raid" {
		t.Fatalf("%v %v", c, tw.bans)
	}
	if !tw.banned["1000"] || !tw.banned["1004"] || !tw.banned["1007"] || tw.banned["1002"] {
		t.Fatal("wrong bans")
	}
}

// A newer file bans only new names; removed ones are offered and unbanned
// only when asked; a ban lifted by hand isn't put back unless asked; undo
// unbans only what the list banned.
func TestSyncByHandAndUndo(t *testing.T) {
	tw := newFake(10)
	tw.banned["1009"] = true // banned before, by someone else
	r := newRig(t, tw)
	ctx := context.Background()
	l, _ := r.s.Import(ctx, "list", r.file("a.txt", "bot0000", "bot0001", "bot0002", "bot0009"), "spam bots")
	r.phase(l.ID, Ready)
	_, _ = r.s.Confirm(ctx, l.ID, ConfirmOptions{})
	r.phase(l.ID, Done)
	if len(tw.bans) != 3 {
		t.Fatalf("%v", tw.bans)
	}
	// Someone unbans bot0001 on Twitch; the comparison notices.
	tw.mu.Lock()
	delete(tw.banned, "1001")
	tw.mu.Unlock()
	if _, err := r.s.Compare(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	r.phase(l.ID, Done)
	if c := r.counts(l.ID); c[StateByHand] != 1 || c[StateBanned] != 2 {
		t.Fatalf("%v", c)
	}
	// The newer file: bot0002 gone, bot0003 new.
	if _, err := r.s.Import(ctx, "list", r.file("b.txt", "bot0000", "bot0001", "bot0003", "bot0009"), ""); err != nil {
		t.Fatal(err)
	}
	r.phase(l.ID, Ready)
	c := r.counts(l.ID)
	if c[StateRemoved] != 1 || c[StateWaiting] != 1 || c[StateByHand] != 1 || c[StateBanned] != 1 || c[StateAlready] != 1 {
		t.Fatalf("%v", c)
	}
	before := len(tw.bans)
	_, _ = r.s.Confirm(ctx, l.ID, ConfirmOptions{})
	r.phase(l.ID, Done)
	// Only bot0003 was banned: bot0000 wasn't asked about again, bot0001
	// stays unbanned, bot0002 stays banned (removed, not unbanned).
	if len(tw.bans) != before+1 || tw.bans[len(tw.bans)-1] != "1003" || tw.banned["1001"] || !tw.banned["1002"] || len(tw.unbans) != 0 {
		t.Fatalf("bans %v unbans %v", tw.bans, tw.unbans)
	}
	_, _ = r.s.Confirm(ctx, l.ID, ConfirmOptions{UnbanRemoved: true, RebanByHand: true})
	r.phase(l.ID, Done)
	if tw.banned["1002"] || !tw.banned["1001"] {
		t.Fatalf("banned %v", tw.banned)
	}
	// Undo: everything the list banned, never bot0009.
	if _, err := r.s.Undo(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	r.phase(l.ID, Done)
	if !tw.banned["1009"] || tw.banned["1000"] || tw.banned["1001"] || tw.banned["1003"] {
		t.Fatalf("banned %v", tw.banned)
	}
}

// Twitch's bucket: the runner stops at what's kept spare and waits for the
// reset; "too many" waits too and sends again; an unanswered ban is
// settled from Twitch's list, never sent twice; a sign-in problem pauses
// the list instead of refusing every name.
func TestPaceUnknownsAndStops(t *testing.T) {
	tw := newFake(30)
	tw.bucket, tw.reset = 25, time.Now().Add(200*time.Millisecond)
	tw.noAnswer["1003"] = true
	r := newRig(t, tw)
	r.s.o.Spare = 5
	ctx := context.Background()
	var names []string
	for i := range 30 {
		names = append(names, fmt.Sprintf("bot%04d", i))
	}
	l, _ := r.s.Import(ctx, "big", r.file("big.txt", names...), "")
	r.phase(l.ID, Ready)
	_, _ = r.s.Confirm(ctx, l.ID, ConfirmOptions{})
	// It stops at the spare and waits for the reset.
	time.Sleep(100 * time.Millisecond)
	tw.mu.Lock()
	left := tw.bucket
	tw.bucket, tw.reset = 1000, time.Now()
	tw.mu.Unlock()
	if left < 5 {
		t.Fatalf("went past the spare: %d left", left)
	}
	r.phase(l.ID, Done)
	count := map[string]int{}
	for _, id := range tw.bans {
		count[id]++
	}
	// bot0005 holds points: left alone.
	if len(count) != 29 || count["1003"] != 1 || r.counts(l.ID)[StateBanned] != 29 {
		t.Fatalf("%v %v", count, r.counts(l.ID))
	}
	// A sign-in problem pauses the list, in words, with nothing refused.
	tw.mu.Lock()
	tw.stop = true
	tw.users["late"] = "5000"
	tw.mu.Unlock()
	_, _ = r.s.Import(ctx, "big", r.file("big2.txt", append(names, "late")...), "")
	r.phase(l.ID, Ready)
	_, _ = r.s.Confirm(ctx, l.ID, ConfirmOptions{})
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := r.s.Get(ctx, l.ID)
		if got.Paused && strings.Contains(got.Problem, "Sign in") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if c := r.counts(l.ID); c[StateRefused] != 0 || c[StateWaiting] != 1 {
		t.Fatalf("%v", c)
	}
	tw.mu.Lock()
	tw.stop = false
	tw.mu.Unlock()
	_, _ = r.s.SetPaused(ctx, l.ID, false)
	r.phase(l.ID, Done)
}

// A restart in the middle carries on from where it was; while you're
// live it waits; Twitch's own list copies into a new list.
func TestRestartLiveAndCopy(t *testing.T) {
	tw := newFake(20)
	r := newRig(t, tw)
	ctx := context.Background()
	var names []string
	for i := range 20 {
		names = append(names, fmt.Sprintf("bot%04d", i))
	}
	l, _ := r.s.Import(ctx, "list", r.file("l.txt", names...), "")
	r.phase(l.ID, Ready)
	r.mu.Lock()
	r.live = true
	r.mu.Unlock()
	_, _ = r.s.Confirm(ctx, l.ID, ConfirmOptions{})
	time.Sleep(60 * time.Millisecond)
	if len(tw.bans) != 0 || !r.s.ProgressOf(l.ID, 20).WaitingLive {
		t.Fatalf("banned while live: %v", tw.bans)
	}
	r.mu.Lock()
	r.live = false
	r.mu.Unlock()
	// Stop part way, as a crash would (one ban sent, no answer saved).
	for deadline := time.Now().Add(5 * time.Second); r.counts(l.ID)[StateBanned] < 5 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	r.s.Close()
	_, _ = r.db.ExecContext(ctx, "UPDATE ban_list_entries SET state = ? WHERE list_id = ? AND state = ? AND name = (SELECT min(name) FROM ban_list_entries WHERE list_id = ? AND state = ?)",
		StateSent, l.ID, StateBanned, l.ID, StateBanned)
	r.start()
	r.phase(l.ID, Done)
	count := map[string]int{}
	for _, id := range tw.bans {
		count[id]++
	}
	for id, n := range count {
		if n != 1 {
			t.Fatalf("%s banned %d times", id, n)
		}
	}
	if len(count) != 19 || r.counts(l.ID)[StateBanned] != 19 {
		t.Fatalf("%v", r.counts(l.ID))
	}
	cp, err := r.s.Copy(ctx, "Twitch's list")
	if err != nil {
		t.Fatal(err)
	}
	r.phase(cp.ID, Done)
	if c := r.counts(cp.ID); c[StateAlready] != 19 {
		t.Fatalf("%v", c)
	}
	if _, err := r.s.Copy(ctx, "Twitch's list"); !errors.Is(err, ErrInvalid) {
		t.Fatal("two lists with one name")
	}
	if err := r.s.Delete(ctx, cp.ID); err != nil || len(tw.unbans) != 0 {
		t.Fatal("deleting unbanned", err)
	}
}

// A list from names (a raid's accounts) is checked like a file, and with
// start banned at once, even while live.
func TestFromNames(t *testing.T) {
	tw := newFake(5)
	r := newRig(t, tw)
	r.mu.Lock()
	r.live = true
	r.mu.Unlock()
	ctx := context.Background()
	if _, err := r.s.FromNames(ctx, "Raid", nil, "", true); !errors.Is(err, ErrInvalid) {
		t.Fatal("no names")
	}
	l, err := r.s.FromNames(ctx, "Raid 20:04", []string{"bot0000", "@BOT0001", "streamer", "not a name"}, "", true)
	if err != nil || l.PauseLive {
		t.Fatalf("%+v %v", l, err)
	}
	r.phase(l.ID, Done)
	if c := r.counts(l.ID); c[StateBanned] != 2 || c[StateKept] != 1 || !tw.banned["1000"] || !tw.banned["1001"] {
		t.Fatalf("%v", c)
	}
	// Without start: it waits while live, as any list does; then it's
	// checked and waits for a confirm.
	l2, _ := r.s.FromNames(ctx, "Later", []string{"bot0002"}, "", false)
	time.Sleep(50 * time.Millisecond)
	if got, _ := r.s.Get(ctx, l2.ID); got.Phase != Checking {
		t.Fatalf("checked while live: %+v", got)
	}
	r.mu.Lock()
	r.live = false
	r.mu.Unlock()
	r.phase(l2.ID, Ready)
}

// A block list (D-109): blocks instead of banning, banned accounts too,
// you and your mods still left alone; an unanswered block is sent again
// (harmless); undo unblocks; it can't be compared.
func TestBlockList(t *testing.T) {
	tw := newFake(5)
	tw.banned["1002"] = true
	tw.mods["1003"] = true
	tw.noAnswer["1001"] = true
	r := newRig(t, tw)
	ctx := context.Background()
	l, err := r.s.BlockNames(ctx, "Follow bots", []string{"bot0000", "bot0001", "bot0002", "bot0003", "streamer"}, true)
	if err != nil || l.Kind != KindBlock {
		t.Fatalf("%+v %v", l, err)
	}
	r.phase(l.ID, Done)
	if len(tw.bans) != 0 || !tw.blocked["1000"] || !tw.blocked["1001"] || !tw.blocked["1002"] || tw.blocked["1003"] || tw.blocked["1"] {
		t.Fatalf("bans %v blocked %v", tw.bans, tw.blocked)
	}
	if c := r.counts(l.ID); c[StateBanned] != 3 || c[StateKept] != 2 {
		t.Fatalf("%v", c)
	}
	if _, err := r.s.Compare(ctx, l.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal("compared a block list")
	}
	if _, err := r.s.Undo(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	r.phase(l.ID, Done)
	if len(tw.blocked) != 0 {
		t.Fatalf("still blocked %v", tw.blocked)
	}
}
