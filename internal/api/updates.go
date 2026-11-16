package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/updates"
)

func updatesError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, updates.ErrDev), errors.Is(err, updates.ErrNothing), errors.Is(err, updates.ErrNotInstalling),
		errors.Is(err, updates.ErrBusy), errors.Is(err, updates.ErrNotReady), errors.Is(err, updates.ErrLive):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

// Installing is the desktop app's (POST /desktop/install-update): only it
// can quit and reopen the app. The CLI and the app both check and download
// through the API.
func updateRoutes(d Deps) []Route {
	if d.Updates == nil {
		return nil
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/updates",
			Summary: "Whether a newer StreamGremlin is out: the running version, the switch, the last check (state up_to_date, available, " +
				"failed, not_checked, off, checking or dev), the newest release with its notes and whether the app can install it, and any download.",
			Permission: PermSettingsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, d.Updates.Status(r.Context()))
			},
		},
		{
			Method: "POST", Path: "/v1/updates/check",
			Summary: "Check GitHub for a newer version now, even with checking off. Asking again within a minute, or before GitHub's " +
				"rate limit allows, answers the last check. A failed check is in the answer's error; 409 for a development build.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				st, err := d.Updates.Check(r.Context())
				if errors.Is(err, updates.ErrDev) {
					updatesError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, st)
			},
		},
		{
			Method: "POST", Path: "/v1/updates/download",
			Summary: "Download the newest version in the background and check its signature; GET /v1/updates shows how it's going. " +
				"Repeating it while it runs or once it's ready changes nothing. 409 when there's nothing the app can install.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.Updates.Download(); err != nil {
					updatesError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusAccepted, d.Updates.Status(r.Context()))
			},
		},
		{
			Method: "PUT", Path: "/v1/settings/updates", Summary: "Turn checking for updates once a day on or off {check}.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Check *bool `json:"check"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.Check == nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "say whether to check: {\"check\": true} or false")
					return
				}
				st, err := d.Updates.SetSettings(r.Context(), updates.Settings{Check: *in.Check})
				if err != nil {
					updatesError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, st)
			},
		},
	}
}
