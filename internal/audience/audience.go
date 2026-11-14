// Package audience says who's around while you're live (M6 slice 7,
// D-102): every few minutes, the people who spoke in chat lately, on
// Twitch and YouTube ("active chatters"), and each minute Twitch's viewer
// count when it changes enough. Nothing here claims anyone is watching:
// neither platform says who is (AGENTS.md: never invent passive presence).
// YouTube's viewer count comes from the youtube package's own check.
package audience

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Event types.
const (
	// TypeActive lists who spoke in chat lately, every ActiveEvery while
	// you're live and someone has.
	TypeActive = "chat.chatters.active"
	// TypeViewers is the viewer count changing (as youtube.TypeViewers).
	TypeViewers = "broadcast.viewers.changed"
	// Source is this package's events' source.
	Source = "streamgremlin"
)

// Timing.
const (
	ActiveEvery  = 5 * time.Minute
	ActiveWindow = 10 * time.Minute
	ViewersEvery = time.Minute
	// MaxListed is the most chatters an event lists by name.
	MaxListed = 100
)

// Inbox is what audience needs of the event inbox.
type Inbox interface {
	Accept(ctx context.Context, env events.Envelope) (events.Result, error)
	SeqBefore(ctx context.Context, t time.Time) (int64, error)
	Wait(ctx context.Context, after int64, limit int, f events.Filter, wait time.Duration) ([]events.Record, error)
}

// Options configure a Service.
type Options struct {
	Inbox Inbox
	// TwitchStream says whether you're live on Twitch, and the viewer
	// count. Nil when Twitch isn't part of this build.
	TwitchStream func(ctx context.Context) (live bool, viewers int, err error)
	// YouTubeLive says whether a YouTube broadcast's chat is being read.
	YouTubeLive func() bool
	Logger      *slog.Logger
	Now         func() time.Time
	// Every replaces ActiveEvery and ViewersEvery in tests.
	ActiveEvery, ViewersEvery time.Duration
}

// Chatter is someone who spoke lately.
type Chatter struct {
	Platform    string    `json:"platform"`
	ID          string    `json:"id"`
	Login       string    `json:"login"`
	DisplayName string    `json:"display_name"`
	LastSpoke   time.Time `json:"last_spoke"`
}

// Service follows chat and asks Twitch for its viewer count.
type Service struct {
	o    Options
	log  *slog.Logger
	mu   sync.Mutex
	seen map[string]Chatter // by platform:id
	// twitchLive is the last answer from TwitchStream.
	twitchLive bool
	announced  *int // Twitch's viewer count last announced
	stop       context.CancelFunc
	wg         sync.WaitGroup
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ActiveEvery == 0 {
		o.ActiveEvery = ActiveEvery
	}
	if o.ViewersEvery == 0 {
		o.ViewersEvery = ViewersEvery
	}
	return &Service{o: o, log: o.Logger.With("component", "audience"), seen: map[string]Chatter{}}
}

// Start follows chat from ActiveWindow ago, so a restart remembers who
// spoke lately, then runs the checks in the background.
func (s *Service) Start(ctx context.Context) error {
	cursor, err := s.o.Inbox.SeqBefore(ctx, s.o.Now().Add(-ActiveWindow))
	if err != nil {
		return err
	}
	rctx, stop := context.WithCancel(context.Background())
	s.stop = stop
	s.wg.Add(3)
	go func() { defer s.wg.Done(); s.follow(rctx, cursor) }()
	go func() { defer s.wg.Done(); s.every(rctx, s.o.ActiveEvery, s.announceActive) }()
	go func() { defer s.wg.Done(); s.every(rctx, s.o.ViewersEvery, s.checkTwitch) }()
	return nil
}

// Close stops. Closing twice is harmless.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
	}
	s.wg.Wait()
}

func (s *Service) every(ctx context.Context, d time.Duration, f func(context.Context)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		f(ctx)
	}
}

var chatTypes = events.Filter{"chat.message.received"}

func (s *Service) follow(ctx context.Context, cursor int64) {
	for ctx.Err() == nil {
		recs, err := s.o.Inbox.Wait(ctx, cursor, 200, chatTypes, 30*time.Second)
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
			s.note(r)
		}
	}
}

