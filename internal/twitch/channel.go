package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Channel events (docs/adr/0015-twitch-channel-events.md): follows, subs,
// gifts, cheers, raids, channel point redemptions, going live and offline,
// and channel updates, each read from EventSub on the chat session.

// Feed is one kind of channel event StreamGremlin asks Twitch for.
type Feed struct {
	Type    string   `json:"type"` // the EventSub subscription type
	Label   string   `json:"label"`
	Version string   `json:"-"`
	Scopes  []string `json:"-"` // any one of them; none: no authorization needed
}

// Feeds are the channel events, in the order they're asked for.
var Feeds = []Feed{
	{Type: "channel.follow", Label: "Follows", Version: "2", Scopes: []string{"moderator:read:followers"}},
	{Type: "channel.subscribe", Label: "New subs", Version: "1", Scopes: []string{"channel:read:subscriptions"}},
	{Type: "channel.subscription.message", Label: "Resubs", Version: "1", Scopes: []string{"channel:read:subscriptions"}},
	{Type: "channel.subscription.gift", Label: "Gifted subs", Version: "1", Scopes: []string{"channel:read:subscriptions"}},
	{Type: "channel.cheer", Label: "Cheers (bits)", Version: "1", Scopes: []string{"bits:read"}},
	{Type: "channel.raid", Label: "Raids", Version: "1"},
	{Type: "channel.channel_points_custom_reward_redemption.add", Label: "Channel point redemptions", Version: "1",
		Scopes: []string{"channel:read:redemptions", "channel:manage:redemptions"}},
	{Type: "stream.online", Label: "Going live", Version: "1"},
	{Type: "stream.offline", Label: "Going offline", Version: "1"},
	{Type: "channel.update", Label: "Title and category changes", Version: "2"},
}

// FeedStatus says whether Twitch sends one kind of event.
type FeedStatus struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	// Problem says why not, in words: a permission to allow by signing in
	// again, or what Twitch said.
	Problem string `json:"problem,omitempty"`
}

// ChannelInfo is the channel's title and category, as Twitch last said.
type ChannelInfo struct {
	Title    string    `json:"title"`
	Category string    `json:"category"`
	Known    bool      `json:"known"`
	At       time.Time `json:"at,omitzero"`
}

// Channel returns the channel's title and category; Known is false until
// Twitch has said.
func (s *Service) Channel() ChannelInfo {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.channel
}

func (s *Service) setChannel(title, category string) {
	s.chatMu.Lock()
	s.channel = ChannelInfo{Title: title, Category: category, Known: true, At: s.now()}
	s.chatMu.Unlock()
}

func (s *Service) setFeeds(f []FeedStatus) {
	s.chatMu.Lock()
	s.chat.Feeds = f
	s.chatMu.Unlock()
}

func (s *Service) setFeed(typ, problem string) {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	for i := range s.chat.Feeds {
		if s.chat.Feeds[i].Type == typ {
			s.chat.Feeds[i].OK, s.chat.Feeds[i].Problem = problem == "", problem
		}
	}
}

// subscribeFeeds asks Twitch for every channel event the sign-in allows.
// A refusal is listed, not fatal: chat keeps working.
func (s *Service) subscribeFeeds(ctx context.Context, clientID, token, sessionID, userID string, granted []string) {
	out := make([]FeedStatus, 0, len(Feeds))
	for _, f := range Feeds {
		st := FeedStatus{Type: f.Type, Label: f.Label}
		allowed := len(f.Scopes) == 0
		for _, sc := range f.Scopes {
			allowed = allowed || slices.Contains(granted, sc)
		}
		if !allowed {
			st.Problem = "Sign in to Twitch again and allow it (" + f.Scopes[0] + ")."
			out = append(out, st)
			continue
		}
		cond := map[string]string{"broadcaster_user_id": userID}
		switch f.Type {
		case "channel.follow":
			cond["moderator_user_id"] = userID
		case "channel.raid":
			cond = map[string]string{"to_broadcaster_user_id": userID}
		}
		sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := s.helix.subscribe(sctx, clientID, token, sessionID, f.Type, f.Version, cond)
		cancel()
		switch {
		case err == nil:
			st.OK = true
		case isHelix(err, http.StatusForbidden):
			st.Problem = "Twitch refused it: " + helixMessage(err) + ". Channel points, subs and bits need an affiliate or partner channel."
		default:
			st.Problem = "Couldn't ask Twitch for it: " + err.Error()
		}
		if !st.OK {
			s.log.Warn("twitch: a channel event isn't available", "type", f.Type, "problem", st.Problem)
		}
		out = append(out, st)
	}
	s.setFeeds(out)
}

