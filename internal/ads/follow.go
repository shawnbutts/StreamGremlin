package ads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

var followBreaks = events.Filter{"ads.break.started"}

// run follows Twitch's break events, announces estimated ends when they're
// due, and checks unknown commercials against the schedule.
func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	// Breaks are keyed, so reading the last hour again records nothing twice.
	cursor, err := s.inbox.SeqBefore(ctx, time.Now().Add(-reconcileFor))
	if err != nil {
		s.log.Warn("couldn't find where to read ad breaks from", "err", err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastCheck := time.Time{}
	for ctx.Err() == nil {
		recs, err := s.inbox.After(ctx, cursor, 200, followBreaks)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("couldn't read ad breaks", "err", err)
		}
		for _, r := range recs {
			cursor = r.Seq
			if err := s.recordBreak(ctx, r); err != nil && ctx.Err() == nil {
				s.log.Error("couldn't record an ad break", "err", err)
			}
		}
		if len(recs) == 200 {
			continue
		}
		s.announceEnds(ctx)
		if time.Since(lastCheck) >= reconcileEvery {
			lastCheck = time.Now()
			s.reconcile(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.inbox.Notifier().C():
		}
	}
}

// recordBreak records a break Twitch said began, and links the request
// that asked for it. Test events are never recorded.
func (s *Service) recordBreak(ctx context.Context, r events.Record) error {
	if r.DeliveryQuality != events.Confirmed {
		return nil
	}
	var p struct {
		Duration  int  `json:"duration_seconds"`
		Automatic bool `json:"is_automatic"`
	}
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return err
	}
	key := r.SourceEventID
	if key == "" {
		key = r.EventID
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	end := r.OccurredAt.Add(time.Duration(p.Duration) * time.Second)
	res, err := tx.ExecContext(ctx, `INSERT INTO ad_breaks (key, started_at, duration, is_automatic, end_estimated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (key) DO NOTHING`, key, ts(r.OccurredAt), p.Duration, p.Automatic, ts(end), ts(s.now()))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already recorded
	}
	if !p.Automatic {
		var id, status string
		// Pending too: Twitch's event can arrive before its answer to the
		// request does (D-094).
		err := tx.QueryRowContext(ctx, `SELECT id, status FROM ad_requests WHERE action = 'commercial' AND status IN ('pending', 'accepted', 'unknown')
			AND confirmed_by = '' AND created_at BETWEEN ? AND ? ORDER BY created_at DESC LIMIT 1`,
			ts(r.OccurredAt.Add(-matchBefore)), ts(r.OccurredAt.Add(matchAfter))).Scan(&id, &status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			if _, err := tx.ExecContext(ctx, "UPDATE ad_breaks SET request_id = ? WHERE key = ?", id, key); err != nil {
				return err
			}
			if err := confirm(ctx, tx, id, status, "event", s.now()); err != nil {
				return err
			}
		}
	}
	s.log.Info("ad break began", "duration", p.Duration, "automatic", p.Automatic)
	return tx.Commit()
}

// confirm marks a request as seen happening. An unknown one becomes
// accepted; an accepted (or pending) one only gains how it was confirmed.
// A pending one's answer, when it comes, keeps it.
func confirm(ctx context.Context, tx *sql.Tx, id, status, by string, now time.Time) error {
	if status == Unknown {
		_, err := tx.ExecContext(ctx, `UPDATE ad_requests SET status = 'accepted', confirmed_by = ?, settled_at = ? WHERE id = ?`, by, ts(now), id)
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE ad_requests SET confirmed_by = ? WHERE id = ?", by, id)
	return err
}

// announceEnds stores an estimated end for each break whose time has come.
// One long overdue (StreamGremlin was closed) is marked without announcing.
func (s *Service) announceEnds(ctx context.Context) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `SELECT key, started_at, duration, end_estimated_at FROM ad_breaks
		WHERE end_announced = 0 AND end_estimated_at <= ?`, ts(now))
	if err != nil {
		return
	}
	type due struct {
		key, started, end string
		duration          int
	}
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.key, &d.started, &d.duration, &d.end) == nil {
			list = append(list, d)
		}
	}
	_ = rows.Close()
	for _, d := range list {
		end, _ := storage.ParseTimestamp(d.end)
		if now.Sub(end) <= staleEnd {
			payload, _ := json.Marshal(map[string]any{"duration_seconds": d.duration, "started_at": d.started, "estimated": true})
			ev := events.Envelope{Type: "ads.break.end_estimated", Source: "core", SourceEventID: "ad_break_end:" + d.key,
				OccurredAt: end, DeliveryQuality: events.Estimated, Payload: payload}
			if _, err := s.inbox.Accept(ctx, ev); err != nil {
				s.log.Warn("couldn't store an ad break's estimated end", "err", err)
				continue
			}
		} else {
			s.log.Info("an ad break ended long ago, while StreamGremlin was closed; not announcing it", "break", d.key)
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE ad_breaks SET end_announced = 1 WHERE key = ?", d.key); err != nil {
			s.log.Error("couldn't mark an ad break's end", "err", err)
		}
	}
}

// reconcile checks recent unknown commercials against the schedule's last
// ad. One that's there is found accepted; nothing is ever sent again.
func (s *Service) reconcile(ctx context.Context) {
	now := s.now()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ad_requests WHERE action = 'commercial' AND status = 'unknown'
		AND confirmed_by = '' AND created_at >= ?`, ts(now.Add(-reconcileFor))).Scan(&n); err != nil || n == 0 {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, sendTimeout)
	sc, err := s.tw.Ads(cctx)
	cancel()
	if err != nil {
		s.log.Info("couldn't check an unclear ad request yet", "err", err)
		return
	}
	if sc.LastAdAt == nil {
		return
	}
	last := *sc.LastAdAt
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM ad_requests WHERE action = 'commercial' AND status = 'unknown' AND confirmed_by = ''
		AND created_at BETWEEN ? AND ? ORDER BY created_at DESC LIMIT 1`, ts(last.Add(-matchBefore)), ts(last.Add(matchAfter))).Scan(&id)
	if err != nil {
		return
	}
	if confirm(ctx, tx, id, Unknown, "schedule", now) == nil && tx.Commit() == nil {
		s.log.Info("an unclear ad request was found in Twitch's schedule: it started", "request", id)
	}
}
