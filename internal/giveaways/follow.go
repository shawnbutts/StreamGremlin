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

	"code.streamgremlin.com/streamgremlin/internal/chat"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

var followChat = events.Filter{"chat.command.received", "chat.message.received"}

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
		recs, err := s.inbox.After(ctx, s.cursor, 200, followChat)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("couldn't read chat", "err", err)
		}
		for _, r := range recs {
			s.mu.Lock()
			s.cursor = r.Seq
			cd := s.handleLocked(ctx, r)
			s.mu.Unlock()
			if cd == nil {
				continue
			}
			// Checked without the lock: asking Twitch never holds up the
			// streamer's buttons.
			q, reason := s.qualify(ctx, *cd)
			s.mu.Lock()
			if err := s.admitLocked(ctx, *cd, q, reason); err != nil {
				s.log.Error("couldn't record a giveaway entry", "err", err)
			}
			s.mu.Unlock()
		}
		s.mu.Lock()
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
	Badges      []struct {
		SetID string `json:"set_id"`
		Info  string `json:"info"`
	} `json:"badges"`
	// Practice is a test chatter's facts, for a practice giveaway's rules.
	Practice *struct {
		FollowDays  int `json:"follow_days"`
		AccountDays int `json:"account_days"`
	} `json:"practice"`
}

