package actions

import (
	"context"
	"errors"

	"code.streamgremlin.com/streamgremlin/internal/giveaways"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerGiveaways adds the giveaway steps (docs/adr/0025-giveaways.md).
// They speak in chat, so they're External: a test event never opens or
// draws one (practice giveaways are opened from the Giveaways screen).
func registerGiveaways(reg *workflow.Registry, svc *giveaways.Service) {
	current := func() (*giveaways.Giveaway, error) {
		g := svc.Current()
		if g == nil {
			return nil, errors.New("no giveaway is running")
		}
		return g, nil
	}
	num := func(c workflow.Call, name string) (int, error) {
		v, ok := c.Args[name]
		if !ok || v == nil {
			return 0, nil
		}
		n, ok := number(v)
		if !ok {
			return 0, errors.New(name + " is a number")
		}
		return n, nil
	}
	reg.Add(workflow.Action{
		Name: "giveaways.open", Label: "Open a giveaway", Group: "Giveaways", External: true,
		Description: "Opens a giveaway and says in chat how to enter. One entry each, and every entry has the same chance. " +
			"One giveaway runs at a time.",
		Params: []workflow.Param{
			{Name: "title", Label: "What's being given away", Type: "text", Required: true, Editor: "message"},
			{Name: "command", Label: "Entry command, without !", Type: "text", DefaultHint: "join"},
			{Name: "winners", Label: "Winners (1 to 20)", Type: "number", DefaultHint: "1"},
			{Name: "minutes", Label: "Close entries after (minutes)", Type: "number", DefaultHint: "When you close them"},
			{Name: "claim_seconds", Label: "Seconds to claim (15 to 600)", Type: "number", DefaultHint: "No claiming: a draw wins at once"},
			{Name: "subscribers_only", Label: "Subscribers only", Type: "choice", Choices: []string{"no", "yes"}, DefaultHint: "no"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			var st giveaways.Settings
			st.Title, _ = c.String("title", "")
			st.Command, _ = c.String("command", "")
			subs, _ := c.String("subscribers_only", "no")
			st.SubscribersOnly = subs == "yes"
			var err error
			if st.Winners, err = num(c, "winners"); err != nil {
				return nil, err
			}
			if st.Minutes, err = num(c, "minutes"); err != nil {
				return nil, err
			}
			if st.ClaimSeconds, err = num(c, "claim_seconds"); err != nil {
				return nil, err
			}
			// The same step again (a retried run) finds its own giveaway.
			if g := svc.Current(); g != nil && g.OpenedBy == "job:"+c.IdempotencyKey {
				return map[string]any{"giveaway_id": g.ID, "repeated": true}, nil
			}
			g, err := svc.Open(ctx, st, "job:"+c.IdempotencyKey)
			if err != nil {
				return nil, err
			}
			return map[string]any{"giveaway_id": g.ID, "command": g.Command}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "giveaways.close", Label: "Close giveaway entries", Group: "Giveaways", External: true, More: true,
		Description: "Stops the running giveaway taking entries, and says how many entered. Closing twice is harmless.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			g, err := current()
			if err != nil {
				return nil, err
			}
			out, err := svc.CloseEntries(ctx, g.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"entries": out.EntryCount, "fingerprint": out.Fingerprint}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "giveaways.draw", Label: "Draw giveaway winners", Group: "Giveaways", External: true,
		Description: "Closes entries if they're open, then draws the winners still wanted and announces them. Never more " +
			"winners than the giveaway has, so repeating it draws no one extra.",
		Params: []workflow.Param{{Name: "count", Label: "How many", Type: "number", DefaultHint: "All still wanted"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			g, err := current()
			if err != nil {
				return nil, err
			}
			n, err := num(c, "count")
			if err != nil {
				return nil, err
			}
			draws, err := svc.Draw(ctx, g.ID, n)
			if err != nil {
				return nil, err
			}
			var names []string
			for _, d := range draws {
				names = append(names, d.Winner.DisplayName)
			}
			return map[string]any{"winners": names}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "giveaways.end", Label: "End the giveaway", Group: "Giveaways", External: true, More: true,
		Description: "Ends the running giveaway and congratulates its winners in chat.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			g, err := current()
			if err != nil {
				return nil, err
			}
			out, err := svc.End(ctx, g.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"state": out.State}, nil
		},
	})
}
