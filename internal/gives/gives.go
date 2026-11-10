// Package gives gives points to everyone in a group at once
// (docs/adr/0035-giving-to-everyone.md): everyone in chat (Twitch's chatter
// list), everyone who chatted in the last N minutes, or subscribers
// (Twitch's subscriber list). The group is worked out once, when the give
// is made, and kept, so a retry pays the same people. Each payment is a
// ledger credit with the operation id give:<give>:<viewer>, so nobody is
// paid twice. A give is undone as a whole, never below zero: whoever has
// spent theirs keeps the difference, and is named.
package gives

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// Audiences.
const (
	Chat   = "chat"   // everyone on Twitch's chatter list
	Recent = "recent" // everyone who chatted in the last N minutes
	Subs   = "subs"   // everyone on Twitch's subscriber list
)

// Limits.
const (
	MaxMinutes = 240
	MaxEach    = 1_000_000
	SampleSize = 10
)

// Errors.
var (
	ErrInvalid  = errors.New("gives: invalid")
	ErrNotFound = errors.New("gives: no such give")
	// ErrEmpty: nobody is in the group. Nothing was given.
	ErrEmpty = errors.New("nobody is in that group, so nothing was given")
	// ErrList: Twitch's list couldn't be read. Nothing was given.
	ErrList = errors.New("the list from Twitch couldn't be read, so nothing was given")
	// ErrConflict: a request id was used for a different give.
	ErrConflict = errors.New("gives: that request id was used for a different give")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Who is a group.
type Who struct {
	Audience string `json:"audience"`
	Minutes  int    `json:"minutes,omitempty"` // for recent
}

// Check says what's wrong with the group, if anything.
func (w Who) Check() error {
	switch w.Audience {
	case Chat, Subs:
		if w.Minutes != 0 {
			return invalid("minutes are only for recent chatters")
		}
	case Recent:
		if w.Minutes < 1 || w.Minutes > MaxMinutes {
			return invalid("recent chatters are those in the last 1 to %d minutes", MaxMinutes)
		}
	default:
		return invalid("the group is chat, recent or subs")
	}
	return nil
}

// Words names the group.
func (w Who) Words() string {
	switch w.Audience {
	case Chat:
		return "everyone in chat"
	case Recent:
		if w.Minutes == 1 {
			return "everyone who chatted in the last minute"
		}
		return fmt.Sprintf("everyone who chatted in the last %d minutes", w.Minutes)
	case Subs:
		return "subscribers"
	}
	return w.Audience
}

// Lists are Twitch's lists.
type Lists interface {
	Chatters(ctx context.Context) ([]events.Actor, error)
	Subscribers(ctx context.Context) ([]events.Actor, error)
}

// RecentChat says who chatted lately.
type RecentChat interface {
	ChattedSince(ctx context.Context, since time.Time) ([]events.Actor, error)
}

// Options are what a Service needs.
type Options struct {
	DB      *storage.DB
	Viewers *viewers.Service
	Points  *points.Ledger
	Twitch  Lists // nil when there's no Twitch
	Recent  RecentChat
	Logger  *slog.Logger
	Now     func() time.Time
}

// Service gives to groups.
type Service struct {
	o   Options
	log *slog.Logger
	mu  sync.Mutex // one give or undo at a time
}

// New returns a Service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, log: o.Logger.With("component", "gives")}
}

// Group is who's in a group now.
type Group struct {
	Who
	Viewers int      `json:"viewers"`
	Sample  []string `json:"sample"` // up to 10 names
	// Note says why the group is empty, when it is.
	Note string `json:"note,omitempty"`
}

// members works out who's in a group now.
func (s *Service) members(ctx context.Context, w Who) ([]events.Actor, error) {
	if err := w.Check(); err != nil {
		return nil, err
	}
	var list []events.Actor
	var err error
	switch w.Audience {
	case Recent:
		if s.o.Recent == nil {
			return nil, invalid("recent chat isn't available")
		}
		list, err = s.o.Recent.ChattedSince(ctx, s.o.Now().Add(-time.Duration(w.Minutes)*time.Minute))
		if err != nil {
			return nil, err
		}
	default:
		if s.o.Twitch == nil {
			return nil, fmt.Errorf("%w: Twitch isn't set up", ErrList)
		}
		if w.Audience == Chat {
			list, err = s.o.Twitch.Chatters(ctx)
		} else {
			list, err = s.o.Twitch.Subscribers(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrList, err)
		}
	}
	seen := map[string]bool{}
	out := make([]events.Actor, 0, len(list))
	for _, a := range list {
		if a.ID == "" || a.Platform == "" || seen[a.Platform+":"+a.ID] {
			continue
		}
		seen[a.Platform+":"+a.ID] = true
		out = append(out, a)
	}
	return out, nil
}

