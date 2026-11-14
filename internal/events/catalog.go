package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Field is one input of a test event's form.
type Field struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // "text", "number" or "choice"
	Default string `json:"default"`
	Max     int    `json:"max"` // longest text, in characters
	// Choices are the values a choice field takes.
	Choices []string `json:"choices,omitempty"`
	// Path, when set, is the payload path the field sets (chatter.role), so
	// a test event can be made to match a trigger's where.
	Path string `json:"path,omitempty"`
}

// Roles are chatters' roles, lowest first. A chatter's role is the highest
// that applies.
var Roles = []string{"viewer", "subscriber", "vip", "moderator", "broadcaster"}

var roleField = Field{Name: "role", Label: "Role", Kind: "choice", Default: "viewer", Max: 11, Choices: Roles, Path: "chatter.role"}

// practiceFields describe a test chatter for practice giveaways' entry
// rules (docs/adr/0026-giveaway-entry-rules.md): Twitch is never asked
// about test chat.
var practiceFields = []Field{
	{Name: "follow_days", Label: "Following for (days, -1 for not following)", Kind: "number", Default: "30", Max: 6},
	{Name: "account_days", Label: "Account age (days)", Kind: "number", Default: "365", Max: 6},
	{Name: "sub_months", Label: "Subscribed for (months, if a subscriber)", Kind: "number", Default: "1", Max: 4},
}

func withPractice(fs []Field) []Field { return append(fs, practiceFields...) }

// Kind is one event type the engine knows: what it means, which sources
// send it, and how to make a test event of it.
type Kind struct {
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Sources     []string `json:"sources"`
	// Sensitive events carry viewers' words; they're sent to readers that
	// ask for them by type, and kept out of logs.
	Sensitive bool    `json:"sensitive"`
	Fields    []Field `json:"fields"`

	// build makes the payload; id is unique to the test event.
	build func(id string, v map[string]string) (payload any, actor *Actor, err error)
}

// TimerFired is the event a timer records each time it goes off.
const TimerFired = "timers.timer.fired"

// ButtonPressed is the event a press of one of the streamer's buttons
// records (M7, D-075).
const ButtonPressed = "buttons.button.pressed"

// ChatSent is StreamGremlin's own chat message, by the bot or a workflow,
// as Twitch echoed it: for the Live screen, never viewer chat (D-087).
const ChatSent = "chat.message.sent"

// FilterCaught is the event a chat filter adds when it catches a message
// (D-085).
const FilterCaught = "chat.filter.caught"

// ClipCreated is the event StreamGremlin adds when it makes a clip, from
// any workflow (D-089).
const ClipCreated = "broadcast.clip.created"

// Suspicious users (D-079): Twitch's flags on a chatter's message, and a
// change in how Twitch treats them.
const (
	SuspiciousMessage = "moderation.suspicious_user.message"
	SuspiciousUpdated = "moderation.suspicious_user.updated"
)

