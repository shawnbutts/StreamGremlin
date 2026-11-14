// Package moderation sends the streamer's moderating requests to Twitch
// or YouTube and records each one with the platform's answer (M7 slice 3,
// M6 slice 5; docs/adr/0041-moderation-record.md). Neither gives these
// requests an idempotency key, so a request is sent at most once: a request id makes a
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
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
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
	// FromWorkflow is a workflow's step (D-100).
	FromWorkflow = "workflow"
)

// Platforms.
const (
	PlatformTwitch  = "twitch"
	PlatformYouTube = "youtube"
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

// YouTube is what moderating needs from the YouTube adapter.
type YouTube interface {
	Moderate(ctx context.Context, m youtube.Mod) (youtube.ModOutcome, error)
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
	// On is shield_mode's on or off (D-105).
	On   *bool  `json:"on,omitempty"`
	From string `json:"from"`
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
	// ResultID is what YouTube made (a ban or a moderator), so it can be
	// undone.
	ResultID string `json:"result_id,omitempty"`
	// On is shield_mode's on or off; Shield is Shield Mode after it, when
	// Twitch said (D-105).
	On     *bool          `json:"on,omitempty"`
	Shield *twitch.Shield `json:"shield,omitempty"`
	// Lock is what a lock changed and what was there before; UnlockOf is
	// the lock an unlock undid.
	Lock     *LockDetail `json:"lock,omitempty"`
	UnlockOf string      `json:"unlock_of,omitempty"`
}

// Service sends and records.
type Service struct {
	// lockMu keeps one lock or unlock at a time (lock.go).
	lockMu sync.Mutex
	db     *storage.DB
	tw     Twitch
	yt     YouTube
	log    *slog.Logger
	now    func() time.Time
}

// New returns a Service.
func New(db *storage.DB, tw Twitch, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, tw: tw, log: log.With("component", "moderation"), now: time.Now}
}

