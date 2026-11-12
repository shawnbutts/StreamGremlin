// Package clients pairs outside programs with the engine and gives each
// its own API token (M7 slice 4; docs/adr/0042-paired-clients.md). The
// first is the Stream Deck plugin.
//
// Pairing: the streamer starts it in the app, which shows a one-time code.
// The plugin sends the code, with its name, and gets a token back. A code
// works once, for CodeLife, and MaxWrong wrong codes end the pairing. Only
// one pairing runs at a time, in memory: a restart ends it. The token is
// stored only as its SHA-256; forgetting the client refuses it at once.
package clients

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Kinds of client.
const StreamDeck = "stream_deck"

// Limits.
const (
	CodeLife   = 5 * time.Minute
	MaxWrong   = 5
	MaxClients = 10
	MaxName    = 60
	maxRequest = 64
	tokenBytes = 32
	touchEvery = time.Minute
)

// The code's letters: no 0, 1, I or O, which are easy to mix up.
const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// Pairing states.
const (
	StateNone    = "none"    // no pairing started, or cancelled
	StateWaiting = "waiting" // a code is shown
	StatePaired  = "paired"  // the code was used
	StateExpired = "expired" // the code ran out
	StateLocked  = "locked"  // too many wrong codes
)

// Errors.
var (
	ErrInvalid   = errors.New("clients: invalid")
	ErrNoPairing = errors.New("clients: no pairing is waiting for a code; start pairing in StreamGremlin")
	ErrWrongCode = errors.New("clients: that code isn't the one StreamGremlin shows")
	ErrFull      = errors.New("clients: too many paired clients")
	ErrNotFound  = errors.New("clients: no such client")
)

// Client is a paired program.
type Client struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Pairing is a code waiting to be typed.
type Pairing struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// State is the pairing as the app shows it. Code is set only while waiting.
type State struct {
	State     string     `json:"state"`
	Kind      string     `json:"kind,omitempty"`
	Code      string     `json:"code,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Client    *Client    `json:"client,omitempty"`
}

type pairing struct {
	kind    string
	code    string // without the dash
	expires time.Time
	wrong   int
	state   string
	// After pairing, a retry of the same request gets the same answer
	// until the code would have run out.
	request string
	client  *Client
	token   string
}

// Service pairs clients and checks their tokens.
type Service struct {
	db  *storage.DB
	now func() time.Time
	log *slog.Logger

	mu      sync.Mutex
	pairing *pairing
	byHash  map[string]*Client // loaded on first use
	touched map[string]time.Time
}

// New returns the service. now is time.Now when nil.
func New(db *storage.DB, now func() time.Time, log *slog.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, now: now, log: log.With("component", "clients"), touched: map[string]time.Time{}}
}

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

func hashOf(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func newCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	out := make([]byte, 8)
	for i, c := range b {
		out[i] = alphabet[int(c)%len(alphabet)] // 256 is a multiple of 32: no bias
	}
	return string(out)
}

func show(code string) string { return code[:4] + "-" + code[4:] }

// normalize turns what was typed into the code's form.
func normalize(typed string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(typed) {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// StartPairing shows a new code, replacing any pairing in progress.
func (s *Service) StartPairing(kind string) (Pairing, error) {
	if kind != StreamDeck {
		return Pairing{}, invalid("a client is a stream_deck")
	}
	if err := s.load(context.Background()); err != nil {
		return Pairing{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.byHash) >= MaxClients {
		return Pairing{}, fmt.Errorf("%w: up to %d; forget one first", ErrFull, MaxClients)
	}
	old := ""
	if s.pairing != nil {
		old = s.pairing.code
	}
	code := newCode()
	for code == old {
		code = newCode()
	}
	s.pairing = &pairing{kind: kind, code: code, expires: s.now().Add(CodeLife), state: StateWaiting}
	s.log.Info("pairing started", "kind", kind)
	return Pairing{Code: show(code), ExpiresAt: s.pairing.expires}, nil
}

// Cancel ends a pairing in progress.
func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairing = nil
}

// State reports the pairing.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pairing
	if p == nil {
		return State{State: StateNone}
	}
	if p.state == StateWaiting && !s.now().Before(p.expires) {
		p.state = StateExpired
	}
	st := State{State: p.state, Kind: p.kind, Client: p.client}
	if p.state == StateWaiting {
		exp := p.expires
		st.Code, st.ExpiresAt = show(p.code), &exp
	}
	return st
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return "", invalid("a name is 1 to %d characters", MaxName)
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", invalid("a name is printable characters")
		}
	}
	return name, nil
}

// Exchange swaps the code shown in the app for a new client and its token.
// requestID makes a retry return the same answer instead of failing.
func (s *Service) Exchange(ctx context.Context, typed, name, requestID string) (Client, string, error) {
	name, err := checkName(name)
	if err != nil {
		return Client{}, "", err
	}
	if len(requestID) > maxRequest {
		return Client{}, "", invalid("a request id is up to %d characters", maxRequest)
	}
	if err := s.load(ctx); err != nil {
		return Client{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pairing
	now := s.now()
	if p == nil || !now.Before(p.expires) && p.state != StateLocked {
		if p != nil && p.state == StateWaiting {
			p.state = StateExpired
		}
		return Client{}, "", ErrNoPairing
	}
	code := normalize(typed)
	match := subtle.ConstantTimeCompare([]byte(code), []byte(p.code)) == 1
	if p.state == StatePaired && match && requestID != "" && requestID == p.request {
		return *p.client, p.token, nil
	}
	if p.state != StateWaiting {
		return Client{}, "", ErrNoPairing
	}
	if !match {
		p.wrong++
		s.log.Warn("wrong pairing code", "tries", p.wrong, "of", MaxWrong)
		if p.wrong >= MaxWrong {
			p.state = StateLocked
			return Client{}, "", ErrNoPairing
		}
		return Client{}, "", ErrWrongCode
	}
	if len(s.byHash) >= MaxClients {
		return Client{}, "", fmt.Errorf("%w: up to %d; forget one first", ErrFull, MaxClients)
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return Client{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	c := Client{ID: "cl_" + hex.EncodeToString(id), Kind: p.kind, Name: name, CreatedAt: now.UTC()}
	used := c.CreatedAt
	c.LastUsedAt = &used
	if _, err := s.db.ExecContext(ctx, `INSERT INTO api_clients (id, kind, name, token_hash, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Kind, c.Name, hashOf(token), storage.Timestamp(c.CreatedAt), storage.Timestamp(used)); err != nil {
		return Client{}, "", fmt.Errorf("clients: save: %w", err)
	}
	stored := c
	s.byHash[hashOf(token)] = &stored
	s.touched[c.ID] = now
	p.state, p.request, p.client, p.token = StatePaired, requestID, &stored, token
	s.log.Info("client paired", "client", c.ID, "kind", c.Kind, "name", c.Name)
	return c, token, nil
}

