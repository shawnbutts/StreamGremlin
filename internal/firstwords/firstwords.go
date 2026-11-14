// Package firstwords finds each viewer's first chat message of a stream
// and adds a chat.first_words.received event for it
// (docs/adr/0021-first-words.md). A stream starts over when OBS or Twitch
// says it went live, when the streamer resets it, or after 12 hours.
//
// It follows the stored chat, so it works for every platform and for test
// messages (judged apart from real ones). Nothing new is stored: on start
// it reads the last 12 hours of chat again.
package firstwords

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/settings"
)

// Type is the event first words arrive as.
const Type = "chat.first_words.received"

// Quiet is how long without a stream start counts as a new stream, as in
// Streamer.bot.
const Quiet = 12 * time.Hour

// lateLimit: after a restart, a message older than this gets no first words
// (it would greet someone long after they spoke).
const lateLimit = 5 * time.Minute

// What starts a stream over.
var starts = map[string]bool{"broadcast.stream.started": true, "broadcast.channel.online": true}

var follow = events.Filter{"chat.message.received", "broadcast.stream.started", "broadcast.channel.online"}

const settingsKey = "first_words"

type saved struct {
	ResetAt time.Time `json:"reset_at"`
}

// seen is a viewer's newest message.
type seen struct {
	at  time.Time
	seq int64
}

// state is one set of viewers: real chat, or test chat.
type state struct {
	last     map[string]seen // by platform:id
	startAt  time.Time       // the newest stream start, for the status
	startSeq int64           // and where it is in the inbox
	resetAt  time.Time       // the newest reset by hand
}

func newState() *state { return &state{last: map[string]seen{}} }

// since is when this stream began, for a message at t: going live, a
// reset, or 12 hours before.
func (st *state) since(t time.Time) time.Time {
	from := t.Add(-Quiet)
	for _, x := range []time.Time{st.startAt, st.resetAt} {
		if x.After(from) {
			from = x
		}
	}
	return from
}

