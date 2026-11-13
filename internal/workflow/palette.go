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
	// list), text, scene, source, on_off, timer (a timer's id) or button
	// (a button's id).
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
	// AddOn is the add-on whose event it is (D-073).
	AddOn string `json:"add_on,omitempty"`
}

var (
	gwTitle  = Value{Token: "giveaway", Label: "The giveaway's title", Expr: "event.payload.title"}
	gwWinner = Value{Token: "winner", Label: "The winner's name", Expr: "event.payload.winner.name"}
	// Wheels (docs/adr/0030-wheels.md).
	wheelTitle = Value{Token: "wheel", Label: "The wheel's title", Expr: "event.payload.title"}
)

var adSeconds = Value{Token: "seconds", Label: "Its length in seconds", Expr: "event.payload.duration_seconds"}

// Palette is the body of GET /v1/palette.
type Palette struct {
	Triggers []TriggerEntry `json:"triggers"`
	Actions  []*Action      `json:"actions"`
}

// Palette returns what the editor offers in this build.
func (e *Engine) Palette() Palette {
	return Palette{Triggers: append(Triggers(), e.reg.AddOnTriggers()...), Actions: e.reg.List()}
}

var (
	who     = MatchField{Path: "chatter.role", Label: "Who can use it", Kind: "who"}
	viewer  = Value{Token: "viewer", Label: "Their name", Expr: "event.actor.display_name"}
	login   = Value{Token: "login", Label: "Their login (for @mentions)", Expr: "event.actor.login"}
	role    = Value{Token: "role", Label: "Their role", Expr: "event.payload.chatter.role"}
	message = Value{Token: "message", Label: "The whole message", Expr: "event.payload.text"}
	source  = MatchField{Path: "source_name", Label: "Only for source", Kind: "source", Help: "Leave empty for any source."}
	srcVal  = Value{Token: "source", Label: "The source", Expr: "event.payload.source_name"}
	tier    = Value{Token: "tier", Label: "The sub tier (1, 2 or 3)", Expr: "event.payload.tier"}
	// from names a gifter or cheerer, or says Anonymous.
	from = Value{Token: "viewer", Label: "Their name (or Anonymous)", Expr: "event.payload.from"}
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
		{Type: "chat.first_words.received", Label: "A viewer chats for the first time this stream", Category: "Chat",
			Help: "Their first message since you went live (OBS or Twitch), since First Words was reset, or after 12 quiet hours. " +
				"Your own messages never count.",
			Match:  []MatchField{who},
			Values: []Value{viewer, login, message, role}},
		{Type: "chat.filter.caught", Label: "A chat filter catches a message", Category: "Chat", More: true,
			Help: "A chat filter on the Chat filters screen caught a viewer's message and deleted it, timed them out or warned them. " +
				"Test messages make test events, and nothing is sent to Twitch.",
			Match: []MatchField{{Path: "rule", Label: "Only for the filter", Kind: "text",
				Help: "words, links, emotes, caps, symbols or repeats. Leave empty for any."}},
			Values: []Value{viewer, login, {Token: "message", Label: "Their message", Expr: "event.payload.text"},
				{Token: "filter", Label: "Which filter (words, links, emotes, caps, symbols or repeats)", Expr: "event.payload.rule"},
				{Token: "action", Label: "What it did (delete, timeout or warn)", Expr: "event.payload.action"},
				{Token: "state", Label: "Twitch's answer (done, refused, unknown, or test)", Expr: "event.payload.state"}}},
		{Type: "chat.poll.started", Label: "A poll starts", Category: "Twitch", More: true,
			Help: "A poll starts in your chat, from StreamGremlin or Twitch (Affiliates and Partners).", Match: []MatchField{}, Values: pollValues(false)},
		{Type: "chat.poll.updated", Label: "Votes come in on a poll", Category: "Twitch", More: true,
			Help: "Votes on the running poll. Often many times a poll.", Match: []MatchField{}, Values: pollValues(false)},
		{Type: "chat.poll.ended", Label: "A poll ends", Category: "Twitch",
			Help: "A poll ends, with the votes for each choice.", Match: []MatchField{}, Values: pollValues(true)},
		{Type: "chat.prediction.started", Label: "A prediction starts", Category: "Twitch", More: true,
			Help: "A prediction opens for bets (Affiliates and Partners).", Match: []MatchField{}, Values: predictionValues(false)},
		{Type: "chat.prediction.updated", Label: "Bets come in on a prediction", Category: "Twitch", More: true,
			Help: "Bets on the running prediction.", Match: []MatchField{}, Values: predictionValues(false)},
		{Type: "chat.prediction.locked", Label: "A prediction locks", Category: "Twitch", More: true,
			Help: "The prediction stops taking bets; the result comes later.", Match: []MatchField{}, Values: predictionValues(false)},
		{Type: "chat.prediction.ended", Label: "A prediction ends", Category: "Twitch",
			Help: "Resolved with a winner, or canceled and refunded.", Match: []MatchField{}, Values: predictionValues(true)},
		{Type: "monetization.hype_train.started", Label: "A Hype Train starts", Category: "Twitch",
			Help: "A Hype Train starts on your channel (Affiliates and Partners).", Match: []MatchField{}, Values: hypeValues(false)},
		{Type: "monetization.hype_train.updated", Label: "The Hype Train moves", Category: "Twitch", More: true,
			Help:  "More contributions to the Hype Train, including going up a level. Often many times a train.",
			Match: []MatchField{}, Values: hypeValues(false)},
		{Type: "monetization.hype_train.ended", Label: "A Hype Train ends", Category: "Twitch",
			Help: "The Hype Train ended, with the level it reached.", Match: []MatchField{}, Values: hypeValues(true)},
		{Type: "giveaways.giveaway.opened", Label: "A giveaway opens", Category: "Giveaways",
			Help: "A giveaway opened for entries. StreamGremlin already says how to enter in chat.", Match: []MatchField{},
			Values: []Value{gwTitle, {Token: "command", Label: "The entry command, without !", Expr: "event.payload.command"}}},
		{Type: "giveaways.giveaway.closed", Label: "Giveaway entries close", Category: "Giveaways", More: true,
			Help: "A giveaway stopped taking entries.", Match: []MatchField{},
			Values: []Value{gwTitle, {Token: "entries", Label: "How many entered", Expr: "event.payload.entries"}}},
		{Type: "giveaways.winner.drawn", Label: "A giveaway winner is drawn", Category: "Giveaways",
			Help:  "A winner was drawn. StreamGremlin already announces them in chat; use this for an alert or a sound.",
			Match: []MatchField{}, Values: []Value{gwTitle, gwWinner}},
		{Type: "giveaways.winner.claimed", Label: "A giveaway winner claims", Category: "Giveaways", More: true,
			Help: "A winner typed the claim command in time.", Match: []MatchField{}, Values: []Value{gwTitle, gwWinner}},
		{Type: "giveaways.winner.forfeited", Label: "A giveaway winner doesn't claim", Category: "Giveaways", More: true,
			Help: "A winner didn't claim in time.", Match: []MatchField{}, Values: []Value{gwTitle, gwWinner}},
		{Type: "giveaways.giveaway.ended", Label: "A giveaway ends", Category: "Giveaways", More: true,
			Help:   "A giveaway ended, or was cancelled.",
			Match:  []MatchField{{Path: "status", Label: "Only when it", Kind: "text", Help: "ended or cancelled. Leave empty for either."}},
			Values: []Value{gwTitle}},
		{Type: "wheels.spin.settled", Label: "A wheel stops", Category: "Wheels",
			Help:   "A wheel stopped on its result, as the overlay shows it. Reply in chat, or switch a scene.",
			Match:  []MatchField{{Path: "wheel", Label: "Only for the wheel", Kind: "text", Help: "its name, like next-game. Leave empty for any."}},
			Values: []Value{wheelTitle, {Token: "result", Label: "What it landed on", Expr: "event.payload.label"}}},
		{Type: "wheels.spin.started", Label: "A wheel starts spinning", Category: "Wheels", More: true,
			Help:   "A wheel started spinning. Its result isn't known yet: use \"A wheel stops\" for that.",
			Match:  []MatchField{{Path: "wheel", Label: "Only for the wheel", Kind: "text", Help: "its name, like next-game. Leave empty for any."}},
			Values: []Value{wheelTitle}},
		{Type: "ads.break.started", Label: "An ad break begins", Category: "Twitch",
			Help:  "Twitch says an ad break began, automatic or one you ran (Affiliates and Partners). The only sure sign an ad is running.",
			Match: []MatchField{}, Values: []Value{adSeconds, {Token: "automatic", Label: "Whether Twitch ran it on its own (true or false)",
				Expr: "event.payload.is_automatic"}}},
		{Type: "ads.break.end_estimated", Label: "An ad break should be over", Category: "Twitch",
			Help: "When an ad break should end: its start plus its length. An estimate, because Twitch doesn't say when ads end; " +
				"some viewers may still see one.", Match: []MatchField{}, Values: []Value{adSeconds}},
		{Type: "ads.break.requested", Label: "Twitch accepts an ad request", Category: "Twitch", More: true,
			Help:  "Twitch accepted a request for an ad break. It isn't running yet: use “An ad break begins” to announce one.",
			Match: []MatchField{}, Values: []Value{{Token: "seconds", Label: "Its length in seconds", Expr: "event.payload.length"}}},
		{Type: "ads.request.refused", Label: "Twitch refuses an ad request", Category: "Twitch", More: true,
			Help:   "Twitch refused an ad break or snooze, so nothing happened.",
			Match:  []MatchField{{Path: "reason", Label: "Only for this reason", Kind: "text", Help: "cooldown, not_live, not_eligible, no_snoozes or refused. Leave empty for any."}},
			Values: []Value{{Token: "why", Label: "Why, in Twitch's words", Expr: "event.payload.message"}}},
		{Type: "ads.request.unresolved", Label: "An ad request's outcome is unclear", Category: "Twitch", More: true,
			Help:  "Twitch didn't answer an ad request clearly: it may or may not have started. It's never sent again.",
			Match: []MatchField{}, Values: []Value{}},
		{Type: "ads.snooze.applied", Label: "The next ad is snoozed", Category: "Twitch", More: true,
			Help: "The next automatic ad was pushed back.", Match: []MatchField{},
			Values: []Value{{Token: "snoozes_left", Label: "Snoozes left", Expr: "event.payload.snooze_count"}}},
		{Type: "broadcast.scene.changed", Label: "OBS switches scene", Category: "OBS",
			Help:  "OBS's live scene changes, whoever changed it.",
			Match: []MatchField{{Path: "scene_name", Label: "Only for scene", Kind: "scene", Help: "Leave empty for any scene."}},
			Values: []Value{{Token: "scene", Label: "The new scene", Expr: "event.payload.scene_name"},
				{Token: "previous_scene", Label: "The scene before", Expr: "event.payload.previous_scene_name"}}},
		{Type: "moderation.suspicious_user.message", Label: "A suspicious user chats", Category: "Twitch",
			Help: "Twitch flags a chatter's message as from a suspicious user: a likely or possible ban evader, someone banned in a " +
				"channel you share bans with, or someone you or a mod marked. A restricted user's message only reaches you and your mods.",
			Match: []MatchField{{Path: "ban_evasion", Label: "Only when Twitch thinks they're a ban evader", Kind: "text",
				Help: "likely or possible. Leave empty for any suspicious user."}},
			Values: []Value{viewer, login, {Token: "message", Label: "Their message", Expr: "event.payload.text"},
				{Token: "treatment", Label: "How Twitch treats them (none, active_monitoring or restricted)", Expr: "event.payload.low_trust_status"},
				{Token: "ban_evasion", Label: "Ban evader: likely, possible, unknown, or empty", Expr: "event.payload.ban_evasion"}}},
		{Type: "moderation.suspicious_user.updated", Label: "A suspicious user's treatment changes", Category: "Twitch", More: true,
			Help:  "You or a mod restrict or monitor a suspicious user, or stop.",
			Match: []MatchField{}, Values: []Value{viewer, login,
				{Token: "treatment", Label: "How Twitch treats them now", Expr: "event.payload.low_trust_status"}}},
		{Type: "audience.follow.created", Label: "Someone follows", Category: "Twitch",
			Help: "A viewer follows your channel.", Match: []MatchField{}, Values: []Value{viewer, login}},
		{Type: "monetization.subscription.started", Label: "Someone subscribes", Category: "Twitch",
			Help:  "A new sub, including someone given a sub as a gift. Resubs are separate.",
			Match: []MatchField{}, Values: []Value{viewer, login, tier,
				{Token: "gift", Label: "Whether it was a gift (true or false)", Expr: "event.payload.is_gift"}}},
		{Type: "monetization.subscription.renewed", Label: "Someone resubscribes", Category: "Twitch",
			Help:  "A viewer shares their resub in chat, with how many months.",
			Match: []MatchField{}, Values: []Value{viewer, login, tier,
				{Token: "months", Label: "Months subscribed in all", Expr: "event.payload.months"},
				{Token: "sub_message", Label: "Their resub message", Expr: "event.payload.text"}}},
		{Type: "monetization.subscription.gifted", Label: "Someone gifts subs", Category: "Twitch",
			Help:  "A viewer, or someone anonymous, gives subs to the community. Each sub also arrives as \"Someone subscribes\".",
			Match: []MatchField{}, Values: []Value{from, tier,
				{Token: "count", Label: "How many subs", Expr: "event.payload.total"}}},
		{Type: "monetization.bits.cheered", Label: "Someone cheers bits", Category: "Twitch",
			Help:  "A viewer, or someone anonymous, cheers bits.",
			Match: []MatchField{}, Values: []Value{from,
				{Token: "bits", Label: "How many bits", Expr: "event.payload.bits"},
				{Token: "cheer_message", Label: "Their message", Expr: "event.payload.text"}}},
		{Type: "audience.raid.received", Label: "Someone raids you", Category: "Twitch",
			Help:  "Another channel raids yours.",
			Match: []MatchField{}, Values: []Value{{Token: "raider", Label: "The raiding channel", Expr: "event.actor.display_name"},
				{Token: "raider_login", Label: "The raiding channel's login (for !so)", Expr: "event.actor.login"},
				{Token: "viewers", Label: "How many viewers came", Expr: "event.payload.viewers"}}},
		{Type: "rewards.channel_points.redeemed", Label: "Someone redeems channel points", Category: "Twitch",
			Help:  "A viewer redeems one of your channel point rewards.",
			Match: []MatchField{{Path: "reward.title", Label: "Only for reward", Kind: "text", Help: "The reward's title, exactly. Leave empty for any reward."}},
			Values: []Value{viewer, login, {Token: "reward", Label: "The reward's title", Expr: "event.payload.reward.title"},
				{Token: "cost", Label: "What it cost", Expr: "event.payload.reward.cost"},
				{Token: "input", Label: "What they typed", Expr: "event.payload.user_input"}}},
		{Type: "broadcast.channel.online", Label: "You go live on Twitch", Category: "Stream",
			Help:  "Twitch says your channel is live. (\"The stream starts\" is OBS starting to stream, a little earlier.)",
			Match: []MatchField{}, Values: []Value{}},
		{Type: "broadcast.channel.offline", Label: "You go offline on Twitch", Category: "Stream",
			Help: "Twitch says your channel is offline.", Match: []MatchField{}, Values: []Value{}},
		{Type: "broadcast.channel.updated", Label: "Your title or category changes", Category: "Stream", More: true,
			Help:  "Your Twitch title or category changes, from anywhere.",
			Match: []MatchField{{Path: "category", Label: "Only for category", Kind: "text", Help: "Leave empty for any category."}},
			Values: []Value{{Token: "title", Label: "The title", Expr: "event.payload.title"},
				{Token: "category", Label: "The category", Expr: "event.payload.category"}}},
		{Type: "timers.timer.fired", Label: "A timer goes off", Category: "Timers",
			Help:  "One of your timers goes off: every so often, on a schedule, or when you click Run now.",
			Match: []MatchField{{Path: "timer_id", Label: "Timer", Kind: "timer"}},
			Values: []Value{{Token: "timer", Label: "The timer's name", Expr: "event.payload.name"},
				{Token: "counter", Label: "How many times it has gone off", Expr: "event.payload.counter"}}},
		{Type: "buttons.button.pressed", Label: "You press a button", Category: "Buttons",
			Help:   "You press one of your buttons: on the Live screen, or later on a Stream Deck. No viewer is behind it.",
			Match:  []MatchField{{Path: "button_id", Label: "Button", Kind: "button"}},
			Values: []Value{{Token: "button", Label: "The button's label", Expr: "event.payload.label"}}},
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

// hypeValues are a Hype Train's values (docs/adr/0022-hype-trains.md).
func hypeValues(ended bool) []Value {
	v := []Value{
		{Token: "level", Label: "The level", Expr: "event.payload.level"},
		{Token: "total", Label: "Total points", Expr: "event.payload.total"},
		{Token: "top_contributor", Label: "The top contributor's name", Expr: `event.payload.?top_contributor.name.orValue("")`},
	}
	if !ended {
		v = append(v, Value{Token: "goal", Label: "Points for the next level", Expr: "event.payload.goal"},
			Value{Token: "progress", Label: "Points toward the next level so far", Expr: "event.payload.progress"})
	}
	return v
}

// pollValues are a poll's values (docs/adr/0023-polls-and-predictions.md).
func pollValues(ended bool) []Value {
	v := []Value{
		{Token: "question", Label: "The question", Expr: "event.payload.title"},
		{Token: "leader", Label: "The choice with the most votes (empty when tied)", Expr: "event.payload.leader"},
		{Token: "votes", Label: "Votes in all", Expr: "event.payload.votes"},
	}
	if ended {
		v = append(v, Value{Token: "poll_status", Label: "How it ended: completed, terminated or archived", Expr: "event.payload.status"})
	}
	return v
}

// predictionValues are a prediction's values.
func predictionValues(ended bool) []Value {
	v := []Value{{Token: "question", Label: "The question", Expr: "event.payload.title"}}
	if ended {
		v = append(v, Value{Token: "winner", Label: "The winning outcome (empty when canceled)", Expr: "event.payload.winner"},
			Value{Token: "prediction_status", Label: "How it ended: resolved or canceled", Expr: "event.payload.status"})
	}
	return v
}
