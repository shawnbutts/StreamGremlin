// Package viewers keeps a record per platform account that has taken part
// (docs/adr/0032-viewers-and-points.md): their names, when they were first
// and last seen, and how much they've chatted. Records come only from real
// events with a viewer, never test events, read in order from the inbox
// with a saved cursor so nothing counts twice. Chat text isn't kept here.
// A viewer can be exported, and forgotten.
package viewers

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

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Errors.
var (
	ErrNotFound = errors.New("viewers: no such viewer")
	ErrInvalid  = errors.New("viewers: invalid")
)

// Viewer is one platform account.
type Viewer struct {
	ID          string     `json:"id"`
	Platform    string     `json:"platform"`
	UserID      string     `json:"user_id"`
	Login       string     `json:"login"`
	DisplayName string     `json:"display_name"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	LastChatAt  *time.Time `json:"last_chat_at,omitempty"`
	Messages    int        `json:"messages"`
	ForgottenAt *time.Time `json:"forgotten_at,omitempty"`
}

// Service keeps viewer records.
type Service struct {
	db    *storage.DB
	inbox *events.Inbox
	log   *slog.Logger
	now   func() time.Time

	mu   sync.Mutex // one reader of the inbox at a time
	stop context.CancelFunc
	done chan struct{}
	once sync.Once
}

// New returns a Service. Call Start to follow the inbox.
func New(db *storage.DB, inbox *events.Inbox, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, inbox: inbox, log: log.With("component", "viewers"), now: time.Now}
}

func ts(t time.Time) string { return storage.Timestamp(t) }

// Start catches up with the inbox, then follows it.
func (s *Service) Start(ctx context.Context) error {
	if err := s.CatchUp(ctx); err != nil {
		return err
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			case <-s.inbox.Notifier().C():
			}
			if err := s.CatchUp(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("couldn't record viewers", "err", err)
			}
		}
	}()
	return nil
}

// Close stops following. Closing twice is harmless.
func (s *Service) Close() {
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
			<-s.done
		}
	})
}

// CatchUp records viewers from the events since the cursor. Each batch and
// the cursor are saved together, so an event never counts twice.
func (s *Service) CatchUp(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		var cursor int64
		if err := s.db.QueryRowContext(ctx, "SELECT seq FROM viewer_cursor WHERE id = 1").Scan(&cursor); err != nil {
			return err
		}
		recs, err := s.inbox.After(ctx, cursor, 500, nil)
		if err != nil || len(recs) == 0 {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if err := s.record(ctx, tx, r); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE viewer_cursor SET seq = ? WHERE id = 1", recs[len(recs)-1].Seq); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if len(recs) < 500 {
			return nil
		}
	}
}

// record notes the viewer behind a real event. Names follow the newest
// event; first and last seen never move the wrong way, even out of order.
func (s *Service) record(ctx context.Context, tx *sql.Tx, r events.Record) error {
	a := r.Actor
	if r.DeliveryQuality == events.Synthetic || events.LogOnly(r) || a == nil || a.ID == "" || a.Platform == "" || r.Source == "core" {
		return nil
	}
	login := strings.ToLower(strings.TrimSpace(a.Login))
	name := strings.TrimSpace(a.DisplayName)
	if name == "" {
		name = login
	}
	at := ts(r.ReceivedAt)
	chat, chatAt := 0, any(nil)
	if r.Type == "chat.message.received" {
		chat, chatAt = 1, at
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO viewers (id, platform, user_id, login, display_name, first_seen, last_seen, last_chat_at, messages)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (platform, user_id) DO UPDATE SET
			login = CASE WHEN excluded.last_seen >= viewers.last_seen AND excluded.login != '' THEN excluded.login ELSE viewers.login END,
			display_name = CASE WHEN excluded.last_seen >= viewers.last_seen AND excluded.display_name != '' THEN excluded.display_name ELSE viewers.display_name END,
			first_seen = min(viewers.first_seen, excluded.first_seen),
			last_seen = max(viewers.last_seen, excluded.last_seen),
			last_chat_at = CASE WHEN excluded.last_chat_at IS NULL THEN viewers.last_chat_at
				WHEN viewers.last_chat_at IS NULL THEN excluded.last_chat_at ELSE max(viewers.last_chat_at, excluded.last_chat_at) END,
			messages = viewers.messages + excluded.messages`,
		newID(), a.Platform, a.ID, login, name, at, at, chatAt, chat)
	return err
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "v_" + hex.EncodeToString(b)
}

