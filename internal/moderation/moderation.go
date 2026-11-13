// Package moderation sends the streamer's moderating requests to Twitch
// and records each one with Twitch's answer (M7 slice 3;
// docs/adr/0041-moderation-record.md). Twitch gives these requests no
// idempotency key, so a request is sent at most once: a request id makes a
// repeat return the stored answer, and an answer that never came is
// recorded as unknown, never retried.
package moderation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// ChatModes is the action that changes the chat modes.
const ChatModes = "chat_modes"

// States. Pending is only seen while Twitch is being asked.
const (
	Pending = "pending"
	Done    = twitch.ModDone
	Refused = twitch.ModRefused
	Unknown = twitch.ModUnknown
)

// Where a request came from.
const (
	FromLive = "live"
	FromCLI  = "cli"
	// FromFilter is a chat filter acting by itself (D-085).
	FromFilter = "filter"
)

// Errors. Validation errors wrap ErrInvalid.
var (
	ErrInvalid  = errors.New("moderation: invalid")
	ErrConflict = errors.New("moderation: that request id was used for something else")
)

const maxRequest = 64

var requestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Twitch is what moderating needs from the Twitch adapter.
type Twitch interface {
	Moderate(ctx context.Context, m twitch.Mod) (twitch.ModOutcome, error)
	ChangeChatSettings(ctx context.Context, c twitch.ChatSettingsChange) (twitch.ChatSettings, twitch.ModOutcome, error)
}

// Request is one moderating request.
type Request struct {
	RequestID string                     `json:"request_id"`
	Action    string                     `json:"action"`
	Platform  string                     `json:"platform"`
	UserID    string                     `json:"user_id,omitempty"`
	Login     string                     `json:"login,omitempty"`
	MessageID string                     `json:"message_id,omitempty"`
	Seconds   int                        `json:"seconds,omitempty"`
	Reason    string                     `json:"reason,omitempty"`
	Chat      *twitch.ChatSettingsChange `json:"chat_modes,omitempty"`
	From      string                     `json:"from"`
}

// Record is a request as recorded, with Twitch's answer.
type Record struct {
	ID         string                     `json:"id"`
	RequestID  string                     `json:"request_id"`
	Action     string                     `json:"action"`
	Platform   string                     `json:"platform"`
	UserID     string                     `json:"user_id,omitempty"`
	Login      string                     `json:"login,omitempty"`
	MessageID  string                     `json:"message_id,omitempty"`
	Seconds    int                        `json:"seconds,omitempty"`
	Reason     string                     `json:"reason,omitempty"`
	Chat       *twitch.ChatSettingsChange `json:"chat_modes,omitempty"`
	From       string                     `json:"from"`
	State      string                     `json:"state"`
	Answer     string                     `json:"answer,omitempty"`
	CreatedAt  time.Time                  `json:"created_at"`
	AnsweredAt *time.Time                 `json:"answered_at,omitempty"`
	// Settings are the chat modes after a change, when Twitch said.
	Settings *twitch.ChatSettings `json:"settings,omitempty"`
}

// Service sends and records.
type Service struct {
	db  *storage.DB
	tw  Twitch
	log *slog.Logger
	now func() time.Time
}

// New returns a Service.
func New(db *storage.DB, tw Twitch, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, tw: tw, log: log.With("component", "moderation"), now: time.Now}
}

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "mod_" + hex.EncodeToString(b)
}

