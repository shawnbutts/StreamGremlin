package twitch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/chat"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
)

// BuiltInClientID is StreamGremlin's registered Twitch app, set at build
// time (-ldflags -X ...BuiltInClientID=...). It's never committed (D-131 in
// StreamWisp). Development builds leave it empty and use a pasted one.
var BuiltInClientID = ""

// ErrNoClientID means neither a built-in nor a pasted Client ID exists.
var ErrNoClientID = errors.New("twitch: no Client ID; paste one from dev.twitch.tv/console, or use an official build")

// ErrInvalid wraps bad input, so the API can answer 400.
var ErrInvalid = errors.New("twitch: invalid")

// Scopes each account asks for. Only what M1 uses (chat, slice 5); later
// milestones add theirs, and the app asks the streamer to sign in again.
var (
	StreamerScopes = []string{"user:read:chat", "user:write:chat",
		// Channel events (docs/adr/0015-twitch-channel-events.md).
		"moderator:read:followers", "channel:read:subscriptions", "bits:read", "channel:read:redemptions",
		// Making and changing rewards, and completing or refunding their
		// redemptions (docs/adr/0018-channel-point-rewards.md).
		ManageScope,
		// Hype Trains (docs/adr/0022-hype-trains.md).
		"channel:read:hype_train",
		// Polls and predictions: read, start and end them (docs/adr/0023-polls-and-predictions.md).
		"channel:manage:polls", "channel:manage:predictions",
		// Ads: the schedule, starting a break and snoozing (docs/adr/0024-ads.md).
		AdsReadScope, CommercialScope, AdsManageScope,
		// Time in chat: who's connected to chat (docs/adr/0034-time-in-chat.md).
		ChattersScope,
		// The Live screen's viewer card: moderators, bans and VIPs (D-078).
		ModerationReadScope, VIPsReadScope,
		// Moderating from chat, chat modes and suspicious users (D-079).
		BansScope, ChatDeleteScope, WarningsScope, ShoutoutsScope, VIPsManageScope, ModsManageScope,
		ChatSettingsScope, SuspiciousReadScope, SuspiciousManage,
		// The title and category, markers, and clips (D-086).
		BroadcastScope, ClipsScope,
		// Twitch's Blocked Terms, from Chat filters (D-088).
		BlockedTermsScope,
		// Shield Mode and locking chat (D-105).
		ShieldScope}
	BotScopes = []string{"user:write:chat", "user:read:moderated_channels"}
)

// validateEvery is Twitch's required validation interval.
const validateEvery = time.Hour

const settingsKey = "twitch"

type savedSettings struct {
	ClientID string `json:"client_id,omitempty"`
}

// ClientIDSource says where the Client ID in use comes from.
type ClientIDSource string

const (
	ClientIDNone    ClientIDSource = "none"
	ClientIDBuiltIn ClientIDSource = "built_in"
	ClientIDPasted  ClientIDSource = "pasted"
)

// Options configure a Service.
type Options struct {
	Secrets  *secrets.Store
	Settings *settings.Store
	// AuthURL is Twitch's OAuth server; tests and mocktwitch change it.
	// Anything but the default is a test server that also serves /helix and
	// the EventSub WebSocket at /ws.
	AuthURL string
	// Inbox receives chat. Nil keeps chat off.
	Inbox  *events.Inbox
	Logger *slog.Logger
}

// Service owns the streamer's and the bot's sign-ins.
type Service struct {
	secrets     *secrets.Store
	settings    *settings.Store
	oauth       *oauth
	helix       *helix
	authURL     string
	eventSubURL string
	inbox       *events.Inbox
	limits      limiter
	own         chat.OwnSends // workflows' messages sent as you, to skip their echo
	pictures    pictureCache

	chatMu sync.Mutex
	// madeCache caches when accounts were made (bans.go).
	madeCache accountCache
	chat      ChatStatus
	channel   ChannelInfo // the channel's title and category
	// clipChecks are the waits before each look for a new clip; nil is
	// Twitch's minute (tests shorten it).
	clipChecks []time.Duration
	log        *slog.Logger
	now        func() time.Time

	ctx    context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup
	notify *events.Notifier

	streamer, bot *Account

	// pollEvery overrides Twitch's polling interval, for tests.
	pollEvery time.Duration
}

// New returns a Service. Call Start to load saved sign-ins.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.AuthURL == "" {
		opts.AuthURL = DefaultAuthURL
	}
	ctx, stop := context.WithCancel(context.Background())
	helixURL, eventSubURL := DefaultHelixURL, DefaultEventSubURL
	if opts.AuthURL != DefaultAuthURL {
		helixURL = strings.TrimRight(opts.AuthURL, "/") + "/helix"
		eventSubURL = "ws" + strings.TrimPrefix(strings.TrimRight(opts.AuthURL, "/"), "http") + "/ws"
	}
	s := &Service{
		secrets: opts.Secrets, settings: opts.Settings, oauth: newOAuth(opts.AuthURL), authURL: opts.AuthURL,
		helix: newHelix(helixURL), eventSubURL: eventSubURL, inbox: opts.Inbox,
		log: opts.Logger.With("component", "twitch"), now: time.Now, ctx: ctx, stop: stop,
		notify: events.NewNotifier(),
	}
	s.chat = ChatStatus{State: ChatOff, Since: s.now()}
	s.streamer = newAccount(s, RoleStreamer, StreamerScopes)
	s.bot = newAccount(s, RoleBot, BotScopes)
	return s
}

