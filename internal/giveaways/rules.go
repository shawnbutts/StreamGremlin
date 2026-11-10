package giveaways

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Entry rules (docs/adr/0026-giveaway-entry-rules.md) decide who may
// enter, never anyone's chance (extra chances are set apart, ADR-0027).
// They're checked when someone enters, outside the database and the
// service's lock, and nothing about a viewer is cached or guessed: when
// Twitch can't be asked, the viewer is told to try again.

// Rule limits.
const (
	MaxRuleDays     = 3650
	MaxSubMonths    = 120
	MaxMessages     = 1000
	MaxCooldownDays = 365
	// streamLookback is how far back a stream start counts; without one,
	// messages count from quietHours ago.
	streamLookback = 48 * time.Hour
	quietHours     = 12 * time.Hour
)

// Rules are who may enter. Each is off at zero.
type Rules struct {
	// Followers: follows the Twitch channel, for at least FollowDays.
	Followers  bool `json:"followers,omitempty"`
	FollowDays int  `json:"follow_days,omitempty"`
	// AccountDays: the Twitch account is at least this old.
	AccountDays int `json:"account_days,omitempty"`
	// SubMonths: subscribed at least this many months, from the subscriber
	// badge on their chat message.
	SubMonths int `json:"sub_months,omitempty"`
	// Messages: chat messages (not commands) StreamGremlin saw from them
	// this stream before they entered.
	Messages int `json:"messages,omitempty"`
	// WinCooldownDays: no win in another giveaway in this many days.
	WinCooldownDays int `json:"win_cooldown_days,omitempty"`
}

// Qualified is what an entry was checked against.
type Qualified struct {
	FollowedAt       *time.Time `json:"followed_at,omitempty"`
	AccountCreatedAt *time.Time `json:"account_created_at,omitempty"`
	SubMonths        int        `json:"sub_months,omitempty"`
	Messages         *int       `json:"messages,omitempty"`
}

// Reasons someone is turned away.
const (
	ReasonSubscribers = "subscribers_only"
	ReasonSubMonths   = "sub_months"
	ReasonFollowers   = "followers"
	ReasonFollowDays  = "follow_days"
	ReasonAccountDays = "account_days"
	ReasonMessages    = "messages"
	ReasonRecentWin   = "recent_win"
	ReasonTwitchOnly  = "twitch_only"
	ReasonUnchecked   = "unchecked"
)

func (r *Rules) normalise(subscribersOnly *bool) error {
	switch {
	case r.FollowDays < 0 || r.FollowDays > MaxRuleDays:
		return invalid("followed for 0 to %d days", MaxRuleDays)
	case r.AccountDays < 0 || r.AccountDays > MaxRuleDays:
		return invalid("an account at least 0 to %d days old", MaxRuleDays)
	case r.SubMonths < 0 || r.SubMonths > MaxSubMonths:
		return invalid("subscribed for 0 to %d months", MaxSubMonths)
	case r.Messages < 0 || r.Messages > MaxMessages:
		return invalid("0 to %d chat messages first", MaxMessages)
	case r.WinCooldownDays < 0 || r.WinCooldownDays > MaxCooldownDays:
		return invalid("no win in the last 0 to %d days", MaxCooldownDays)
	}
	if r.FollowDays > 0 {
		r.Followers = true
	}
	if r.SubMonths > 0 {
		*subscribersOnly = true
	}
	return nil
}

// twitchOnly reports whether a rule needs Twitch to answer for the viewer.
func (r Rules) twitchOnly() bool { return r.Followers || r.AccountDays > 0 || r.SubMonths > 0 }

func days(n int) string { return plural(n, "day", "days") }