// Options configure a Service.
type Options struct {
	Inbox    *events.Inbox
	Settings *settings.Store
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service finds first words.
type Service struct {
	inbox    *events.Inbox
	settings *settings.Store
	log      *slog.Logger
	now      func() time.Time

	mu         sync.Mutex
	real, test *state
	cursor     int64

	stop context.CancelFunc
	done chan struct{}
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{inbox: o.Inbox, settings: o.Settings, log: o.Logger.With("component", "firstwords"), now: o.Now,
		real: newState(), test: newState()}
}

// Start reads the last 12 hours of chat again, then follows new chat.
func (s *Service) Start(ctx context.Context) error {
	var sv saved
	if _, err := s.settings.Load(ctx, settingsKey, &sv); err != nil {
		return err
	}
	s.real.resetAt, s.test.resetAt = sv.ResetAt, sv.ResetAt
	// Arrival times are stamped by the inbox's clock (the real one), so the
	// place to start reading again is found with the real clock too.
	from, err := s.inbox.SeqBefore(ctx, time.Now().Add(-Quiet-time.Minute))
	if err != nil {
		return err
	}
	s.cursor = from
	if err := s.catchUp(ctx, time.Now()); err != nil {
		return err
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	s.done = make(chan struct{})
	go s.run(ctx)
	return nil
}

// Close stops following.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
}

// catchUp reads everything after the cursor that's already stored. Only
// messages that arrived shortly before started get first words now: older
// ones were judged before the restart, or are too old to greet.
func (s *Service) catchUp(ctx context.Context, started time.Time) error {
	for {
		recs, err := s.inbox.After(ctx, s.cursor, 500, follow)
		if err != nil {
			return err
		}
		for _, r := range recs {
			s.handle(ctx, r, started.Sub(r.ReceivedAt) <= lateLimit)
		}
		if len(recs) < 500 {
			return nil
		}
	}
}

func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	for ctx.Err() == nil {
		recs, err := s.inbox.Wait(ctx, s.cursor, 200, follow, 20*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("couldn't read chat; trying again", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, r := range recs {
			s.handle(ctx, r, true)
		}
	}
}

// handle judges one stored event, and stores first words when it finds
// them and emit allows.
func (s *Service) handle(ctx context.Context, r events.Record, emit bool) {
	s.mu.Lock()
	s.cursor = r.Seq
	if events.Backlog(r) {
		s.mu.Unlock()
		return
	}
	test := r.DeliveryQuality == events.Synthetic
	if starts[r.Type] && r.Source == events.SourceYouTube {
		s.mu.Unlock()
		return // YouTube going live doesn't start a stream over yet (D-099)
	}
	if starts[r.Type] {
		// A real start starts both over; a test start only test chat. It's
		// placed by its order in the inbox, not its time: a message stored
		// before it, even in the same millisecond, was before it.
		if !test {
			s.real.startAt, s.real.startSeq = r.OccurredAt, r.Seq
		}
		s.test.startAt, s.test.startSeq = r.OccurredAt, r.Seq
		s.mu.Unlock()
		return
	}
	st := s.real
	if test {
		st = s.test
	}
	first := s.judgeLocked(st, r)
	s.mu.Unlock()
	if !first || !emit {
		return
	}
	env := events.Envelope{
		Type: Type, Source: r.Source, SourceEventID: "first:" + sourceID(r), OccurredAt: r.OccurredAt,
		ChannelID: r.ChannelID, Actor: r.Actor, Payload: r.Payload, CorrelationID: r.CorrelationID, CausationID: r.EventID,
		DeliveryQuality: r.DeliveryQuality,
	}
	if _, err := s.inbox.Accept(ctx, env); err != nil {
		s.log.Warn("couldn't store first words", "err", err)
	}
}

func sourceID(r events.Record) string {
	if r.SourceEventID != "" {
		return r.SourceEventID
	}
	return r.EventID
}

// judgeLocked says whether a chat message is its viewer's first of the
// stream, and remembers it. The streamer's own messages (which include what
// StreamGremlin sends as them) never count; the bot's are never stored.
func (s *Service) judgeLocked(st *state, r events.Record) bool {
	if r.Actor == nil || r.Actor.ID == "" {
		return false
	}
	var p struct {
		Chatter struct {
			Role string `json:"role"`
		} `json:"chatter"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	if p.Chatter.Role == "broadcaster" {
		return false
	}
	key := r.Actor.Platform + ":" + r.Actor.ID
	prev, ok := st.last[key]
	st.last[key] = seen{at: r.OccurredAt, seq: r.Seq}
	if len(st.last) > 20000 {
		cut := r.OccurredAt.Add(-Quiet)
		for k, x := range st.last {
			if x.at.Before(cut) {
				delete(st.last, k)
			}
		}
	}
	// The start of the stream by time (a reset, 12 quiet hours), but going
	// live by order.
	from := r.OccurredAt.Add(-Quiet)
	if st.resetAt.After(from) {
		from = st.resetAt
	}
	return !ok || prev.seq < st.startSeq || prev.at.Before(from)
}

// Status is where the current stream's first words stand.
type Status struct {
	// Since is when this stream began, for first words.
	Since time.Time `json:"since"`
	// Why: "stream start", "reset" or "12 hours".
	Why string `json:"why"`
	// Viewers who've chatted since.
	Viewers int `json:"viewers"`
}

// Status reports the real chat's current stream.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	since := s.real.since(now)
	why := "12 hours"
	switch {
	case !since.After(now.Add(-Quiet)):
	case since.Equal(s.real.resetAt):
		why = "reset"
	default:
		why = "stream start"
	}
	n := 0
	for _, x := range s.real.last {
		if x.seq > s.real.startSeq && !x.at.Before(since) {
			n++
		}
	}
	return Status{Since: since, Why: why, Viewers: n}
}

// Reset starts first words over now: everyone's next message counts.
// It's saved, so a restart keeps it.
func (s *Service) Reset(ctx context.Context) (Status, error) {
	at := s.now()
	if err := s.settings.Save(ctx, settingsKey, saved{ResetAt: at}); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	s.real.resetAt, s.test.resetAt = at, at
	s.mu.Unlock()
	s.log.Info("first words reset")
	return s.Status(), nil
}