// Catalog lists the types the engine knows in this build. Slices add their
// types here as they add the adapters that send them (docs/adr/0004-events.md).
// Payload shapes aren't frozen until the slice that sends the real event.
func Catalog() []Kind {
	return []Kind{
		{
			Type: "chat.message.received", Label: "Chat message",
			Description: "A viewer sent a message in chat.",
			Sources:     []string{"twitch"}, Sensitive: true,
			Fields: withPractice([]Field{
				{Name: "user", Label: "Viewer", Kind: "text", Default: "TestViewer", Max: 25},
				{Name: "text", Label: "Message", Kind: "text", Default: "Hello from a test event!", Max: 500},
				roleField,
			}),
			build: func(id string, v map[string]string) (any, *Actor, error) {
				a := testViewer(v["user"])
				ch, err := practiceChatter(a, v)
				if err != nil {
					return nil, nil, err
				}
				return map[string]any{
					"message_id":   id,
					"text":         v["text"],
					"chatter":      ch,
					"message_type": "text",
					"color":        "",
				}, a, nil
			},
		},
		{
			// Stored by the Twitch adapter for the bot's and workflows' own
			// messages (D-087). No trigger offers it: it isn't viewer chat.
			Type: ChatSent, Label: "StreamGremlin's own message",
			Description: "A message the bot, or a workflow as you, sent in your chat, as Twitch showed it. It's never viewer chat.",
			Sources:     []string{"twitch"}, Sensitive: true,
			Fields: []Field{{Name: "text", Label: "Message", Kind: "text", Default: "Thanks for the follow!", Max: 500}},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				a := testViewer("GremlinBot")
				ch, err := chatter(a, "viewer")
				if err != nil {
					return nil, nil, err
				}
				return map[string]any{"message_id": id, "text": v["text"], "chatter": ch, "message_type": "text", "color": "",
					"sent_by": "bot"}, a, nil
			},
		},
		{
			// Made by internal/filters when a chat filter catches a message (D-085).
			Type: FilterCaught, Label: "Chat filter caught a message",
			Description: "A chat filter caught a viewer's message and deleted it, timed them out or warned them.",
			Sources:     []string{"twitch"}, Sensitive: true,
			Fields: []Field{
				{Name: "user", Label: "Viewer", Kind: "text", Default: "TestViewer", Max: 25},
				{Name: "text", Label: "Message", Kind: "text", Default: "buy followers at example.com", Max: 500},
				{Name: "rule", Label: "Filter", Kind: "choice", Default: "links", Max: 10, Path: "rule",
					Choices: []string{"words", "links", "emotes", "caps", "symbols", "repeats"}},
				{Name: "action", Label: "What it did", Kind: "choice", Default: "delete", Max: 10, Choices: []string{"delete", "timeout", "warn"}},
			},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				a := testViewer(v["user"])
				ch, err := chatter(a, "viewer")
				if err != nil {
					return nil, nil, err
				}
				secs := 0
				if v["action"] == "timeout" {
					secs = 600
				}
				return map[string]any{"rule": v["rule"], "action": v["action"], "seconds": secs, "state": "test",
					"message_id": id, "text": v["text"], "chatter": ch}, a, nil
			},
		},
		{
			// Made by internal/firstwords from a chat message (docs/adr/0021-first-words.md);
			// a test chat message makes a test one too.
			Type: "chat.first_words.received", Label: "First words",
			Description: "A viewer's first chat message this stream (since you went live, a reset, or 12 quiet hours).",
			Sources:     []string{"twitch"}, Sensitive: true,
			Fields: []Field{
				{Name: "user", Label: "Viewer", Kind: "text", Default: "NewViewer", Max: 25},
				{Name: "text", Label: "Message", Kind: "text", Default: "Hi everyone!", Max: 500},
				roleField,
			},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				a := testViewer(v["user"])
				ch, err := chatter(a, v["role"])
				if err != nil {
					return nil, nil, err
				}
				return map[string]any{"message_id": id, "text": v["text"], "chatter": ch, "message_type": "text", "color": ""}, a, nil
			},
		},
		{
			Type: "chat.command.received", Label: "Chat command",
			Description: "A viewer typed a command, such as !hello, in chat.",
			Sources:     []string{"twitch"}, Sensitive: true,
			Fields: withPractice([]Field{
				{Name: "user", Label: "Viewer", Kind: "text", Default: "TestViewer", Max: 25},
				{Name: "command", Label: "Command", Kind: "text", Default: "!hello", Max: 50},
				{Name: "args", Label: "After the command", Kind: "text", Default: "", Max: 450},
				roleField,
			}),
			build: func(id string, v map[string]string) (any, *Actor, error) {
				cmd := strings.TrimSpace(v["command"])
				if !strings.HasPrefix(cmd, "!") || len(cmd) < 2 || strings.ContainsAny(cmd, " \t") {
					return nil, nil, invalid("a command starts with ! and has no spaces, like !hello")
				}
				a := testViewer(v["user"])
				ch, err := practiceChatter(a, v)
				if err != nil {
					return nil, nil, err
				}
				args := strings.TrimSpace(v["args"])
				text := cmd
				if args != "" {
					text += " " + args
				}
				return map[string]any{
					"message_id":   id,
					"text":         text,
					"command":      strings.ToLower(cmd[1:]),
					"args":         args,
					"arg_list":     strings.Fields(args),
					"chatter":      ch,
					"message_type": "text",
					"color":        "",
				}, a, nil
			},
		},
		{
			Type: "broadcast.scene.changed", Label: "OBS scene changed",
			Description: "OBS switched to another scene.",
			Sources:     []string{"obs"},
			Fields: []Field{
				{Name: "scene_name", Label: "New scene", Kind: "text", Default: "Starting Soon", Max: 256},
				{Name: "previous_scene_name", Label: "Previous scene", Kind: "text", Default: "Just Chatting", Max: 256},
			},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				if strings.TrimSpace(v["scene_name"]) == "" {
					return nil, nil, invalid("the new scene needs a name")
				}
				return map[string]any{
					"scene_name":          v["scene_name"],
					"previous_scene_name": v["previous_scene_name"],
					"changed_by":          "external",
				}, nil, nil
			},
		},
		obsKind("broadcast.scene_item.visibility_changed", "OBS source shown or hidden", "A source in a scene was shown or hidden, by anyone.",
			[]Field{{Name: "scene_name", Label: "Scene", Kind: "text", Default: "Just Chatting", Max: 256},
				{Name: "source_name", Label: "Source", Kind: "text", Default: "Webcam", Max: 256},
				{Name: "visible", Label: "Visible (true or false)", Kind: "text", Default: "true", Max: 5}},
			func(v map[string]string) map[string]any {
				return map[string]any{"scene_name": v["scene_name"], "source_name": v["source_name"], "visible": v["visible"] == "true"}
			}),
		obsKind("broadcast.filter.enabled_changed", "OBS filter turned on or off", "A filter on a source or scene was turned on or off, by anyone.",
			[]Field{{Name: "source_name", Label: "Source", Kind: "text", Default: "Webcam", Max: 256},
				{Name: "filter_name", Label: "Filter", Kind: "text", Default: "Blur", Max: 256},
				{Name: "enabled", Label: "On (true or false)", Kind: "text", Default: "true", Max: 5}},
			func(v map[string]string) map[string]any {
				return map[string]any{"source_name": v["source_name"], "filter_name": v["filter_name"], "enabled": v["enabled"] == "true"}
			}),
		obsKind("broadcast.audio.mute_changed", "OBS audio muted or unmuted", "An audio source was muted or unmuted, by anyone.",
			[]Field{{Name: "source_name", Label: "Source", Kind: "text", Default: "Mic/Aux", Max: 256},
				{Name: "muted", Label: "Muted (true or false)", Kind: "text", Default: "true", Max: 5}},
			func(v map[string]string) map[string]any {
				return map[string]any{"source_name": v["source_name"], "muted": v["muted"] == "true"}
			}),
		obsKind("broadcast.media.started", "OBS media started", "A media source started playing.",
			[]Field{{Name: "source_name", Label: "Source", Kind: "text", Default: "Intro Video", Max: 256}},
			func(v map[string]string) map[string]any { return map[string]any{"source_name": v["source_name"]} }),
		obsKind("broadcast.media.ended", "OBS media ended", "A media source finished playing.",
			[]Field{{Name: "source_name", Label: "Source", Kind: "text", Default: "Intro Video", Max: 256}},
			func(v map[string]string) map[string]any { return map[string]any{"source_name": v["source_name"]} }),
		twitchKind(SuspiciousMessage, "Suspicious user chats",
			"Twitch flagged a chatter as suspicious: a likely or possible ban evader, banned in a channel you share bans with, or marked by you or a mod.", true,
			[]Field{{Name: "text", Label: "Their message", Kind: "text", Default: "hello again", Max: 500},
				{Name: "status", Label: "Twitch's treatment", Kind: "choice", Default: "active_monitoring", Max: 20, Choices: []string{"none", "active_monitoring", "restricted"}},
				{Name: "ban_evasion", Label: "Ban evader", Kind: "choice", Default: "likely", Max: 10, Choices: []string{"likely", "possible", "unknown", ""}}},
			func(a *Actor, v map[string]string) map[string]any {
				types := []string{}
				if v["ban_evasion"] == "likely" || v["ban_evasion"] == "possible" {
					types = append(types, "ban_evader")
				}
				return map[string]any{"message_id": "test", "text": v["text"], "low_trust_status": v["status"], "types": types,
					"ban_evasion": v["ban_evasion"], "shared_ban_channel_ids": []string{}}
			}),
		twitchKind(SuspiciousUpdated, "Suspicious user's treatment changed", "You or a mod changed how Twitch treats a suspicious user.", false,
			[]Field{{Name: "status", Label: "Twitch's treatment", Kind: "choice", Default: "restricted", Max: 20, Choices: []string{"none", "active_monitoring", "restricted"}}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"low_trust_status": v["status"], "moderator": map[string]any{"id": "1", "login": "you", "name": "You"}}
			}),
		twitchKind("audience.follow.created", "Follow", "Someone followed your channel.", false, nil,
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"followed_at": "2026-11-08T20:00:00Z"}
			}),
		twitchKind("monetization.subscription.started", "New sub", "Someone subscribed, or was given a sub.", false,
			[]Field{tierField, {Name: "is_gift", Label: "Given as a gift", Kind: "choice", Default: "false", Max: 5, Choices: []string{"false", "true"}}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"tier": atoi(v["tier"]), "is_gift": v["is_gift"] == "true"}
			}),
		twitchKind("monetization.subscription.renewed", "Resub", "Someone shared their resub, with a message.", true,
			[]Field{tierField, {Name: "months", Label: "Months in all", Kind: "number", Default: "12", Max: 5},
				{Name: "text", Label: "Their message", Kind: "text", Default: "A whole year!", Max: 500}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"tier": atoi(v["tier"]), "months": atoi(v["months"]), "text": v["text"], "duration_months": 1}
			}),
		twitchKind("monetization.subscription.gifted", "Gifted subs", "Someone gave subs to the community.", false,
			[]Field{tierField, {Name: "total", Label: "How many", Kind: "number", Default: "5", Max: 4}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"tier": atoi(v["tier"]), "total": atoi(v["total"]), "is_anonymous": false, "from": a.DisplayName}
			}),
		twitchKind("monetization.bits.cheered", "Cheer", "Someone cheered bits.", true,
			[]Field{{Name: "bits", Label: "Bits", Kind: "number", Default: "100", Max: 7},
				{Name: "text", Label: "Their message", Kind: "text", Default: "Cheer100 nice!", Max: 500}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"bits": atoi(v["bits"]), "text": v["text"], "is_anonymous": false, "from": a.DisplayName}
			}),
		twitchKind("audience.raid.received", "Raid", "Another channel raided yours.", false,
			[]Field{{Name: "viewers", Label: "Viewers", Kind: "number", Default: "25", Max: 7}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"viewers": atoi(v["viewers"])}
			}),
		twitchKind("rewards.channel_points.redeemed", "Channel point redemption", "Someone redeemed one of your channel point rewards.", true,
			[]Field{{Name: "reward", Label: "Reward", Kind: "text", Default: "Hydrate", Max: 45, Path: "reward.title"},
				{Name: "cost", Label: "Cost", Kind: "number", Default: "100", Max: 7},
				{Name: "user_input", Label: "What they typed", Kind: "text", Default: "", Max: 500}},
			func(a *Actor, v map[string]string) map[string]any {
				return map[string]any{"redemption_id": "test", "user_input": v["user_input"], "status": "unfulfilled",
					"reward": map[string]any{"id": "test", "title": v["reward"], "cost": atoi(v["cost"]), "prompt": ""}}
			}),
		pollKind("chat.poll.started", "Poll starts", "A poll started in your chat.", ""),
		pollKind("chat.poll.updated", "Poll votes", "Votes came in on your poll.", ""),
		pollKind("chat.poll.ended", "Poll ends", "Your poll ended.", "completed"),
		predictionKind("chat.prediction.started", "Prediction starts", "A prediction started in your chat.", "begin"),
		predictionKind("chat.prediction.updated", "Prediction bets", "Viewers bet on your prediction.", "progress"),
		predictionKind("chat.prediction.locked", "Prediction locks", "Your prediction stopped taking bets.", "lock"),
		predictionKind("chat.prediction.ended", "Prediction ends", "Your prediction ended: resolved with a winner, or canceled and refunded.", "end"),
		hypeKind("monetization.hype_train.started", "Hype Train starts", "A Hype Train started on your channel.", false),
		hypeKind("monetization.hype_train.updated", "Hype Train progress", "Your Hype Train got more contributions, or went up a level.", false),
		hypeKind("monetization.hype_train.ended", "Hype Train ends", "Your Hype Train ended.", true),
		// Giveaways (docs/adr/0025-giveaways.md).
		giveawayKind("giveaways.giveaway.opened", "Giveaway opens", "A giveaway opened for entries.", nil),
		giveawayKind("giveaways.giveaway.closed", "Giveaway entries close", "A giveaway stopped taking entries; the list is fixed.",
			func(p map[string]any, _ *Actor) { p["entries"], p["fingerprint"] = 12, "test" }),
		giveawayKind("giveaways.giveaway.ended", "Giveaway ends", "A giveaway ended, or was cancelled.",
			func(p map[string]any, a *Actor) {
				p["status"] = "ended"
				p["winners"] = []any{map[string]any{"user_id": a.ID, "login": a.Login, "name": a.DisplayName, "entry": 3}}
			}),
		giveawayWinnerKind("giveaways.winner.drawn", "Giveaway winner drawn", "A giveaway's winner was drawn."),
		giveawayWinnerKind("giveaways.winner.claimed", "Giveaway win claimed", "A giveaway's winner claimed in time."),
		giveawayWinnerKind("giveaways.winner.forfeited", "Giveaway win not claimed", "A giveaway's winner didn't claim in time."),
		// Wheels (docs/adr/0030-wheels.md): a spin started (no result), and
		// stopped with its result.
		{Type: "wheels.spin.started", Label: "Wheel spin starts", Description: "A wheel started spinning. Its result isn't known yet here.",
			Sources: []string{"core"}, Fields: []Field{{Name: "title", Label: "Wheel", Kind: "text", Default: "Next game", Max: 100}},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				return map[string]any{"spin_id": id, "wheel": "test", "title": v["title"], "started_by": "a test"}, nil, nil
			}},
		{Type: "wheels.spin.settled", Label: "Wheel stops", Description: "A wheel stopped, with its result.",
			Sources: []string{"core"}, Fields: []Field{{Name: "title", Label: "Wheel", Kind: "text", Default: "Next game", Max: 100},
				{Name: "label", Label: "Result", Kind: "text", Default: "Mystery pick", Max: 60}},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				return map[string]any{"spin_id": id, "wheel": "test", "title": v["title"], "started_by": "a test", "label": v["label"],
					"choice": 1, "display": "no_audience", "draw": 0, "total": 1}, nil, nil
			}},
		{Type: "overlays.timer.ended", Label: "Countdown ends", Description: "A countdown on a clock and timer overlay reached zero (D-092).",
			Sources: []string{"core"}, Fields: []Field{{Name: "label", Label: "Label", Kind: "text", Default: "Starting soon", Max: 60}},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				return map[string]any{"overlay": "test", "label": v["label"]}, nil, nil
			}},
		// Ads (docs/adr/0024-ads.md): asked for, refused, begun, ended (estimated).
		adKind("ads.break.started", "Ad break begins", "Twitch says an ad break began on your channel, automatic or one you ran.",
			[]string{"twitch"}, []Field{{Name: "duration", Label: "Length (seconds)", Kind: "number", Default: "90", Max: 3},
				{Name: "automatic", Label: "Automatic", Kind: "choice", Default: "no", Max: 3, Choices: []string{"no", "yes"}}},
			func(v map[string]string) map[string]any {
				return map[string]any{"duration_seconds": atoi(v["duration"]), "started_at": "2026-11-08T20:00:00Z",
					"is_automatic": v["automatic"] == "yes", "requester_id": "", "requester_login": ""}
			}),
		adKind("ads.break.end_estimated", "Ad break should be over",
			"The time an ad break should end: its start plus its length. An estimate: Twitch doesn't say when ads end.",
			[]string{"core"}, []Field{{Name: "duration", Label: "Length (seconds)", Kind: "number", Default: "90", Max: 3}},
			func(v map[string]string) map[string]any {
				return map[string]any{"duration_seconds": atoi(v["duration"]), "started_at": "2026-11-08T20:00:00Z", "estimated": true}
			}),
		adKind("ads.break.requested", "Ad break accepted", "Twitch accepted a request for an ad break. It isn't running until Twitch says it began.",
			[]string{"core"}, []Field{{Name: "length", Label: "Length (seconds)", Kind: "number", Default: "90", Max: 3}},
			func(v map[string]string) map[string]any {
				return map[string]any{"request_id": "test", "action": "commercial", "length": atoi(v["length"])}
			}),
		adKind("ads.request.refused", "Ad request refused", "Twitch refused an ad break or snooze, so nothing happened.",
			[]string{"core"}, []Field{{Name: "reason", Label: "Why", Kind: "choice", Default: "cooldown", Max: 12,
				Choices: []string{"cooldown", "not_live", "not_eligible", "no_snoozes", "refused"}, Path: "reason"}},
			func(v map[string]string) map[string]any {
				return map[string]any{"request_id": "test", "action": "commercial", "reason": v["reason"], "message": "a test refusal"}
			}),
		adKind("ads.request.unresolved", "Ad request unclear", "Twitch didn't answer an ad request clearly: it may or may not have started.",
			[]string{"core"}, nil, func(map[string]string) map[string]any {
				return map[string]any{"request_id": "test", "action": "commercial"}
			}),
		adKind("ads.snooze.applied", "Ad snoozed", "The next automatic ad was pushed back.", []string{"core"},
			[]Field{{Name: "snoozes", Label: "Snoozes left", Kind: "number", Default: "2", Max: 1}},
			func(v map[string]string) map[string]any {
				return map[string]any{"request_id": "test", "action": "snooze", "next_ad_at": "2026-11-08T20:35:00Z", "snooze_count": atoi(v["snoozes"])}
			}),
		{Type: "broadcast.channel.online", Label: "Live on Twitch", Description: "Your channel went live on Twitch.",
			Sources: []string{"twitch"}, Fields: []Field{},
			build: func(string, map[string]string) (any, *Actor, error) {
				return map[string]any{"stream_type": "live", "started_at": "2026-11-08T20:00:00Z"}, nil, nil
			}},
		{Type: "broadcast.channel.offline", Label: "Offline on Twitch", Description: "Your channel went offline on Twitch.",
			Sources: []string{"twitch"}, Fields: []Field{},
			build: func(string, map[string]string) (any, *Actor, error) { return map[string]any{}, nil, nil }},
		{Type: "broadcast.channel.updated", Label: "Title or category changed", Description: "Your channel's title or category changed on Twitch.",
			Sources: []string{"twitch"},
			Fields: []Field{{Name: "title", Label: "Title", Kind: "text", Default: "Speedrun night", Max: 140},
				{Name: "category", Label: "Category", Kind: "text", Default: "Celeste", Max: 140, Path: "category"}},
			build: func(_ string, v map[string]string) (any, *Actor, error) {
				return map[string]any{"title": v["title"], "category": v["category"], "category_id": "", "language": "en", "labels": []string{}}, nil, nil
			}},
		{
			Type: TimerFired, Label: "Timer went off",
			Description: "One of your timers went off, on its schedule or by Run now.",
			Sources:     []string{"timers"},
			Fields: []Field{
				{Name: "timer_id", Label: "Timer", Kind: "text", Default: "socials", Max: 64, Path: "timer_id"},
				{Name: "name", Label: "Timer's name", Kind: "text", Default: "Socials", Max: 100},
				{Name: "counter", Label: "Times it has gone off", Kind: "number", Default: "1", Max: 10},
			},
			build: func(_ string, v map[string]string) (any, *Actor, error) {
				n, _ := strconv.ParseInt(v["counter"], 10, 64)
				return map[string]any{"timer_id": v["timer_id"], "name": v["name"], "counter": n, "manual": false}, nil, nil
			},
		},
		{
			Type: ClipCreated, Label: "Clip made",
			Description: "StreamGremlin made a clip of your stream: !clip, a button or any workflow. Clips made on Twitch's site aren't seen.",
			Sources:     []string{"twitch"},
			Fields: []Field{
				{Name: "user", Label: "Who asked", Kind: "text", Default: "TestViewer", Max: 25},
				{Name: "title", Label: "Title", Kind: "text", Default: "What a jump", Max: 100},
			},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				clip := "TestClip" + id
				return map[string]any{"id": clip, "url": "https://clips.twitch.tv/" + clip, "edit_url": "https://clips.twitch.tv/" + clip + "/edit",
					"title": v["title"]}, testViewer(v["user"]), nil
			},
		},
		{
			Type: ButtonPressed, Label: "Button pressed",
			Description: "You pressed one of your buttons, on the Live screen or elsewhere.",
			Sources:     []string{"buttons"},
			Fields: []Field{
				{Name: "button_id", Label: "Button", Kind: "text", Default: "brb", Max: 64, Path: "button_id"},
				{Name: "label", Label: "Its label", Kind: "text", Default: "Be right back", Max: 40},
			},
			build: func(_ string, v map[string]string) (any, *Actor, error) {
				return map[string]any{"button_id": v["button_id"], "label": v["label"], "from": "live"}, nil, nil
			},
		},
		obsKind("broadcast.stream.started", "Stream started", "OBS started streaming.", nil,
			func(map[string]string) map[string]any { return map[string]any{} }),
		obsKind("broadcast.stream.stopped", "Stream stopped", "OBS stopped streaming.", nil,
			func(map[string]string) map[string]any { return map[string]any{} }),
		obsKind("broadcast.recording.started", "Recording started", "OBS started recording.", nil,
			func(map[string]string) map[string]any { return map[string]any{} }),
		obsKind("broadcast.recording.stopped", "Recording stopped", "OBS stopped recording and saved the file.",
			[]Field{{Name: "output_path", Label: "File", Kind: "text", Default: "/Users/you/Movies/recording.mkv", Max: 1024}},
			func(v map[string]string) map[string]any { return map[string]any{"output_path": v["output_path"]} }),
		obsKind("broadcast.replay.saved", "Replay saved", "OBS saved the replay buffer to a file.",
			[]Field{{Name: "path", Label: "File", Kind: "text", Default: "/Users/you/Movies/replay.mkv", Max: 1024}},
			func(v map[string]string) map[string]any { return map[string]any{"path": v["path"]} }),
	}
}