// UseYouTube lets requests for platform youtube through. Until it's
// called they're refused as invalid.
func (s *Service) UseYouTube(yt YouTube) { s.yt = yt }

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
		answer = 'StreamGremlin stopped before the answer came, so it may or may not have happened.' WHERE state = 'pending'`,
		storage.Timestamp(s.now()))
	return err
}

func (s *Service) check(r Request) error {
	switch {
	case r.From != FromLive && r.From != FromCLI && r.From != FromFilter && r.From != FromWorkflow:
		return invalid("a request comes from live, cli, filter or workflow")
	case r.Platform != PlatformTwitch && r.Platform != PlatformYouTube:
		return invalid("platform must be twitch or youtube")
	case r.Platform == PlatformYouTube && s.yt == nil:
		return invalid("YouTube isn't set up")
	case r.RequestID == "" || len(r.RequestID) > maxRequest || !requestPattern.MatchString(r.RequestID):
		return invalid("a request id is 1 to %d letters, digits, - and _", maxRequest)
	case len(r.Login) > 64:
		return invalid("a login is up to 64 characters")
	}
	if r.Platform == PlatformYouTube {
		return checkYouTube(r)
	}
	switch r.Action {
	case ShieldMode, Lock, Unlock:
		if r.UserID != "" || r.MessageID != "" || r.Seconds != 0 || r.Reason != "" || r.Chat != nil {
			return invalid("%s takes nothing but on", r.Action)
		}
		if (r.Action == ShieldMode) != (r.On != nil) {
			return invalid("shield_mode takes on, true or false; lock and unlock take nothing")
		}
		return nil
	}
	if r.On != nil {
		return invalid("only shield_mode takes on")
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
		rec.Seconds == r.Seconds && rec.Reason == r.Reason && string(a) == string(b) && (rec.On == nil) == (r.On == nil) &&
		(rec.On == nil || *rec.On == *r.On)
}

// Do sends a request once and records it with Twitch's answer. A request
// id seen before returns the stored record, with dup true, and sends
// nothing.
func (s *Service) Do(ctx context.Context, r Request) (Record, bool, error) {
	if err := s.check(r); err != nil {
		if errors.Is(err, twitch.ErrInvalid) || errors.Is(err, youtube.ErrInvalid) {
			return Record{}, false, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return Record{}, false, err
	}
	var ytMod youtube.Mod
	if r.Platform == PlatformYouTube {
		var err error
		if ytMod, err = s.youtubeMod(ctx, r); err != nil {
			return Record{}, false, err
		}
	}
	if r.Action == Lock || r.Action == Unlock {
		// One at a time: a lock records what was there before it.
		s.lockMu.Lock()
		defer s.lockMu.Unlock()
	}
	chat := ""
	if r.Chat != nil {
		b, err := json.Marshal(r.Chat)
		if err != nil {
			return Record{}, false, err
		}
		chat = string(b)
	}
	if r.On != nil {
		chat = fmt.Sprintf(`{"on":%t}`, *r.On)
	}
	rec := Record{ID: newID(), RequestID: r.RequestID, Action: r.Action, Platform: r.Platform, UserID: r.UserID, Login: r.Login,
		MessageID: r.MessageID, Seconds: r.Seconds, Reason: r.Reason, Chat: r.Chat, On: r.On, From: r.From, State: Pending, CreatedAt: s.now().UTC()}
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
	detail := ""
	switch {
	case r.Action == ShieldMode || r.Action == Lock || r.Action == Unlock:
		out, detail = s.shieldOrLock(send, &rec)
	case r.Action == ChatModes:
		var cs twitch.ChatSettings
		cs, out, err = s.tw.ChangeChatSettings(send, *r.Chat)
		if err == nil && out.State == Done {
			rec.Settings = &cs
		}
	case r.Platform == PlatformYouTube:
		var yo youtube.ModOutcome
		yo, err = s.yt.Moderate(send, ytMod)
		out, rec.ResultID = twitch.ModOutcome{State: yo.State, Detail: yo.Detail}, yo.ID
	default:
		out, err = s.tw.Moderate(send, twitch.Mod{Action: r.Action, UserID: r.UserID, MessageID: r.MessageID, Seconds: r.Seconds, Reason: r.Reason})
	}
	if err != nil {
		// Checked above, so this is unexpected: nothing was sent.
		out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
	}
	at := s.now().UTC()
	rec.State, rec.Answer, rec.AnsweredAt = out.State, out.Detail, &at
	if detail == "" {
		detail = chat
	}
	if _, err := s.db.ExecContext(send, `UPDATE moderation SET state = ?, answer = ?, answered_at = ?, result_id = ?, detail_json = ? WHERE id = ?`,
		rec.State, rec.Answer, storage.Timestamp(at), rec.ResultID, detail, rec.ID); err != nil {
		return Record{}, false, fmt.Errorf("moderation: record the answer: %w", err)
	}
	s.log.Info("moderated", "platform", rec.Platform, "action", rec.Action, "user_id", rec.UserID, "login", rec.Login, "from", rec.From, "state", rec.State)
	return rec, false, nil
}

const selectRecord = `SELECT id, request_id, action, platform, user_id, user_login, message_id, seconds, reason, detail_json,
	from_where, state, answer, created_at, answered_at, result_id FROM moderation `

func scan(sc interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var chat, created string
	var answered sql.NullString
	if err := sc.Scan(&r.ID, &r.RequestID, &r.Action, &r.Platform, &r.UserID, &r.Login, &r.MessageID, &r.Seconds, &r.Reason, &chat,
		&r.From, &r.State, &r.Answer, &created, &answered, &r.ResultID); err != nil {
		return Record{}, err
	}
	if chat != "" {
		if err := r.readDetail(chat); err != nil {
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

// Actions are the actions a request may name, on either platform.
func Actions() []string {
	return append(slices.Clone(twitch.ModActions), ChatModes, ShieldMode, Lock, Unlock)
}

// checkYouTube checks a YouTube request: YouTube has no chat modes,
// warnings, shoutouts, VIPs or suspicious-user treatment. A reason is kept
// in the record only: YouTube takes none.
func checkYouTube(r Request) error {
	if r.Chat != nil || r.Action == ChatModes {
		return invalid("YouTube's chat modes can only be changed in YouTube Studio")
	}
	if r.Reason != "" && r.Action != youtube.ModBan && r.Action != youtube.ModTimeout {
		return invalid("only a ban or a timeout has a reason")
	}
	if len([]rune(r.Reason)) > twitch.MaxModReason {
		return invalid("a reason is up to %d characters", twitch.MaxModReason)
	}
	m := youtube.Mod{Action: r.Action, ChannelID: r.UserID, MessageID: r.MessageID, Seconds: r.Seconds}
	if r.Action == youtube.ModUnban {
		m.BanID = "checked-later" // found in the record by youtubeMod
	}
	if r.Action == youtube.ModDelete {
		m.ChannelID = ""
	}
	return youtube.CheckMod(m)
}

// youtubeMod makes the request YouTube is sent. Undoing a ban needs the
// ban's id from when StreamGremlin made it; removing a moderator uses
// theirs when StreamGremlin added them (else YouTube is asked).
func (s *Service) youtubeMod(ctx context.Context, r Request) (youtube.Mod, error) {
	m := youtube.Mod{Action: r.Action, ChannelID: r.UserID, MessageID: r.MessageID, Seconds: r.Seconds}
	if r.Action == youtube.ModDelete {
		m.ChannelID = ""
	}
	switch r.Action {
	case youtube.ModUnban:
		ban, err := s.YouTubeBan(ctx, r.UserID)
		if err != nil {
			return m, err
		}
		if ban == nil {
			return m, invalid("YouTube can only undo a ban or timeout made from StreamGremlin: undo this one in YouTube Studio")
		}
		m.BanID = ban.ResultID
	case youtube.ModUnmod:
		last, ok, err := s.latestDone(ctx, r.UserID, youtube.ModMod, youtube.ModUnmod)
		if err != nil {
			return m, err
		}
		if ok && last.Action == youtube.ModMod {
			m.ModeratorID = last.ResultID
		}
	}
	return m, nil
}

// YouTubeBan is the ban or timeout StreamGremlin made on a YouTube
// viewer that hasn't been undone from here, or nil: only it can be undone.
func (s *Service) YouTubeBan(ctx context.Context, channelID string) (*Record, error) {
	last, ok, err := s.latestDone(ctx, channelID, youtube.ModBan, youtube.ModTimeout, youtube.ModUnban)
	if err != nil || !ok || last.Action == youtube.ModUnban || last.ResultID == "" {
		return nil, err
	}
	return &last, nil
}

// latestDone is the newest done YouTube request for a viewer among actions.
func (s *Service) latestDone(ctx context.Context, channelID string, actions ...string) (Record, bool, error) {
	q := selectRecord + `WHERE platform = 'youtube' AND user_id = ? AND state = 'done' AND action IN (?` +
		strings.Repeat(", ?", len(actions)-1) + `) ORDER BY created_at DESC, rowid DESC LIMIT 1`
	args := []any{channelID}
	for _, a := range actions {
		args = append(args, a)
	}
	rec, err := scan(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	return rec, err == nil, err
}
