package api

import (
	"net/http"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overview"
)

// homeSettings is what Home remembers: when failed runs were marked seen.
type homeSettings struct {
	FailuresSeenAt time.Time `json:"failures_seen_at"`
}

const homeSettingsKey = "home"

func overviewRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/overview",
			Summary:    "What Home shows: the Get set up checklist, ticked from real state, what needs you now, and workflow counts.",
			Permission: PermHealthRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var home homeSettings
				if _, err := d.Settings.Load(r.Context(), homeSettingsKey, &home); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				stats, err := d.Workflows.StatsSince(r.Context(), home.FailuresSeenAt)
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
		},
		{
			Method: "POST", Path: "/v1/overview/failures-seen",
			Summary: "Mark the failed workflow runs so far as seen: Home's notice then counts only failures after now. " +
				"The runs and their traces are kept.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				// The same clock as the runs' times (the engine's, the real one).
				now := time.Now()
				if err := d.Settings.Save(r.Context(), homeSettingsKey, homeSettings{FailuresSeenAt: now}); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"seen_at": now})
			},
		},
	}
}
