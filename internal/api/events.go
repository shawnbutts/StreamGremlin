package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// EventPage is the body of GET /v1/events.
type EventPage struct {
	Events []events.Record `json:"events"`
	// Head is the newest stored event's seq. A reader whose cursor is past
	// it is reading a different data folder, and starts again.
	Head int64 `json:"head"`
}

// EventCatalog is the body of GET /v1/events/catalog.
type EventCatalog struct {
	Kinds []events.Kind `json:"kinds"`
}

// SimulateResult is the body of POST /v1/events/simulate.
type SimulateResult struct {
	Event     events.Record `json:"event"`
	Duplicate bool          `json:"duplicate"`
}

// maxWait keeps a long poll inside the server's 30-second write timeout.
const maxWait = 20 * time.Second

func eventRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/events",
			Summary: "Stored events, oldest first. ?after=SEQ reads on from a cursor and ?wait=SECONDS (up to 20) waits for the next; " +
				"without after, the newest. ?limit= (1-200, default 50), ?types=chat.*,broadcast.scene.changed. " +
				"?q=TEXT (without after) finds the newest whose type, viewer or contents contain it.",
			Permission: PermEventsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				limit, err := intParam(q.Get("limit"), 50, 1, 200)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 200")
					return
				}
				wait, err := intParam(q.Get("wait"), 0, 0, int(maxWait/time.Second))
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "wait must be 0 to 20 seconds")
					return
				}
				var filter events.Filter
				if t := q.Get("types"); t != "" {
					for _, p := range strings.Split(t, ",") {
						if !events.ValidPattern(p) {
							writeError(w, http.StatusBadRequest, CodeInvalidRequest, "types: "+strconv.Quote(p)+" isn't a type or a prefix like chat.*")
							return
						}
						filter = append(filter, p)
					}
				}
				var recs []events.Record
				if text := strings.TrimSpace(q.Get("q")); text != "" {
					// A search: the newest matches, not a cursor to follow.
					if q.Get("after") != "" || utf8.RuneCountInString(text) > events.MaxSearch {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest, "q is up to 100 characters, without after")
						return
					}
					recs, err = d.Events.Search(r.Context(), text, limit, filter)
				} else if a := q.Get("after"); a != "" {
					after, perr := strconv.ParseInt(a, 10, 64)
					if perr != nil || after < 0 {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest, "after must be an event's seq")
						return
					}
					recs, err = d.Events.Wait(r.Context(), after, limit, filter, time.Duration(wait)*time.Second)
				} else {
					recs, err = d.Events.Latest(r.Context(), limit, filter)
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				head, err := d.Events.Head(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, EventPage{Events: recs, Head: head})
			},
		},
		{
			Method: "GET", Path: "/v1/events/catalog", Summary: "The event types this build knows, and the fields of their test events.",
			Permission: PermEventsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, EventCatalog{Kinds: events.Catalog()})
			},
		},
		{
			Method: "POST", Path: "/v1/events/simulate",
			Summary:    "Store a test event (delivery_quality synthetic). Repeating a request_id returns the first event.",
			Permission: PermEventsSimulate,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in events.Simulation
				if !decodeJSON(w, r, &in) {
					return
				}
				res, err := d.Events.Simulate(r.Context(), in)
				switch {
				case errors.Is(err, events.ErrInvalid):
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), "events: invalid event: "))
					return
				case err != nil:
					internalError(w, r, d.Logger, err)
					return
				}
				status := http.StatusCreated
				if res.Duplicate {
					status = http.StatusOK
				}
				writeJSON(w, status, SimulateResult{Event: res.Record, Duplicate: res.Duplicate})
			},
		},
	}
}

func intParam(s string, def, lo, hi int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, errors.New("out of range")
	}
	return n, nil
}
