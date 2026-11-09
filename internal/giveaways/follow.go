package giveaways

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

var followCommands = events.Filter{"chat.command.received"}

// Start finds the unfinished giveaway, marks messages that were being sent
// when StreamGremlin stopped as unknown (never resent), and follows chat.
func (s *Service) Start(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE giveaway_messages SET state = 'unknown',
		detail = 'StreamGremlin stopped while sending it' WHERE state = 'sending'`); err != nil {
		return fmt.Errorf("giveaways: %w", err)
	}
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM giveaways WHERE state IN ('open', 'closed')").Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("giveaways: %w", err)
	default:
		g, err := s.load(ctx, s.db, id)
		if err != nil {
			return err
		}
		s.setCurrent(&g)
	}
	// Commands stored shortly before a stop are read again: entries and
	// claims are recorded once, so nothing happens twice.
	if s.cursor, err = s.inbox.SeqBefore(ctx, time.Now().Add(-2*time.Minute)); err != nil {
		return err
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	s.done = make(chan struct{}, 2)
	go s.run(ctx)
	go s.send(ctx)
	return nil
}

// Close stops following and sending. Closing twice is harmless.
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		if s.stop != nil {
			s.stop()
			<-s.done
			<-s.done
		}
	})
}

func (s *Service) run(ctx context.Context) {
	defer func() { s.done <- struct{}{} }()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		recs, err := s.inbox.After(ctx, s.cursor, 200, followCommands)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("couldn't read chat commands", "err", err)
		}
		s.mu.Lock()
		for _, r := range recs {
			s.cursor = r.Seq
			s.handleLocked(ctx, r)
		}
		s.timersLocked(ctx)
		s.mu.Unlock()
		if len(recs) == 200 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.inbox.Notifier().C():
		}
	}
}

// chatter is who sent a command, from its payload.
type chatter struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"display_name"`
	Broadcaster bool   `json:"is_broadcaster"`
	Subscriber  bool   `json:"is_subscriber"`
}

// handleLocked enters a viewer, or takes a winner's claim.
func (s *Service) handleLocked(ctx context.Context, r events.Record) {
	g := s.current
	if g == nil || r.ReceivedAt.Before(g.OpenedAt) {
		return
	}
	// A practice giveaway hears only test chat; a live one only real chat.
	if (r.DeliveryQuality == events.Synthetic) != g.Practice {
		return
	}
	var p struct {
		Command string  `json:"command"`
		Chatter chatter `json:"chatter"`
	}
	if json.Unmarshal(r.Payload, &p) != nil || p.Chatter.ID == "" {
		return
	}
	platform := r.Source
	if r.Actor != nil {
		platform = r.Actor.Platform
	}
	if p.Chatter.DisplayName == "" {
		p.Chatter.DisplayName = p.Chatter.Login
	}
	cmd := strings.ToLower(p.Command)
	switch {
	case cmd == g.Command && g.State == Open:
		if g.ClosesAt != nil && !r.ReceivedAt.Before(*g.ClosesAt) {
			return
		}
		if err := s.enterLocked(ctx, g, r, platform, p.Chatter); err != nil {
			s.log.Error("couldn't record a giveaway entry", "err", err)
		}
	case g.ClaimCommand != "" && cmd == g.ClaimCommand:
		s.claimLocked(ctx, g, r, platform, p.Chatter.ID)
	}
}

func (s *Service) enterLocked(ctx context.Context, g *Giveaway, r events.Record, platform string, c chatter) error {
	if c.Broadcaster {
		return nil // your own giveaway: you can't win it
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if g.SubscribersOnly && !c.Subscriber {
			_, err := tx.ExecContext(ctx, `INSERT INTO giveaway_refusals (giveaway_id, platform, user_id, login, display_name, reason,
				refused_at) VALUES (?, ?, ?, ?, ?, 'subscribers_only', ?) ON CONFLICT DO NOTHING`, g.ID, platform, c.ID, c.Login,
				c.DisplayName, ts(s.now()))
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO giveaway_entries (giveaway_id, seq, platform, user_id, login, display_name, event_id,
			entered_at) SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?, ? FROM giveaway_entries WHERE giveaway_id = ?
			ON CONFLICT DO NOTHING`, g.ID, platform, c.ID, c.Login, c.DisplayName, r.EventID, ts(s.now()), g.ID)
		return err
	})
}

func (s *Service) claimLocked(ctx context.Context, g *Giveaway, r events.Record, platform, userID string) {
	draws, err := drawsOf(ctx, s.db, g.ID)
	if err != nil {
		return
	}
	for _, d := range draws {
		if d.Outcome != PendingClaim || d.Winner.Platform != platform || d.Winner.UserID != userID {
			continue
		}
		// Judged by when the claim arrived, not when it's read.
		if d.ClaimUntil != nil && r.ReceivedAt.After(*d.ClaimUntil) {
			return
		}
		err := s.tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE giveaway_draws SET outcome = 'claimed', settled_at = ?, settled_by = 'winner'
				WHERE id = ? AND outcome = 'pending_claim'`, ts(s.now()), d.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			return s.say(ctx, tx, g, fmt.Sprintf("@%s claimed %s! 🎉", d.Winner.Login, g.Title))
		})
		if err != nil {
			s.log.Error("couldn't record a claim", "err", err)
			return
		}
		s.emit(ctx, g, "giveaways.winner.claimed", "claimed:"+d.ID, actorOf(d.Winner), s.publicFields(g, map[string]any{"draw": d.N,
			"winner": winnerFields(d.Winner)}))
		return
	}
}

