package actions

import (
	"context"
	"errors"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/ads"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// chatNotFromStreamer: chat started this run, and not the streamer's own.
// Only the streamer runs ads from chat: a viewer's or a moderator's "!ad"
// would cost the streamer's viewers an ad they didn't choose.
func chatNotFromStreamer(c workflow.Call) bool {
	typ, _ := c.Event["type"].(string)
	if !strings.HasPrefix(typ, "chat.message.") && !strings.HasPrefix(typ, "chat.command.") {
		return false
	}
	p, _ := c.Event["payload"].(map[string]any)
	who, _ := p["chatter"].(map[string]any)
	return who["is_broadcaster"] != true
}

func requestedBy(c workflow.Call) string {
	by := "workflow " + c.WorkflowID
	if c.Actor != nil && c.Actor.Login != "" {
		by += " for " + c.Actor.Login
	}
	return by
}

var errAdsFromChat = errors.New("only you can run ads from chat: this chat message wasn't yours, so nothing was asked of Twitch")

// registerAds adds the ad steps (docs/adr/0024-ads.md). They reach Twitch,
// so they're External. A refused or unclear request fails the step, so
// steps after it never announce an ad that isn't happening.
func registerAds(reg *workflow.Registry, svc *ads.Service) {
	reg.Add(workflow.Action{
		Name: "ads.commercial.start", Label: "Run an ad break", Group: "Twitch", External: true, More: true,
		Description: "Asks Twitch to start an ad break now (Affiliate and Partner channels, while live). Twitch needs a gap between " +
			"ads. Never sent twice: if Twitch's answer is unclear, StreamGremlin checks rather than asking again. The step fails " +
			"when Twitch refuses, so later steps don't announce an ad.",
		Params: []workflow.Param{{Name: "seconds", Label: "Length (seconds, 1 to 180)", Type: "number", Required: true, DefaultHint: "90"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if chatNotFromStreamer(c) {
				return nil, errAdsFromChat
			}
			n, ok := number(c.Args["seconds"])
			if !ok {
				return nil, errors.New("the length is a number of seconds")
			}
			q, err := svc.StartCommercial(ctx, n, "job:"+c.IdempotencyKey, requestedBy(c))
			if err != nil {
				return nil, err
			}
			return map[string]any{"request_id": q.ID, "status": q.Status, "length": q.AcceptedLength}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "ads.snooze", Label: "Snooze the next ad", Group: "Twitch", External: true, More: true,
		Description: "Pushes Twitch's next automatic ad back 5 minutes, if you have a snooze left. Fails when Twitch refuses.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if chatNotFromStreamer(c) {
				return nil, errAdsFromChat
			}
			q, err := svc.Snooze(ctx, "job:"+c.IdempotencyKey, requestedBy(c))
			if err != nil {
				return nil, err
			}
			out := map[string]any{"request_id": q.ID, "snoozes_left": q.SnoozeCount}
			if q.NextAdAt != nil {
				out["next_ad_at"] = q.NextAdAt
			}
			return out, nil
		},
	})
}
