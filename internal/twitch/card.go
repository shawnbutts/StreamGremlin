package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// A viewer card: what Twitch says about one viewer, for the Live screen
// (docs/milestones/M7.md, slice 2; D-078). Each fact is read with its own
// documented call and is either known or listed as unknown with why: a
// missing permission or Twitch not answering is never shown as "no".
// Nothing is cached here; the app asks again when the card opens.

// Scopes the card reads with, besides FollowersScope and SubscribersScope.
const (
	// ModerationReadScope reads the channel's moderators and banned users.
	ModerationReadScope = "moderation:read"
	// VIPsReadScope reads the channel's VIPs.
	VIPsReadScope = "channel:read:vips"
)

// Facts on a card, as named in Unknown.
const (
	FactAccount      = "account"
	FactFollow       = "follow"
	FactSubscription = "subscription"
	FactVIP          = "vip"
	FactModerator    = "moderator"
	FactBanned       = "banned"
)

// anonymousGifter is the gifter_id Twitch documents for anonymous gifts.
const anonymousGifter = "274598607"

// Card is what Twitch says about one viewer. A nil fact is unknown, and
// Unknown says why.
type Card struct {
	UserID string `json:"user_id"`
	// From Get Users (no scope).
	Login           string        `json:"login,omitempty"`
	DisplayName     string        `json:"display_name,omitempty"`
	Description     string        `json:"description,omitempty"`
	CreatedAt       *time.Time    `json:"created_at,omitempty"`
	BroadcasterType string        `json:"broadcaster_type,omitempty"` // affiliate, partner or ""
	StaffType       string        `json:"staff_type,omitempty"`       // admin, global_mod, staff or ""
	Picture         string        `json:"picture,omitempty"`
	Follow          *Follow       `json:"follow,omitempty"`
	Subscription    *Sub          `json:"subscription,omitempty"`
	VIP             *bool         `json:"vip,omitempty"`
	Moderator       *bool         `json:"moderator,omitempty"`
	Ban             *Ban          `json:"ban,omitempty"`
	Unknown         []UnknownFact `json:"unknown"`
	ReadAt          time.Time     `json:"read_at"`
}

// Sub is a viewer's subscription to the channel.
type Sub struct {
	Subscribed bool `json:"subscribed"`
	// Tier is 1, 2 or 3 (Prime shows as 1: Twitch sends it as 1000).
	Tier int  `json:"tier,omitempty"`
	Gift bool `json:"gift,omitempty"`
	// Gifter is who gave it, "" when it isn't a gift or was anonymous.
	Gifter    string `json:"gifter,omitempty"`
	Anonymous bool   `json:"anonymous,omitempty"`
}

// Ban is whether a viewer is banned or timed out in the channel.
type Ban struct {
	Banned bool `json:"banned"`
	// Until is when a timeout ends; nil for a ban that doesn't end.
	Until  *time.Time `json:"until,omitempty"`
	Since  *time.Time `json:"since,omitempty"`
	Reason string     `json:"reason,omitempty"`
	By     string     `json:"by,omitempty"`
}

// UnknownFact is a fact Twitch didn't give, and why, in words.
type UnknownFact struct {
	Fact string `json:"fact"`
	Why  string `json:"why"`
}

// unknownWhy puts a failed read in words for the card.
func unknownWhy(err error) string {
	switch {
	case errors.Is(err, errStreamerSignIn):
		return "Sign in with your Twitch account to see this."
	case errors.Is(err, ErrNeedsPermission):
		return "Sign in to Twitch again and allow StreamGremlin to see this."
	default:
		return "Twitch didn't answer."
	}
}

// ViewerCard asks Twitch for everything the card shows about userID, each
// fact at once and independently.
func (s *Service) ViewerCard(ctx context.Context, userID string) (Card, error) {
	if !userIDPattern(userID) {
		return Card{}, fmt.Errorf("%w: a Twitch user id is digits", ErrInvalid)
	}
	c := Card{UserID: userID, Unknown: []UnknownFact{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	read := func(fact string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f()
			if err == nil {
				return
			}
			mu.Lock()
			c.Unknown = append(c.Unknown, UnknownFact{Fact: fact, Why: unknownWhy(err)})
			mu.Unlock()
			if !errors.Is(err, errStreamerSignIn) && !errors.Is(err, ErrNeedsPermission) {
				s.log.Warn("couldn't read a viewer card fact", "fact", fact, "err", err)
			}
		}()
	}
	read(FactAccount, func() error {
		u, err := s.cardUser(ctx, userID)
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			c.Login, c.DisplayName, c.Description = u.Login, u.DisplayName, u.Description
			c.BroadcasterType, c.StaffType, c.Picture = u.BroadcasterType, u.Type, u.picture()
			if !u.CreatedAt.IsZero() {
				at := u.CreatedAt
				c.CreatedAt = &at
			}
		}
		return err
	})
	read(FactFollow, func() error {
		f, err := s.Follower(ctx, userID)
		if err == nil {
			mu.Lock()
			c.Follow = &f
			mu.Unlock()
		}
		return err
	})
	read(FactSubscription, func() error {
		sub, err := s.cardSub(ctx, userID)
		if err == nil {
			mu.Lock()
			c.Subscription = &sub
			mu.Unlock()
		}
		return err
	})
	read(FactVIP, func() error {
		v, err := s.listed(ctx, VIPsReadScope, "/channels/vips", userID)
		if err == nil {
			mu.Lock()
			c.VIP = &v
			mu.Unlock()
		}
		return err
	})
	read(FactModerator, func() error {
		v, err := s.listed(ctx, ModerationReadScope, "/moderation/moderators", userID)
		if err == nil {
			mu.Lock()
			c.Moderator = &v
			mu.Unlock()
		}
		return err
	})
	read(FactBanned, func() error {
		b, err := s.cardBan(ctx, userID)
		if err == nil {
			mu.Lock()
			c.Ban = &b
			mu.Unlock()
		}
		return err
	})
	wg.Wait()
	sortUnknown(c.Unknown)
	c.ReadAt = s.now()
	return c, nil
}