// emptyNote says why a group is empty.
func emptyNote(w Who) string {
	switch w.Audience {
	case Chat:
		return "Twitch's list of who's in your chat is empty. Twitch updates it a little late."
	case Subs:
		return "Twitch listed no subscribers. A channel that isn't an Affiliate or Partner has none."
	}
	return fmt.Sprintf("Nobody has chatted in the last %d minutes.", w.Minutes)
}

// Preview says who's in a group now, without giving anything.
func (s *Service) Preview(ctx context.Context, w Who) (Group, error) {
	list, err := s.members(ctx, w)
	if err != nil {
		return Group{}, err
	}
	g := Group{Who: w, Viewers: len(list), Sample: []string{}}
	for _, a := range list[:min(len(list), SampleSize)] {
		g.Sample = append(g.Sample, name(a))
	}
	if len(list) == 0 {
		g.Note = emptyNote(w)
	}
	return g, nil
}

func name(a events.Actor) string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Login
}

// Request is a give.
type Request struct {
	Who
	Amount    int64  `json:"amount"` // each
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
	By        string `json:"-"`
}

// Give is one give to a group.
type Give struct {
	ID        string     `json:"id"`
	Audience  string     `json:"audience"`
	Minutes   int        `json:"minutes,omitempty"`
	Words     string     `json:"words"` // the group, in words
	Amount    int64      `json:"amount"`
	Memo      string     `json:"memo"`
	By        string     `json:"by"`
	Viewers   int        `json:"viewers"`
	Total     int64      `json:"total"`
	CreatedAt time.Time  `json:"created_at"`
	PaidAt    *time.Time `json:"paid_at,omitempty"`
	UndoneAt  *time.Time `json:"undone_at,omitempty"`
	// Applied is false when the request was seen before: nothing more was given.
	Applied bool `json:"applied"`
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "g_" + hex.EncodeToString(b)
}