// Who says who can enter, as the opening message does ("" for anyone).
func Who(g *Giveaway) string {
	var parts []string
	r := g.Rules
	switch {
	case r.SubMonths > 0:
		parts = append(parts, fmt.Sprintf("subscribers of %s or more", plural(r.SubMonths, "month", "months")))
	case g.SubscribersOnly:
		parts = append(parts, "subscribers")
	}
	switch {
	case r.FollowDays > 0:
		parts = append(parts, fmt.Sprintf("followers of %s or more", days(r.FollowDays)))
	case r.Followers:
		parts = append(parts, "followers")
	}
	if r.AccountDays > 0 {
		parts = append(parts, fmt.Sprintf("Twitch accounts at least %s old", days(r.AccountDays)))
	}
	if r.Messages > 0 {
		parts = append(parts, fmt.Sprintf("after %s in chat this stream", plural(r.Messages, "message", "messages")))
	}
	if r.WinCooldownDays > 0 {
		parts = append(parts, fmt.Sprintf("no giveaway win in the last %s", days(r.WinCooldownDays)))
	}
	return strings.Join(parts, "; ")
}

// refusalWords end a chat message naming people turned away for reason.
func refusalWords(g *Giveaway, reason string) string {
	again := fmt.Sprintf(" Type !%s to try again.", g.Command)
	r := g.Rules
	switch reason {
	case ReasonSubscribers:
		return fmt.Sprintf(": %s is for subscribers only.", g.Title)
	case ReasonSubMonths:
		return fmt.Sprintf(": %s is for subscribers of %s or more.", g.Title, plural(r.SubMonths, "month", "months"))
	case ReasonFollowers:
		return fmt.Sprintf(": %s is for followers. Follow, then type !%s.", g.Title, g.Command)
	case ReasonFollowDays:
		return fmt.Sprintf(": %s is for followers of %s or more.", g.Title, days(r.FollowDays))
	case ReasonAccountDays:
		return fmt.Sprintf(": %s is for Twitch accounts at least %s old.", g.Title, days(r.AccountDays))
	case ReasonMessages:
		return fmt.Sprintf(": %s needs %s in chat this stream first.%s", g.Title, plural(r.Messages, "message", "messages"), again)
	case ReasonRecentWin:
		return fmt.Sprintf(": you won a giveaway in the last %s, so you can't enter %s.", days(r.WinCooldownDays), g.Title)
	case ReasonTwitchOnly:
		return fmt.Sprintf(": %s's rules can only be checked for Twitch chat.", g.Title)
	default:
		return fmt.Sprintf(": Twitch couldn't be asked just now, so you're not in %s yet.%s", g.Title, again)
	}
}

// candidate is someone asking to enter, waiting to be checked.
type candidate struct {
	g        Giveaway
	r        events.Record
	platform string
	c        chatter
}

// subMonths reads months subscribed from the subscriber badge's info,
// which Twitch documents as the months subscribed. ok is false without
// one (a founder badge carries no months).
func subMonths(c chatter) (int, bool) {
	for _, b := range c.Badges {
		if b.SetID == "subscriber" {
			n, err := strconv.Atoi(b.Info)
			return n, err == nil
		}
	}
	return 0, false
}

// qualify checks a candidate against the rules: cheap checks first, then
// Twitch. reason is "" when they may enter.
func (s *Service) qualify(ctx context.Context, cd candidate) (q Qualified, reason string) {
	g, c := &cd.g, cd.c
	r := g.Rules
	if r.twitchOnly() && cd.platform != "twitch" {
		return q, ReasonTwitchOnly
	}
	if g.SubscribersOnly && !c.Subscriber {
		return q, ReasonSubscribers
	}
	if r.SubMonths > 0 {
		m, _ := subMonths(c)
		q.SubMonths = m
		if m < r.SubMonths {
			return q, ReasonSubMonths
		}
	}
	now := s.now()
	if r.WinCooldownDays > 0 {
		won, err := s.wonSince(ctx, g, cd.platform, c.ID, now.AddDate(0, 0, -r.WinCooldownDays))
		if err != nil {
			s.log.Warn("couldn't check recent wins", "err", err)
			return q, ReasonUnchecked
		}
		if won {
			return q, ReasonRecentWin
		}
	}
	if r.Messages > 0 {
		n, err := s.messagesBefore(ctx, g, cd)
		if err != nil {
			s.log.Warn("couldn't count chat messages", "err", err)
			return q, ReasonUnchecked
		}
		q.Messages = &n
		if n < r.Messages {
			return q, ReasonMessages
		}
	}
	if r.Followers {
		followed, follows, ok := s.followed(ctx, g, c)
		if !ok {
			return q, ReasonUnchecked
		}
		if !follows {
			return q, ReasonFollowers
		}
		q.FollowedAt = &followed
		if r.FollowDays > 0 && followed.After(now.AddDate(0, 0, -r.FollowDays)) {
			return q, ReasonFollowDays
		}
	}
	if r.AccountDays > 0 {
		made, ok := s.accountMade(ctx, g, c)
		if !ok {
			return q, ReasonUnchecked
		}
		q.AccountCreatedAt = &made
		if made.After(now.AddDate(0, 0, -r.AccountDays)) {
			return q, ReasonAccountDays
		}
	}
	return q, ""
}

