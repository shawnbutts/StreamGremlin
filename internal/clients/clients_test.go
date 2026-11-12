package clients

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type rig struct {
	t   *testing.T
	ctx context.Context
	db  *storage.DB
	svc *Service
	now time.Time
}

func newRig(t *testing.T) *rig {
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
	r := &rig{t: t, ctx: ctx, db: db, now: time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)}
	r.svc = New(db, func() time.Time { return r.now }, nil)
	return r
}

func (r *rig) start() Pairing {
	r.t.Helper()
	p, err := r.svc.StartPairing(StreamDeck)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *rig) pair(code, name, req string) (Client, string) {
	r.t.Helper()
	c, tok, err := r.svc.Exchange(r.ctx, code, name, req)
	if err != nil {
		r.t.Fatal(err)
	}
	return c, tok
}

// A code is shown, typed into the plugin, and swapped once for a token
// that names a Stream Deck; the token is kept only as a hash.
func TestPairing(t *testing.T) {
	r := newRig(t)
	if st := r.svc.State(); st.State != StateNone {
		t.Fatal(st)
	}
	p := r.start()
	if len(p.Code) != 9 || p.Code[4] != '-' || !p.ExpiresAt.Equal(r.now.Add(CodeLife)) {
		t.Fatalf("code: %+v", p)
	}
	if st := r.svc.State(); st.State != StateWaiting || st.Code != p.Code {
		t.Fatal(st)
	}
	// Typed loosely: lower case, without the dash, with spaces.
	typed := " " + strings.ToLower(strings.ReplaceAll(p.Code, "-", "")) + " "
	c, tok := r.pair(typed, "  Stream Deck on the desk ", "req1")
	if c.Kind != StreamDeck || c.Name != "Stream Deck on the desk" || !strings.HasPrefix(c.ID, "cl_") || len(tok) < 40 {
		t.Fatalf("%+v %q", c, tok)
	}
	if st := r.svc.State(); st.State != StatePaired || st.Client == nil || st.Client.ID != c.ID {
		t.Fatal(st)
	}
	got, ok := r.svc.Authenticate(r.ctx, tok)
	if !ok || got.ID != c.ID {
		t.Fatal("token refused", got)
	}
	if _, ok := r.svc.Authenticate(r.ctx, tok+"x"); ok {
		t.Fatal("wrong token accepted")
	}
	var raw string
	if err := r.db.QueryRowContext(r.ctx, "SELECT token_hash FROM api_clients").Scan(&raw); err != nil || raw == tok || strings.Contains(raw, tok) {
		t.Fatal("token stored as itself", raw, err)
	}
	// The same request again (a retry after a dropped answer) gets the same
	// token; anything else finds the code used up.
	if _, again := r.pair(p.Code, "Stream Deck on the desk", "req1"); again != tok {
		t.Fatal("retry got another token")
	}
	if _, _, err := r.svc.Exchange(r.ctx, p.Code, "Another", "req2"); !errors.Is(err, ErrNoPairing) {
		t.Fatalf("used twice: %v", err)
	}
	list, _ := r.svc.List(r.ctx)
	if len(list) != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("%+v", list)
	}
}

