package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// Reading YouTube chat (M6 slice 2, D-097, ADR-0047): find the streamer's
// live broadcast, then keep its chat stream open, resuming from the last
// page token after a drop. Messages go to the event inbox as
// chat.message.received from source "youtube".

// ChatState says whether StreamGremlin is reading YouTube chat.
type ChatState string

const (
	ChatOff          ChatState = "off"          // your channel isn't signed in
	ChatLooking      ChatState = "looking"      // signed in; no live broadcast found yet
	ChatConnecting   ChatState = "connecting"   // opening the broadcast's chat
	ChatConnected    ChatState = "connected"    // messages arrive
	ChatDisconnected ChatState = "disconnected" // lost, or YouTube couldn't be asked; trying again
	ChatQuota        ChatState = "quota"        // today's quota is used up; waiting for the reset
)

// ChatStatus is YouTube chat's state for the app.
type ChatStatus struct {
	State  ChatState `json:"state"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`
	// Broadcast is the live broadcast being read, while there is one.
	Broadcast *Broadcast `json:"broadcast,omitempty"`
	// CheckedAt is when YouTube was last asked for a live broadcast, and
	// NextCheck when it will be next, while looking.
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	NextCheck *time.Time `json:"next_check,omitempty"`
}

// LookEvery is how often StreamGremlin asks YouTube whether you're live,
// while you're not (1 unit each: at most 720 a day). Going live on Twitch
// and Find my stream ask at once.
const LookEvery = 2 * time.Minute

const (
	resumeKey   = "youtube.chat"
	saveEvery   = 5 * time.Second
	maxBackoff  = time.Minute
	rateBackoff = 10 * time.Second
	// imageSize is the size of authors' pictures asked for (Google's default).
	imageSize = 88
)

// resume is where to start reading a chat again after a restart.
type resume struct {
	LiveChatID string `json:"live_chat_id"`
	PageToken  string `json:"page_token"`
}

// FindNow asks YouTube for a live broadcast now, instead of at the next
// check. It does nothing while chat is being read.
func (s *Service) FindNow() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// ChatStatus returns chat's state.
func (s *Service) ChatStatus() ChatStatus {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	c := s.chat
	if c.Broadcast != nil {
		b := *c.Broadcast
		c.Broadcast = &b
	}
	return c
}

func (s *Service) setChat(st ChatState, detail string, b *Broadcast, next *time.Time) {
	s.chatMu.Lock()
	same := st == s.chat.State && detail == s.chat.Detail && sameBroadcast(b, s.chat.Broadcast) &&
		((next == nil) == (s.chat.NextCheck == nil)) && (next == nil || next.Equal(*s.chat.NextCheck))
	if same {
		s.chatMu.Unlock()
		return
	}
	if st != s.chat.State {
		s.chat.Since = s.now()
		s.log.Info("youtube chat state", "state", string(st), "detail", detail)
	}
	s.chat.State, s.chat.Detail, s.chat.NextCheck = st, detail, next
	if b != nil {
		c := *b
		s.chat.Broadcast = &c
	} else {
		s.chat.Broadcast = nil
	}
	s.chatMu.Unlock()
	s.changed()
}

func sameBroadcast(a, b *Broadcast) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Title == b.Title
}

// streamerReady reports whether your channel can be read: signed in, or
// unknown (Google couldn't be asked; reading is tried anyway).
func (s *Service) streamerReady() (bool, string) {
	st := s.streamer.Status()
	if (st.State == StateConnected || st.State == StateUnknown) && st.Channel != nil {
		return true, st.Channel.ID
	}
	return false, ""
}

// waitChat waits for d (0: no timer), FindNow, a change in your channel's
// sign-in, or the end. It reports false at the end.
func (s *Service) waitChat(ctx context.Context, d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	ready, channel := s.streamerReady()
	state := s.streamer.Status().State
	for {
		changed := s.accounts.C()
		if r, c := s.streamerReady(); r != ready || c != channel || s.streamer.Status().State != state {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer:
			return true
		case <-s.kick:
			return true
		case <-changed:
		}
	}
}

