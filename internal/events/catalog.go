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

// Catalog lists the types the engine knows in this build. Slices add their
// types here as they add the adapters that send them (docs/adr/0004-events.md).
// Payload shapes aren't frozen until the slice that sends the real event.
func Catalog() []Kind {
	return []Kind{
		{
			Type: "chat.message.received", Label: "Chat message",
			Description: "A viewer sent a message in chat.",
			Sources:     []string{"twitch"}, Sensitive: true,
			Fields: []Field{
				{Name: "user", Label: "Viewer", Kind: "text", Default: "TestViewer", Max: 25},
				{Name: "text", Label: "Message", Kind: "text", Default: "Hello from a test event!", Max: 500},
				roleField,
			},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				a := testViewer(v["user"])
				ch, err := chatter(a, v["role"])
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
			Fields: []Field{
				{Name: "user", Label: "Viewer", Kind: "text", Default: "TestViewer", Max: 25},
				{Name: "command", Label: "Command", Kind: "text", Default: "!hello", Max: 50},
				{Name: "args", Label: "After the command", Kind: "text", Default: "", Max: 450},
				roleField,
			},
			build: func(id string, v map[string]string) (any, *Actor, error) {
				cmd := strings.TrimSpace(v["command"])
				if !strings.HasPrefix(cmd, "!") || len(cmd) < 2 || strings.ContainsAny(cmd, " \t") {
					return nil, nil, invalid("a command starts with ! and has no spaces, like !hello")
				}
				a := testViewer(v["user"])
				ch, err := chatter(a, v["role"])
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
