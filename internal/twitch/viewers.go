package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Facts about one viewer, for giveaway entry rules
// (docs/adr/0026-giveaway-entry-rules.md). Asked when they're needed and
// never cached, so a viewer who follows after being turned away can enter.

// FollowersScope reads who follows the channel.
const FollowersScope = "moderator:read:followers"

// Follow says whether a viewer follows the channel, and since when.
type Follow struct {
	Follows    bool      `json:"follows"`
	FollowedAt time.Time `json:"followed_at,omitempty"`
}

// Follower asks Twitch's Get Channel Followers whether userID follows the
// streamer's channel. An empty answer means they don't: Twitch documents
// it so for a token that has the scope, which is checked first.
func (s *Service) Follower(ctx context.Context, userID string) (Follow, error) {
	if !userIDPattern(userID) {
		return Follow{}, fmt.Errorf("%w: a Twitch user id is digits", ErrInvalid)
	}
	var out struct {
		Data []struct {
			UserID     string    `json:"user_id"`
			FollowedAt time.Time `json:"followed_at"`
		} `json:"data"`
	}
	err := s.streamerRaw(ctx, FollowersScope, "GET", "/channels/followers", url.Values{"user_id": {userID}}, nil, &out)
	if err != nil {
		return Follow{}, viewerError(err)
	}
	for _, d := range out.Data {
		if d.UserID == userID && !d.FollowedAt.IsZero() {
			return Follow{Follows: true, FollowedAt: d.FollowedAt}, nil
		}
	}
	if len(out.Data) > 0 {
		return Follow{}, errors.New("twitch: an unreadable follower answer")
	}
	return Follow{}, nil
}

// AccountCreated asks Twitch's Get Users when userID's account was made.
// It needs no scope.
func (s *Service) AccountCreated(ctx context.Context, userID string) (time.Time, error) {
	if !userIDPattern(userID) {
		return time.Time{}, fmt.Errorf("%w: a Twitch user id is digits", ErrInvalid)
	}
	var out struct {
		Data []struct {
			ID        string    `json:"id"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"data"`
	}
	// Get Users takes "id", not broadcaster_id; streamerRaw adds that too,
	// which Twitch ignores.
	err := s.streamerRaw(ctx, "", "GET", "/users", url.Values{"id": {userID}}, nil, &out)
	if err != nil {
		return time.Time{}, viewerError(err)
	}
	for _, d := range out.Data {
		if d.ID == userID && !d.CreatedAt.IsZero() {
			return d.CreatedAt, nil
		}
	}
	return time.Time{}, errors.New("twitch: no such account")
}

func viewerError(err error) error {
	if errors.Is(err, errStreamerSignIn) {
		return err
	}
	var he *helixError
	if errors.As(err, &he) && he.Status == 401 {
		return fmt.Errorf("%w: %s", ErrNeedsPermission, he.Message)
	}
	return fmt.Errorf("twitch: %w", err)
}

// ChattersScope reads who's connected to the channel's chat
// (docs/adr/0034-time-in-chat.md).
const ChattersScope = "moderator:read:chatters"

// MaxChatters is the most chatters read: 50 pages of 1000.
const MaxChatters = 50_000

// Chatters asks Twitch's Get Chatters who's connected to the streamer's
// chat, apart from the streamer and the bot. Twitch updates the list with
// a delay after people join and leave, so it's who was there a little
// while ago. Each viewer is listed once.
func (s *Service) Chatters(ctx context.Context) ([]events.Actor, error) {
	st := s.streamer.Status()
	skip := map[string]bool{st.UserID: true}
	if b := s.bot.Status(); b.UserID != "" {
		skip[b.UserID] = true
	}
	out := []events.Actor{}
	after := ""
	for {
		var page struct {
			Data []struct {
				UserID    string `json:"user_id"`
				UserLogin string `json:"user_login"`
				UserName  string `json:"user_name"`
			} `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		q := url.Values{"moderator_id": {st.UserID}, "first": {"1000"}}
		if after != "" {
			q.Set("after", after)
		}
		if err := s.streamerRaw(ctx, ChattersScope, "GET", "/chat/chatters", q, nil, &page); err != nil {
			return nil, viewerError(err)
		}
		for _, d := range page.Data {
			if d.UserID == "" || skip[d.UserID] {
				continue
			}
			skip[d.UserID] = true
			out = append(out, events.Actor{Platform: "twitch", ID: d.UserID, Login: d.UserLogin, DisplayName: d.UserName})
		}
		if page.Pagination.Cursor == "" || len(out) >= MaxChatters {
			return out, nil
		}
		after = page.Pagination.Cursor
	}
}
