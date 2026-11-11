// Package rounds keeps plugins' rounds (docs/adr/0039-points-for-plugins.md):
// viewers' stakes held for a game, paid out or refunded, exactly once.
// The ledger holds the points and is the record of what was staked; this
// package says what each round was, its stated rules, its rolls, and how
// it ended. Round operations run one at a time, engine-wide.
package rounds

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// Limits on rounds.
const (
	MaxOpen      = 3 // open rounds per plugin
	MaxRolls     = 1000
	MaxSides     = 1_000_000
	MaxTitle     = 100
	MaxRules     = 300
	MaxReason    = 200
	SweepEvery   = 5 * time.Second
	stateOpen    = "open"
	stateSettle  = "settling"
	stateSettled = "settled"
	stateRefund  = "refunded"
)

// ErrRefused: what the plugin asked can't be done; the message says why,
// for the plugin. Nothing changed.
var ErrRefused = errors.New("rounds: refused")

func refused(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// Limits are what the streamer approved for a plugin's rounds.
type Limits struct {
	Currency        string
	MaxStake        int64
	MaxMultiplier   float64
	MaxRoundSeconds int
}

// MaxPayout is the most a stake can pay back.
func (l Limits) MaxPayout(stake int64) int64 {
	return int64(math.Floor(float64(stake)*l.MaxMultiplier + 1e-9))
}

// Round is one round, as recorded.
type Round struct {
	ID       string           `json:"id"`
	Plugin   string           `json:"plugin"`
	Version  string           `json:"version"`
	Title    string           `json:"title"`
	Rules    string           `json:"rules"` // as the plugin stated them
	Currency string           `json:"currency"`
	State    string           `json:"state"`
	Reason   string           `json:"reason,omitempty"` // why it was refunded
	OpenedAt time.Time        `json:"opened_at"`
	ClosesBy time.Time        `json:"closes_by"`
	ClosedAt *time.Time       `json:"closed_at,omitempty"`
	Payouts  map[string]int64 `json:"payouts,omitempty"` // by viewer id
	Stakes   []Stake          `json:"stakes"`
	Rolls    []Roll           `json:"rolls"`
}

// Stake is a viewer's points in a round.
type Stake struct {
	Viewer string    `json:"viewer"`
	Name   string    `json:"name"`
	Amount int64     `json:"amount"`
	At     time.Time `json:"at"`
}

// Roll is a roll StreamGremlin made for a round.
type Roll struct {
	Seq   int       `json:"seq"`
	Label string    `json:"label"`
	Sides int       `json:"sides"`
	Value int       `json:"value"`
	At    time.Time `json:"at"`
}

// Options configure a Service.
type Options struct {
	DB      *storage.DB
	Ledger  *points.Ledger
	Viewers *viewers.Service
	Logger  *slog.Logger
	Now     func() time.Time
	// Roll, in tests, replaces crypto/rand: a number from 1 to sides.
	Roll func(sides int) int
}

// Service keeps the rounds.
type Service struct {
	opts Options
	log  *slog.Logger
	mu   sync.Mutex // one round operation at a time
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Roll == nil {
		opts.Roll = cryptoRoll
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{opts: opts, log: log}
}

func cryptoRoll(sides int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(sides)))
	if err != nil {
		panic(err) // crypto/rand never fails on supported systems
	}
	return int(n.Int64()) + 1
}

func opPrefix(plugin, round string) string { return "plugin:" + plugin + ":round:" + round + ":" }

func roundID(plugin, key string) string {
	if key == "" {
		var b [12]byte
		_, _ = rand.Read(b[:])
		return "r_" + hex.EncodeToString(b[:])
	}
	sum := sha256.Sum256([]byte(plugin + "\x00" + key))
	return "r_" + hex.EncodeToString(sum[:12])
}

func plainText(s string, most int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= most && !strings.ContainsFunc(s, func(r rune) bool { return r < 32 })
}

