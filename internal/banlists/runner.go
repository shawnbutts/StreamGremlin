package banlists

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// The runner works through one list at a time, one batch or one request
// at a time, saving each name's state before and after each request.

// readBatch is how many names are saved at once while reading.
const readBatch = 1000

// Start runs the lists in the background.
func (s *Service) Start(ctx context.Context) {
	rctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stop = stop
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.run(rctx) }()
}

// Close stops after the request in flight. Closing twice is harmless.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
	}
	s.wg.Wait()
}

func (s *Service) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	case <-s.kick:
		return true
	}
}

func (s *Service) run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, wait, err := s.step(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.log.Warn("ban lists: a step failed", "err", err)
			wait = 5 * time.Second
		}
		if !worked {
			if wait == 0 {
				wait = s.o.Idle
			}
			s.sleep(ctx, wait)
		}
	}
}

// step does one piece of work on the first list that has some. It reports
// whether it did anything, and how long to wait when it didn't.
func (s *Service) step(ctx context.Context) (bool, time.Duration, error) {
	if until := s.waitingUntil(); until.After(s.o.Now()) {
		return false, until.Sub(s.o.Now()), nil
	}
	lists, err := s.All(ctx)
	if err != nil {
		return false, 0, err
	}
	live := s.o.Live != nil && s.o.Live()
	waitingLive := map[string]bool{}
	defer func() {
		s.mu.Lock()
		s.waitingLive = waitingLive
		s.mu.Unlock()
	}()
	// Oldest first.
	for i := len(lists) - 1; i >= 0; i-- {
		l := lists[i]
		if !busy(l.Phase) || l.Paused {
			continue
		}
		if l.Phase != Reading && live && l.PauseLive {
			waitingLive[l.ID] = true
			continue
		}
		var err error
		switch l.Phase {
		case Reading:
			err = s.read(ctx, l)
		case Checking:
			err = s.check(ctx, l)
		case Banning, Undoing:
			err = s.apply(ctx, l)
		case Copying:
			err = s.copyPage(ctx, l)
		case Comparing:
			err = s.compare(ctx, l)
		}
		var stop *stopError
		if errors.As(err, &stop) {
			s.stopList(ctx, l.ID, stop.why)
			return true, 0, nil
		}
		return err == nil, 0, err
	}
	return false, 0, nil
}

// stopError stops a list with a reason the streamer must fix (a sign-in,
// a permission): every request would fail the same way.
type stopError struct{ why string }

func (e *stopError) Error() string { return e.why }

func (s *Service) stopList(ctx context.Context, id, why string) {
	_, _ = s.o.DB.ExecContext(ctx, "UPDATE ban_lists SET paused = 1, problem = ?, updated_at = ? WHERE id = ?", why, storage.Timestamp(s.o.Now()), id)
	s.log.Warn("ban list paused", "list", id, "why", why)
}

func (s *Service) setPhase(ctx context.Context, id, phase string) error {
	_, err := s.o.DB.ExecContext(ctx, "UPDATE ban_lists SET phase = ?, cursor = '', updated_at = ? WHERE id = ?", phase, storage.Timestamp(s.o.Now()), id)
	if err == nil {
		s.log.Info("ban list", "list", id, "phase", phase)
	}
	return err
}

func (s *Service) waitingUntil() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitUntil
}

// pace waits when Twitch's bucket is down to what's kept spare, or Twitch
// said to wait.
func (s *Service) pace(ri twitch.RateInfo, told bool) {
	var until time.Time
	switch {
	case told && ri.Known:
		until = ri.Reset.Add(time.Second)
	case told:
		until = s.o.Now().Add(30 * time.Second)
	case ri.Known && ri.Remaining <= s.o.Spare:
		until = ri.Reset.Add(time.Second)
	}
	if !until.IsZero() {
		s.mu.Lock()
		s.waitUntil = until
		s.mu.Unlock()
	}
}

func (s *Service) noteDone(id string) {
	s.mu.Lock()
	s.done[id] = append(s.done[id], s.o.Now())
	s.mu.Unlock()
}

