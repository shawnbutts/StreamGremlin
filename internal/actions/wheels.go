package actions

import (
	"context"
	"errors"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/wheels"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerWheels adds the wheel steps (docs/adr/0030-wheels.md). Spinning is
// External: a test event never spins a wheel, so the record holds only real
// spins. Saying the odds only reads.
func registerWheels(reg *workflow.Registry, svc *wheels.Service) {
	unavailable := errors.New("wheels aren't available")
	reg.Add(workflow.Action{
		Name: "wheels.spin", Label: "Spin a wheel", Group: "Wheels", External: true,
		Description: "Spins a wheel and waits until it stops, as the wheel overlay shows it. The result is drawn by StreamGremlin " +
			"and recorded; the next steps can use it, such as a chat reply with ${steps.<id>.output.label}.",
		Params: []workflow.Param{
			{Name: "wheel", Label: "Wheel", Type: "text", Required: true, DefaultHint: "its name, like next-game"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if svc == nil {
				return nil, unavailable
			}
			name, _ := c.String("wheel", "")
			name = strings.TrimSpace(name)
			by := "a workflow"
			if c.WorkflowID != "" {
				by = "workflow " + c.WorkflowID
			}
			// The step's key: a retried run finds its spin, never a second one.
			sp, repeated, err := svc.Spin(ctx, wheels.SpinRequest{Wheel: name, Key: "job:" + c.IdempotencyKey, By: by, Actor: c.Viewer()})
			if err != nil {
				return nil, err
			}
			done, err := svc.Wait(ctx, sp.ID)
			if err != nil {
				return nil, err
			}
			if done.SettledAt == nil {
				return nil, errors.New("the wheel hasn't stopped yet")
			}
			return map[string]any{"spin_id": done.ID, "label": done.Label, "choice": done.Winner + 1, "display": done.Display,
				"repeated": repeated}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "wheels.odds", Label: "Say a wheel's odds", Group: "Wheels", More: true,
		Description: "Gives a wheel's odds as text for a chat reply: \"Next game: Mario 50%, Zelda 25%, …\", as ${steps.<id>.output.text}.",
		Params: []workflow.Param{
			{Name: "wheel", Label: "Wheel", Type: "text", Required: true, DefaultHint: "its name, like next-game"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if svc == nil {
				return nil, unavailable
			}
			name, _ := c.String("wheel", "")
			w, err := svc.Get(ctx, strings.TrimSpace(name))
			if err != nil {
				return nil, err
			}
			return map[string]any{"text": wheels.OddsWords(w), "odds": wheels.OddsOf(w)}, nil
		},
	})
}
