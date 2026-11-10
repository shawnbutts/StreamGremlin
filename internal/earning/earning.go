// Package earning pays points for taking part (docs/adr/0033-earning-rules.md):
// chatting (with a wait between awards per viewer), a first follow (once
// per viewer, ever), subscribing and resubscribing by tier, gifting subs
// (to the gifter), and cheering bits. It reads the inbox in order with its
// own saved cursor, which starts at the newest event the first time, so
// turning earning on never pays for what happened before. Every award is a
// ledger credit with an operation id made from the event, so reading the
// same events again never pays twice. Only real events from a platform
// count: never test events, and never the streamer's own.
package earning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// Limits.
const (
	MaxAward = 100_000   // in one rule
	MaxWait  = 24 * 3600 // seconds between chat awards
	// By is who the ledger says paid.
	By          = "earning rules"
	settingsKey = "earning"
)

// ErrInvalid: the rules don't make sense.
var ErrInvalid = errors.New("earning: invalid")

// Chat is the rule for chatting.
type Chat struct {
	Amount int64 `json:"amount"`
	// EverySeconds is the least time between one viewer's chat awards.
	EverySeconds int `json:"every_seconds"`
	// LiveOnly: chat earns only while you're known to be live.
	LiveOnly bool `json:"live_only"`
}

// Subs is what a sub or resub earns, by tier.
type Subs struct {
	Tier1 int64 `json:"tier1"`
	Tier2 int64 `json:"tier2"`
	Tier3 int64 `json:"tier3"`
}

// Time is the rule for time in chat (docs/adr/0034-time-in-chat.md): each
// interval while you're live, viewers who chatted within ActiveMinutes earn
// Active, and viewers on Twitch's chatter list who didn't earn Quiet.
type Time struct {
	On            bool  `json:"on"`
	EveryMinutes  int   `json:"every_minutes"`
	Active        int64 `json:"active"`
	ActiveMinutes int   `json:"active_minutes"`
	Quiet         int64 `json:"quiet"`
}

// Rules is how viewers earn. An amount of 0 turns that rule off.
type Rules struct {
	On     bool  `json:"on"`
	Chat   Chat  `json:"chat"`
	Time   Time  `json:"time"`
	Follow int64 `json:"follow"` // once per viewer, ever
	Sub    Subs  `json:"sub"`    // gifted subs earn for the gifter, not the recipient
	Gift   int64 `json:"gift"`   // per sub gifted
	Bits   int64 `json:"bits"`   // per 100 bits
}

// Defaults are the rules before the streamer changes them: off.
func Defaults() Rules {
	return Rules{Chat: Chat{Amount: 1, EverySeconds: 60, LiveOnly: true}, Time: Time{EveryMinutes: 5, Active: 10, ActiveMinutes: 10, Quiet: 3}, Follow: 50, Sub: Subs{500, 1000, 2500}, Gift: 250, Bits: 10}
}

// Check says what's wrong with the rules, if anything.
func (r Rules) Check() error {
	for _, a := range []struct {
		name string
		n    int64
	}{{"chatting", r.Chat.Amount}, {"time in chat, chatting,", r.Time.Active}, {"time in chat, quiet,", r.Time.Quiet}, {"a follow", r.Follow}, {"a tier 1 sub", r.Sub.Tier1}, {"a tier 2 sub", r.Sub.Tier2},
		{"a tier 3 sub", r.Sub.Tier3}, {"a gifted sub", r.Gift}, {"100 bits", r.Bits}} {
		if a.n < 0 || a.n > MaxAward {
			return fmt.Errorf("%w: %s earns 0 to %d", ErrInvalid, a.name, MaxAward)
		}
	}
	if r.Time.EveryMinutes < 1 || r.Time.EveryMinutes > 60 {
		return fmt.Errorf("%w: time in chat pays every 1 to 60 minutes", ErrInvalid)
	}
	if r.Time.ActiveMinutes < 1 || r.Time.ActiveMinutes > 120 {
		return fmt.Errorf("%w: chatting counts for 1 to 120 minutes", ErrInvalid)
	}
	if r.Chat.EverySeconds < 0 || r.Chat.EverySeconds > MaxWait {
		return fmt.Errorf("%w: the wait between chat awards is 0 to %d seconds", ErrInvalid, MaxWait)
	}
	return nil
}

// Live states.
const (
	Live    = "live"
	Offline = "offline"
	Unknown = "unknown"
)

var liveTypes = map[string]bool{"broadcast.stream.started": true, "broadcast.channel.online": true,
	"broadcast.stream.stopped": false, "broadcast.channel.offline": false}

var follow = events.Filter{"chat.message.received", "audience.follow.created", "monetization.subscription.started",
	"monetization.subscription.renewed", "monetization.subscription.gifted", "monetization.bits.cheered",
	"broadcast.stream.started", "broadcast.channel.online", "broadcast.stream.stopped", "broadcast.channel.offline"}