// runChat reads YouTube chat while your channel is signed in.
func (s *Service) runChat(ctx context.Context) {
	for ctx.Err() == nil {
		if ready, _ := s.streamerReady(); !ready {
			detail := "Sign in to YouTube with your channel to read its chat."
			if st := s.streamer.Status(); st.State == StateNotConfigured {
				detail = "Add your Google project and sign in to read YouTube chat."
			}
			s.setChat(ChatOff, detail, nil, nil)
			if !s.waitChat(ctx, 0) {
				return
			}
			continue
		}
		if q := s.quota.status(ctx); q.Exhausted {
			s.setChat(ChatQuota, "Today's YouTube quota is used up. Chat is read again after it resets at midnight Pacific time.", nil, &q.ResetsAt)
			// A minute past the reset, so the new day has surely begun.
			if !s.waitChat(ctx, max(q.ResetsAt.Sub(s.now())+time.Minute, time.Minute)) {
				return
			}
			continue
		}
		b, err := s.findBroadcast(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrQuotaExhausted):
			continue
		case err != nil:
			next := s.now().Add(s.lookEvery)
			s.setChat(ChatDisconnected, "Couldn't ask YouTube whether you're live: "+describe(err), nil, &next)
			if !s.waitChat(ctx, s.lookEvery) {
				return
			}
		case b == nil:
			next := s.now().Add(s.lookEvery)
			s.setChat(ChatLooking, s.lastEnded, nil, &next)
			if !s.waitChat(ctx, s.lookEvery) {
				return
			}
		default:
			s.readChat(ctx, *b)
		}
	}
}

// findBroadcast asks YouTube for your live broadcast (1 unit). It skips
// broadcasts whose chat ended or is off.
func (s *Service) findBroadcast(ctx context.Context) (*Broadcast, error) {
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.data.activeBroadcasts(ctx, tok.Reveal())
	now := s.now()
	s.chatMu.Lock()
	s.chat.CheckedAt = &now
	s.chatMu.Unlock()
	if errors.Is(err, errAuth) {
		s.streamer.Validate(ctx)
	}
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if !s.endedChats[b.LiveChatID] {
			return &b, nil
		}
	}
	return nil, nil
}

func describe(err error) string {
	var ae *apiError
	switch {
	case errors.As(err, &ae) && ae.Reason == "liveStreamingNotEnabled":
		return "YouTube says live streaming isn't turned on for your channel."
	case errors.As(err, &ae) && ae.Reason == "insufficientLivePermissions":
		return "YouTube says your channel can't stream live."
	case errors.As(err, &ae):
		return ae.Message
	case errors.Is(err, errAuth):
		return "YouTube refused the sign-in; it's being checked."
	}
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "youtube: ")
	return msg
}

// outcome is how one chat stream ended.
type outcome int

const (
	outDropped outcome = iota // the connection ended; resume
	outFailed                 // an error; wait, then resume
	outRate                   // too soon after the last; wait longer
	outEnded                  // the chat ended or is off: look for a broadcast again
	outQuota                  // the quota's spent
	outAuth                   // the sign-in was refused; check it
	outInvalid                // the page token was refused: start without it
	outStopped                // StreamGremlin is stopping, or you signed out
)