// timersLocked closes entries when their time is up, forfeits unclaimed
// wins, and tells new entrants they're in.
func (s *Service) timersLocked(ctx context.Context) {
	g := s.current
	if g == nil {
		return
	}
	now := s.now()
	if g.State == Open && g.ClosesAt != nil && !now.Before(*g.ClosesAt) {
		if _, err := s.closeLocked(ctx, g); err != nil {
			s.log.Error("couldn't close entries on time", "err", err)
		}
	}
	if g.State == Closed && g.ClaimSeconds > 0 {
		s.forfeitLocked(ctx, g, now)
	}
	if time.Since(s.lastAck) >= s.ackEvery {
		s.ackLocked(ctx, false)
	}
}

func (s *Service) forfeitLocked(ctx context.Context, g *Giveaway, now time.Time) {
	draws, err := drawsOf(ctx, s.db, g.ID)
	if err != nil {
		return
	}
	for _, d := range draws {
		if d.Outcome != PendingClaim || d.ClaimUntil == nil || now.Before(d.ClaimUntil.Add(claimGrace)) {
			continue
		}
		err := s.tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE giveaway_draws SET outcome = 'forfeited', settled_at = ?, settled_by = 'time'
				WHERE id = ? AND outcome = 'pending_claim'`, ts(now), d.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			return s.say(ctx, tx, g, fmt.Sprintf("@%s didn't claim %s in time, so the win passes on.", d.Winner.Login, g.Title))
		})
		if err != nil {
			s.log.Error("couldn't record a forfeit", "err", err)
			continue
		}
		s.emit(ctx, g, "giveaways.winner.forfeited", "forfeited:"+d.ID, actorOf(d.Winner), s.publicFields(g, map[string]any{"draw": d.N,
			"winner": winnerFields(d.Winner)}))
	}
}

// ackLocked tells new entrants they're in, and those turned away why, a
// few names to a message, so a busy chat isn't flooded.
func (s *Service) ackLocked(ctx context.Context, force bool) {
	g := s.current
	s.lastAck = time.Now()
	if g == nil || (g.State != Open && !force) {
		return
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var names []string
		rows, err := tx.QueryContext(ctx, "SELECT login FROM giveaway_entries WHERE giveaway_id = ? AND announced = 0 ORDER BY seq", g.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			names = append(names, "@"+n)
		}
		_ = rows.Close()
		var total int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM giveaway_entries WHERE giveaway_id = ?", g.ID).Scan(&total); err != nil {
			return err
		}
		for _, batch := range batches(names, fmt.Sprintf(": you're in %s! (%s so far)", g.Title, plural(total, "entry", "entries"))) {
			if err := s.say(ctx, tx, g, batch); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE giveaway_entries SET announced = 1 WHERE giveaway_id = ? AND announced = 0", g.ID); err != nil {
			return err
		}
		names = names[:0]
		rows, err = tx.QueryContext(ctx, "SELECT login FROM giveaway_refusals WHERE giveaway_id = ? AND announced = 0 ORDER BY refused_at", g.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			names = append(names, "@"+n)
		}
		_ = rows.Close()
		for _, batch := range batches(names, fmt.Sprintf(": %s is for subscribers only.", g.Title)) {
			if err := s.say(ctx, tx, g, batch); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE giveaway_refusals SET announced = 1 WHERE giveaway_id = ? AND announced = 0", g.ID)
		return err
	})
	if err != nil {
		s.log.Error("couldn't tell entrants they're in", "err", err)
	}
}

// batches joins names into messages that fit chat, each ending in tail.
func batches(names []string, tail string) []string {
	var out []string
	cur := ""
	for _, n := range names {
		next := n
		if cur != "" {
			next = cur + ", " + n
		}
		if len([]rune(next+tail)) > twitch.MaxChatRunes-10 && cur != "" {
			out = append(out, cur+tail)
			next = n
		}
		cur = next
	}
	if cur != "" {
		out = append(out, cur+tail)
	}
	return out
}

// send posts queued messages in order, each claimed before sending and
// never resent.
func (s *Service) send(ctx context.Context) {
	defer func() { s.done <- struct{}{} }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		for s.sendOne(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
	}
}

// sendOne sends the oldest queued message. It reports whether to go on.
func (s *Service) sendOne(ctx context.Context) bool {
	var id int64
	var gid, text, created string
	err := s.db.QueryRowContext(ctx, "SELECT id, giveaway_id, text, created_at FROM giveaway_messages WHERE state = 'queued' ORDER BY id LIMIT 1").
		Scan(&id, &gid, &text, &created)
	if err != nil {
		return false
	}
	set := func(state, detail string) {
		var sent any
		if state == "sent" {
			sent = ts(s.now())
		}
		if _, err := s.db.ExecContext(context.WithoutCancel(ctx), "UPDATE giveaway_messages SET state = ?, detail = ?, sent_at = ? WHERE id = ?",
			state, detail, sent, id); err != nil {
			s.log.Error("couldn't record a giveaway message", "err", err)
		}
	}
	if at, _ := storage.ParseTimestamp(created); s.now().Sub(at) > staleMessage {
		set("failed", "it couldn't be sent in time, so it wasn't sent late")
		return true
	}
	res, err := s.db.ExecContext(ctx, "UPDATE giveaway_messages SET state = 'sending' WHERE id = ? AND state = 'queued'", id)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return true
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	_, err = s.tw.SendChat(sctx, twitch.SendRequest{Text: text, Workflow: "giveaway:" + gid})
	cancel()
	var drop *twitch.DropError
	switch {
	case err == nil:
		set("sent", "")
		return true
	case errors.Is(err, twitch.ErrRateLimited):
		// Nothing was sent: it goes again shortly.
		set("queued", "")
		return false
	case errors.Is(err, twitch.ErrNotSignedIn), errors.Is(err, twitch.ErrInvalid), errors.Is(err, twitch.ErrNeedsPermission),
		errors.Is(err, twitch.ErrNoClientID), errors.As(err, &drop):
		set("failed", err.Error())
	default:
		set("unknown", "Twitch didn't answer clearly, so it may or may not be in chat: "+err.Error())
	}
	s.log.Warn("a giveaway message wasn't sent", "err", err)
	return true
}

// Fingerprint is the SHA-256 of the entry list: one line per entry, in
// order, "<seq>\t<platform>:<user id>\n".
func Fingerprint(entries []Entry) string {
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%d\t%s:%s\n", e.Seq, e.Platform, e.UserID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Verify checks a closed giveaway's record: the entry list matches its
// fingerprint, and each winner sits at the recorded place among the
// entries not drawn before. It returns what doesn't hold, or nothing.
// It can't replay the randomness: crypto/rand has no public seed.
func Verify(d Detail) []string {
	var bad []string
	if d.Fingerprint == "" {
		return []string{"entries are still open, so there's no fingerprint to check yet"}
	}
	if got := Fingerprint(d.Entries); got != d.Fingerprint {
		bad = append(bad, fmt.Sprintf("the entry list's fingerprint is %s, not the %s recorded at closing", got, d.Fingerprint))
	}
	drawn := map[int]bool{}
	for _, dr := range d.Draws {
		var cands []int
		for _, e := range d.Entries {
			if !drawn[e.Seq] {
				cands = append(cands, e.Seq)
			}
		}
		switch {
		case len(cands) != dr.Candidates:
			bad = append(bad, fmt.Sprintf("draw %d: %d entries weren't drawn yet, not %d", dr.N, len(cands), dr.Candidates))
		case dr.PickIndex < 0 || dr.PickIndex >= len(cands) || cands[dr.PickIndex] != dr.EntrySeq:
			bad = append(bad, fmt.Sprintf("draw %d: entry %d isn't number %d of those not yet drawn", dr.N, dr.EntrySeq, dr.PickIndex+1))
		}
		drawn[dr.EntrySeq] = true
	}
	return bad
}