// Open opens a round for a plugin. A key (the step's) makes it the same
// round when asked again: an open one is given back, a closed one refused.
func (s *Service) Open(ctx context.Context, plugin, version, title, rules, key string, lim Limits) (Round, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	title, rules = strings.TrimSpace(title), strings.TrimSpace(rules)
	switch {
	case title == "" || !plainText(title, MaxTitle):
		return Round{}, refused("a round's title is 1 to %d characters", MaxTitle)
	case rules == "" || !plainText(rules, MaxRules):
		return Round{}, refused("a round's rules are 1 to %d characters: its odds, in words", MaxRules)
	case len(key) > 256:
		return Round{}, refused("a round's id is up to 256 characters")
	}
	if _, err := s.opts.Ledger.Currency(ctx, lim.Currency); err != nil {
		return Round{}, refused("the currency %q doesn't exist", lim.Currency)
	}
	id := roundID(plugin, key)
	if r, err := s.get(ctx, id); err == nil {
		if r.Plugin != plugin {
			return Round{}, refused("that round id is taken")
		}
		if r.State != stateOpen {
			return Round{}, refused("that round is %s already", r.State)
		}
		return r, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Round{}, err
	}
	var n int
	if err := s.opts.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_rounds WHERE package_id = ? AND state = 'open'", plugin).Scan(&n); err != nil {
		return Round{}, err
	}
	if n >= MaxOpen {
		return Round{}, refused("a plugin has up to %d rounds open", MaxOpen)
	}
	now := s.opts.Now()
	closes := now.Add(time.Duration(lim.MaxRoundSeconds) * time.Second)
	if _, err := s.opts.DB.ExecContext(ctx, `INSERT INTO plugin_rounds (id, package_id, version, title, rules, currency, state, opened_at, closes_by)
		VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?)`, id, plugin, version, title, rules, lim.Currency, storage.Timestamp(now), storage.Timestamp(closes)); err != nil {
		return Round{}, fmt.Errorf("rounds: open: %w", err)
	}
	s.log.Info("plugin round opened", "plugin", plugin, "round", id, "title", title, "closes_by", closes)
	return s.get(ctx, id)
}

// viewer is the viewer behind a call: its event's actor.
func (s *Service) viewer(ctx context.Context, actor *events.Actor) (viewers.Viewer, error) {
	if actor == nil || actor.ID == "" || actor.Platform == "" {
		return viewers.Viewer{}, refused("no viewer started this: only a viewer's own command can stake or ask their balance")
	}
	if s.opts.Viewers == nil {
		return viewers.Viewer{}, refused("viewers aren't available")
	}
	return s.opts.Viewers.Ensure(ctx, *actor)
}

// Balance is the balance of the viewer behind the call.
func (s *Service) Balance(ctx context.Context, actor *events.Actor, lim Limits) (int64, error) {
	v, err := s.viewer(ctx, actor)
	if err != nil {
		return 0, err
	}
	return s.opts.Ledger.Balance(ctx, v.ID, lim.Currency)
}

// openRound is a plugin's round that's open and not past its time.
func (s *Service) openRound(ctx context.Context, plugin, id string) (Round, error) {
	r, err := s.get(ctx, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Round{}, refused("there's no round %q", id)
	case err != nil:
		return Round{}, err
	case r.Plugin != plugin:
		return Round{}, refused("there's no round %q", id)
	case r.State != stateOpen:
		return Round{}, refused("the round is %s", r.State)
	case !s.opts.Now().Before(r.ClosesBy):
		return Round{}, refused("the round ran out of time")
	}
	return r, nil
}