// Recover marks requests a crash left pending as unknown: Twitch may or
// may not have acted. Run it once at start, before Do.
func (s *Service) Recover(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE moderation SET state = 'unknown', answered_at = ?,
		answer = 'StreamGremlin stopped before Twitch answered, so it may or may not have happened.' WHERE state = 'pending'`,
		storage.Timestamp(s.now()))
	return err
}

func check(r Request) error {
	switch {
	case r.From != FromLive && r.From != FromCLI && r.From != FromFilter:
		return invalid("a request comes from live, cli or filter")
	case r.Platform != "twitch":
		return invalid("platform must be twitch")
	case r.RequestID == "" || len(r.RequestID) > maxRequest || !requestPattern.MatchString(r.RequestID):
		return invalid("a request id is 1 to %d letters, digits, - and _", maxRequest)
	case len(r.Login) > 64:
		return invalid("a login is up to 64 characters")
	}
	if r.Action == ChatModes {
		if r.Chat == nil {
			return invalid("chat modes need a change")
		}
		if r.UserID != "" || r.MessageID != "" || r.Seconds != 0 || r.Reason != "" {
			return invalid("chat modes take only the change")
		}
		return twitch.CheckChatSettings(*r.Chat)
	}
	if r.Chat != nil {
		return invalid("only chat_modes takes a change")
	}
	return twitch.CheckMod(twitch.Mod{Action: r.Action, UserID: r.UserID, MessageID: r.MessageID, Seconds: r.Seconds, Reason: r.Reason})
}

// same reports whether a stored record is the request again.
func same(rec Record, r Request) bool {
	a, _ := json.Marshal(rec.Chat)
	b, _ := json.Marshal(r.Chat)
	return rec.Action == r.Action && rec.Platform == r.Platform && rec.UserID == r.UserID && rec.MessageID == r.MessageID &&
		rec.Seconds == r.Seconds && rec.Reason == r.Reason && string(a) == string(b)
}

// Do sends a request once and records it with Twitch's answer. A request
// id seen before returns the stored record, with dup true, and sends
// nothing.
func (s *Service) Do(ctx context.Context, r Request) (Record, bool, error) {
	if err := check(r); err != nil {
		if errors.Is(err, twitch.ErrInvalid) {
			return Record{}, false, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return Record{}, false, err
	}
	chat := ""
	if r.Chat != nil {
		b, err := json.Marshal(r.Chat)
		if err != nil {
			return Record{}, false, err
		}
		chat = string(b)
	}
	rec := Record{ID: newID(), RequestID: r.RequestID, Action: r.Action, Platform: r.Platform, UserID: r.UserID, Login: r.Login,
		MessageID: r.MessageID, Seconds: r.Seconds, Reason: r.Reason, Chat: r.Chat, From: r.From, State: Pending, CreatedAt: s.now().UTC()}
	res, err := s.db.ExecContext(ctx, `INSERT INTO moderation (id, request_id, action, platform, user_id, user_login, message_id, seconds,
		reason, detail_json, from_where, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)
		ON CONFLICT (request_id) DO NOTHING`,
		rec.ID, rec.RequestID, rec.Action, rec.Platform, rec.UserID, rec.Login, rec.MessageID, rec.Seconds, rec.Reason, chat, rec.From,
		storage.Timestamp(rec.CreatedAt))
	if err != nil {
		return Record{}, false, fmt.Errorf("moderation: record: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		old, err := s.byRequest(ctx, r.RequestID)
		if err != nil {
			return Record{}, false, err
		}
		if !same(old, r) {
			return Record{}, false, ErrConflict
		}
		return old, true, nil
	}

	// Sent once. Not under the request's context: once asked, the answer
	// is recorded even if the caller goes away.
	send := context.WithoutCancel(ctx)
	var out twitch.ModOutcome
	if r.Action == ChatModes {
		var cs twitch.ChatSettings
		cs, out, err = s.tw.ChangeChatSettings(send, *r.Chat)
		if err == nil && out.State == Done {
			rec.Settings = &cs
		}
	} else {
		out, err = s.tw.Moderate(send, twitch.Mod{Action: r.Action, UserID: r.UserID, MessageID: r.MessageID, Seconds: r.Seconds, Reason: r.Reason})
	}
	if err != nil {
		// Checked above, so this is unexpected: nothing was sent.
		out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
	}
	at := s.now().UTC()
	rec.State, rec.Answer, rec.AnsweredAt = out.State, out.Detail, &at
	if _, err := s.db.ExecContext(send, `UPDATE moderation SET state = ?, answer = ?, answered_at = ? WHERE id = ?`,
		rec.State, rec.Answer, storage.Timestamp(at), rec.ID); err != nil {
		return Record{}, false, fmt.Errorf("moderation: record the answer: %w", err)
	}
	s.log.Info("moderated", "action", rec.Action, "user_id", rec.UserID, "login", rec.Login, "from", rec.From, "state", rec.State)
	return rec, false, nil
}

const selectRecord = `SELECT id, request_id, action, platform, user_id, user_login, message_id, seconds, reason, detail_json,
	from_where, state, answer, created_at, answered_at FROM moderation `

func scan(sc interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var chat, created string
	var answered sql.NullString
	if err := sc.Scan(&r.ID, &r.RequestID, &r.Action, &r.Platform, &r.UserID, &r.Login, &r.MessageID, &r.Seconds, &r.Reason, &chat,
		&r.From, &r.State, &r.Answer, &created, &answered); err != nil {
		return Record{}, err
	}
	if chat != "" {
		r.Chat = &twitch.ChatSettingsChange{}
		if err := json.Unmarshal([]byte(chat), r.Chat); err != nil {
			return Record{}, err
		}
	}
	var err error
	if r.CreatedAt, err = storage.ParseTimestamp(created); err != nil {
		return Record{}, err
	}
	if answered.Valid {
		t, err := storage.ParseTimestamp(answered.String)
		if err != nil {
			return Record{}, err
		}
		r.AnsweredAt = &t
	}
	return r, nil
}

func (s *Service) byRequest(ctx context.Context, id string) (Record, error) {
	return scan(s.db.QueryRowContext(ctx, selectRecord+"WHERE request_id = ?", id))
}

// List returns the newest records, up to limit: for one account when
// userID is set, else everyone's.
func (s *Service) List(ctx context.Context, platform, userID string, limit int) ([]Record, error) {
	q, args := selectRecord+"ORDER BY created_at DESC, rowid DESC LIMIT ?", []any{limit}
	if userID != "" {
		q, args = selectRecord+"WHERE platform = ? AND user_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?", []any{platform, userID, limit}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Actions are the actions a request may name.
func Actions() []string { return append(slices.Clone(twitch.ModActions), ChatModes) }
