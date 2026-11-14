package earning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Time in chat (docs/adr/0034-time-in-chat.md): at the end of each interval
// while you're live, viewers who chatted recently earn, and quiet viewers
// on Twitch's chatter list earn less. Intervals missed while StreamGremlin
// was closed (or the computer slept) are never paid: who was there then
// can't be known.

// keepTicks is how long ticks are kept.
const keepTicks = 30 * 24 * time.Hour

// Tick is one time-in-chat interval's end.
type Tick struct {
	At     time.Time `json:"at"`
	Active int       `json:"active"` // paid for chatting
	Quiet  int       `json:"quiet"`  // paid for being in chat
	// Note says why no one, or only chatters, were paid.
	Note string `json:"note,omitempty"`
}

// Notes.
const (
	NoteOff         = "time in chat is off"
	NoteNotLive     = "you weren't live"
	NoteUnknownLive = "it was unknown whether you were live"
)

// maybeTick pays the interval that just ended, once, if it's not late.
// It returns the newest interval end seen.
func (s *Service) maybeTick(ctx context.Context, last time.Time) time.Time {
	rules, err := s.Rules(ctx)
	if err != nil {
		return last
	}
	now := s.o.Now()
	end := now.Truncate(time.Duration(rules.Time.EveryMinutes) * s.minute)
	if !end.After(last) {
		return last
	}
	if now.Sub(end) > s.minute { // more than a minute late
		s.log.Info("a time-in-chat interval wasn't paid: it ended while StreamGremlin wasn't running or the computer slept", "at", end)
		return end
	}
	if _, err := s.Tick(ctx, end); err != nil && ctx.Err() == nil {
		s.log.Warn("couldn't pay time in chat", "at", end, "err", err)
	}
	return end
}

// Tick pays the interval ending at end: chat is read up to now first. A
// tick already paid is answered as it was.
func (s *Service) Tick(ctx context.Context, end time.Time) (Tick, error) {
	if err := s.CatchUp(ctx); err != nil {
		return Tick{}, err
	}
	end = end.UTC()
	t := Tick{At: end}
	rules, err := s.Rules(ctx)
	if err != nil {
		return t, err
	}
	if !rules.On || !rules.Time.On || (rules.Time.Active == 0 && rules.Time.Quiet == 0) {
		t.Note = NoteOff
		return t, nil
	}
	if prev, err := s.tickAt(ctx, end); err == nil {
		return prev, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return t, err
	}
	switch s.LiveState() {
	case Live:
	case Offline:
		t.Note = NoteNotLive
		return t, s.saveTick(ctx, t)
	default:
		t.Note = NoteUnknownLive
		return t, s.saveTick(ctx, t)
	}
	op := func(viewer string) string { return "earn:time:" + end.Format(time.RFC3339) + ":" + viewer }
	paid := map[string]bool{} // platform:id
	if rules.Time.Active > 0 {
		from := end.Add(-time.Duration(rules.Time.ActiveMinutes) * s.minute)
		active, err := s.chattedBetween(ctx, from, end)
		if err != nil {
			return t, err
		}
		for _, a := range active {
			ok, err := s.pay(ctx, a, op, rules.Time.Active, "time in chat, chatting")
			if err != nil {
				return t, err
			}
			paid[a.Platform+":"+a.ID] = true
			if ok {
				t.Active++
			}
		}
	}
	if rules.Time.Quiet > 0 {
		var list []events.Actor
		err := errors.New("no Twitch connection is set up")
		if s.o.Chatters != nil {
			list, err = s.o.Chatters.Chatters(ctx)
		}
		if err != nil {
			t.Note = fmt.Sprintf("Twitch's chatter list couldn't be read (%v), so only viewers who chatted earned", err)
		}
		for _, a := range list {
			if paid[a.Platform+":"+a.ID] {
				continue
			}
			paid[a.Platform+":"+a.ID] = true
			ok, err := s.pay(ctx, a, op, rules.Time.Quiet, "time in chat")
			if err != nil {
				return t, err
			}
			if ok {
				t.Quiet++
			}
		}
	}
	s.log.Info("time in chat paid", "at", end, "chatting", t.Active, "quiet", t.Quiet, "note", t.Note)
	return t, s.saveTick(ctx, t)
}

// pay credits one viewer for an interval. ok is false when it was paid
// before (a repeat), or couldn't be.
func (s *Service) pay(ctx context.Context, a events.Actor, op func(string) string, amount int64, memo string) (bool, error) {
	v, err := s.o.Viewers.Ensure(ctx, a)
	if err != nil {
		return false, err
	}
	// One person linked on two platforms is paid once an interval.
	holder, err := s.o.Points.Owner(ctx, v.ID)
	if err != nil {
		return false, err
	}
	res, err := s.o.Points.Credit(ctx, points.Op{ID: op(holder), Viewer: v.ID, Amount: amount, Memo: memo, By: By})
	switch {
	case errors.Is(err, points.ErrConflict), errors.Is(err, points.ErrInvalid):
		s.log.Debug("not paid", "viewer", v.ID, "err", err)
		return false, nil
	case err != nil:
		return false, err
	}
	return res.Applied, nil
}

// ChattedSince is who chatted after since, up to now, from real chat (never
// test chat or the streamer's): for giving points to recent chatters.
func (s *Service) ChattedSince(ctx context.Context, since time.Time) ([]events.Actor, error) {
	if err := s.CatchUp(ctx); err != nil {
		return nil, err
	}
	return s.chattedBetween(ctx, since, s.o.Now())
}

// chattedBetween is who chatted after from, up to end.
func (s *Service) chattedBetween(ctx context.Context, from, end time.Time) ([]events.Actor, error) {
	rows, err := s.o.DB.QueryContext(ctx, `SELECT platform, user_id, login, display_name FROM earning_active
		WHERE last_chat_at > ? AND last_chat_at <= ? ORDER BY last_chat_at`, storage.Timestamp(from), storage.Timestamp(end))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []events.Actor{}
	for rows.Next() {
		var a events.Actor
		if err := rows.Scan(&a.Platform, &a.ID, &a.Login, &a.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) saveTick(ctx context.Context, t Tick) error {
	if _, err := s.o.DB.ExecContext(ctx, "INSERT INTO earning_ticks (at, active, quiet, note) VALUES (?, ?, ?, ?) ON CONFLICT (at) DO NOTHING",
		storage.Timestamp(t.At), t.Active, t.Quiet, t.Note); err != nil {
		return err
	}
	_, err := s.o.DB.ExecContext(ctx, "DELETE FROM earning_ticks WHERE at < ?", storage.Timestamp(t.At.Add(-keepTicks)))
	return err
}

func (s *Service) tickAt(ctx context.Context, at time.Time) (Tick, error) {
	return s.scanTick(s.o.DB.QueryRowContext(ctx, "SELECT at, active, quiet, note FROM earning_ticks WHERE at = ?", storage.Timestamp(at)))
}

// LastTick is the newest time-in-chat tick, if any.
func (s *Service) LastTick(ctx context.Context) (*Tick, error) {
	t, err := s.scanTick(s.o.DB.QueryRowContext(ctx, "SELECT at, active, quiet, note FROM earning_ticks ORDER BY at DESC LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) scanTick(row *sql.Row) (Tick, error) {
	var t Tick
	var at string
	if err := row.Scan(&at, &t.Active, &t.Quiet, &t.Note); err != nil {
		return t, err
	}
	t.At, _ = storage.ParseTimestamp(at)
	return t, nil
}
