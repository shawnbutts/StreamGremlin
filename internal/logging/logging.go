// Package logging sets up structured logs to stderr and to a file in the
// data folder. Callers must never log secrets; the API logs no headers.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// maxSize is how large a log may grow before it is rotated at startup.
const maxSize = 10 << 20

// Open returns a logger writing text to stderr and to <logDir>/<component>.log,
// plus a function that closes the file. A log over 10 MiB is renamed to
// <component>.log.1 first, replacing any older one.
func Open(logDir, component string, level slog.Level) (*slog.Logger, func() error, error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}
	path := filepath.Join(logDir, component+".log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}
	h := slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: level})
	return slog.New(h).With("component", component), f.Close, nil
}
