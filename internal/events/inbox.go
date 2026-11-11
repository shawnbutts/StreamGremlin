package events

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// ErrConflict means an event arrived under an id already used by a
// different event. It isn't stored: the first one wins.
var ErrConflict = errors.New("events: another event already has this id")

// ErrNotFound means no stored event has the id.
var ErrNotFound = errors.New("events: no such event")

// Result says what Accept did.
type Result struct {
	Record Record
	// Duplicate is true when the event was already stored; Record is then
	// the original, and nothing new happened.
	Duplicate bool
}

// Inbox stores events once and serves them by position.
type Inbox struct {
	db     *storage.DB
	notify *Notifier
	log    *slog.Logger
	now    func() time.Time
}

// NewInbox returns an inbox on db.
func NewInbox(db *storage.DB, log *slog.Logger) *Inbox {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Inbox{db: db, notify: NewNotifier(), log: log, now: time.Now}
}

// Notifier signals new events. Readers wait on it, then read from their cursor.
func (in *Inbox) Notifier() *Notifier { return in.notify }

// Accept validates and stores an event. A redelivery (same event_id, or same
// source and source_event_id, with the same content) returns the original
// with Duplicate set. Readers are woken after the event is committed.
func (in *Inbox) Accept(ctx context.Context, env Envelope) (Result, error) {
	env.Prepare(in.now())
	if err := env.Validate(); err != nil {
		return Result{}, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, env.Payload); err != nil {
		return Result{}, invalid("payload isn't valid JSON")
	}
	env.Payload = compact.Bytes()
	hash, err := contentHash(env)
	if err != nil {
		return Result{}, err
	}
	var actor any
	if env.Actor != nil {
		b, err := json.Marshal(env.Actor)
		if err != nil {
			return Result{}, err
		}
		actor = string(b)
	}
	var sourceEventID any
	if env.SourceEventID != "" {
		sourceEventID = env.SourceEventID
	}

	// One immediate (write) transaction: look for the original, else insert.
	// Checking first, rather than INSERT OR IGNORE, keeps seq free of gaps.
	tx, err := in.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("events: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	orig, origHash, err := scan(tx.QueryRowContext(ctx, selectRecord+`
		WHERE event_id = ? OR (source = ? AND source_event_id = ?)
		ORDER BY seq LIMIT 1`, env.EventID, env.Source, env.SourceEventID))
	switch {
	case err == nil:
		if origHash != hash {
			in.log.Warn("event refused: its id belongs to a different event",
				"type", env.Type, "source", env.Source, "source_event_id", env.SourceEventID, "original_seq", orig.Seq)
			return Result{}, fmt.Errorf("%w (stored as #%d)", ErrConflict, orig.Seq)
		}
		return Result{Record: orig, Duplicate: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Result{}, fmt.Errorf("events: look for a duplicate: %w", err)
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO events (event_id, type, schema_version, source, source_event_id, occurred_at,
			received_at, channel_id, actor, payload, correlation_id, causation_id, delivery_quality, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING seq`,
		env.EventID, env.Type, env.SchemaVersion, env.Source, sourceEventID,
		storage.Timestamp(env.OccurredAt), storage.Timestamp(env.ReceivedAt), env.ChannelID, actor,
		string(env.Payload), env.CorrelationID, env.CausationID, string(env.DeliveryQuality), hash,
	).Scan(&seq); err != nil {
		return Result{}, fmt.Errorf("events: store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("events: commit: %w", err)
	}
	in.notify.Notify()
	env.ReceivedAt = env.ReceivedAt.UTC().Truncate(time.Millisecond)
	env.OccurredAt = env.OccurredAt.UTC().Truncate(time.Millisecond)
	return Result{Record: Record{Seq: seq, Envelope: env}}, nil
}

// contentHash covers what makes two deliveries the same event. It leaves
// out event_id and received_at, which a redelivery may change.
func contentHash(e Envelope) (string, error) {
	b, err := json.Marshal(struct {
		Type          string          `json:"type"`
		SchemaVersion int             `json:"v"`
		Source        string          `json:"source"`
		SourceEventID string          `json:"sid"`
		OccurredAt    string          `json:"at"`
		ChannelID     string          `json:"ch"`
		Actor         *Actor          `json:"actor"`
		Payload       json.RawMessage `json:"payload"`
		Quality       Quality         `json:"q"`
	}{e.Type, e.SchemaVersion, e.Source, e.SourceEventID, storage.Timestamp(e.OccurredAt), e.ChannelID, e.Actor, e.Payload, e.DeliveryQuality})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

const selectRecord = `SELECT seq, event_id, type, schema_version, source, source_event_id, occurred_at,
	received_at, channel_id, actor, payload, correlation_id, causation_id, delivery_quality, content_hash
	FROM events `

type scanner interface{ Scan(...any) error }

func scan(s scanner) (Record, string, error) {
	var r Record
	var sid, actor sql.NullString
	var occurred, received, payload, quality, hash string
	if err := s.Scan(&r.Seq, &r.EventID, &r.Type, &r.SchemaVersion, &r.Source, &sid, &occurred,
		&received, &r.ChannelID, &actor, &payload, &r.CorrelationID, &r.CausationID, &quality, &hash); err != nil {
		return Record{}, "", err
	}
	r.SourceEventID = sid.String
	r.DeliveryQuality = Quality(quality)
	r.Payload = json.RawMessage(payload)
	var err error
	if r.OccurredAt, err = storage.ParseTimestamp(occurred); err != nil {
		return Record{}, "", err
	}
	if r.ReceivedAt, err = storage.ParseTimestamp(received); err != nil {
		return Record{}, "", err
	}
	if actor.Valid {
		r.Actor = &Actor{}
		if err := json.Unmarshal([]byte(actor.String), r.Actor); err != nil {
			return Record{}, "", err
		}
	}
	return r, hash, nil
}

// Get returns the event with event_id id.
func (in *Inbox) Get(ctx context.Context, id string) (Record, error) {
	r, _, err := scan(in.db.QueryRowContext(ctx, selectRecord+"WHERE event_id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("events: get: %w", err)
	}
	return r, nil
}

// BySource returns the event a source stored under its own id.
func (in *Inbox) BySource(ctx context.Context, source, id string) (Record, error) {
	return in.bySourceEventID(ctx, source, id)
}

func (in *Inbox) bySourceEventID(ctx context.Context, source, id string) (Record, error) {
	r, _, err := scan(in.db.QueryRowContext(ctx, selectRecord+"WHERE source = ? AND source_event_id = ?", source, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("events: get: %w", err)
	}
	return r, nil
}

// Head is the newest event's seq, or 0 when the inbox is empty.
func (in *Inbox) Head(ctx context.Context) (int64, error) {
	var head int64
	if err := in.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&head); err != nil {
		return 0, fmt.Errorf("events: head: %w", err)
	}
	return head, nil
}

// CountViewerChat counts real chat messages after seq from viewers: not
// test events, and not the streamer's account, whose messages include what
// StreamGremlin itself sends as the streamer. (The bot's are never stored.)
func (in *Inbox) CountViewerChat(ctx context.Context, after int64) (int, error) {
	var n int
	err := in.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE type = 'chat.message.received' AND seq > ?
		AND delivery_quality != 'synthetic'
		AND COALESCE(json_extract(payload, '$.chatter.role'), '') != 'broadcaster'`, after).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("events: count: %w", err)
	}
	return n, nil
}

// LatestReal returns the newest event of any of the types, test events
// left out; ok is false when there's none.
func (in *Inbox) LatestReal(ctx context.Context, types ...string) (rec Record, ok bool, err error) {
	if len(types) == 0 {
		return Record{}, false, nil
	}
	cond, args := Filter(types).where()
	recs, err := in.query(ctx, "WHERE delivery_quality != 'synthetic'"+cond+" ORDER BY seq DESC LIMIT 1", append(args, 1)...)
	if err != nil || len(recs) == 0 {
		return Record{}, false, err
	}
	return recs[0], true, nil
}

// LatestRealUpTo is LatestReal among the events up to seq upTo.
func (in *Inbox) LatestRealUpTo(ctx context.Context, upTo int64, types ...string) (rec Record, ok bool, err error) {
	if len(types) == 0 {
		return Record{}, false, nil
	}
	cond, args := Filter(types).where()
	recs, err := in.query(ctx, "WHERE delivery_quality != 'synthetic' AND seq <= ?"+cond+" ORDER BY seq DESC LIMIT ?", append(append([]any{upTo}, args...), 1)...)
	if err != nil || len(recs) == 0 {
		return Record{}, false, err
	}
	return recs[0], true, nil
}

// Filter limits which types a reader sees. Empty means every type. Each
// pattern is a type ("chat.message.received") or a prefix ending in ".*"
// ("chat.*" matches chat.message.received and chat.command.received).
type Filter []string

// ValidPattern reports whether p can be used in a Filter.
func ValidPattern(p string) bool {
	if base, ok := strings.CutSuffix(p, ".*"); ok {
		return base != "" && typePattern.MatchString(base+".x.y") && len(p) <= maxTypeLen
	}
	return ValidType(p)
}

func (f Filter) where() (string, []any) {
	if len(f) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, p := range f {
		if base, ok := strings.CutSuffix(p, ".*"); ok {
			parts = append(parts, `type LIKE ? ESCAPE '\'`)
			args = append(args, strings.ReplaceAll(base, "_", `\_`)+".%")
			continue
		}
		parts = append(parts, "type = ?")
		args = append(args, p)
	}
	return " AND (" + strings.Join(parts, " OR ") + ")", args
}

// After returns up to limit events with seq > after, oldest first.
func (in *Inbox) After(ctx context.Context, after int64, limit int, f Filter) ([]Record, error) {
	cond, args := f.where()
	return in.query(ctx, "WHERE seq > ?"+cond+" ORDER BY seq LIMIT ?", append(append([]any{after}, args...), limit)...)
}

// Latest returns the newest limit events, oldest first.
func (in *Inbox) Latest(ctx context.Context, limit int, f Filter) ([]Record, error) {
	cond, args := f.where()
	recs, err := in.query(ctx, "WHERE 1"+cond+" ORDER BY seq DESC LIMIT ?", append(args, limit)...)
	for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
		recs[i], recs[j] = recs[j], recs[i]
	}
	return recs, err
}