// Start loads saved sign-ins and checks them with Twitch in the background,
// then hourly. It never blocks the engine's start on the network.
func (s *Service) Start(ctx context.Context) {
	for _, a := range s.accounts() {
		a.load(ctx)
	}
	if id, _ := s.ClientID(ctx); id == "" {
		for _, a := range s.accounts() {
			a.mu.Lock()
			if a.status.State == StateSignedOut {
				a.setStatus(StateNotConfigured, "")
			}
			a.mu.Unlock()
		}
	}
	if s.inbox != nil {
		s.wg.Add(1)
		go s.runChat()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			for _, a := range s.accounts() {
				a.Validate(s.ctx)
			}
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(validateEvery):
			}
		}
	}()
}

// Close stops background work and pending sign-ins.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

func (s *Service) accounts() []*Account { return []*Account{s.streamer, s.bot} }

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

// Changes is closed at the next change of any account's state. Chat
// (slice 5) uses it to stop when a sign-in ends.
func (s *Service) Changes() <-chan struct{} { return s.notify.C() }

func (s *Service) changed() { s.notify.Notify() }

// ClientID returns the Client ID in use: a pasted one wins over the
// built-in one, so a streamer can use their own app.
func (s *Service) ClientID(ctx context.Context) (string, ClientIDSource) {
	var saved savedSettings
	if ok, err := s.settings.Load(ctx, settingsKey, &saved); err == nil && ok && saved.ClientID != "" {
		return saved.ClientID, ClientIDPasted
	}
	if BuiltInClientID != "" {
		return BuiltInClientID, ClientIDBuiltIn
	}
	return "", ClientIDNone
}

var clientIDPattern = regexp.MustCompile(`^[a-z0-9]{10,64}$`)

// SetClientID saves a pasted Client ID; empty goes back to the built-in
// one. Sign-ins made with another Client ID end at their next check, which
// runs now.
func (s *Service) SetClientID(ctx context.Context, id string) error {
	if id != "" && !clientIDPattern.MatchString(id) {
		return fmt.Errorf("%w: a Client ID is 10 to 64 lowercase letters and digits, from dev.twitch.tv/console", ErrInvalid)
	}
	if err := s.settings.Save(ctx, settingsKey, savedSettings{ClientID: id}); err != nil {
		return err
	}
	current, _ := s.ClientID(ctx)
	for _, a := range s.accounts() {
		a.CancelSignIn()
		a.mu.Lock()
		switch {
		case current == "" && a.tok == nil:
			a.setStatus(StateNotConfigured, "")
		case current != "" && a.status.State == StateNotConfigured:
			a.setStatus(StateSignedOut, "")
		}
		a.mu.Unlock()
		a.Validate(ctx)
	}
	s.log.Info("twitch client id changed", "source", string(map[bool]ClientIDSource{true: ClientIDPasted, false: ClientIDBuiltIn}[id != ""]))
	return nil
}

// Status is what the app shows on the Connections screen.
type Status struct {
	ClientID       string         `json:"client_id"`
	ClientIDSource ClientIDSource `json:"client_id_source"`
	// HasBuiltIn says whether this build carries StreamGremlin's own Client ID.
	HasBuiltIn bool `json:"has_built_in"`
	// TestServer is set when sign-in goes to a stand-in for Twitch, such as
	// mocktwitch. The app says so, so a test is never mistaken for Twitch.
	TestServer string        `json:"test_server,omitempty"`
	Streamer   AccountStatus `json:"streamer"`
	Bot        AccountStatus `json:"bot"`
	Chat       ChatStatus    `json:"chat"`
	// Channel is the title and category, when Twitch has said.
	Channel  *ChannelInfo `json:"channel,omitempty"`
	Manifest Manifest     `json:"manifest"`
}

// Status returns both accounts' states.
func (s *Service) Status(ctx context.Context) Status {
	id, src := s.ClientID(ctx)
	st := Status{ClientID: id, ClientIDSource: src, HasBuiltIn: BuiltInClientID != "", Streamer: s.streamer.Status(), Bot: s.bot.Status(), Chat: s.ChatStatus(), Manifest: TwitchManifest()}
	if s.authURL != DefaultAuthURL {
		st.TestServer = s.authURL
	}
	if ch := s.Channel(); ch.Known {
		st.Channel = &ch
	}
	return st
}
