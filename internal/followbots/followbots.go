// Package followbots finds follows that look like follow bots, for the
// streamer to review (M8 slice 5, D-109): Twitch follows from accounts
// made only days before they followed, that never chatted, especially when
// several came within minutes. Nothing is done here: the review blocks or
// bans what the streamer picks, through a ban list (D-106).
package followbots

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// TypeFollow is a follow, from Twitch.
const TypeFollow = "audience.follow.created"

// Limits.
const (
	MaxFollows = 5000 // the most recent follows looked at
	MaxHours   = 24 * 30
)

// ErrInvalid wraps bad options.
var ErrInvalid = errors.New("followbots: invalid")

// Options are what a review looks for.
type Options struct {
	// Hours: follows from the last this many hours.
	Hours int `json:"hours"`
	// Days: accounts younger than this when they followed.
	Days int `json:"days"`
	// Burst follows this many within Minutes minutes are "in a burst".
	Burst   int `json:"burst"`
	Minutes int `json:"minutes"`
}

// Defaults are a review's options until changed.
func Defaults() Options { return Options{Hours: 24, Days: 7, Burst: 5, Minutes: 10} }

// Check says what's wrong with options.
func Check(o Options) error {
	switch {
	case o.Hours < 1 || o.Hours > MaxHours:
		return fmt.Errorf("%w: look back 1 to %d hours", ErrInvalid, MaxHours)
	case o.Days < 1 || o.Days > 365:
		return fmt.Errorf("%w: an account's age is 1 to 365 days", ErrInvalid)
	case o.Burst < 2 || o.Burst > 100 || o.Minutes < 1 || o.Minutes > 120:
		return fmt.Errorf("%w: a burst is 2 to 100 follows within 1 to 120 minutes", ErrInvalid)
	}
	return nil
}

// Suspect is one follow that looks like a bot.
type Suspect struct {
	UserID      string    `json:"user_id"`
	Login       string    `json:"login"`
	DisplayName string    `json:"display_name"`
	FollowedAt  time.Time `json:"followed_at"`
	// AgeDays is how old the account was when it followed.
	AgeDays int `json:"age_days"`
	// InBurst: one of several young, silent follows within minutes.
	InBurst bool `json:"in_burst"`
}

// Review is what a review found.
type Review struct {
	Options  Options   `json:"options"`
	Follows  int       `json:"follows"` // follows looked at
	Suspects []Suspect `json:"suspects"`
	// Unknown counts follows whose account age Twitch didn't give.
	Unknown int `json:"unknown"`
}

// Inbox is what a review reads.
type Inbox interface {
	Latest(ctx context.Context, limit int, f events.Filter) ([]events.Record, error)
}

// Deps are what a review asks.
type Deps struct {
	Inbox Inbox
	// Users looks accounts up, 100 at a time, for their age.
	Users func(ctx context.Context, logins, ids []string) ([]twitch.User, twitch.RateInfo, error)
	// Chatted says whether StreamGremlin has seen the account chat.
	Chatted func(ctx context.Context, userID string) (bool, error)
	Now     func() time.Time
}

// Find reviews the recent follows.
func Find(ctx context.Context, d Deps, o Options) (Review, error) {
	if err := Check(o); err != nil {
		return Review{}, err
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	since := now().Add(-time.Duration(o.Hours) * time.Hour)
	recs, err := d.Inbox.Latest(ctx, MaxFollows, events.Filter{TypeFollow})
	if err != nil {
		return Review{}, err
	}
	// The first follow of each account in the time, oldest first.
	type follow struct {
		a  events.Actor
		at time.Time
	}
	seen := map[string]bool{}
	var follows []follow
	for _, r := range recs {
		if r.Source != "twitch" || r.Actor == nil || r.Actor.ID == "" || r.DeliveryQuality == events.Synthetic || r.OccurredAt.Before(since) {
			continue
		}
		if seen[r.Actor.ID] {
			continue
		}
		seen[r.Actor.ID] = true
		follows = append(follows, follow{*r.Actor, r.OccurredAt})
	}
	sort.Slice(follows, func(i, j int) bool { return follows[i].at.Before(follows[j].at) })
	rv := Review{Options: o, Follows: len(follows), Suspects: []Suspect{}}
	// Ages, 100 at a time.
	made := map[string]time.Time{}
	for i := 0; i < len(follows); i += twitch.MaxLookup {
		var ids []string
		for _, f := range follows[i:min(i+twitch.MaxLookup, len(follows))] {
			ids = append(ids, f.a.ID)
		}
		users, _, err := d.Users(ctx, nil, ids)
		if err != nil {
			return Review{}, err
		}
		for _, u := range users {
			made[u.ID] = u.CreatedAt
		}
	}
	var young []Suspect
	for _, f := range follows {
		m, ok := made[f.a.ID]
		if !ok || m.IsZero() {
			rv.Unknown++
			continue
		}
		age := int(f.at.Sub(m).Hours() / 24)
		if age >= o.Days {
			continue
		}
		chatted, err := d.Chatted(ctx, f.a.ID)
		if err != nil {
			return Review{}, err
		}
		if chatted {
			continue
		}
		young = append(young, Suspect{UserID: f.a.ID, Login: f.a.Login, DisplayName: f.a.DisplayName, FollowedAt: f.at, AgeDays: age})
	}
	// In a burst: Burst or more young, silent follows within Minutes.
	window := time.Duration(o.Minutes) * time.Minute
	for i := range young {
		n := 0
		for j := range young {
			d := young[j].FollowedAt.Sub(young[i].FollowedAt)
			if d >= 0 && d <= window {
				n++
			}
		}
		if n >= o.Burst {
			for j := range young {
				d := young[j].FollowedAt.Sub(young[i].FollowedAt)
				if d >= 0 && d <= window {
					young[j].InBurst = true
				}
			}
		}
	}
	// Newest first.
	for i := len(young) - 1; i >= 0; i-- {
		rv.Suspects = append(rv.Suspects, young[i])
	}
	return rv, nil
}

// ChattedFrom says from the viewers' record whether an account has chatted.
func ChattedFrom(v *viewers.Service) func(ctx context.Context, userID string) (bool, error) {
	return func(ctx context.Context, userID string) (bool, error) {
		x, err := v.ByAccount(ctx, "twitch", userID)
		if errors.Is(err, viewers.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return x.Messages > 0, nil
	}
}
