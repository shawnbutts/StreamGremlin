package youtube

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"code.streamgremlin.com/streamgremlin/internal/chat"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// BuiltInClientID and BuiltInClientSecret are StreamGremlin's own Google
// project, set at build time (-ldflags -X) once Google approves it
// (D-095). They're never committed. Until then every build uses the
// streamer's own project, pasted on Connections.
var (
	BuiltInClientID     = ""
	BuiltInClientSecret = ""
)

// ErrNoClient means there's no client ID: neither a built-in one nor the
// streamer's own.
var ErrNoClient = errors.New("youtube: no Google client ID; add your own Google project's on Connections")

// ErrInvalid wraps bad input, so the API can answer 400.
var ErrInvalid = errors.New("youtube: invalid")

// Scope is what both accounts ask for: YouTube's "manage your YouTube
// account" over TLS. Chat, moderating and the broadcast all need it; no
// narrower scope covers sending chat (D-096).
const Scope = "https://www.googleapis.com/auth/youtube.force-ssl"

// Scopes each account asks for.
var (
	StreamerScopes = []string{Scope}
	BotScopes      = []string{Scope}
)

// checkEvery is how often a sign-in is checked with Google. Access tokens
// last an hour, so each check is a refresh.
const checkEvery = 50 * time.Minute

const (
	settingsKey     = "youtube"
	clientSecretKey = "youtube/client-secret"
)

type savedSettings struct {
	ClientID string `json:"client_id,omitempty"`
}

// ClientSource says whose Google project signs in.
type ClientSource string

const (
	ClientNone    ClientSource = "none"
	ClientBuiltIn ClientSource = "built_in"
	ClientOwn     ClientSource = "own" // the streamer's own project
)

// Options configure a Service.
type Options struct {
	Secrets  *secrets.Store
	Settings *settings.Store
	// Inbox receives YouTube chat; nil reads none.
	Inbox *events.Inbox
	// TestServer replaces Google with a stand-in serving every endpoint
	// under one loopback base, such as mockyoutube. The app says so.
	TestServer string
	Logger     *slog.Logger
}

// Service owns the streamer's and the bot's YouTube sign-ins.
type Service struct {
	secrets  *secrets.Store
	settings *settings.Store
	oauth    *oauth
	data     *data
	quota    *quota
	test     string
	log      *slog.Logger
	now      func() time.Time

	ctx    context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup
	notify *events.Notifier

	streamer, bot *Account
	// signInFor is how long a sign-in waits for the browser.
	signInFor time.Duration

	// Chat (chat.go). endedChats and lastEnded belong to runChat.
	inbox      *events.Inbox
	chatConn   *grpc.ClientConn
	chatMu     sync.Mutex
	chat       ChatStatus
	kick       chan struct{}
	accounts   *events.Notifier // an account's state changed
	endedChats map[string]bool
	lastEnded  string
	lookEvery  time.Duration
	own        chat.OwnSends // workflows' messages sent as you, to skip their echo
	retryAfter time.Duration // the first wait before reconnecting
}

// New returns a Service. Call Start to load saved sign-ins.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	ep := defaultEndpoints()
	if opts.TestServer != "" {
		ep = testEndpoints(opts.TestServer)
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Service{
		secrets: opts.Secrets, settings: opts.Settings, oauth: newOAuth(ep), test: opts.TestServer,
		log: opts.Logger.With("component", "youtube"), now: time.Now, ctx: ctx, stop: stop,
		notify: events.NewNotifier(), signInFor: 10 * time.Minute,
		inbox: opts.Inbox, kick: make(chan struct{}, 1), accounts: events.NewNotifier(),
		endedChats: map[string]bool{}, lookEvery: LookEvery, retryAfter: time.Second,
		chat: ChatStatus{State: ChatOff, Since: time.Now()},
	}
	s.chatConn = newChatConn(opts.TestServer)
	s.quota = &quota{settings: opts.Settings, now: func() time.Time { return s.now() }}
	s.data = newData(ep.api, s.quota)
	s.streamer = newAccount(s, RoleStreamer, StreamerScopes)
	s.bot = newAccount(s, RoleBot, BotScopes)
	return s
}

// Start loads saved sign-ins and checks them with Google in the
// background, then regularly. It never blocks the engine on the network.
func (s *Service) Start(ctx context.Context) {
	for _, a := range s.all() {
		a.load(ctx)
	}
	if c, _ := s.client(ctx); c.ID == "" {
		for _, a := range s.all() {
			a.mu.Lock()
			if a.status.State == StateSignedOut {
				a.setStatus(StateNotConfigured, "")
			}
			a.mu.Unlock()
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			for _, a := range s.all() {
				a.Validate(s.ctx)
			}
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(checkEvery):
			}
		}
	}()
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.runChat(s.ctx) }()
	go func() { defer s.wg.Done(); s.followTwitch(s.ctx) }()
}

// Close stops background work and pending sign-ins.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
	_ = s.chatConn.Close()
}

func (s *Service) all() []*Account { return []*Account{s.streamer, s.bot} }

