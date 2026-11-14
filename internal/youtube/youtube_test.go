package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/mockyoutube"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

const testClientID = "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type rig struct {
	t     *testing.T
	mock  *mockyoutube.Server
	url   string
	sec   *secrets.Store
	set   *settings.Store
	logs  *syncBuf
	inbox *events.Inbox
	// look replaces LookEvery, so tests needn't wait 2 minutes.
	look time.Duration
	// check replaces CheckEvery (an hour unless a test says).
	check time.Duration
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m := mockyoutube.New()
	srv := mockyoutube.Serve(m)
	t.Cleanup(srv.Close)
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
	return &rig{t: t, mock: m, url: srv.URL, sec: sec, set: settings.NewStore(db), logs: &syncBuf{},
		inbox: events.NewInbox(db, slog.New(slog.DiscardHandler))}
}

func (r *rig) service() *Service {
	r.t.Helper()
	s := New(Options{Secrets: r.sec, Settings: r.set, TestServer: r.url, Inbox: r.inbox,
		Logger: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if r.look > 0 {
		s.lookEvery = r.look
	}
	s.retryAfter = 20 * time.Millisecond
	s.checkEvery = time.Hour
	if r.check > 0 {
		s.checkEvery = r.check
	}
	s.Start(context.Background())
	r.t.Cleanup(s.Close)
	return s
}

func (r *rig) configured() *Service {
	r.t.Helper()
	s := r.service()
	if err := s.SetClient(context.Background(), testClientID, secrets.Value{}); err != nil {
		r.t.Fatal(err)
	}
	return s
}

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

// browse acts as the browser: it opens the sign-in page, adds what the
// stand-in's chooser would, and follows Google's redirect back.
func browse(t *testing.T, authURL string, extra url.Values) (int, string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, v := range extra {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// answers says whether anything still listens at u.
func answers(u string) bool {
	resp, err := http.Get(u) //nolint:gosec // a loopback address from the test's own sign-in
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func signIn(t *testing.T, a *Account, channel string) (int, string) {
	t.Helper()
	p, err := a.StartSignIn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return browse(t, p.AuthURL, url.Values{"mock_channel": {channel}})
}

func TestNotConfiguredUntilAClientIsGiven(t *testing.T) {
	r := newRig(t)
	s := r.service()
	ctx := context.Background()
	if st := s.Status(ctx); st.ClientSource != ClientNone || st.Streamer.State != StateNotConfigured || st.Bot.State != StateNotConfigured {
		t.Fatalf("%+v", st)
	}
	if _, err := s.streamer.StartSignIn(ctx); !errors.Is(err, ErrNoClient) {
		t.Fatal(err)
	}
	for _, bad := range []string{"abc", "123-ABC.apps.googleusercontent.com", testClientID + " "} {
		if err := s.SetClient(ctx, bad, secrets.Value{}); err == nil {
			t.Fatal(bad)
		}
	}
	if err := s.SetClient(ctx, "", secrets.NewValue("GOCSPX-abcdefghij")); err == nil {
		t.Fatal("a secret without a client ID")
	}
	if err := s.SetClient(ctx, testClientID, secrets.NewValue("bad secret!")); err == nil {
		t.Fatal("a bad secret")
	}
	if err := s.SetClient(ctx, testClientID, secrets.NewValue("GOCSPX-abcdefghij")); err != nil {
		t.Fatal(err)
	}
	st := s.Status(ctx)
	if st.ClientSource != ClientOwn || st.ClientID != testClientID || !st.HasSecret || st.Streamer.State != StateSignedOut ||
		st.TestServer != r.url {
		t.Fatalf("%+v", st)
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "GOCSPX") {
		t.Fatal("the secret is in the status")
	}
	if err := s.SetClient(ctx, "", secrets.Value{}); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(ctx); st.ClientSource != ClientNone || st.HasSecret || st.Streamer.State != StateNotConfigured {
		t.Fatalf("%+v", st)
	}
	if _, err := r.sec.Get(ctx, clientSecretKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("the secret was kept", err)
	}
}

func TestSignInThroughTheBrowser(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	ctx := context.Background()
	p, err := s.streamer.StartSignIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(p.AuthURL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || len(q.Get("state")) < 30 ||
		q.Get("scope") != Scope || q.Get("access_type") != "offline" || !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatal(p.AuthURL)
	}
	if st := s.streamer.Status(); st.State != StateSigningIn || st.SignIn == nil || st.SignIn.AuthURL != p.AuthURL {
		t.Fatalf("%+v", st)
	}
	// A visit without the right state changes nothing.
	resp, err := http.Get(q.Get("redirect_uri") + "?code=x&state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || s.streamer.Status().State != StateSigningIn {
		t.Fatal(resp.StatusCode, s.streamer.Status().State)
	}

	code, page := browse(t, p.AuthURL, url.Values{"mock_channel": {"UCstreamer"}})
	if code != 200 || !strings.Contains(page, "Signed in as Streamer") {
		t.Fatal(code, page)
	}
	st := waitFor(t, s.streamer, StateConnected)
	if st.Channel == nil || st.Channel.ID != "UCstreamer" || st.Channel.Handle != "@streamer" || len(st.MissingScopes) != 0 || st.Detail != "" {
		t.Fatalf("%+v", st)
	}
	// Signing in also looks for a live broadcast at once (1 unit).
	time.Sleep(50 * time.Millisecond)
	if q := s.Status(ctx).Quota; q.Used != CostChannelsList+r.mock.Calls("liveBroadcasts")*CostBroadcastsList || q.Limit != DailyQuota || q.Exhausted {
		t.Fatalf("%+v", q)
	}
	// The address is gone once the sign-in is done.
	if answers(q.Get("redirect_uri")) {
		time.Sleep(200 * time.Millisecond)
		if answers(q.Get("redirect_uri")) {
			t.Fatal("the loopback address still answers")
		}
	}
	b, _ := json.Marshal(s.Status(ctx))
	v, _ := r.sec.Get(ctx, "youtube/streamer/oauth")
	var tok tokens
	_ = json.Unmarshal([]byte(v.Reveal()), &tok)
	if tok.RefreshToken == "" || strings.Contains(string(b), tok.RefreshToken) || strings.Contains(string(b), tok.AccessToken) ||
		strings.Contains(r.logs.String(), tok.AccessToken) || strings.Contains(r.logs.String(), tok.RefreshToken) {
		t.Fatal("a token leaked")
	}

	// A restart checks it with Google again.
	s2 := r.service()
	st = waitFor(t, s2.streamer, StateConnected)
	if st.Channel.Title != "Streamer" || st.CheckedAt == nil {
		t.Fatalf("%+v", st)
	}
	if tok, err := s2.streamer.AccessToken(ctx); err != nil || tok.IsZero() {
		t.Fatal(err)
	}
}

func TestDeniedAndExpiredSignIns(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	ctx := context.Background()
	p, _ := s.streamer.StartSignIn(ctx)
	if _, page := browse(t, p.AuthURL, url.Values{"mock_deny": {"1"}}); !strings.Contains(page, "access_denied") {
		t.Fatal(page)
	}
	if st := waitFor(t, s.streamer, StateSignedOut); !strings.Contains(st.Detail, "access_denied") {
		t.Fatalf("%+v", st)
	}

	s.signInFor = 50 * time.Millisecond
	if _, err := s.bot.StartSignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if st := waitFor(t, s.bot, StateSignedOut); !strings.Contains(st.Detail, "in time") {
		t.Fatalf("%+v", st)
	}

	s.signInFor = time.Minute
	p, _ = s.bot.StartSignIn(ctx)
	s.bot.CancelSignIn()
	if st := s.bot.Status(); st.State != StateSignedOut {
		t.Fatalf("%+v", st)
	}
	// Its address closes, so Google's redirect has nowhere to land.
	time.Sleep(100 * time.Millisecond)
	u, _ := url.Parse(p.AuthURL)
	if answers(u.Query().Get("redirect_uri")) {
		t.Fatal("a cancelled sign-in's address still answers")
	}
	if s.bot.Status().State != StateSignedOut {
		t.Fatal("a cancelled sign-in finished")
	}
}

func TestTheBotMustBeAnotherChannel(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
	if _, page := signIn(t, s.bot, "UCstreamer"); !strings.Contains(page, "your own channel") {
		t.Fatal(page)
	}
	waitFor(t, s.bot, StateSignedOut)
	// The streamer's sign-in wasn't ended by refusing the bot's.
	s.streamer.Validate(context.Background())
	if st := s.streamer.Status(); st.State != StateConnected {
		t.Fatalf("%+v", st)
	}
	if _, page := signIn(t, s.bot, "nochannel"); !strings.Contains(page, "no YouTube channel") {
		t.Fatal(page)
	}
	if code, page := signIn(t, s.bot, "UCbot"); code != 200 || !strings.Contains(page, "Signed in as Bot") {
		t.Fatal(page)
	}
	if st := waitFor(t, s.bot, StateConnected); st.Channel.ID != "UCbot" {
		t.Fatalf("%+v", st)
	}
}

func TestEndedSignInsAndClientChanges(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	ctx := context.Background()
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)

	r.mock.EndSignIns()
	s.streamer.Validate(ctx)
	if st := s.streamer.Status(); st.State != StateNeedsSignIn || !strings.Contains(st.Detail, "Testing") || st.Channel != nil {
		t.Fatalf("%+v", st)
	}
	if _, err := r.sec.Get(ctx, "youtube/streamer/oauth"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("an ended sign-in was kept")
	}

	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
	other := strings.Replace(testClientID, "123456789012", "999999999999", 1)
	if err := s.SetClient(ctx, other, secrets.Value{}); err != nil {
		t.Fatal(err)
	}
	if st := s.streamer.Status(); st.State != StateNeedsSignIn || !strings.Contains(st.Detail, "client ID changed") {
		t.Fatalf("%+v", st)
	}
}

func TestAClientSecretWhenGoogleWantsOne(t *testing.T) {
	r := newRig(t)
	r.mock.RequireSecret("GOCSPX-right-secret")
	s := r.configured()
	ctx := context.Background()
	if _, page := signIn(t, s.streamer, "UCstreamer"); !strings.Contains(page, "client ID or secret") {
		t.Fatal(page)
	}
	waitFor(t, s.streamer, StateSignedOut)
	if err := s.SetClient(ctx, testClientID, secrets.NewValue("GOCSPX-right-secret")); err != nil {
		t.Fatal(err)
	}
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
	// A wrong secret later keeps the sign-in, as unknown, so fixing it brings it back.
	if err := s.SetClient(ctx, testClientID, secrets.NewValue("GOCSPX-wrong-secret")); err != nil {
		t.Fatal(err)
	}
	if st := s.streamer.Status(); st.State != StateUnknown || !strings.Contains(st.Detail, "client ID or secret") || st.Channel == nil {
		t.Fatalf("%+v", st)
	}
	if err := s.SetClient(ctx, testClientID, secrets.NewValue("GOCSPX-right-secret")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s.streamer, StateConnected)
}

func TestQuotaIsCountedAndRespected(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	ctx := context.Background()
	day := time.Date(2026, 11, 13, 23, 30, 0, 0, pacific)
	s.now = func() time.Time { return day }
	r.mock.QuotaExceeded(true)
	if _, page := signIn(t, s.streamer, "UCstreamer"); !strings.Contains(page, "quota") {
		t.Fatal(page)
	}
	q := s.Status(ctx).Quota
	if !q.Exhausted || q.Used != 1 || q.Day != "2026-11-13" || !q.ResetsAt.Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, pacific)) {
		t.Fatalf("%+v", q)
	}
	// Nothing more is asked of YouTube today.
	r.mock.QuotaExceeded(false)
	calls := r.mock.Calls("channels")
	if _, page := signIn(t, s.streamer, "UCstreamer"); !strings.Contains(page, "quota") || r.mock.Calls("channels") != calls {
		t.Fatal(page)
	}
	// A restart remembers it.
	s2 := r.service()
	s2.now = s.now
	if q := s2.Status(ctx).Quota; !q.Exhausted || q.Used != 1 {
		t.Fatalf("%+v", q)
	}
	// Midnight Pacific starts a new day.
	day = day.Add(time.Hour)
	if q := s.Status(ctx).Quota; q.Exhausted || q.Used != 0 || q.Day != "2026-11-14" {
		t.Fatalf("%+v", q)
	}
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
}

func TestSignOutEndsItAtGoogle(t *testing.T) {
	r := newRig(t)
	s := r.configured()
	ctx := context.Background()
	signIn(t, s.streamer, "UCstreamer")
	waitFor(t, s.streamer, StateConnected)
	if err := s.streamer.SignOut(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.streamer.Status(); st.State != StateSignedOut || st.Channel != nil {
		t.Fatalf("%+v", st)
	}
	if len(r.mock.Revoked()) != 1 || r.mock.ActiveTokens() != 0 {
		t.Fatal(r.mock.Revoked(), r.mock.ActiveTokens())
	}
	if err := s.streamer.SignOut(ctx); err != nil {
		t.Fatal("signing out twice", err)
	}
	if _, err := s.streamer.AccessToken(ctx); err == nil {
		t.Fatal("a token after signing out")
	}
}
