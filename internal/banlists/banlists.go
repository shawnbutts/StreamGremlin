// Package banlists keeps the streamer's own ban lists and applies them to
// Twitch (M8 slice 2, D-106, docs/adr/0049-ban-lists.md). A list is read
// from a file, each name looked up and checked (already banned, you, your
// mods and VIPs, viewers with points), shown before anything is banned,
// then banned one account at a time at Twitch's pace. Each name's state is
// saved as it goes, so a run carries on after a crash or restart; a ban
// whose answer never came is checked before it's sent again. Importing a
// newer file bans only new names and offers to unban the ones removed, and
// only bans the list made are ever undone. A ban lifted by hand on Twitch
// is never put back unless the streamer asks.
package banlists

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Phases of a list.
const (
	Reading   = "reading"   // the file is being read
	Checking  = "checking"  // names are being looked up on Twitch
	Ready     = "ready"     // checked: waiting for the streamer to confirm
	Banning   = "banning"   // banning (and unbanning removed names)
	Undoing   = "undoing"   // unbanning everything the list banned
	Copying   = "copying"   // copying Twitch's ban list into the list
	Comparing = "comparing" // checking the list's bans are still on Twitch
	Done      = "done"
)

// Entry states.
const (
	StateNew       = "new"        // read, not looked up yet
	StateNotFound  = "not_found"  // Twitch has no such account (or it's deleted or suspended)
	StateKept      = "kept"       // left alone: you, a mod, a VIP, a viewer with points
	StateAlready   = "already"    // banned on Twitch, not by this list
	StateWaiting   = "waiting"    // to be banned
	StateSent      = "sent"       // a ban was sent and no answer came: checked before anything else
	StateBanned    = "banned"     // banned by this list
	StateRefused   = "refused"    // Twitch refused to ban it
	StateByHand    = "by_hand"    // banned by this list, then unbanned on Twitch by hand
	StateRemoved   = "removed"    // banned by this list, not in the newer file: offered for unbanning
	StateUnban     = "unban"      // to be unbanned
	StateUnbanSent = "unban_sent" // an unban was sent and no answer came
	StateUnbanned  = "unbanned"   // unbanned by this list
	StateStopped   = "stopped"    // was waiting when the list was undone: never banned
)

// Limits.
const (
	MaxName   = 60
	MaxReason = 500
	// Spare is how much of Twitch's rate-limit bucket ban lists always
	// leave for chat, moderating and workflows.
	Spare = 200
)

// Errors. Bad input wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("banlists: invalid")
	ErrNotFound = errors.New("banlists: no such ban list")
	ErrBusy     = errors.New("banlists: the list is busy")
)

// Twitch is what ban lists need from Twitch.
type Twitch interface {
	LookUpUsers(ctx context.Context, logins, ids []string) ([]twitch.User, twitch.RateInfo, error)
	BannedAmong(ctx context.Context, ids []string) (map[string]bool, twitch.RateInfo, error)
	RolesAmong(ctx context.Context, ids []string) (mods, vips map[string]bool, ri twitch.RateInfo, err error)
	BansPage(ctx context.Context, after string) ([]twitch.BannedUser, string, twitch.RateInfo, error)
	ListBan(ctx context.Context, userID, reason string) (twitch.ListOutcome, string, twitch.RateInfo)
	ListUnban(ctx context.Context, userID string) (twitch.ListOutcome, string, twitch.RateInfo)
	StreamerID() string
}

// Options configure a Service.
type Options struct {
	DB     *storage.DB
	Twitch Twitch
	// Holders says which Twitch user ids hold points; nil keeps no one.
	Holders func(ctx context.Context, userIDs []string) (map[string]bool, error)
	// Live says whether you're live on Twitch now.
	Live   func() bool
	Logger *slog.Logger
	Now    func() time.Time
	// Spare replaces Spare, and Idle how long the runner waits with
	// nothing to do, in tests.
	Spare int
	Idle  time.Duration
}

// Service keeps the lists and runs them, one request at a time.
type Service struct {
	o    Options
	log  *slog.Logger
	kick chan struct{}
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu sync.Mutex
	// done is when each recent ban or unban finished, per list, for the
	// pace and the estimated finish.
	done map[string][]time.Time
	// waitingLive is the lists waiting until you're offline; waitUntil
	// when Twitch said to wait.
	waitingLive map[string]bool
	waitUntil   time.Time
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Spare == 0 {
		o.Spare = Spare
	}
	if o.Idle == 0 {
		o.Idle = 30 * time.Second
	}
	return &Service{o: o, log: o.Logger.With("component", "banlists"), kick: make(chan struct{}, 1),
		done: map[string][]time.Time{}, waitingLive: map[string]bool{}}
}