// lookupStop turns a failed lookup into a stop when it would fail for
// everyone.
func lookupErr(err error) error {
	if errors.Is(err, twitch.ErrNeedsPermission) || twitch.IsSignInError(err) || errors.Is(err, twitch.ErrNoClientID) {
		return &stopError{why: "Sign in to Twitch again on Connections, and allow StreamGremlin to moderate: " +
			strings.TrimPrefix(err.Error(), "twitch: ")}
	}
	return err
}

// read reads the list's file into names, then marks what's gone from it.
// Reading again after a restart is harmless.
func (s *Service) read(ctx context.Context, l List) error {
	f, err := os.Open(l.filePath) //nolint:gosec // the streamer's own file, read for names only
	if err != nil {
		return &stopError{why: "StreamGremlin can't read the file any more: import it again."}
	}
	defer func() { _ = f.Close() }()
	now := storage.Timestamp(s.o.Now())
	var batch []Name
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.tx(ctx, func(tx *sql.Tx) error {
			st, err := tx.PrepareContext(ctx, `INSERT INTO ban_list_entries (list_id, name, state, generation, updated_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (list_id, name) DO UPDATE SET generation = excluded.generation`)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			for _, n := range batch {
				if _, err := st.ExecContext(ctx, l.ID, string(n), StateNew, l.generation, now); err != nil {
					return err
				}
			}
			return nil
		})
		batch = batch[:0]
		return err
	}
	skipped, err := Read(f, func(n Name) error {
		batch = append(batch, n)
		if len(batch) == readBatch {
			return flush()
		}
		return ctx.Err()
	})
	if err == nil {
		err = flush()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return &stopError{why: "The file couldn't be read: " + strings.TrimPrefix(err.Error(), "banlists: ")}
	}
	// Names gone from the newer file: what this list banned is offered for
	// unbanning; the rest are forgotten. Unanswered requests stay until
	// they're settled.
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ban_list_entries WHERE list_id = ? AND generation < ? AND state NOT IN (?, ?, ?, ?, ?)`,
			l.ID, l.generation, StateBanned, StateRemoved, StateSent, StateUnbanSent, StateUnban); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ban_list_entries SET state = ?, updated_at = ? WHERE list_id = ? AND generation < ? AND state = ?`,
			StateRemoved, now, l.ID, l.generation, StateBanned); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ban_lists SET phase = ?, file_path = '', skipped = ?, updated_at = ? WHERE id = ?`,
			Checking, skipped, now, l.ID)
		return err
	})
}

// check looks up to 100 new names: found or not, already banned, or left
// alone; the rest wait to be banned.
func (s *Service) check(ctx context.Context, l List) error {
	batch, err := s.Entries(ctx, l.ID, []string{StateNew}, "", twitch.MaxLookup)
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		return s.setPhase(ctx, l.ID, Ready)
	}
	var logins, ids []string
	for _, e := range batch {
		if n := Name(e.Name); n.IsID() {
			ids = append(ids, n.Value())
		} else {
			logins = append(logins, n.Value())
		}
	}
	users, ri, err := s.o.Twitch.LookUpUsers(ctx, logins, ids)
	s.pace(ri, false)
	if err != nil {
		return lookupErr(err)
	}
	byLogin, byID := map[string]twitch.User{}, map[string]twitch.User{}
	var found []string
	for _, u := range users {
		byLogin[strings.ToLower(u.Login)], byID[u.ID] = u, u
		found = append(found, u.ID)
	}
	banned, ri, err := s.o.Twitch.BannedAmong(ctx, found)
	s.pace(ri, false)
	if err != nil {
		return lookupErr(err)
	}
	mods, vips, ri, err := s.o.Twitch.RolesAmong(ctx, found)
	s.pace(ri, false)
	if err != nil {
		return lookupErr(err)
	}
	holders := map[string]bool{}
	if l.KeepPoints && s.o.Holders != nil {
		if holders, err = s.o.Holders(ctx, found); err != nil {
			return err
		}
	}
	me := s.o.Twitch.StreamerID()
	now := storage.Timestamp(s.o.Now())
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, e := range batch {
			n := Name(e.Name)
			u, ok := byLogin[n.Value()]
			if n.IsID() {
				u, ok = byID[n.Value()]
			}
			state, detail := StateWaiting, ""
			switch {
			case !ok:
				state = StateNotFound
			case u.ID == me:
				state, detail = StateKept, "you"
			case mods[u.ID]:
				state, detail = StateKept, "a moderator"
			case vips[u.ID]:
				state, detail = StateKept, "a VIP"
			case holders[u.ID]:
				state, detail = StateKept, "has points"
			case banned[u.ID]:
				state = StateAlready
			}
			if _, err := tx.ExecContext(ctx, `UPDATE ban_list_entries SET user_id = ?, login = ?, state = ?, detail = ?, updated_at = ?
				WHERE list_id = ? AND name = ?`, u.ID, u.Login, state, detail, now, l.ID, e.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) setEntry(ctx context.Context, listID, name, state, detail string) error {
	_, err := s.o.DB.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, detail = ?, updated_at = ? WHERE list_id = ? AND name = ?",
		state, detail, storage.Timestamp(s.o.Now()), listID, name)
	return err
}

// apply settles unanswered requests first, then sends one unban or one
// ban.
func (s *Service) apply(ctx context.Context, l List) error {
	// Unanswered bans and unbans: Twitch's list says what happened.
	pending, err := s.Entries(ctx, l.ID, []string{StateSent, StateUnbanSent}, "", twitch.MaxLookup)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		var ids []string
		for _, e := range pending {
			ids = append(ids, e.UserID)
		}
		banned, ri, err := s.o.Twitch.BannedAmong(ctx, ids)
		s.pace(ri, false)
		if err != nil {
			return lookupErr(err)
		}
		return s.tx(ctx, func(tx *sql.Tx) error {
			now := storage.Timestamp(s.o.Now())
			for _, e := range pending {
				state := StateWaiting // the ban didn't happen: send it again
				switch {
				case e.State == StateSent && banned[e.UserID] && l.Phase == Undoing:
					state = StateUnban
				case e.State == StateSent && l.Phase == Undoing:
					state = StateStopped
				case e.State == StateSent && banned[e.UserID]:
					state = StateBanned
				case e.State == StateUnbanSent && banned[e.UserID]:
					state = StateUnban
				case e.State == StateUnbanSent:
					state = StateUnbanned
				}
				if _, err := tx.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, updated_at = ? WHERE list_id = ? AND name = ?",
					state, now, l.ID, e.Name); err != nil {
					return err
				}
			}
			return nil
		})
	}
	states := []string{StateUnban}
	if l.Phase == Banning {
		states = []string{StateUnban, StateWaiting}
	}
	for _, st := range states {
		next, err := s.Entries(ctx, l.ID, []string{st}, "", 1)
		if err != nil {
			return err
		}
		if len(next) == 0 {
			continue
		}
		return s.send(ctx, l, next[0])
	}
	return s.setPhase(ctx, l.ID, Done)
}

// send sends one ban or unban, saving that it was sent first.
func (s *Service) send(ctx context.Context, l List, e Entry) error {
	unban := e.State == StateUnban
	sent, back := StateSent, StateWaiting
	if unban {
		sent, back = StateUnbanSent, StateUnban
	}
	if err := s.setEntry(ctx, l.ID, e.Name, sent, ""); err != nil {
		return err
	}
	// Once sent, the answer is saved even if StreamGremlin is stopping.
	sctx := context.WithoutCancel(ctx)
	var out twitch.ListOutcome
	var detail string
	var ri twitch.RateInfo
	if unban {
		out, detail, ri = s.o.Twitch.ListUnban(sctx, e.UserID)
	} else {
		out, detail, ri = s.o.Twitch.ListBan(sctx, e.UserID, l.Reason)
	}
	s.pace(ri, out == twitch.ListWait)
	state := ""
	switch out {
	case twitch.ListDone:
		state = map[bool]string{false: StateBanned, true: StateUnbanned}[unban]
	case twitch.ListAlready:
		state, detail = StateAlready, "banned already, not by this list"
	case twitch.ListNotBanned:
		state, detail = StateUnbanned, "wasn't banned"
	case twitch.ListRefused:
		state = StateRefused
		if unban {
			state = StateBanned
		}
	case twitch.ListStop:
		if err := s.setEntry(sctx, l.ID, e.Name, back, ""); err != nil {
			return err
		}
		return &stopError{why: detail}
	case twitch.ListWait:
		return s.setEntry(sctx, l.ID, e.Name, back, "")
	default:
		// No answer: left as sent, and settled from Twitch's list next.
		s.mu.Lock()
		s.waitUntil = s.o.Now().Add(5 * time.Second)
		s.mu.Unlock()
		return nil
	}
	if err := s.setEntry(sctx, l.ID, e.Name, state, detail); err != nil {
		return err
	}
	s.noteDone(l.ID)
	return nil
}

// copyPage copies one page of Twitch's ban list.
func (s *Service) copyPage(ctx context.Context, l List) error {
	bans, next, ri, err := s.o.Twitch.BansPage(ctx, l.cursor)
	s.pace(ri, false)
	if err != nil {
		return lookupErr(err)
	}
	now := storage.Timestamp(s.o.Now())
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, b := range bans {
			name := strings.ToLower(b.Login)
			if _, ok := ParseName(name); !ok {
				name = "id:" + b.UserID
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO ban_list_entries (list_id, name, user_id, login, state, generation, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (list_id, name) DO NOTHING`, l.ID, name, b.UserID, b.Login, StateAlready, l.generation, now); err != nil {
				return err
			}
		}
		phase := Copying
		if next == "" {
			phase = Done
		}
		_, err := tx.ExecContext(ctx, "UPDATE ban_lists SET phase = ?, cursor = ?, updated_at = ? WHERE id = ?", phase, next, now, l.ID)
		return err
	})
}

