package api

import (
	"net/http"
)

func firstWordsRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/first-words",
			Summary: "Where First Words stand: since when this stream counts (going live, a reset, or 12 quiet hours), " +
				"and how many viewers have chatted since.",
			Permission: PermEventsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, d.FirstWords.Status())
			},
		},
		{
			Method: "POST", Path: "/v1/first-words/reset",
			Summary:    "Start First Words over: everyone's next message counts as their first words again. Repeating it is harmless.",
			Permission: PermFirstWordsReset,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				st, err := d.FirstWords.Reset(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				d.Logger.Info("first words reset from the api")
				writeJSON(w, http.StatusOK, st)
			},
		},
	}
}