// load reads the clients into memory once.
func (s *Service) load(ctx context.Context) error {
	s.mu.Lock()
	loaded := s.byHash != nil
	s.mu.Unlock()
	if loaded {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, name, token_hash, created_at, last_used_at FROM api_clients`)
	if err != nil {
		return fmt.Errorf("clients: load: %w", err)
	}
	defer func() { _ = rows.Close() }()
	m := map[string]*Client{}
	for rows.Next() {
		var hash string
		c, err := scan(rows, &hash)
		if err != nil {
			return err
		}
		m[hash] = &c
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.byHash == nil {
		s.byHash = m
	}
	s.mu.Unlock()
	return nil
}

func scan(sc interface{ Scan(...any) error }, hash *string) (Client, error) {
	var c Client
	var created string
	var used sql.NullString
	if err := sc.Scan(&c.ID, &c.Kind, &c.Name, hash, &created, &used); err != nil {
		return Client{}, fmt.Errorf("clients: read: %w", err)
	}
	c.CreatedAt, _ = storage.ParseTimestamp(created)
	if used.Valid {
		t, _ := storage.ParseTimestamp(used.String)
		c.LastUsedAt = &t
	}
	return c, nil
}

// Authenticate returns the client a token belongs to.
func (s *Service) Authenticate(ctx context.Context, token string) (Client, bool) {
	if token == "" || s.load(ctx) != nil {
		return Client{}, false
	}
	hash := hashOf(token)
	s.mu.Lock()
	c, ok := s.byHash[hash]
	if !ok {
		s.mu.Unlock()
		return Client{}, false
	}
	now := s.now()
	touch := now.Sub(s.touched[c.ID]) >= touchEvery
	if touch {
		s.touched[c.ID] = now
		t := now.UTC()
		c.LastUsedAt = &t
	}
	out := *c
	s.mu.Unlock()
	if touch {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_clients SET last_used_at = ? WHERE id = ?`, storage.Timestamp(now), out.ID); err != nil {
			s.log.Warn("couldn't note when a client was used", "client", out.ID, "err", err)
		}
	}
	return out, true
}

// List returns the paired clients, newest first.
func (s *Service) List(ctx context.Context) ([]Client, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, name, token_hash, created_at, last_used_at FROM api_clients ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("clients: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Client{}
	for rows.Next() {
		var hash string
		c, err := scan(rows, &hash)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Forget unpairs a client; its token is refused from now on.
func (s *Service) Forget(ctx context.Context, id string) (bool, error) {
	if err := s.load(ctx); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_clients WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("clients: forget: %w", err)
	}
	for h, c := range s.byHash {
		if c.ID == id {
			delete(s.byHash, h)
		}
	}
	delete(s.touched, id)
	if s.pairing != nil && s.pairing.client != nil && s.pairing.client.ID == id {
		s.pairing = nil // a retry mustn't hand back a forgotten token
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.log.Info("client forgotten", "client", id)
	}
	return n > 0, nil
}
