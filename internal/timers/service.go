package timers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Inbox is what timers need of the event inbox.
type Inbox interface {
	Head(ctx context.Context) (int64, error)
	CountViewerChat(ctx context.Context, after int64) (int, error)
	LatestReal(ctx context.Context, types ...string) (events.Record, bool, error)
	Accept(ctx context.Context, env events.Envelope) (events.Result, error)
}

// Options configure a Service.
type Options struct {
	DB     *storage.DB
	Inbox  Inbox
	Logger *slog.Logger
	Now    func() time.Time
	// Tick is how often due timers are looked at (default 1 second).
	Tick time.Duration
	// Random returns a number in [0, n); default crypto/rand.
	Random func(n int64) int64
}

// Service keeps timers and makes them go off.
type Service struct {
	*Store
	inbox Inbox
	log   *slog.Logger
	now   func() time.Time
	tick  time.Duration
	rnd   func(n int64) int64

	mu    sync.Mutex
	slots map[string]*slot // enabled timers' schedules, in memory
	wake  chan struct{}
	stop  context.CancelFunc
	done  chan struct{}
}

// slot is one enabled timer's place in the schedule. A restart, a change
// or turning it on starts a new slot, so missed turns never fire in a burst.
type slot struct {
	rev   time.Time // the timer's updated_at when the slot began
	due   time.Time // zero: never (a schedule that matches no date)
	since int64     // chat lines count after this event seq
	lines int       // chat lines counted while due
}

// New returns a Service. Start runs it.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Tick == 0 {
		opts.Tick = time.Second
	}
	if opts.Random == nil {
		opts.Random = func(n int64) int64 {
			v, err := rand.Int(rand.Reader, big.NewInt(n))
			if err != nil {
				return 0
			}
			return v.Int64()
		}
	}
	return &Service{Store: &Store{db: opts.DB}, inbox: opts.Inbox, log: opts.Logger.With("component", "timers"),
		now: opts.Now, tick: opts.Tick, rnd: opts.Random, slots: map[string]*slot{}, wake: make(chan struct{}, 1)}
}

// Start runs the schedule until Close.
func (s *Service) Start(ctx context.Context) {
	ctx, s.stop = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(s.tick)
		defer t.Stop()
		for {
			s.RunDue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.wake:
			}
		}
	}()
}

// Close stops the schedule.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
}

// Changed asks the schedule to look again now, after a save or a change.
func (s *Service) Changed() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// next is when t goes off next, counting from now.
func (s *Service) next(t Timer, now time.Time) time.Time {
	if t.Cron != "" {
		c, err := ParseCron(t.Cron)
		if err != nil {
			return time.Time{}
		}
		return c.Next(now.Local())
	}
	d := time.Duration(t.EverySeconds) * time.Second
	if t.UpToSeconds > t.EverySeconds {
		d += time.Duration(s.rnd(int64(t.UpToSeconds-t.EverySeconds)+1)) * time.Second
	}
	return now.Add(d)
}

// Live reports whether OBS last said it's streaming: the newest real
// stream started or stopped event. ok is false when OBS never said.
func (s *Service) Live(ctx context.Context) (live, ok bool) {
	rec, found, err := s.inbox.LatestReal(ctx, "broadcast.stream.started", "broadcast.stream.stopped")
	if err != nil || !found {
		return false, false
	}
	return rec.Type == "broadcast.stream.started", true
}

// RunDue makes due timers go off. The loop calls it every tick.
func (s *Service) RunDue(ctx context.Context) {
	list, err := s.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("couldn't read timers", "err", err)
		}
		return
	}
	now := s.now()
	head, err := s.inbox.Head(ctx)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	on := map[string]bool{}
	var live, liveKnown, liveRead bool
	for _, t := range list {
		if !t.Enabled {
			continue
		}
		on[t.ID] = true
		sl := s.slots[t.ID]
		if sl == nil || !sl.rev.Equal(t.UpdatedAt) {
			s.slots[t.ID] = &slot{rev: t.UpdatedAt, due: s.next(t, now), since: head}
			continue
		}
		if sl.due.IsZero() || now.Before(sl.due) {
			continue
		}
		if t.LiveOnly {
			if !liveRead {
				live, liveKnown = s.Live(ctx)
				liveRead = true
			}
			if !live || !liveKnown {
				// Its turn passes: waiting for the stream to start would fire
				// every live-only timer at once when it did.
				sl.due = s.next(t, now)
				continue
			}
		}
		if t.ChatLines > 0 {
			n, err := s.inbox.CountViewerChat(ctx, sl.since)
			if err != nil {
				continue
			}
			sl.lines = n
			if n < t.ChatLines {
				continue // it stays due, and goes off once chat catches up
			}
		}
		fired, _, err := s.fire(ctx, t.ID, false, now)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("a timer couldn't go off", "timer", t.ID, "err", err)
			}
			sl.due = s.next(t, now) // try at its next turn, not every second
			continue
		}
		if !fired.Enabled {
			delete(s.slots, t.ID)
			continue
		}
		sl.rev, sl.lines = fired.UpdatedAt, 0
		sl.due = s.next(t, now)
		if h, err := s.inbox.Head(ctx); err == nil {
			sl.since = h
		}
	}
	for id := range s.slots {
		if !on[id] {
			delete(s.slots, id)
		}
	}
}