// handleLocked takes a winner's claim, or returns someone asking to enter,
// to be checked against the rules.
func (s *Service) handleLocked(ctx context.Context, r events.Record) *candidate {
	g := s.current
	if g == nil || r.Seq <= g.afterSeq || r.ReceivedAt.Before(g.OpenedAt) {
		return nil
	}
	// A practice giveaway hears only test chat; a live one only real chat.
	if (r.DeliveryQuality == events.Synthetic) != g.Practice {
		return nil
	}
	p, platform, ok := chatOf(r)
	if !ok {
		return nil
	}
	byChat := r.Type == "chat.message.received"
	cmd := strings.ToLower(p.Command)
	if byChat {
		// A command is read from its own event.
		if c, ok := chat.ParseCommand(p.Text); ok {
			cmd = c.Name
		}
		if !g.EnterByChat || cmd == g.Command || (g.ClaimCommand != "" && cmd == g.ClaimCommand) {
			return nil
		}
	} else if g.ClaimCommand != "" && cmd == g.ClaimCommand {
		s.claimLocked(ctx, g, r, platform, p.Chatter.ID)
		return nil
	} else if cmd != g.Command {
		return nil
	}
	if g.State != Open || (g.ClosesAt != nil && !r.ReceivedAt.Before(*g.ClosesAt)) || p.Chatter.Broadcaster {
		return nil // your own giveaway: you can't win it
	}
	var in int
	var refused string
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM giveaway_entries WHERE giveaway_id = ?1 AND platform = ?2 AND user_id = ?3),
		COALESCE((SELECT reason FROM giveaway_refusals WHERE giveaway_id = ?1 AND platform = ?2 AND user_id = ?3), '')`,
		g.ID, platform, p.Chatter.ID).Scan(&in, &refused); err != nil {
		s.log.Error("couldn't read giveaway entries", "err", err)
		return nil
	}
	// Someone turned away is checked again when they type the command, so
	// a busy chat doesn't ask Twitch about every line; messages counted
	// are only in the database, so those are checked again as they chat.
	if in > 0 || (byChat && refused != "" && refused != ReasonMessages) {
		return nil
	}
	return &candidate{g: *g, r: r, platform: platform, c: p.Chatter}
}

// chatPayload is what a chat message or command says, the parts read.
type chatPayload struct {
	Command string  `json:"command"`
	Text    string  `json:"text"`
	Chatter chatter `json:"chatter"`
}

// chatOf reads a chat record: what it says, and the chatter's platform.
func chatOf(r events.Record) (p chatPayload, platform string, ok bool) {
	if json.Unmarshal(r.Payload, &p) != nil || p.Chatter.ID == "" {
		return p, "", false
	}
	platform = r.Source
	if r.Actor != nil {
		platform = r.Actor.Platform
	}
	if p.Chatter.DisplayName == "" {
		p.Chatter.DisplayName = p.Chatter.Login
	}
	return p, platform, true
}

// admitLocked records the checked candidate: in, or turned away with the
// reason (said in chat once, and again only when the reason changes).
func (s *Service) admitLocked(ctx context.Context, cd candidate, q Qualified, reason string) error {
	g, c := s.current, cd.c
	// Entries may have closed, or the giveaway ended, while Twitch answered.
	if g == nil || g.ID != cd.g.ID || g.State != Open {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if reason != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO giveaway_refusals (giveaway_id, platform, user_id, login, display_name, reason,
				refused_at, announced) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (giveaway_id, platform, user_id) DO UPDATE SET
				reason = excluded.reason, refused_at = excluded.refused_at, announced = excluded.announced
				WHERE giveaway_refusals.reason != excluded.reason`,
				g.ID, cd.platform, c.ID, c.Login, c.DisplayName, reason, ts(s.now()), cd.quiet)
			return err
		}
		qualified := []byte("{}")
		if g.Rules != (Rules{}) {
			qualified, _ = json.Marshal(q)
		}
		// Chances are set as they enter, from the chat message, and kept.
		chances := 1
		if c.Subscriber {
			chances = max(g.SubscriberChances, 1)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO giveaway_entries (giveaway_id, seq, platform, user_id, login, display_name, event_id,
			entered_at, qualified, chances, announced) SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?, ?, ?, ?, ? FROM giveaway_entries
			WHERE giveaway_id = ? ON CONFLICT DO NOTHING`, g.ID, cd.platform, c.ID, c.Login, c.DisplayName, cd.r.EventID, ts(s.now()),
			string(qualified), chances, cd.quiet, g.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM giveaway_refusals WHERE giveaway_id = ? AND platform = ? AND user_id = ?", g.ID,
			cd.platform, c.ID)
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
		rows, err := tx.QueryContext(ctx, "SELECT login, chances FROM giveaway_entries WHERE giveaway_id = ? AND announced = 0 ORDER BY seq",
			g.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n string
			var chances int
			_ = rows.Scan(&n, &chances)
			if chances > 1 {
				n = fmt.Sprintf("%s (%d chances)", n, chances)
			}
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
		refused := map[string][]string{}
		var reasons []string
		rows, err = tx.QueryContext(ctx, `SELECT login, reason FROM giveaway_refusals WHERE giveaway_id = ? AND announced = 0
			ORDER BY refused_at, login`, g.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n, reason string
			_ = rows.Scan(&n, &reason)
			if refused[reason] == nil {
				reasons = append(reasons, reason)
			}
			refused[reason] = append(refused[reason], "@"+n)
		}
		_ = rows.Close()
		for _, reason := range reasons {
			for _, batch := range batches(refused[reason], refusalWords(g, reason)) {
				if err := s.say(ctx, tx, g, batch); err != nil {
					return err
				}
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
// order, "<seq>\t<platform>:<user id>\n". A weighted giveaway's lines
// end with the entry's chances, "\t<chances>", so they can't change after
// closing; unweighted lines are as they always were.
func Fingerprint(entries []Entry, weighted bool) string {
	h := sha256.New()
	for _, e := range entries {
		if weighted {
			fmt.Fprintf(h, "%d\t%s:%s\t%d\n", e.Seq, e.Platform, e.UserID, e.Chances)
		} else {
			fmt.Fprintf(h, "%d\t%s:%s\n", e.Seq, e.Platform, e.UserID)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Tickets counts the tickets entries hold: their chances, at least one each.
func Tickets(entries []Entry) int {
	n := 0
	for _, e := range entries {
		n += max(e.Chances, 1)
	}
	return n
}

// Holder is the index of the entry holding ticket t (from 0), with each
// entry's tickets in a row, in entry order.
func Holder(entries []Entry, t int) int {
	for i, e := range entries {
		t -= max(e.Chances, 1)
		if t < 0 {
			return i
		}
	}
	return -1
}

// Verify checks a closed giveaway's record: the entry list matches its
// fingerprint, and each winner holds the recorded ticket among the entries
// not drawn before. It returns what doesn't hold, or nothing. It can't
// replay the randomness: crypto/rand has no public seed.
func Verify(d Detail) []string {
	var bad []string
	if d.Fingerprint == "" {
		return []string{"entries are still open, so there's no fingerprint to check yet"}
	}
	if got := Fingerprint(d.Entries, d.Weighted()); got != d.Fingerprint {
		bad = append(bad, fmt.Sprintf("the entry list's fingerprint is %s, not the %s recorded at closing", got, d.Fingerprint))
	}
	drawn := map[int]bool{}
	for _, dr := range d.Draws {
		var cands []Entry
		for _, e := range d.Entries {
			if !drawn[e.Seq] {
				cands = append(cands, e)
			}
		}
		tickets := Tickets(cands)
		at := -1
		if dr.PickIndex >= 0 && dr.PickIndex < tickets {
			at = Holder(cands, dr.PickIndex)
		}
		switch {
		case tickets != dr.Candidates && d.Weighted():
			bad = append(bad, fmt.Sprintf("draw %d: those not yet drawn held %d tickets, not %d", dr.N, tickets, dr.Candidates))
		case tickets != dr.Candidates:
			bad = append(bad, fmt.Sprintf("draw %d: %d entries weren't drawn yet, not %d", dr.N, tickets, dr.Candidates))
		case at < 0 || cands[at].Seq != dr.EntrySeq:
			if d.Weighted() {
				bad = append(bad, fmt.Sprintf("draw %d: entry %d doesn't hold ticket %d of those not yet drawn", dr.N, dr.EntrySeq, dr.PickIndex+1))
			} else {
				bad = append(bad, fmt.Sprintf("draw %d: entry %d isn't number %d of those not yet drawn", dr.N, dr.EntrySeq, dr.PickIndex+1))
			}
		}
		drawn[dr.EntrySeq] = true
	}
	return bad
}