// readChat reads a broadcast's chat until it ends, you sign out, or the
// quota runs out, reconnecting with the page token after a drop.
func (s *Service) readChat(ctx context.Context, b Broadcast) {
	ready, channel := s.streamerReady()
	if !ready {
		return
	}
	s.lastEnded = ""
	// Stop reading when your channel signs out or changes.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			changed := s.accounts.C()
			if r, c := s.streamerReady(); !r || c != channel {
				cancel()
				return
			}
			select {
			case <-sctx.Done():
				return
			case <-changed:
			}
		}
	}()

	r := s.loadResume(ctx)
	token := ""
	if r.LiveChatID == b.LiveChatID {
		token = r.PageToken
	}
	backoff := s.retryAfter
	for sctx.Err() == nil {
		if err := s.quota.spend(ctx, CostStreamList); err != nil {
			return // the quota: runChat says so
		}
		s.setChat(ChatConnecting, "", &b, nil)
		began := s.now()
		out, detail := s.streamOnce(sctx, b, channel, &token)
		lasted := s.now().Sub(began)
		var wait time.Duration
		switch out {
		case outStopped:
			return
		case outEnded:
			s.endedChats[b.LiveChatID] = true
			s.lastEnded = fmt.Sprintf("The chat of “%s” ended. Looking for your next live broadcast.", b.Title)
			if detail != "" {
				s.lastEnded = detail
			}
			s.saveResume(ctx, resume{})
			return
		case outQuota:
			s.quota.exhausted(ctx)
			return
		case outAuth:
			s.streamer.Validate(ctx)
			wait = backoff
		case outInvalid:
			token = ""
			wait = s.retryAfter
		case outRate:
			wait = max(backoff, rateBackoff)
		case outDropped:
			// A stream that ran a while is a normal end: resume after the
			// shortest wait.
			if lasted > 30*time.Second {
				backoff = s.retryAfter
			}
			wait = backoff
		case outFailed:
			wait = backoff
		}
		if out != outDropped || lasted <= 30*time.Second {
			backoff = min(backoff*2, maxBackoff)
		}
		if out != outDropped && out != outInvalid {
			s.setChat(ChatDisconnected, detail, &b, nil)
		}
		s.log.Info("youtube chat stream ended; resuming", "after", wait.String(), "detail", detail)
		select {
		case <-sctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// streamOnce opens one chat stream and stores what arrives until it ends.
func (s *Service) streamOnce(ctx context.Context, b Broadcast, channel string, token *string) (outcome, string) {
	at, err := s.streamer.AccessToken(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return outStopped, ""
		}
		return outFailed, "Couldn't refresh your YouTube sign-in: " + describe(err)
	}
	mctx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+at.Reveal())
	st, err := s.chatConn.NewStream(mctx, &grpc.StreamDesc{ServerStreams: true}, livechat.Method,
		grpc.ForceCodecV2(livechat.Codec{}))
	if err != nil {
		return s.classify(ctx, err, *token != "")
	}
	req := livechat.Request{LiveChatID: b.LiveChatID, PageToken: *token, ProfileImageSize: imageSize,
		Part: []string{"id", "snippet", "authorDetails"}}
	if err := st.SendMsg(&livechat.Raw{B: req.Marshal()}); err != nil && !errors.Is(err, io.EOF) {
		return s.classify(ctx, err, *token != "")
	}
	if err := st.CloseSend(); err != nil {
		return s.classify(ctx, err, *token != "")
	}
	// Without a page token, Google first sends recent history: messages
	// from before StreamGremlin was reading are marked as such.
	backlog := *token == ""
	lastSave := time.Time{}
	defer func() { s.saveResume(context.WithoutCancel(ctx), resume{LiveChatID: b.LiveChatID, PageToken: *token}) }()
	for {
		var raw livechat.Raw
		if err := st.RecvMsg(&raw); err != nil {
			return s.classify(ctx, err, *token != "")
		}
		resp, err := livechat.ParseResponse(raw.B)
		if err != nil {
			s.log.Warn("youtube chat: an answer that couldn't be read")
			return outFailed, "YouTube sent something StreamGremlin couldn't read."
		}
		s.setChat(ChatConnected, "", &b, nil)
		ended := s.storeChat(ctx, b, channel, resp.Items, backlog)
		backlog = false
		if resp.NextPageToken != "" {
			*token = resp.NextPageToken
			if now := s.now(); now.Sub(lastSave) >= saveEvery {
				s.saveResume(ctx, resume{LiveChatID: b.LiveChatID, PageToken: *token})
				lastSave = now
			}
		}
		if ended {
			return outEnded, ""
		}
		if resp.OfflineAt != "" {
			return outEnded, fmt.Sprintf("“%s” went offline. Looking for your next live broadcast.", b.Title)
		}
	}
}

// classify turns a stream's error into what to do next. Google sends
// FAILED_PRECONDITION for a chat that ended or is off, and can't say which.
func (s *Service) classify(ctx context.Context, err error, hadToken bool) (outcome, string) {
	if ctx.Err() != nil {
		return outStopped, ""
	}
	if errors.Is(err, io.EOF) {
		return outDropped, ""
	}
	st, _ := status.FromError(err)
	msg := st.Message()
	switch st.Code() {
	case codes.OK:
		return outDropped, ""
	case codes.Canceled:
		return outDropped, ""
	case codes.FailedPrecondition:
		return outEnded, "Your broadcast's chat ended, or is turned off. Looking for your next live broadcast."
	case codes.NotFound:
		return outEnded, "YouTube couldn't find your broadcast's chat. Looking for your next live broadcast."
	case codes.Unauthenticated:
		return outAuth, "YouTube refused the sign-in; it's being checked."
	case codes.PermissionDenied:
		return outFailed, "YouTube won't let your channel read this chat: " + msg
	case codes.ResourceExhausted:
		if strings.Contains(strings.ToLower(msg), "quota") {
			return outQuota, ""
		}
		return outRate, "YouTube asked StreamGremlin to slow down. Trying again shortly."
	case codes.InvalidArgument:
		if hadToken {
			return outInvalid, ""
		}
		return outFailed, "YouTube refused the request: " + msg
	}
	s.log.Info("youtube chat stream error", "code", st.Code().String(), "message", msg)
	return outFailed, "Lost YouTube chat; trying again."
}