// compare checks 100 of the list's bans against Twitch's list.
func (s *Service) compare(ctx context.Context, l List) error {
	batch, err := s.Entries(ctx, l.ID, []string{StateBanned}, l.cursor, twitch.MaxLookup)
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		return s.setPhase(ctx, l.ID, Done)
	}
	var ids []string
	for _, e := range batch {
		ids = append(ids, e.UserID)
	}
	banned, ri, err := s.o.Twitch.BannedAmong(ctx, ids)
	s.pace(ri, false)
	if err != nil {
		return lookupErr(err)
	}
	now := storage.Timestamp(s.o.Now())
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, e := range batch {
			if banned[e.UserID] {
				continue
			}
			if _, err := tx.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, detail = ?, updated_at = ? WHERE list_id = ? AND name = ?",
				StateByHand, "unbanned on Twitch", now, l.ID, e.Name); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE ban_lists SET cursor = ?, updated_at = ? WHERE id = ?", batch[len(batch)-1].Name, now, l.ID)
		return err
	})
}

// Left counts the requests a list still has to send.
func Left(phase string, counts map[string]int) int {
	switch phase {
	case Banning:
		return counts[StateWaiting] + counts[StateUnban] + counts[StateSent] + counts[StateUnbanSent]
	case Undoing:
		return counts[StateUnban] + counts[StateUnbanSent]
	}
	return 0
}

// Describe says a list's phase in words.
func Describe(l List) string {
	if l.Paused && l.Problem != "" {
		return "Paused: " + l.Problem
	}
	if l.Paused {
		return "Paused."
	}
	return map[string]string{Reading: "Reading the file…", Checking: "Looking the names up on Twitch…",
		Ready: "Checked: confirm to start.", Banning: "Banning…", Undoing: "Unbanning what this list banned…",
		Copying: "Copying Twitch's ban list…", Comparing: "Checking its bans are still on Twitch…",
		Done: "Done."}[l.Phase] + func() string {
		if l.Problem != "" {
			return fmt.Sprintf(" (%s)", l.Problem)
		}
		return ""
	}()
}
