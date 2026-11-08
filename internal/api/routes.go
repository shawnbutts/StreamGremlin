package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/buildinfo"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Deps are the services the routes use.
type Deps struct {
	DataDir   string
	StartedAt time.Time
	DB        *storage.DB
	Settings  *settings.Store
	Secrets   *secrets.Store
	Now       func() time.Time
	Logger    *slog.Logger
}

// Status is the body of GET /v1/status.
type Status struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	StartedAt     string `json:"started_at"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	DataDir       string `json:"data_dir"`
	SchemaVersion int    `json:"schema_version"`
}

// Routes returns every route of the API.
func Routes(d Deps) []Route {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return append([]Route{
		{
			Method: "GET", Path: "/v1/health", Summary: "Liveness check. Public; says only ok.",
			CLIOnly: "unauthenticated liveness probe for scripts and tests; the app uses /v1/status",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			},
		},
		{
			Method: "GET", Path: "/v1/status", Summary: "Engine version, uptime, data folder and schema version.",
			Permission: PermHealthRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v, err := d.DB.SchemaVersion(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, Status{
					Status:        "running",
					Version:       buildinfo.Version,
					Commit:        buildinfo.Commit,
					StartedAt:     storage.Timestamp(d.StartedAt),
					UptimeSeconds: int64(d.Now().Sub(d.StartedAt).Seconds()),
					DataDir:       d.DataDir,
					SchemaVersion: v,
				})
			},
		},
		{
			Method: "GET", Path: "/v1/settings/ui", Summary: "The desktop app's appearance settings.",
			Permission: PermSettingsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				ui, err := d.Settings.UI(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, ui)
			},
		},
		{
			Method: "PUT", Path: "/v1/settings/ui", Summary: "Replace the appearance settings. Repeating a request is harmless.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in settings.UI
				if !decodeJSON(w, r, &in) {
					return
				}
				ui, err := d.Settings.SetUI(r.Context(), in)
				if errors.Is(err, settings.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
					return
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, ui)
			},
		},
	}, secretRoutes(d)...)
}

func internalError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	log.Error("api handler failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, CodeInternal, "something went wrong; see the engine log")
}
