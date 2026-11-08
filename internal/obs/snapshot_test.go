package obs

import (
	"context"
	"errors"
	"testing"
)

// takeAndSettle takes a snapshot and waits until OBS has reported the switch.
func takeAndSettle(t *testing.T, s *Service, key, scene string) Snapshot {
	t.Helper()
	snap, err := s.Snapshots().Take(context.Background(), key, scene)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Switched {
		waitFor(t, "the switch to be ours", func() bool { return s.Status().SceneSetBy == owner(snap.ID) })
	}
	return snap
}

func TestSnapshotSwitchesBackWhileItOwnsTheScene(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	ctx := context.Background()
	snap := takeAndSettle(t, s, "brb", "Be Right Back")
	if snap.PreviousScene != "Starting Soon" || r.mock.Current() != "Be Right Back" {
		t.Fatalf("%+v", snap)
	}
	open, _ := s.Snapshots().Open(ctx)
	if len(open) != 1 || open[0].WouldRestore == nil || !*open[0].WouldRestore {
		t.Fatalf("%+v", open)
	}
	got, err := s.Snapshots().Restore(ctx, "brb")
	if err != nil || got.Status != SnapRestored || r.mock.Current() != "Starting Soon" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.Snapshots().Restore(ctx, "brb"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("restoring twice: %v", err)
	}
	waitFor(t, "the restored event", func() bool { return len(eventsOf(t, r.inbox, "broadcast.scene.restored")) == 1 })
}

// The AGENTS.md invariant: a manual OBS scene change always defeats
// automatic scene restoration.
func TestAManualChangeAlwaysDefeatsRestoration(t *testing.T) {
	cases := map[string]func(r *rig){
		"to another scene":          func(r *rig) { r.mock.SwitchByHand("Gaming") },
		"away and back to the same": func(r *rig) { r.mock.SwitchByHand("Gaming"); r.mock.SwitchByHand("Be Right Back") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			s := r.connected()
			takeAndSettle(t, s, "brb", "Be Right Back")
			before := s.Status().LastManualChangeAt
			change(r)
			waitFor(t, "the manual change", func() bool {
				at := s.Status().LastManualChangeAt
				return at != nil && (before == nil || at.After(*before)) && s.Status().Scene == r.mock.Current()
			})
			stayed := r.mock.Current()
			got, err := s.Snapshots().Restore(context.Background(), "brb")
			if err != nil || got.Status != SnapSkipped || got.Reason != ReasonManualChange {
				t.Fatalf("%+v %v", got, err)
			}
			if r.mock.Current() != stayed {
				t.Fatal("restoration overrode the streamer's choice")
			}
			skipped := eventsOf(t, r.inbox, "broadcast.scene.restore_skipped")
			if len(skipped) != 1 || skipped[0]["reason"] != ReasonManualChange {
				t.Fatalf("%+v", skipped)
			}
		})
	}
}

func TestAChangeWhileDisconnectedDefeatsRestorationToo(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	ctx := context.Background()
	takeAndSettle(t, s, "brb", "Be Right Back")
	r.mock.Drop()
	waitState(t, s, StateDisconnected)
	if _, err := s.Snapshots().Restore(ctx, "brb"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("disconnected: %v", err) // nothing decided while blind
	}
	r.mock.SwitchByHand("Gaming") // nobody sees this live
	waitState(t, s, StateConnected)
	got, err := s.Snapshots().Restore(ctx, "brb")
	if err != nil || got.Reason != ReasonManualChange || r.mock.Current() != "Gaming" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSecondBreakReusesTheFirstSnapshot(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	ctx := context.Background()
	first := takeAndSettle(t, s, "brb", "Be Right Back")
	again, err := s.Snapshots().Take(ctx, "brb", "Be Right Back")
	if err != nil || again.ID != first.ID || again.PreviousScene != "Starting Soon" {
		t.Fatalf("%+v %v", again, err)
	}
	if _, err := s.Snapshots().Take(ctx, "brb", "Gaming"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another scene under the same name: %v", err)
	}
	// Another StreamGremlin switch supersedes it.
	if _, err := s.SwitchScene(ctx, "Gaming", "workflow:x"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ours", func() bool { return s.Status().SceneSetBy == "workflow:x" })
	got, _ := s.Snapshots().Restore(ctx, "brb")
	if got.Reason != ReasonSuperseded || r.mock.Current() != "Gaming" {
		t.Fatalf("%+v", got)
	}
}

func TestReleaseNoSwitchAndRestart(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	ctx := context.Background()
	snap := takeAndSettle(t, s, "same", "Starting Soon")
	if snap.Switched {
		t.Fatal("switched to the scene it was on")
	}
	if got, _ := s.Snapshots().Restore(ctx, "same"); got.Reason != ReasonNoSwitch {
		t.Fatal(got)
	}
	takeAndSettle(t, s, "brb", "Be Right Back")
	if got, err := s.Snapshots().Release(ctx, "brb"); err != nil || got.Status != SnapReleased || r.mock.Current() != "Be Right Back" {
		t.Fatalf("%+v %v", got, err)
	}
	takeAndSettle(t, s, "break", "Gaming")
	s.Close()
	s2 := r.service() // a restart: ownership was in memory, so nothing switches back
	waitState(t, s2, StateConnected)
	if _, err := s2.Snapshots().Restore(ctx, "break"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal(err)
	}
	var reason string
	_ = r.db.QueryRow("SELECT reason FROM obs_snapshots WHERE key = 'break'").Scan(&reason)
	if reason != ReasonRestarted || r.mock.Current() != "Gaming" {
		t.Fatal(reason)
	}
	if _, err := r.db.Exec("DELETE FROM obs_snapshots"); err == nil {
		t.Fatal("snapshots were deleted")
	}
}

func TestFailedSwitchSettlesAsFailed(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	got, err := s.Snapshots().Take(context.Background(), "brb", "No Such Scene")
	if !IsNotFound(err) || got.Status != SnapFailed || got.Reason != ReasonSwitchFailed {
		t.Fatalf("%+v %v", got, err)
	}
	if open, _ := s.Snapshots().Open(context.Background()); len(open) != 0 {
		t.Fatal(open)
	}
	if _, err := s.Snapshots().Take(context.Background(), "Bad Key", "Gaming"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