// note remembers a viewer's message: never a test event, history from
// before StreamGremlin was reading, or you.
func (s *Service) note(r events.Record) {
	if r.Actor == nil || r.Actor.ID == "" || r.DeliveryQuality == events.Synthetic {
		return
	}
	var p struct {
		Backlog bool `json:"backlog"`
		Chatter struct {
			IsBroadcaster bool `json:"is_broadcaster"`
		} `json:"chatter"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	if p.Backlog || p.Chatter.IsBroadcaster {
		return
	}
	platform := r.Actor.Platform
	if platform == "" {
		platform = r.Source
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := platform + ":" + r.Actor.ID
	if c, ok := s.seen[key]; ok && c.LastSpoke.After(r.OccurredAt) {
		return
	}
	s.seen[key] = Chatter{Platform: platform, ID: r.Actor.ID, Login: r.Actor.Login, DisplayName: r.Actor.DisplayName, LastSpoke: r.OccurredAt}
}

// Active lists who spoke within ActiveWindow, most recent first.
func (s *Service) Active() []Chatter {
	since := s.o.Now().Add(-ActiveWindow)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Chatter{}
	for k, c := range s.seen {
		if c.LastSpoke.Before(since) {
			delete(s.seen, k)
			continue
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Chatter) int { return b.LastSpoke.Compare(a.LastSpoke) })
	return out
}

func (s *Service) live() bool {
	s.mu.Lock()
	tw := s.twitchLive
	s.mu.Unlock()
	return tw || (s.o.YouTubeLive != nil && s.o.YouTubeLive())
}

// announceActive stores who spoke lately, while you're live and someone
// has.
func (s *Service) announceActive(ctx context.Context) {
	if !s.live() {
		return
	}
	list := s.Active()
	if len(list) == 0 {
		return
	}
	now := s.o.Now()
	chatters := []map[string]any{}
	names := []string{}
	counts := map[string]int{}
	for i, c := range list {
		counts[c.Platform]++
		if i >= MaxListed {
			continue
		}
		chatters = append(chatters, map[string]any{"platform": c.Platform, "id": c.ID, "login": c.Login, "display_name": c.DisplayName,
			"minutes_ago": int(now.Sub(c.LastSpoke) / time.Minute)})
		names = append(names, c.DisplayName)
	}
	raw, _ := json.Marshal(map[string]any{"chatters": chatters, "count": len(list), "twitch_count": counts["twitch"],
		"youtube_count": counts[events.SourceYouTube], "names": strings.Join(names, ", "), "minutes": int(ActiveWindow / time.Minute)})
	env := events.Envelope{EventID: events.NewID(), Type: TypeActive, Source: Source,
		SourceEventID: fmt.Sprintf("active:%d", now.Truncate(time.Second).Unix()), OccurredAt: now, Payload: raw,
		DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	if _, err := s.o.Inbox.Accept(ctx, env); err != nil && ctx.Err() == nil {
		s.log.Warn("couldn't store active chatters", "err", err)
	}
}

// ViewersChanged reports whether a count moved enough since the last one
// announced: by a tenth, and by at least one (as YouTube's).
func ViewersChanged(last, now int) bool {
	d := now - last
	if d < 0 {
		d = -d
	}
	return d >= max(1, last/10)
}

// checkTwitch asks Twitch whether you're live and how many watch, and
// stores the count when it changed enough. The first count of a stream is
// learned, not announced.
func (s *Service) checkTwitch(ctx context.Context) {
	if s.o.TwitchStream == nil {
		return
	}
	live, viewers, err := s.o.TwitchStream(ctx)
	if err != nil {
		return // signed out, or Twitch couldn't be asked: the count is unknown
	}
	s.mu.Lock()
	s.twitchLive = live
	if !live {
		s.announced = nil
		s.mu.Unlock()
		return
	}
	if s.announced == nil {
		s.announced = &viewers
		s.mu.Unlock()
		return
	}
	prev := *s.announced
	if !ViewersChanged(prev, viewers) {
		s.mu.Unlock()
		return
	}
	s.announced = &viewers
	s.mu.Unlock()
	now := s.o.Now()
	raw, _ := json.Marshal(map[string]any{"viewers": viewers, "previous": prev, "change": viewers - prev})
	env := events.Envelope{EventID: events.NewID(), Type: TypeViewers, Source: "twitch",
		SourceEventID: fmt.Sprintf("viewers:%d", now.UnixNano()), OccurredAt: now, Payload: raw, DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	if _, err := s.o.Inbox.Accept(ctx, env); err != nil && ctx.Err() == nil {
		s.log.Warn("couldn't store the viewer count", "err", err)
	}
}

// TwitchLive reports whether the last check found you live on Twitch.
func (s *Service) TwitchLive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.twitchLive
}