// Options are what a Service needs.
type Options struct {
	DB       *storage.DB
	Inbox    *events.Inbox
	Settings *settings.Store
	Viewers  *viewers.Service
	Points   *points.Ledger
	// Chatters is Twitch's chatter list; nil when there's no Twitch.
	Chatters ChatterList
	Logger   *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// ChatterList says who's connected to chat, apart from the streamer and
// their bot.
type ChatterList interface {
	Chatters(ctx context.Context) ([]events.Actor, error)
}

// Service pays points by the rules.
type Service struct {
	o   Options
	log *slog.Logger

	minute time.Duration // a real minute, except in tests

	mu        sync.Mutex // one reader of the inbox at a time; guards live
	live      string     // as of the cursor
	liveKnown bool       // live has been read

	stop context.CancelFunc
	done chan struct{}
	once sync.Once
}

// New returns a Service. Call Start to follow the inbox.
func New(o Options) *Service {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, log: log.With("component", "earning"), live: Unknown, minute: time.Minute}
}

// Rules returns the rules (the defaults, off, until saved).
func (s *Service) Rules(ctx context.Context) (Rules, error) {
	r := Defaults()
	_, err := s.o.Settings.Load(ctx, settingsKey, &r)
	return r, err
}

// SetRules saves the rules. They apply from the next event on.
func (s *Service) SetRules(ctx context.Context, r Rules) (Rules, error) {
	if err := r.Check(); err != nil {
		return Rules{}, err
	}
	if err := s.o.Settings.Save(ctx, settingsKey, r); err != nil {
		return Rules{}, err
	}
	s.log.Info("earning rules saved", "on", r.On)
	return r, nil
}

// SetMinute shortens time in chat's minute, for tests. Call before Start.
func (s *Service) SetMinute(d time.Duration) { s.minute = d }

// LiveState is whether you're live as far as earning has read: live,
// offline, or unknown when nothing has said.
func (s *Service) LiveState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live
}

