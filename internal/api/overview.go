package api

import (
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/overview"
)

func overviewRoutes(d Deps) []Route {
	return []Route{{
		Method: "GET", Path: "/v1/overview",
		Summary:    "What Home shows: the Get set up checklist, ticked from real state, what needs you now, and workflow counts.",
		Permission: PermHealthRead,
		Handler: func(w http.ResponseWriter, r *http.Request) {
			stats, err := d.Workflows.Stats(r.Context())
			if err != nil {
				internalError(w, r, d.Logger, err)
				return
			}
			cmds, err := d.Workflows.Commands(r.Context())
			if err != nil {
				internalError(w, r, d.Logger, err)
				return
			}
			writeJSON(w, http.StatusOK, overview.Build(overview.Input{
				Twitch: d.Twitch.Status(r.Context()), OBS: d.OBS.Status(), Workflows: stats, Commands: cmds,
			}))
		},
	}}
}
