// Package ads asks Twitch for ad breaks and snoozes, and records what it
// asked and what Twitch said happened, apart (docs/adr/0024-ads.md).
//
// A request is recorded before it's sent, sent once, and never retried. A
// refused request announces nothing. A break is recorded only from Twitch's
// own event; its end is always an estimate (Twitch sends no end), marked so.
package ads

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
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Twitch is what ads need from Twitch.
type Twitch interface {
	StartCommercial(ctx context.Context, length int) (twitch.CommercialResult, error)
	SnoozeAd(ctx context.Context) (twitch.AdSchedule, error)
	Ads(ctx context.Context) (twitch.AdSchedule, error)
}

// Timing.
const (
	// unresolvedFor: an earlier commercial whose outcome is unknown blocks a
	// new one this long (Twitch's own gap between ads), so it can't start two.
	unresolvedFor = 8 * time.Minute
	// A break confirms a request made from 10 minutes before to 1 after it.
	matchBefore = 10 * time.Minute
	matchAfter  = time.Minute
	// An estimated end more than this late isn't announced (after a restart).
	staleEnd = 5 * time.Minute
	// Unknown commercials younger than this are checked against the schedule.
	reconcileFor   = time.Hour
	reconcileEvery = 30 * time.Second
	sendTimeout    = 15 * time.Second
)

// Statuses.
const (
	Pending  = "pending"
	Accepted = "accepted"
	Refused  = "refused"
	Unknown  = "unknown"
)

// ErrUnknown: the request was sent and no clear answer came. It may or may
// not have started; it's checked against the schedule, never sent again.
var ErrUnknown = errors.New("ads: Twitch didn't answer clearly, so the ad may or may not have started; StreamGremlin checks and won't ask again")

// RefusedError: nothing happened, from StreamGremlin's own checks or
// Twitch's answer.
type RefusedError struct {
	Reason  string // invalid, in_progress, cooldown, unresolved, not_live, not_eligible, no_snoozes, not_sent or refused
	Message string
}

func (e *RefusedError) Error() string { return "ads: " + e.Message }

