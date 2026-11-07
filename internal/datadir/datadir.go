// Package datadir finds, creates and locks the data folder: the one place
// StreamGremlin keeps its database, API token, logs and backups.
package datadir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/brand"
)

// EnvVar overrides the default data folder.
var EnvVar = brand.Env("DATA_DIR")

// Default returns the data folder: $STREAMGREMLIN_DATA_DIR if set, otherwise
// the per-user folder for this OS.
//
// Test binaries must never touch the user's real data folder, so Default
// refuses to guess when running under `go test` without the variable set.
func Default() (string, error) {
	if dir := os.Getenv(EnvVar); dir != "" {
		return filepath.Abs(dir)
	}
	if testing.Testing() {
		return "", fmt.Errorf("datadir: %s must be set in tests; refusing to use the real data folder", EnvVar)
	}
	return osDefault(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func osDefault(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	switch goos {
	case "windows":
		if dir := getenv("LocalAppData"); dir != "" {
			return filepath.Join(dir, brand.Name), nil
		}
		return "", errors.New("datadir: %LocalAppData% is not set")
	case "darwin":
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("datadir: %w", err)
		}
		return filepath.Join(h, "Library", "Application Support", brand.Name), nil
	default:
		if dir := getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, brand.Slug), nil
		}
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("datadir: %w", err)
		}
		return filepath.Join(h, ".local", "share", brand.Slug), nil
	}
}

// Ensure creates the data folder if needed and makes it private to the user.
func Ensure(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("datadir: create %s: %w", dir, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a folder needs its execute bit
			return fmt.Errorf("datadir: protect %s: %w", dir, err)
		}
	}
	return nil
}

// Paths of the files inside the data folder.
func DatabasePath(dir string) string { return filepath.Join(dir, brand.Slug+".db") }
func TokenPath(dir string) string    { return filepath.Join(dir, "api-token") }
func AddrPath(dir string) string     { return filepath.Join(dir, "api-addr") }
func LockPath(dir string) string     { return filepath.Join(dir, "lock") }
func LogDir(dir string) string       { return filepath.Join(dir, "logs") }
func BackupDir(dir string) string    { return filepath.Join(dir, "backups") }
