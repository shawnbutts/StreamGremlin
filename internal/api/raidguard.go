package api

import (
	"errors"
	"net/http"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/raidguard"
)

// The raid guard (M8 slice 3, D-107).

// RaidGuardView is the body of GET and PUT /v1/raid-guard.
type RaidGuardView struct {
	Settings raidguard.Settings `json:"settings"`
	// Alert is the latest burst, until it's dismissed or 15 minutes pass.
	Alert *raidguard.Alert `json:"alert"`
}

func raidGuardRoutes(d Deps) []Route {
	if d.RaidGuard == nil {
		return nil
	}
	view := func(w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, RaidGuardView{Settings: d.RaidGuard.Settings(), Alert: d.RaidGuard.Alert()})
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/raid-guard",
			Summary: "The raid guard's settings (mode off, alert or lock; how many first-time chatters, or Twitch accounts younger " +
				"than days, within window_seconds is a burst; a closer watch for after_raid_minutes after a raid) and the latest alert.",
			Permission: PermModerationRead,
			Handler:    func(w http.ResponseWriter, r *http.Request) { view(w) },
		},
		{
			Method: "PUT", Path: "/v1/raid-guard",
			Summary:    "Save the raid guard's settings, all at once (GET's settings, changed).",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in raidguard.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				if _, err := d.RaidGuard.SetSettings(r.Context(), in); err != nil {
					if errors.Is(err, raidguard.ErrInvalid) {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), "raidguard: invalid: "))
						return
					}
					internalError(w, r, d.Logger, err)
					return
				}
				view(w)
			},
		},
		{
			Method: "POST", Path: "/v1/raid-guard/test",
			Summary:    "Show a test alert on Live, stored as a test event: nothing is locked, and no workflow runs outside a test.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				d.RaidGuard.Test(r.Context())
				view(w)
			},
		},
		{
			Method: "POST", Path: "/v1/raid-guard/dismiss",
			Summary:    "Hide the alert on Live. The event stays in the log.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				d.RaidGuard.Dismiss()
				view(w)
			},
		},
	}
}
