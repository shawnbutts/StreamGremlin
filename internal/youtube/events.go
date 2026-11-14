package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// YouTube's events (M6 slice 4, D-099), from the chat stream: Super Chats,
// Super Stickers, Jewels, memberships, members-only chat, bans and
// timeouts; and your broadcast going live and offline. Memberships use
// Twitch's subscription types, from source youtube, since members count as
// subscribers (D-098); the rest have types of their own.

// Event types only YouTube sends.
const (
	TypeSuperChat    = "monetization.super_chat.received"
	TypeSuperSticker = "monetization.super_sticker.received"
	TypeJewels       = "monetization.jewels.gifted"
	TypeMembersOnly  = "chat.members_only.changed"
	TypeBanned       = "moderation.user.banned"
	TypeTimedOut     = "moderation.user.timed_out"
	typeSubStarted   = "monetization.subscription.started"
	typeSubRenewed   = "monetization.subscription.renewed"
	typeSubGifted    = "monetization.subscription.gifted"
	typeOnline       = "broadcast.channel.online"
	typeOffline      = "broadcast.channel.offline"
	youtubeWatch     = "https://www.youtube.com/watch?v="
	noTier           = 0 // YouTube's membership levels have names, not numbers
)

// amount is micros as a number of the currency's units (5.0 for $5).
func amount(micros uint64) float64 { return float64(micros) / 1e6 }

// eventEnvelope turns a YouTube message other than text into its event, or
// nil for a type StreamGremlin doesn't store (deletions, polls: later).
func eventEnvelope(m livechat.Message, b Broadcast, channel string, backlog bool) *events.Envelope {
	sn, a := m.Snippet, m.Author
	actor := &events.Actor{Platform: events.SourceYouTube, ID: a.ChannelID, DisplayName: a.DisplayName}
	from := a.DisplayName
	var typ string
	var p map[string]any
	switch {
	case sn.SuperChat != nil:
		d := sn.SuperChat
		typ, p = TypeSuperChat, map[string]any{"amount": amount(d.AmountMicros), "amount_micros": d.AmountMicros,
			"currency": d.Currency, "amount_display": d.AmountDisplayString, "tier": d.Tier, "text": d.UserComment, "from": from}
	case sn.SuperSticker != nil:
		d := sn.SuperSticker
		typ, p = TypeSuperSticker, map[string]any{"amount": amount(d.AmountMicros), "amount_micros": d.AmountMicros,
			"currency": d.Currency, "amount_display": d.AmountDisplayString, "tier": d.Tier, "sticker_id": d.StickerID,
			"sticker": d.AltText, "from": from}
	case sn.Gift != nil:
		d := sn.Gift
		typ, p = TypeJewels, map[string]any{"gift": d.Name, "jewels": d.JewelsCount, "count": max(d.ComboCount, 1),
			"description": d.AltText, "from": from}
	case sn.NewSponsor != nil:
		d := sn.NewSponsor
		typ, p = typeSubStarted, map[string]any{"tier": noTier, "is_gift": false, "level": d.MemberLevelName, "is_upgrade": d.IsUpgrade}
	case sn.GiftMembershipReceived != nil:
		d := sn.GiftMembershipReceived
		typ, p = typeSubStarted, map[string]any{"tier": noTier, "is_gift": true, "level": d.MemberLevelName,
			"gifter_channel_id": d.GifterChannelID}
	case sn.MemberMilestone != nil:
		d := sn.MemberMilestone
		typ, p = typeSubRenewed, map[string]any{"tier": noTier, "level": d.MemberLevelName, "months": d.MemberMonth,
			"text": d.UserComment, "duration_months": 1}
	case sn.MembershipGifting != nil:
		d := sn.MembershipGifting
		typ, p = typeSubGifted, map[string]any{"tier": noTier, "level": d.LevelName, "total": d.Count, "is_anonymous": false, "from": from}
	case sn.Type == livechat.TypeSponsorOnlyModeStarted || sn.Type == livechat.TypeSponsorOnlyModeEnded:
		typ, p = TypeMembersOnly, map[string]any{"enabled": sn.Type == livechat.TypeSponsorOnlyModeStarted}
		actor = nil
	case sn.UserBanned != nil:
		d := sn.UserBanned
		typ = TypeBanned
		p = map[string]any{"permanent": true, "reason": ""}
		if d.BanType == livechat.BanTemporary {
			typ, p = TypeTimedOut, map[string]any{"duration_seconds": d.DurationSeconds}
		}
		// Who it's about is the viewer banned; the author is who did it.
		p["moderator"] = map[string]any{"id": a.ChannelID, "display_name": a.DisplayName}
		p["channel_url"] = d.ChannelURL
		actor = &events.Actor{Platform: events.SourceYouTube, ID: d.ChannelID, DisplayName: d.DisplayName}
	default:
		return nil
	}
	p["message_id"], p["live_chat_id"], p["broadcast_id"], p["message_type"] = m.ID, b.LiveChatID, b.ID, sn.Type.String()
	if backlog {
		p["backlog"] = true // from before StreamGremlin was reading: starts nothing
	}
	occurred, err := time.Parse(time.RFC3339Nano, sn.PublishedAt)
	if err != nil {
		occurred = time.Now()
	}
	raw, _ := json.Marshal(p)
	env := events.Envelope{EventID: events.NewID(), Type: typ, Source: events.SourceYouTube, SourceEventID: m.ID,
		OccurredAt: occurred, ChannelID: channel, Actor: actor, Payload: raw, DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	return &env
}

// storeEvent stores a YouTube event, once.
func (s *Service) storeEvent(ctx context.Context, env events.Envelope) {
	if s.inbox == nil {
		return
	}
	if _, err := s.inbox.Accept(ctx, env); err != nil && !errors.Is(err, events.ErrConflict) {
		s.log.Warn("youtube: couldn't store an event", "type", env.Type, "err", err)
	}
}

// broadcastEvent records your broadcast going live or offline, once each
// (by the broadcast's ID, so a restart or resumed stream doesn't repeat it).
func (s *Service) broadcastEvent(ctx context.Context, b Broadcast, channel string, online bool) {
	typ, id := typeOffline, "offline:"+b.ID
	p := map[string]any{"broadcast_id": b.ID, "title": b.Title, "url": youtubeWatch + b.ID}
	occurred := s.now()
	if online {
		typ, id = typeOnline, "online:"+b.ID
		p["stream_type"] = "live"
		if !b.StartedAt.IsZero() {
			occurred = b.StartedAt
			p["started_at"] = b.StartedAt.UTC().Format(time.RFC3339)
		}
	}
	raw, _ := json.Marshal(p)
	env := events.Envelope{EventID: events.NewID(), Type: typ, Source: events.SourceYouTube, SourceEventID: id,
		OccurredAt: occurred, ChannelID: channel, Payload: raw, DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	s.storeEvent(ctx, env)
}

// storeDeleted records that a chat message was deleted, once, whether
// Google's tombstone or StreamGremlin's own delete says so first. Live
// shows the message struck through.
func (s *Service) storeDeleted(ctx context.Context, messageID, chatID string) {
	channel := ""
	if st := s.streamer.Status(); st.Channel != nil {
		channel = st.Channel.ID
	}
	raw, _ := json.Marshal(map[string]any{"message_id": messageID, "live_chat_id": chatID})
	env := events.Envelope{EventID: events.NewID(), Type: events.ChatDeleted, Source: events.SourceYouTube,
		SourceEventID: "deleted:" + messageID, OccurredAt: s.now(), ChannelID: channel, Payload: raw, DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	s.storeEvent(ctx, env)
}
