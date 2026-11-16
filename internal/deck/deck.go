// Package deck is what a paired Stream Deck may use beyond buttons
// (D-123; docs/adr/0053-stream-deck-keys.md): counters (global variables
// it may show and change by one), switches (workflows, timers and the chat
// lock it may turn on and off) and the stream's status. The streamer
// chooses the counters and switches; nothing is allowed until then, and
// every request is checked against the choice as it is at that moment.
package deck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/timers"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// SettingsKey is where the choice is kept in the settings store.
const SettingsKey = "streamdeck"

// Limits.
const (
	MaxCounters = 20
	MaxSwitches = 20
	// RecentFor is how long a counter request's id is remembered, so a
	// retry after a lost answer doesn't count twice.
	RecentFor = 10 * time.Minute
	maxRecent = 1000
	maxID     = 64
)

// Switch kinds.
const (
	KindWorkflow = "workflow"
	KindTimer    = "timer"
	KindChatLock = "chat_lock"
)

// Counter changes.
const (
	Add   = "add"
	Take  = "take"
	Reset = "reset"
)

// Errors.
var (
	ErrInvalid = errors.New("deck: invalid")
	// ErrNotAllowed: the streamer hasn't chosen it for the Stream Deck.
	ErrNotAllowed = errors.New("deck: not chosen for the Stream Deck")
	// ErrNotNumber: a counter's variable holds something else.
	ErrNotNumber = errors.New("deck: the variable doesn't hold a number")
	// ErrGone: a switch's workflow or timer was deleted.
	ErrGone = errors.New("deck: it's gone")
	// ErrUnknown: whether it's on can't be known now.
	ErrUnknown = errors.New("deck: unknown")
	// ErrRefused: Twitch refused the chat lock or unlock.
	ErrRefused = errors.New("deck: refused")
)

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

// Switch is one switch a deck may use. ID is empty for the chat lock.
type Switch struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// Settings is what the streamer chose.
type Settings struct {
	Counters []string `json:"counters"`
	Switches []Switch `json:"switches"`
	// Runs are workflows a key may run now, whatever their trigger (D-124).
	Runs []string `json:"runs"`
	// Scenes: keys may switch OBS's scene.
	Scenes bool `json:"scenes"`
	// Countdowns are clock overlays (countdowns and stopwatches) keys may
	// start, pause and reset.
	Countdowns []string `json:"countdowns"`
	// Ads: keys may snooze Twitch's next automatic ad.
	Ads bool `json:"ads"`
}

// fill makes nil lists empty, as JSON shows them.
func (s *Settings) fill() {
	if s.Counters == nil {
		s.Counters = []string{}
	}
	if s.Switches == nil {
		s.Switches = []Switch{}
	}
	if s.Runs == nil {
		s.Runs = []string{}
	}
	if s.Countdowns == nil {
		s.Countdowns = []string{}
	}
}

// Check reports whether the choice is well formed.
func (s Settings) Check() error {
	if len(s.Counters) > MaxCounters {
		return invalid("up to %d counters", MaxCounters)
	}
	if len(s.Switches) > MaxSwitches {
		return invalid("up to %d switches", MaxSwitches)
	}
	if len(s.Runs) > MaxSwitches {
		return invalid("up to %d workflows to run", MaxSwitches)
	}
	if len(s.Countdowns) > MaxSwitches {
		return invalid("up to %d countdowns", MaxSwitches)
	}
	for _, list := range [][]string{s.Runs, s.Countdowns} {
		seen := map[string]bool{}
		for _, id := range list {
			if id == "" || len(id) > maxID {
				return invalid("a workflow or countdown needs its id")
			}
			if seen[id] {
				return invalid("%s is listed twice", id)
			}
			seen[id] = true
		}
	}
	seen := map[string]bool{}
	for _, n := range s.Counters {
		if !variables.NamePattern.MatchString(n) {
			return invalid("a counter is a variable name: %s", variables.NameHint)
		}
		if seen[n] {
			return invalid("%s is listed twice", n)
		}
		seen[n] = true
	}
	for _, sw := range s.Switches {
		switch sw.Kind {
		case KindWorkflow, KindTimer:
			if sw.ID == "" || len(sw.ID) > maxID {
				return invalid("a %s switch needs its id", sw.Kind)
			}
		case KindChatLock:
			if sw.ID != "" {
				return invalid("the chat lock switch has no id")
			}
		default:
			return invalid("a switch is a workflow, a timer or the chat lock")
		}
		key := sw.Kind + ":" + sw.ID
		if seen[key] {
			return invalid("a switch is listed twice")
		}
		seen[key] = true
	}
	return nil
}

