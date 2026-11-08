package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// ChatState says whether StreamGremlin is reading the channel's chat.
type ChatState string

const (
	ChatOff          ChatState = "off"          // your account isn't signed in, or lacks user:read:chat
	ChatConnecting   ChatState = "connecting"   // opening the session
	ChatConnected    ChatState = "connected"    // subscribed: messages arrive
	ChatDisconnected ChatState = "disconnected" // lost; trying again
)

// ChatStatus is chat's state for the app.
type ChatStatus struct {
	State  ChatState `json:"state"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`
	// BotModerator: whether the bot moderates the channel, so it may send
	// faster. Nil while unknown.
	BotModerator *bool `json:"bot_moderator,omitempty"`
	// Feeds says which channel events Twitch sends, and why not.
	Feeds []FeedStatus `json:"feeds"`
}

const keepaliveSeconds = 30

// errRevoked ends a session because Twitch revoked its subscription.
type errRevoked struct{ status string }

func (e errRevoked) Error() string { return "Twitch revoked the chat subscription (" + e.status + ")" }

// wsMessage is one EventSub WebSocket message.
type wsMessage struct {
	Metadata struct {
		MessageID        string `json:"message_id"`
		MessageType      string `json:"message_type"`
		MessageTimestamp string `json:"message_timestamp"`
		SubscriptionType string `json:"subscription_type"`
	} `json:"metadata"`
	Payload struct {
		Session *struct {
			ID                      string `json:"id"`
			KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
			ReconnectURL            string `json:"reconnect_url"`
		} `json:"session"`
		Subscription *struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"subscription"`
		Event json.RawMessage `json:"event"`
	} `json:"payload"`
}

type frame struct {
	conn *websocket.Conn
	msg  wsMessage
	err  error
}

func readLoop(ctx context.Context, c *websocket.Conn, out chan<- frame) {
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			select {
			case out <- frame{conn: c, err: err}:
			case <-ctx.Done():
			}
			return
		}
		var m wsMessage
		if err := json.Unmarshal(data, &m); err != nil {
			continue // not ours to understand; Twitch only sends JSON
		}
		select {
		case out <- frame{conn: c, msg: m}:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) setChat(st ChatState, detail string) {
	s.chatMu.Lock()
	if st == s.chat.State && detail == s.chat.Detail {
		s.chatMu.Unlock()
		return // no news: waking listeners would wake runChat itself
	}
	if st != s.chat.State {
		s.chat.Since = s.now()
		s.log.Info("twitch chat state", "state", string(st), "detail", detail)
	}
	s.chat.State, s.chat.Detail = st, detail
	if st == ChatOff {
		s.chat.Feeds = nil // nothing is asked for while chat is off
	}
	s.chatMu.Unlock()
	s.changed()
}

// ChatStatus returns chat's state.
func (s *Service) ChatStatus() ChatStatus {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	c := s.chat
	c.Feeds = slices.Clone(c.Feeds)
	if c.Feeds == nil {
		c.Feeds = []FeedStatus{}
	}
	if c.BotModerator != nil {
		v := *c.BotModerator
		c.BotModerator = &v
	}
	return c
}

func (s *Service) setBotModerator(v *bool) {
	s.chatMu.Lock()
	s.chat.BotModerator = v
	s.chatMu.Unlock()
}

// runChat keeps a chat session open while your account is signed in, and
// reconnects with backoff when it drops. Twitch doesn't replay messages sent
// while disconnected; the state says so.
func (s *Service) runChat() {
	defer s.wg.Done()
	backoff := time.Second
	for s.ctx.Err() == nil {
		changes := s.Changes()
		st := s.streamer.Status()
		ready := st.State == StateConnected || (st.State == StateUnknown && st.UserID != "")
		switch {
		case !ready:
			s.setChat(ChatOff, "Sign in with your Twitch account to read chat.")
		case !slices.Contains(st.Scopes, "user:read:chat"):
			s.setChat(ChatOff, "Sign in again and allow reading chat.")
			ready = false
		}
		if !ready {
			select {
			case <-changes:
			case <-s.ctx.Done():
			}
			continue
		}

		// The session lasts until it fails, or your account changes.
		ctx, cancel := context.WithCancel(s.ctx)
		go func(userID string) {
			for {
				select {
				case <-ctx.Done():
					return
				case <-s.Changes():
				}
				if now := s.streamer.Status(); now.UserID != userID ||
					(now.State != StateConnected && now.State != StateUnknown) {
					cancel()
					return
				}
			}
		}(st.UserID)
		start := s.now()
		err := s.chatSession(ctx, st.UserID)
		cancel()
		if s.ctx.Err() != nil {
			return
		}
		var rev errRevoked
		switch {
		case errors.As(err, &rev):
			s.streamer.Validate(s.ctx) // ends the sign-in if Twitch revoked it
		case isHelix(err, 401):
			s.streamer.Validate(s.ctx) // refreshes once, or ends the sign-in
		}
		if after := s.streamer.Status(); after.UserID != st.UserID || after.State == StateNeedsSignIn {
			backoff = time.Second
			continue // the account changed: start again at once
		}
		if s.now().Sub(start) > time.Minute {
			backoff = time.Second
		}
		detail := "Lost the chat connection"
		if err != nil {
			detail += ": " + err.Error()
		}
		s.setChat(ChatDisconnected, detail+". Trying again. Twitch doesn't resend messages missed while disconnected.")
		wait := backoff/2 + rand.N(backoff/2+1) //nolint:gosec // jitter for reconnects, not a secret or a draw
		backoff = min(backoff*2, time.Minute)
		select {
		case <-time.After(wait):
		case <-s.ctx.Done():
			return
		}
	}
}

func isHelix(err error, status int) bool {
	var he *helixError
	return errors.As(err, &he) && he.Status == status
}

// chatSession opens one EventSub session, subscribes to the channel's chat,
// and stores every message until the session ends.
func (s *Service) chatSession(ctx context.Context, userID string) error {
	s.setChat(ChatConnecting, "")
	u, err := url.Parse(s.eventSubURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("keepalive_timeout_seconds", fmt.Sprint(keepaliveSeconds))
	u.RawQuery = q.Encode()

	frames := make(chan frame, 64)
	cur, err := s.dial(ctx, u.String(), frames)
	if err != nil {
		return err
	}
	defer func() { _ = cur.CloseNow() }()

	// The welcome comes first; the subscription must follow within 10 seconds.
	welcome, err := waitWelcome(ctx, frames, cur, 10*time.Second)
	if err != nil {
		return err
	}
	keepalive := time.Duration(welcome.KeepaliveTimeoutSeconds) * time.Second
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return err
	}
	clientID, _ := s.ClientID(ctx)
	sctx, scancel := context.WithTimeout(ctx, 8*time.Second)
	err = s.helix.subscribeChat(sctx, clientID, tok.Reveal(), welcome.ID, userID, userID)
	scancel()
	if err != nil {
		return fmt.Errorf("couldn't subscribe to chat: %w", err)
	}
	s.subscribeFeeds(ctx, clientID, tok.Reveal(), welcome.ID, userID, s.streamer.Status().Scopes)
	s.loadChannel(ctx, clientID, tok.Reveal(), userID)
	s.setChat(ChatConnected, "")
	go s.watchBot(ctx, userID)

	var next *websocket.Conn
	timer := time.NewTimer(keepaliveWindow(keepalive))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = cur.Close(websocket.StatusNormalClosure, "")
			return ctx.Err()
		case <-timer.C:
			return errors.New("no keepalive from Twitch: the connection went quiet")
		case f := <-frames:
			if f.conn != cur && f.conn != next {
				continue // an old connection's last words
			}
			if f.err != nil {
				if f.conn == next {
					next = nil
					continue // the old one may still be fine
				}
				if next != nil {
					continue // handing over: wait for the new welcome
				}
				return fmt.Errorf("the connection closed: %s", closeReason(f.err))
			}
			if f.conn == next {
				if f.msg.Metadata.MessageType == "session_welcome" {
					_ = cur.Close(websocket.StatusNormalClosure, "reconnected")
					cur, next = next, nil
					timer.Reset(keepaliveWindow(keepalive))
					continue
				}
			} else {
				timer.Reset(keepaliveWindow(keepalive))
			}
			switch f.msg.Metadata.MessageType {
			case "notification":
				if f.msg.Metadata.SubscriptionType == "channel.chat.message" {
					s.storeChat(ctx, f.msg)
				} else {
					s.storeChannelEvent(ctx, f.msg)
				}
			case "session_reconnect":
				if p := f.msg.Payload.Session; p != nil && p.ReconnectURL != "" && next == nil {
					// Use Twitch's URL as given; subscriptions carry over.
					if c, err := s.dial(ctx, p.ReconnectURL, frames); err == nil {
						next = c
						defer func() { _ = c.CloseNow() }()
					}
				}
			case "revocation":
				status, typ := "unknown", ""
				if f.msg.Payload.Subscription != nil {
					status, typ = f.msg.Payload.Subscription.Status, f.msg.Payload.Subscription.Type
				}
				if typ != "" && typ != "channel.chat.message" {
					// Only that kind of event stops; chat goes on.
					s.setFeed(typ, "Twitch stopped sending it ("+status+"). Sign in again to restart it.")
					continue
				}
				return errRevoked{status: status}
			}
		}
	}
}

func keepaliveWindow(k time.Duration) time.Duration {
	if k <= 0 {
		k = keepaliveSeconds * time.Second
	}
	return k + k/5 + time.Second
}

func (s *Service) dial(ctx context.Context, u string, frames chan<- frame) (*websocket.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(dctx, u, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errUnreachable, describeNetErr(err))
	}
	c.SetReadLimit(1 << 20)
	go readLoop(ctx, c, frames)
	return c, nil
}

type welcomeSession struct {
	ID                      string
	KeepaliveTimeoutSeconds int
}

func waitWelcome(ctx context.Context, frames <-chan frame, c *websocket.Conn, within time.Duration) (welcomeSession, error) {
	t := time.NewTimer(within)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return welcomeSession{}, ctx.Err()
		case <-t.C:
			return welcomeSession{}, errors.New("no welcome from Twitch on the chat connection")
		case f := <-frames:
			if f.conn != c {
				continue
			}
			if f.err != nil {
				return welcomeSession{}, fmt.Errorf("the connection closed: %s", closeReason(f.err))
			}
			if f.msg.Metadata.MessageType == "session_welcome" && f.msg.Payload.Session != nil {
				p := f.msg.Payload.Session
				return welcomeSession{ID: p.ID, KeepaliveTimeoutSeconds: p.KeepaliveTimeoutSeconds}, nil
			}
		}
	}
}

// closeReason names Twitch's documented close codes.
func closeReason(err error) string {
	switch websocket.CloseStatus(err) {
	case 4000:
		return "Twitch had an internal error (4000)"
	case 4001:
		return "Twitch closed it after an unexpected message (4001)"
	case 4002:
		return "missed a ping (4002)"
	case 4003:
		return "no subscription in time (4003)"
	case 4004:
		return "the reconnect took too long (4004)"
	case 4005:
		return "network timeout (4005)"
	case 4006:
		return "network error (4006)"
	case 4007:
		return "invalid reconnect URL (4007)"
	case -1:
		return describeNetErr(err)
	}
	return err.Error()
}

// storeChat stores a chat message (and its command) in the inbox. The bot's
// own messages aren't viewer chat: they're read for its badges, then dropped,
// so StreamGremlin never answers itself. Neither are workflows' messages sent
// as you (ownSends).
func (s *Service) storeChat(ctx context.Context, m wsMessage) {
	var ev chatEvent
	if err := json.Unmarshal(m.Payload.Event, &ev); err != nil || ev.MessageID == "" {
		s.log.Warn("twitch chat: an event that couldn't be read")
		return
	}
	if bot := s.bot.Status(); bot.UserID != "" && ev.ChatterUserID == bot.UserID {
		if ev.SourceBroadcasterUserID == "" || ev.SourceBroadcasterUserID == ev.BroadcasterUserID {
			mod := hasBadge(ev.Badges, "moderator") || hasBadge(ev.Badges, "vip")
			s.setBotModerator(&mod)
		}
		return
	}
	if st := s.streamer.Status(); st.UserID != "" && ev.ChatterUserID == st.UserID && s.own.claim(ev.MessageID, ev.Message.Text, s.now()) {
		s.log.Debug("twitch chat: a workflow's own message, not stored")
		return
	}
	if s.inbox == nil {
		return
	}
	occurred, err := time.Parse(time.RFC3339Nano, m.Metadata.MessageTimestamp)
	if err != nil {
		occurred = s.now()
	}
	msg, cmd := chatEnvelopes(ev, occurred)
	res, err := s.inbox.Accept(ctx, msg)
	if err != nil {
		s.log.Warn("twitch chat: couldn't store a message", "err", err)
		return
	}
	if cmd != nil {
		cmd.CausationID, cmd.CorrelationID = res.Record.EventID, res.Record.EventID
		if _, err := s.inbox.Accept(ctx, *cmd); err != nil && !errors.Is(err, events.ErrConflict) {
			s.log.Warn("twitch chat: couldn't store a command", "err", err)
		}
	}
}

// watchBot checks whether the bot moderates the channel while chat is
// open: now, whenever the bot signs in or out, and hourly.
func (s *Service) watchBot(ctx context.Context, broadcasterID string) {
	last := "unchecked"
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		changes := s.Changes()
		if bot := s.bot.Status(); bot.UserID+string(bot.State) != last {
			last = bot.UserID + string(bot.State)
			s.checkBotModerator(ctx, broadcasterID)
		}
		select {
		case <-ctx.Done():
			return
		case <-changes:
		case <-hourly.C:
			last = "unchecked"
		}
	}
}

// checkBotModerator asks Twitch whether the bot moderates the channel, so
// it may send at the higher limit.
func (s *Service) checkBotModerator(ctx context.Context, broadcasterID string) {
	bot := s.bot.Status()
	if bot.State != StateConnected || !slices.Contains(bot.Scopes, "user:read:moderated_channels") {
		s.setBotModerator(nil)
		return
	}
	tok, err := s.bot.AccessToken(ctx)
	if err != nil {
		return
	}
	clientID, _ := s.ClientID(ctx)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mod, err := s.helix.moderates(cctx, clientID, tok.Reveal(), bot.UserID, broadcasterID)
	if err != nil {
		s.log.Info("twitch: couldn't check whether the bot is a moderator", "err", err)
		return
	}
	s.setBotModerator(&mod)
}

// limiter allows at most n sends per window per sender, plus at most one
// per second unless burst is allowed.
type limiter struct {
	mu   sync.Mutex
	sent map[string][]time.Time
}

func (l *limiter) reserve(sender string, now time.Time, perWindow int, oneASecond bool, maxWait time.Duration) (wait time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sent == nil {
		l.sent = map[string][]time.Time{}
	}
	keep := l.sent[sender][:0]
	for _, t := range l.sent[sender] {
		if now.Sub(t) < 30*time.Second {
			keep = append(keep, t)
		}
	}
	l.sent[sender] = keep
	if len(keep) >= perWindow {
		return 0, false
	}
	if oneASecond && len(keep) > 0 {
		if gap := time.Second - now.Sub(keep[len(keep)-1]); gap > 0 {
			wait = gap
		}
	}
	if wait > maxWait {
		return 0, false
	}
	l.sent[sender] = append(keep, now.Add(wait))
	return wait, true
}
