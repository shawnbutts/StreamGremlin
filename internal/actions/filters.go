package actions

import (
	"context"
	"errors"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/filters"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerFilters adds the chat filters' step (D-085): !permit.
func registerFilters(reg *workflow.Registry, svc *filters.Service) {
	reg.Add(workflow.Action{
		Name: "filters.permit", Label: "Let a viewer post a link", Group: "Twitch", SavesState: true, More: true,
		Description: "The link filter lets one viewer post links for a while (1 to 10 minutes; 1 if not set). " +
			"Only links: the other filters still apply. Forgotten when StreamGremlin restarts.",
		Params: []workflow.Param{
			{Name: "viewer", Label: "Viewer's name", Type: "text", Required: true, Editor: "message"},
			{Name: "minutes", Label: "For how many minutes (1 to 10)", Type: "number", DefaultHint: "1"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			login, _ := c.String("viewer", "")
			login = strings.TrimPrefix(strings.TrimSpace(login), "@")
			if login == "" || strings.ContainsAny(login, " \t") || len(login) > 25 {
				return nil, errors.New("name one viewer, like @friend")
			}
			minutes := 1
			if v, ok := c.Args["minutes"]; ok && v != nil {
				n, ok := number(v)
				if !ok || n < 1 || n > 10 {
					return nil, errors.New("a permit lasts 1 to 10 minutes")
				}
				minutes = n
			}
			platform := "twitch"
			if c.Actor != nil && c.Actor.Platform != "" {
				platform = c.Actor.Platform
			}
			out := map[string]any{"viewer": login, "minutes": minutes}
			if c.DryRun {
				out["test"] = true
				return out, nil
			}
			svc.Permit(platform, login, time.Duration(minutes)*time.Minute)
			return out, nil
		},
	})
}
