// Package raidguard notices a hate raid as it starts (M8 slice 3, D-107):
// a burst of chatters StreamGremlin has never seen, or of Twitch accounts
// made only days ago, within a short time, watched more closely for a
// while after a raid arrives. It warns on Live and starts "A raid looks
// suspicious"; it locks chat only when the streamer chose that, and never
// unlocks on its own (D-104). Mods, VIPs, subscribers, you, and viewers
// StreamGremlin already knows never count.
package raidguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// TypeSuspected is the event for a raid that looks suspicious.
const TypeSuspected = "moderation.raid.suspected"

// Source is the raid guard's events' source.
const Source = "streamgremlin"

// Modes.
const (
	ModeOff   = "off"
	ModeAlert = "alert" // warn on Live and start workflows (the default)
	ModeLock  = "lock"  // and lock chat
)

// Reasons.
const (
	ReasonFirstTime   = "first_time"
	ReasonNewAccounts = "new_accounts"
)

// Limits on the settings.
const (
	MinCount, MaxCount       = 2, 100
	MinWindow, MaxWindow     = 10, 600
	MinDays, MaxDays         = 1, 365
	MaxAfterRaid             = 30
	Cooldown                 = 5 * time.Minute
	AlertShows               = 15 * time.Minute
	MaxListed                = 100
	settingsKey              = "raidguard"
	knownGrace               = 3 * time.Second // a viewer first seen this close to their message is new
	maxAgeCache              = 20_000
	defaultFirst, defaultNew = 8, 5
)

// ErrInvalid wraps bad settings.
var ErrInvalid = errors.New("raidguard: invalid")

// Settings are the raid guard's choices.
type Settings struct {
	Mode string `json:"mode"`
	// FirstTime: this many chatters never seen before within Window
	// seconds is a burst.
	FirstTime int `json:"first_time"`
	// NewAccounts: this many Twitch accounts younger than Days days within
	// Window seconds is a burst.
	NewAccounts int `json:"new_accounts"`
	Days        int `json:"days"`
	Window      int `json:"window_seconds"`
	// AfterRaid: for this many minutes after a raid arrives, half the
	// counts (at least 2) are a burst. 0: no closer watch.
	AfterRaid int `json:"after_raid_minutes"`
}

// Defaults are the settings until the streamer changes them.
func Defaults() Settings {
	return Settings{Mode: ModeAlert, FirstTime: defaultFirst, NewAccounts: defaultNew, Days: 7, Window: 30, AfterRaid: 2}
}

// Check says what's wrong with settings.
func Check(s Settings) error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }
	switch {
	case s.Mode != ModeOff && s.Mode != ModeAlert && s.Mode != ModeLock:
		return bad("mode is off, alert or lock")
	case s.FirstTime < MinCount || s.FirstTime > MaxCount || s.NewAccounts < MinCount || s.NewAccounts > MaxCount:
		return bad("a count is %d to %d", MinCount, MaxCount)
	case s.Window < MinWindow || s.Window > MaxWindow:
		return bad("the time is %d to %d seconds", MinWindow, MaxWindow)
	case s.Days < MinDays || s.Days > MaxDays:
		return bad("an account's age is %d to %d days", MinDays, MaxDays)
	case s.AfterRaid < 0 || s.AfterRaid > MaxAfterRaid:
		return bad("the closer watch after a raid is 0 to %d minutes", MaxAfterRaid)
	}
	return nil
}

// Inbox is what the raid guard needs of the event inbox.
type Inbox interface {
	Accept(ctx context.Context, env events.Envelope) (events.Result, error)
	Head(ctx context.Context) (int64, error)
	Wait(ctx context.Context, after int64, limit int, f events.Filter, wait time.Duration) ([]events.Record, error)
}

// Settings are kept with the app's settings.
type Store interface {
	Load(ctx context.Context, key string, v any) (bool, error)
	Save(ctx context.Context, key string, v any) error
}