// Start catches up with the inbox, then follows it.
func (s *Service) Start(ctx context.Context) error {
	if err := s.CatchUp(ctx); err != nil {
		return err
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		// The interval that had begun at the start is never paid: who was in
		// chat before is unknown.
		var last time.Time
		if r, err := s.Rules(ctx); err == nil {
			last = s.o.Now().Truncate(time.Duration(r.Time.EveryMinutes) * s.minute)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			case <-s.o.Inbox.Notifier().C():
			}
			if err := s.CatchUp(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("couldn't pay points by the rules", "err", err)
			}
			last = s.maybeTick(ctx, last)
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

// cursor returns where earning has read to; the first time, the newest
// event, so nothing before it pays.
func (s *Service) cursor(ctx context.Context) (int64, error) {
	var seq int64
	err := s.o.DB.QueryRowContext(ctx, "SELECT seq FROM earning_cursor WHERE id = 1").Scan(&seq)
	if !errors.Is(err, sql.ErrNoRows) {
		return seq, err
	}
	head, err := s.o.Inbox.Head(ctx)
	if err != nil {
		return 0, err
	}
	if _, err := s.o.DB.ExecContext(ctx, "INSERT INTO earning_cursor (id, seq) VALUES (1, ?) ON CONFLICT (id) DO NOTHING", head); err != nil {
		return 0, err
	}
	return s.cursor(ctx)
}

// CatchUp pays for the events since the cursor, in order. The cursor moves
// past each event once it's handled; an event read again pays nothing more.
func (s *Service) CatchUp(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, err := s.cursor(ctx)
	if err != nil {
		return err
	}
	if !s.liveKnown {
		types := make([]string, 0, len(liveTypes))
		for t := range liveTypes {
			types = append(types, t)
		}
		rec, ok, err := s.o.Inbox.LatestRealUpTo(ctx, at, types...)
		if err != nil {
			return err
		}
		if ok {
			s.live = map[bool]string{true: Live, false: Offline}[liveTypes[rec.Type]]
		}
		s.liveKnown = true
	}
	rules, err := s.Rules(ctx)
	if err != nil {
		return err
	}
	for {
		recs, err := s.o.Inbox.After(ctx, at, 200, follow)
		if err != nil || len(recs) == 0 {
			return err
		}
		for _, r := range recs {
			if err := s.handle(ctx, rules, r); err != nil {
				return err
			}
			if _, err := s.o.DB.ExecContext(ctx, "UPDATE earning_cursor SET seq = ? WHERE id = 1", r.Seq); err != nil {
				return err
			}
			at = r.Seq
		}
		if len(recs) < 200 {
			return nil
		}
	}
}

// award is what one event earns.
type award struct {
	op     string
	amount int64
	memo   string
}

func (s *Service) handle(ctx context.Context, rules Rules, r events.Record) error {
	if on, ok := liveTypes[r.Type]; ok {
		if r.DeliveryQuality != events.Synthetic {
			s.live = map[bool]string{true: Live, false: Offline}[on]
		}
		return nil
	}
	a := r.Actor
	if r.DeliveryQuality != events.Confirmed || r.Source == "core" || a == nil || a.ID == "" || a.Platform == "" ||
		(r.ChannelID != "" && a.ID == r.ChannelID) {
		return nil
	}
	var p struct {
		Tier    int  `json:"tier"`
		IsGift  bool `json:"is_gift"`
		Total   int  `json:"total"`
		Bits    int  `json:"bits"`
		Chatter struct {
			Role string `json:"role"`
		} `json:"chatter"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	if p.Chatter.Role == "broadcaster" {
		return nil
	}
	if r.Type == "chat.message.received" {
		// Who chatted when, for time in chat, even while earning is off.
		name := a.DisplayName
		if name == "" {
			name = a.Login
		}
		if _, err := s.o.DB.ExecContext(ctx, `INSERT INTO earning_active (platform, user_id, login, display_name, last_chat_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (platform, user_id) DO UPDATE SET login = excluded.login,
			display_name = excluded.display_name, last_chat_at = max(last_chat_at, excluded.last_chat_at)`,
			a.Platform, a.ID, a.Login, name, storage.Timestamp(r.ReceivedAt)); err != nil {
			return err
		}
	}
	if !rules.On {
		return nil
	}
	tier := func() int64 {
		switch p.Tier {
		case 2:
			return rules.Sub.Tier2
		case 3:
			return rules.Sub.Tier3
		}
		return rules.Sub.Tier1
	}
	tierNo := max(p.Tier, 1)
	var aw award
	switch r.Type {
	case "chat.message.received":
		if rules.Chat.LiveOnly && s.live != Live {
			return nil
		}
		aw = award{"earn:chat:" + r.EventID, rules.Chat.Amount, "chatting"}
	case "audience.follow.created":
		aw = award{"", rules.Follow, "following"} // the op is per viewer, below
	case "monetization.subscription.started":
		if p.IsGift {
			return nil // the gifter earns
		}
		aw = award{"earn:sub:" + r.EventID, tier(), fmt.Sprintf("subscribing (tier %d)", tierNo)}
	case "monetization.subscription.renewed":
		aw = award{"earn:sub:" + r.EventID, tier(), fmt.Sprintf("resubscribing (tier %d)", tierNo)}
	case "monetization.subscription.gifted":
		n := int64(max(p.Total, 1))
		aw = award{"earn:gift:" + r.EventID, rules.Gift * n, fmt.Sprintf("gifting %d %s", n, plural(n, "sub", "subs"))}
	case "monetization.bits.cheered":
		aw = award{"earn:bits:" + r.EventID, rules.Bits * int64(p.Bits/100), fmt.Sprintf("cheering %d bits", p.Bits)}
	default:
		return nil
	}
	if aw.amount <= 0 {
		return nil
	}
	aw.amount = min(aw.amount, points.MaxAmount)
	v, err := s.o.Viewers.Ensure(ctx, *a)
	if err != nil {
		return err
	}
	if aw.op == "" {
		aw.op = "earn:follow:" + v.ID
	}
	if r.Type == "chat.message.received" {
		ok, err := s.chatDue(ctx, v.ID, r.ReceivedAt, rules.Chat.EverySeconds)
		if err != nil || !ok {
			return err
		}
	}
	res, err := s.o.Points.Credit(ctx, points.Op{ID: aw.op, Viewer: v.ID, Amount: aw.amount, Memo: aw.memo, By: By})
	switch {
	case errors.Is(err, points.ErrConflict):
		// Paid before at another amount (the rule changed since): kept as it was.
		s.log.Debug("already earned", "op", aw.op)
	case errors.Is(err, points.ErrInvalid):
		s.log.Warn("couldn't pay points", "op", aw.op, "err", err)
	case err != nil:
		return err
	case res.Applied:
		s.log.Debug("earned", "viewer", v.ID, "amount", aw.amount, "for", aw.memo)
	}
	if r.Type == "chat.message.received" {
		_, err := s.o.DB.ExecContext(ctx, `INSERT INTO earning_chat (viewer, last_at) VALUES (?, ?)
			ON CONFLICT (viewer) DO UPDATE SET last_at = max(last_at, excluded.last_at)`, v.ID, storage.Timestamp(r.ReceivedAt))
		return err
	}
	return nil
}

// chatDue says whether a viewer's chat at t is far enough after their last
// chat award.
func (s *Service) chatDue(ctx context.Context, viewer string, t time.Time, every int) (bool, error) {
	if every == 0 {
		return true, nil // every message; a message read again is still paid once
	}
	var last string
	err := s.o.DB.QueryRowContext(ctx, "SELECT last_at FROM earning_chat WHERE viewer = ?", viewer).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	// An unreadable time doesn't hold anyone back.
	if prev, err := storage.ParseTimestamp(last); err == nil && t.Sub(prev) < time.Duration(every)*time.Second {
		return false, nil
	}
	return true, nil
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
