package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Twitch's API and EventSub endpoints.
const (
	DefaultHelixURL    = "https://api.twitch.tv/helix"
	DefaultEventSubURL = "wss://eventsub.wss.twitch.tv/ws"
)

// helix calls the Twitch API with a user access token.
type helix struct {
	base string
	http *http.Client
}

func newHelix(base string) *helix {
	return &helix{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

// helixError is a 4xx answer from the API.
type helixError struct {
	Status  int
	Message string
}

func (e *helixError) Error() string { return fmt.Sprintf("twitch: %s (%d)", e.Message, e.Status) }

func (h *helix) call(ctx context.Context, method, path, clientID, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", errUnreachable, describeNetErr(err))
	}
	defer resp.Body.Close()
	noteRate(ctx, resp)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreachable, err)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: Twitch answered %s", errUnreachable, resp.Status)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return &helixError{Status: resp.StatusCode, Message: e.Message}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: an answer that isn't JSON", errUnreachable)
	}
	return nil
}

// subscribeChat creates the channel.chat.message subscription on a
// WebSocket session. 409 (already exists) counts as success.
func (h *helix) subscribeChat(ctx context.Context, clientID, token, sessionID, broadcasterID, userID string) error {
	return h.subscribe(ctx, clientID, token, sessionID, "channel.chat.message", "1",
		map[string]string{"broadcaster_user_id": broadcasterID, "user_id": userID})
}

// subscribe creates an EventSub subscription on a WebSocket session. 409
// (already exists) counts as success.
func (h *helix) subscribe(ctx context.Context, clientID, token, sessionID, typ, version string, cond map[string]string) error {
	err := h.call(ctx, "POST", "/eventsub/subscriptions", clientID, token, map[string]any{
		"type":      typ,
		"version":   version,
		"condition": cond,
		"transport": map[string]string{"method": "websocket", "session_id": sessionID},
	}, nil)
	if isHelix(err, http.StatusConflict) {
		return nil
	}
	return err
}

// channelInfo reads Get Channel Information: the title and category. It
// needs no scope.
func (h *helix) channelInfo(ctx context.Context, clientID, token, broadcasterID string) (title, category string, err error) {
	var out struct {
		Data []struct {
			Title    string `json:"title"`
			GameName string `json:"game_name"`
		} `json:"data"`
	}
	if err := h.call(ctx, "GET", "/channels?broadcaster_id="+url.QueryEscape(broadcasterID), clientID, token, nil, &out); err != nil {
		return "", "", err
	}
	if len(out.Data) == 0 {
		return "", "", fmt.Errorf("%w: no such channel", errUnreachable)
	}
	return out.Data[0].Title, out.Data[0].GameName, nil
}

// sentMessage is Send Chat Message's answer.
type sentMessage struct {
	MessageID  string `json:"message_id"`
	IsSent     bool   `json:"is_sent"`
	DropReason *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"drop_reason"`
}

func (h *helix) sendChat(ctx context.Context, clientID, token, broadcasterID, senderID, text, replyTo string) (sentMessage, error) {
	body := map[string]any{"broadcaster_id": broadcasterID, "sender_id": senderID, "message": text}
	if replyTo != "" {
		body["reply_parent_message_id"] = replyTo
	}
	var out struct {
		Data []sentMessage `json:"data"`
	}
	if err := h.call(ctx, "POST", "/chat/messages", clientID, token, body, &out); err != nil {
		return sentMessage{}, err
	}
	if len(out.Data) == 0 {
		return sentMessage{}, fmt.Errorf("%w: an empty answer to Send Chat Message", errUnreachable)
	}
	return out.Data[0], nil
}

// moderates reports whether userID moderates broadcasterID's channel.
func (h *helix) moderates(ctx context.Context, clientID, token, userID, broadcasterID string) (bool, error) {
	after := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"user_id": {userID}, "first": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data []struct {
				BroadcasterID string `json:"broadcaster_id"`
			} `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		if err := h.call(ctx, "GET", "/moderation/channels?"+q.Encode(), clientID, token, nil, &out); err != nil {
			return false, err
		}
		for _, d := range out.Data {
			if d.BroadcasterID == broadcasterID {
				return true, nil
			}
		}
		if out.Pagination.Cursor == "" {
			return false, nil
		}
		after = out.Pagination.Cursor
	}
	return false, nil
}
