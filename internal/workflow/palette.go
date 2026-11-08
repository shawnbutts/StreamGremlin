package workflow

// The palette describes, in plain words, what the workflow editor offers
// (docs/adr/0010-editor-run-delete.md). The engine never reads it; tests
// keep it matched to the event catalog and the action registry.

// MatchField is one thing a trigger can be limited to: a path in the
// event's payload, shown as a control.
type MatchField struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	// Kind: command (a chat command, without "!"), who (a chatter.role
	// list), text, scene, source, on_off or timer (a timer's id).
	Kind string `json:"kind"`
	Help string `json:"help,omitempty"`
}

// Value is something a message can include, shown as {token} and stored
// as ${expr}.
type Value struct {
	Token string `json:"token"`
	Label string `json:"label"`
	Expr  string `json:"expr"`
}

// TriggerEntry is one event type as the editor offers it.
type TriggerEntry struct {
	Type     string       `json:"type"`
	Label    string       `json:"label"`
	Category string       `json:"category"`
	Help     string       `json:"help"`
	More     bool         `json:"more,omitempty"` // behind "More triggers…"
	Match    []MatchField `json:"match"`
	Values   []Value      `json:"values"`
}

// Palette is the body of GET /v1/palette.
type Palette struct {
	Triggers []TriggerEntry `json:"triggers"`
	Actions  []*Action      `json:"actions"`
}

// Palette returns what the editor offers in this build.
func (e *Engine) Palette() Palette { return Palette{Triggers: Triggers(), Actions: e.reg.List()} }

var (
	who     = MatchField{Path: "chatter.role", Label: "Who can use it", Kind: "who"}
	viewer  = Value{Token: "viewer", Label: "Their name", Expr: "event.actor.display_name"}
	login   = Value{Token: "login", Label: "Their login (for @mentions)", Expr: "event.actor.login"}
	role    = Value{Token: "role", Label: "Their role", Expr: "event.payload.chatter.role"}
	message = Value{Token: "message", Label: "The whole message", Expr: "event.payload.text"}
	source  = MatchField{Path: "source_name", Label: "Only for source", Kind: "source", Help: "Leave empty for any source."}
	srcVal  = Value{Token: "source", Label: "The source", Expr: "event.payload.source_name"}
)

// Triggers lists the events the editor offers, most used first.
func Triggers() []TriggerEntry {
	return []TriggerEntry{
		{Type: "chat.command.received", Label: "A viewer types a command", Category: "Chat",
			Help:  "Someone types !something in your chat.",
			Match: []MatchField{{Path: "command", Label: "Command", Kind: "command", Help: "Without the !, like hello for !hello."}, who},
			Values: []Value{viewer, login,
				{Token: "args", Label: "What they typed after the command", Expr: "event.payload.args"},
				{Token: "first_word", Label: "The first word after the command", Expr: `event.payload.arg_list[?0].orValue("")`},
				message, role}},
		{Type: "chat.message.received", Label: "Someone chats", Category: "Chat",
			Help:   "Any message in your chat, commands included.",
			Match:  []MatchField{who},
			Values: []Value{viewer, login, message, role}},
		{Type: "broadcast.scene.changed", Label: "OBS switches scene", Category: "OBS",
			Help:  "OBS's live scene changes, whoever changed it.",
			Match: []MatchField{{Path: "scene_name", Label: "Only for scene", Kind: "scene", Help: "Leave empty for any scene."}},
			Values: []Value{{Token: "scene", Label: "The new scene", Expr: "event.payload.scene_name"},
				{Token: "previous_scene", Label: "The scene before", Expr: "event.payload.previous_scene_name"}}},
		{Type: "timers.timer.fired", Label: "A timer goes off", Category: "Timers",
			Help:  "One of your timers goes off: every so often, on a schedule, or when you click Run now.",
			Match: []MatchField{{Path: "timer_id", Label: "Timer", Kind: "timer"}},
			Values: []Value{{Token: "timer", Label: "The timer's name", Expr: "event.payload.name"},
				{Token: "counter", Label: "How many times it has gone off", Expr: "event.payload.counter"}}},
		{Type: "broadcast.stream.started", Label: "The stream starts", Category: "Stream", Help: "OBS starts streaming.",
			Match: []MatchField{}, Values: []Value{}},
		{Type: "broadcast.stream.stopped", Label: "The stream stops", Category: "Stream", Help: "OBS stops streaming.",
			Match: []MatchField{}, Values: []Value{}},
		{Type: "broadcast.scene_item.visibility_changed", Label: "A source is shown or hidden", Category: "OBS", More: true,
			Help: "A source in a scene is shown or hidden, by anyone.",
			Match: []MatchField{{Path: "scene_name", Label: "Only in scene", Kind: "scene", Help: "Leave empty for any scene."}, source,
				{Path: "visible", Label: "Only when", Kind: "on_off", Help: "On: shown. Off: hidden."}},
			Values: []Value{{Token: "scene", Label: "The scene", Expr: "event.payload.scene_name"}, srcVal}},
		{Type: "broadcast.filter.enabled_changed", Label: "A filter is turned on or off", Category: "OBS", More: true,
			Help: "A filter on a source or scene is turned on or off, by anyone.",
			Match: []MatchField{source, {Path: "filter_name", Label: "Only for filter", Kind: "text", Help: "Leave empty for any filter."},
				{Path: "enabled", Label: "Only when", Kind: "on_off"}},
			Values: []Value{srcVal, {Token: "filter", Label: "The filter", Expr: "event.payload.filter_name"}}},
		{Type: "broadcast.audio.mute_changed", Label: "Audio is muted or unmuted", Category: "OBS", More: true,
			Help:   "An audio source is muted or unmuted, by anyone.",
			Match:  []MatchField{source, {Path: "muted", Label: "Only when", Kind: "on_off", Help: "On: muted. Off: unmuted."}},
			Values: []Value{srcVal}},
		{Type: "broadcast.media.started", Label: "A media source starts playing", Category: "OBS", More: true,
			Help: "A video or audio file in OBS starts.", Match: []MatchField{source}, Values: []Value{srcVal}},
		{Type: "broadcast.media.ended", Label: "A media source finishes", Category: "OBS", More: true,
			Help: "A video or audio file in OBS reaches its end.", Match: []MatchField{source}, Values: []Value{srcVal}},
		{Type: "broadcast.recording.started", Label: "Recording starts", Category: "Stream", More: true,
			Help: "OBS starts recording.", Match: []MatchField{}, Values: []Value{}},
		{Type: "broadcast.recording.stopped", Label: "Recording stops", Category: "Stream", More: true,
			Help: "OBS stops recording and saves the file.", Match: []MatchField{},
			Values: []Value{{Token: "file", Label: "The recording's file", Expr: "event.payload.output_path"}}},
		{Type: "broadcast.replay.saved", Label: "The replay buffer is saved", Category: "Stream", More: true,
			Help: "OBS saves the replay buffer to a file.", Match: []MatchField{},
			Values: []Value{{Token: "file", Label: "The replay's file", Expr: "event.payload.path"}}},
	}
}
