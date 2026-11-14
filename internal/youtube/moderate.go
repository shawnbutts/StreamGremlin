package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// Moderating YouTube chat (M6 slice 5, D-100): Google's documented
// liveChatMessages.delete, liveChatBans and liveChatModerators, always as
// your channel (the chat's owner, who may do all of them). None takes an
// idempotency key, so nothing here is retried: an answer that doesn't come
// leaves the outcome unknown, and the caller says so.

// Quota costs of moderating, from Google's table.
const (
	CostMessageDelete   = 20
	CostBan             = 200 // liveChatBans.insert and .delete alike
	CostModerator       = 200 // liveChatModerators.insert and .delete alike
	CostModeratorsList  = 50
	MaxTimeout          = 1_209_600 // two weeks, as Twitch: Google states no limit
	moderatorsFreshness = 10 * time.Minute
)

// Moderation actions YouTube has. They're named as Twitch's.
const (
	ModDelete  = "delete"
	ModTimeout = "timeout"
	ModBan     = "ban"
	ModUnban   = "unban"
	ModMod     = "mod"
	ModUnmod   = "unmod"
)

// ModActions are YouTube's actions, in the order screens list them.
var ModActions = []string{ModDelete, ModTimeout, ModBan, ModUnban, ModMod, ModUnmod}

// ModCost is what an action costs in quota units.
func ModCost(action string) int {
	switch action {
	case ModDelete:
		return CostMessageDelete
	case ModTimeout, ModBan, ModUnban:
		return CostBan
	case ModMod, ModUnmod:
		return CostModerator
	}
	return 0
}

// Mod is one moderation request to YouTube.
type Mod struct {
	Action    string
	ChannelID string // the viewer: everything but delete
	MessageID string // delete
	Seconds   int    // timeout: 1 to MaxTimeout
	// BanID is the ban to undo, as YouTube answered when it was made:
	// unban can't work without it (Google lists no bans).
	BanID string
	// ModeratorID is the moderator to remove, as YouTube answered when
	// they were added; when empty, it's looked up (50 units).
	ModeratorID string
}

// ModOutcome is how YouTube answered: done, refused (nothing happened,
// with why) or unknown (no answer: it may or may not have happened).
type ModOutcome struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// ID is what YouTube made: a ban's or a moderator's id, kept so it can
	// be undone.
	ID string `json:"id,omitempty"`
}

// Outcome states, the same words as Twitch's.
const (
	ModDone    = "done"
	ModRefused = "refused"
	ModUnknown = "unknown"
)