// Give gives Amount to everyone in the group, once each. The group is
// worked out now and kept; the same request id again finishes and returns
// the same give, never paying anyone twice.
func (s *Service) Give(ctx context.Context, r Request) (Give, error) {
	r.Reason = strings.TrimSpace(r.Reason)
	switch {
	case r.RequestID == "" || len(r.RequestID) > 200:
		return Give{}, invalid("a request id is 1 to 200 characters")
	case r.Amount < 1 || r.Amount > MaxEach:
		return Give{}, invalid("each viewer gets 1 to %d", MaxEach)
	case utf8.RuneCountInString(r.Reason) > points.MaxMemo:
		return Give{}, invalid("a reason is up to %d characters", points.MaxMemo)
	}
	if err := r.Check(); err != nil {
		return Give{}, err
	}
	if r.By == "" {
		r.By = "the app"
	}
	if r.Reason == "" {
		r.Reason = "a give to " + r.Words()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, err := s.byRequest(ctx, r.RequestID); err == nil {
		if g.Audience != r.Audience || g.Minutes != r.Minutes || g.Amount != r.Amount || g.Memo != r.Reason {
			return Give{}, ErrConflict
		}
		return s.pay(ctx, g, false)
	} else if !errors.Is(err, ErrNotFound) {
		return Give{}, err
	}
	list, err := s.members(ctx, r.Who)
	if err != nil {
		return Give{}, err
	}
	if len(list) == 0 {
		return Give{}, fmt.Errorf("%w. %s", ErrEmpty, emptyNote(r.Who))
	}
	ids := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, a := range list {
		v, err := s.o.Viewers.Ensure(ctx, a)
		if err != nil {
			return Give{}, err
		}
		if !seen[v.ID] {
			seen[v.ID] = true
			ids = append(ids, v.ID)
		}
	}
	g := Give{ID: newID(), Audience: r.Audience, Minutes: r.Minutes, Words: r.Words(), Amount: r.Amount, Memo: r.Reason, By: r.By,
		Viewers: len(ids), Total: r.Amount * int64(len(ids)), CreatedAt: s.o.Now().UTC()}
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return Give{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO points_gives (id, request_id, audience, minutes, amount, memo, by, viewers, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, g.ID, r.RequestID, g.Audience, g.Minutes, g.Amount, g.Memo, g.By, g.Viewers, storage.Timestamp(g.CreatedAt)); err != nil {
		return Give{}, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "INSERT INTO points_give_members (give, viewer) VALUES (?, ?)", g.ID, id); err != nil {
			return Give{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Give{}, err
	}
	s.log.Info("giving to a group", "give", g.ID, "audience", g.Audience, "viewers", g.Viewers, "each", g.Amount, "by", g.By)
	return s.pay(ctx, g, true)
}

func opID(give, viewer string) string { return "give:" + give + ":" + viewer }

// pay pays every member not yet paid, unless the give was undone.
func (s *Service) pay(ctx context.Context, g Give, fresh bool) (Give, error) {
	if g.PaidAt == nil && g.UndoneAt == nil {
		rows, err := s.o.DB.QueryContext(ctx, "SELECT viewer FROM points_give_members WHERE give = ? ORDER BY rowid", g.ID)
		if err != nil {
			return g, err
		}
		var members []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				_ = rows.Close()
				return g, err
			}
			members = append(members, v)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return g, err
		}
		for _, v := range members {
			_, err := s.o.Points.Credit(ctx, points.Op{ID: opID(g.ID, v), Viewer: v, Amount: g.Amount, Memo: g.Memo, By: g.By})
			if err != nil && !errors.Is(err, points.ErrConflict) {
				return g, err
			}
		}
		now := s.o.Now().UTC()
		if _, err := s.o.DB.ExecContext(ctx, "UPDATE points_gives SET paid_at = ? WHERE id = ?", storage.Timestamp(now), g.ID); err != nil {
			return g, err
		}
		g.PaidAt = &now
		s.log.Info("gave to a group", "give", g.ID, "viewers", g.Viewers)
	}
	g.Applied = fresh
	return g, nil
}

const giveColumns = "id, audience, minutes, amount, memo, by, viewers, created_at, paid_at, undone_at"

type scanner interface{ Scan(...any) error }

func scan(sc scanner) (Give, error) {
	var g Give
	var at string
	var paid, undone sql.NullString
	if err := sc.Scan(&g.ID, &g.Audience, &g.Minutes, &g.Amount, &g.Memo, &g.By, &g.Viewers, &at, &paid, &undone); err != nil {
		return g, err
	}
	g.CreatedAt, _ = storage.ParseTimestamp(at)
	for _, p := range []struct {
		src sql.NullString
		dst **time.Time
	}{{paid, &g.PaidAt}, {undone, &g.UndoneAt}} {
		if p.src.Valid {
			t, _ := storage.ParseTimestamp(p.src.String)
			*p.dst = &t
		}
	}
	g.Words = Who{Audience: g.Audience, Minutes: g.Minutes}.Words()
	g.Total = g.Amount * int64(g.Viewers)
	return g, nil
}

func (s *Service) one(ctx context.Context, where string, arg any) (Give, error) {
	g, err := scan(s.o.DB.QueryRowContext(ctx, "SELECT "+giveColumns+" FROM points_gives WHERE "+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

func (s *Service) byRequest(ctx context.Context, requestID string) (Give, error) {
	return s.one(ctx, "request_id = ?", requestID)
}

// Get returns a give.
func (s *Service) Get(ctx context.Context, id string) (Give, error) {
	return s.one(ctx, "id = ?", id)
}

// List returns the newest gives first.
func (s *Service) List(ctx context.Context, limit int) ([]Give, error) {
	rows, err := s.o.DB.QueryContext(ctx, "SELECT "+giveColumns+" FROM points_gives ORDER BY created_at DESC, rowid DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Give{}
	for rows.Next() {
		g, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UndoResult is how undoing a give went.
type UndoResult struct {
	Give   Give `json:"give"`
	Undone int  `json:"undone"` // payments taken back, now or before
	// Short names the viewers who have spent some of it, and keep theirs:
	// nobody goes below zero. Undoing again later tries them again.
	Short []string `json:"short"`
}

// Undo takes a give back from everyone, never below zero. The give is
// undone once everyone's payment is.
func (s *Service) Undo(ctx context.Context, id string) (UndoResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.Get(ctx, id)
	if err != nil {
		return UndoResult{}, err
	}
	txs, err := s.o.Points.ByOpPrefix(ctx, "give:"+g.ID+":")
	if err != nil {
		return UndoResult{}, err
	}
	res := UndoResult{Short: []string{}}
	for _, t := range txs {
		if t.ReversedBy != "" {
			res.Undone++
			continue
		}
		_, err := s.o.Points.Undo(ctx, t.ID, points.Op{ID: "undo:" + t.ID, Memo: "undoes a give: " + g.Memo})
		switch {
		case err == nil, errors.Is(err, points.ErrReversed):
			res.Undone++
		case errors.Is(err, points.ErrInsufficient):
			n := t.Viewer
			if v, err := s.o.Viewers.Get(ctx, t.Viewer); err == nil {
				n = v.DisplayName
			}
			res.Short = append(res.Short, n)
		default:
			return res, err
		}
	}
	if len(res.Short) == 0 && g.UndoneAt == nil {
		now := s.o.Now().UTC()
		if _, err := s.o.DB.ExecContext(ctx, "UPDATE points_gives SET undone_at = ? WHERE id = ?", storage.Timestamp(now), g.ID); err != nil {
			return res, err
		}
		g.UndoneAt = &now
	}
	res.Give = g
	s.log.Info("give undone", "give", g.ID, "undone", res.Undone, "short", len(res.Short))
	return res, nil
}
