package secrets

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrNotFound means no secret is saved under the key.
	ErrNotFound = errors.New("secrets: not found")
	// ErrInvalid wraps a bad key or value, so the API can answer 400.
	ErrInvalid = errors.New("secrets: invalid")
	// ErrStoreMismatch means a different store was asked for than the one
	// this data folder already uses.
	ErrStoreMismatch = errors.New("secrets: this data folder already uses another secret store")
	// ErrUnavailable means the store couldn't be reached, for example a
	// locked keyring that didn't answer in time.
	ErrUnavailable = errors.New("secrets: the secret store didn't answer")
)

// Kind names a secret store.
type Kind string

const (
	KindAuto    Kind = "auto"    // the keyring if it answers, else the file; only as a request
	KindKeyring Kind = "keyring" // the OS credential store
	KindFile    Kind = "file"    // AES-256-GCM file in the data folder
)

// ParseKind reads a store name from a flag or setting. Empty means auto.
func ParseKind(s string) (Kind, error) {
	switch Kind(s) {
	case "", KindAuto:
		return KindAuto, nil
	case KindKeyring, KindFile:
		return Kind(s), nil
	}
	return "", fmt.Errorf("secrets: unknown store %q; use auto, keyring or file", s)
}

// backend stores values by key. It knows nothing of the index.
type backend interface {
	get(ctx context.Context, key string) (string, error) // ErrNotFound when missing
	set(ctx context.Context, key, value string) error
	del(ctx context.Context, key string) error // nil when already missing
}
