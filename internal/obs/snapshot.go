package obs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// A snapshot remembers the scene before StreamGremlin switched away, to
// switch back later, as for a "be right back" break. Switching back happens
// only while StreamGremlin still owns the scene: any change by someone else
// since, even to the same scene and back, means the streamer took over, and
// their choice stands (AGENTS.md: a manual change always defeats restoration).

// Snapshot statuses and the reasons a snapshot didn't switch back.
const (
	SnapOpen     = "open"
	SnapRestored = "restored"
	SnapSkipped  = "skipped"
	SnapFailed   = "failed"
	SnapReleased = "released"

	ReasonManualChange     = "manual_change"     // someone else changed the scene
	ReasonSuperseded       = "superseded"        // StreamGremlin switched again since
	ReasonOwnershipUnknown = "ownership_unknown" // nobody knows who set the scene
	ReasonNoSwitch         = "no_switch"         // OBS was already on the scene
	ReasonRestarted        = "restarted"         // StreamGremlin restarted; ownership was lost
	ReasonSwitchFailed     = "switch_failed"
	ReasonRestoreFailed    = "restore_failed"
)

var reasonText = map[string]string{
	ReasonManualChange:     "The scene was changed in OBS after the switch, so it was left as it is.",
	ReasonSuperseded:       "StreamGremlin switched scenes again since, so it was left as it is.",
	ReasonOwnershipUnknown: "It isn't known who set the current scene, so it was left as it is.",
	ReasonNoSwitch:         "OBS was already on that scene, so there was nothing to switch back.",
	ReasonRestarted:        "StreamGremlin restarted, so it no longer knows who set the scene.",
	ReasonSwitchFailed:     "Switching to the scene failed.",
	ReasonRestoreFailed:    "Switching back failed.",
}

// ErrNoSnapshot: nothing is open under the key.
var ErrNoSnapshot = errors.New("obs: no open snapshot under that name")

