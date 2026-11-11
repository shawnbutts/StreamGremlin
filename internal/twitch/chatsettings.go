package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// Chat modes (M7 slice 3; D-079): Get Chat Settings (no scope) and Update
// Chat Settings (moderator:manage:chat_settings), as the streamer.

// Scopes for chat modes and suspicious users.
const (
	ChatSettingsScope   = "moderator:manage:chat_settings"
	SuspiciousReadScope = "moderator:read:suspicious_users"
)

// Limits Twitch documents for chat modes.
const (
	MaxFollowerMinutes = 129_600 // 90 days
	MinSlowSeconds     = 3
	MaxSlowSeconds     = 120
)

// ChatSettings are the channel's chat modes. Nil numbers mean the mode is
// off. Unknown is set when the settings couldn't be read.
type ChatSettings struct {
	EmoteOnly       bool `json:"emote_mode"`
	FollowersOnly   bool `json:"follower_mode"`
	FollowerMinutes *int `json:"follower_mode_duration"`
	Slow            bool `json:"slow_mode"`
	SlowSeconds     *int `json:"slow_mode_wait_time"`
	SubscribersOnly bool `json:"subscriber_mode"`
	UniqueChat      bool `json:"unique_chat_mode"`
}

// ChatSettingsChange changes some modes; nil fields stay as they are.
type ChatSettingsChange struct {
	EmoteOnly       *bool `json:"emote_mode,omitempty"`
	FollowersOnly   *bool `json:"follower_mode,omitempty"`
	FollowerMinutes *int  `json:"follower_mode_duration,omitempty"`
	Slow            *bool `json:"slow_mode,omitempty"`
	SlowSeconds     *int  `json:"slow_mode_wait_time,omitempty"`
	SubscribersOnly *bool `json:"subscriber_mode,omitempty"`
	UniqueChat      *bool `json:"unique_chat_mode,omitempty"`
}

// CheckChatSettings reports what's wrong with a change before it's sent.
func CheckChatSettings(c ChatSettingsChange) error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }
	switch {
	case c == ChatSettingsChange{}:
		return bad("nothing to change")
	case c.FollowerMinutes != nil && (*c.FollowerMinutes < 0 || *c.FollowerMinutes > MaxFollowerMinutes):
		return bad("followers-only is for followers of 0 minutes to 90 days (%d minutes)", MaxFollowerMinutes)
	case c.FollowerMinutes != nil && (c.FollowersOnly == nil || !*c.FollowersOnly):
		return bad("a follow time needs followers-only on")
	case c.SlowSeconds != nil && (*c.SlowSeconds < MinSlowSeconds || *c.SlowSeconds > MaxSlowSeconds):
		return bad("slow mode waits %d to %d seconds", MinSlowSeconds, MaxSlowSeconds)
	case c.SlowSeconds != nil && (c.Slow == nil || !*c.Slow):
		return bad("a wait needs slow mode on")
	}
	return nil
}

// ChatSettings reads the channel's chat modes.
func (s *Service) ChatSettings(ctx context.Context) (ChatSettings, error) {
	var out struct {
		Data []ChatSettings `json:"data"`
	}
	if err := s.streamerRaw(ctx, "", "GET", "/chat/settings", nil, nil, &out); err != nil {
		return ChatSettings{}, viewerError(err)
	}
	if len(out.Data) != 1 {
		return ChatSettings{}, errors.New("twitch: an unreadable chat settings answer")
	}
	return out.Data[0], nil
}

// ChangeChatSettings changes the channel's chat modes, once (never
// retried), and returns them as Twitch now has them.
func (s *Service) ChangeChatSettings(ctx context.Context, c ChatSettingsChange) (ChatSettings, ModOutcome, error) {
	if err := CheckChatSettings(c); err != nil {
		return ChatSettings{}, ModOutcome{}, err
	}
	var out struct {
		Data []ChatSettings `json:"data"`
	}
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}}
	err := s.streamerRaw(ctx, ChatSettingsScope, "PATCH", "/chat/settings", q, c, &out)
	o := modOutcome(err)
	if o.State != ModDone {
		return ChatSettings{}, o, nil
	}
	if len(out.Data) != 1 {
		return ChatSettings{}, ModOutcome{State: ModUnknown, Detail: "Twitch's answer couldn't be read; check the chat modes."}, nil
	}
	return out.Data[0], o, nil
}