// Stake charges the viewer behind the call into a round, once.
func (s *Service) Stake(ctx context.Context, plugin, id string, actor *events.Actor, amount int64, lim Limits) (Stake, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.openRound(ctx, plugin, id)
	if err != nil {
		return Stake{}, 0, err
	}
	if amount < 1 || amount > lim.MaxStake {
		return Stake{}, 0, refused("a stake is 1 to %d", lim.MaxStake)
	}
	v, err := s.viewer(ctx, actor)
	if err != nil {
		return Stake{}, 0, err
	}
	staked, err := s.stakes(ctx, plugin, id)
	if err != nil {
		return Stake{}, 0, err
	}
	if t, ok := staked[v.ID]; ok {
		return Stake{}, 0, refused("%s is in this round already, with %d", v.DisplayName, -t.Amount)
	}
	res, err := s.opts.Ledger.Debit(ctx, points.Op{ID: opPrefix(plugin, id) + "stake:" + v.ID, Currency: r.Currency, Viewer: v.ID, Amount: amount,
		Memo: cut(r.Title+": stake", points.MaxMemo), By: "plugin " + plugin})
	var short *points.InsufficientError
	switch {
	case errors.As(err, &short):
		return Stake{}, short.Has, refused("%s has %d %s, not %d", v.DisplayName, short.Has, short.Name, amount)
	case err != nil:
		return Stake{}, 0, err
	}
	now := s.opts.Now()
	if _, err := s.opts.DB.ExecContext(ctx, `INSERT OR IGNORE INTO plugin_round_stakes (round_id, viewer_id, name, amount, staked_at)
		VALUES (?, ?, ?, ?, ?)`, id, v.ID, v.DisplayName, amount, storage.Timestamp(now)); err != nil {
		s.log.Error("plugin round stake not listed (it's in the ledger)", "round", id, "err", err)
	}
	return Stake{Viewer: v.ID, Name: v.DisplayName, Amount: amount, At: now}, res.Balance, nil
}

// stakes are the round's stakes as the ledger holds them, by viewer.
func (s *Service) stakes(ctx context.Context, plugin, id string) (map[string]points.Transaction, error) {
	list, err := s.opts.Ledger.ByOpPrefix(ctx, opPrefix(plugin, id)+"stake:")
	if err != nil {
		return nil, err
	}
	out := map[string]points.Transaction{}
	for _, t := range list {
		if t.Kind == points.Debit {
			out[t.Viewer] = t
		}
	}
	return out, nil
}