// Five wrong codes stop the pairing; a code runs out; starting again
// replaces the old code; cancelling ends it; with no pairing nothing works.
func TestPairingRefusals(t *testing.T) {
	r := newRig(t)
	if _, _, err := r.svc.Exchange(r.ctx, "ABCD-EFGH", "Deck", "r"); !errors.Is(err, ErrNoPairing) {
		t.Fatal(err)
	}
	p := r.start()
	for i := range MaxWrong {
		_, _, err := r.svc.Exchange(r.ctx, "2222-2222", "Deck", "r")
		if i < MaxWrong-1 && !errors.Is(err, ErrWrongCode) {
			t.Fatal(i, err)
		}
	}
	if st := r.svc.State(); st.State != StateLocked {
		t.Fatal("not locked:", st)
	}
	if _, _, err := r.svc.Exchange(r.ctx, p.Code, "Deck", "r"); !errors.Is(err, ErrNoPairing) {
		t.Fatal("right code after locking:", err)
	}

	p = r.start()
	r.now = r.now.Add(CodeLife + time.Second)
	if _, _, err := r.svc.Exchange(r.ctx, p.Code, "Deck", "r"); !errors.Is(err, ErrNoPairing) {
		t.Fatal("expired:", err)
	}
	if st := r.svc.State(); st.State != StateExpired {
		t.Fatal(st)
	}

	old := r.start()
	p = r.start()
	if old.Code == p.Code {
		t.Fatal("same code twice")
	}
	if _, _, err := r.svc.Exchange(r.ctx, old.Code, "Deck", "r"); !errors.Is(err, ErrWrongCode) {
		t.Fatal("old code:", err)
	}
	r.svc.Cancel()
	if _, _, err := r.svc.Exchange(r.ctx, p.Code, "Deck", "r"); !errors.Is(err, ErrNoPairing) {
		t.Fatal("cancelled:", err)
	}
	if st := r.svc.State(); st.State != StateNone {
		t.Fatal(st)
	}

	// Bad names are refused without using up the code.
	p = r.start()
	for _, name := range []string{"", "   ", strings.Repeat("x", MaxName+1), "bad\x00name"} {
		if _, _, err := r.svc.Exchange(r.ctx, p.Code, name, "r"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	r.pair(p.Code, "Deck", "r")
}

// Two plugins racing with the same code: one pairs.
func TestPairingRace(t *testing.T) {
	r := newRig(t)
	p := r.start()
	var wg sync.WaitGroup
	var mu sync.Mutex
	paired := 0
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := r.svc.Exchange(r.ctx, p.Code, "Deck", "req"+strconv.Itoa(i)); err == nil {
				mu.Lock()
				paired++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if list, _ := r.svc.List(r.ctx); paired != 1 || len(list) != 1 {
		t.Fatal(paired, list)
	}
}

// Forgetting a client refuses its token at once; clients survive a
// restart, a pairing in progress doesn't; there's a limit.
func TestForgetRestartLimit(t *testing.T) {
	r := newRig(t)
	p := r.start()
	c, tok := r.pair(p.Code, "Deck", "r")
	r.start()
	// A restart: a new service on the same data.
	r.svc = New(r.db, func() time.Time { return r.now }, nil)
	if st := r.svc.State(); st.State != StateNone {
		t.Fatal("pairing survived a restart", st)
	}
	if _, ok := r.svc.Authenticate(r.ctx, tok); !ok {
		t.Fatal("token lost on restart")
	}
	existed, err := r.svc.Forget(r.ctx, c.ID)
	if err != nil || !existed {
		t.Fatal(existed, err)
	}
	if _, ok := r.svc.Authenticate(r.ctx, tok); ok {
		t.Fatal("forgotten token accepted")
	}
	if existed, _ := r.svc.Forget(r.ctx, c.ID); existed {
		t.Fatal("forgotten twice")
	}
	for i := range MaxClients {
		p := r.start()
		r.pair(p.Code, "Deck", "r"+strconv.Itoa(i))
	}
	if _, err := r.svc.StartPairing(StreamDeck); !errors.Is(err, ErrFull) {
		t.Fatal("full:", err)
	}
	if _, err := r.svc.StartPairing("phone"); !errors.Is(err, ErrInvalid) {
		t.Fatal("kind:", err)
	}
}

// Last used is kept, but written at most once a minute.
func TestLastUsed(t *testing.T) {
	r := newRig(t)
	p := r.start()
	c, tok := r.pair(p.Code, "Deck", "r")
	first := r.now
	r.now = r.now.Add(30 * time.Second)
	r.svc.Authenticate(r.ctx, tok)
	list, _ := r.svc.List(r.ctx)
	if !list[0].LastUsedAt.Equal(first) {
		t.Fatal("written within the minute", list[0].LastUsedAt)
	}
	r.now = r.now.Add(time.Minute)
	r.svc.Authenticate(r.ctx, tok)
	list, _ = r.svc.List(r.ctx)
	if !list[0].LastUsedAt.Equal(r.now) || list[0].ID != c.ID {
		t.Fatal("not written after a minute", list[0].LastUsedAt)
	}
}