func (s Settings) hasCounter(name string) bool {
	for _, n := range s.Counters {
		if n == name {
			return true
		}
	}
	return false
}

func has(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

func (s Settings) hasSwitch(kind, id string) bool {
	for _, sw := range s.Switches {
		if sw.Kind == kind && sw.ID == id {
			return true
		}
	}
	return false
}

// Status is the stream's state, as a status key shows it. Nil is unknown.
type Status struct {
	Live *bool `json:"live"`
	// Chat is Twitch chat's state: off, connecting, connected or
	// disconnected.
	Chat string `json:"chat"`
	// OBS is OBS's connection: off, connecting, connected, disconnected or
	// needs_action.
	OBS        string `json:"obs"`
	ChatLocked *bool  `json:"chat_locked"`
}

// Counter is a chosen counter and its value: a number, nil when it isn't
// set yet, or something else (shown as unknown).
type Counter struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// SwitchState is a chosen switch, its label and whether it's on (nil:
// unknown). Gone is set when its workflow or timer was deleted.
type SwitchState struct {
	Switch
	Label string `json:"label"`
	On    *bool  `json:"on"`
	Gone  bool   `json:"gone,omitempty"`
}

// View is everything a deck's keys show.
type View struct {
	Status     Status           `json:"status"`
	Counters   []Counter        `json:"counters"`
	Switches   []SwitchState    `json:"switches"`
	Runs       []RunState       `json:"runs"`
	Scenes     *Scenes          `json:"scenes"`
	Countdowns []CountdownState `json:"countdowns"`
	Ads        *Ads             `json:"ads"`
}

// Workflows is what switches need of workflows.
type Workflows interface {
	Get(ctx context.Context, id string) (workflow.Summary, error)
	SetEnabled(ctx context.Context, id string, on bool) error
}

// Timers is what switches need of timers.
type Timers interface {
	Get(ctx context.Context, id string) (timers.Timer, error)
	Save(ctx context.Context, t timers.Timer, now time.Time) (timers.Timer, bool, error)
	Changed()
}

// Locker is what the chat lock switch needs of moderation.
type Locker interface {
	Locked(ctx context.Context) (*moderation.Record, error)
	Do(ctx context.Context, r moderation.Request) (moderation.Record, bool, error)
}

// Options are the service's parts.
type Options struct {
	Settings  *settings.Store
	Variables *variables.Store
	Workflows Workflows
	Timers    Timers
	Lock      Locker
	// Live is whether you're live, ok false when unknown.
	Live func(ctx context.Context) (live, ok bool)
	// Chat is Twitch chat's state; OBS is OBS's.
	Chat func() string
	OBS  func() string
	// SignedIn is whether the streamer's Twitch account is signed in: the
	// chat lock is unknown otherwise.
	SignedIn func() bool
	// What run, scene, countdown and ad keys need (D-124); nil in tests
	// that don't use them.
	Inbox     Inbox
	Scener    Scener
	Overlays  OverlayStore
	Clocks    Clocks
	AdsSource AdsSource
	// NewID makes a moderation request id.
	NewID  func() string
	Now    func() time.Time
	Logger *slog.Logger
}

// Service answers a deck's keys.
type Service struct {
	o Options

	mu     sync.Mutex
	recent map[string]recent

	adMu     sync.Mutex
	adCache  *Ads
	adReadAt time.Time
}

type recent struct {
	value any
	at    time.Time
}

// New returns the service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	o.Logger = o.Logger.With("component", "deck")
	return &Service{o: o, recent: map[string]recent{}}
}