// Roll makes a roll for an open round, and records it.
func (s *Service) Roll(ctx context.Context, plugin, id, label string, sides int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.openRound(ctx, plugin, id); err != nil {
		return 0, err
	}
	if sides < 2 || sides > MaxSides {
		return 0, refused("a roll has 2 to %d sides", MaxSides)
	}
	label = cut(strings.TrimSpace(label), 60)
	var n int
	if err := s.opts.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_round_rolls WHERE round_id = ?", id).Scan(&n); err != nil {
		return 0, err
	}
	if n >= MaxRolls {
		return 0, refused("a round has up to %d rolls", MaxRolls)
	}
	v := s.opts.Roll(sides)
	if _, err := s.opts.DB.ExecContext(ctx, `INSERT INTO plugin_round_rolls (round_id, seq, label, sides, value, at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, n+1, label, sides, v, storage.Timestamp(s.opts.Now())); err != nil {
		return 0, fmt.Errorf("rounds: roll: %w", err)
	}
	return v, nil
}

// Settle pays each staked viewer their payout, at most the stake times
// the multiplier; anyone not listed gets nothing back. The round closes.
func (s *Service) Settle(ctx context.Context, plugin, id string, payouts map[string]int64, lim Limits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.openRound(ctx, plugin, id)
	if err != nil {
		return err
	}
	staked, err := s.stakes(ctx, plugin, id)
	if err != nil {
		return err
	}
	for viewer, amount := range payouts {
		t, ok := staked[viewer]
		switch {
		case !ok:
			return refused("%s has no stake in this round", viewer)
		case amount < 0 || amount > lim.MaxPayout(-t.Amount):
			return refused("a payout is 0 to %d for a stake of %d", lim.MaxPayout(-t.Amount), -t.Amount)
		}
	}
	raw, _ := json.Marshal(payouts)
	res, err := s.opts.DB.ExecContext(ctx, "UPDATE plugin_rounds SET state = 'settling', payouts = ? WHERE id = ? AND state = 'open'", string(raw), id)
	if err != nil {
		return fmt.Errorf("rounds: settle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return refused("the round closed just now")
	}
	r.Payouts = payouts
	return s.pay(ctx, r)
}

// pay applies a settling round's payouts, each once, and closes it.
func (s *Service) pay(ctx context.Context, r Round) error {
	viewersPaid := make([]string, 0, len(r.Payouts))
	for v := range r.Payouts {
		viewersPaid = append(viewersPaid, v)
	}
	sort.Strings(viewersPaid)
	for _, v := range viewersPaid {
		amount := r.Payouts[v]
		if amount <= 0 {
			continue
		}
		if _, err := s.opts.Ledger.Credit(ctx, points.Op{ID: opPrefix(r.Plugin, r.ID) + "pay:" + v, Currency: r.Currency, Viewer: v, Amount: amount,
			Memo: cut(r.Title+": payout", points.MaxMemo), By: "plugin " + r.Plugin}); err != nil {
			return fmt.Errorf("rounds: pay %s: %w", v, err)
		}
	}
	if _, err := s.opts.DB.ExecContext(ctx, "UPDATE plugin_rounds SET state = 'settled', closed_at = ? WHERE id = ?",
		storage.Timestamp(s.opts.Now()), r.ID); err != nil {
		return fmt.Errorf("rounds: settled: %w", err)
	}
	s.log.Info("plugin round settled", "plugin", r.Plugin, "round", r.ID, "payouts", len(r.Payouts))
	return nil
}

// Cancel refunds every stake in a plugin's open round and closes it.
func (s *Service) Cancel(ctx context.Context, plugin, id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(ctx, id)
	switch {
	case errors.Is(err, sql.ErrNoRows) || err == nil && r.Plugin != plugin:
		return refused("there's no round %q", id)
	case err != nil:
		return err
	case r.State != stateOpen:
		return refused("the round is %s", r.State)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled by the plugin"
	}
	return s.refund(ctx, r, cut(reason, MaxReason))
}

// refund undoes every stake the ledger holds for the round, each once,
// and closes it with the reason.
func (s *Service) refund(ctx context.Context, r Round, reason string) error {
	staked, err := s.stakes(ctx, r.Plugin, r.ID)
	if err != nil {
		return err
	}
	for v, t := range staked {
		_, err := s.opts.Ledger.Undo(ctx, t.ID, points.Op{ID: opPrefix(r.Plugin, r.ID) + "refund:" + v,
			Memo: cut(r.Title+": refunded ("+reason+")", points.MaxMemo), By: "plugin " + r.Plugin})
		if err != nil && !errors.Is(err, points.ErrReversed) {
			return fmt.Errorf("rounds: refund %s: %w", v, err)
		}
	}
	if _, err := s.opts.DB.ExecContext(ctx, "UPDATE plugin_rounds SET state = 'refunded', reason = ?, closed_at = ? WHERE id = ?",
		reason, storage.Timestamp(s.opts.Now()), r.ID); err != nil {
		return fmt.Errorf("rounds: refunded: %w", err)
	}
	s.log.Info("plugin round refunded", "plugin", r.Plugin, "round", r.ID, "stakes", len(staked), "reason", reason)
	return nil
}

// RefundOpen refunds every open round of a plugin (every plugin's, for
// ""), saying why. It returns how many.
func (s *Service) RefundOpen(ctx context.Context, plugin, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, args := "SELECT id FROM plugin_rounds WHERE state = 'open'", []any{}
	if plugin != "" {
		q, args = q+" AND package_id = ?", append(args, plugin)
	}
	return s.refundEach(ctx, q, args, func(Round) string { return reason })
}

func (s *Service) refundEach(ctx context.Context, q string, args []any, why func(Round) string) (int, error) {
	ids, err := s.ids(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		r, err := s.get(ctx, id)
		if err != nil {
			return 0, err
		}
		if err := s.refund(ctx, r, why(r)); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// Sweep refunds the rounds past their time.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refundEach(ctx, "SELECT id FROM plugin_rounds WHERE state = 'open' AND closes_by <= ?", []any{storage.Timestamp(s.opts.Now())},
		func(r Round) string {
			return fmt.Sprintf("it ran past its %s", r.ClosesBy.Sub(r.OpenedAt).Round(time.Second))
		})
}

// Recover finishes rounds a stop interrupted when StreamGremlin starts:
// settling ones are paid, each payout once; open ones are refunded.
func (s *Service) Recover(ctx context.Context) error {
	s.mu.Lock()
	settling, err := s.ids(ctx, "SELECT id FROM plugin_rounds WHERE state = 'settling'")
	if err == nil {
		for _, id := range settling {
			var r Round
			if r, err = s.get(ctx, id); err == nil {
				err = s.pay(ctx, r)
			}
			if err != nil {
				break
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = s.RefundOpen(ctx, "", "StreamGremlin stopped while it was open")
	return err
}

func (s *Service) ids(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.opts.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const roundColumns = `id, package_id, version, title, rules, currency, state, reason, opened_at, closes_by, closed_at, payouts`

func scanRound(sc interface{ Scan(...any) error }) (Round, error) {
	var r Round
	var opened, closes string
	var closed, payouts sql.NullString
	if err := sc.Scan(&r.ID, &r.Plugin, &r.Version, &r.Title, &r.Rules, &r.Currency, &r.State, &r.Reason, &opened, &closes, &closed, &payouts); err != nil {
		return r, err
	}
	r.OpenedAt, _ = storage.ParseTimestamp(opened)
	r.ClosesBy, _ = storage.ParseTimestamp(closes)
	if closed.Valid {
		t, _ := storage.ParseTimestamp(closed.String)
		r.ClosedAt = &t
	}
	if payouts.Valid {
		_ = json.Unmarshal([]byte(payouts.String), &r.Payouts)
	}
	return r, nil
}

// get reads a round, with its stakes and rolls.
func (s *Service) get(ctx context.Context, id string) (Round, error) {
	r, err := scanRound(s.opts.DB.QueryRowContext(ctx, "SELECT "+roundColumns+" FROM plugin_rounds WHERE id = ?", id))
	if err != nil {
		return r, err
	}
	return r, s.fill(ctx, &r)
}

func (s *Service) fill(ctx context.Context, r *Round) error {
	r.Stakes, r.Rolls = []Stake{}, []Roll{}
	rows, err := s.opts.DB.QueryContext(ctx, "SELECT viewer_id, name, amount, staked_at FROM plugin_round_stakes WHERE round_id = ? ORDER BY staked_at, viewer_id", r.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var st Stake
		var at string
		if err := rows.Scan(&st.Viewer, &st.Name, &st.Amount, &at); err != nil {
			_ = rows.Close()
			return err
		}
		st.At, _ = storage.ParseTimestamp(at)
		r.Stakes = append(r.Stakes, st)
	}
	_ = rows.Close()
	rows, err = s.opts.DB.QueryContext(ctx, "SELECT seq, label, sides, value, at FROM plugin_round_rolls WHERE round_id = ? ORDER BY seq", r.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rl Roll
		var at string
		if err := rows.Scan(&rl.Seq, &rl.Label, &rl.Sides, &rl.Value, &at); err != nil {
			return err
		}
		rl.At, _ = storage.ParseTimestamp(at)
		r.Rolls = append(r.Rolls, rl)
	}
	return rows.Err()
}

// List is a plugin's rounds, newest first.
func (s *Service) List(ctx context.Context, plugin string, limit int) ([]Round, error) {
	rows, err := s.opts.DB.QueryContext(ctx, "SELECT "+roundColumns+" FROM plugin_rounds WHERE package_id = ? ORDER BY opened_at DESC, id LIMIT ?", plugin, limit)
	if err != nil {
		return nil, fmt.Errorf("rounds: list: %w", err)
	}
	out := []Round{}
	for rows.Next() {
		r, err := scanRound(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	_ = rows.Close()
	for i := range out {
		if err := s.fill(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