// Ensure returns the viewer for a platform account, making the record if
// it's new (a step giving points to someone StreamGremlin hasn't recorded
// yet, say).
func (s *Service) Ensure(ctx context.Context, a events.Actor) (Viewer, error) {
	if a.Platform == "" || a.ID == "" {
		return Viewer{}, fmt.Errorf("%w: a viewer has a platform and an id", ErrInvalid)
	}
	if v, err := s.ByAccount(ctx, a.Platform, a.ID); err == nil {
		return v, nil
	}
	now := ts(s.now())
	name := a.DisplayName
	if name == "" {
		name = a.Login
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO viewers (id, platform, user_id, login, display_name, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (platform, user_id) DO NOTHING`, newID(), a.Platform, a.ID, strings.ToLower(a.Login), name, now, now)
	if err != nil {
		return Viewer{}, err
	}
	return s.ByAccount(ctx, a.Platform, a.ID)
}

const columns = "id, platform, user_id, login, display_name, first_seen, last_seen, last_chat_at, messages, forgotten_at"

type scanner interface{ Scan(...any) error }

func scan(sc scanner) (Viewer, error) {
	var v Viewer
	var first, last string
	var chat, forgot sql.NullString
	if err := sc.Scan(&v.ID, &v.Platform, &v.UserID, &v.Login, &v.DisplayName, &first, &last, &chat, &v.Messages, &forgot); err != nil {
		return v, err
	}
	v.FirstSeen, _ = storage.ParseTimestamp(first)
	v.LastSeen, _ = storage.ParseTimestamp(last)
	for _, p := range []struct {
		src sql.NullString
		dst **time.Time
	}{{chat, &v.LastChatAt}, {forgot, &v.ForgottenAt}} {
		if p.src.Valid {
			t, _ := storage.ParseTimestamp(p.src.String)
			*p.dst = &t
		}
	}
	return v, nil
}

func (s *Service) one(ctx context.Context, where string, args ...any) (Viewer, error) {
	v, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM viewers WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// Get returns a viewer by id.
func (s *Service) Get(ctx context.Context, id string) (Viewer, error) {
	return s.one(ctx, "id = ?", id)
}

// ByAccount returns the viewer for a platform account.
func (s *Service) ByAccount(ctx context.Context, platform, userID string) (Viewer, error) {
	return s.one(ctx, "platform = ? AND user_id = ?", platform, userID)
}

// ByLogin returns the viewer most recently seen with a login ("@" allowed).
func (s *Service) ByLogin(ctx context.Context, login string) (Viewer, error) {
	login = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(login), "@"))
	if login == "" {
		return Viewer{}, ErrNotFound
	}
	return s.one(ctx, "lower(login) = ? AND forgotten_at IS NULL ORDER BY last_seen DESC LIMIT 1", login)
}

// Search lists viewers whose login or name starts with q (any case; all
// when empty), most recently seen first. Forgotten viewers aren't listed.
func (s *Service) Search(ctx context.Context, q string, limit int) ([]Viewer, error) {
	q = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q), "@"))
	esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+` FROM viewers WHERE forgotten_at IS NULL
		AND (lower(login) LIKE ? ESCAPE '\' OR lower(display_name) LIKE ? ESCAPE '\') ORDER BY last_seen DESC LIMIT ?`, esc, esc, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Viewer{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// All lists every viewer, forgotten ones too (unnamed), first seen first:
// for exporting everyone.
func (s *Service) All(ctx context.Context) ([]Viewer, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM viewers ORDER BY first_seen, id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Viewer{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Forget blanks a viewer: their account id, names and activity. The row
// stays, so the ledger still adds up; if the account takes part again, it
// becomes a new, unconnected viewer.
func (s *Service) Forget(ctx context.Context, id string) (Viewer, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE viewers SET user_id = 'forgotten:' || id, login = '', display_name = 'Forgotten viewer',
		last_chat_at = NULL, messages = 0, forgotten_at = ? WHERE id = ? AND forgotten_at IS NULL`, ts(s.now()), id)
	if err != nil {
		return Viewer{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return Viewer{}, err
		}
	}
	s.log.Info("viewer forgotten", "viewer", id)
	return s.Get(ctx, id)
}
