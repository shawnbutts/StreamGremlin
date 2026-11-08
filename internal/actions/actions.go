// Package actions registers the steps workflows can use, on top of the
// Twitch and OBS services. Every action that reaches Twitch or OBS is
// External, so it never runs for a test event (docs/adr/0008-workflows.md).
package actions

import (
	"context"
	"errors"
	"fmt"

	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Register adds every action to reg.
func Register(reg *workflow.Registry, tw *twitch.Service, ob *obs.Service) {
	text := func(name, label string, required bool) workflow.Param {
		return workflow.Param{Name: name, Label: label, Type: "text", Required: required}
	}
	choice := func(name, label string, required bool, choices ...string) workflow.Param {
		return workflow.Param{Name: name, Label: label, Type: "choice", Required: required, Choices: choices}
	}
	mode := choice("mode", "On, off or toggle", true, "on", "off", "toggle")

	reg.Add(workflow.Action{
		Name: "note", Label: "Write a note in the run's trace", Group: "General",
		Description: "Records text in the trace. Useful to see what a workflow knew; reaches nothing outside.",
		Params:      []workflow.Param{text("text", "Text", true)},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			t, err := c.String("text", "")
			return map[string]any{"text": t}, err
		},
	})

	reg.Add(workflow.Action{
		Name: "chat.send", Label: "Send a chat message", Group: "Twitch", External: true,
		Description: "Posts in your Twitch chat, as the bot when one is signed in. Never retried: a repeat could post twice.",
		Params: []workflow.Param{text("message", "Message", true), choice("as", "Send as", false, "bot", "streamer"),
			text("reply_to", "Reply to message id", false)},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			msg, err := c.String("message", "")
			if err != nil {
				return nil, err
			}
			as, _ := c.String("as", "")
			reply, _ := c.String("reply_to", "")
			return tw.SendChat(ctx, twitch.SendRequest{Text: msg, As: twitch.Sender(as), ReplyTo: reply})
		},
	})

	obsAction := func(name, label, desc string, params []workflow.Param, run func(ctx context.Context, c workflow.Call) (any, error)) {
		reg.Add(workflow.Action{Name: name, Label: label, Group: "OBS", External: true, Description: desc, Params: params, Run: run})
	}
	str := func(c workflow.Call, name string) string { s, _ := c.String(name, ""); return s }

	obsAction("obs.scene.switch", "Switch scene", "Makes a scene live. Safe to repeat.",
		[]workflow.Param{text("scene", "Scene", true)},
		func(ctx context.Context, c workflow.Call) (any, error) {
			prev, err := ob.SwitchScene(ctx, str(c, "scene"), "workflow:"+c.WorkflowID)
			return map[string]any{"scene": str(c, "scene"), "previous_scene": prev}, err
		})
	obsAction("obs.source.set_visible", "Show or hide a source", "Shows or hides a source in a scene. Safe to repeat.",
		[]workflow.Param{text("scene", "Scene", true), text("source", "Source", true), {Name: "visible", Label: "Visible", Type: "bool", Required: true}},
		func(ctx context.Context, c workflow.Call) (any, error) {
			v, err := c.Bool("visible", true)
			if err != nil {
				return nil, err
			}
			prev, err := ob.SetSourceVisible(ctx, str(c, "scene"), str(c, "source"), v)
			return map[string]any{"visible": v, "previous_visible": prev}, err
		})
	obsAction("obs.filter.set", "Turn a filter on or off", "On, off or toggle. Toggle isn't safe to repeat.",
		[]workflow.Param{text("source", "Source or scene", true), text("filter", "Filter", true), mode},
		func(ctx context.Context, c workflow.Call) (any, error) {
			on, err := ob.SetFilter(ctx, str(c, "source"), str(c, "filter"), obs.Mode(str(c, "mode")))
			return map[string]any{"enabled": on}, err
		})
	obsAction("obs.audio.set_muted", "Mute or unmute", "Mutes, unmutes or flips an audio source.",
		[]workflow.Param{text("source", "Audio source", true), mode},
		func(ctx context.Context, c workflow.Call) (any, error) {
			m, err := ob.SetMuted(ctx, str(c, "source"), obs.Mode(str(c, "mode")))
			return map[string]any{"muted": m}, err
		})
	obsAction("obs.text.set", "Set a text source's text", "Only text sources; nothing else about the source changes.",
		[]workflow.Param{text("source", "Text source", true), text("text", "Text", true)},
		func(ctx context.Context, c workflow.Call) (any, error) {
			return map[string]any{"text": str(c, "text")}, ob.SetText(ctx, str(c, "source"), str(c, "text"))
		})
	obsAction("obs.media.control", "Control a media source", "Play, pause, stop, restart, next or previous.",
		[]workflow.Param{text("source", "Media source", true), choice("action", "What to do", true, "play", "pause", "stop", "restart", "next", "previous")},
		func(ctx context.Context, c workflow.Call) (any, error) {
			return map[string]any{"action": str(c, "action")}, ob.Media(ctx, str(c, "source"), str(c, "action"))
		})
	obsAction("obs.recording.control", "Start or stop recording", "Does nothing if it already is. Safe to repeat.",
		[]workflow.Param{choice("action", "Start or stop", true, "start", "stop")},
		func(ctx context.Context, c workflow.Call) (any, error) {
			changed, path, err := ob.Recording(ctx, str(c, "action") == "start")
			return map[string]any{"changed": changed, "output_path": path}, err
		})
	obsAction("obs.replay.save", "Save the replay buffer", "Only while OBS's replay buffer runs.", nil,
		func(ctx context.Context, c workflow.Call) (any, error) { return map[string]any{}, ob.SaveReplay(ctx) })
	obsAction("obs.screenshot", "Take a screenshot", "Saves a PNG of a source into a folder on the computer OBS runs on. StreamGremlin names the file.",
		[]workflow.Param{text("source", "Source or scene", true), text("folder", "Folder (full path)", true)},
		func(ctx context.Context, c workflow.Call) (any, error) {
			p, err := ob.Screenshot(ctx, str(c, "source"), str(c, "folder"))
			return map[string]any{"path": p}, err
		})
	obsAction("obs.scene.snapshot_and_switch", "Switch scene for now", "Remembers the current scene under a name and switches. Switch back with \"Switch back\".",
		[]workflow.Param{text("key", "Name to remember it by", true), text("scene", "Scene", true)},
		func(ctx context.Context, c workflow.Call) (any, error) {
			return ob.Snapshots().Take(ctx, str(c, "key"), str(c, "scene"))
		})
	obsAction("obs.scene.restore", "Switch back", "Switches back to the remembered scene, only if nobody changed the scene in OBS meanwhile. Otherwise stays, and says why.",
		[]workflow.Param{text("key", "Name it was remembered by", true)},
		func(ctx context.Context, c workflow.Call) (any, error) {
			s, err := ob.Snapshots().Restore(ctx, str(c, "key"))
			if errors.Is(err, obs.ErrNoSnapshot) {
				return map[string]any{"status": "none"}, nil // nothing to switch back: not a failure
			}
			return s, err
		})
	obsAction("obs.scene.release", "Forget a remembered scene", "Stays on the current scene.",
		[]workflow.Param{text("key", "Name it was remembered by", true)},
		func(ctx context.Context, c workflow.Call) (any, error) {
			s, err := ob.Snapshots().Release(ctx, str(c, "key"))
			if errors.Is(err, obs.ErrNoSnapshot) {
				return map[string]any{"status": "none"}, nil
			}
			return s, err
		})
	reg.Add(workflow.Action{
		Name: "obs.scene.current", Label: "Read the current scene", Group: "OBS",
		Description: "Reads OBS's live scene. Changes nothing, so it runs for test events too; fails while OBS isn't connected.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			cur, _, err := ob.Scenes(ctx)
			if err != nil {
				return nil, fmt.Errorf("couldn't read OBS's scene: %w", err)
			}
			return map[string]any{"scene": cur}, nil
		},
	})
}