// Request is one thing asked of Twitch.
type Request struct {
	ID             string     `json:"id"`
	Action         string     `json:"action"` // commercial or snooze
	Length         int        `json:"length,omitempty"`
	RequestedBy    string     `json:"requested_by"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason,omitempty"`
	Message        string     `json:"message,omitempty"`
	AcceptedLength *int       `json:"accepted_length,omitempty"`
	RetryUntil     *time.Time `json:"retry_until,omitempty"`
	NextAdAt       *time.Time `json:"next_ad_at,omitempty"`
	SnoozeCount    *int       `json:"snooze_count,omitempty"`
	ConfirmedBy    string     `json:"confirmed_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	SettledAt      *time.Time `json:"settled_at,omitempty"`
}

// Break is one ad break Twitch said started.
type Break struct {
	Key            string    `json:"key"`
	StartedAt      time.Time `json:"started_at"`
	Duration       int       `json:"duration_seconds"`
	Automatic      bool      `json:"is_automatic"`
	RequestID      string    `json:"request_id,omitempty"`
	EndEstimatedAt time.Time `json:"end_estimated_at"` // always an estimate
}

// Options configure a Service.
type Options struct {
	DB     *storage.DB
	Twitch Twitch
	Inbox  *events.Inbox
	Logger *slog.Logger
	Now    func() time.Time
}

// Service asks for ads and follows breaks.
type Service struct {
	db    *storage.DB
	tw    Twitch
	inbox *events.Inbox
	log   *slog.Logger
	now   func() time.Time

	beginMu sync.Mutex // checks and records a request as one step

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
	return &Service{db: o.DB, tw: o.Twitch, inbox: o.Inbox, log: o.Logger.With("component", "ads"), now: o.Now}
}

func ts(t time.Time) string { return storage.Timestamp(t) }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

// Start marks requests left pending by a stop as unknown (never resent),
// then follows breaks, announces estimated ends and checks unknown ones.
func (s *Service) Start(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE ad_requests SET status = 'unknown', reason = 'interrupted',
		message = 'StreamGremlin stopped before Twitch answered', settled_at = ? WHERE status = 'pending'`, ts(s.now())); err != nil {
		return fmt.Errorf("ads: %w", err)
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

const reqColumns = `id, action, length, requested_by, status, reason, message, accepted_length, retry_until, next_ad_at,
	snooze_count, confirmed_by, created_at, settled_at`

func scanRequest(r interface{ Scan(...any) error }) (Request, error) {
	var q Request
	var accepted, snoozes sql.NullInt64
	var retry, next, settled sql.NullString
	var created string
	if err := r.Scan(&q.ID, &q.Action, &q.Length, &q.RequestedBy, &q.Status, &q.Reason, &q.Message, &accepted, &retry, &next,
		&snoozes, &q.ConfirmedBy, &created, &settled); err != nil {
		return q, err
	}
	q.CreatedAt, _ = storage.ParseTimestamp(created)
	if accepted.Valid {
		n := int(accepted.Int64)
		q.AcceptedLength = &n
	}
	if snoozes.Valid {
		n := int(snoozes.Int64)
		q.SnoozeCount = &n
	}
	for _, p := range []struct {
		src sql.NullString
		dst **time.Time
	}{{retry, &q.RetryUntil}, {next, &q.NextAdAt}, {settled, &q.SettledAt}} {
		if p.src.Valid {
			t, _ := storage.ParseTimestamp(p.src.String)
			*p.dst = &t
		}
	}
	return q, nil
}

// begin records a request as pending after StreamGremlin's own checks, or
// returns the one already made with that operation key.
func (s *Service) begin(ctx context.Context, action string, length int, opKey, by string) (Request, bool, error) {
	s.beginMu.Lock()
	defer s.beginMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if old, err := scanRequest(tx.QueryRowContext(ctx, "SELECT "+reqColumns+" FROM ad_requests WHERE op_key = ?", opKey)); err == nil {
		return old, true, nil // the same request again: nothing is sent
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Request{}, false, err
	}
	now := s.now()
	refuse := func(reason, msg string) (Request, bool, error) {
		return Request{}, false, &RefusedError{Reason: reason, Message: msg}
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM ad_requests WHERE action = ? AND status = 'pending'", action).Scan(&n); err != nil {
		return Request{}, false, err
	}
	if n > 0 {
		return refuse("in_progress", "another "+action+" request is being sent")
	}
	if action == "commercial" {
		if length < 1 || length > twitch.MaxCommercialLength {
			return refuse("invalid", fmt.Sprintf("an ad break is 1 to %d seconds", twitch.MaxCommercialLength))
		}
		var until sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT MAX(retry_until) FROM ad_requests WHERE action = 'commercial'
			AND status IN ('accepted', 'refused')`).Scan(&until); err != nil {
			return Request{}, false, err
		}
		if until.Valid {
			if t, _ := storage.ParseTimestamp(until.String); now.Before(t) {
				return refuse("cooldown", fmt.Sprintf("Twitch needs a break between ads: try again in %s", t.Sub(now).Round(time.Second)))
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ad_requests WHERE action = 'commercial' AND status = 'unknown'
			AND confirmed_by = '' AND created_at >= ?`, ts(now.Add(-unresolvedFor))).Scan(&n); err != nil {
			return Request{}, false, err
		}
		if n > 0 {
			return refuse("unresolved", "an earlier ad request's outcome isn't known yet, and a second could start a second break; "+
				"StreamGremlin is checking with Twitch")
		}
	}
	q := Request{ID: events.NewID(), Action: action, Length: length, RequestedBy: by, Status: Pending, CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ad_requests (id, op_key, action, length, requested_by, status, created_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)`, q.ID, opKey, action, length, by, ts(now)); err != nil {
		return Request{}, false, err
	}
	return q, false, tx.Commit()
}

// settle records the answer, and the event that says it.
func (s *Service) settle(ctx context.Context, q *Request, err error) {
	now := s.now()
	q.SettledAt = &now
	var refused *twitch.AdRefusedError
	var ev events.Envelope
	payload := map[string]any{"request_id": q.ID, "action": q.Action}
	switch {
	case err == nil:
		q.Status = Accepted
	case errors.As(err, &refused):
		q.Status, q.Reason, q.Message = Refused, refused.Reason, refused.Error()
	case errors.Is(err, twitch.ErrAdOutcomeUnknown), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		q.Status, q.Reason, q.Message = Unknown, "no_answer", ErrUnknown.Error()
	default:
		// It never left StreamGremlin (signed out, a permission missing).
		q.Status, q.Reason, q.Message = Refused, "not_sent", err.Error()
	}
	// Twitch's event may have said the break began before its answer came
	// (D-094): then no answer is still a break that began.
	var confirmed string
	_ = s.db.QueryRowContext(ctx, "SELECT confirmed_by FROM ad_requests WHERE id = ?", q.ID).Scan(&confirmed)
	if confirmed != "" && q.Status == Unknown {
		q.Status, q.Reason, q.Message = Accepted, "", ""
	}
	if _, xerr := s.db.ExecContext(ctx, `UPDATE ad_requests SET status = ?, reason = ?, message = ?, accepted_length = ?, retry_until = ?,
		next_ad_at = ?, snooze_count = ?, settled_at = ? WHERE id = ?`, q.Status, q.Reason, q.Message, q.AcceptedLength, tsPtr(q.RetryUntil),
		tsPtr(q.NextAdAt), q.SnoozeCount, ts(now), q.ID); xerr != nil {
		s.log.Error("couldn't record an ad request's answer", "request", q.ID, "err", xerr)
	}
	switch {
	case q.Status == Accepted && q.Action == "commercial":
		payload["length"], payload["retry_until"] = q.AcceptedLength, tsPtr(q.RetryUntil)
		ev = events.Envelope{Type: "ads.break.requested"}
	case q.Status == Accepted:
		payload["next_ad_at"], payload["snooze_count"] = tsPtr(q.NextAdAt), q.SnoozeCount
		ev = events.Envelope{Type: "ads.snooze.applied"}
	case q.Status == Refused:
		payload["reason"], payload["message"] = q.Reason, q.Message
		ev = events.Envelope{Type: "ads.request.refused"}
	default:
		ev = events.Envelope{Type: "ads.request.unresolved"}
	}
	ev.Source, ev.SourceEventID, ev.OccurredAt, ev.DeliveryQuality = "core", "ad_request:"+q.ID, now, events.Confirmed
	ev.Payload, _ = json.Marshal(payload)
	if _, err := s.inbox.Accept(ctx, ev); err != nil {
		s.log.Warn("couldn't store an ad event", "err", err)
	}
	s.log.Info("ad request answered", "action", q.Action, "status", q.Status, "reason", q.Reason)
}

func outcome(q Request) error {
	switch q.Status {
	case Refused:
		return &RefusedError{Reason: q.Reason, Message: q.Message}
	case Unknown:
		return ErrUnknown
	case Pending:
		return &RefusedError{Reason: "in_progress", Message: "this request is still being sent"}
	}
	return nil
}

// StartCommercial asks Twitch for an ad break of length seconds. opKey
// makes it safe to repeat: the same key returns the first request, and
// nothing is sent again. by says who asked. A refusal or an unknown
// outcome is an error, so nothing after it announces an ad.
func (s *Service) StartCommercial(ctx context.Context, length int, opKey, by string) (Request, error) {
	q, repeated, err := s.begin(ctx, "commercial", length, opKey, by)
	if err != nil || repeated {
		if err == nil {
			err = outcome(q)
		}
		return q, err
	}
	// Sent once, and not cut short by the caller going away.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()
	res, err := s.tw.StartCommercial(sctx, length)
	if err == nil {
		n := res.Length
		q.AcceptedLength = &n
		if res.RetryAfter > 0 {
			t := s.now().Add(time.Duration(res.RetryAfter) * time.Second)
			q.RetryUntil = &t
		}
	}
	s.settle(context.WithoutCancel(ctx), &q, err)
	return q, outcome(q)
}

// Snooze pushes the next automatic ad back, if Twitch allows. opKey makes
// it safe to repeat.
func (s *Service) Snooze(ctx context.Context, opKey, by string) (Request, error) {
	q, repeated, err := s.begin(ctx, "snooze", 0, opKey, by)
	if err != nil || repeated {
		if err == nil {
			err = outcome(q)
		}
		return q, err
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()
	sc, err := s.tw.SnoozeAd(sctx)
	if err == nil {
		q.NextAdAt = sc.NextAdAt
		n := sc.SnoozeCount
		q.SnoozeCount = &n
	}
	s.settle(context.WithoutCancel(ctx), &q, err)
	return q, outcome(q)
}

// Schedule is Twitch's ad schedule, read now.
func (s *Service) Schedule(ctx context.Context) (twitch.AdSchedule, error) { return s.tw.Ads(ctx) }

// Recent lists the newest requests and breaks.
func (s *Service) Recent(ctx context.Context, n int) ([]Request, []Break, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+reqColumns+" FROM ad_requests ORDER BY created_at DESC, rowid DESC LIMIT ?", n)
	if err != nil {
		return nil, nil, err
	}
	reqs := []Request{}
	for rows.Next() {
		q, err := scanRequest(rows)
		if err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		reqs = append(reqs, q)
	}
	_ = rows.Close()
	brows, err := s.db.QueryContext(ctx, `SELECT key, started_at, duration, is_automatic, COALESCE(request_id, ''), end_estimated_at
		FROM ad_breaks ORDER BY started_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = brows.Close() }()
	breaks := []Break{}
	for brows.Next() {
		var b Break
		var started, end string
		var auto int
		if err := brows.Scan(&b.Key, &started, &b.Duration, &auto, &b.RequestID, &end); err != nil {
			return nil, nil, err
		}
		b.StartedAt, _ = storage.ParseTimestamp(started)
		b.EndEstimatedAt, _ = storage.ParseTimestamp(end)
		b.Automatic = auto == 1
		breaks = append(breaks, b)
	}
	return reqs, breaks, brows.Err()
}