// storeChat stores a response's messages. It reports whether the chat ended.
func (s *Service) storeChat(ctx context.Context, b Broadcast, channel string, items []livechat.Message, backlog bool) bool {
	ended := false
	botChannel := ""
	if bs := s.bot.Status(); bs.Channel != nil {
		botChannel = bs.Channel.ID
	}
	for _, m := range items {
		switch m.Snippet.Type {
		case livechat.TypeChatEnded:
			ended = true
			continue
		case livechat.TypeText:
		default:
			// Super Chats, memberships and the rest come in slice 4, deletions
			// and bans with moderating in slice 5.
			s.log.Debug("youtube chat: not stored yet", "type", m.Snippet.Type.String())
			continue
		}
		if s.inbox == nil || m.ID == "" {
			continue
		}
		env := chatEnvelope(m, b, channel, backlog)
		if botChannel != "" && m.Author.ChannelID == botChannel {
			// The bot's own message: shown, never viewer chat (D-087).
			var p map[string]any
			_ = json.Unmarshal(env.Payload, &p)
			p["sent_by"] = "bot"
			env.Payload, _ = json.Marshal(p)
			env.Type, env.SourceEventID = events.ChatSent, "sent:"+m.ID
		}
		if _, err := s.inbox.Accept(ctx, env); err != nil && !errors.Is(err, events.ErrConflict) {
			s.log.Warn("youtube chat: couldn't store a message", "err", err)
		}
	}
	return ended
}

// chatEnvelope turns a YouTube text message into chat.message.received,
// with the same chatter shape as Twitch's where YouTube has the same
// thing. YouTube has no logins: a channel ID and a display name.
func chatEnvelope(m livechat.Message, b Broadcast, channel string, backlog bool) events.Envelope {
	a := m.Author
	isB := a.IsChatOwner || (a.ChannelID != "" && a.ChannelID == channel)
	role := "viewer"
	switch {
	case isB:
		role = "broadcaster"
	case a.IsChatModerator:
		role = "moderator"
	case a.IsChatSponsor:
		role = "member"
	}
	payload := map[string]any{
		"message_id":   m.ID,
		"text":         m.Snippet.Text,
		"message_type": m.Snippet.Type.String(),
		"emotes":       []string{},
		"live_chat_id": b.LiveChatID,
		"broadcast_id": b.ID,
		"chatter": map[string]any{
			"id": a.ChannelID, "login": "", "display_name": a.DisplayName, "badges": []any{}, "role": role,
			"is_broadcaster": isB, "is_moderator": a.IsChatModerator, "is_vip": false, "is_subscriber": false,
			"is_member": a.IsChatSponsor, "is_verified": a.IsVerified,
			"channel_url": a.ChannelURL, "profile_image_url": a.ProfileImageURL,
		},
	}
	if backlog {
		// From before StreamGremlin was reading: slice 3 won't answer these.
		payload["backlog"] = true
	}
	occurred, err := time.Parse(time.RFC3339Nano, m.Snippet.PublishedAt)
	if err != nil {
		occurred = time.Now()
	}
	raw, _ := json.Marshal(payload)
	env := events.Envelope{
		EventID: events.NewID(), Type: "chat.message.received", Source: events.SourceYouTube, SourceEventID: m.ID,
		OccurredAt: occurred, ChannelID: channel,
		Actor:   &events.Actor{Platform: events.SourceYouTube, ID: a.ChannelID, DisplayName: a.DisplayName},
		Payload: raw, DeliveryQuality: events.Confirmed,
	}
	env.CorrelationID = env.EventID
	return env
}

func (s *Service) loadResume(ctx context.Context) resume {
	var r resume
	_, _ = s.settings.Load(ctx, resumeKey, &r)
	return r
}

func (s *Service) saveResume(ctx context.Context, r resume) {
	if err := s.settings.Save(ctx, resumeKey, r); err != nil {
		s.log.Warn("youtube chat: couldn't save where chat is up to", "err", err)
	}
}

// followTwitch asks YouTube at once when your Twitch stream goes live,
// since you're probably live on YouTube too.
func (s *Service) followTwitch(ctx context.Context) {
	if s.inbox == nil {
		return
	}
	cursor, err := s.inbox.Head(ctx)
	if err != nil {
		return
	}
	follow := events.Filter{"broadcast.stream.started", "broadcast.channel.online"}
	for ctx.Err() == nil {
		recs, err := s.inbox.Wait(ctx, cursor, 50, follow, 30*time.Second)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, r := range recs {
			cursor = r.Seq
			if r.Source == "twitch" && r.DeliveryQuality != events.Synthetic {
				s.FindNow()
			}
		}
	}
}
