// Package giveaways runs chat giveaways (docs/adr/0025-giveaways.md):
// viewers enter with a chat command, every entry has the same chance, the
// entry list is fingerprinted when entries close, and each winner is
// drawn with crypto/rand and recorded so anyone can check the draw.
//
// Chat carries everything a viewer needs: how to enter, that they're in,
// when entries close, who won and how to claim. Messages are claimed
// before sending and never resent. A practice giveaway takes only test
// chat, and keeps its messages instead of sending them.
package giveaways

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Limits.
const (
	MaxTitle     = 100
	MaxWinners   = 20
	MinClaim     = 15
	MaxClaim     = 600
	MaxMinutes   = 24 * 60
	claimGrace   = 2 * time.Second // lets a claim typed just in time arrive
	staleMessage = 2 * time.Minute // a message that couldn't go out by then isn't sent late
)

// States and outcomes.
const (
	Open      = "open"
	Closed    = "closed"
	Ended     = "ended"
	Cancelled = "cancelled"

	Won          = "won"
	PendingClaim = "pending_claim"
	Claimed      = "claimed"
	Forfeited    = "forfeited"
	SetAside     = "set_aside"
)

var (
	// ErrInvalid: the settings break a rule, said in words.
	ErrInvalid = errors.New("giveaways: invalid")
	// ErrNotFound: no such giveaway.
	ErrNotFound = errors.New("giveaways: no such giveaway")
	// ErrState: the giveaway can't do that now, said in words.
	ErrState = errors.New("giveaways: not now")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

func notNow(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrState}, a...)...)
}

// Twitch is what giveaways need from Twitch: sending chat.
type Twitch interface {
	SendChat(ctx context.Context, req twitch.SendRequest) (twitch.SendResult, error)
}

// Settings open a giveaway.
type Settings struct {
	Title string `json:"title"`
	// Command enters, without "!" ("join" when empty).
	Command string `json:"command"`
	Winners int    `json:"winners"` // 1 when 0
	// ClaimSeconds, when set (15 to 600), is how long a winner has to type
	// !claim. Without it, a draw wins at once.
	ClaimSeconds int `json:"claim_seconds"`
	// ClaimCommand is the claim command ("claim" when empty).
	ClaimCommand string `json:"claim_command"`
	// Minutes, when set, closes entries by themselves after that long.
	Minutes         int  `json:"minutes"`
	SubscribersOnly bool `json:"subscribers_only"`
	// Practice takes only test chat and sends nothing.
	Practice bool `json:"practice"`
}

// Giveaway is one giveaway.
type Giveaway struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Command         string     `json:"command"`
	ClaimCommand    string     `json:"claim_command,omitempty"`
	Winners         int        `json:"winners"`
	ClaimSeconds    int        `json:"claim_seconds"`
	SubscribersOnly bool       `json:"subscribers_only"`
	Practice        bool       `json:"practice"`
	State           string     `json:"state"`
	OpenedBy        string     `json:"opened_by"`
	OpenedAt        time.Time  `json:"opened_at"`
	ClosesAt        *time.Time `json:"closes_at,omitempty"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	EntryCount      int        `json:"entry_count"`
	Fingerprint     string     `json:"fingerprint,omitempty"`
}

// Entry is one viewer in, in entry order.
type Entry struct {
	Seq         int       `json:"seq"`
	Platform    string    `json:"platform"`
	UserID      string    `json:"user_id"`
	Login       string    `json:"login"`
	DisplayName string    `json:"display_name"`
	EnteredAt   time.Time `json:"entered_at"`
}

// Draw is one winner drawn, with how.
type Draw struct {
	ID         string     `json:"id"`
	N          int        `json:"n"`
	EntrySeq   int        `json:"entry_seq"`
	PickIndex  int        `json:"pick_index"` // from 0, among Candidates
	Candidates int        `json:"candidates"` // entries not drawn before it
	Outcome    string     `json:"outcome"`
	ClaimUntil *time.Time `json:"claim_until,omitempty"`
	DrawnAt    time.Time  `json:"drawn_at"`
	SettledAt  *time.Time `json:"settled_at,omitempty"`
	SettledBy  string     `json:"settled_by,omitempty"`
	Winner     Entry      `json:"winner"`
}

// Refusal is someone turned away.
type Refusal struct {
	Platform    string    `json:"platform"`
	UserID      string    `json:"user_id"`
	Login       string    `json:"login"`
	DisplayName string    `json:"display_name"`
	Reason      string    `json:"reason"`
	RefusedAt   time.Time `json:"refused_at"`
}

// Message is something the giveaway said, or would say in practice.
type Message struct {
	ID        int64      `json:"id"`
	Text      string     `json:"text"`
	State     string     `json:"state"` // queued, sending, sent, failed, unknown or practice
	Detail    string     `json:"detail,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

