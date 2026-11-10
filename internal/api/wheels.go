package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/wheels"
)

// Wheels (docs/adr/0030-wheels.md).

func wheelError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, wheels.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, wheels.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, wheels.ErrExists), errors.Is(err, wheels.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, wheels.ErrBusy):
		writeError(w, http.StatusTooManyRequests, CodeRateLimited, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

// WheelView is a wheel with its odds.
type WheelView struct {
	wheels.Wheel
	Odds []wheels.Odds `json:"odds"`
}

// WheelsView is the body of GET /v1/wheels.
type WheelsView struct {
	Wheels []WheelView   `json:"wheels"`
	Spins  []wheels.Spin `json:"spins"`
	Pages  int           `json:"pages"` // wheel overlays open in OBS
}

func wheelView(w wheels.Wheel) WheelView { return WheelView{Wheel: w, Odds: wheels.OddsOf(w)} }

func wheelRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/wheels",
			Summary:    "Every wheel with its exact odds, the newest spins (?wheel= for one wheel's), and how many wheel overlays are open in OBS.",
			Permission: PermWheelsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Wheels.List(r.Context())
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				spins, err := d.Wheels.Spins(r.Context(), r.URL.Query().Get("wheel"), 25)
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				out := WheelsView{Wheels: []WheelView{}, Spins: spins}
				for _, x := range list {
					out.Wheels = append(out.Wheels, wheelView(x))
				}
				if d.Hub != nil {
					out.Pages = d.Hub.WheelPages()
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "POST", Path: "/v1/wheels",
			Summary:    "Add a wheel {name, title, choices: [{label, weight?, color?}], spin_seconds?}. A taken name is refused (409).",
			Permission: PermWheelsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Name string `json:"name"`
					wheels.Settings
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				x, err := d.Wheels.Create(r.Context(), in.Name, in.Settings)
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				d.Logger.Info("wheel added", "wheel", x.Name)
				writeJSON(w, http.StatusCreated, wheelView(x))
			},
		},
		{
			Method: "PUT", Path: "/v1/wheels/{name}",
			Summary:    "Change a wheel {title, choices, spin_seconds?}. A change makes a new version; spins keep the version they were spun from.",
			Permission: PermWheelsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in wheels.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				x, err := d.Wheels.Update(r.Context(), r.PathValue("name"), in)
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, wheelView(x))
			},
		},
		{
			Method: "DELETE", Path: "/v1/wheels/{name}",
			Summary:    "Delete a wheel. Its spins are kept.",
			Permission: PermWheelsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				ok, err := d.Wheels.Delete(r.Context(), r.PathValue("name"))
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				if !ok {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such wheel")
					return
				}
				writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
			},
		},
		{
			Method: "POST", Path: "/v1/wheels/{name}/spin",
			Summary: "Spin a wheel {request_id}. The result is drawn and recorded at once and answered (202); the wheel overlay then " +
				"spins to it, and settled_at is set when it stops. Repeating a request_id returns the first spin (200).",
			Permission: PermWheelsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.RequestID == "" {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "request_id is required: a new id for each spin")
					return
				}
				sp, repeated, err := d.Wheels.Spin(r.Context(), wheels.SpinRequest{Wheel: r.PathValue("name"), Key: "api:" + in.RequestID,
					By: "the app"})
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				status := http.StatusAccepted
				if repeated {
					status = http.StatusOK
				}
				writeJSON(w, status, sp)
			},
		},
		{
			Method: "GET", Path: "/v1/spins/{id}",
			Summary:    "One spin: the wheel as it was, the number drawn, the result, and whether it has stopped.",
			Permission: PermWheelsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				sp, err := d.Wheels.GetSpin(r.Context(), r.PathValue("id"))
				if err != nil {
					wheelError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, sp)
			},
		},
	}
}