func helixMessage(err error) string {
	var he *helixError
	if errors.As(err, &he) {
		return he.Message
	}
	return err.Error()
}

// loadChannel reads the channel's title and category once per session.
func (s *Service) loadChannel(ctx context.Context, clientID, token, userID string) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	title, category, err := s.helix.channelInfo(cctx, clientID, token, userID)
	if err != nil {
		s.log.Warn("twitch: couldn't read the channel's title and category", "err", err)
		return
	}
	s.setChannel(title, category)
}

// eventUser is the viewer fields EventSub events share. Anonymous gifts and
// cheers have none.
type eventUser struct {
	UserID    *string `json:"user_id"`
	UserLogin *string `json:"user_login"`
	UserName  *string `json:"user_name"`
}

func (u eventUser) actor() *events.Actor {
	if u.UserID == nil || *u.UserID == "" {
		return nil
	}
	a := &events.Actor{Platform: "twitch", ID: *u.UserID}
	if u.UserLogin != nil {
		a.Login = *u.UserLogin
	}
	if u.UserName != nil {
		a.DisplayName = *u.UserName
	}
	return a
}

// fromName is who gave a gift or cheer, for messages: "Anonymous" when
// they chose not to say.
func fromName(a *events.Actor) string {
	if a == nil {
		return "Anonymous"
	}
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Login
}

// tierNumber turns Twitch's "1000", "2000", "3000" into 1, 2, 3. Prime subs
// arrive as tier 1000.
func tierNumber(t string) int {
	switch t {
	case "2000":
		return 2
	case "3000":
		return 3
	}
	return 1
}

