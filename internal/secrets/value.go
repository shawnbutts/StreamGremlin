// Package secrets keeps sign-in tokens and passwords out of the database,
// logs and API answers (docs/adr/0003-secrets.md). Values live in the OS
// keyring or in an encrypted file; the database holds only an index of keys.
package secrets

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
)

const redacted = "[redacted]"

// MaxValueBytes caps a secret's size. Windows Credential Manager allows
// 2560 bytes and the macOS `security` command line 4096, after encoding.
const MaxValueBytes = 2048

// Value holds a secret. Every way of printing, logging or encoding it gives
// "[redacted]"; only Reveal returns the contents, at the point of use.
type Value struct{ s string }

// NewValue wraps s.
func NewValue(s string) Value { return Value{s: s} }

// Reveal returns the secret. Call it only where the value leaves for the
// service that needs it, and never log the result.
func (v Value) Reveal() string { return v.s }

// IsZero reports whether the value is empty.
func (v Value) IsZero() bool { return v.s == "" }

func (Value) String() string               { return redacted }
func (Value) GoString() string             { return redacted }
func (Value) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Value) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Format covers every fmt verb, including %x and %q.
func (Value) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// UnmarshalJSON refuses: secrets never arrive inside a JSON document.
func (*Value) UnmarshalJSON([]byte) error {
	return errors.New("secrets: a Value can't be decoded from JSON")
}

// UnmarshalText refuses, for the same reason.
func (*Value) UnmarshalText([]byte) error {
	return errors.New("secrets: a Value can't be decoded from text")
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*(/[a-z0-9][a-z0-9_.-]*)*$`)

// ValidKey reports whether key is a valid secret key: lowercase segments
// of letters, digits, '_', '.' and '-', separated by '/', such as
// "twitch/12345/oauth". At most 128 characters.
func ValidKey(key string) bool { return len(key) <= 128 && keyPattern.MatchString(key) }