// Settings returns the choice; empty when nothing was chosen.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	if _, err := s.o.Settings.Load(ctx, SettingsKey, &out); err != nil {
		return Settings{}, err
	}
	out.fill()
	return out, nil
}

// SetSettings saves the choice.
func (s *Service) SetSettings(ctx context.Context, in Settings) (Settings, error) {
	in.fill()
	if err := in.Check(); err != nil {
		return Settings{}, err
	}
	if err := s.o.Settings.Save(ctx, SettingsKey, in); err != nil {
		return Settings{}, err
	}
	s.o.Logger.Info("stream deck choice saved", "counters", len(in.Counters), "switches", len(in.Switches), "runs", len(in.Runs),
		"scenes", in.Scenes, "countdowns", len(in.Countdowns), "ads", in.Ads)
	return in, nil
}

func (s *Service) status(ctx context.Context) Status {
	st := Status{Chat: s.o.Chat(), OBS: s.o.OBS()}
	if live, ok := s.o.Live(ctx); ok {
		st.Live = &live
	}
	if on, err := s.locked(ctx); err == nil {
		st.ChatLocked = &on
	}
	return st
}

// locked is whether chat is locked from StreamGremlin; ErrUnknown while
// the streamer's Twitch account isn't signed in.
func (s *Service) locked(ctx context.Context) (bool, error) {
	if !s.o.SignedIn() {
		return false, ErrUnknown
	}
	rec, err := s.o.Lock.Locked(ctx)
	if err != nil {
		return false, err
	}
	return rec != nil, nil
}

// View is what the keys show: the status, and each chosen counter and
// switch as it is now.
func (s *Service) View(ctx context.Context) (View, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return View{}, err
	}
	v := View{Status: s.status(ctx), Counters: []Counter{}, Switches: []SwitchState{}, Runs: []RunState{}, Countdowns: []CountdownState{}}
	for _, n := range set.Counters {
		c := Counter{Name: n}
		got, err := s.o.Variables.Get(ctx, variables.Key{Scope: variables.Global, Name: n})
		switch {
		case errors.Is(err, variables.ErrNotFound):
		case err != nil:
			return View{}, err
		default:
			c.Value = got.Value
		}
		v.Counters = append(v.Counters, c)
	}
	for _, sw := range set.Switches {
		st, err := s.switchState(ctx, sw)
		if err != nil {
			return View{}, err
		}
		v.Switches = append(v.Switches, st)
	}
	if err := s.more(ctx, set, &v); err != nil {
		return View{}, err
	}
	return v, nil
}

func yes(b bool) *bool { return &b }

func (s *Service) switchState(ctx context.Context, sw Switch) (SwitchState, error) {
	st := SwitchState{Switch: sw}
	switch sw.Kind {
	case KindWorkflow:
		w, err := s.o.Workflows.Get(ctx, sw.ID)
		if errors.Is(err, workflow.ErrNotFound) {
			st.Label, st.Gone = "Workflow gone", true
			return st, nil
		}
		if err != nil {
			return st, err
		}
		st.Label, st.On = w.Name, yes(w.Enabled)
	case KindTimer:
		t, err := s.o.Timers.Get(ctx, sw.ID)
		if errors.Is(err, timers.ErrNotFound) {
			st.Label, st.Gone = "Timer gone", true
			return st, nil
		}
		if err != nil {
			return st, err
		}
		st.Label, st.On = t.Name, yes(t.Enabled)
	case KindChatLock:
		st.Label = "Chat lock"
		on, err := s.locked(ctx)
		switch {
		case errors.Is(err, ErrUnknown):
		case err != nil:
			return st, err
		default:
			st.On = &on
		}
	}
	return st, nil
}