// SeqBefore returns the seq of the last event received before t, so a
// reader can start from there (0 when none).
func (in *Inbox) SeqBefore(ctx context.Context, t time.Time) (int64, error) {
	var seq int64
	err := in.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events WHERE received_at < ?", storage.Timestamp(t)).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("events: seq before: %w", err)
	}
	return seq, nil
}

// StreamStartSeq returns the seq of the newest stream start (Twitch went
// live, or OBS started streaming) received after since: real ones, or test
// ones when synthetic. ok is false when there's none.
func (in *Inbox) StreamStartSeq(ctx context.Context, since time.Time, synthetic bool) (seq int64, ok bool, err error) {
	var n sql.NullInt64
	err = in.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM events WHERE type IN ('broadcast.channel.online', 'broadcast.stream.started')
		AND received_at >= ? AND (delivery_quality = 'synthetic') = ?`, storage.Timestamp(since), synthetic).Scan(&n)
	if err != nil {
		return 0, false, fmt.Errorf("events: stream start: %w", err)
	}
	return n.Int64, n.Valid, nil
}

// CountChatFrom counts one account's chat messages with after < seq <
// before, leaving out commands (text starting with "!"): real messages, or
// test ones when synthetic.
func (in *Inbox) CountChatFrom(ctx context.Context, platform, userID string, after, before int64, synthetic bool) (int, error) {
	var n int
	err := in.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE type = 'chat.message.received' AND seq > ? AND seq < ?
		AND (delivery_quality = 'synthetic') = ? AND json_extract(actor, '$.platform') = ? AND json_extract(actor, '$.id') = ?
		AND substr(COALESCE(json_extract(payload, '$.text'), ''), 1, 1) != '!'`, after, before, synthetic, platform, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("events: count chat: %w", err)
	}
	return n, nil
}