var tierField = Field{Name: "tier", Label: "Tier", Kind: "choice", Default: "1", Max: 1, Choices: []string{"1", "2", "3"}}

func atoi(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// twitchKind is a catalog entry for a Twitch channel event with a viewer
// (docs/adr/0015-twitch-channel-events.md).
func twitchKind(typ, label, desc string, sensitive bool, fields []Field, payload func(*Actor, map[string]string) map[string]any) Kind {
	fields = append([]Field{{Name: "user", Label: "Viewer", Kind: "text", Default: "TestViewer", Max: 25}}, fields...)
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"twitch"}, Sensitive: sensitive, Fields: fields,
		build: func(_ string, v map[string]string) (any, *Actor, error) {
			a := testViewer(v["user"])
			return payload(a, v), a, nil
		}}
}

// obsKind is a catalog entry for an event OBS reports.
func obsKind(typ, label, desc string, fields []Field, payload func(map[string]string) map[string]any) Kind {
	if fields == nil {
		fields = []Field{}
	}
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"obs"}, Fields: fields,
		build: func(_ string, v map[string]string) (any, *Actor, error) { return payload(v), nil, nil }}
}

// KindOf returns the catalog entry for typ.
func KindOf(typ string) (Kind, bool) {
	for _, k := range Catalog() {
		if k.Type == typ {
			return k, true
		}
	}
	return Kind{}, false
}

