package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sending YouTube chat (M6 slice 3, D-098): liveChatMessages.insert, 20
// units a message, to the chat being read.

// MaxPartRunes is how long one YouTube message is made. Google's reference
// gives no limit; YouTube's own chat box takes 200 characters, so longer
// messages are split at a space into up to MaxParts messages.
const (
	MaxPartRunes = 200
	MaxParts     = 3
	// MaxSendRunes is the longest message sent, as Twitch's limit.
	MaxSendRunes = 500
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
	Text string `json:"text"`
	As   Sender `json:"as,omitempty"`
	// Workflow is the workflow sending it, if any. Its echo is never
	// stored as you chatting, so no workflow answers it.
	Workflow string `json:"-"`
}

// SendResult says what YouTube did with it.
type SendResult struct {
	MessageIDs []string `json:"message_ids"`
	SentAs     Role     `json:"sent_as"`
	Channel    string   `json:"channel"`
	// Note explains a fallback or a split.
	Note string `json:"note,omitempty"`
}

var (
	// ErrNotLive: no live broadcast's chat is being read, so there's
	// nowhere to send.
	ErrNotLive = errors.New("youtube: not live on YouTube, so there's no chat to send to")
	// ErrNotSignedIn: your channel must be signed in to send to its chat.
	ErrNotSignedIn = errors.New("youtube: sign in with your YouTube channel to send chat")
	// ErrRateLimited: YouTube said too many messages were sent.
	ErrRateLimited = errors.New("youtube: YouTube says too many chat messages were sent just now")
)

// splitMessage cuts text into parts of at most max runes, at spaces where
// it can.
func splitMessage(text string, max int) []string {
	var parts []string
	for utf8.RuneCountInString(text) > max {
		r := []rune(text)
		cut := max
		for i := max; i > max/2; i-- {
			if unicode.IsSpace(r[i]) {
				cut = i
				break
			}
		}
		parts = append(parts, strings.TrimSpace(string(r[:cut])))
		text = strings.TrimSpace(string(r[cut:]))
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}

// SendChat posts a message to the chat of the live broadcast being read.
// It's never retried: YouTube can't recognise a repeat.
func (s *Service) SendChat(ctx context.Context, req SendRequest) (SendResult, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" || utf8.RuneCountInString(text) > MaxSendRunes {
		return SendResult{}, fmt.Errorf("%w: a chat message is 1 to %d characters", ErrInvalid, MaxSendRunes)
	}
	switch req.As {
	case SendAuto, SendBot, SendStreamer:
	default:
		return SendResult{}, fmt.Errorf("%w: send as bot or streamer", ErrInvalid)
	}
	if ready, _ := s.streamerReady(); !ready {
		return SendResult{}, ErrNotSignedIn
	}
	c := s.ChatStatus()
	if c.Broadcast == nil || c.Broadcast.LiveChatID == "" {
		return SendResult{}, ErrNotLive
	}
	parts := splitMessage(text, MaxPartRunes)
	res := SendResult{MessageIDs: []string{}}
	acct := s.streamer
	if req.As != SendStreamer {
		if bot := s.bot.Status(); bot.State == StateConnected {
			acct = s.bot
		} else if req.As == SendBot {
			res.Note = "No bot channel is signed in, so it was sent as you."
		}
	}
	for i, part := range parts {
		id, err := s.sendPart(ctx, acct, c.Broadcast.LiveChatID, part, req.Workflow)
		if i == 0 && acct == s.bot && errors.Is(err, errAuth) {
			// YouTube refused the bot's sign-in, so nothing was posted: send
			// as you instead, and check the bot's sign-in.
			go s.bot.Validate(s.ctx)
			res.Note = "The bot's sign-in didn't work, so it was sent as you."
			acct = s.streamer
			id, err = s.sendPart(ctx, acct, c.Broadcast.LiveChatID, part, req.Workflow)
		}
		if err != nil {
			if i > 0 {
				return res, fmt.Errorf("youtube: only %d of %d parts were sent: %w", i, len(parts), sendError(acct, err))
			}
			return SendResult{}, sendError(acct, err)
		}
		res.MessageIDs = append(res.MessageIDs, id)
	}
	if len(parts) > 1 {
		res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" Sent as %d messages: YouTube takes %d characters at a time.", len(parts), MaxPartRunes))
	}
	st := acct.Status()
	res.SentAs = st.Role
	if st.Channel != nil {
		res.Channel = st.Channel.Title
	}
	s.log.Info("youtube chat sent", "as", string(st.Role), "chars", utf8.RuneCountInString(text), "parts", len(parts))
	return res, nil
}

func sendError(acct *Account, err error) error {
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.Reason {
		case "rateLimitExceeded":
			return ErrRateLimited
		case "liveChatEnded", "liveChatDisabled", "liveChatNotFound":
			return fmt.Errorf("%w (%s)", ErrNotLive, ae.Reason)
		case "forbidden":
			name := string(acct.role)
			if st := acct.Status(); st.Channel != nil {
				name = st.Channel.Title
			}
			return fmt.Errorf("youtube: YouTube doesn't let %s post in this chat (banned or timed out?)", name)
		}
	}
	return err
}

// sendPart posts one message (20 units) and returns its ID. A workflow's
// message as you is expected, so its echo isn't stored as you chatting.
func (s *Service) sendPart(ctx context.Context, acct *Account, chatID, text, workflow string) (string, error) {
	tok, err := acct.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	own := workflow != "" && acct == s.streamer
	if own {
		s.own.Expect(text, s.now())
	}
	body := map[string]any{"snippet": map[string]any{
		"liveChatId": chatID, "type": "textMessageEvent",
		"textMessageDetails": map[string]any{"messageText": text},
	}}
	var out struct {
		ID string `json:"id"`
	}
	err = s.data.call(ctx, "POST", tok.Reveal(), "/liveChat/messages", url.Values{"part": {"snippet"}}, body, CostMessageInsert, &out)
	if errors.Is(err, errAuth) && acct == s.streamer {
		go s.streamer.Validate(s.ctx)
	}
	if own {
		s.own.Settle(text, out.ID, s.now())
	}
	return out.ID, err
}