var (
	channelIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// YouTube's message, ban and moderator ids are base64-like, with dots.
	resourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._=%-]{1,512}$`)
)

// CheckMod reports what's wrong with a request before anything is sent.
func CheckMod(m Mod) error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }
	known := false
	for _, a := range ModActions {
		known = known || a == m.Action
	}
	switch {
	case !known:
		return bad("YouTube can't %q: it can delete, timeout, ban, unban, mod or unmod", m.Action)
	case m.Action == ModDelete && !resourceIDPattern.MatchString(m.MessageID):
		return bad("deleting needs the message's id")
	case m.Action != ModDelete && !channelIDPattern.MatchString(m.ChannelID):
		return bad("a YouTube channel id is letters, digits, - and _")
	case m.Action == ModTimeout && (m.Seconds < 1 || m.Seconds > MaxTimeout):
		return bad("a timeout is 1 second to 2 weeks (%d seconds)", MaxTimeout)
	case m.Action != ModTimeout && m.Seconds != 0:
		return bad("only a timeout has a length")
	case m.BanID != "" && (m.Action != ModUnban || !resourceIDPattern.MatchString(m.BanID)):
		return bad("only an unban takes a ban id")
	case m.Action == ModUnban && m.BanID == "":
		return bad("YouTube can only undo a ban StreamGremlin made: undo this one in YouTube Studio")
	case m.ModeratorID != "" && (m.Action != ModUnmod || !resourceIDPattern.MatchString(m.ModeratorID)):
		return bad("only removing a moderator takes a moderator id")
	}
	return nil
}

// Moderate sends one request to YouTube as your channel, to the chat being
// read, and says how it went. It's never retried.
func (s *Service) Moderate(ctx context.Context, m Mod) (ModOutcome, error) {
	if err := CheckMod(m); err != nil {
		return ModOutcome{}, err
	}
	if ready, _ := s.streamerReady(); !ready {
		return ModOutcome{State: ModRefused, Detail: "Sign in with your YouTube channel first."}, nil
	}
	chatID := ""
	if c := s.ChatStatus(); c.Broadcast != nil {
		chatID = c.Broadcast.LiveChatID
	}
	// Deleting and unbanning name what they act on; the rest act on the
	// live chat.
	if chatID == "" && m.Action != ModDelete && m.Action != ModUnban {
		return ModOutcome{State: ModRefused, Detail: "You're not live on YouTube, so there's no chat to moderate."}, nil
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		// Nothing was sent: the sign-in couldn't give a token.
		return ModOutcome{State: ModRefused, Detail: "Sign in with your YouTube channel again."}, nil //nolint:nilerr // a refusal, said in words
	}
	var out struct {
		ID string `json:"id"`
	}
	switch m.Action {
	case ModDelete:
		err = s.data.call(ctx, "DELETE", tok.Reveal(), "/liveChat/messages", url.Values{"id": {m.MessageID}}, nil, CostMessageDelete, nil)
	case ModTimeout, ModBan:
		sn := map[string]any{"liveChatId": chatID, "type": "permanent", "bannedUserDetails": map[string]any{"channelId": m.ChannelID}}
		if m.Action == ModTimeout {
			sn["type"], sn["banDurationSeconds"] = "temporary", m.Seconds
		}
		err = s.data.call(ctx, "POST", tok.Reveal(), "/liveChat/bans", url.Values{"part": {"snippet"}}, map[string]any{"snippet": sn}, CostBan, &out)
	case ModUnban:
		err = s.data.call(ctx, "DELETE", tok.Reveal(), "/liveChat/bans", url.Values{"id": {m.BanID}}, nil, CostBan, nil)
	case ModMod:
		sn := map[string]any{"liveChatId": chatID, "moderatorDetails": map[string]any{"channelId": m.ChannelID}}
		err = s.data.call(ctx, "POST", tok.Reveal(), "/liveChat/moderators", url.Values{"part": {"snippet"}}, map[string]any{"snippet": sn}, CostModerator, &out)
	case ModUnmod:
		id := m.ModeratorID
		if id == "" {
			list, lerr := s.Moderators(ctx)
			if lerr != nil {
				return modOutcome(lerr), nil
			}
			for _, x := range list {
				if x.ChannelID == m.ChannelID {
					id = x.ID
				}
			}
			if id == "" {
				return ModOutcome{State: ModRefused, Detail: "They aren't a moderator of this chat, as YouTube lists them."}, nil
			}
		}
		err = s.data.call(ctx, "DELETE", tok.Reveal(), "/liveChat/moderators", url.Values{"id": {id}}, nil, CostModerator, nil)
	}
	if errors.Is(err, errAuth) {
		go s.streamer.Validate(s.ctx)
	}
	o := modOutcome(err)
	if o.State == ModDone {
		o.ID = out.ID
		switch m.Action {
		case ModMod, ModUnmod:
			s.forgetModerators()
		case ModDelete:
			// Google may or may not send a tombstone for it: Live shows it
			// deleted either way.
			s.storeDeleted(context.WithoutCancel(ctx), m.MessageID, chatID)
		}
	}
	s.log.Info("youtube moderated", "action", m.Action, "channel_id", m.ChannelID, "state", o.State)
	return o, nil
}

// modOutcome puts YouTube's answer in words. Only a request YouTube
// answered (or one never sent) is known not to have happened.
func modOutcome(err error) ModOutcome {
	if err == nil {
		return ModOutcome{State: ModDone}
	}
	refused := func(d string) ModOutcome { return ModOutcome{State: ModRefused, Detail: d} }
	switch {
	case errors.Is(err, ErrQuotaExhausted):
		return refused("Today's YouTube quota is used up; it resets at midnight Pacific time.")
	case errors.Is(err, ErrNotLive):
		return refused("You're not live on YouTube, so there's no chat to moderate.")
	case errors.Is(err, ErrNotSignedIn):
		return refused("Sign in with your YouTube channel first.")
	case errors.Is(err, errAuth):
		return refused("YouTube refused your sign-in: sign in with your YouTube channel again.")
	}
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.Reason {
		case "liveChatBanInsertionNotAllowed":
			return refused("YouTube doesn't let you ban or time out the channel's owner or a moderator.")
		case "modificationNotAllowed":
			return refused("YouTube won't delete that message (a moderator's or yours).")
		case "liveChatMessageNotFound":
			return refused("YouTube says that message isn't there: it may already be deleted.")
		case "liveChatBanNotFound":
			return refused("YouTube says that ban isn't there: it may have ended or been undone in YouTube Studio.")
		case "liveChatNotFound", "liveChatEnded", "liveChatDisabled":
			return refused("That YouTube chat has ended.")
		case "liveChatUserNotFound":
			return refused("YouTube says that viewer isn't in this chat.")
		case "insufficientPermissions", "forbidden":
			return refused("YouTube says your channel isn't allowed to do that here: " + ae.Message)
		case "rateLimitExceeded":
			return refused("YouTube says too many requests were sent just now.")
		}
		return refused(fmt.Sprintf("YouTube refused: %s (%s)", ae.Message, ae.Reason))
	}
	return ModOutcome{State: ModUnknown, Detail: "YouTube didn't answer, so it may or may not have happened. Check the chat before trying again."}
}

// Moderator is one of the chat's moderators, as YouTube lists them.
type Moderator struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	DisplayName string `json:"display_name"`
}

type moderatorList struct {
	mu     sync.Mutex
	chatID string
	at     time.Time
	list   []Moderator
}

func (s *Service) forgetModerators() {
	s.mods.mu.Lock()
	s.mods.chatID = ""
	s.mods.mu.Unlock()
}

// Moderators lists the live chat's moderators: 50 units a page of 50,
// kept 10 minutes. Only the chat's owner may ask.
func (s *Service) Moderators(ctx context.Context) ([]Moderator, error) {
	c := s.ChatStatus()
	if c.Broadcast == nil || c.Broadcast.LiveChatID == "" {
		return nil, ErrNotLive
	}
	chatID := c.Broadcast.LiveChatID
	s.mods.mu.Lock()
	defer s.mods.mu.Unlock()
	if s.mods.chatID == chatID && s.now().Sub(s.mods.at) < moderatorsFreshness {
		return append([]Moderator(nil), s.mods.list...), nil
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return nil, ErrNotSignedIn
	}
	list := []Moderator{}
	page := ""
	for range 20 { // 1,000 moderators at most
		q := url.Values{"liveChatId": {chatID}, "part": {"snippet"}, "maxResults": {"50"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var out struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ID      string `json:"id"`
				Snippet struct {
					ModeratorDetails struct {
						ChannelID   string `json:"channelId"`
						DisplayName string `json:"displayName"`
					} `json:"moderatorDetails"`
				} `json:"snippet"`
			} `json:"items"`
		}
		if err := s.data.get(ctx, tok.Reveal(), "/liveChat/moderators", q, CostModeratorsList, &out); err != nil {
			return nil, err
		}
		for _, it := range out.Items {
			list = append(list, Moderator{ID: it.ID, ChannelID: it.Snippet.ModeratorDetails.ChannelID, DisplayName: it.Snippet.ModeratorDetails.DisplayName})
		}
		if page = out.NextPageToken; page == "" {
			break
		}
	}
	s.mods.chatID, s.mods.at, s.mods.list = chatID, s.now(), list
	return append([]Moderator(nil), list...), nil
}

// QuotaLeft is today's units left, as StreamGremlin counts them: 0 once
// Google has said the quota is spent.
func (s *Service) QuotaLeft(ctx context.Context) int {
	q := s.quota.status(ctx)
	if q.Exhausted {
		return 0
	}
	return max(q.Limit-q.Used, 0)
}