func testViewer(name string) *Actor {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "TestViewer"
	}
	login := strings.ToLower(name)
	return &Actor{Platform: "twitch", ID: "test-" + login, Login: login, DisplayName: name}
}

func chatter(a *Actor, role string) (map[string]any, error) {
	if role == "" {
		role = "viewer"
	}
	rank := -1
	for i, r := range Roles {
		if r == role {
			rank = i
		}
	}
	if rank < 0 {
		return nil, invalid("the role is viewer, subscriber, vip, moderator or broadcaster")
	}
	// The same shape as real Twitch chat (internal/twitch/chatmsg.go). A
	// test chatter has only the badge of their role.
	return map[string]any{
		"id": a.ID, "login": a.Login, "display_name": a.DisplayName, "badges": []any{}, "role": role,
		"is_broadcaster": role == "broadcaster", "is_moderator": role == "moderator", "is_vip": role == "vip",
		"is_subscriber": role == "subscriber",
	}, nil
}

// practiceChatter is a test chatter with what a practice giveaway's entry
// rules read: chatter.practice, and a subscriber's months in the badge's
// info, as Twitch sends it.
func practiceChatter(a *Actor, v map[string]string) (map[string]any, error) {
	ch, err := chatter(a, v["role"])
	if err != nil {
		return nil, err
	}
	num := func(name string, lo, hi int) (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(v[name]))
		if err != nil || n < lo || n > hi {
			return 0, invalid("%s is a whole number from %d to %d", name, lo, hi)
		}
		return n, nil
	}
	follow, err := num("follow_days", -1, 36500)
	if err != nil {
		return nil, err
	}
	account, err := num("account_days", 0, 36500)
	if err != nil {
		return nil, err
	}
	months, err := num("sub_months", 0, 1200)
	if err != nil {
		return nil, err
	}
	ch["practice"] = map[string]any{"follow_days": follow, "account_days": account}
	if ch["is_subscriber"] == true && months > 0 {
		ch["badges"] = []any{map[string]string{"set_id": "subscriber", "id": "0", "info": strconv.Itoa(months)}}
	}
	return ch, nil
}