// Options configure a Service.
type Options struct {
	Inbox    Inbox
	Settings Store
	// Known says when StreamGremlin first saw an account, if it has.
	Known func(ctx context.Context, platform, userID string) (time.Time, bool, error)
	// Users looks Twitch accounts up (for their age); nil skips ages.
	Users func(ctx context.Context, logins, ids []string) ([]twitch.User, twitch.RateInfo, error)
	// Lock locks chat (a lock in the moderation record); nil can't lock.
	Lock   func(ctx context.Context) (state, answer string)
	Logger *slog.Logger
	Now    func() time.Time
}

// Account is one account in a burst.
type Account struct {
	Platform    string    `json:"platform"`
	ID          string    `json:"id"`
	Login       string    `json:"login"`
	DisplayName string    `json:"display_name"`
	At          time.Time `json:"at"`
	// AgeDays is how old the Twitch account is, when known.
	AgeDays *int `json:"age_days,omitempty"`
}

// Alert is what Live shows while a burst is recent.
type Alert struct {
	At        time.Time `json:"at"`
	Reason    string    `json:"reason"`
	Count     int       `json:"count"`
	Window    int       `json:"window_seconds"`
	AfterRaid string    `json:"after_raid,omitempty"` // the raider, when it came right after a raid
	Accounts  []Account `json:"accounts"`
	// Locked says whether the guard locked chat, and Lock how it went.
	Locked bool   `json:"locked"`
	Lock   string `json:"lock,omitempty"`
	// Test is a test alert: nothing happened.
	Test bool `json:"test,omitempty"`
}

// Service watches chat.
type Service struct {
	o    Options
	log  *slog.Logger
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu        sync.Mutex
	settings  Settings
	seen      []Account            // first-time chatters within the window
	young     []Account            // new accounts within the window
	counted   map[string]bool      // platform:id counted already
	ages      map[string]time.Time // Twitch id -> made
	lastAlert time.Time
	raidAt    time.Time
	raidFrom  string
	alert     *Alert
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, log: o.Logger.With("component", "raidguard"), settings: Defaults(), counted: map[string]bool{},
		ages: map[string]time.Time{}}
}

// Start loads the settings and follows chat from now.
func (s *Service) Start(ctx context.Context) error {
	var st Settings
	if ok, err := s.o.Settings.Load(ctx, settingsKey, &st); err != nil {
		return err
	} else if ok && Check(st) == nil {
		s.settings = st
	}
	cursor, err := s.o.Inbox.Head(ctx)
	if err != nil {
		return err
	}
	rctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stop = stop
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.follow(rctx, cursor) }()
	return nil
}

// Close stops. Closing twice is harmless.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
	}
	s.wg.Wait()
}

// Settings returns the settings.
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings checks, saves and uses new settings.
func (s *Service) SetSettings(ctx context.Context, st Settings) (Settings, error) {
	if err := Check(st); err != nil {
		return Settings{}, err
	}
	if err := s.o.Settings.Save(ctx, settingsKey, st); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	s.log.Info("raid guard settings", "mode", st.Mode, "first_time", st.FirstTime, "new_accounts", st.NewAccounts, "days", st.Days,
		"window", st.Window)
	return st, nil
}

// Alert returns the alert to show, or nil: the newest, until it's
// dismissed or AlertShows has passed.
func (s *Service) Alert() *Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alert == nil || s.o.Now().Sub(s.alert.At) > AlertShows {
		return nil
	}
	a := *s.alert
	a.Accounts = append([]Account(nil), s.alert.Accounts...)
	return &a
}

// Dismiss hides the alert.
func (s *Service) Dismiss() {
	s.mu.Lock()
	s.alert = nil
	s.mu.Unlock()
}

var follow = events.Filter{"chat.message.received", "audience.raid.received"}

func (s *Service) follow(ctx context.Context, cursor int64) {
	for ctx.Err() == nil {
		recs, err := s.o.Inbox.Wait(ctx, cursor, 200, follow, 30*time.Second)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, r := range recs {
			cursor = r.Seq
			s.note(ctx, r)
		}
	}
}