// Snapshot is one switch-and-remember.
type Snapshot struct {
	ID            string     `json:"id"`
	Key           string     `json:"key"`
	PreviousScene string     `json:"previous_scene"`
	TargetScene   string     `json:"target_scene"`
	Switched      bool       `json:"switched"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason,omitempty"`
	Message       string     `json:"message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
	// WouldRestore says, for an open snapshot, whether switching back would
	// happen now, and if not, why (Reason).
	WouldRestore *bool `json:"would_restore,omitempty"`
}

// Keeper takes and settles snapshots. One operation at a time.
type Keeper struct {
	svc *Service
	mu  sync.Mutex
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func owner(id string) string { return "snapshot:" + id }

// Take remembers the current scene under key and switches to scene. Asking
// again for the same key and scene while StreamGremlin still holds it reuses
// the open snapshot, so a second break during the first still returns to
// the scene from before the first.
func (k *Keeper) Take(ctx context.Context, key, scene string) (Snapshot, error) {
	if !keyPattern.MatchString(key) {
		return Snapshot{}, fmt.Errorf("%w: a snapshot name is lowercase letters, digits, - and _", ErrInvalid)
	}
	if strings.TrimSpace(scene) == "" {
		return Snapshot{}, fmt.Errorf("%w: which scene?", ErrInvalid)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	current, setBy, manualAt, known := k.svc.control()
	if !known {
		return Snapshot{}, ErrNotConnected
	}
	if open, err := k.open(ctx, key); err == nil {
		if ok, reason := verdict(open, current, setBy, manualAt); ok || reason == ReasonNoSwitch && current == open.TargetScene {
			if open.TargetScene == scene {
				return open, nil
			}
			return Snapshot{}, fmt.Errorf("%w: %q is already holding %s; switch back or release it first", ErrInvalid, key, open.TargetScene)
		} else if _, err := k.settle(ctx, open, SnapSkipped, reason); err != nil {
			return Snapshot{}, err
		}
	} else if !errors.Is(err, ErrNoSnapshot) {
		return Snapshot{}, err
	}

	snap := Snapshot{ID: events.NewID(), Key: key, PreviousScene: current, TargetScene: scene,
		Switched: current != scene, Status: SnapOpen, CreatedAt: k.svc.now().UTC().Truncate(time.Millisecond)}
	if _, err := k.svc.db.ExecContext(ctx, `
		INSERT INTO obs_snapshots (id, key, previous_scene, target_scene, switched, status, created_at)
		VALUES (?, ?, ?, ?, ?, 'open', ?)`,
		snap.ID, key, snap.PreviousScene, scene, b2i(snap.Switched), storage.Timestamp(snap.CreatedAt)); err != nil {
		return Snapshot{}, fmt.Errorf("obs: record the snapshot: %w", err)
	}
	if !snap.Switched {
		return snap, nil
	}
	if _, err := k.svc.SwitchScene(ctx, scene, owner(snap.ID)); err != nil {
		if isUnknown(err) {
			return snap, err // it may have switched: the snapshot stays open
		}
		failed, serr := k.settle(ctx, snap, SnapFailed, ReasonSwitchFailed)
		if serr != nil {
			return snap, serr
		}
		return failed, err
	}
	return snap, nil
}

// verdict says whether restoring snap now would switch back, and if not why.
func verdict(snap Snapshot, current, setBy string, manualAt time.Time) (bool, string) {
	switch {
	case !snap.Switched:
		return false, ReasonNoSwitch
	case !manualAt.IsZero() && manualAt.After(snap.CreatedAt):
		return false, ReasonManualChange
	case current == snap.TargetScene && setBy == owner(snap.ID):
		return true, ""
	case strings.HasPrefix(setBy, "snapshot:") || setBy != "":
		return false, ReasonSuperseded
	}
	return false, ReasonOwnershipUnknown
}

// Restore switches back to the scene from before the snapshot, if
// StreamGremlin still owns the current scene; otherwise it settles the
// snapshot as skipped, with the reason, and leaves OBS alone. With OBS
// disconnected nothing is decided: it returns ErrNotConnected and stays open.
func (k *Keeper) Restore(ctx context.Context, key string) (Snapshot, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	snap, err := k.open(ctx, key)
	if err != nil {
		return Snapshot{}, err
	}
	current, setBy, manualAt, known := k.svc.control()
	if !known {
		return snap, ErrNotConnected
	}
	ok, reason := verdict(snap, current, setBy, manualAt)
	if !ok {
		return k.settle(ctx, snap, SnapSkipped, reason)
	}
	if _, err := k.svc.SwitchScene(ctx, snap.PreviousScene, "restore:"+snap.ID); err != nil {
		if isUnknown(err) {
			return snap, err // it may have switched back: decide nothing
		}
		settled, serr := k.settle(ctx, snap, SnapFailed, ReasonRestoreFailed)
		if serr != nil {
			return snap, serr
		}
		return settled, err
	}
	return k.settle(ctx, snap, SnapRestored, "")
}

// Release closes a snapshot without switching: the streamer stays.
func (k *Keeper) Release(ctx context.Context, key string) (Snapshot, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	snap, err := k.open(ctx, key)
	if err != nil {
		return Snapshot{}, err
	}
	return k.settle(ctx, snap, SnapReleased, "")
}

// Open lists open snapshots, each with whether it would switch back now.
func (k *Keeper) Open(ctx context.Context) ([]Snapshot, error) {
	rows, err := k.svc.db.QueryContext(ctx, selectSnapshot+"WHERE status = 'open' ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current, setBy, manualAt, known := k.svc.control()
	out := []Snapshot{}
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		if known {
			ok, reason := verdict(s, current, setBy, manualAt)
			s.WouldRestore, s.Reason, s.Message = &ok, reason, reasonText[reason]
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// settleAfterRestart skips every snapshot left open: who set the scene was
// known only to the old process, so switching back now could undo the
// streamer's own choice.
func (k *Keeper) settleAfterRestart(ctx context.Context) error {
	rows, err := k.svc.db.QueryContext(ctx, selectSnapshot+"WHERE status = 'open'")
	if err != nil {
		return fmt.Errorf("obs: read snapshots: %w", err)
	}
	var open []Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		open = append(open, s)
	}
	_ = rows.Close()
	for _, s := range open {
		if _, err := k.settle(ctx, s, SnapSkipped, ReasonRestarted); err != nil {
			return err
		}
	}
	return nil
}

func (k *Keeper) open(ctx context.Context, key string) (Snapshot, error) {
	s, err := scanSnapshot(k.svc.db.QueryRowContext(ctx, selectSnapshot+"WHERE key = ? AND status = 'open'", key))
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNoSnapshot
	}
	return s, err
}

// settle records how a snapshot ended, and an event saying so.
func (k *Keeper) settle(ctx context.Context, s Snapshot, status, reason string) (Snapshot, error) {
	now := k.svc.now().UTC().Truncate(time.Millisecond)
	if _, err := k.svc.db.ExecContext(ctx,
		"UPDATE obs_snapshots SET status = ?, reason = ?, settled_at = ? WHERE id = ? AND status = 'open'",
		status, reason, storage.Timestamp(now), s.ID); err != nil {
		return s, fmt.Errorf("obs: settle the snapshot: %w", err)
	}
	s.Status, s.Reason, s.Message, s.SettledAt = status, reason, reasonText[reason], &now
	if status == SnapReleased {
		return s, nil
	}
	typ := "broadcast.scene.restored"
	if status != SnapRestored {
		typ = "broadcast.scene.restore_skipped"
	}
	payload := map[string]any{"snapshot_id": s.ID, "key": s.Key, "previous_scene": s.PreviousScene,
		"target_scene": s.TargetScene, "status": status}
	if reason != "" {
		payload["reason"], payload["message"] = reason, reasonText[reason]
	}
	if k.svc.inbox != nil {
		raw, _ := jsonMarshal(payload)
		if _, err := k.svc.inbox.Accept(ctx, events.Envelope{Type: typ, Source: "core", SourceEventID: typ + ":" + s.ID,
			Payload: raw, DeliveryQuality: events.Confirmed}); err != nil {
			k.svc.log.Warn("obs: couldn't store a snapshot event", "err", err)
		}
	}
	k.svc.log.Info("obs snapshot settled", "key", s.Key, "status", status, "reason", reason)
	return s, nil
}

const selectSnapshot = `SELECT id, key, previous_scene, target_scene, switched, status, reason, created_at, settled_at FROM obs_snapshots `

type rowScanner interface{ Scan(...any) error }

func scanSnapshot(r rowScanner) (Snapshot, error) {
	var s Snapshot
	var sw int
	var created string
	var settled sql.NullString
	if err := r.Scan(&s.ID, &s.Key, &s.PreviousScene, &s.TargetScene, &sw, &s.Status, &s.Reason, &created, &settled); err != nil {
		return s, err
	}
	s.Switched = sw == 1
	s.Message = reasonText[s.Reason]
	var err error
	if s.CreatedAt, err = storage.ParseTimestamp(created); err != nil {
		return s, err
	}
	if settled.Valid {
		t, err := storage.ParseTimestamp(settled.String)
		if err != nil {
			return s, err
		}
		s.SettledAt = &t
	}
	return s, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