// Simulation asks for a test event.
type Simulation struct {
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields"`
	// RequestID makes retries safe: the same id gives the same event.
	RequestID string `json:"request_id"`
}

// Build turns a Simulation into a synthetic event. Missing fields take
// their defaults; unknown fields and over-long values are refused.
func (s Simulation) Build() (Envelope, error) {
	k, ok := KindOf(s.Type)
	if !ok {
		return Envelope{}, invalid("there's no test event of type %q", s.Type)
	}
	if !validID(s.RequestID) || len(s.RequestID) > 64 {
		return Envelope{}, invalid("request_id is required: a short id without spaces, new for each test event")
	}
	values := map[string]string{}
	known := map[string]Field{}
	for _, f := range k.Fields {
		known[f.Name] = f
		values[f.Name] = f.Default
	}
	for name, v := range s.Fields {
		f, ok := known[name]
		if !ok {
			return Envelope{}, invalid("%s has no field %q", k.Label, name)
		}
		if len([]rune(v)) > f.Max {
			return Envelope{}, invalid("%s is longer than %d characters", f.Label, f.Max)
		}
		if f.Kind == "number" {
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return Envelope{}, invalid("%s must be a number", f.Label)
			}
		}
		values[name] = v
	}
	payload, actor, err := k.build("test-"+s.RequestID, values)
	if err != nil {
		return Envelope{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("events: encode test payload: %w", err)
	}
	return Envelope{
		Type:            k.Type,
		Source:          k.Sources[0],
		SourceEventID:   "test:" + s.RequestID,
		Actor:           actor,
		Payload:         raw,
		DeliveryQuality: Synthetic,
	}, nil
}

