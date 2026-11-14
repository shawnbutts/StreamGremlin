package filters

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/settings"
)

const settingsKey = "chat_filters"

var follow = events.Filter{"chat.message.received"}

// Moderator sends and records moderating requests (moderation.Service).
type Moderator interface {
	Do(ctx context.Context, r moderation.Request) (moderation.Record, bool, error)
}

// Options configure a Service.
type Options struct {
	Inbox      *events.Inbox
	Settings   *settings.Store
	Moderation Moderator
	// BotID returns the bot account's Twitch user id, or "": the bot is
	// never filtered.
	BotID func() string
	// YouTubeQuotaLeft is today's YouTube quota units left; nil counts
	// as none (D-100).
	YouTubeQuotaLeft func(context.Context) int
	Logger           *slog.Logger
	Now              func() time.Time
}

// Service watches chat and acts on what the filters catch.
type Service struct {
	o       Options
	log     *slog.Logger
	repeats *Repeats
	permits *Permits

	mu       sync.Mutex
	compiled *Compiled
	cursor   int64

	stop context.CancelFunc
	done chan struct{}
	once sync.Once
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.BotID == nil {
		o.BotID = func() string { return "" }
	}
	return &Service{o: o, log: o.Logger.With("component", "filters"), repeats: NewRepeats(), permits: NewPermits()}
}

// Settings returns the saved settings, or the defaults.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := Defaults()
	if _, err := s.o.Settings.Load(ctx, settingsKey, &st); err != nil {
		return Settings{}, err
	}
	return st, nil
}

// SetSettings checks and saves the settings. They apply to the next
// message.
func (s *Service) SetSettings(ctx context.Context, st Settings) (Settings, error) {
	c, err := Compile(st)
	if err != nil {
		return Settings{}, err
	}
	st = c.Settings()
	if err := s.o.Settings.Save(ctx, settingsKey, st); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	s.compiled = c
	s.mu.Unlock()
	s.log.Info("chat filters saved", "on", st.On, "words", len(st.Words.List))
	return st, nil
}

func (s *Service) current(ctx context.Context) (*Compiled, error) {
	s.mu.Lock()
	c := s.compiled
	s.mu.Unlock()
	if c != nil {
		return c, nil
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if c, err = Compile(st); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.compiled = c
	s.mu.Unlock()
	return c, nil
}

// Try says which filter would catch a message, with the saved settings
// (or st, when given), as if the filters were on. Repeats needs several
// messages, so it's never the answer.
func (s *Service) Try(ctx context.Context, st *Settings, m Message) (string, error) {
	var c *Compiled
	var err error
	if st != nil {
		c, err = Compile(*st)
	} else {
		c, err = s.current(ctx)
	}
	if err != nil {
		return "", err
	}
	on := c.Settings()
	on.On = true
	c2, err := Compile(on)
	if err != nil {
		return "", err
	}
	m.At, m.Permits = s.o.Now(), s.permits
	return c2.Judge(m, nil), nil
}

// Permit lets a viewer post links for d (!permit).
func (s *Service) Permit(platform, login string, d time.Duration) {
	s.permits.Allow(platform, login, d, s.o.Now())
	s.log.Info("a viewer may post links for a while", "login", login, "seconds", int(d.Seconds()))
}

// Recent returns what the filters caught lately, newest first.
func (s *Service) Recent(ctx context.Context, limit int) ([]events.Record, error) {
	return s.o.Inbox.Latest(ctx, limit, events.Filter{events.FilterCaught})
}

// Start follows chat from now on: messages from before are never judged.
func (s *Service) Start(ctx context.Context) error {
	head, err := s.o.Inbox.Head(ctx)
	if err != nil {
		return err
	}
	s.cursor = head
	if _, err := s.current(ctx); err != nil {
		s.log.Error("the chat filters' settings couldn't be read; filters are off until they're saved again", "err", err)
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			if err := s.catchUp(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("couldn't read chat; trying again", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			case <-s.o.Inbox.Notifier().C():
			}
		}
	}()
	return nil
}

// Close stops following. Closing twice is harmless.
func (s *Service) Close() {
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
			<-s.done
		}
	})
}

func (s *Service) catchUp(ctx context.Context) error {
	for {
		recs, err := s.o.Inbox.After(ctx, s.cursor, 200, follow)
		if err != nil {
			return err
		}
		for _, r := range recs {
			s.handle(ctx, r)
			s.cursor = r.Seq
		}
		if len(recs) < 200 {
			return nil
		}
	}
}