// Detail is a giveaway with everything recorded about it.
type Detail struct {
	Giveaway
	Entries  []Entry   `json:"entries"`
	Draws    []Draw    `json:"draws"`
	Refusals []Refusal `json:"refusals"`
	Messages []Message `json:"messages"`
}

// Options configure a Service.
type Options struct {
	DB     *storage.DB
	Inbox  *events.Inbox
	Twitch Twitch
	// Commands refuses claims that a workflow that's on already answers.
	Commands func(claims []commands.Claim) error
	Logger   *slog.Logger
	Now      func() time.Time
	// AckEvery is how often new entries hear they're in (5 seconds).
	AckEvery time.Duration
}

// Service runs giveaways.
type Service struct {
	db       *storage.DB
	inbox    *events.Inbox
	tw       Twitch
	commands func([]commands.Claim) error
	log      *slog.Logger
	now      func() time.Time
	ackEvery time.Duration

	mu      sync.Mutex // every change, one at a time
	current *Giveaway  // the unfinished one, or nil
	// held is the commands current holds, read without mu: the workflow
	// engine asks while holding its own lock.
	held    atomic.Pointer[[]commands.Claim]
	cursor  int64
	lastAck time.Time

	wake      chan struct{} // a message is queued
	stop      context.CancelFunc
	closeOnce sync.Once
	done      chan struct{}
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.AckEvery == 0 {
		o.AckEvery = 5 * time.Second
	}
	if o.Commands == nil {
		o.Commands = func([]commands.Claim) error { return nil }
	}
	return &Service{db: o.DB, inbox: o.Inbox, tw: o.Twitch, commands: o.Commands, log: o.Logger.With("component", "giveaways"),
		now: o.Now, ackEvery: o.AckEvery, wake: make(chan struct{}, 1)}
}

func ts(t time.Time) string { return storage.Timestamp(t) }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

var commandName = regexp.MustCompile(`^[a-z0-9_]{1,25}$`)

func (st *Settings) normalise() error {
	st.Title = strings.TrimSpace(st.Title)
	st.Command = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(st.Command), "!"))
	st.ClaimCommand = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(st.ClaimCommand), "!"))
	if st.Command == "" {
		st.Command = "join"
	}
	if st.Winners == 0 {
		st.Winners = 1
	}
	switch {
	case st.Title == "" || utf8.RuneCountInString(st.Title) > MaxTitle:
		return invalid("a giveaway's title is 1 to %d characters", MaxTitle)
	case !commandName.MatchString(st.Command):
		return invalid("the entry command is letters, digits or _, up to 25, like join")
	case st.Winners < 1 || st.Winners > MaxWinners:
		return invalid("a giveaway has 1 to %d winners", MaxWinners)
	case st.ClaimSeconds != 0 && (st.ClaimSeconds < MinClaim || st.ClaimSeconds > MaxClaim):
		return invalid("the time to claim is %d to %d seconds, or none", MinClaim, MaxClaim)
	case st.Minutes < 0 || st.Minutes > MaxMinutes:
		return invalid("entries close by themselves after up to %d minutes, or not at all", MaxMinutes)
	}
	if st.ClaimSeconds == 0 {
		st.ClaimCommand = ""
		return nil
	}
	if st.ClaimCommand == "" {
		st.ClaimCommand = "claim"
	}
	if !commandName.MatchString(st.ClaimCommand) {
		return invalid("the claim command is letters, digits or _, up to 25, like claim")
	}
	if st.ClaimCommand == st.Command {
		return invalid("the entry and claim commands must differ")
	}
	return nil
}

