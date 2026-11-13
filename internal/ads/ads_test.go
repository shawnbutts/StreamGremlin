package ads

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// fakeTwitch answers as told and counts what was sent.
type fakeTwitch struct {
	mu          sync.Mutex
	commercials int
	snoozes     int
	commErr     error
	retryAfter  int
	schedule    twitch.AdSchedule
	gate        chan struct{} // when set, StartCommercial waits on it
}

func (f *fakeTwitch) StartCommercial(ctx context.Context, length int) (twitch.CommercialResult, error) {
	f.mu.Lock()
	f.commercials++
	err, retry, gate := f.commErr, f.retryAfter, f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return twitch.CommercialResult{}, err
	}
	return twitch.CommercialResult{Length: length, RetryAfter: retry}, nil
}

func (f *fakeTwitch) SnoozeAd(context.Context) (twitch.AdSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snoozes++
	if f.schedule.SnoozeCount == 0 {
		return twitch.AdSchedule{}, &twitch.AdRefusedError{Reason: "no_snoozes"}
	}
	f.schedule.SnoozeCount--
	return f.schedule, nil
}

func (f *fakeTwitch) Ads(context.Context) (twitch.AdSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.schedule, nil
}

func (f *fakeTwitch) sent() int { f.mu.Lock(); defer f.mu.Unlock(); return f.commercials }

type rig struct {
	t     *testing.T
	db    *storage.DB
	inbox *events.Inbox
	tw    *fakeTwitch
	svc   *Service
	mu    sync.Mutex
	now   time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, inbox: events.NewInbox(db, nil), tw: &fakeTwitch{}, now: time.Now().UTC()}
	r.start()
	return r
}

func (r *rig) clock() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }

func (r *rig) wait(d time.Duration) { r.mu.Lock(); r.now = r.now.Add(d); r.mu.Unlock() }

func (r *rig) start() {
	r.t.Helper()
	if r.svc != nil {
		r.svc.Close()
	}
	r.svc = New(Options{DB: r.db, Twitch: r.tw, Inbox: r.inbox, Now: r.clock})
	if err := r.svc.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(r.svc.Close)
}

func (r *rig) events(types ...string) []events.Record {
	r.t.Helper()
	recs, err := r.inbox.Latest(context.Background(), 100, events.Filter(types))
	if err != nil {
		r.t.Fatal(err)
	}
	return recs
}

func (r *rig) eventually(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	r.t.Fatal("timed out waiting for", what)
}

// breakBegins stores Twitch's event for a break that began at.
func (r *rig) breakBegins(at time.Time, seconds int, automatic bool, q events.Quality) {
	r.t.Helper()
	p, _ := json.Marshal(map[string]any{"duration_seconds": seconds, "is_automatic": automatic, "started_at": at.Format(time.RFC3339)})
	_, err := r.inbox.Accept(context.Background(), events.Envelope{Type: "ads.break.started", Source: "twitch",
		SourceEventID: "ad_break:1:" + at.Format(time.RFC3339), OccurredAt: at, DeliveryQuality: q, Payload: p})
	if err != nil {
		r.t.Fatal(err)
	}
}

func refusedFor(err error) string {
	var re *RefusedError
	if errors.As(err, &re) {
		return re.Reason
	}
	return ""
}

func TestCommercialIsSentOnceAndCoolsDown(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.retryAfter = 480
	q, err := r.svc.StartCommercial(ctx, 90, "op-1", "!ad by streamer")
	if err != nil || q.Status != Accepted || *q.AcceptedLength != 90 || q.RetryUntil == nil {
		t.Fatal(q, err)
	}
	// The same operation again: the same request, nothing sent.
	again, err := r.svc.StartCommercial(ctx, 90, "op-1", "!ad by streamer")
	if err != nil || again.ID != q.ID || r.tw.sent() != 1 {
		t.Fatal(again, err, r.tw.sent())
	}
	// A new one inside Twitch's gap is refused here, and never sent.
	if _, err := r.svc.StartCommercial(ctx, 30, "op-2", "app"); refusedFor(err) != "cooldown" || r.tw.sent() != 1 {
		t.Fatal(err, r.tw.sent())
	}
	r.wait(481 * time.Second)
	if _, err := r.svc.StartCommercial(ctx, 30, "op-3", "app"); err != nil || r.tw.sent() != 2 {
		t.Fatal(err)
	}
	for _, bad := range []int{0, 181} {
		if _, err := r.svc.StartCommercial(ctx, bad, "bad", "app"); refusedFor(err) != "invalid" {
			t.Fatal(bad, err)
		}
	}
	if n := len(r.events("ads.break.requested")); n != 2 {
		t.Fatal("requested events:", n)
	}
	// Accepted isn't "an ad is running": no break until Twitch says so.
	_, breaks, _ := r.svc.Recent(ctx, 10)
	if len(breaks) != 0 {
		t.Fatal(breaks)
	}
}

