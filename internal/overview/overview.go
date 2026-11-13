// Package overview works out what Home shows (D-018 in docs/DECISIONS.md): the
// Get set up checklist, ticked from real state and never from what the
// streamer clicked, and what needs the streamer now. It's a plain function
// of the services' states, so it's tested without them.
package overview

import (
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Screens a step or item opens.
const (
	ScreenConnections = "connections"
	ScreenWorkflows   = "workflows"
)

// Step is one Get set up step.
type Step struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Help     string `json:"help"`
	Done     bool   `json:"done"`
	Optional bool   `json:"optional,omitempty"`
	Screen   string `json:"screen"`
}

// Item levels: problem (something is wrong), unknown (it can't be checked
// right now) and tip (it works, but could work better).
const (
	Problem = "problem"
	Unknown = "unknown"
	Tip     = "tip"
)

// Item is one thing that needs the streamer.
type Item struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Screen  string `json:"screen"`
	// Action is something Home can do about it in place:
	// "mark_failures_seen".
	Action string `json:"action,omitempty"`
}

// ActionMarkFailuresSeen marks the failed runs counted so far as seen.
const ActionMarkFailuresSeen = "mark_failures_seen"

// Overview is the body of GET /v1/overview.
type Overview struct {
	Checklist []Step         `json:"checklist"`
	SetUp     bool           `json:"set_up"` // every step that isn't optional is done
	Attention []Item         `json:"attention"`
	Workflows workflow.Stats `json:"workflows"`
}

// Input is what Build reads.
type Input struct {
	Twitch    twitch.Status
	OBS       obs.Status
	Workflows workflow.Stats
	Commands  []commands.Entry
}

// signedIn: a saved sign-in, confirmed or not checkable right now.
func signedIn(a twitch.AccountStatus) bool {
	return a.State == twitch.StateConnected || a.State == twitch.StateUnknown
}

func detail(s string) string {
	if s == "" {
		return ""
	}
	return ": " + strings.TrimSuffix(s, ".")
}

// Build works out the checklist and what needs the streamer.
func Build(in Input) Overview {
	tw, ob, wf := in.Twitch, in.OBS, in.Workflows
	twitchHelp := "Lets StreamGremlin read your chat and answer in it. You enter a code at twitch.tv/activate."
	if tw.ClientIDSource == twitch.ClientIDNone {
		twitchHelp = "First paste your Twitch app's Client ID on Connections; then sign in with a code at twitch.tv/activate."
	}
	steps := []Step{
		{ID: "twitch", Label: "Sign in with your Twitch account", Help: twitchHelp, Done: signedIn(tw.Streamer), Screen: ScreenConnections},
		{ID: "bot", Label: "Add a bot account", Optional: true, Done: signedIn(tw.Bot), Screen: ScreenConnections,
			Help: "So answers come from a bot's name instead of yours. Skip it to answer as yourself."},
		{ID: "obs", Label: "Connect OBS", Optional: true, Done: ob.Enabled, Screen: ScreenConnections,
			Help: "So workflows can switch scenes and show sources. Turn on OBS's WebSocket server (Tools → WebSocket Server Settings)."},
		{ID: "workflow", Label: "Turn on a workflow", Done: wf.On > 0, Screen: ScreenWorkflows,
			Help: "A workflow does something when something happens, like answering !hello. Build one in plain words."},
		{ID: "test", Label: "Try it with a test event", Done: wf.Tested, Screen: ScreenWorkflows,
			Help: "Press Test on a workflow: it runs without reaching your chat or OBS, and its trace shows what it would have done."},
		{ID: "live", Label: "See it answer in your chat", Done: wf.Answered, Screen: ScreenWorkflows,
			Help: "Type its command in your Twitch chat and watch it answer."},
	}
	o := Overview{Checklist: steps, SetUp: true, Attention: []Item{}, Workflows: wf}
	for _, s := range steps {
		if !s.Optional && !s.Done {
			o.SetUp = false
		}
	}
	add := func(level, screen, format string, a ...any) {
		o.Attention = append(o.Attention, Item{Level: level, Message: fmt.Sprintf(format, a...), Screen: screen})
	}

	// Twitch: what's wrong, never guessed. Unknown stays unknown.
	for _, a := range []struct {
		st   twitch.AccountStatus
		whom string
	}{{tw.Streamer, "your Twitch account"}, {tw.Bot, "the bot account"}} {
		switch a.st.State {
		case twitch.StateNeedsSignIn:
			add(Problem, ScreenConnections, "Sign in to %s again%s.", a.whom, detail(a.st.Detail))
		case twitch.StateUnknown:
			add(Unknown, ScreenConnections, "Can't check %s right now%s.", a.whom, detail(a.st.Detail))
		}
		// A sign-in from before StreamGremlin asked for more (D-087): what
		// it can't do until the streamer signs in again.
		if signedIn(a.st) && len(a.st.MissingScopes) > 0 {
			add(Problem, ScreenConnections, "Sign in to %s again on Connections to let StreamGremlin %s.", a.whom,
				list(twitch.ScopeWords(a.st.MissingScopes)))
		}
	}
	if signedIn(tw.Streamer) {
		switch tw.Chat.State {
		case twitch.ChatDisconnected:
			add(Problem, ScreenConnections, "Chat isn't being read%s. StreamGremlin keeps trying; messages sent meanwhile are missed.", detail(tw.Chat.Detail))
		case twitch.ChatOff:
			add(Problem, ScreenConnections, "Chat isn't being read%s.", detail(tw.Chat.Detail))
		}
		// Channel events the sign-in doesn't allow yet. Ones Twitch refuses
		// (not an affiliate, say) are listed on Connections, not here: signing
		// in again wouldn't help.
		var needSignIn []string
		for _, f := range tw.Chat.Feeds {
			if !f.OK && strings.Contains(f.Problem, "Sign in") {
				needSignIn = append(needSignIn, strings.ToLower(f.Label))
			}
		}
		// Said once: the item above already asks to sign in again.
		if len(needSignIn) > 0 && len(tw.Streamer.MissingScopes) == 0 {
			add(Tip, ScreenConnections, "Sign in to Twitch again to get %s for your workflows.", list(needSignIn))
		}
		if signedIn(tw.Bot) && tw.Chat.BotModerator != nil && !*tw.Chat.BotModerator {
			add(Tip, ScreenConnections, "Make %s a moderator (type /mod %s in your chat) so it can answer faster and isn't caught by your chat settings.",
				tw.Bot.Login, tw.Bot.Login)
		}
	}

	if ob.Enabled {
		switch ob.State {
		case obs.StateNeedsAction:
			add(Problem, ScreenConnections, "OBS needs you%s.", detail(ob.Detail))
		case obs.StateDisconnected:
			add(Unknown, ScreenConnections, "OBS isn't connected%s. StreamGremlin keeps trying; is OBS open?", detail(ob.Detail))
		}
	}

	for _, c := range in.Commands {
		if c.Conflict {
			add(Problem, ScreenWorkflows, "!%s is answered by more than one workflow. Turn one off.", c.Command)
		}
	}
	if wf.Failed24h > 0 {
		runs := "runs"
		if wf.Failed24h == 1 {
			runs = "run"
		}
		when := "in the last 24 hours"
		if wf.FailedSinceSeen {
			when = "since you marked them as seen"
		}
		add(Problem, ScreenWorkflows, "%d workflow %s failed %s. Their traces say why.", wf.Failed24h, runs, when)
		o.Attention[len(o.Attention)-1].Action = ActionMarkFailuresSeen
	}
	return o
}

// list joins words as people write them: "a, b and c".
func list(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
