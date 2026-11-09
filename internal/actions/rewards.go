package actions

import (
	"context"
	"errors"
	"fmt"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// redemptionOf finds the reward and redemption a run was started by.
func redemptionOf(c workflow.Call) (rewardID, redemptionID string, ok bool) {
	if c.Event == nil || c.Event["type"] != "rewards.channel_points.redeemed" {
		return "", "", false
	}
	p, _ := c.Event["payload"].(map[string]any)
	rw, _ := p["reward"].(map[string]any)
	rewardID, _ = rw["id"].(string)
	redemptionID, _ = p["redemption_id"].(string)
	return rewardID, redemptionID, rewardID != "" && redemptionID != ""
}

// registerRewards adds the channel point steps
// (docs/adr/0018-channel-point-rewards.md). They reach Twitch, so they're
// External: a test event never completes, refunds or changes anything.
func registerRewards(reg *workflow.Registry, tw *twitch.Service) {
	reg.Add(workflow.Action{
		Name: "rewards.redemption.set", Label: "Complete or refund the redemption", Group: "Twitch", External: true,
		Description: "Marks the channel point redemption that started this workflow as done, or refunds the viewer's points. " +
			"Only for rewards made in StreamGremlin. Safe to repeat.",
		Params: []workflow.Param{
			{Name: "action", Label: "Done or refund", Type: "choice", Required: true, Choices: []string{"done", "refund"}},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			reward, id, ok := redemptionOf(c)
			if !ok {
				return nil, errors.New("this step works only when a viewer redeems channel points: there's no redemption here")
			}
			action, _ := c.String("action", "")
			d, repeated, err := tw.SetRedemption(ctx, reward, id, action == "done")
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": d.Status, "repeated": repeated, "reward": d.Reward, "viewer": d.UserName}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "rewards.update", Label: "Change a reward", Group: "Twitch", External: true,
		Description: "Turns one of your channel point rewards on or off, pauses or resumes it, or changes its cost. " +
			"Only for rewards made in StreamGremlin. Safe to repeat.",
		Params: []workflow.Param{
			{Name: "reward", Label: "Reward", Type: "text", Required: true, Literal: true, Editor: "reward"},
			{Name: "state", Label: "On, off, pause or resume", Type: "choice", Choices: []string{"on", "off", "pause", "resume"},
				DefaultHint: "Leave it as it is"},
			{Name: "cost", Label: "New cost (points)", Type: "number", DefaultHint: "Leave it as it is"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			name, _ := c.String("reward", "")
			r, err := tw.Reward(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", name, err)
			}
			if !r.Managed {
				return nil, fmt.Errorf("%q: %w", r.Title, twitch.ErrNotManaged)
			}
			var ch twitch.RewardChange
			on, off := true, false
			switch state, _ := c.String("state", ""); state {
			case "on":
				ch.Enabled = &on
			case "off":
				ch.Enabled = &off
			case "pause":
				ch.Paused = &on
			case "resume":
				ch.Paused = &off
			}
			if v, ok := c.Args["cost"]; ok && v != nil {
				n, ok := number(v)
				if !ok || n < 1 {
					return nil, errors.New("a reward costs at least 1 point")
				}
				ch.Cost = &n
			}
			if ch.Enabled == nil && ch.Paused == nil && ch.Cost == nil {
				return nil, errors.New("choose on, off, pause or resume, or a new cost")
			}
			up, err := tw.UpdateReward(ctx, r.ID, ch)
			if err != nil {
				return nil, err
			}
			return map[string]any{"reward": up.Title, "enabled": up.Enabled, "paused": up.Paused, "cost": up.Cost}, nil
		},
	})
}
