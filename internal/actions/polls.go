package actions

import (
	"context"
	"errors"
	"fmt"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerPolls adds the poll and prediction steps
// (docs/adr/0023-polls-and-predictions.md). They reach Twitch, so they're
// External: a test event never starts or ends one.
func registerPolls(reg *workflow.Registry, tw *twitch.Service) {
	textP := func(name, label string, required bool, editor string) workflow.Param {
		return workflow.Param{Name: name, Label: label, Type: "text", Required: required, Editor: editor}
	}
	list := func(c workflow.Call, prefix string, n int) []string {
		var out []string
		for i := 1; i <= n; i++ {
			if s, _ := c.String(fmt.Sprintf("%s_%d", prefix, i), ""); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	seconds := func(c workflow.Call, def int) (int, error) {
		v, ok := c.Args["seconds"]
		if !ok || v == nil {
			return def, nil
		}
		n, ok := number(v)
		if !ok {
			return 0, errors.New("seconds is a number")
		}
		return n, nil
	}

	pollParams := []workflow.Param{textP("question", "Question", true, "message")}
	for i := 1; i <= twitch.MaxPollChoices; i++ {
		pollParams = append(pollParams, textP(fmt.Sprintf("choice_%d", i), fmt.Sprintf("Choice %d", i), i <= 2, ""))
	}
	pollParams = append(pollParams,
		workflow.Param{Name: "seconds", Label: "How long it runs (seconds, 15 to 1800)", Type: "number", DefaultHint: "60"},
		workflow.Param{Name: "points_per_vote", Label: "Channel points for each extra vote", Type: "number", DefaultHint: "No extra votes"})
	reg.Add(workflow.Action{
		Name: "polls.start", Label: "Start a poll", Group: "Twitch", External: true,
		Description: "Starts a poll in your chat. Twitch runs one at a time, on Affiliate and Partner channels. Safe to repeat.",
		Params:      pollParams,
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			q, _ := c.String("question", "")
			secs, err := seconds(c, 60)
			if err != nil {
				return nil, err
			}
			per := 0
			if v, ok := c.Args["points_per_vote"]; ok && v != nil {
				n, ok := number(v)
				if !ok {
					return nil, errors.New("channel points per vote is a number")
				}
				per = n
			}
			p, created, err := tw.StartPoll(ctx, twitch.NewPoll{Title: q, Choices: list(c, "choice", twitch.MaxPollChoices), Seconds: secs,
				ChannelPointsPerVote: per})
			if err != nil {
				return nil, err
			}
			return map[string]any{"poll_id": p.ID, "question": p.Title, "choices": len(p.Choices), "repeated": !created}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "polls.end", Label: "End the poll", Group: "Twitch", External: true, More: true,
		Description: "Ends the running poll early: its results stay in chat for a while, or it's removed at once.",
		Params: []workflow.Param{{Name: "how", Label: "Results", Type: "choice", Choices: []string{"show", "remove"},
			DefaultHint: "Show them"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			how, _ := c.String("how", "show")
			p, err := tw.EndPoll(ctx, how == "remove")
			if errors.Is(err, twitch.ErrNoPoll) {
				return map[string]any{"ended": false, "why": "no poll was running"}, nil // nothing to end: not a failure
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{"ended": true, "status": p.Status}, nil
		},
	})

	predParams := []workflow.Param{textP("question", "Question", true, "message")}
	for i := 1; i <= 5; i++ {
		predParams = append(predParams, textP(fmt.Sprintf("outcome_%d", i), fmt.Sprintf("Outcome %d", i), i <= 2, ""))
	}
	predParams = append(predParams, workflow.Param{Name: "seconds", Label: "How long it takes bets (seconds, 30 to 1800)", Type: "number",
		DefaultHint: "120"})
	reg.Add(workflow.Action{
		Name: "predictions.start", Label: "Start a prediction", Group: "Twitch", External: true,
		Description: "Opens a channel points prediction. Twitch runs one at a time, on Affiliate and Partner channels. Safe to repeat.",
		Params:      predParams,
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			q, _ := c.String("question", "")
			secs, err := seconds(c, 120)
			if err != nil {
				return nil, err
			}
			p, created, err := tw.StartPrediction(ctx, twitch.NewPrediction{Title: q, Outcomes: list(c, "outcome", 5), Seconds: secs})
			if err != nil {
				return nil, err
			}
			return map[string]any{"prediction_id": p.ID, "question": p.Title, "outcomes": len(p.Outcomes), "repeated": !created}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "predictions.end", Label: "End the prediction", Group: "Twitch", External: true,
		Description: "Locks the newest prediction (no more bets), resolves it with the winning outcome (its words or its number), " +
			"or cancels it and refunds everyone. Safe to repeat.",
		Params: []workflow.Param{
			{Name: "how", Label: "Lock, resolve or cancel", Type: "choice", Required: true, Choices: []string{"lock", "resolve", "cancel"}},
			textP("winner", "Winning outcome (for resolve): its words or its number", false, "message"),
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			how, _ := c.String("how", "")
			winner, _ := c.String("winner", "")
			p, repeated, err := tw.EndPrediction(ctx, how, winner)
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": p.Status, "repeated": repeated}, nil
		},
	})
}
