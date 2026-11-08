package twitch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxChatRunes is Twitch's limit for one chat message.
const MaxChatRunes = 500

// Local send limits, under Twitch's: 20 per 30 seconds and one per second
// for an ordinary account, 100 per 30 seconds for the broadcaster or a
// moderator. Over Twitch's limit, Twitch ignores the account for an hour.
const (
	sendLimitUser = 18
	sendLimitMod  = 90
	maxSlotWait   = 3 * time.Second
)

// Sender chooses which account sends.
type Sender string

const (
	SendAuto     Sender = ""         // the bot when one is signed in, otherwise you
	SendBot      Sender = "bot"      // the bot; you when no bot is signed in
	SendStreamer Sender = "streamer" // always you
)

// SendRequest is one chat message to send.
type SendRequest struct {
	Text    string `json:"text"`
	As      Sender `json:"as,omitempty"`
	ReplyTo string `json:"reply_to,omitempty"`
	// Workflow is the workflow sending it, if any. Its echo is never
	// stored as you chatting, so no workflow answers it (never set by
	// the API: what you type in the app is you chatting).
	Workflow string `json:"-"`
}

// SendResult says what Twitch did with it.
type SendResult struct {
	MessageID string `json:"message_id"`
	SentAs    Role   `json:"sent_as"`
	Login     string `json:"login"`
	// Note explains a fallback, such as the bot being signed out.
	Note string `json:"note,omitempty"`
}

var (
	// ErrNotSignedIn: your account must be signed in to send to your chat.
	ErrNotSignedIn = errors.New("twitch: sign in with your Twitch account to send chat")
	// ErrRateLimited: sending now would pass Twitch's limit; nothing was sent.
	ErrRateLimited = errors.New("twitch: too many chat messages; nothing was sent")
)

// DropError means Twitch accepted the request but didn't post the message,
// for example because AutoMod held it.
type DropError struct{ Code, Message string }

func (e *DropError) Error() string {
	return fmt.Sprintf("twitch: Twitch didn't post the message: %s (%s)", e.Message, e.Code)
}

// SendChat posts a message to your channel's chat. It's never retried:
// Twitch has no way to recognise a repeat, so a retry after an unclear
// answer could post twice.
func (s *Service) SendChat(ctx context.Context, req SendRequest) (SendResult, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" || utf8.RuneCountInString(text) > MaxChatRunes {
		return SendResult{}, fmt.Errorf("%w: a chat message is 1 to %d characters", ErrInvalid, MaxChatRunes)
	}
	switch req.As {
	case SendAuto, SendBot, SendStreamer:
	default:
		return SendResult{}, fmt.Errorf("%w: send as bot or streamer", ErrInvalid)
	}
	streamer := s.streamer.Status()
	if streamer.State != StateConnected && (streamer.State != StateUnknown || streamer.UserID == "") {
		return SendResult{}, ErrNotSignedIn
	}
	res := SendResult{}
	acct := s.streamer
	if req.As != SendStreamer {
		if bot := s.bot.Status(); bot.State == StateConnected {
			acct = s.bot
		} else if req.As == SendBot {
			res.Note = "No bot account is signed in, so it was sent as you."
		}
	}
	if req.Workflow != "" {
		s.own.expect(text, s.now())
	}
	out, err := s.sendAs(ctx, acct, streamer.UserID, text, req.ReplyTo)
	if req.Workflow != "" {
		// Settled after any fallback below.
		defer func() { s.own.settle(text, out.MessageID, s.now()) }()
	}
	if acct == s.bot && isHelix(err, 401) {
		// Twitch refused the bot's sign-in, so nothing was posted: send as
		// you instead, and check the bot's sign-in.
		go s.bot.Validate(s.ctx)
		res.Note = "The bot's sign-in didn't work, so it was sent as you."
		acct = s.streamer
		out, err = s.sendAs(ctx, acct, streamer.UserID, text, req.ReplyTo)
	}
	if err != nil {
		return SendResult{}, err
	}
	st := acct.Status()
	res.MessageID, res.SentAs, res.Login = out.MessageID, st.Role, st.Login
	s.log.Info("twitch chat sent", "as", string(st.Role), "chars", utf8.RuneCountInString(text))
	return res, nil
}

func (s *Service) sendAs(ctx context.Context, acct *Account, broadcasterID, text, replyTo string) (sentMessage, error) {
	st := acct.Status()
	perWindow, oneASecond := sendLimitUser, true
	if acct == s.streamer {
		perWindow, oneASecond = sendLimitMod, false
	} else if c := s.ChatStatus(); c.BotModerator != nil && *c.BotModerator {
		perWindow, oneASecond = sendLimitMod, false
	}
	wait, ok := s.limits.reserve(st.UserID, s.now(), perWindow, oneASecond, maxSlotWait)
	if !ok {
		return sentMessage{}, ErrRateLimited
	}
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return sentMessage{}, ctx.Err()
		}
	}
	tok, err := acct.AccessToken(ctx)
	if err != nil {
		return sentMessage{}, err
	}
	clientID, _ := s.ClientID(ctx)
	out, err := s.helix.sendChat(ctx, clientID, tok.Reveal(), broadcasterID, st.UserID, text, replyTo)
	switch {
	case isHelix(err, 429):
		return out, ErrRateLimited
	case isHelix(err, 403):
		return out, fmt.Errorf("twitch: Twitch doesn't let %s send in this chat (banned or timed out?)", st.Login)
	case err != nil:
		return out, err
	case !out.IsSent:
		d := &DropError{Code: "unknown", Message: "no reason given"}
		if out.DropReason != nil {
			d.Code, d.Message = out.DropReason.Code, out.DropReason.Message
		}
		return out, d
	}
	return out, nil
}
