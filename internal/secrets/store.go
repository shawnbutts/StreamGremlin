package secrets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Options configure Open.
type Options struct {
	// DataDir holds the encrypted file, if that store is used.
	DataDir string
	// Want is the store asked for. Auto, the default, keeps whatever this
	// data folder already uses, and on first start picks the keyring if it
	// answers, else the file.
	Want   Kind
	Logger *slog.Logger

	// For tests: a fake keyring, and a stand-in for "is this a test binary".
	provider provider
	inTest   *bool
	getenv   func(string) string
	goos     string
}

// Store saves secrets in one backend and keeps an index of keys in the
// database. Only the engine process touches it; the API never returns values.
type Store struct {
	db      *storage.DB
	b       backend
	kind    Kind
	dataDir string
	log     *slog.Logger
	now     func() time.Time
	mu      sync.Mutex // orders index and backend changes
}

// Entry describes one saved secret, without its value.
type Entry struct {
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CheckState is the result of trying to read one secret.
type CheckState string

const (
	CheckOK         CheckState = "ok"         // read back successfully
	CheckMissing    CheckState = "missing"    // listed, but the store has no value
	CheckUnreadable CheckState = "unreadable" // the store refused or didn't answer
)

// CheckResult is one secret's check.
type CheckResult struct {
	Key    string     `json:"key"`
	State  CheckState `json:"state"`
	Detail string     `json:"detail,omitempty"`
}

// Open returns the data folder's store, choosing and recording it on first
// start. It never touches the keyring after the first start, so a locked
// keyring can't stop the engine from starting.
func Open(ctx context.Context, db *storage.DB, opts Options) (*Store, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.provider == nil {
		opts.provider = osKeyring{}
	}
	inTest := testing.Testing()
	if opts.inTest != nil {
		inTest = *opts.inTest
	}
	if opts.getenv == nil {
		opts.getenv = os.Getenv
	}
	if opts.goos == "" {
		opts.goos = runtime.GOOS
	}
	if opts.Want == "" {
		opts.Want = KindAuto
	}
	if _, err := ParseKind(string(opts.Want)); err != nil {
		return nil, err
	}

	kind, installID, err := chosen(ctx, db)
	if err != nil {
		return nil, err
	}
	if kind == "" {
		kind, installID, err = choose(ctx, db, opts, inTest)
		if err != nil {
			return nil, err
		}
	} else if opts.Want != KindAuto && opts.Want != kind {
		return nil, fmt.Errorf("%w: it uses %s, and %s was asked for. Secrets don't move between stores on their own",
			ErrStoreMismatch, kind, opts.Want)
	}

	s := &Store{db: db, kind: kind, dataDir: opts.DataDir, log: opts.Logger, now: time.Now}
	switch kind {
	case KindKeyring:
		if _, isOS := opts.provider.(osKeyring); isOS && !realKeyringAllowed(inTest) {
			return nil, errors.New("secrets: tests must not use the real OS keyring; set STREAMGREMLIN_TEST_OS_KEYRING=1 to opt in")
		}
		s.b = newKeyringBackend(opts.provider, installID)
	case KindFile:
		if opts.DataDir == "" {
			return nil, errors.New("secrets: the file store needs a data folder")
		}
		s.b = newFileBackend(opts.DataDir, installID)
	}
	return s, nil
}

func chosen(ctx context.Context, db *storage.DB) (Kind, string, error) {
	var kind, id string
	err := db.QueryRowContext(ctx, "SELECT kind, installation_id FROM secret_store WHERE id = 1").Scan(&kind, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("secrets: read the chosen store: %w", err)
	}
	return Kind(kind), id, nil
}

func choose(ctx context.Context, db *storage.DB, opts Options, inTest bool) (Kind, string, error) {
	kind := opts.Want
	if kind == KindAuto {
		_, isOS := opts.provider.(osKeyring)
		switch {
		case isOS && !realKeyringAllowed(inTest):
			kind = KindFile // tests never probe the real keyring
		case keyringAnswers(ctx, opts.provider, opts.goos, opts.getenv):
			kind = KindKeyring
		default:
			kind = KindFile
		}
	}
	idb := make([]byte, 16)
	if _, err := rand.Read(idb); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(idb)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO secret_store (id, kind, installation_id, chosen_at) VALUES (1, ?, ?, ?)",
		string(kind), id, storage.Timestamp(time.Now())); err != nil {
		return "", "", fmt.Errorf("secrets: record the chosen store: %w", err)
	}
	opts.Logger.Info("secret store chosen", "store", string(kind))
	return kind, id, nil
}

// Kind is the store in use: keyring or file.
func (s *Store) Kind() Kind { return s.kind }

// Label names the store for the streamer.
func (s *Store) Label() string {
	if s.kind == KindKeyring {
		return KeyringLabel(runtime.GOOS)
	}
	return "an encrypted file in your data folder"
}

// Set saves value under key, replacing any old value. The index row is
// written first, so a crash in between leaves a listed secret that checks
// as missing, never a hidden one. Saving the same value again is harmless.
func (s *Store) Set(ctx context.Context, key string, value Value) error {
	if !ValidKey(key) {
		return fmt.Errorf("%w: key %q", ErrInvalid, key)
	}
	if value.IsZero() || len(value.Reveal()) > MaxValueBytes {
		return fmt.Errorf("%w: a secret must be 1 to %d bytes", ErrInvalid, MaxValueBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := storage.Timestamp(s.now())
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO secrets (key, created_at, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET updated_at = excluded.updated_at`, key, now, now); err != nil {
		return fmt.Errorf("secrets: index %s: %w", key, err)
	}
	if err := s.b.set(ctx, key, value.Reveal()); err != nil {
		return err
	}
	s.log.Info("secret saved", "key", key, "store", string(s.kind))
	return nil
}

// Get returns the value saved under key.
func (s *Store) Get(ctx context.Context, key string) (Value, error) {
	if !ValidKey(key) {
		return Value{}, fmt.Errorf("%w: key %q", ErrInvalid, key)
	}
	v, err := s.b.get(ctx, key)
	if err != nil {
		return Value{}, err
	}
	return NewValue(v), nil
}

// Delete forgets key. Forgetting a key that isn't saved is harmless.
func (s *Store) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return fmt.Errorf("%w: key %q", ErrInvalid, key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.b.del(ctx, key); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE key = ?", key); err != nil {
		return fmt.Errorf("secrets: unindex %s: %w", key, err)
	}
	s.log.Info("secret forgotten", "key", key, "store", string(s.kind))
	return nil
}

// List returns the saved keys in order, without values.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, created_at, updated_at FROM secrets ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("secrets: list: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var created, updated string
		if err := rows.Scan(&e.Key, &created, &updated); err != nil {
			return nil, err
		}
		if e.CreatedAt, err = storage.ParseTimestamp(created); err != nil {
			return nil, err
		}
		if e.UpdatedAt, err = storage.ParseTimestamp(updated); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Check tries to read every listed secret, so the streamer can see which
// would need signing in again. It never returns values.
func (s *Store) Check(ctx context.Context) ([]CheckResult, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CheckResult, 0, len(entries))
	for _, e := range entries {
		r := CheckResult{Key: e.Key, State: CheckOK}
		_, err := s.b.get(ctx, e.Key)
		switch {
		case err == nil:
		case errors.Is(err, ErrNotFound):
			r.State, r.Detail = CheckMissing, "Saved earlier, but "+s.Label()+" no longer has it."
		default:
			// Includes running out of time: the rest are reported, not dropped.
			r.State, r.Detail = CheckUnreadable, err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}
