package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

const testClientID = "abcdefghij0123456789klmnopqrst"

type rig struct {
	t      *testing.T
	mock   *mocktwitch.Server
	url    string
	db     *storage.DB
	dir    string
	sec    *secrets.Store
	set    *settings.Store
	logBuf *syncBuf
	inbox  *events.Inbox
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func newRig(t *testing.T) *rig {
	t.Helper()
	m := mocktwitch.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	m.SetBase(srv.URL)
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
	sec, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, mock: m, url: srv.URL, db: db, dir: dir, sec: sec, set: settings.NewStore(db), logBuf: &syncBuf{},
		inbox: events.NewInbox(db, nil)}
}

// service starts a Service against the mock, as the engine would.
func (r *rig) service() *Service {
	r.t.Helper()
	s := New(Options{Secrets: r.sec, Settings: r.set, AuthURL: r.url, Inbox: r.inbox,
		Logger: slog.New(slog.NewTextHandler(r.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	s.pollEvery = 10 * time.Millisecond
	s.Start(context.Background())
	r.t.Cleanup(s.Close)
	return s
}

func (r *rig) configured() *Service {
	r.t.Helper()
	s := r.service()
	if err := s.SetClientID(context.Background(), testClientID); err != nil {
		r.t.Fatal(err)
	}
	return s
}

// waitFor polls until the account reaches state.
func waitFor(t *testing.T, a *Account, state State) AccountStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := a.Status(); st.State == state {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: state %s (%s), want %s", a.role, a.Status().State, a.Status().Detail, state)
	return AccountStatus{}
}

// signIn runs a whole device code sign-in as login.
func (r *rig) signIn(s *Service, role Role, login string) AccountStatus {
	r.t.Helper()
	a, _ := s.Account(role)
	p, err := a.StartSignIn(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	if a.Status().State != StateSigningIn || a.Status().SignIn.UserCode != p.UserCode {
		r.t.Fatalf("pending: %+v", a.Status())
	}
	if err := r.mock.Approve(p.UserCode, login); err != nil {
		r.t.Fatal(err)
	}
	return waitFor(r.t, a, StateConnected)
}

func TestNeedsAClientID(t *testing.T) {
	r := newRig(t)
	s := r.service()
	st := s.Status(context.Background())
	if st.ClientIDSource != ClientIDNone || st.Streamer.State != StateNotConfigured || st.Bot.State != StateNotConfigured {
		t.Fatalf("%+v", st)
	}
	if _, err := s.streamer.StartSignIn(context.Background()); !errors.Is(err, ErrNoClientID) {
		t.Fatal(err)
	}
	for _, bad := range []string{"short", "UPPERCASEUPPERCASE", "has space in it ok", strings.Repeat("a", 65)} {
		if err := s.SetClientID(context.Background(), bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if err := s.SetClientID(context.Background(), testClientID); err != nil {
		t.Fatal(err)
	}
	st = s.Status(context.Background())
	if st.ClientID != testClientID || st.ClientIDSource != ClientIDPasted || st.Streamer.State != StateSignedOut || st.TestServer == "" {
		t.Fatalf("%+v", st)
	}
}

func TestBuiltInClientIDAndPastedOverride(t *testing.T) {
	old := BuiltInClientID
	BuiltInClientID = "builtin0123456789"
	t.Cleanup(func() { BuiltInClientID = old })
	r := newRig(t)
	s := r.service()
	if id, src := s.ClientID(context.Background()); id != "builtin0123456789" || src != ClientIDBuiltIn {
		t.Fatal(id, src)
	}
	if s.streamer.Status().State != StateSignedOut {
		t.Fatal(s.streamer.Status())
	}
	_ = s.SetClientID(context.Background(), testClientID)
	if _, src := s.ClientID(context.Background()); src != ClientIDPasted {
		t.Fatal(src)
	}
	_ = s.SetClientID(context.Background(), "")
	if _, src := s.ClientID(context.Background()); src != ClientIDBuiltIn {
		t.Fatal(src)
	}
}

func TestSignInSavesAndSurvivesRestart(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	st := r.signIn(s, RoleStreamer, "GremlinStreamer")
	if st.Login != "gremlinstreamer" || st.UserID == "" || len(st.MissingScopes) != 0 || st.CheckedAt == nil {
		t.Fatalf("%+v", st)
	}
	// The token is a secret: never in the status, never in the log.
	v, err := r.sec.Get(context.Background(), "twitch/streamer/oauth")
	if err != nil {
		t.Fatal(err)
	}
	var tok tokens
	_ = json.Unmarshal([]byte(v.Reveal()), &tok)
	statusJSON, _ := json.Marshal(s.Status(context.Background()))
	for _, secret := range []string{tok.AccessToken, tok.RefreshToken} {
		if secret == "" || strings.Contains(string(statusJSON), secret) || strings.Contains(r.logBuf.String(), secret) {
			t.Fatal("a token leaked into the status or the log")
		}
	}
	s.Close()

	s2 := r.service() // a restart: loads, then checks with Twitch
	waitFor(t, s2.streamer, StateConnected)
	if s2.streamer.Status().Login != "gremlinstreamer" {
		t.Fatal(s2.streamer.Status())
	}
}

func TestExpiredAccessTokenIsRefreshedAndSaved(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	r.mock.ExpireAccessTokens("streamer")
	s.streamer.Validate(context.Background())
	if st := s.streamer.Status(); st.State != StateConnected {
		t.Fatalf("%+v", st)
	}
	// The new pair was saved: a restart works without the spent refresh token.
	s.Close()
	s2 := r.service()
	waitFor(t, s2.streamer, StateConnected)
}

func TestAccessTokenRefreshesOnceForConcurrentCallers(t *testing.T) {
	r := newRig(t)
	r.mock.AccessLifetime = 5 * time.Minute // inside the 10-minute margin: every call wants a refresh
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.streamer.AccessToken(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err) // a refresh token used twice would fail here
	}
	if s.streamer.Status().State != StateConnected {
		t.Fatal(s.streamer.Status())
	}
}

func TestRemovedAppEndsTheSession(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	changes := s.Changes()
	r.mock.Disconnect("streamer")
	s.streamer.Validate(context.Background())
	st := s.streamer.Status()
	if st.State != StateNeedsSignIn || st.Login != "" || !strings.Contains(st.Detail, "Sign in again") {
		t.Fatalf("%+v", st)
	}
	if _, err := r.sec.Get(context.Background(), "twitch/streamer/oauth"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("ended sign-in still saved")
	}
	select {
	case <-changes:
	default:
		t.Fatal("listeners weren't told")
	}
	if _, err := s.streamer.AccessToken(context.Background()); err == nil {
		t.Fatal("an ended sign-in still gives tokens")
	}
}

func TestTwitchDownIsUnknownNotSignedOut(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	r.mock.SetDown(true)
	s.streamer.Validate(context.Background())
	st := s.streamer.Status()
	if st.State != StateUnknown || st.Login != "streamer" || !strings.Contains(st.Detail, "Couldn't check") {
		t.Fatalf("%+v", st)
	}
	if _, err := r.sec.Get(context.Background(), "twitch/streamer/oauth"); err != nil {
		t.Fatal("token dropped while Twitch was down")
	}
	r.mock.SetDown(false)
	s.streamer.Validate(context.Background())
	if s.streamer.Status().State != StateConnected {
		t.Fatal(s.streamer.Status())
	}
}

func TestBotMustBeAnotherAccount(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	p, err := s.bot.StartSignIn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revokedBefore := r.mock.Revoked()
	_ = r.mock.Approve(p.UserCode, "streamer")
	st := waitFor(t, s.bot, StateSignedOut)
	if !strings.Contains(st.Detail, "your own account") {
		t.Fatalf("%+v", st)
	}
	if r.mock.Revoked() != revokedBefore+1 {
		t.Fatal("the refused token wasn't revoked")
	}
	st = r.signIn(s, RoleBot, "GremlinBot")
	if st.Login != "gremlinbot" {
		t.Fatal(st)
	}
	// Signing the streamer in as the bot's account is refused too.
	p, _ = s.streamer.StartSignIn(context.Background())
	_ = r.mock.Approve(p.UserCode, "gremlinbot")
	st = waitFor(t, s.streamer, StateConnected) // falls back to the existing sign-in
	if st.Login != "streamer" || !strings.Contains(st.Detail, "bot's account") {
		t.Fatalf("%+v", st)
	}
}

func TestDeniedExpiredCancelledAndReplacedSignIns(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	p, _ := s.streamer.StartSignIn(context.Background())
	_ = r.mock.Deny(p.UserCode)
	if st := waitFor(t, s.streamer, StateSignedOut); !strings.Contains(st.Detail, "didn't complete") {
		t.Fatalf("denied: %+v", st)
	}

	r.mock.CodeLifetime = 100 * time.Millisecond
	_, _ = s.streamer.StartSignIn(context.Background())
	if st := waitFor(t, s.streamer, StateSignedOut); !strings.Contains(st.Detail, "expired") {
		t.Fatalf("expired: %+v", st)
	}
	r.mock.CodeLifetime = time.Minute

	_, _ = s.streamer.StartSignIn(context.Background())
	s.streamer.CancelSignIn()
	s.streamer.CancelSignIn() // harmless twice
	if st := s.streamer.Status(); st.State != StateSignedOut || st.SignIn != nil {
		t.Fatalf("cancelled: %+v", st)
	}

	first, _ := s.streamer.StartSignIn(context.Background())
	second, _ := s.streamer.StartSignIn(context.Background())
	_ = r.mock.Approve(first.UserCode, "someone") // the replaced code no longer counts
	time.Sleep(60 * time.Millisecond)
	if st := s.streamer.Status(); st.State != StateSigningIn || st.SignIn.UserCode != second.UserCode {
		t.Fatalf("replaced: %+v", st)
	}
	_ = r.mock.Approve(second.UserCode, "streamer")
	if st := waitFor(t, s.streamer, StateConnected); st.Login != "streamer" {
		t.Fatal(st)
	}
}

func TestSignOutRevokesAndForgets(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleBot, "bot")
	for i := 0; i < 2; i++ { // harmless twice
		if err := s.bot.SignOut(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if s.bot.Status().State != StateSignedOut || r.mock.ValidTokens("bot") != 0 {
		t.Fatalf("%+v, %d live tokens", s.bot.Status(), r.mock.ValidTokens("bot"))
	}
	if _, err := r.sec.Get(context.Background(), "twitch/bot/oauth"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("still saved")
	}
}

func TestSignOutWorksWhileTwitchIsDown(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	r.mock.SetDown(true)
	if err := s.streamer.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.streamer.Status().State != StateSignedOut {
		t.Fatal(s.streamer.Status())
	}
}

func TestMissingScopesAreShown(t *testing.T) {
	r := newRig(t)
	r.mock.GrantScopes = []string{"user:read:chat"}
	s := r.configured()
	st := r.signIn(s, RoleStreamer, "streamer")
	if len(st.MissingScopes) != 1 || st.MissingScopes[0] != "user:write:chat" || !strings.Contains(st.Detail, "user:write:chat") {
		t.Fatalf("%+v", st)
	}
}

func TestChangingTheClientIDEndsSignIns(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	r.signIn(s, RoleStreamer, "streamer")
	if err := s.SetClientID(context.Background(), "another0123456789app"); err != nil {
		t.Fatal(err)
	}
	if st := s.streamer.Status(); st.State != StateNeedsSignIn || !strings.Contains(st.Detail, "Client ID changed") {
		t.Fatalf("%+v", st)
	}
}

func TestManifestIsComplete(t *testing.T) {
	m := TwitchManifest()
	if m.Provider != "twitch" || m.VerifiedAt == "" || len(m.Docs) == 0 {
		t.Fatal(m)
	}
	for _, c := range m.Capabilities {
		if (c.Support == Partial || c.Support == Unknown || c.Support == Unsupported) && c.Notes == "" {
			t.Errorf("%s: %s needs a note", c.Name, c.Support)
		}
		if c.Support == PermissionRequired && len(c.Scopes) == 0 {
			t.Errorf("%s needs its scopes", c.Name)
		}
	}
}
