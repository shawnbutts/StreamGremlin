package twitch

import (
	"encoding/json"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/chat"
	"code.streamgremlin.com/streamgremlin/internal/events"
)

// chatEvent is the channel.chat.message v1 event, the fields we use.
type chatEvent struct {
	BroadcasterUserID    string `json:"broadcaster_user_id"`
	BroadcasterUserLogin string `json:"broadcaster_user_login"`
	ChatterUserID        string `json:"chatter_user_id"`
	ChatterUserLogin     string `json:"chatter_user_login"`
	ChatterUserName      string `json:"chatter_user_name"`
	MessageID            string `json:"message_id"`
	Message              struct {
		Text      string `json:"text"`
		Fragments []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"fragments"`
	} `json:"message"`
	MessageType string  `json:"message_type"`
	Color       string  `json:"color"`
	Badges      []badge `json:"badges"`
	Cheer       *struct {
		Bits int `json:"bits"`
	} `json:"cheer"`
	Reply *struct {
		ParentMessageID string `json:"parent_message_id"`
	} `json:"reply"`
	ChannelPointsCustomRewardID string `json:"channel_points_custom_reward_id"`
	SourceBroadcasterUserID     string `json:"source_broadcaster_user_id"`
}

type badge struct {
	SetID string `json:"set_id"`
	ID    string `json:"id"`
	Info  string `json:"info"`
}

func hasBadge(bs []badge, set string) bool {
	for _, b := range bs {
		if b.SetID == set {
			return true
		}
	}
	return false
}

// chatterPayload is the "chatter" object of chat events, the same shape
// test events use (events.Catalog).
func chatterPayload(ev chatEvent) map[string]any {
	isB := ev.ChatterUserID == ev.BroadcasterUserID || hasBadge(ev.Badges, "broadcaster")
	isM := hasBadge(ev.Badges, "moderator")
	isV := hasBadge(ev.Badges, "vip")
	isS := hasBadge(ev.Badges, "subscriber") || hasBadge(ev.Badges, "founder")
	role := "viewer"
	switch {
	case isB:
		role = "broadcaster"
	case isM:
		role = "moderator"
	case isV:
		role = "vip"
	case isS:
		role = "subscriber"
	}
	badges := ev.Badges
	if badges == nil {
		badges = []badge{}
	}
	return map[string]any{
		"id": ev.ChatterUserID, "login": ev.ChatterUserLogin, "display_name": ev.ChatterUserName,
		"badges": badges, "role": role,
		"is_broadcaster": isB, "is_moderator": isM, "is_vip": isV, "is_subscriber": isS,
	}
}

// chatEnvelopes turns one Twitch chat message into a chat.message.received
// event and, for "!name args", a chat.command.received event. Messages from
// another channel in a shared chat never make commands.
func chatEnvelopes(ev chatEvent, occurred time.Time) (msg events.Envelope, cmd *events.Envelope) {
	payload := map[string]any{
		"message_id":   ev.MessageID,
		"text":         ev.Message.Text,
		"chatter":      chatterPayload(ev),
		"message_type": ev.MessageType,
		"color":        ev.Color,
	}
	// The emotes' names, once per use, for the emote filter (D-085).
	emotes := []string{}
	for _, f := range ev.Message.Fragments {
		if f.Type == "emote" {
			emotes = append(emotes, f.Text)
		}
	}
	payload["emotes"] = emotes
	if ev.Cheer != nil {
		payload["bits"] = ev.Cheer.Bits
	}
	if ev.Reply != nil && ev.Reply.ParentMessageID != "" {
		payload["reply_parent_message_id"] = ev.Reply.ParentMessageID
	}
	if ev.ChannelPointsCustomRewardID != "" {
		payload["channel_points_custom_reward_id"] = ev.ChannelPointsCustomRewardID
	}
	shared := ev.SourceBroadcasterUserID != "" && ev.SourceBroadcasterUserID != ev.BroadcasterUserID
	if shared {
		payload["source_broadcaster_id"] = ev.SourceBroadcasterUserID
	}
	actor := &events.Actor{Platform: "twitch", ID: ev.ChatterUserID, Login: ev.ChatterUserLogin, DisplayName: ev.ChatterUserName}
	raw, _ := json.Marshal(payload)
	msg = events.Envelope{
		EventID: events.NewID(), Type: "chat.message.received", Source: "twitch", SourceEventID: ev.MessageID,
		OccurredAt: occurred, ChannelID: ev.BroadcasterUserID, Actor: actor, Payload: raw, DeliveryQuality: events.Confirmed,
	}
	msg.CorrelationID = msg.EventID
	c, ok := chat.ParseCommand(ev.Message.Text)
	if !ok || shared {
		return msg, nil
	}
	payload["command"], payload["args"], payload["arg_list"] = c.Name, c.Args, c.ArgList
	raw, _ = json.Marshal(payload)
	cmd = &events.Envelope{
		Type: "chat.command.received", Source: "twitch", SourceEventID: "cmd:" + ev.MessageID,
		OccurredAt: occurred, ChannelID: ev.BroadcasterUserID, Actor: actor, Payload: raw,
		CorrelationID: msg.EventID, CausationID: msg.EventID, DeliveryQuality: events.Confirmed,
	}
	return msg, cmd
}