func TestRefusedAnnouncesNothing(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.commErr = &twitch.AdRefusedError{Reason: "not_live", Message: "channel is not live"}
	q, err := r.svc.StartCommercial(ctx, 60, "op", "app")
	if refusedFor(err) != "not_live" || q.Status != Refused {
		t.Fatal(q, err)
	}
	if len(r.events("ads.break.requested")) != 0 || len(r.events("ads.request.refused")) != 1 {
		t.Fatal("a refused request was announced")
	}
	// An answered request never changes, even by hand.
	if _, err := r.db.ExecContext(ctx, "UPDATE ad_requests SET status = 'accepted' WHERE id = ?", q.ID); err == nil {
		t.Fatal("an answered request changed")
	}
	if _, err := r.db.ExecContext(ctx, "DELETE FROM ad_requests"); err == nil {
		t.Fatal("a request was deleted")
	}
	// Repeating the refused operation repeats the refusal without sending.
	if _, err := r.svc.StartCommercial(ctx, 60, "op", "app"); refusedFor(err) != "not_live" || r.tw.sent() != 1 {
		t.Fatal(err, r.tw.sent())
	}
}

func TestUnknownIsNeverResentAndTheBreakConfirmsIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.tw.commErr = twitch.ErrAdOutcomeUnknown
	q, err := r.svc.StartCommercial(ctx, 60, "op-1", "app")
	if !errors.Is(err, ErrUnknown) || q.Status != Unknown {
		t.Fatal(q, err)
	}
	if len(r.events("ads.request.unresolved")) != 1 || len(r.events("ads.break.requested")) != 0 {
		t.Fatal("unknown announced as started")
	}
	// A second could start a second break.
	r.tw.commErr = nil
	if _, err := r.svc.StartCommercial(ctx, 60, "op-2", "app"); refusedFor(err) != "unresolved" || r.tw.sent() != 1 {
		t.Fatal(err)
	}
	// Twitch's event says it began: that's the break, and the confirmation.
	start := r.clock().Add(20 * time.Second)
	r.breakBegins(start, 60, false, events.Confirmed)
	r.breakBegins(start, 60, false, events.Confirmed) // delivered twice
	r.eventually("the break", func() bool {
		reqs, breaks, _ := r.svc.Recent(ctx, 10)
		return len(breaks) == 1 && breaks[0].RequestID == q.ID && reqs[0].Status == Accepted && reqs[0].ConfirmedBy == "event"
	})
	// Its end is estimated, never confirmed, and announced once.
	r.wait(81 * time.Second)
	r.eventually("the estimated end", func() bool { return len(r.events("ads.break.end_estimated")) == 1 })
	end := r.events("ads.break.end_estimated")[0]
	if end.DeliveryQuality != events.Estimated || !end.OccurredAt.Equal(start.Add(time.Minute).Truncate(time.Millisecond)) {
		t.Fatal(end.DeliveryQuality, end.OccurredAt, start)
	}
	time.Sleep(1200 * time.Millisecond)
	if n := len(r.events("ads.break.end_estimated")); n != 1 {
		t.Fatal("ends announced:", n)
	}
}

func TestTestBreaksAreNotRecorded(t *testing.T) {
	r := newRig(t)
	r.breakBegins(r.clock(), 30, true, events.Synthetic)
	r.breakBegins(r.clock().Add(time.Second), 30, true, events.Confirmed)
	r.eventually("the real break", func() bool {
		_, b, _ := r.svc.Recent(context.Background(), 10)
		return len(b) == 1 && b[0].Automatic && b[0].RequestID == ""
	})
}