// Simulate stores a test event. Repeating the same RequestID returns the
// first event as a duplicate, whatever the fields say.
func (in *Inbox) Simulate(ctx context.Context, s Simulation) (Result, error) {
	env, err := s.Build()
	if err != nil {
		return Result{}, err
	}
	if rec, err := in.bySourceEventID(ctx, env.Source, env.SourceEventID); err == nil {
		return Result{Record: rec, Duplicate: true}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}
	res, err := in.Accept(ctx, env)
	if errors.Is(err, ErrConflict) {
		// The same request raced in twice; the first one won.
		if rec, lerr := in.bySourceEventID(ctx, env.Source, env.SourceEventID); lerr == nil {
			return Result{Record: rec, Duplicate: true}, nil
		}
	}
	return res, err
}

// hypeKind is a Hype Train event (docs/adr/0022-hype-trains.md): no one
// viewer; the top contributor is a test viewer.
func hypeKind(typ, label, desc string, ended bool) Kind {
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"twitch"},
		Fields: []Field{{Name: "level", Label: "Level", Kind: "number", Default: "2", Max: 3},
			{Name: "total", Label: "Total points", Kind: "number", Default: "2100", Max: 9},
			{Name: "user", Label: "Top contributor", Kind: "text", Default: "TestViewer", Max: 25}},
		build: func(_ string, v map[string]string) (any, *Actor, error) {
			a := testViewer(v["user"])
			top := map[string]any{"user_id": a.ID, "login": a.Login, "name": a.DisplayName, "type": "bits", "total": atoi(v["total"]) / 2}
			p := map[string]any{"hype_train_id": "test", "level": atoi(v["level"]), "total": atoi(v["total"]), "train_type": "regular",
				"is_shared": false, "started_at": "2026-11-08T20:00:00Z", "top_contributions": []any{top}, "top_contributor": top}
			if ended {
				p["ended_at"], p["cooldown_ends_at"] = "2026-11-08T20:05:00Z", "2026-11-08T21:05:00Z"
			} else {
				p["progress"], p["goal"], p["expires_at"] = 300, 2000, "2026-11-08T20:10:00Z"
			}
			return p, nil, nil
		}}
}

