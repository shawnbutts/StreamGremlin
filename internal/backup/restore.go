package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Result says how the last restore went.
type Result struct {
	At         time.Time `json:"at"`
	From       string    `json:"from"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	PreRestore string    `json:"pre_restore,omitempty"` // the backup of what it replaced
	// KeptAside: when the replaced database couldn't be backed up (it was
	// damaged, say), its files were kept in this folder instead.
	KeptAside string `json:"kept_aside,omitempty"`
}

func pendingPath(dataDir string) string { return filepath.Join(dataDir, "restore-pending") }
func resultPath(dataDir string) string  { return filepath.Join(dataDir, "restore-result.json") }

// Stage checks a backup fully and marks it to be restored at the next
// start. Nothing changes until then; staging another replaces the mark.
func Stage(ctx context.Context, dataDir, name string) error {
	tmp, err := Verify(ctx, dataDir, name)
	if err != nil {
		return err
	}
	_ = os.Remove(tmp)
	return os.WriteFile(pendingPath(dataDir), []byte(name), 0o600)
}

// Pending returns the backup marked to be restored, or "".
func Pending(dataDir string) string {
	b, err := os.ReadFile(pendingPath(dataDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Unstage removes the mark.
func Unstage(dataDir string) error {
	err := os.Remove(pendingPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// LastResult returns how the last restore went, or nil.
func LastResult(dataDir string) *Result {
	b, err := os.ReadFile(resultPath(dataDir))
	if err != nil {
		return nil
	}
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// ApplyPending restores the marked backup. Call it at start, holding the
// data folder's lock, before anything opens the database. The mark is
// removed first, so a backup that fails can't stop every later start. On
// any failure the current database is left as it was.
func ApplyPending(ctx context.Context, dataDir, appVersion string, now time.Time) *Result {
	name := Pending(dataDir)
	if name == "" {
		return nil
	}
	_ = Unstage(dataDir)
	res := &Result{At: now.UTC(), From: name}
	if err := apply(ctx, dataDir, appVersion, name, now, res); err != nil {
		res.Error = strings.TrimPrefix(err.Error(), "backup: it can't be used: ")
	} else {
		res.OK = true
	}
	if b, err := json.Marshal(res); err == nil {
		_ = os.WriteFile(resultPath(dataDir), b, 0o600)
	}
	return res
}

func apply(ctx context.Context, dataDir, appVersion, name string, now time.Time, res *Result) error {
	staged, err := Verify(ctx, dataDir, name)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()
	dbPath := datadir.DatabasePath(dataDir)
	parts := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}

	// Keep what's being replaced: as a backup, or, if the database can't be
	// opened to make one, as its files.
	aside := filepath.Join(Dir(dataDir), fmt.Sprintf("before-restore-%s", now.UTC().Format(stamp)))
	keepFiles := false
	if _, err := os.Stat(dbPath); err == nil {
		db, err := storage.Open(ctx, dbPath)
		if err == nil {
			var info Info
			info, err = Create(ctx, db, dataDir, PreRestore, appVersion, now)
			_ = db.Close()
			res.PreRestore = info.Name
		}
		if err != nil {
			keepFiles = true
		}
	}
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return err
	}
	var moved []string
	undo := func() {
		_ = os.Remove(dbPath)
		for _, p := range moved {
			_ = os.Rename(filepath.Join(aside, filepath.Base(p)), p)
		}
	}
	for _, p := range parts {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Rename(p, filepath.Join(aside, filepath.Base(p))); err != nil {
			undo()
			return fmt.Errorf("couldn't move the current database aside: %w", err)
		}
		moved = append(moved, p)
	}
	if err := os.Rename(staged, dbPath); err != nil {
		undo()
		return fmt.Errorf("couldn't put the backup in place: %w", err)
	}
	if keepFiles {
		res.KeptAside = filepath.Base(aside)
	} else {
		_ = os.RemoveAll(aside)
	}
	return nil
}