// chatPayload is what the filters read from a chat message.
type chatPayload struct {
	MessageID           string   `json:"message_id"`
	Text                string   `json:"text"`
	Emotes              []string `json:"emotes"`
	SourceBroadcasterID string   `json:"source_broadcaster_id"`
	Chatter             struct {
		ID            string `json:"id"`
		Login         string `json:"login"`
		Role          string `json:"role"`
		IsBroadcaster bool   `json:"is_broadcaster"`
		IsModerator   bool   `json:"is_moderator"`
		IsVIP         bool   `json:"is_vip"`
		IsSubscriber  bool   `json:"is_subscriber"`
	} `json:"chatter"`
}

var requestSafe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,50}$`)

// handle judges one chat message and acts on a catch.
func (s *Service) handle(ctx context.Context, r events.Record) {
	if events.Backlog(r) {
		return // from before StreamGremlin was reading
	}
	a := r.Actor
	if a == nil || a.ID == "" || r.Source == "core" {
		return
	}
	var p chatPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return
	}
	// Another channel's message in a shared chat isn't ours to moderate,
	// and the bot is never filtered.
	if (p.SourceBroadcasterID != "" && p.SourceBroadcasterID != r.ChannelID) || (r.ChannelID != "" && a.ID == r.ChannelID) {
		return
	}
	if bot := s.o.BotID(); bot != "" && a.ID == bot {
		return
	}
	c, err := s.current(ctx)
	if err != nil {
		return
	}
	m := Message{Platform: a.Platform, UserID: a.ID, Login: a.Login, Role: p.Chatter.Role, IsBroadcaster: p.Chatter.IsBroadcaster,
		IsModerator: p.Chatter.IsModerator, IsVIP: p.Chatter.IsVIP, IsSubscriber: p.Chatter.IsSubscriber, Text: p.Text,
		Emotes: p.Emotes, At: r.OccurredAt, Permits: s.permits}
	rule := c.Judge(m, s.repeats)
	if rule == "" {
		return
	}
	rs, _ := c.Settings().RuleFor(rule)
	action, seconds, why := rs.Action, rs.Seconds, ""
	if a.Platform == events.SourceYouTube {
		left := 0
		if s.o.YouTubeQuotaLeft != nil {
			left = s.o.YouTubeQuotaLeft(ctx)
		}
		action, seconds, why = rs.OnYouTube(left)
	}
	out := map[string]any{"rule": rule, "action": action, "seconds": seconds, "message_id": p.MessageID, "text": p.Text}
	if why != "" {
		out["note"] = why
	}
	var chatter map[string]any
	var raw map[string]json.RawMessage
	if json.Unmarshal(r.Payload, &raw) == nil {
		_ = json.Unmarshal(raw["chatter"], &chatter)
	}
	out["chatter"] = chatter

	if r.DeliveryQuality == events.Synthetic {
		// A test message: judged, but nothing reaches Twitch.
		out["state"] = "test"
	} else {
		id := p.MessageID
		if !requestSafe.MatchString(id) {
			id = r.EventID
		}
		req := moderation.Request{RequestID: "filter-" + id, Action: action, Platform: a.Platform, UserID: a.ID, Login: a.Login,
			Seconds: seconds, From: moderation.FromFilter}
		if action == ActionDelete {
			req.MessageID = p.MessageID
		} else {
			req.Reason = Reason(rule)
		}
		rec, _, err := s.o.Moderation.Do(ctx, req)
		if err != nil {
			s.log.Warn("a chat filter couldn't act", "rule", rule, "action", action, "user_id", a.ID, "err", err)
			out["state"], out["answer"] = moderation.Refused, err.Error()
		} else {
			out["state"], out["answer"], out["request_id"] = rec.State, rec.Answer, rec.RequestID
		}
		s.log.Info("a chat filter caught a message", "platform", a.Platform, "rule", rule, "action", action, "user_id", a.ID, "login", a.Login,
			"state", out["state"])
	}
	payload, _ := json.Marshal(out)
	src := r.SourceEventID
	if src == "" {
		src = r.EventID
	}
	env := events.Envelope{Type: events.FilterCaught, Source: r.Source, SourceEventID: "filter:" + src, OccurredAt: s.o.Now(),
		ChannelID: r.ChannelID, Actor: r.Actor, Payload: payload, CorrelationID: r.CorrelationID, CausationID: r.EventID,
		DeliveryQuality: r.DeliveryQuality}
	if _, err := s.o.Inbox.Accept(ctx, env); err != nil {
		s.log.Warn("couldn't store what a chat filter caught", "err", err)
	}
}
