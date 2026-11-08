package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/obs"
)

// OBSConfigRequest is the body of PUT /v1/connections/obs. Password is
// written only when given; ClearPassword removes it. Neither is ever returned.
type OBSConfigRequest struct {
	URL           string  `json:"url"`
	Enabled       bool    `json:"enabled"`
	Password      *string `json:"password,omitempty"`
	ClearPassword bool    `json:"clear_password,omitempty"`
}

// OBSScenes is the body of GET /v1/obs/scenes.
type OBSScenes struct {
	Current string   `json:"current"`
	Scenes  []string `json:"scenes"`
}

type obsSwitch struct {
	Scene string `json:"scene"`
}

type obsVisibility struct {
	Scene   string `json:"scene"`
	Source  string `json:"source"`
	Visible bool   `json:"visible"`
}

type obsSnapshotRequest struct {
	Key   string `json:"key"`
	Scene string `json:"scene"`
}

// obsError answers an OBS failure. An unknown outcome is never reported as
// success or failure: the request may or may not have happened.
func obsError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var re *obs.RequestError
	switch {
	case errors.Is(err, obs.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, obs.ErrNotConnected):
		writeError(w, http.StatusConflict, CodeNotConnected, "OBS isn't connected, so nothing was changed.")
	case errors.Is(err, obs.ErrOutcomeUnknown):
		writeError(w, http.StatusGatewayTimeout, CodeUnknownOutcome, err.Error())
	case errors.Is(err, obs.ErrNoSnapshot):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case obs.IsNotFound(err):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.As(err, &re):
		writeError(w, http.StatusBadGateway, CodeUnavailable, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

func obsRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "PUT", Path: "/v1/connections/obs",
			Summary:    "OBS's WebSocket address, on or off, and its password (write-only). Reconnects at once.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in OBSConfigRequest
				if !decodeJSON(w, r, &in) {
					return
				}
				pw := in.Password
				if in.ClearPassword {
					empty := ""
					pw = &empty
				} else if pw != nil && *pw == "" {
					pw = nil // an empty box keeps the saved password
				}
				if err := d.OBS.Configure(r.Context(), obs.Config{URL: in.URL, Enabled: in.Enabled}, pw); err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, d.OBS.Status())
			},
		},
		{
			Method: "GET", Path: "/v1/obs/scenes", Summary: "OBS's scenes, top first, and the current one. Live from OBS.",
			Permission: PermOBSRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				cur, scenes, err := d.OBS.Scenes(r.Context())
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, OBSScenes{Current: cur, Scenes: scenes})
			},
		},
		{
			Method: "GET", Path: "/v1/obs/scene-items", Summary: "The sources in ?scene=, top first, with whether each is visible.",
			Permission: PermOBSRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				items, err := d.OBS.SceneItems(r.Context(), r.URL.Query().Get("scene"))
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"items": items})
			},
		},
		{
			Method: "POST", Path: "/v1/obs/scene", Summary: "Switch OBS to a scene. Safe to repeat.",
			Permission: PermOBSControl,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in obsSwitch
				if !decodeJSON(w, r, &in) {
					return
				}
				prev, err := d.OBS.SwitchScene(r.Context(), in.Scene, "app")
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"scene": in.Scene, "previous_scene": prev})
			},
		},
		{
			Method: "PUT", Path: "/v1/obs/scene-items/visibility", Summary: "Show or hide a source in a scene. Safe to repeat.",
			Permission: PermOBSControl,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in obsVisibility
				if !decodeJSON(w, r, &in) {
					return
				}
				prev, err := d.OBS.SetSourceVisible(r.Context(), in.Scene, in.Source, in.Visible)
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"visible": in.Visible, "previous_visible": prev})
			},
		},
		{
			Method: "GET", Path: "/v1/obs/snapshots", Summary: "Open scene snapshots, each with whether switching back would happen now.",
			Permission: PermOBSRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				open, err := d.OBS.Snapshots().Open(r.Context())
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"snapshots": open})
			},
		},
		{
			Method: "POST", Path: "/v1/obs/snapshots",
			Summary:    "Remember the current scene under key and switch to scene. Repeating it while StreamGremlin holds the scene reuses it.",
			Permission: PermOBSControl,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in obsSnapshotRequest
				if !decodeJSON(w, r, &in) {
					return
				}
				snap, err := d.OBS.Snapshots().Take(r.Context(), in.Key, in.Scene)
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, snap)
			},
		},
		{
			Method: "POST", Path: "/v1/obs/snapshots/{key}/restore",
			Summary:    "Switch back, only if StreamGremlin still owns the scene; otherwise settle as skipped with the reason.",
			Permission: PermOBSControl,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				snap, err := d.OBS.Snapshots().Restore(r.Context(), r.PathValue("key"))
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, snap)
			},
		},
		{
			Method: "POST", Path: "/v1/obs/snapshots/{key}/release", Summary: "Close a snapshot and stay on the current scene.",
			Permission: PermOBSControl,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				snap, err := d.OBS.Snapshots().Release(r.Context(), r.PathValue("key"))
				if err != nil {
					obsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, snap)
			},
		},
	}
}
