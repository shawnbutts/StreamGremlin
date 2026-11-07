package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
)

// Permission names one thing a caller may do. Every route declares one.
type Permission string

const (
	PermHealthRead    Permission = "health.read"
	PermSettingsRead  Permission = "settings.read"
	PermSettingsWrite Permission = "settings.write"
)

// Principal is an authenticated caller.
type Principal struct {
	Name  string
	perms map[Permission]bool // nil means every permission
}

// Admin is the holder of the API token: the streamer, through the app or CLI.
var Admin = Principal{Name: "admin"}

// Can reports whether p holds perm. Every route's check goes through here.
func (p Principal) Can(perm Permission) bool {
	return p.perms == nil || p.perms[perm]
}

// tokenBytes is the random length of the API token before encoding.
const tokenBytes = 32

// LoadOrCreateToken reads the API token from path, creating a new random one
// (mode 0600) if the file is missing. Deleting the file and restarting
// rotates the token.
func LoadOrCreateToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		tok := strings.TrimSpace(string(b))
		if raw, derr := base64.RawURLEncoding.DecodeString(tok); derr != nil || len(raw) != tokenBytes {
			return "", fmt.Errorf("api: the token file %s is damaged; delete it and restart to make a new one", path)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0o600); err != nil {
				return "", fmt.Errorf("api: protect token file: %w", err)
			}
		}
		return tok, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("api: read token: %w", err)
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("api: generate token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("api: write token: %w", err)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("api: write token: %w", err)
	}
	return tok, f.Close()
}

// tokenMatches compares in constant time.
func tokenMatches(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// loopbackHost reports whether the Host header names this machine. Checking
// it blocks DNS-rebinding attacks from web pages.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// RequireLoopback refuses any listen address that isn't a loopback IP.
func RequireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("api: listen address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("api: listen address %q is not a loopback IP; remote access isn't supported", addr)
	}
	return nil
}