// giveawayKind is a giveaway event with no one viewer.
func giveawayKind(typ, label, desc string, more func(map[string]any, *Actor)) Kind {
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"core"},
		Fields: []Field{{Name: "title", Label: "Giveaway", Kind: "text", Default: "a Steam key", Max: 100}},
		build: func(_ string, v map[string]string) (any, *Actor, error) {
			p := map[string]any{"giveaway_id": "test", "title": v["title"], "command": "join", "winners": 1, "practice": true,
				"subscribers_only": false}
			if more != nil {
				more(p, testViewer("TestViewer"))
			}
			return p, nil, nil
		}}
}

// giveawayWinnerKind is a giveaway event about its winner.
func giveawayWinnerKind(typ, label, desc string) Kind {
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"core"},
		Fields: []Field{{Name: "title", Label: "Giveaway", Kind: "text", Default: "a Steam key", Max: 100},
			{Name: "user", Label: "Winner", Kind: "text", Default: "TestViewer", Max: 25}},
		build: func(_ string, v map[string]string) (any, *Actor, error) {
			a := testViewer(v["user"])
			return map[string]any{"giveaway_id": "test", "title": v["title"], "command": "join", "winners": 1, "practice": true,
				"subscribers_only": false, "draw": 1, "pick_index": 2, "candidates": 12, "claim_until": nil,
				"winner": map[string]any{"user_id": a.ID, "login": a.Login, "name": a.DisplayName, "entry": 3}}, a, nil
		}}
}

