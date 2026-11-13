package actions

import (
	"context"
	"errors"
	"math"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerOverlayTimers adds starting, pausing, resetting and adding time
// to a clock and timer overlay's countdown or stopwatch (D-092). Buttons
// and Stream Deck keys run them through workflows.
func registerOverlayTimers(reg *workflow.Registry, timers *overlays.Timers) {
	which := workflow.Param{Name: "overlay", Label: "Countdown or stopwatch", Type: "text", Required: true, Literal: true, Editor: "timer_overlay",
		Pattern: overlays.NamePattern, PatternHint: "a clock and timer overlay's name, like starting-soon"}
	minutes := func(c workflow.Call, name string) (int, bool, error) {
		v, ok := c.Args[name]
		if !ok || v == nil || v == "" {
			return 0, false, nil
		}
		var m float64
		switch x := v.(type) {
		case int64:
			m = float64(x)
		case float64:
			m = x
		default:
			return 0, false, errors.New("minutes are a number, like 5 or 0.5")
		}
		s := int(math.Round(m * 60))
		if s < 1 || s > overlays.MaxTimerSeconds {
			return 0, false, errors.New("a countdown is from 1 second to 24 hours")
		}
		return s, true, nil
	}
	do := func(ctx context.Context, c workflow.Call, op overlays.TimerOp) (any, error) {
		if timers == nil {
			return nil, errors.New("timers aren't set up")
		}
		name, _ := c.String("overlay", "")
		st, err := timers.Do(ctx, strings.TrimSpace(name), op)
		if errors.Is(err, overlays.ErrNotTimer) {
			return nil, errors.New("that overlay isn't a countdown or a stopwatch")
		}
		if err != nil {
			return nil, errors.New(strings.TrimPrefix(strings.TrimPrefix(err.Error(), "overlays: "), "invalid: "))
		}
		return map[string]any{"overlay": name, "running": st.Running, "seconds": st.BaseMS / 1000}, nil
	}
	reg.Add(workflow.Action{
		Name: "overlays.timer.start", Label: "Start a countdown or stopwatch", Group: "Overlays",
		Description: "Starts a clock and timer overlay's countdown or stopwatch, or goes on from where it was paused. A countdown " +
			"can run for a number of minutes, or until a time like 19:00 (tomorrow if it's passed); with neither, a countdown " +
			"that reached zero starts again from its length. \"A countdown ends\" runs when it reaches zero.",
		Params: []workflow.Param{which,
			{Name: "minutes", Label: "Count down for (minutes)", Type: "number", DefaultHint: "Go on, or the overlay's length"},
			{Name: "until", Label: "Count down until (like 19:00)", Type: "text", Pattern: `^([01]?[0-9]|2[0-3]):[0-5][0-9]$`,
				PatternHint: "a time on a 24-hour clock, like 19:00", DefaultHint: "No time"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			s, _, err := minutes(c, "minutes")
			if err != nil {
				return nil, err
			}
			until, _ := c.String("until", "")
			return do(ctx, c, overlays.TimerOp{Action: "start", Seconds: s, Until: strings.TrimSpace(until)})
		},
	})
	reg.Add(workflow.Action{
		Name: "overlays.timer.pause", Label: "Pause a countdown or stopwatch", Group: "Overlays",
		Description: "Stops a countdown or stopwatch where it is. Start it again to go on.",
		Params:      []workflow.Param{which},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			return do(ctx, c, overlays.TimerOp{Action: "pause"})
		},
	})
	reg.Add(workflow.Action{
		Name: "overlays.timer.reset", Label: "Reset a countdown or stopwatch", Group: "Overlays",
		Description: "Stops it, and puts a countdown back to its length and a stopwatch back to zero.",
		Params:      []workflow.Param{which},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			return do(ctx, c, overlays.TimerOp{Action: "reset"})
		},
	})
	reg.Add(workflow.Action{
		Name: "overlays.timer.add", Label: "Add time to a countdown", Group: "Overlays",
		Description: "Makes a countdown longer, running or paused: for a late start, or a sub that adds time.",
		Params: []workflow.Param{which,
			{Name: "minutes", Label: "Minutes to add", Type: "number", Required: true}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			s, ok, err := minutes(c, "minutes")
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errors.New("say how many minutes to add")
			}
			return do(ctx, c, overlays.TimerOp{Action: "add", Seconds: s})
		},
	})
}
