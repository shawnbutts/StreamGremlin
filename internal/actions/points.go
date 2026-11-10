package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerPoints adds the points steps (docs/adr/0032-viewers-and-points.md).
// Giving and taking are External: a test event never changes a real
// balance. Each step's idempotency key is its operation id, so a retried
// run never pays twice. Checking only reads, so it runs for tests too.
func registerPoints(reg *workflow.Registry, vs *viewers.Service, ledger *points.Ledger) {
	unavailable := errors.New("points aren't available")
	// who finds the viewer: "@name" or a name, or the event's viewer.
	who := func(ctx context.Context, c workflow.Call, create bool) (viewers.Viewer, error) {
		name, _ := c.String("viewer", "")
		name = strings.TrimSpace(name)
		if name != "" {
			v, err := vs.ByLogin(ctx, name)
			if errors.Is(err, viewers.ErrNotFound) {
				return v, fmt.Errorf("StreamGremlin hasn't seen %s in chat yet", name)
			}
			return v, err
		}
		a := c.Viewer()
		if a == nil {
			a = c.Actor
		}
		if a == nil || a.ID == "" {
			return viewers.Viewer{}, errors.New("this event has no viewer: say whose points, like @name")
		}
		if !create {
			if v, err := vs.ByAccount(ctx, a.Platform, a.ID); err == nil {
				return v, nil
			}
			return viewers.Viewer{Platform: a.Platform, UserID: a.ID, Login: a.Login, DisplayName: a.DisplayName}, nil
		}
		return vs.Ensure(ctx, events.Actor{Platform: a.Platform, ID: a.ID, Login: a.Login, DisplayName: a.DisplayName})
	}
	amountOf := func(c workflow.Call) (int64, error) {
		v, ok := c.Args["amount"]
		n, okn := number(v)
		if !ok || !okn || n <= 0 {
			return 0, errors.New("the amount is a whole number above 0")
		}
		return int64(n), nil
	}
	by := func(c workflow.Call) string {
		if c.WorkflowID != "" {
			return "workflow " + c.WorkflowID
		}
		return "a workflow"
	}
	move := func(give bool) func(ctx context.Context, c workflow.Call) (any, error) {
		return func(ctx context.Context, c workflow.Call) (any, error) {
			if vs == nil || ledger == nil {
				return nil, unavailable
			}
			amount, err := amountOf(c)
			if err != nil {
				return nil, err
			}
			v, err := who(ctx, c, true)
			if err != nil {
				return nil, err
			}
			reason, _ := c.String("reason", "")
			currency, _ := c.String("currency", "")
			op := points.Op{ID: "job:" + c.IdempotencyKey, Currency: currency, Viewer: v.ID, Amount: amount, Memo: reason, By: by(c)}
			var res points.Result
			if give {
				res, err = ledger.Credit(ctx, op)
			} else {
				res, err = ledger.Debit(ctx, op)
			}
			if err != nil {
				return nil, err
			}
			cur, _ := ledger.Currency(ctx, currency)
			return map[string]any{"balance": res.Balance, "amount": amount, "viewer": v.DisplayName, "currency": cur.Name,
				"transaction": res.Transaction.ID, "repeated": !res.Applied}, nil
		}
	}
	params := []workflow.Param{
		{Name: "amount", Label: "How many", Type: "number", Required: true},
		{Name: "viewer", Label: "Whose", Type: "text", DefaultHint: "the viewer behind the event"},
		{Name: "reason", Label: "Reason", Type: "text", DefaultHint: "none"},
	}
	reg.Add(workflow.Action{
		Name: "points.give", Label: "Give points", Group: "Points", External: true,
		Description: "Gives a viewer points. A retried run never gives them twice; a test event never changes a real balance.",
		Params:      params, Run: move(true),
	})
	reg.Add(workflow.Action{
		Name: "points.take", Label: "Take points", Group: "Points", External: true,
		Description: "Takes a viewer's points, as for something they bought. Fails, changing nothing, if they haven't enough: " +
			"\"not enough points: has 60, needs 100\".",
		Params: params, Run: move(false),
	})
	reg.Add(workflow.Action{
		Name: "points.balance", Label: "Check points", Group: "Points",
		Description: "Finds how many points a viewer has, for a reply like \"@name has ${steps.<id>.output.balance} points\".",
		Params:      []workflow.Param{{Name: "viewer", Label: "Whose", Type: "text", DefaultHint: "the viewer behind the event"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if vs == nil || ledger == nil {
				return nil, unavailable
			}
			v, err := who(ctx, c, false)
			if err != nil {
				return nil, err
			}
			currency, _ := c.String("currency", "")
			cur, err := ledger.Currency(ctx, currency)
			if err != nil {
				return nil, err
			}
			var b int64
			if v.ID != "" {
				if b, err = ledger.Balance(ctx, v.ID, cur.ID); err != nil {
					return nil, err
				}
			}
			return map[string]any{"balance": b, "viewer": v.DisplayName, "currency": cur.Name}, nil
		},
	})
}