func (s *Service) poke() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// List is a ban list as stored.
type List struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Reason     string    `json:"reason"`
	Phase      string    `json:"phase"`
	Paused     bool      `json:"paused"`
	PauseLive  bool      `json:"pause_live"`
	KeepPoints bool      `json:"keep_points"`
	FileName   string    `json:"file_name,omitempty"`
	Skipped    int       `json:"skipped"`
	Problem    string    `json:"problem,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	filePath   string
	generation int
	cursor     string
}

const listColumns = `id, name, reason, phase, paused, pause_live, keep_points, file_path, file_name, generation, skipped, cursor, problem,
	created_at, updated_at`

func scanList(sc interface{ Scan(...any) error }) (List, error) {
	var l List
	var paused, live, keep int
	var created, updated string
	if err := sc.Scan(&l.ID, &l.Name, &l.Reason, &l.Phase, &paused, &live, &keep, &l.filePath, &l.FileName, &l.generation, &l.Skipped,
		&l.cursor, &l.Problem, &created, &updated); err != nil {
		return List{}, err
	}
	l.Paused, l.PauseLive, l.KeepPoints = paused != 0, live != 0, keep != 0
	var err error
	if l.CreatedAt, err = storage.ParseTimestamp(created); err != nil {
		return List{}, err
	}
	if l.UpdatedAt, err = storage.ParseTimestamp(updated); err != nil {
		return List{}, err
	}
	return l, nil
}

// Get returns one list.
func (s *Service) Get(ctx context.Context, id string) (List, error) {
	l, err := scanList(s.o.DB.QueryRowContext(ctx, "SELECT "+listColumns+" FROM ban_lists WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return List{}, ErrNotFound
	}
	return l, err
}

func (s *Service) byName(ctx context.Context, name string) (List, error) {
	l, err := scanList(s.o.DB.QueryRowContext(ctx, "SELECT "+listColumns+" FROM ban_lists WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return List{}, ErrNotFound
	}
	return l, err
}

// All returns every list, newest first.
func (s *Service) All(ctx context.Context) ([]List, error) {
	rows, err := s.o.DB.QueryContext(ctx, "SELECT "+listColumns+" FROM ban_lists ORDER BY created_at DESC, id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []List{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Counts returns how many of a list's names are in each state.
func (s *Service) Counts(ctx context.Context, id string) (map[string]int, error) {
	rows, err := s.o.DB.QueryContext(ctx, "SELECT state, count(*) FROM ban_list_entries WHERE list_id = ? GROUP BY state", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Entry is one name in a list.
type Entry struct {
	Name   string `json:"name"`
	UserID string `json:"user_id,omitempty"`
	Login  string `json:"login,omitempty"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Entries returns a list's names in the given states (all when none),
// in name order after after, up to limit.
func (s *Service) Entries(ctx context.Context, id string, states []string, after string, limit int) ([]Entry, error) {
	q, args := "SELECT name, user_id, login, state, detail FROM ban_list_entries WHERE list_id = ? AND name > ?", []any{id, after}
	if len(states) > 0 {
		q += " AND state IN (" + strings.TrimSuffix(strings.Repeat("?,", len(states)), ",") + ")"
		for _, st := range states {
			args = append(args, st)
		}
	}
	q += " ORDER BY name LIMIT ?"
	args = append(args, limit)
	rows, err := s.o.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Name, &e.UserID, &e.Login, &e.State, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "bl_" + hex.EncodeToString(b)
}

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return "", invalid("a list's name is 1 to %d characters", MaxName)
	}
	return name, nil
}

// busy phases are those the runner is working through.
func busy(phase string) bool {
	switch phase {
	case Reading, Checking, Banning, Undoing, Copying, Comparing:
		return true
	}
	return false
}