// Fire makes a timer go off now (Run now): its workflows run for real, its
// count goes up, and its schedule is unchanged.
func (s *Service) Fire(ctx context.Context, id string) (Timer, events.Record, error) {
	return s.fire(ctx, id, true, s.now())
}

func (s *Service) fire(ctx context.Context, id string, manual bool, now time.Time) (Timer, events.Record, error) {
	t, err := s.countFire(ctx, id, manual, now)
	if err != nil {
		return Timer{}, events.Record{}, err
	}
	payload := map[string]any{"timer_id": t.ID, "name": t.Name, "counter": t.Counter, "manual": manual, "chat_lines": t.ChatLines}
	if t.Cron != "" {
		payload["cron"] = t.Cron
	} else {
		payload["every_seconds"] = t.EverySeconds
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Timer{}, events.Record{}, err
	}
	res, err := s.inbox.Accept(ctx, events.Envelope{
		Type: events.TimerFired, Source: "timers",
		// The count and when the timer was made name this turn: a timer
		// deleted and made again under the same id starts a new series.
		SourceEventID:   t.ID + ":" + strconv.FormatInt(t.CreatedAt.UnixMilli(), 36) + ":" + strconv.FormatInt(t.Counter, 10),
		OccurredAt:      now,
		Payload:         raw,
		DeliveryQuality: events.Confirmed,
	})
	if err != nil {
		return Timer{}, events.Record{}, fmt.Errorf("timers: record that %s went off: %w", t.ID, err)
	}
	s.log.Info("timer went off", "timer", t.ID, "counter", t.Counter, "manual", manual, "turned_off", t.Once && !manual)
	return t, res.Record, nil
}

// Status is a timer with where its schedule stands.
type Status struct {
	Timer
	// NextAt is when its time comes; nil when it's off or never comes.
	NextAt *time.Time `json:"next_at,omitempty"`
	// Waiting: "" (counting down), "chat" (its time came; waiting for
	// chat lines), "off" or "never" (a schedule with no date).
	Waiting string `json:"waiting,omitempty"`
	// Lines is how many chat lines it has seen while waiting for them.
	Lines int `json:"lines,omitempty"`
}

// Statuses returns every timer and where its schedule stands.
func (s *Service) Statuses(ctx context.Context) ([]Status, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(list))
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, t := range list {
		out = append(out, s.status(t, now))
	}
	return out, nil
}

// StatusOf returns one timer's status.
func (s *Service) StatusOf(ctx context.Context, id string) (Status, error) {
	t, err := s.Get(ctx, id)
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status(t, s.now()), nil
}

func (s *Service) status(t Timer, now time.Time) Status {
	st := Status{Timer: t}
	if !t.Enabled {
		st.Waiting = "off"
		return st
	}
	sl := s.slots[t.ID]
	if sl == nil || !sl.rev.Equal(t.UpdatedAt) {
		// Not scheduled yet (just saved): show the turn it will get.
		due := s.next(Timer{Cron: t.Cron, EverySeconds: t.EverySeconds}, now)
		if due.IsZero() {
			st.Waiting = "never"
		} else {
			st.NextAt = &due
		}
		return st
	}
	if sl.due.IsZero() {
		st.Waiting = "never"
		return st
	}
	due := sl.due
	st.NextAt = &due
	if t.ChatLines > 0 && !now.Before(sl.due) {
		st.Waiting, st.Lines = "chat", sl.lines
	}
	return st
}