func TestARestartMarksInFlightUnknownAndTheScheduleSettlesIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	gate := make(chan struct{})
	r.tw.gate = gate
	done := make(chan Request)
	go func() { q, _ := r.svc.StartCommercial(ctx, 30, "op", "app"); done <- q }()
	r.eventually("the send", func() bool { return r.tw.sent() == 1 })
	// While it's in flight another is refused, not queued.
	if _, err := r.svc.Snooze(ctx, "snooze-op", "app"); err != nil && refusedFor(err) != "no_snoozes" {
		t.Fatal(err)
	}
	// StreamGremlin stops before Twitch answers: simulate by starting a new
	// service on the same data while the request is pending.
	r.svc.Close()
	svc := New(Options{DB: r.db, Twitch: r.tw, Inbox: r.inbox, Now: r.clock})
	r.tw.mu.Lock()
	r.tw.schedule = twitch.AdSchedule{LastAdAt: new(r.clock().Add(5 * time.Second))}
	r.tw.mu.Unlock()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	reqs, _, _ := svc.Recent(ctx, 10)
	var q Request
	for _, x := range reqs {
		if x.Action == "commercial" {
			q = x
		}
	}
	if q.Status != Unknown && q.ConfirmedBy == "" {
		t.Fatal(q)
	}
	r.eventually("the schedule check", func() bool {
		reqs, _, _ := svc.Recent(ctx, 10)
		for _, x := range reqs {
			if x.Action == "commercial" {
				return x.Status == Accepted && x.ConfirmedBy == "schedule"
			}
		}
		return false
	})
	if r.tw.sent() != 1 {
		t.Fatal("sent again")
	}
	close(gate)
	<-done
}

func TestStaleEndsAreNotAnnounced(t *testing.T) {
	r := newRig(t)
	r.breakBegins(r.clock().Add(-20*time.Minute), 60, true, events.Confirmed)
	r.eventually("the break marked", func() bool {
		var n int
		_ = r.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM ad_breaks WHERE end_announced = 1").Scan(&n)
		return n == 1
	})
	if n := len(r.events("ads.break.end_estimated")); n != 0 {
		t.Fatal("a long-past end was announced:", n)
	}
}

func TestSnooze(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	next := r.clock().Add(10 * time.Minute)
	r.tw.schedule = twitch.AdSchedule{SnoozeCount: 1, NextAdAt: &next}
	q, err := r.svc.Snooze(ctx, "s1", "app")
	if err != nil || q.Status != Accepted || *q.SnoozeCount != 0 || q.NextAdAt == nil {
		t.Fatal(q, err)
	}
	if _, err := r.svc.Snooze(ctx, "s2", "app"); refusedFor(err) != "no_snoozes" {
		t.Fatal(err)
	}
	if len(r.events("ads.snooze.applied")) != 1 || len(r.events("ads.request.refused")) != 1 {
		t.Fatal("snooze events")
	}
}

// Twitch's event can say a break began before Twitch answers the request
// (D-094): the break is still linked to it, and the answer keeps that;
// no answer at all is still a break that began, not an unknown.
func TestTheBreakCanArriveBeforeTheAnswer(t *testing.T) {
	for _, answer := range []error{nil, twitch.ErrAdOutcomeUnknown} {
		r := newRig(t)
		ctx := context.Background()
		gate := make(chan struct{})
		r.tw.mu.Lock()
		r.tw.gate, r.tw.commErr = gate, answer
		r.tw.mu.Unlock()
		done := make(chan Request)
		go func() { q, _ := r.svc.StartCommercial(ctx, 60, "op", "app"); done <- q }()
		r.eventually("the request sent", func() bool { r.tw.mu.Lock(); defer r.tw.mu.Unlock(); return r.tw.commercials == 1 })
		r.breakBegins(r.clock().Add(time.Second), 60, false, events.Confirmed)
		r.eventually("the break", func() bool { _, breaks, _ := r.svc.Recent(ctx, 10); return len(breaks) == 1 })
		close(gate)
		q := <-done
		reqs, breaks, _ := r.svc.Recent(ctx, 10)
		if breaks[0].RequestID != q.ID || reqs[0].Status != Accepted || reqs[0].ConfirmedBy != "event" {
			t.Fatalf("answer %v: %+v %+v", answer, reqs[0], breaks[0])
		}
	}
}