// Import reads a file into a new list, or a newer version into the list
// with that name: names new to it are checked and banned once you
// confirm, and names no longer in it are offered for unbanning. The file
// is read in the background, from where it is now.
func (s *Service) Import(ctx context.Context, name, path, reason string) (List, error) {
	name, err := checkName(name)
	if err != nil {
		return List{}, err
	}
	if !filepath.IsAbs(path) {
		return List{}, invalid("the file's path must be absolute")
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return List{}, invalid("StreamGremlin can't read that file")
	}
	f, err := os.Open(path) //nolint:gosec // the streamer's own file, read for names only
	if err != nil {
		return List{}, invalid("StreamGremlin can't read that file")
	}
	_ = f.Close()
	now := storage.Timestamp(s.o.Now())
	old, err := s.byName(ctx, name)
	switch {
	case errors.Is(err, ErrNotFound):
		if reason == "" {
			reason = "Ban list: " + name
		}
		if utf8.RuneCountInString(reason) > MaxReason {
			return List{}, invalid("a reason is up to %d characters", MaxReason)
		}
		id := newID()
		if _, err := s.o.DB.ExecContext(ctx, `INSERT INTO ban_lists (id, name, reason, phase, file_path, file_name, generation, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`, id, name, reason, Reading, path, filepath.Base(path), now, now); err != nil {
			return List{}, err
		}
		s.log.Info("ban list imported", "list", id, "file", filepath.Base(path))
		s.poke()
		return s.Get(ctx, id)
	case err != nil:
		return List{}, err
	}
	if busy(old.Phase) && !old.Paused {
		return List{}, fmt.Errorf("%w: it's %s: pause it first", ErrBusy, old.Phase)
	}
	if reason != "" && utf8.RuneCountInString(reason) > MaxReason {
		return List{}, invalid("a reason is up to %d characters", MaxReason)
	}
	if reason == "" {
		reason = old.Reason
	}
	if _, err := s.o.DB.ExecContext(ctx, `UPDATE ban_lists SET phase = ?, paused = 0, file_path = ?, file_name = ?, generation = generation + 1,
		reason = ?, skipped = 0, problem = '', updated_at = ? WHERE id = ?`, Reading, path, filepath.Base(path), reason, now, old.ID); err != nil {
		return List{}, err
	}
	s.log.Info("ban list imported again", "list", old.ID, "file", filepath.Base(path))
	s.poke()
	return s.Get(ctx, old.ID)
}

// Copy starts a new list from Twitch's own ban list: everyone banned for
// good now. Saved as a file, it's an export; the list never unbans them.
func (s *Service) Copy(ctx context.Context, name string) (List, error) {
	name, err := checkName(name)
	if err != nil {
		return List{}, err
	}
	if _, err := s.byName(ctx, name); err == nil {
		return List{}, invalid("there's a list called %q already", name)
	}
	id, now := newID(), storage.Timestamp(s.o.Now())
	if _, err := s.o.DB.ExecContext(ctx, `INSERT INTO ban_lists (id, name, reason, phase, generation, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`, id, name, "Copied from Twitch", Copying, now, now); err != nil {
		return List{}, err
	}
	s.poke()
	return s.Get(ctx, id)
}

// ConfirmOptions are what a confirm does besides banning the new names.
type ConfirmOptions struct {
	// UnbanRemoved unbans names this list banned that aren't in the
	// newer file.
	UnbanRemoved bool `json:"unban_removed"`
	// RebanByHand bans again the names someone unbanned on Twitch.
	RebanByHand bool `json:"reban_by_hand"`
}

// Confirm starts banning a checked list.
func (s *Service) Confirm(ctx context.Context, id string, o ConfirmOptions) (List, error) {
	l, err := s.Get(ctx, id)
	if err != nil {
		return List{}, err
	}
	if l.Phase != Ready && l.Phase != Done {
		return List{}, fmt.Errorf("%w: it's %s", ErrBusy, l.Phase)
	}
	now := storage.Timestamp(s.o.Now())
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if o.UnbanRemoved {
			if _, err := tx.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, updated_at = ? WHERE list_id = ? AND state = ?",
				StateUnban, now, id, StateRemoved); err != nil {
				return err
			}
		}
		if o.RebanByHand {
			if _, err := tx.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, detail = '', updated_at = ? WHERE list_id = ? AND state = ?",
				StateWaiting, now, id, StateByHand); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE ban_lists SET phase = ?, paused = 0, problem = '', updated_at = ? WHERE id = ?", Banning, now, id)
		return err
	})
	if err != nil {
		return List{}, err
	}
	s.log.Info("ban list confirmed", "list", id, "unban_removed", o.UnbanRemoved, "reban_by_hand", o.RebanByHand)
	s.poke()
	return s.Get(ctx, id)
}

// Undo unbans everything the list banned, and bans nothing more.
func (s *Service) Undo(ctx context.Context, id string) (List, error) {
	l, err := s.Get(ctx, id)
	if err != nil {
		return List{}, err
	}
	if l.Phase == Reading || l.Phase == Checking || l.Phase == Copying {
		return List{}, fmt.Errorf("%w: it's %s: wait until it's checked", ErrBusy, l.Phase)
	}
	now := storage.Timestamp(s.o.Now())
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, updated_at = ? WHERE list_id = ? AND state IN (?, ?)",
			StateUnban, now, id, StateBanned, StateRemoved); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE ban_list_entries SET state = ?, updated_at = ? WHERE list_id = ? AND state = ?",
			StateStopped, now, id, StateWaiting); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE ban_lists SET phase = ?, paused = 0, problem = '', updated_at = ? WHERE id = ?", Undoing, now, id)
		return err
	})
	if err != nil {
		return List{}, err
	}
	s.log.Info("ban list undoing", "list", id)
	s.poke()
	return s.Get(ctx, id)
}