type chatPayload struct {
	Backlog bool `json:"backlog"`
	Chatter struct {
		Role          string `json:"role"`
		IsBroadcaster bool   `json:"is_broadcaster"`
		IsModerator   bool   `json:"is_moderator"`
		IsVIP         bool   `json:"is_vip"`
		IsSubscriber  bool   `json:"is_subscriber"`
	} `json:"chatter"`
}

// note looks at one event: a raid starts the closer watch; a chat message
// from an account never seen, or made days ago, counts toward a burst.
func (s *Service) note(ctx context.Context, r events.Record) {
	if r.DeliveryQuality == events.Synthetic {
		return
	}
	st := s.Settings()
	if st.Mode == ModeOff {
		return
	}
	if r.Type == "audience.raid.received" {
		s.mu.Lock()
		s.raidAt, s.raidFrom = r.OccurredAt, ""
		if r.Actor != nil {
			s.raidFrom = r.Actor.DisplayName
		}
		s.mu.Unlock()
		return
	}
	if r.Actor == nil || r.Actor.ID == "" {
		return
	}
	var p chatPayload
	_ = json.Unmarshal(r.Payload, &p)
	c := p.Chatter
	if p.Backlog || c.IsBroadcaster || c.IsModerator || c.IsVIP || c.IsSubscriber {
		return
	}
	platform := r.Actor.Platform
	if platform == "" {
		platform = r.Source
	}
	key := platform + ":" + r.Actor.ID
	s.mu.Lock()
	done := s.counted[key]
	s.mu.Unlock()
	if done {
		return
	}
	if s.o.Known != nil {
		first, ok, err := s.o.Known(ctx, platform, r.Actor.ID)
		if err != nil {
			return
		}
		if ok && first.Before(r.OccurredAt.Add(-knownGrace)) {
			return // a viewer StreamGremlin already knew
		}
	}
	a := Account{Platform: platform, ID: r.Actor.ID, Login: r.Actor.Login, DisplayName: r.Actor.DisplayName, At: r.OccurredAt}
	young := false
	if platform == "twitch" {
		if made, ok := s.age(ctx, r.Actor.ID); ok {
			days := int(r.OccurredAt.Sub(made).Hours() / 24)
			a.AgeDays = &days
			young = days < st.Days
		}
	}
	s.mu.Lock()
	s.counted[key] = true
	s.seen = append(s.seen, a)
	if young {
		s.young = append(s.young, a)
	}
	alert := s.judgeLocked(st, r.OccurredAt)
	s.mu.Unlock()
	if alert != nil {
		s.raise(ctx, alert)
	}
}

// age is when a Twitch account was made, asked of Twitch once.
func (s *Service) age(ctx context.Context, id string) (time.Time, bool) {
	s.mu.Lock()
	t, ok := s.ages[id]
	s.mu.Unlock()
	if ok {
		return t, true
	}
	if s.o.Users == nil {
		return time.Time{}, false
	}
	users, _, err := s.o.Users(ctx, nil, []string{id})
	if err != nil || len(users) == 0 || users[0].CreatedAt.IsZero() {
		return time.Time{}, false
	}
	s.mu.Lock()
	if len(s.ages) >= maxAgeCache {
		s.ages = map[string]time.Time{}
	}
	s.ages[id] = users[0].CreatedAt
	s.mu.Unlock()
	return users[0].CreatedAt, true
}

func trim(list []Account, since time.Time) []Account {
	out := list[:0]
	for _, a := range list {
		if !a.At.Before(since) {
			out = append(out, a)
		}
	}
	return out
}

