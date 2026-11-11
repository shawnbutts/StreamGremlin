package api

import (
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/packages"
)

// Lua plugins (docs/adr/0038-lua-plugins.md): on and off, settings, and
// what each has asked StreamGremlin to do.

func pluginRoutes(d Deps) []Route {
	plugin := func(w http.ResponseWriter, r *http.Request) (packages.Package, bool) {
		p, _, err := d.Packages.Plugin(r.Context(), r.PathValue("id"))
		if err != nil {
			packageError(w, r, d, err)
			return p, false
		}
		if d.Plugins == nil {
			writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "plugins aren't available")
			return p, false
		}
		return p, true
	}
	return []Route{
		{
			Method: "PUT", Path: "/v1/packages/{id}/enabled",
			Summary: "Turn a Lua plugin on (loading its script: one that won't load is 409, and it's left off saying why) or off " +
				"(its worker stops, and its memory with it; its workflows stay on, and its steps fail while it's off). Body: {\"enabled\": true}.",
			Permission: PermPackagesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Enabled *bool `json:"enabled"`
				}
				if !decodeJSON(w, r, &body) {
					return
				}
				if body.Enabled == nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "enabled is true or false")
					return
				}
				if _, ok := plugin(w, r); !ok {
					return
				}
				if err := d.Plugins.SetEnabled(r.Context(), r.PathValue("id"), *body.Enabled); err != nil {
					d.Logger.Warn("plugin not turned on", "plugin", r.PathValue("id"), "err", err)
					packageError(w, r, d, err)
					return
				}
				p, err := d.Packages.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				d.Logger.Info("plugin switched", "plugin", p.ID, "on", *body.Enabled)
				writeJSON(w, http.StatusOK, p)
			},
		},
		{
			Method: "PUT", Path: "/v1/packages/{id}/settings",
			Summary: "Change a Lua plugin's settings, each checked against its manifest; ones not given keep their value. A plugin " +
				"that's on starts again, fresh, to read them. Body: {\"settings\": {\"key\": value}}.",
			Permission: PermPackagesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Settings map[string]any `json:"settings"`
				}
				if !decodeJSON(w, r, &body) {
					return
				}
				if _, ok := plugin(w, r); !ok {
					return
				}
				id := r.PathValue("id")
				if _, err := d.Packages.SetSettings(r.Context(), id, body.Settings, d.Now()); err != nil {
					packageError(w, r, d, err)
					return
				}
				if err := d.Plugins.Restart(r.Context(), id); err != nil {
					// Saved, but it didn't load with them: it's off now, saying why.
					d.Logger.Warn("plugin didn't load with its new settings", "plugin", id, "err", err)
				}
				p, err := d.Packages.Get(r.Context(), id)
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, p)
			},
		},
		{
			Method: "GET", Path: "/v1/packages/{id}/rounds",
			Summary: "A Lua plugin's rounds (docs/adr/0039-points-for-plugins.md), newest first: each with its title, state (open, " +
				"settling, settled, refunded and why), the rules as the plugin stated them, every stake, every roll StreamGremlin made, and " +
				"the payouts. Kept after the plugin is removed. ?limit= 1-200 (20).",
			Permission: PermPackagesRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 20, 1, 200)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit is 1 to 200")
					return
				}
				if d.Rounds == nil {
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "rounds aren't available")
					return
				}
				list, err := d.Rounds.List(r.Context(), r.PathValue("id"), limit)
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"rounds": list})
			},
		},
		{
			Method: "GET", Path: "/v1/packages/{id}/activity",
			Summary: "What a Lua plugin has asked StreamGremlin to do, newest first: each chat message (sent, refused and why, or " +
				"failed) and each line it logged. The last 1,000 are kept. ?limit= 1-1000 (100).",
			Permission: PermPackagesRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 100, 1, packages.MaxActivity)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit is 1 to 1000")
					return
				}
				list, err := d.Packages.ActivityOf(r.Context(), r.PathValue("id"), limit)
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"activity": list})
			},
		},
	}
}