// SetCommands sets the check against workflows' chat commands, for when
// the workflow engine is made after the giveaways.
func (s *Service) SetCommands(f func([]commands.Claim) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = f
}

// owner names a giveaway as a holder of chat commands.
func owner(g *Giveaway) string { return "giveaway:" + g.ID }

func claimsOf(g *Giveaway) []commands.Claim {
	name := "the giveaway “" + g.Title + "”"
	out := []commands.Claim{{Command: g.Command, Owner: owner(g), Name: name}}
	if g.ClaimCommand != "" {
		out = append(out, commands.Claim{Command: g.ClaimCommand, Owner: owner(g), Name: name})
	}
	return out
}

// Claims lists the chat commands the unfinished giveaway holds, so no
// workflow is turned on to answer them meanwhile.
func (s *Service) Claims() []commands.Claim {
	if p := s.held.Load(); p != nil {
		return *p
	}
	return nil
}

// setCurrent records the unfinished giveaway (nil for none).
func (s *Service) setCurrent(g *Giveaway) {
	s.current = g
	var cs []commands.Claim
	if g != nil && !g.Practice {
		cs = claimsOf(g)
	}
	s.held.Store(&cs)
}

// Open opens a giveaway for entries and announces it. by says who.
func (s *Service) Open(ctx context.Context, st Settings, by string) (Giveaway, error) {
	if err := st.normalise(); err != nil {
		return Giveaway{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		return Giveaway{}, notNow("“%s” isn't finished yet: end or cancel it first", s.current.Title)
	}
	now := s.now()
	g := Giveaway{ID: events.NewID(), Title: st.Title, Command: st.Command, ClaimCommand: st.ClaimCommand, Winners: st.Winners,
		ClaimSeconds: st.ClaimSeconds, SubscribersOnly: st.SubscribersOnly, Practice: st.Practice, State: Open, OpenedBy: by, OpenedAt: now}
	if !st.Practice {
		// A practice giveaway hears only test chat, which no workflow
		// shares with live chat, so it holds nothing.
		if err := s.commands(claimsOf(&g)); err != nil {
			return Giveaway{}, err
		}
	}
	if st.Minutes > 0 {
		t := now.Add(time.Duration(st.Minutes) * time.Minute)
		g.ClosesAt = &t
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO giveaways (id, title, command, claim_command, winners, claim_seconds,
			subscribers_only, practice, state, opened_by, opened_at, closes_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?)`,
			g.ID, g.Title, g.Command, g.ClaimCommand, g.Winners, g.ClaimSeconds, g.SubscribersOnly, g.Practice, by, ts(now),
			tsPtr(g.ClosesAt)); err != nil {
			return err
		}
		return s.say(ctx, tx, &g, openingWords(&g))
	})
	if err != nil {
		return Giveaway{}, err
	}
	s.setCurrent(&g)
	s.emit(ctx, &g, "giveaways.giveaway.opened", "opened", nil, s.publicFields(&g, nil))
	s.log.Info("giveaway opened", "giveaway", g.ID, "practice", g.Practice, "winners", g.Winners)
	return g, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func openingWords(g *Giveaway) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🎉 Giveaway: %s! Type !%s in chat to enter", g.Title, g.Command)
	if g.SubscribersOnly {
		b.WriteString(" (subscribers only)")
	}
	b.WriteString(". One entry each, and every entry has the same chance")
	if g.Winners > 1 {
		fmt.Fprintf(&b, "; %s will be drawn", plural(g.Winners, "winner", "winners"))
	}
	b.WriteString(".")
	if g.ClosesAt != nil {
		fmt.Fprintf(&b, " Entries close in %s.", plural(int(g.ClosesAt.Sub(g.OpenedAt).Round(time.Minute).Minutes()), "minute", "minutes"))
	}
	if g.ClaimCommand != "" {
		fmt.Fprintf(&b, " Winners type !%s within %d seconds to claim.", g.ClaimCommand, g.ClaimSeconds)
	}
	return b.String()
}

func (s *Service) tx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// say queues a chat message, or keeps it as practice.
func (s *Service) say(ctx context.Context, tx *sql.Tx, g *Giveaway, text string) error {
	state := "queued"
	if g.Practice {
		state = "practice"
	}
	if utf8.RuneCountInString(text) > twitch.MaxChatRunes {
		text = string([]rune(text)[:twitch.MaxChatRunes-1]) + "…"
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO giveaway_messages (giveaway_id, text, state, created_at) VALUES (?, ?, ?, ?)",
		g.ID, text, state, ts(s.now()))
	if err == nil && !g.Practice {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return err
}

// load reads a giveaway.
func (s *Service) load(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Giveaway, error) {
	var g Giveaway
	var opened string
	var closes, closed, ended sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, title, command, claim_command, winners, claim_seconds, subscribers_only, practice, state,
		opened_by, opened_at, closes_at, closed_at, ended_at, entry_count, fingerprint FROM giveaways WHERE id = ?`, id).Scan(
		&g.ID, &g.Title, &g.Command, &g.ClaimCommand, &g.Winners, &g.ClaimSeconds, &g.SubscribersOnly, &g.Practice, &g.State,
		&g.OpenedBy, &opened, &closes, &closed, &ended, &g.EntryCount, &g.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	g.OpenedAt, _ = storage.ParseTimestamp(opened)
	for _, p := range []struct {
		src sql.NullString
		dst **time.Time
	}{{closes, &g.ClosesAt}, {closed, &g.ClosedAt}, {ended, &g.EndedAt}} {
		if p.src.Valid {
			t, _ := storage.ParseTimestamp(p.src.String)
			*p.dst = &t
		}
	}
	return g, nil
}

// unfinished finds the giveaway with id that isn't finished.
func (s *Service) unfinished(id string) (*Giveaway, error) {
	if s.current == nil || s.current.ID != id {
		if _, err := s.load(context.Background(), s.db, id); err != nil {
			return nil, err
		}
		return nil, notNow("this giveaway is already finished")
	}
	return s.current, nil
}

func entriesOf(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]Entry, error) {
	rows, err := q.QueryContext(ctx, `SELECT seq, platform, user_id, login, display_name, entered_at FROM giveaway_entries
		WHERE giveaway_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var at string
		if err := rows.Scan(&e.Seq, &e.Platform, &e.UserID, &e.Login, &e.DisplayName, &at); err != nil {
			return nil, err
		}
		e.EnteredAt, _ = storage.ParseTimestamp(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

func drawsOf(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]Draw, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.id, d.n, d.entry_seq, d.pick_index, d.candidates, d.outcome, d.claim_until, d.drawn_at,
		d.settled_at, d.settled_by, e.platform, e.user_id, e.login, e.display_name, e.entered_at
		FROM giveaway_draws d JOIN giveaway_entries e ON e.giveaway_id = d.giveaway_id AND e.seq = d.entry_seq
		WHERE d.giveaway_id = ? ORDER BY d.n`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Draw{}
	for rows.Next() {
		var d Draw
		var until, settled sql.NullString
		var drawn, entered string
		if err := rows.Scan(&d.ID, &d.N, &d.EntrySeq, &d.PickIndex, &d.Candidates, &d.Outcome, &until, &drawn, &settled, &d.SettledBy,
			&d.Winner.Platform, &d.Winner.UserID, &d.Winner.Login, &d.Winner.DisplayName, &entered); err != nil {
			return nil, err
		}
		d.Winner.Seq = d.EntrySeq
		d.DrawnAt, _ = storage.ParseTimestamp(drawn)
		d.Winner.EnteredAt, _ = storage.ParseTimestamp(entered)
		if until.Valid {
			t, _ := storage.ParseTimestamp(until.String)
			d.ClaimUntil = &t
		}
		if settled.Valid {
			t, _ := storage.ParseTimestamp(settled.String)
			d.SettledAt = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CloseEntries stops entries, fixes the entry list and its fingerprint, and says
// how many entered.
func (s *Service) CloseEntries(ctx context.Context, id string) (Giveaway, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.unfinished(id)
	if err != nil {
		return Giveaway{}, err
	}
	if g.State != Open {
		return *g, nil // closing twice is harmless
	}
	return s.closeLocked(ctx, g)
}

func (s *Service) closeLocked(ctx context.Context, g *Giveaway) (Giveaway, error) {
	s.ackLocked(ctx, true) // everyone who got in hears it before the close
	now := s.now()
	next := *g
	err := s.tx(ctx, func(tx *sql.Tx) error {
		list, err := entriesOf(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		next.State, next.ClosedAt, next.EntryCount, next.Fingerprint = Closed, &now, len(list), Fingerprint(list)
		if _, err := tx.ExecContext(ctx, `UPDATE giveaways SET state = 'closed', closed_at = ?, entry_count = ?, fingerprint = ?
			WHERE id = ? AND state = 'open'`, ts(now), next.EntryCount, next.Fingerprint, g.ID); err != nil {
			return err
		}
		words := fmt.Sprintf("Entries for %s are closed: %s. Drawing soon!", g.Title, plural(len(list), "entry", "entries"))
		if len(list) == 0 {
			words = fmt.Sprintf("Entries for %s are closed, and no one entered.", g.Title)
		}
		return s.say(ctx, tx, g, words)
	})
	if err != nil {
		return Giveaway{}, err
	}
	*g = next
	s.emit(ctx, g, "giveaways.giveaway.closed", "closed", nil, s.publicFields(g, map[string]any{"entries": g.EntryCount,
		"fingerprint": g.Fingerprint}))
	s.log.Info("giveaway entries closed", "giveaway", g.ID, "entries", g.EntryCount)
	return *g, nil
}

// pick returns a uniform index in [0, n) from crypto/rand.
func pick(n int) (int, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

// Draw closes entries if they're open, then draws count winners (all
// still wanted when 0). A winner is never drawn twice, and no more are
// drawn than the giveaway's winners, so repeating it draws no one extra.
func (s *Service) Draw(ctx context.Context, id string, count int) ([]Draw, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.unfinished(id)
	if err != nil {
		return nil, err
	}
	if g.State == Open {
		if _, err := s.closeLocked(ctx, g); err != nil {
			return nil, err
		}
	}
	return s.drawLocked(ctx, g, count, "")
}

func (s *Service) drawLocked(ctx context.Context, g *Giveaway, count int, prefix string) ([]Draw, error) {
	var out []Draw
	err := s.tx(ctx, func(tx *sql.Tx) error {
		list, err := entriesOf(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		draws, err := drawsOf(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		drawn, active := map[int]bool{}, 0
		for _, d := range draws {
			drawn[d.EntrySeq] = true
			if d.Outcome == Won || d.Outcome == PendingClaim || d.Outcome == Claimed {
				active++
			}
		}
		wanted := g.Winners - active
		if wanted <= 0 {
			return notNow("all %s are drawn", plural(g.Winners, "winner", "winners"))
		}
		if count <= 0 || count > wanted {
			count = wanted
		}
		var candidates []Entry
		for _, e := range list {
			if !drawn[e.Seq] {
				candidates = append(candidates, e)
			}
		}
		if len(candidates) == 0 {
			if len(list) == 0 {
				return notNow("no one entered “%s”", g.Title)
			}
			return notNow("everyone who entered “%s” has been drawn", g.Title)
		}
		now := s.now()
		for i := 0; i < count && len(candidates) > 0; i++ {
			at, err := pick(len(candidates))
			if err != nil {
				return err
			}
			e := candidates[at]
			d := Draw{ID: events.NewID(), N: len(draws) + 1, EntrySeq: e.Seq, PickIndex: at, Candidates: len(candidates), Outcome: Won,
				DrawnAt: now, Winner: e}
			if g.ClaimSeconds > 0 {
				d.Outcome = PendingClaim
				t := now.Add(time.Duration(g.ClaimSeconds) * time.Second)
				d.ClaimUntil = &t
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO giveaway_draws (id, giveaway_id, n, entry_seq, pick_index, candidates, outcome,
				claim_until, drawn_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.ID, g.ID, d.N, d.EntrySeq, d.PickIndex, d.Candidates,
				d.Outcome, tsPtr(d.ClaimUntil), ts(now)); err != nil {
				return err
			}
			words := fmt.Sprintf("%s🎉 @%s wins %s! Congratulations!", prefix, e.Login, g.Title)
			if g.ClaimSeconds > 0 {
				words = fmt.Sprintf("%s🎉 @%s wins %s! Type !%s within %d seconds to claim it.", prefix, e.Login, g.Title, g.ClaimCommand,
					g.ClaimSeconds)
			}
			if err := s.say(ctx, tx, g, words); err != nil {
				return err
			}
			draws = append(draws, d)
			out = append(out, d)
			candidates = append(candidates[:at:at], candidates[at+1:]...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, d := range out {
		s.emit(ctx, g, "giveaways.winner.drawn", "drawn:"+d.ID, actorOf(d.Winner), s.publicFields(g, map[string]any{"draw": d.N,
			"pick_index": d.PickIndex, "candidates": d.Candidates, "claim_until": tsPtr(d.ClaimUntil), "winner": winnerFields(d.Winner)}))
		s.log.Info("giveaway winner drawn", "giveaway", g.ID, "draw", d.N, "of", d.Candidates)
	}
	return out, nil
}

// SetAside sets a winner aside (with who did it) and draws a
// replacement, who is never someone already drawn.
func (s *Service) SetAside(ctx context.Context, id, drawID, by string) ([]Draw, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.unfinished(id)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE giveaway_draws SET outcome = 'set_aside', settled_at = ?, settled_by = ?
		WHERE id = ? AND giveaway_id = ? AND outcome IN ('won', 'pending_claim', 'claimed')`, ts(s.now()), by, drawID, g.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notNow("that winner was already set aside or didn't claim in time")
	}
	s.log.Info("giveaway winner set aside", "giveaway", g.ID, "by", by)
	draws, err := s.drawLocked(ctx, g, 1, "Drawing again: ")
	if errors.Is(err, ErrState) {
		_ = s.tx(ctx, func(tx *sql.Tx) error {
			return s.say(ctx, tx, g, fmt.Sprintf("No one is left to draw for %s.", g.Title))
		})
		return nil, err
	}
	return draws, err
}

// End finishes the giveaway and congratulates its winners.
func (s *Service) End(ctx context.Context, id string) (Giveaway, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.unfinished(id)
	if err != nil {
		return Giveaway{}, err
	}
	if g.State == Open {
		return Giveaway{}, notNow("entries are still open: draw first, or cancel it")
	}
	draws, err := drawsOf(ctx, s.db, g.ID)
	if err != nil {
		return Giveaway{}, err
	}
	var names []string
	var winners []any
	for _, d := range draws {
		switch d.Outcome {
		case PendingClaim:
			return Giveaway{}, notNow("@%s still has time to claim: wait, or set them aside", d.Winner.Login)
		case Won, Claimed:
			names = append(names, "@"+d.Winner.Login)
			winners = append(winners, winnerFields(d.Winner))
		}
	}
	words := fmt.Sprintf("%s is over. Congratulations to %s! 🎉", g.Title, strings.Join(names, ", "))
	if len(names) == 0 {
		words = fmt.Sprintf("%s is over, with no winner this time.", g.Title)
	}
	return s.finishLocked(ctx, g, Ended, words, map[string]any{"winners": winners})
}

// Cancel finishes the giveaway without winners.
func (s *Service) Cancel(ctx context.Context, id string) (Giveaway, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.unfinished(id)
	if err != nil {
		return Giveaway{}, err
	}
	return s.finishLocked(ctx, g, Cancelled, fmt.Sprintf("%s was cancelled. Sorry, no winner this time.", g.Title),
		map[string]any{"winners": []any{}})
}

func (s *Service) finishLocked(ctx context.Context, g *Giveaway, state, words string, extra map[string]any) (Giveaway, error) {
	now := s.now()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE giveaways SET state = ?, ended_at = ? WHERE id = ?", state, ts(now), g.ID); err != nil {
			return err
		}
		return s.say(ctx, tx, g, words)
	})
	if err != nil {
		return Giveaway{}, err
	}
	g.State, g.EndedAt = state, &now
	done := *g
	s.setCurrent(nil)
	extra["status"] = state
	s.emit(ctx, &done, "giveaways.giveaway.ended", "ended", nil, s.publicFields(&done, extra))
	s.log.Info("giveaway finished", "giveaway", g.ID, "state", state)
	return done, nil
}

// Current is the unfinished giveaway, or nil.
func (s *Service) Current() *Giveaway {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	g := *s.current
	return &g
}

// Get returns a giveaway with its entries, draws, refusals and messages.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	g, err := s.load(ctx, s.db, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Giveaway: g}
	if d.Entries, err = entriesOf(ctx, s.db, id); err != nil {
		return d, err
	}
	if g.State == Open {
		d.EntryCount = len(d.Entries)
	}
	if d.Draws, err = drawsOf(ctx, s.db, id); err != nil {
		return d, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT platform, user_id, login, display_name, reason, refused_at FROM giveaway_refusals
		WHERE giveaway_id = ? ORDER BY refused_at`, id)
	if err != nil {
		return d, err
	}
	d.Refusals = []Refusal{}
	for rows.Next() {
		var r Refusal
		var at string
		if err := rows.Scan(&r.Platform, &r.UserID, &r.Login, &r.DisplayName, &r.Reason, &at); err != nil {
			_ = rows.Close()
			return d, err
		}
		r.RefusedAt, _ = storage.ParseTimestamp(at)
		d.Refusals = append(d.Refusals, r)
	}
	_ = rows.Close()
	mrows, err := s.db.QueryContext(ctx, `SELECT id, text, state, detail, created_at, sent_at FROM giveaway_messages
		WHERE giveaway_id = ? ORDER BY id`, id)
	if err != nil {
		return d, err
	}
	defer func() { _ = mrows.Close() }()
	d.Messages = []Message{}
	for mrows.Next() {
		var m Message
		var at string
		var sent sql.NullString
		if err := mrows.Scan(&m.ID, &m.Text, &m.State, &m.Detail, &at, &sent); err != nil {
			return d, err
		}
		m.CreatedAt, _ = storage.ParseTimestamp(at)
		if sent.Valid {
			t, _ := storage.ParseTimestamp(sent.String)
			m.SentAt = &t
		}
		d.Messages = append(d.Messages, m)
	}
	return d, mrows.Err()
}

// Recent lists the newest giveaways.
func (s *Service) Recent(ctx context.Context, n int) ([]Giveaway, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM giveaways ORDER BY opened_at DESC, rowid DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := []Giveaway{}
	for _, id := range ids {
		g, err := s.load(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		if g.State == Open {
			_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM giveaway_entries WHERE giveaway_id = ?", id).Scan(&g.EntryCount)
		}
		out = append(out, g)
	}
	return out, nil
}

func actorOf(e Entry) *events.Actor {
	return &events.Actor{Platform: e.Platform, ID: e.UserID, Login: e.Login, DisplayName: e.DisplayName}
}

func winnerFields(e Entry) map[string]any {
	return map[string]any{"user_id": e.UserID, "login": e.Login, "name": e.DisplayName, "entry": e.Seq}
}

func (s *Service) publicFields(g *Giveaway, extra map[string]any) map[string]any {
	p := map[string]any{"giveaway_id": g.ID, "title": g.Title, "command": g.Command, "winners": g.Winners, "practice": g.Practice,
		"subscribers_only": g.SubscribersOnly}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// emit stores an event about a giveaway. A practice giveaway's are test
// events, so workflows never act outside for them.
func (s *Service) emit(ctx context.Context, g *Giveaway, typ, key string, actor *events.Actor, payload map[string]any) {
	q := events.Confirmed
	if g.Practice {
		q = events.Synthetic
	}
	raw, _ := json.Marshal(payload)
	ev := events.Envelope{Type: typ, Source: "core", SourceEventID: "giveaway:" + g.ID + ":" + key, OccurredAt: s.now(), Actor: actor,
		DeliveryQuality: q, Payload: raw}
	if _, err := s.inbox.Accept(context.WithoutCancel(ctx), ev); err != nil {
		s.log.Warn("couldn't store a giveaway event", "type", typ, "err", err)
	}
}
