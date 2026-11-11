package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
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
	// Hype Trains (docs/adr/0022-hype-trains.md): Affiliate or Partner.
	{Type: "channel.hype_train.begin", Label: "Hype Train starts", Version: "2", Scopes: []string{"channel:read:hype_train"}},
	{Type: "channel.hype_train.progress", Label: "Hype Train progress", Version: "2", Scopes: []string{"channel:read:hype_train"}},
	{Type: "channel.hype_train.end", Label: "Hype Train ends", Version: "2", Scopes: []string{"channel:read:hype_train"}},
	// Polls and predictions (docs/adr/0023-polls-and-predictions.md): Affiliate or Partner.
	{Type: "channel.poll.begin", Label: "Polls start", Version: "1", Scopes: pollScopes},
	{Type: "channel.poll.progress", Label: "Poll votes", Version: "1", Scopes: pollScopes},
	{Type: "channel.poll.end", Label: "Polls end", Version: "1", Scopes: pollScopes},
	{Type: "channel.prediction.begin", Label: "Predictions start", Version: "1", Scopes: predictionScopes},
	{Type: "channel.prediction.progress", Label: "Prediction bets", Version: "1", Scopes: predictionScopes},
	{Type: "channel.prediction.lock", Label: "Predictions lock", Version: "1", Scopes: predictionScopes},
	{Type: "channel.prediction.end", Label: "Predictions end", Version: "1", Scopes: predictionScopes},
	// Ad breaks (docs/adr/0024-ads.md): Affiliate or Partner.
	{Type: "channel.ad_break.begin", Label: "Ad breaks", Version: "1", Scopes: []string{"channel:read:ads"}},
	// Suspicious users (D-079): Twitch's ban-evasion and shared-ban flags.
	{Type: "channel.suspicious_user.message", Label: "Suspicious users' messages", Version: "1", Scopes: suspiciousScopes},
	{Type: "channel.suspicious_user.update", Label: "Suspicious users' treatment", Version: "1", Scopes: suspiciousScopes},
}