// Compare checks the list's bans are still on Twitch: one lifted is marked
// unbanned by hand, and left alone.
func (s *Service) Compare(ctx context.Context, id string) (List, error) {
	l, err := s.Get(ctx, id)
	if err != nil {
		return List{}, err
	}
	if l.Phase != Done {
		return List{}, fmt.Errorf("%w: it's %s", ErrBusy, l.Phase)
	}
	if _, err := s.o.DB.ExecContext(ctx, "UPDATE ban_lists SET phase = ?, cursor = '', paused = 0, problem = '', updated_at = ? WHERE id = ?",
		Comparing, storage.Timestamp(s.o.Now()), id); err != nil {
		return List{}, err
	}
	s.poke()
	return s.Get(ctx, id)
}

// SetPaused pauses or resumes a list.
func (s *Service) SetPaused(ctx context.Context, id string, paused bool) (List, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return List{}, err
	}
	p := 0
	if paused {
		p = 1
	}
	if _, err := s.o.DB.ExecContext(ctx, "UPDATE ban_lists SET paused = ?, problem = CASE WHEN ? THEN problem ELSE '' END, updated_at = ? WHERE id = ?",
		p, paused, storage.Timestamp(s.o.Now()), id); err != nil {
		return List{}, err
	}
	s.poke()
	return s.Get(ctx, id)
}

// Settings are a list's choices.
type Settings struct {
	Reason     *string `json:"reason,omitempty"`
	PauseLive  *bool   `json:"pause_live,omitempty"`
	KeepPoints *bool   `json:"keep_points,omitempty"`
}

// Set changes a list's choices. Keeping viewers with points applies to
// names checked from then on.
func (s *Service) Set(ctx context.Context, id string, st Settings) (List, error) {
	l, err := s.Get(ctx, id)
	if err != nil {
		return List{}, err
	}
	if st.Reason != nil {
		r := strings.TrimSpace(*st.Reason)
		if r == "" || utf8.RuneCountInString(r) > MaxReason {
			return List{}, invalid("a reason is 1 to %d characters", MaxReason)
		}
		l.Reason = r
	}
	if st.PauseLive != nil {
		l.PauseLive = *st.PauseLive
	}
	if st.KeepPoints != nil {
		l.KeepPoints = *st.KeepPoints
	}
	if _, err := s.o.DB.ExecContext(ctx, "UPDATE ban_lists SET reason = ?, pause_live = ?, keep_points = ?, updated_at = ? WHERE id = ?",
		l.Reason, l.PauseLive, l.KeepPoints, storage.Timestamp(s.o.Now()), id); err != nil {
		return List{}, err
	}
	s.poke()
	return s.Get(ctx, id)
}

// Delete forgets a list. It doesn't unban anyone: undo it first for that.
func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.o.DB.ExecContext(ctx, "DELETE FROM ban_lists WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.mu.Lock()
	delete(s.done, id)
	delete(s.waitingLive, id)
	s.mu.Unlock()
	return nil
}

func (s *Service) tx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Progress is what the runner knows of a list now.
type Progress struct {
	// WaitingLive: it waits until you're offline on Twitch.
	WaitingLive bool `json:"waiting_live"`
	// PerMinute is the bans and unbans done in the last 5 minutes, a
	// minute; Finish when the rest would be done at that pace.
	PerMinute float64    `json:"per_minute"`
	Finish    *time.Time `json:"finish,omitempty"`
	// WaitUntil is when Twitch said requests may go on, while it waits.
	WaitUntil *time.Time `json:"wait_until,omitempty"`
}

// ProgressOf says how a list is going; left is how many requests remain.
func (s *Service) ProgressOf(id string, left int) Progress {
	now := s.o.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Progress{WaitingLive: s.waitingLive[id]}
	recent := s.done[id][:0]
	for _, t := range s.done[id] {
		if now.Sub(t) <= 5*time.Minute {
			recent = append(recent, t)
		}
	}
	s.done[id] = recent
	if len(recent) > 1 {
		span := now.Sub(recent[0])
		if span < time.Minute {
			span = time.Minute
		}
		p.PerMinute = float64(len(recent)) / span.Minutes()
		if left > 0 && p.PerMinute > 0 {
			f := now.Add(time.Duration(float64(left) / p.PerMinute * float64(time.Minute)))
			p.Finish = &f
		}
	}
	if s.waitUntil.After(now) {
		w := s.waitUntil
		p.WaitUntil = &w
	}
	return p
}