// channelEnvelope turns one channel event into an inbox event. ok is false
// for types it doesn't know or events it can't read.
func channelEnvelope(typ, messageID string, raw json.RawMessage, occurred time.Time) (env events.Envelope, ok bool) {
	var base struct {
		eventUser
		BroadcasterUserID string `json:"broadcaster_user_id"`
	}
	if json.Unmarshal(raw, &base) != nil {
		return events.Envelope{}, false
	}
	env = events.Envelope{Source: "twitch", SourceEventID: messageID, OccurredAt: occurred, ChannelID: base.BroadcasterUserID,
		Actor: base.actor(), DeliveryQuality: events.Confirmed}
	var payload map[string]any
	switch typ {
	case "channel.follow":
		var e struct {
			FollowedAt string `json:"followed_at"`
		}
		_ = json.Unmarshal(raw, &e)
		env.Type, payload = "audience.follow.created", map[string]any{"followed_at": e.FollowedAt}
	case "channel.subscribe":
		var e struct {
			Tier   string `json:"tier"`
			IsGift bool   `json:"is_gift"`
		}
		_ = json.Unmarshal(raw, &e)
		env.Type, payload = "monetization.subscription.started", map[string]any{"tier": tierNumber(e.Tier), "is_gift": e.IsGift}
	case "channel.subscription.message":
		var e struct {
			Tier    string `json:"tier"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Cumulative int  `json:"cumulative_months"`
			Streak     *int `json:"streak_months"`
			Duration   int  `json:"duration_months"`
		}
		_ = json.Unmarshal(raw, &e)
		payload = map[string]any{"tier": tierNumber(e.Tier), "text": e.Message.Text, "months": e.Cumulative, "duration_months": e.Duration}
		if e.Streak != nil {
			payload["streak_months"] = *e.Streak
		}
		env.Type = "monetization.subscription.renewed"
	case "channel.subscription.gift":
		var e struct {
			Tier       string `json:"tier"`
			Total      int    `json:"total"`
			Cumulative *int   `json:"cumulative_total"`
			Anonymous  bool   `json:"is_anonymous"`
		}
		_ = json.Unmarshal(raw, &e)
		payload = map[string]any{"tier": tierNumber(e.Tier), "total": e.Total, "is_anonymous": e.Anonymous}
		if e.Cumulative != nil {
			payload["cumulative_total"] = *e.Cumulative
		}
		if e.Anonymous {
			env.Actor = nil
		}
		payload["from"] = fromName(env.Actor)
		env.Type = "monetization.subscription.gifted"
	case "channel.cheer":
		var e struct {
			Bits      int    `json:"bits"`
			Message   string `json:"message"`
			Anonymous bool   `json:"is_anonymous"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Anonymous {
			env.Actor = nil
		}
		env.Type, payload = "monetization.bits.cheered", map[string]any{"bits": e.Bits, "text": e.Message, "is_anonymous": e.Anonymous,
			"from": fromName(env.Actor)}
	case "channel.raid":
		var e struct {
			FromID    string `json:"from_broadcaster_user_id"`
			FromLogin string `json:"from_broadcaster_user_login"`
			FromName  string `json:"from_broadcaster_user_name"`
			ToID      string `json:"to_broadcaster_user_id"`
			Viewers   int    `json:"viewers"`
		}
		_ = json.Unmarshal(raw, &e)
		env.ChannelID = e.ToID
		env.Actor = &events.Actor{Platform: "twitch", ID: e.FromID, Login: e.FromLogin, DisplayName: e.FromName}
		env.Type, payload = "audience.raid.received", map[string]any{"viewers": e.Viewers}
	case "channel.channel_points_custom_reward_redemption.add":
		var e struct {
			ID         string `json:"id"`
			UserInput  string `json:"user_input"`
			Status     string `json:"status"`
			RedeemedAt string `json:"redeemed_at"`
			Reward     struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Cost   int    `json:"cost"`
				Prompt string `json:"prompt"`
			} `json:"reward"`
		}
		if json.Unmarshal(raw, &e) != nil || e.ID == "" {
			return events.Envelope{}, false
		}
		// The redemption's id, not the message's: the same redemption is
		// one event, however Twitch delivers it.
		env.SourceEventID = "redemption:" + e.ID
		env.Type, payload = "rewards.channel_points.redeemed", map[string]any{
			"redemption_id": e.ID, "user_input": e.UserInput, "status": e.Status, "redeemed_at": e.RedeemedAt,
			"reward": map[string]any{"id": e.Reward.ID, "title": e.Reward.Title, "cost": e.Reward.Cost, "prompt": e.Reward.Prompt},
		}
	case "stream.online":
		var e struct {
			Type      string `json:"type"`
			StartedAt string `json:"started_at"`
		}
		_ = json.Unmarshal(raw, &e)
		env.Type, payload = "broadcast.channel.online", map[string]any{"stream_type": e.Type, "started_at": e.StartedAt}
	case "stream.offline":
		env.Type, payload = "broadcast.channel.offline", map[string]any{}
	case "channel.update":
		var e struct {
			Title        string   `json:"title"`
			Language     string   `json:"language"`
			CategoryID   string   `json:"category_id"`
			CategoryName string   `json:"category_name"`
			Labels       []string `json:"content_classification_labels"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Labels == nil {
			e.Labels = []string{}
		}
		env.Type, payload = "broadcast.channel.updated", map[string]any{"title": e.Title, "language": e.Language,
			"category_id": e.CategoryID, "category": e.CategoryName, "labels": e.Labels}
	default:
		return events.Envelope{}, false
	}
	env.Payload, _ = json.Marshal(payload)
	return env, true
}

// storeChannelEvent stores one channel event. A redelivery (the same
// message id) is stored once, by the inbox.
func (s *Service) storeChannelEvent(ctx context.Context, m wsMessage) {
	occurred, err := time.Parse(time.RFC3339Nano, m.Metadata.MessageTimestamp)
	if err != nil {
		occurred = s.now()
	}
	env, ok := channelEnvelope(m.Metadata.SubscriptionType, m.Metadata.MessageID, m.Payload.Event, occurred)
	if !ok {
		s.log.Warn("twitch: a channel event that couldn't be read", "type", m.Metadata.SubscriptionType)
		return
	}
	if env.Type == "broadcast.channel.updated" {
		var p struct{ Title, Category string }
		_ = json.Unmarshal(env.Payload, &p)
		s.setChannel(p.Title, p.Category)
	}
	if s.inbox == nil {
		return
	}
	if _, err := s.inbox.Accept(ctx, env); err != nil && !errors.Is(err, events.ErrConflict) {
		s.log.Warn("twitch: couldn't store a channel event", "type", env.Type, "err", err)
	}
}