// LatestFrom is one account's newest real event of the given types.
func (in *Inbox) LatestFrom(ctx context.Context, platform, userID string, types ...string) (rec Record, ok bool, err error) {
	cond, args := Filter(types).where()
	recs, err := in.query(ctx, `WHERE delivery_quality != 'synthetic' AND json_extract(actor, '$.platform') = ?
		AND json_extract(actor, '$.id') = ?`+cond+" ORDER BY seq DESC LIMIT 1", append(append([]any{platform, userID}, args...), 1)...)
	if err != nil || len(recs) == 0 {
		return Record{}, false, err
	}
	return recs[0], true, nil
}

// ChatFrom lists one account's newest chat messages, up to limit, oldest
// first: real messages, or test ones when synthetic. Commands are included.
func (in *Inbox) ChatFrom(ctx context.Context, platform, userID string, limit int, synthetic bool) ([]Record, error) {
	recs, err := in.query(ctx, `WHERE type = 'chat.message.received' AND (delivery_quality = 'synthetic') = ?
		AND json_extract(actor, '$.platform') = ? AND json_extract(actor, '$.id') = ? ORDER BY seq DESC LIMIT ?`,
		synthetic, platform, userID, limit)
	slices.Reverse(recs)
	return recs, err
}

// SeqBeforeUpTo is SeqBefore among events up to seq upTo, so a window
// ending there starts in the right place even when later events carry
// earlier times.
func (in *Inbox) SeqBeforeUpTo(ctx context.Context, t time.Time, upTo int64) (int64, error) {
	var seq int64
	err := in.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events WHERE received_at < ? AND seq <= ?",
		storage.Timestamp(t), upTo).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("events: seq before: %w", err)
	}
	return seq, nil
}

