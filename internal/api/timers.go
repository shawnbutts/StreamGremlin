package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/timers"
)

func timerError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, timers.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, timers.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, timers.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such timer")
	default:
		internalError(w, r, d.Logger, err)
	}
}

// TimerList is the body of GET /v1/timers.
type TimerList struct {
	Timers []timers.Status `json:"timers"`
	// Live is whether OBS last said it's streaming, for "only while live";
	// nil when OBS hasn't said either way.
	Live *bool `json:"live,omitempty"`
}

func timerRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/timers",
			Summary:    "Every timer, by name, with when it goes off next, and whether OBS says you're live.",
			Permission: PermTimersRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Timers.Statuses(r.Context())
				if err != nil {
					timerError(w, r, d, err)
					return
				}
				out := TimerList{Timers: list}
				if live, ok := d.Timers.Live(r.Context()); ok {
					out.Live = &live
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "PUT", Path: "/v1/timers/{id}",
			Summary: "Create or replace a timer: {name, enabled, every_seconds and up_to_seconds, or cron, chat_lines, live_only, once}. " +
				"The count is kept; a new timer may set it (counter). Saving it unchanged is harmless and keeps its schedule.",
			Permission: PermTimersWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in timers.Timer
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.ID != "" && in.ID != r.PathValue("id") {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the id in the body doesn't match the path")
					return
				}
				in.ID = r.PathValue("id")
				t, created, err := d.Timers.Save(r.Context(), in, d.Now())
				if err != nil {
					timerError(w, r, d, err)
					return
				}
				d.Timers.Changed()
				st, err := d.Timers.StatusOf(r.Context(), t.ID)
				if err != nil {
					timerError(w, r, d, err)
					return
				}
				code := http.StatusOK
				if created {
					code = http.StatusCreated
					d.Logger.Info("timer created", "timer", t.ID, "enabled", t.Enabled)
				} else {
					d.Logger.Info("timer saved", "timer", t.ID, "enabled", t.Enabled)
				}
				writeJSON(w, code, st)
			},
		},
		{
			Method: "DELETE", Path: "/v1/timers/{id}",
			Summary:    "Delete a timer. Workflows that start from it are kept, and never start from it again.",
			Permission: PermTimersWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				id := r.PathValue("id")
				existed, err := d.Timers.Delete(r.Context(), id)
				if err != nil {
					timerError(w, r, d, err)
					return
				}
				if !existed {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such timer")
					return
				}
				d.Timers.Changed()
				d.Logger.Info("timer deleted", "timer", id)
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "POST", Path: "/v1/timers/{id}/fire",
			Summary: "Run now: the timer goes off at once, even when it's off. Its workflows run for real and its count goes up; " +
				"its schedule doesn't change. Each request is another turn. Answers the timer and the event.",
			Permission: PermTimersWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				t, rec, err := d.Timers.Fire(r.Context(), r.PathValue("id"))
				if err != nil {
					timerError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"timer": t, "event": rec})
			},
		},
	}
}