// Step changes a number by a counter change: add one, take one but never
// below 0, or reset to 0. Nil counts as 0. Integers stay integers.
func Step(prev any, change string) (any, error) {
	switch change {
	case Add, Take, Reset:
	default:
		return nil, invalid("a change is add, take or reset")
	}
	if prev == nil {
		prev = int64(0)
	}
	switch p := prev.(type) {
	case int64:
		switch change {
		case Add:
			if p == math.MaxInt64 {
				return nil, fmt.Errorf("%w: it's as big as it can be", ErrInvalid)
			}
			return p + 1, nil
		case Take:
			return max(p-1, 0), nil
		}
		return int64(0), nil
	case float64:
		switch change {
		case Add:
			return p + 1, nil
		case Take:
			return math.Max(p-1, 0), nil
		}
		return int64(0), nil
	}
	return nil, ErrNotNumber
}

// Count changes a chosen counter by one, as from client. The same
// request id within RecentFor answers the first result again.
func (s *Service) Count(ctx context.Context, name, change, requestID, client string) (Counter, error) {
	if requestID == "" || len(requestID) > maxID {
		return Counter{}, invalid("a request id is 1 to %d characters", maxID)
	}
	if _, err := Step(nil, change); err != nil {
		return Counter{}, err
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return Counter{}, err
	}
	if !set.hasCounter(name) {
		return Counter{}, ErrNotAllowed
	}
	key := client + "\x00" + requestID
	s.mu.Lock()
	defer s.mu.Unlock() // one count at a time: a retry waits for the first
	now := s.o.Now()
	for k, r := range s.recent {
		if now.Sub(r.at) > RecentFor {
			delete(s.recent, k)
		}
	}
	if r, ok := s.recent[key]; ok {
		return Counter{Name: name, Value: r.value}, nil
	}
	_, value, err := s.o.Variables.Update(ctx, variables.Key{Scope: variables.Global, Name: name},
		func(prev any) (any, error) { return Step(prev, change) }, variables.Change{By: "stream_deck:" + client})
	if err != nil {
		return Counter{}, err
	}
	if len(s.recent) >= maxRecent {
		clear(s.recent)
	}
	s.recent[key] = recent{value: value, at: now}
	s.o.Logger.Info("counter changed from the stream deck", "variable", name, "change", change, "client", client)
	return Counter{Name: name, Value: value}, nil
}

// Turn turns a chosen switch on or off, as from client, and returns it as
// it is then. Already as asked, nothing changes.
func (s *Service) Turn(ctx context.Context, kind, id string, on bool, client string) (SwitchState, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return SwitchState{}, err
	}
	if !set.hasSwitch(kind, id) {
		return SwitchState{}, ErrNotAllowed
	}
	sw := Switch{Kind: kind, ID: id}
	st, err := s.switchState(ctx, sw)
	if err != nil {
		return st, err
	}
	if st.Gone {
		return st, ErrGone
	}
	if st.On == nil {
		return st, ErrUnknown
	}
	if *st.On == on {
		return st, nil
	}
	switch kind {
	case KindWorkflow:
		if err := s.o.Workflows.SetEnabled(ctx, id, on); err != nil {
			return st, err
		}
	case KindTimer:
		t, err := s.o.Timers.Get(ctx, id)
		if err != nil {
			return st, err
		}
		t.Enabled = on
		if _, _, err := s.o.Timers.Save(ctx, t, s.o.Now()); err != nil {
			return st, err
		}
		s.o.Timers.Changed()
	case KindChatLock:
		action := moderation.Unlock
		if on {
			action = moderation.Lock
		}
		rec, _, err := s.o.Lock.Do(ctx, moderation.Request{RequestID: s.o.NewID(), Action: action, Platform: moderation.PlatformTwitch,
			From: moderation.FromDeck})
		if err != nil {
			return st, err
		}
		s.o.Logger.Info("chat lock from the stream deck", "on", on, "state", rec.State, "client", client)
		switch rec.State {
		case moderation.Done:
		case moderation.Refused:
			return st, fmt.Errorf("%w: %s", ErrRefused, rec.Answer)
		default:
			return st, fmt.Errorf("%w: %s", ErrUnknown, rec.Answer)
		}
	}
	if kind != KindChatLock {
		s.o.Logger.Info("switched from the stream deck", "kind", kind, "id", id, "on", on, "client", client)
	}
	return s.switchState(ctx, sw)
}