// adKind is an ad event: no viewer.
func adKind(typ, label, desc string, sources []string, fields []Field, payload func(map[string]string) map[string]any) Kind {
	if fields == nil {
		fields = []Field{}
	}
	return Kind{Type: typ, Label: label, Description: desc, Sources: sources, Fields: fields,
		build: func(_ string, v map[string]string) (any, *Actor, error) { return payload(v), nil, nil }}
}

// pollKind is a poll event (docs/adr/0023-polls-and-predictions.md).
func pollKind(typ, label, desc, status string) Kind {
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"twitch"},
		Fields: []Field{{Name: "title", Label: "Question", Kind: "text", Default: "What should I play next?", Max: 60},
			{Name: "first", Label: "First choice", Kind: "text", Default: "Celeste", Max: 25},
			{Name: "second", Label: "Second choice", Kind: "text", Default: "Hades", Max: 25}},
		build: func(_ string, v map[string]string) (any, *Actor, error) {
			choices := []any{map[string]any{"id": "c1", "title": v["first"], "votes": 3, "channel_points_votes": 0},
				map[string]any{"id": "c2", "title": v["second"], "votes": 5, "channel_points_votes": 0}}
			p := map[string]any{"poll_id": "test", "title": v["title"], "choices": choices, "votes": 8, "leader": v["second"], "tied": false,
				"channel_points_voting": false, "channel_points_per_vote": 0, "started_at": "2026-11-08T20:00:00Z"}
			if status != "" {
				p["status"], p["ended_at"] = status, "2026-11-08T20:01:00Z"
			} else {
				p["ends_at"] = "2026-11-08T20:01:00Z"
			}
			return p, nil, nil
		}}
}

// predictionKind is a prediction event.
func predictionKind(typ, label, desc, phase string) Kind {
	return Kind{Type: typ, Label: label, Description: desc, Sources: []string{"twitch"},
		Fields: []Field{{Name: "title", Label: "Question", Kind: "text", Default: "Will I beat this boss?", Max: 45},
			{Name: "first", Label: "First outcome", Kind: "text", Default: "Yes", Max: 25},
			{Name: "second", Label: "Second outcome", Kind: "text", Default: "No", Max: 25}},
		build: func(_ string, v map[string]string) (any, *Actor, error) {
			outcomes := []any{
				map[string]any{"id": "o1", "title": v["first"], "color": "blue", "users": 4, "channel_points": 1200, "top_predictors": []any{}},
				map[string]any{"id": "o2", "title": v["second"], "color": "pink", "users": 2, "channel_points": 800, "top_predictors": []any{}}}
			p := map[string]any{"prediction_id": "test", "title": v["title"], "outcomes": outcomes, "started_at": "2026-11-08T20:00:00Z"}
			switch phase {
			case "end":
				p["status"], p["ended_at"], p["winning_outcome_id"], p["winner"] = "resolved", "2026-11-08T20:05:00Z", "o1", v["first"]
			case "lock":
				p["locked_at"] = "2026-11-08T20:02:00Z"
			default:
				p["locks_at"] = "2026-11-08T20:02:00Z"
			}
			return p, nil, nil
		}}
}

// SourceYouTube is the source of YouTube's events (M6, D-095).
const SourceYouTube = "youtube"

// LogOnly reports whether r is a YouTube event that only the event log
// reads for now. M6 brings YouTube in slice by slice (D-097): until
// commands, workflows, filters, points, giveaways, first words and viewers
// learn YouTube, each skips its events rather than treat a YouTube viewer
// as a Twitch one.
func LogOnly(r Record) bool { return r.Source == SourceYouTube }