// judgeLocked drops what's out of the window and says whether there's a
// burst; nil when not, or within the cooldown. Call with mu held.
func (s *Service) judgeLocked(st Settings, now time.Time) *Alert {
	window := time.Duration(st.Window) * time.Second
	since := now.Add(-window)
	s.seen, s.young = trim(s.seen, since), trim(s.young, since)
	first, young := st.FirstTime, st.NewAccounts
	afterRaid := ""
	if st.AfterRaid > 0 && !s.raidAt.IsZero() && now.Sub(s.raidAt) <= time.Duration(st.AfterRaid)*time.Minute {
		first, young, afterRaid = max(MinCount, first/2), max(MinCount, young/2), s.raidFrom
		if afterRaid == "" {
			afterRaid = "a raid"
		}
	}
	var reason string
	var list []Account
	switch {
	case len(s.young) >= young:
		reason, list = ReasonNewAccounts, s.young
	case len(s.seen) >= first:
		reason, list = ReasonFirstTime, s.seen
	default:
		return nil
	}
	if !s.lastAlert.IsZero() && now.Sub(s.lastAlert) < Cooldown {
		return nil
	}
	s.lastAlert = now
	a := &Alert{At: now, Reason: reason, Count: len(list), Window: st.Window, AfterRaid: afterRaid}
	for i, x := range list {
		if i == MaxListed {
			break
		}
		a.Accounts = append(a.Accounts, x)
	}
	// Counted once: the next burst is new accounts after this one.
	s.seen, s.young = nil, nil
	return a
}

// raise shows the alert, locks chat when chosen, and stores the event.
func (s *Service) raise(ctx context.Context, a *Alert) {
	if s.Settings().Mode == ModeLock && s.o.Lock != nil && !a.Test {
		state, answer := s.o.Lock(context.WithoutCancel(ctx))
		a.Locked, a.Lock = state == "done", answer
	}
	s.mu.Lock()
	s.alert = a
	s.mu.Unlock()
	names := make([]string, 0, len(a.Accounts))
	for _, x := range a.Accounts {
		names = append(names, x.DisplayName)
	}
	raw, _ := json.Marshal(map[string]any{"reason": a.Reason, "count": a.Count, "window_seconds": a.Window, "after_raid": a.AfterRaid,
		"accounts": a.Accounts, "names": strings.Join(names, ", "), "locked": a.Locked, "lock": a.Lock})
	q := events.Confirmed
	if a.Test {
		q = events.Synthetic
	}
	env := events.Envelope{EventID: events.NewID(), Type: TypeSuspected, Source: Source,
		SourceEventID: fmt.Sprintf("raid:%d", a.At.UnixNano()), OccurredAt: a.At, Payload: raw, DeliveryQuality: q}
	env.CorrelationID = env.EventID
	if _, err := s.o.Inbox.Accept(context.WithoutCancel(ctx), env); err != nil {
		s.log.Warn("couldn't store the raid alert", "err", err)
	}
	s.log.Warn("a raid looks suspicious", "reason", a.Reason, "count", a.Count, "locked", a.Locked, "test", a.Test)
}

// Test shows a test alert on Live and stores it as a test event: nothing
// is locked and no workflow runs outside a test.
func (s *Service) Test(ctx context.Context) Alert {
	now := s.o.Now()
	st := s.Settings()
	a := &Alert{At: now, Reason: ReasonNewAccounts, Count: 3, Window: st.Window, Test: true}
	for i, n := range []string{"TestRaider1", "TestRaider2", "TestRaider3"} {
		age := i
		a.Accounts = append(a.Accounts, Account{Platform: "twitch", ID: "test-" + fmt.Sprint(i), Login: strings.ToLower(n), DisplayName: n,
			At: now, AgeDays: &age})
	}
	s.raise(ctx, a)
	return *a
}

// KnownFrom says when the viewers service first saw an account.
func KnownFrom(v *viewers.Service) func(ctx context.Context, platform, userID string) (time.Time, bool, error) {
	return func(ctx context.Context, platform, userID string) (time.Time, bool, error) {
		x, err := v.ByAccount(ctx, platform, userID)
		if errors.Is(err, viewers.ErrNotFound) {
			return time.Time{}, false, nil
		}
		if err != nil {
			return time.Time{}, false, err
		}
		return x.FirstSeen, true, nil
	}
}
