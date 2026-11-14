package youtube

import (
	"context"
	"errors"
	"sync"
	"time"
	_ "time/tzdata" // Pacific time on every system, Windows included

	"code.streamgremlin.com/streamgremlin/internal/settings"
)

// DailyQuota is a Google project's default Data API quota: 10,000 units a
// day, for everyone signing in through that project (D-095).
const DailyQuota = 10_000

// ErrQuotaExhausted means the day's quota is spent: Google said so, and
// nothing more is asked of the Data API until midnight Pacific time.
var ErrQuotaExhausted = errors.New("youtube: today's YouTube quota is used up; it resets at midnight Pacific time")

// Quota costs of the calls StreamGremlin makes, from Google's table
// (https://developers.google.com/youtube/v3/determine_quota_cost).
const (
	CostChannelsList   = 1
	CostBroadcastsList = 1
	// CostStreamList is a chat stream's cost, counted once a connection:
	// Google's table doesn't say whether a long connection costs more.
	CostStreamList = 5
)

var pacific = func() *time.Location {
	l, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PT", -8*3600)
	}
	return l
}()

// QuotaStatus is today's use, as StreamGremlin counts it. Google doesn't
// report a project's use to apps, so it's an estimate: it counts only
// this computer's calls, and another app on the same project isn't seen.
type QuotaStatus struct {
	Used     int       `json:"used"`
	Limit    int       `json:"limit"`
	Day      string    `json:"day"` // the Pacific day counted, 2026-11-13
	ResetsAt time.Time `json:"resets_at"`
	// Exhausted: Google refused a call for quota today.
	Exhausted bool `json:"exhausted"`
}

const quotaKey = "youtube.quota"

type savedQuota struct {
	Day       string `json:"day"`
	Used      int    `json:"used"`
	Exhausted bool   `json:"exhausted,omitempty"`
}

// quota counts units spent today and remembers a refusal until midnight
// Pacific time. It's kept in settings, so a restart keeps the count.
type quota struct {
	mu       sync.Mutex
	settings *settings.Store
	now      func() time.Time
	loaded   bool
	q        savedQuota
}

func pacificDay(t time.Time) (string, time.Time) {
	p := t.In(pacific)
	y, m, d := p.Date()
	return p.Format("2006-01-02"), time.Date(y, m, d+1, 0, 0, 0, 0, pacific)
}

// current loads and rolls the day over. Call with mu held.
func (q *quota) current(ctx context.Context) {
	if !q.loaded {
		var s savedQuota
		if ok, err := q.settings.Load(ctx, quotaKey, &s); err == nil && ok {
			q.q = s
		}
		q.loaded = true
	}
	if day, _ := pacificDay(q.now()); q.q.Day != day {
		q.q = savedQuota{Day: day}
	}
}

// spend counts units before a call (Google counts a call even when it
// fails), or refuses it when Google has said the quota is spent.
func (q *quota) spend(ctx context.Context, units int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.current(ctx)
	if q.q.Exhausted {
		return ErrQuotaExhausted
	}
	q.q.Used += units
	_ = q.settings.Save(ctx, quotaKey, q.q) // best effort: the count is an estimate
	return nil
}

// exhausted records Google's refusal for quota.
func (q *quota) exhausted(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.current(ctx)
	q.q.Exhausted = true
	_ = q.settings.Save(ctx, quotaKey, q.q)
}

func (q *quota) status(ctx context.Context) QuotaStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.current(ctx)
	_, resets := pacificDay(q.now())
	return QuotaStatus{Used: q.q.Used, Limit: DailyQuota, Day: q.q.Day, ResetsAt: resets, Exhausted: q.q.Exhausted}
}