var (
	pollScopes       = []string{"channel:read:polls", "channel:manage:polls"}
	predictionScopes = []string{"channel:read:predictions", "channel:manage:predictions"}
	suspiciousScopes = []string{SuspiciousReadScope}
)

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
		case "channel.follow", "channel.suspicious_user.message", "channel.suspicious_user.update":
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
	case "channel.hype_train.begin", "channel.hype_train.progress", "channel.hype_train.end":
		var e hypeTrainEvent
		if json.Unmarshal(raw, &e) != nil {
			return events.Envelope{}, false
		}
		env.Type = map[string]string{"channel.hype_train.begin": "monetization.hype_train.started",
			"channel.hype_train.progress": "monetization.hype_train.updated", "channel.hype_train.end": "monetization.hype_train.ended"}[typ]
		env.Actor = nil // the channel's own train: no one viewer
		payload = e.payload(typ == "channel.hype_train.end")
	case "channel.ad_break.begin":
		var e struct {
			Duration       json.RawMessage `json:"duration_seconds"`
			StartedAt      json.RawMessage `json:"started_at"`
			IsAutomatic    json.RawMessage `json:"is_automatic"`
			RequesterID    string          `json:"requester_user_id"`
			RequesterLogin string          `json:"requester_user_login"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return events.Envelope{}, false
		}
		dur, err := adInt(e.Duration)
		started, terr := adTime(e.StartedAt)
		if err != nil || terr != nil || started == nil {
			return events.Envelope{}, false
		}
		auto := strings.Trim(string(e.IsAutomatic), `"`) == "true"
		// Keyed by when it started: a redelivery under a new message id is
		// still one break.
		env.Type, env.Actor, env.OccurredAt = "ads.break.started", nil, *started
		env.SourceEventID = "ad_break:" + base.BroadcasterUserID + ":" + started.Format(time.RFC3339)
		payload = map[string]any{"duration_seconds": dur, "started_at": started.Format(time.RFC3339), "is_automatic": auto,
			"requester_id": e.RequesterID, "requester_login": e.RequesterLogin}
	case "channel.poll.begin", "channel.poll.progress", "channel.poll.end":
		var e pollEvent
		if json.Unmarshal(raw, &e) != nil {
			return events.Envelope{}, false
		}
		env.Type = map[string]string{"channel.poll.begin": "chat.poll.started", "channel.poll.progress": "chat.poll.updated",
			"channel.poll.end": "chat.poll.ended"}[typ]
		env.Actor = nil
		payload = e.payload()
	case "channel.prediction.begin", "channel.prediction.progress", "channel.prediction.lock", "channel.prediction.end":
		var e predictionEvent
		if json.Unmarshal(raw, &e) != nil {
			return events.Envelope{}, false
		}
		env.Type = map[string]string{"channel.prediction.begin": "chat.prediction.started", "channel.prediction.progress": "chat.prediction.updated",
			"channel.prediction.lock": "chat.prediction.locked", "channel.prediction.end": "chat.prediction.ended"}[typ]
		env.Actor = nil
		payload = e.payload()
	case "channel.suspicious_user.message":
		var e struct {
			LowTrust   string   `json:"low_trust_status"`
			Types      []string `json:"types"`
			BanEvasion string   `json:"ban_evasion_evaluation"`
			SharedBans []string `json:"shared_ban_channel_ids"`
			Message    struct {
				ID   string `json:"message_id"`
				Text string `json:"text"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &e) != nil || env.Actor == nil {
			return events.Envelope{}, false
		}
		if e.Types == nil {
			e.Types = []string{}
		}
		if e.SharedBans == nil {
			e.SharedBans = []string{}
		}
		// Keyed by the chat message: a redelivery is one event.
		if e.Message.ID != "" {
			env.SourceEventID = "suspicious:" + e.Message.ID
		}
		env.Type, payload = "moderation.suspicious_user.message", map[string]any{"message_id": e.Message.ID, "text": e.Message.Text,
			"low_trust_status": e.LowTrust, "types": e.Types, "ban_evasion": e.BanEvasion, "shared_ban_channel_ids": e.SharedBans}
	case "channel.suspicious_user.update":
		var e struct {
			LowTrust string `json:"low_trust_status"`
			ModID    string `json:"moderator_user_id"`
			ModLogin string `json:"moderator_user_login"`
			ModName  string `json:"moderator_user_name"`
		}
		if json.Unmarshal(raw, &e) != nil || env.Actor == nil {
			return events.Envelope{}, false
		}
		env.Type, payload = "moderation.suspicious_user.updated", map[string]any{"low_trust_status": e.LowTrust,
			"moderator": map[string]any{"id": e.ModID, "login": e.ModLogin, "name": e.ModName}}
	default:
		return events.Envelope{}, false
	}
	env.Payload, _ = json.Marshal(payload)
	return env, true
}

// hypeTrainEvent is channel.hype_train.* v2, as Twitch's reference shows it.
type hypeTrainEvent struct {
	ID               string `json:"id"`
	Level            int    `json:"level"`
	Total            int    `json:"total"`
	Progress         int    `json:"progress"`
	Goal             int    `json:"goal"`
	Type             string `json:"type"` // regular, treasure, golden_kappa
	IsShared         bool   `json:"is_shared_train"`
	StartedAt        string `json:"started_at"`
	ExpiresAt        string `json:"expires_at"`
	EndedAt          string `json:"ended_at"`
	CooldownEndsAt   string `json:"cooldown_ends_at"`
	AllTimeHighLevel int    `json:"all_time_high_level"`
	AllTimeHighTotal int    `json:"all_time_high_total"`
	Top              []struct {
		UserID    string `json:"user_id"`
		UserLogin string `json:"user_login"`
		UserName  string `json:"user_name"`
		Type      string `json:"type"` // bits, subscription, other
		Total     int    `json:"total"`
	} `json:"top_contributions"`
}

// payload is the train in StreamGremlin's words. top is the biggest
// contributor (by Twitch's own total), or none.
func (e hypeTrainEvent) payload(ended bool) map[string]any {
	tops := []map[string]any{}
	var top map[string]any
	best := -1
	for _, c := range e.Top {
		x := map[string]any{"user_id": c.UserID, "login": c.UserLogin, "name": c.UserName, "type": c.Type, "total": c.Total}
		tops = append(tops, x)
		if c.Total > best {
			best, top = c.Total, x
		}
	}
	p := map[string]any{"hype_train_id": e.ID, "level": e.Level, "total": e.Total, "train_type": e.Type,
		"is_shared": e.IsShared, "started_at": e.StartedAt, "top_contributions": tops}
	if top != nil {
		p["top_contributor"] = top
	}
	if ended {
		p["ended_at"], p["cooldown_ends_at"] = e.EndedAt, e.CooldownEndsAt
	} else {
		p["progress"], p["goal"], p["expires_at"] = e.Progress, e.Goal, e.ExpiresAt
	}
	if e.AllTimeHighLevel > 0 {
		p["all_time_high_level"], p["all_time_high_total"] = e.AllTimeHighLevel, e.AllTimeHighTotal
	}
	return p
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