// MaxSearch is the longest text Search looks for.
const MaxSearch = 100

// Search returns the newest limit events whose type, viewer or text in
// their contents contain text (ignoring case for A to Z), oldest first.
func (in *Inbox) Search(ctx context.Context, text string, limit int, f Filter) ([]Record, error) {
	cond, args := f.where()
	esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(strings.TrimSpace(text)))
	like := "%" + esc + "%"
	// Only text values count, never field names: "name" mustn't find
	// every event that has a scene_name.
	where := `WHERE (lower(type) LIKE ? ESCAPE '\'
		OR EXISTS (SELECT 1 FROM json_tree(COALESCE(actor, '{}')) WHERE json_tree.type = 'text' AND lower(json_tree.value) LIKE ? ESCAPE '\')
		OR EXISTS (SELECT 1 FROM json_tree(payload) WHERE json_tree.type = 'text' AND lower(json_tree.value) LIKE ? ESCAPE '\'))`
	recs, err := in.query(ctx, where+cond+" ORDER BY seq DESC LIMIT ?", append(append([]any{like, like, like}, args...), limit)...)
	for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
		recs[i], recs[j] = recs[j], recs[i]
	}
	return recs, err
}

func (in *Inbox) query(ctx context.Context, tail string, args ...any) ([]Record, error) {
	rows, err := in.db.QueryContext(ctx, selectRecord+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("events: list: %w", err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, _, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("events: read: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Wait returns events after the cursor, waiting up to wait for the first to
// arrive. It never misses one: it takes the wake-up signal before reading.
func (in *Inbox) Wait(ctx context.Context, after int64, limit int, f Filter, wait time.Duration) ([]Record, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		signal := in.notify.C()
		recs, err := in.After(ctx, after, limit, f)
		if err != nil || len(recs) > 0 || wait <= 0 {
			return recs, err
		}
		select {
		case <-signal:
		case <-timer.C:
			return recs, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Notifier wakes every waiter when something new is stored. It never blocks
// the writer and never drops a reader: readers re-read from their cursor.
type Notifier struct {
	mu sync.Mutex
	ch chan struct{}
}

// NewNotifier returns a Notifier.
func NewNotifier() *Notifier { return &Notifier{ch: make(chan struct{})} }

// C is closed at the next Notify.
func (n *Notifier) C() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// Notify wakes everyone waiting on C.
func (n *Notifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	close(n.ch)
	n.ch = make(chan struct{})
}
