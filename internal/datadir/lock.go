package datadir

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrLocked means another StreamGremlin process is using the data folder.
var ErrLocked = errors.New("another StreamGremlin process is using this data folder")

// LockedError says which process holds the lock, when that is known.
type LockedError struct {
	Dir string
	PID int // 0 when unknown
}

func (e *LockedError) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("%s (%s, process %d)", ErrLocked, e.Dir, e.PID)
	}
	return fmt.Sprintf("%s (%s)", ErrLocked, e.Dir)
}

func (e *LockedError) Is(target error) bool { return target == ErrLocked }

// Lock is an exclusive, OS-level lock on a data folder. The OS releases it
// when the process exits, even after a crash, so there are no stale locks.
type Lock struct {
	f *os.File
}

// Acquire takes the lock on dir without waiting. If another process holds it,
// the error matches ErrLocked.
func Acquire(dir string) (*Lock, error) {
	path := LockPath(dir)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("datadir: open lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errWouldBlock) {
			return nil, &LockedError{Dir: dir, PID: readPID(path)}
		}
		return nil, fmt.Errorf("datadir: lock: %w", err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	}
	return &Lock{f: f}, nil
}

// Release gives the lock up.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = l.f.Truncate(0)
	err := unlockFile(l.f)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}
