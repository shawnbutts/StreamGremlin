package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/ads"
	"code.streamgremlin.com/streamgremlin/internal/buildinfo"
	"code.streamgremlin.com/streamgremlin/internal/earning"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/firstwords"
	"code.streamgremlin.com/streamgremlin/internal/giveaways"
	"code.streamgremlin.com/streamgremlin/internal/gives"
	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/maintenance"
	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/packages"
	"code.streamgremlin.com/streamgremlin/internal/plugins"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/quotes"
	"code.streamgremlin.com/streamgremlin/internal/rounds"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/sounds"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/timers"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/wheels"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Deps are the services the routes use.
type Deps struct {
	DataDir   string
	StartedAt time.Time
	DB        *storage.DB
	Settings  *settings.Store
	Secrets   *secrets.Store
	Events    *events.Inbox
	Twitch    *twitch.Service
	OBS       *obs.Service
	Workflows *workflow.Engine
	Variables *variables.Store
	Timers    *timers.Service
	Quotes    *quotes.Store
	Sounds    *sounds.Store
	Player    *sounds.Player
	Overlays  *overlays.Store
	Hub       *overlays.Hub
	// Packages are installed themes (and later plugins); Themes is what
	// overlays may show, refreshed as packages change.
	Packages *packages.Store
	Themes   *overlays.Catalogue
	// OverlayServer is where overlay pages are served ("" when off), and
	// a note when it isn't the usual address.
	OverlayServer func() (base, note string)
	FirstWords    *firstwords.Service
	Ads           *ads.Service
	Giveaways     *giveaways.Service
	Wheels        *wheels.Service
	Viewers       *viewers.Service
	Points        *points.Ledger
	Earning       *earning.Service
	Gives         *gives.Service
	// Lua runs scripts tried from the step editor.
	Lua *lua.Runner
	// Plugins runs installed Lua plugins.
	Plugins *plugins.Manager
	// Rounds are plugins' rounds (ADR-0039).
	Rounds *rounds.Service
	// Maintenance runs backups and sweeps; nil in tests that don't need it.
	Maintenance *maintenance.Service
	Now         func() time.Time
	Logger      *slog.Logger
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
			Method: "PUT", Path: "/v1/settings/ui",
			Summary: "Change the appearance settings {theme?, text_size?, density?, more?}; fields left out keep what's saved. " +
				"Repeating a request is harmless.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				// Fields left out keep what's saved.
				in, err := d.Settings.UI(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
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
	}, append(append(append(append(secretRoutes(d), eventRoutes(d)...), connectionRoutes(d)...), obsRoutes(d)...), append(append(append(workflowRoutes(d), variableRoutes(d)...), overviewRoutes(d)...), append(append(append(backupRoutes(d), timerRoutes(d)...), quoteRoutes(d)...), append(append(append(soundRoutes(d), overlayRoutes(d)...), rewardRoutes(d)...), append(append(firstWordsRoutes(d), pollRoutes(d)...), append(append(adRoutes(d), giveawayRoutes(d)...), append(append(wheelRoutes(d), pointsRoutes(d)...), append(append(packageRoutes(d), pluginRoutes(d)...), luaRoutes(d)...)...)...)...)...)...)...)...)...)
}

func internalError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	log.Error("api handler failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, CodeInternal, "something went wrong; see the engine log")
}
