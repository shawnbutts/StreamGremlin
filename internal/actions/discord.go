package actions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/discord"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerDiscord adds posting to Discord through a webhook (D-089), and
// summing up the stream for a post when it ends.
func registerDiscord(reg *workflow.Registry, dc *discord.Service, inbox *events.Inbox) {
	str := func(c workflow.Call, name string) string { s, _ := c.String(name, ""); return strings.TrimSpace(s) }
	reg.Add(workflow.Action{
		Name: "discord.send", Label: "Post to Discord", Group: "Discord", External: true,
		Description: "Posts a message, a card, or both, to a Discord channel through a webhook you added on Connections. " +
			"Nobody is notified unless you choose @everyone or @here, whatever the text says. Long text is cut to fit " +
			"Discord's limits. Sent once: if Discord doesn't answer, it isn't sent again.",
		Params: []workflow.Param{
			{Name: "webhook", Label: "Webhook", Type: "text", Required: true, Literal: true, Editor: "discord_webhook"},
			{Name: "message", Label: "Message", Type: "text", Editor: "message", DefaultHint: "Only the card"},
			{Name: "mention", Label: "Notify", Type: "choice", Choices: []string{"none", "everyone", "here"}, DefaultHint: "Nobody"},
			{Name: "title", Label: "Card title", Type: "text", Editor: "message", DefaultHint: "No card"},
			{Name: "description", Label: "Card text", Type: "text", Editor: "message"},
			{Name: "link", Label: "Card link (the title links to it)", Type: "text", Editor: "message"},
			{Name: "picture", Label: "Card picture (a web link)", Type: "text", Editor: "message"},
			{Name: "color", Label: "Card colour", Type: "text", Pattern: `^(#?[0-9a-fA-F]{6})?$`, PatternHint: "Like #9146ff", DefaultHint: "Discord's grey"},
			{Name: "footer", Label: "Card footer", Type: "text", Editor: "message"},
			{Name: "username", Label: "Post as (name)", Type: "text", Editor: "message", DefaultHint: "The webhook's name in Discord"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if dc == nil {
				return nil, errors.New("discord isn't set up")
			}
			mention := discord.MentionNone
			switch m := str(c, "mention"); m {
			case "", "none":
			case "everyone", "here":
				mention = discord.Mention(m)
			default:
				return nil, errors.New("notify is none, everyone or here")
			}
			msg := discord.Message{Content: str(c, "message"), Username: str(c, "username"), Mention: mention,
				Embed: discord.Embed{Title: str(c, "title"), Description: str(c, "description"), URL: str(c, "link"),
					Image: str(c, "picture"), Color: str(c, "color"), Footer: str(c, "footer")}}
			sent, err := dc.Send(ctx, str(c, "webhook"), msg)
			if err != nil {
				return nil, errors.New(strings.TrimPrefix(err.Error(), "discord: "))
			}
			return map[string]any{"message_id": sent.MessageID, "webhook": str(c, "webhook"), "truncated": sent.Truncated}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "stream.summary", Label: "Sum up the stream", Group: "Twitch",
		Description: "Counts what happened since you last went live on Twitch: how long, new followers, subs, gifted subs, " +
			"bits, raids and chatters, and on YouTube new members, gifted memberships and Super Chats (chatters count both). " +
			"Test events aren't counted. Changes nothing, so it runs for test events too. Its " +
			"state is unknown when StreamGremlin didn't see you go live.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if inbox == nil {
				return map[string]any{"state": "unknown"}, nil
			}
			return summarize(ctx, inbox, time.Now())
		},
	})
}

// summaryCap bounds how many events a summary reads.
const summaryCap = 100000

func summarize(ctx context.Context, inbox *events.Inbox, now time.Time) (map[string]any, error) {
	on, ok, err := inbox.LatestReal(ctx, "broadcast.channel.online")
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{"state": "unknown", "why": "StreamGremlin didn't see you go live"}, nil
	}
	end := now
	state := "live"
	if off, ok, err := inbox.LatestReal(ctx, "broadcast.channel.offline"); err != nil {
		return nil, err
	} else if ok && off.Seq > on.Seq {
		end, state = off.OccurredAt, "ended"
	}
	var follows, subs, gifted, bits, raids, raiders, messages, members, giftedMembers, superChats int64
	chatters := map[string]bool{}
	ytChatters := 0
	after := on.Seq
	types := events.Filter{"audience.follow.created", "monetization.subscription.started", "monetization.subscription.renewed",
		"monetization.subscription.gifted", "monetization.bits.cheered", "audience.raid.received", "chat.message.received",
		"chat.command.received", "broadcast.channel.offline", "monetization.super_chat.received", "monetization.super_sticker.received"}
	for read := 0; read < summaryCap; {
		recs, err := inbox.After(ctx, after, 1000, types)
		if err != nil {
			return nil, err
		}
		if len(recs) == 0 {
			break
		}
		read += len(recs)
		done := false
		for _, r := range recs {
			after = r.Seq
			if r.DeliveryQuality == events.Synthetic || events.Backlog(r) {
				continue
			}
			yt := r.Source == events.SourceYouTube
			if r.Type == "broadcast.channel.offline" {
				if yt {
					continue // the summary is of the Twitch stream
				}
				done = true
				break
			}
			var p struct {
				Total   int64 `json:"total"`
				Bits    int64 `json:"bits"`
				Viewers int64 `json:"viewers"`
				Chatter struct {
					Role string `json:"role"`
				} `json:"chatter"`
			}
			_ = json.Unmarshal(r.Payload, &p)
			switch r.Type {
			case "audience.follow.created":
				follows++
			case "monetization.subscription.started", "monetization.subscription.renewed":
				if yt {
					members++
				} else {
					subs++
				}
			case "monetization.subscription.gifted":
				if yt {
					giftedMembers += max(p.Total, 1)
				} else {
					gifted += max(p.Total, 1)
				}
			case "monetization.super_chat.received", "monetization.super_sticker.received":
				superChats++
			case "monetization.bits.cheered":
				bits += p.Bits
			case "audience.raid.received":
				raids++
				raiders += p.Viewers
			default: // chat
				if p.Chatter.Role == "broadcaster" || r.Actor == nil {
					continue
				}
				messages++
				key := r.Actor.Platform + ":" + r.Actor.ID
				if yt && !chatters[key] {
					ytChatters++
				}
				chatters[key] = true
			}
		}
		if done {
			break
		}
	}
	d := end.Sub(on.OccurredAt)
	return map[string]any{"state": state, "started_at": on.OccurredAt.UTC().Format(time.RFC3339),
		"duration": twitch.DurationWords(d), "duration_seconds": int(d.Seconds()),
		"follows": follows, "subs": subs, "gifted_subs": gifted, "bits": bits, "raids": raids, "raiders": raiders,
		"chatters": len(chatters), "messages": messages,
		"youtube_chatters": ytChatters, "members": members, "gifted_memberships": giftedMembers, "super_chats": superChats}, nil
}
