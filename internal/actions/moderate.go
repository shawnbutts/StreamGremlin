package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerModeration adds the step that moderates the viewer behind the
// event (D-100): delete their message, time them out or ban them, on the
// platform the event came from, as you, through the moderation record.
func registerModeration(reg *workflow.Registry, mods *moderation.Service) {
	reg.Add(workflow.Action{
		Name: "chat.moderate", Label: "Moderate the viewer", Group: "Chat", External: true, More: true,
		Description: "Deletes the message being answered, or times out or bans the viewer who sent it, on Twitch or YouTube, " +
			"wherever it came from, as you. On YouTube a delete costs 20 quota units and a timeout or ban 200. Sent once and " +
			"kept in the moderation record; never retried. A ban can't be taken back by the viewer: think about who can start it.",
		Params: []workflow.Param{
			{Name: "what", Label: "What to do", Type: "choice", Required: true, Choices: []string{"delete", "timeout", "ban"}},
			{Name: "seconds", Label: "Timeout length (seconds)", Type: "number", Editor: "seconds", DefaultHint: "600 (10 minutes)"},
			{Name: "reason", Label: "Reason (Twitch shows it; YouTube takes none)", Type: "text"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			what, _ := c.String("what", "")
			reason, _ := c.String("reason", "")
			platform, _ := c.Event["source"].(string)
			if platform != moderation.PlatformTwitch && platform != moderation.PlatformYouTube {
				return nil, errors.New("only an event from Twitch or YouTube chat has a viewer to moderate")
			}
			if c.Actor == nil || c.Actor.ID == "" {
				return nil, errors.New("this event has no viewer to moderate")
			}
			req := moderation.Request{Action: what, Platform: platform, UserID: c.Actor.ID, Login: c.Actor.Login, From: moderation.FromWorkflow}
			switch what {
			case "delete":
				p, _ := c.Event["payload"].(map[string]any)
				id, _ := p["message_id"].(string)
				if id == "" {
					return nil, errors.New("this event has no chat message to delete")
				}
				req.MessageID = id
			case "timeout":
				req.Seconds = 600
				if v, ok := c.Args["seconds"]; ok && v != nil {
					n, ok := number(v)
					if !ok || n < 1 {
						return nil, errors.New("a timeout lasts at least 1 second")
					}
					req.Seconds = n
				}
				req.Reason = reason
			case "ban":
				req.Reason = reason
			default:
				return nil, fmt.Errorf("what to do is delete, timeout or ban, not %q", what)
			}
			// The same step of the same run is the same request, so a
			// repeat never moderates twice.
			return doMod(ctx, mods, c, req)
		},
	})
}

// registerProtection adds Shield Mode and locking chat (D-105): Twitch
// only, as you, through the moderation record. They're always offered.
func registerProtection(reg *workflow.Registry, mods *moderation.Service) {
	reg.Add(workflow.Action{
		Name: "twitch.shield_mode.set", Label: "Turn Shield Mode on or off", Group: "Twitch", External: true,
		Description: "Turns Twitch's Shield Mode on or off, as you. Shield Mode applies the stricter settings you chose in Twitch's " +
			"Mod View. Sent once and kept in the moderation record.",
		Params: []workflow.Param{{Name: "on", Label: "Shield Mode", Type: "choice", Required: true, Choices: []string{"on", "off"}}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			v, _ := c.String("on", "")
			if v != "on" && v != "off" {
				return nil, fmt.Errorf("on or off, not %q", v)
			}
			on := v == "on"
			return doMod(ctx, mods, c, moderation.Request{Action: moderation.ShieldMode, Platform: moderation.PlatformTwitch, On: &on,
				From: moderation.FromWorkflow})
		},
	})
	reg.Add(workflow.Action{
		Name: "twitch.chat.lock", Label: "Lock or unlock chat", Group: "Twitch", External: true,
		Description: fmt.Sprintf("Lock: Shield Mode, emote-only and followers-only (followed at least %d minutes) on at once, as "+
			"Live's Lock chat button does. Unlock: puts back only what the lock changed. Kept in the moderation record.",
			moderation.LockFollowerMinutes),
		Params: []workflow.Param{{Name: "how", Label: "Lock or unlock", Type: "choice", Required: true, Choices: []string{"lock", "unlock"}}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			how, _ := c.String("how", "")
			if how != moderation.Lock && how != moderation.Unlock {
				return nil, fmt.Errorf("lock or unlock, not %q", how)
			}
			return doMod(ctx, mods, c, moderation.Request{Action: how, Platform: moderation.PlatformTwitch, From: moderation.FromWorkflow})
		},
	})
}

// doMod sends a step's request once: the same step of the same run is the
// same request, so a repeat never moderates twice.
func doMod(ctx context.Context, mods *moderation.Service, c workflow.Call, req moderation.Request) (any, error) {
	if mods == nil {
		return nil, errors.New("moderating isn't available here")
	}
	sum := sha256.Sum256([]byte(c.IdempotencyKey))
	req.RequestID = "wf-" + hex.EncodeToString(sum[:20])
	rec, _, err := mods.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"platform": rec.Platform, "action": rec.Action, "state": rec.State, "answer": rec.Answer, "request_id": rec.RequestID}
	switch rec.State {
	case moderation.Done:
		return out, nil
	case moderation.Unknown:
		return out, fmt.Errorf("no answer, so it may or may not have happened: %s", rec.Answer)
	}
	return out, fmt.Errorf("refused: %s", rec.Answer)
}