var factOrder = map[string]int{FactAccount: 0, FactFollow: 1, FactSubscription: 2, FactVIP: 3, FactModerator: 4, FactBanned: 5}

func sortUnknown(u []UnknownFact) {
	for i := 1; i < len(u); i++ {
		for j := i; j > 0 && factOrder[u[j].Fact] < factOrder[u[j-1].Fact]; j-- {
			u[j], u[j-1] = u[j-1], u[j]
		}
	}
}

type helixUser struct {
	ID              string    `json:"id"`
	Login           string    `json:"login"`
	DisplayName     string    `json:"display_name"`
	Type            string    `json:"type"`
	BroadcasterType string    `json:"broadcaster_type"`
	Description     string    `json:"description"`
	ProfileImageURL string    `json:"profile_image_url"`
	CreatedAt       time.Time `json:"created_at"`
}

// picture is the profile picture, only from Twitch's own image host
// (as for alert overlays).
func (u helixUser) picture() string {
	if GoodPicture(u.ProfileImageURL) {
		return u.ProfileImageURL
	}
	return ""
}

func (s *Service) cardUser(ctx context.Context, userID string) (helixUser, error) {
	var out struct {
		Data []helixUser `json:"data"`
	}
	if err := s.streamerRaw(ctx, "", "GET", "/users", url.Values{"id": {userID}}, nil, &out); err != nil {
		return helixUser{}, viewerError(err)
	}
	for _, d := range out.Data {
		if d.ID == userID {
			return d, nil
		}
	}
	// Twitch: "The list doesn't include deleted or suspended accounts."
	return helixUser{}, errors.New("twitch: no such account (deleted or suspended)")
}

// cardSub reads Get Broadcaster Subscriptions for one user_id. An empty
// list is taken as not subscribed: the reference doesn't say so for the
// filter, only for a channel with no subscribers (manifest).
func (s *Service) cardSub(ctx context.Context, userID string) (Sub, error) {
	var out struct {
		Data []struct {
			UserID     string `json:"user_id"`
			Tier       string `json:"tier"`
			IsGift     bool   `json:"is_gift"`
			GifterID   string `json:"gifter_id"`
			GifterName string `json:"gifter_name"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, SubscribersScope, "GET", "/subscriptions", url.Values{"user_id": {userID}}, nil, &out); err != nil {
		return Sub{}, viewerError(err)
	}
	for _, d := range out.Data {
		if d.UserID != userID {
			continue
		}
		sub := Sub{Subscribed: true, Gift: d.IsGift}
		switch d.Tier {
		case "1000":
			sub.Tier = 1
		case "2000":
			sub.Tier = 2
		case "3000":
			sub.Tier = 3
		}
		if d.IsGift {
			if d.GifterID == anonymousGifter || d.GifterID == "" {
				sub.Anonymous = true
			} else {
				sub.Gifter = d.GifterName
			}
		}
		return sub, nil
	}
	if len(out.Data) > 0 {
		return Sub{}, errors.New("twitch: an unreadable subscription answer")
	}
	return Sub{}, nil
}

// listed asks a Get VIPs or Get Moderators style endpoint whether userID is
// on the channel's list. Twitch documents that IDs not on the list are
// ignored, so an empty answer means no.
func (s *Service) listed(ctx context.Context, scope, path, userID string) (bool, error) {
	var out struct {
		Data []struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, scope, "GET", path, url.Values{"user_id": {userID}}, nil, &out); err != nil {
		return false, viewerError(err)
	}
	for _, d := range out.Data {
		if d.UserID == userID {
			return true, nil
		}
	}
	if len(out.Data) > 0 {
		return false, errors.New("twitch: an unreadable list answer")
	}
	return false, nil
}

// cardBan reads Get Banned Users for one user_id. expires_at is "" for a
// ban that doesn't end, and a time for a timeout.
func (s *Service) cardBan(ctx context.Context, userID string) (Ban, error) {
	var out struct {
		Data []struct {
			UserID        string `json:"user_id"`
			ExpiresAt     string `json:"expires_at"`
			CreatedAt     string `json:"created_at"`
			Reason        string `json:"reason"`
			ModeratorName string `json:"moderator_name"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, ModerationReadScope, "GET", "/moderation/banned", url.Values{"user_id": {userID}}, nil, &out); err != nil {
		return Ban{}, viewerError(err)
	}
	for _, d := range out.Data {
		if d.UserID != userID {
			continue
		}
		b := Ban{Banned: true, Reason: d.Reason, By: d.ModeratorName}
		if d.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, d.ExpiresAt)
			if err != nil {
				return Ban{}, errors.New("twitch: an unreadable timeout")
			}
			if !t.After(s.now()) {
				return Ban{}, nil // a timeout that has ended
			}
			b.Until = &t
		}
		if t, err := time.Parse(time.RFC3339, d.CreatedAt); err == nil {
			b.Since = &t
		}
		return b, nil
	}
	if len(out.Data) > 0 {
		return Ban{}, errors.New("twitch: an unreadable ban answer")
	}
	return Ban{}, nil
}