// followed asks Twitch, or a practice chatter's facts.
func (s *Service) followed(ctx context.Context, g *Giveaway, c chatter) (at time.Time, follows, ok bool) {
	if g.Practice {
		if c.Practice == nil {
			return at, false, false
		}
		if c.Practice.FollowDays < 0 {
			return at, false, true
		}
		return s.now().Add(-time.Duration(c.Practice.FollowDays) * 24 * time.Hour), true, true
	}
	f, err := s.tw.Follower(ctx, c.ID)
	if err != nil {
		s.log.Warn("couldn't ask Twitch who follows", "err", err)
		return at, false, false
	}
	return f.FollowedAt, f.Follows, true
}

func (s *Service) accountMade(ctx context.Context, g *Giveaway, c chatter) (time.Time, bool) {
	if g.Practice {
		if c.Practice == nil {
			return time.Time{}, false
		}
		return s.now().Add(-time.Duration(c.Practice.AccountDays) * 24 * time.Hour), true
	}
	t, err := s.tw.AccountCreated(ctx, c.ID)
	if err != nil {
		s.log.Warn("couldn't ask Twitch how old an account is", "err", err)
		return time.Time{}, false
	}
	return t, true
}

// wonSince reports a win (won or claimed) in another giveaway since t; a
// practice giveaway counts only practice wins, and a real one real wins.
func (s *Service) wonSince(ctx context.Context, g *Giveaway, platform, userID string, t time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM giveaway_draws d
		JOIN giveaway_entries e ON e.giveaway_id = d.giveaway_id AND e.seq = d.entry_seq
		JOIN giveaways w ON w.id = d.giveaway_id
		WHERE d.outcome IN ('won', 'claimed') AND w.id != ? AND w.practice = ? AND e.platform = ? AND e.user_id = ? AND d.drawn_at >= ?`,
		g.ID, g.Practice, platform, userID, ts(t)).Scan(&n)
	return n > 0, err
}

// messagesBefore counts their chat messages this stream before the one
// that entered them: since the newest stream start in the last 48 hours,
// or the last 12 hours when StreamGremlin didn't see one.
func (s *Service) messagesBefore(ctx context.Context, g *Giveaway, cd candidate) (int, error) {
	now := s.now()
	from, ok, err := s.inbox.StreamStartSeq(ctx, now.Add(-streamLookback), g.Practice)
	if err != nil {
		return 0, err
	}
	if !ok {
		if from, err = s.inbox.SeqBefore(ctx, now.Add(-quietHours)); err != nil {
			return 0, err
		}
	}
	return s.inbox.CountChatFrom(ctx, cd.platform, cd.c.ID, from, cd.r.Seq, g.Practice)
}

// Weighted reports whether some entries may hold more than one chance.
func (g *Giveaway) Weighted() bool { return g.SubscriberChances > 1 }

// ChancesWords says how likely each entry is to win, for chat and the
// app: equal, or subscribers' chances in numbers. The odds are never
// hidden or called equal when they aren't.
func ChancesWords(g *Giveaway) string {
	if !g.Weighted() {
		return "every entry has the same chance"
	}
	return fmt.Sprintf("subscribers get %d chances to everyone else's 1", g.SubscriberChances)
}
