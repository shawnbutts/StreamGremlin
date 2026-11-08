package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/zalando/go-keyring"

	"code.streamgremlin.com/streamgremlin/internal/brand"
)

// keyringTimeout bounds every keyring call, so an unlock prompt nobody
// answers can't hang the engine. It stays under the API's 30-second write
// timeout, so the caller always gets an answer.
const keyringTimeout = 25 * time.Second

// probeTimeout bounds the first-start check of whether a keyring answers.
const probeTimeout = 5 * time.Second

// provider is the OS keyring; tests swap in a fake so they never touch the
// real one (AGENTS.md).
type provider interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (osKeyring) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (osKeyring) Delete(s, u string) error        { return keyring.Delete(s, u) }

// keyringBackend keeps each secret as one item: service
// "StreamGremlin (<installation id>)", account = key. The installation id
// keeps separate data folders apart.
type keyringBackend struct {
	p       provider
	service string
	timeout time.Duration
}

func newKeyringBackend(p provider, installationID string) *keyringBackend {
	return &keyringBackend{p: p, service: keyringService(installationID), timeout: keyringTimeout}
}

func keyringService(installationID string) string {
	return brand.Name + " (" + installationID + ")"
}

func (k *keyringBackend) get(ctx context.Context, key string) (string, error) {
	var out string
	err := k.call(ctx, func() error {
		v, err := k.p.Get(k.service, key)
		out = v
		return err
	})
	return out, err
}

func (k *keyringBackend) set(ctx context.Context, key, value string) error {
	return k.call(ctx, func() error { return k.p.Set(k.service, key, value) })
}

func (k *keyringBackend) del(ctx context.Context, key string) error {
	err := k.call(ctx, func() error { return k.p.Delete(k.service, key) })
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// call runs fn with a deadline. The keyring libraries can't be cancelled,
// so on timeout the call is left to finish in the background.
func (k *keyringBackend) call(ctx context.Context, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		switch {
		case err == nil:
			return nil
		case errors.Is(err, keyring.ErrNotFound):
			return ErrNotFound
		case errors.Is(err, keyring.ErrSetDataTooBig):
			return fmt.Errorf("%w: the value is too large for the keyring", ErrInvalid)
		}
		return fmt.Errorf("secrets: keyring: %w", err)
	case <-ctx.Done():
		return fmt.Errorf("%w: %s didn't answer within %s; it may be locked or waiting for you to allow access",
			ErrUnavailable, KeyringLabel(runtime.GOOS), k.timeout)
	}
}

// keyringAnswers reports whether the OS keyring answers a read-only lookup
// of a key that doesn't exist. It never writes.
func keyringAnswers(ctx context.Context, p provider, goos string, getenv func(string) string) bool {
	if goos != "darwin" && goos != "windows" && getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		// Without a session bus, the D-Bus library would start a stray daemon.
		return false
	}
	k := &keyringBackend{p: p, service: brand.Name + " (probe)", timeout: probeTimeout}
	_, err := k.get(ctx, "probe")
	return err == nil || errors.Is(err, ErrNotFound)
}

// KeyringLabel names the OS keyring the way the streamer knows it.
func KeyringLabel(goos string) string {
	switch goos {
	case "darwin":
		return "macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	}
	return "your system keyring (Secret Service)"
}

// realKeyringAllowed guards against tests touching the developer's keyring.
func realKeyringAllowed(inTest bool) bool {
	return !inTest || os.Getenv(brand.Env("TEST_OS_KEYRING")) == "1"
}
