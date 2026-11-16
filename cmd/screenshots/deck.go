package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
)

// deckChoices fills Connections' "What your Stream Deck can use" (D-123,
// D-124) with made-up choices: a death counter, the Hydrate timer and the
// chat lock as switches, a workflow to run, scenes, a countdown and ads.
func deckChoices(ctx context.Context, c *client.Client, _ *mocktwitch.Server) error {
	if err := c.Do(ctx, "PUT", "/v1/variables", map[string]any{"scope": "global", "owner": "", "name": "deaths", "value": 7}, nil); err != nil {
		return err
	}
	if err := c.Do(ctx, "POST", "/v1/overlays", map[string]string{"name": "starting", "kind": "timer"}, nil); err != nil {
		return err
	}
	var list struct {
		Overlays []struct {
			Name string         `json:"name"`
			Look map[string]any `json:"look"`
		} `json:"overlays"`
	}
	if err := c.Do(ctx, "GET", "/v1/overlays", nil, &list); err != nil {
		return err
	}
	for _, o := range list.Overlays {
		if o.Name == "starting" {
			o.Look["timer_mode"], o.Look["timer_label"], o.Look["timer_length"] = "countdown", "Starting soon", 300
			if err := c.Do(ctx, "PUT", "/v1/overlays/starting", map[string]any{"look": o.Look}, nil); err != nil {
				return err
			}
		}
	}
	// A workflow to run that needs no viewer: the Socials reminder, which
	// the Socials timer starts.
	raw, err := os.ReadFile(filepath.Join("examples", "workflows", "socials.json"))
	if err != nil {
		return err
	}
	if err := c.DoRaw(ctx, "PUT", "/v1/workflows/timer.socials", raw, nil); err != nil {
		return err
	}
	var wfs struct {
		Workflows []struct {
			ID       string `json:"id"`
			Triggers []struct {
				Type string `json:"type"`
			} `json:"triggers"`
		} `json:"workflows"`
	}
	if err := c.Do(ctx, "GET", "/v1/workflows", nil, &wfs); err != nil {
		return err
	}
	runs := []string{}
	for _, w := range wfs.Workflows {
		for _, t := range w.Triggers {
			if slices.Contains([]string{events.ButtonPressed, events.TimerFired}, t.Type) && len(runs) == 0 {
				runs = append(runs, w.ID)
			}
		}
	}
	if len(runs) == 0 {
		return fmt.Errorf("no workflow started by a button or timer to run")
	}
	return c.Do(ctx, "PUT", "/v1/settings/streamdeck", map[string]any{
		"counters": []string{"deaths"},
		"switches": []map[string]string{{"kind": "timer", "id": "hydrate"}, {"kind": "chat_lock"}},
		"runs":     runs, "scenes": true, "countdowns": []string{"starting"}, "ads": true,
	}, nil)
}