// newChatConn makes the gRPC connection for chat streams: Google's, over
// TLS, or a loopback test server's without it. It connects when first used.
func newChatConn(test string) *grpc.ClientConn {
	target := "dns:///" + livechat.Host
	creds := credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	if test != "" {
		if u, err := url.Parse(test); err == nil {
			target = "passthrough:///" + u.Host
		}
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds), grpc.WithUserAgent("StreamGremlin"),
		// A ping every 2 minutes finds a connection that silently died.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 2 * time.Minute, Timeout: 20 * time.Second}))
	if err != nil {
		panic(fmt.Sprintf("youtube: chat connection: %v", err)) // only a malformed target, which this never makes
	}
	return conn
}

// Account returns the account for role.
func (s *Service) Account(role Role) (*Account, bool) {
	switch role {
	case RoleStreamer:
		return s.streamer, true
	case RoleBot:
		return s.bot, true
	}
	return nil, false
}

func (s *Service) other(role Role) *Account {
	if role == RoleBot {
		return s.streamer
	}
	return s.bot
}

// Changes is closed at the next change of any account's state.
func (s *Service) Changes() <-chan struct{} { return s.notify.C() }

func (s *Service) changed() { s.notify.Notify() }

// accountChanged is called when an account's state changes.
func (s *Service) accountChanged() {
	s.accounts.Notify()
	s.changed()
}

// client returns the Google project in use: the streamer's own wins over
// the built-in one.
func (s *Service) client(ctx context.Context) (client, ClientSource) {
	var saved savedSettings
	if ok, err := s.settings.Load(ctx, settingsKey, &saved); err == nil && ok && saved.ClientID != "" {
		c := client{ID: saved.ClientID}
		if v, err := s.secrets.Get(ctx, clientSecretKey); err == nil {
			c.Secret = v.Reveal()
		}
		return c, ClientOwn
	}
	if BuiltInClientID != "" {
		return client{ID: BuiltInClientID, Secret: BuiltInClientSecret}, ClientBuiltIn
	}
	return client{}, ClientNone
}

var (
	clientIDPattern     = regexp.MustCompile(`^[0-9]{6,30}-[a-z0-9]{10,64}\.apps\.googleusercontent\.com$`)
	clientSecretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,100}$`)
)

// SetClient saves the streamer's own Google project: its client ID and,
// if Google gave one, its client secret (kept in the secret store). An
// empty ID goes back to the built-in project. Sign-ins made with another
// client ID end at their next check, which runs now.
func (s *Service) SetClient(ctx context.Context, id string, secret secrets.Value) error {
	if id != "" && !clientIDPattern.MatchString(id) {
		return fmt.Errorf("%w: a client ID looks like 123456789012-abc…xyz.apps.googleusercontent.com; "+
			"copy it from your Google project's Credentials page", ErrInvalid)
	}
	if !secret.IsZero() && (id == "" || !clientSecretPattern.MatchString(secret.Reveal())) {
		return fmt.Errorf("%w: a client secret is 10 to 100 letters, digits, '-' and '_', such as GOCSPX-…", ErrInvalid)
	}
	if secret.IsZero() {
		if err := s.secrets.Delete(ctx, clientSecretKey); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return err
		}
	} else if err := s.secrets.Set(ctx, clientSecretKey, secret); err != nil {
		return err
	}
	if err := s.settings.Save(ctx, settingsKey, savedSettings{ClientID: id}); err != nil {
		return err
	}
	current, _ := s.client(ctx)
	for _, a := range s.all() {
		a.CancelSignIn()
		a.mu.Lock()
		switch {
		case current.ID == "" && a.tok == nil:
			a.setStatus(StateNotConfigured, "")
		case current.ID != "" && a.status.State == StateNotConfigured:
			a.setStatus(StateSignedOut, "")
		}
		a.mu.Unlock()
		a.Validate(ctx)
	}
	src := ClientBuiltIn
	if id != "" {
		src = ClientOwn
	}
	s.log.Info("youtube client changed", "source", string(src), "secret", !secret.IsZero())
	return nil
}

// Status is what the app shows on Connections. It never holds tokens or
// the client secret.
type Status struct {
	ClientID     string       `json:"client_id"`
	ClientSource ClientSource `json:"client_source"`
	HasSecret    bool         `json:"has_secret"`
	// HasBuiltIn says whether this build carries StreamGremlin's own project.
	HasBuiltIn bool `json:"has_built_in"`
	// TestServer is set when sign-in goes to a stand-in for Google, such as
	// mockyoutube, so a test is never mistaken for YouTube.
	TestServer string        `json:"test_server,omitempty"`
	Streamer   AccountStatus `json:"streamer"`
	Bot        AccountStatus `json:"bot"`
	Quota      QuotaStatus   `json:"quota"`
	Chat       ChatStatus    `json:"chat"`
	Manifest   Manifest      `json:"manifest"`
}

// Status returns both accounts' states and today's quota.
func (s *Service) Status(ctx context.Context) Status {
	c, src := s.client(ctx)
	return Status{
		ClientID: c.ID, ClientSource: src, HasSecret: c.Secret != "", HasBuiltIn: BuiltInClientID != "",
		TestServer: s.test, Streamer: s.streamer.Status(), Bot: s.bot.Status(), Quota: s.quota.status(ctx), Chat: s.ChatStatus(),
		Manifest: YouTubeManifest(),
	}
}
