package overview

import (
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

func acct(st twitch.State, login string) twitch.AccountStatus {
	return twitch.AccountStatus{State: st, Login: login}
}

func done(o Overview) map[string]bool {
	m := map[string]bool{}
	for _, s := range o.Checklist {
		m[s.ID] = s.Done
	}
	return m
}

func messages(o Overview) string {
	var b strings.Builder
	for _, a := range o.Attention {
		b.WriteString(a.Level + ": " + a.Message + "\n")
	}
	return b.String()
}

func TestAFreshInstallHasEverythingToDo(t *testing.T) {
	o := Build(Input{Twitch: twitch.Status{ClientIDSource: twitch.ClientIDNone,
		Streamer: acct(twitch.StateNotConfigured, ""), Bot: acct(twitch.StateNotConfigured, ""), Chat: twitch.ChatStatus{State: twitch.ChatOff}},
		OBS: obs.Status{State: obs.StateOff}})
	if o.SetUp || len(o.Attention) != 0 {
		t.Fatalf("%+v", o)
	}
	for id, d := range done(o) {
		if d {
			t.Errorf("%s done on a fresh install", id)
		}
	}
	if !strings.Contains(o.Checklist[0].Help, "Client ID") {
		t.Fatal("no Client ID: the first step should say so")
	}
}

func TestTheChecklistFollowsRealState(t *testing.T) {
	yes := true
	o := Build(Input{
		Twitch: twitch.Status{ClientIDSource: twitch.ClientIDPasted, Streamer: acct(twitch.StateConnected, "me"), Bot: acct(twitch.StateSignedOut, ""),
			Chat: twitch.ChatStatus{State: twitch.ChatConnected, BotModerator: &yes}},
		OBS:       obs.Status{Config: obs.Config{Enabled: false}},
		Workflows: workflow.Stats{Total: 2, On: 1, Tested: true, Answered: true},
	})
	d := done(o)
	if !d["twitch"] || d["bot"] || d["obs"] || !d["workflow"] || !d["test"] || !d["live"] {
		t.Fatalf("%v", d)
	}
	if !o.SetUp {
		t.Fatal("the bot and OBS are optional: this is set up")
	}
	// An unknown sign-in still counts as signed in, and says it can't be checked.
	o = Build(Input{Twitch: twitch.Status{Streamer: twitch.AccountStatus{State: twitch.StateUnknown, Detail: "Twitch didn't answer."},
		Chat: twitch.ChatStatus{State: twitch.ChatConnecting}}})
	if !done(o)["twitch"] || !strings.Contains(messages(o), "unknown: Can't check your Twitch account right now: Twitch didn't answer.") {
		t.Fatalf("%s", messages(o))
	}
}

func TestWhatNeedsTheStreamer(t *testing.T) {
	no := false
	o := Build(Input{
		Twitch: twitch.Status{
			Streamer: acct(twitch.StateConnected, "me"), Bot: acct(twitch.StateConnected, "gremlinbot"),
			Chat: twitch.ChatStatus{State: twitch.ChatDisconnected, Detail: "the connection dropped", BotModerator: &no,
				Feeds: []twitch.FeedStatus{
					{Label: "Follows", Problem: "Sign in to Twitch again and allow it (moderator:read:followers)."},
					{Label: "Cheers (bits)", Problem: "Sign in to Twitch again and allow it (bits:read)."},
					{Label: "Channel point redemptions", Problem: "Twitch refused it: not an affiliate."},
					{Label: "Raids", OK: true},
				}},
		},
		OBS:       obs.Status{Config: obs.Config{Enabled: true}, State: obs.StateNeedsAction, Detail: "OBS refused the password."},
		Workflows: workflow.Stats{On: 3, Failed24h: 1},
		Commands:  []commands.Entry{{Command: "so", Conflict: true}, {Command: "hello"}},
	})
	got := messages(o)
	for _, want := range []string{
		"problem: Chat isn't being read: the connection dropped.",
		"tip: Make gremlinbot a moderator (type /mod gremlinbot",
		"tip: Sign in to Twitch again to get follows and cheers (bits) for your workflows.",
		"problem: OBS needs you: OBS refused the password.",
		"problem: !so is answered by more than one workflow.",
		"problem: 1 workflow run failed in the last 24 hours.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "redemptions") {
		t.Error("a refusal that signing in again won't fix was listed")
	}
	if last := o.Attention[len(o.Attention)-1]; last.Action != ActionMarkFailuresSeen || last.Screen != ScreenWorkflows {
		t.Errorf("the failed runs can't be marked as seen: %+v", last)
	}
	seen := Build(Input{Workflows: workflow.Stats{Failed24h: 2, FailedSinceSeen: true}})
	if got := messages(seen); !strings.Contains(got, "problem: 2 workflow runs failed since you marked them as seen.") {
		t.Errorf("%s", got)
	}
	if strings.Contains(got, "!hello") {
		t.Error("a command without a conflict was listed")
	}
	// A needed sign-in, and OBS that's merely closed (unknown, not a problem).
	o = Build(Input{
		Twitch: twitch.Status{Streamer: twitch.AccountStatus{State: twitch.StateNeedsSignIn, Detail: "Twitch ended the sign-in."}, Bot: acct(twitch.StateSignedOut, "")},
		OBS:    obs.Status{Config: obs.Config{Enabled: true}, State: obs.StateDisconnected},
	})
	got = messages(o)
	if !strings.Contains(got, "problem: Sign in to your Twitch account again: Twitch ended the sign-in.") ||
		!strings.Contains(got, "unknown: OBS isn't connected.") || strings.Contains(got, "Chat isn't") {
		t.Fatalf("%s", got)
	}
}
