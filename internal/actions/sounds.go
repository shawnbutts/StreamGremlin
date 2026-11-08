package actions

import (
	"context"
	"errors"
	"fmt"

	"code.streamgremlin.com/streamgremlin/internal/sounds"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerSounds adds the sound steps (docs/adr/0016-sounds.md). They're
// External: a test event never makes a sound, and a retried step doesn't
// play again.
func registerSounds(reg *workflow.Registry, lib *sounds.Store, player *sounds.Player) {
	reg.Add(workflow.Action{
		Name: "sounds.play", Label: "Play a sound", Group: "Sounds", External: true,
		Description: "Plays a sound from your library in the StreamGremlin window, at its own volume or another. " +
			"Fails when no window is open. OBS hears it through Desktop Audio.",
		Params: []workflow.Param{
			{Name: "sound", Label: "Sound", Type: "text", Required: true, Editor: "sound",
				Pattern: sounds.NamePattern, PatternHint: "a sound's name, like airhorn"},
			{Name: "volume", Label: "Volume (0 to 100)", Type: "number", DefaultHint: "The sound's own volume"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			name, _ := c.String("sound", "")
			s, err := lib.Get(ctx, name)
			if errors.Is(err, sounds.ErrNotFound) {
				return nil, fmt.Errorf("there's no sound %q in your library", name)
			}
			if err != nil {
				return nil, err
			}
			volume := s.Volume
			if v, ok := c.Args["volume"]; ok && v != nil {
				n, ok := number(v)
				if !ok || n < 0 || n > 100 {
					return nil, errors.New("the volume is 0 to 100")
				}
				volume = n
			}
			res, err := player.Play(ctx, s, volume, c.IdempotencyKey)
			if err != nil {
				return res, err
			}
			return res, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "sounds.stop", Label: "Stop sounds", Group: "Sounds", External: true, More: true,
		Description: "Stops every sound playing in the StreamGremlin window.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			return map[string]any{"stopped": true}, player.Stop()
		},
	})
}

func number(v any) (int, bool) {
	switch x := v.(type) {
	case int64:
		return int(x), true
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
	}
	return 0, false
}
