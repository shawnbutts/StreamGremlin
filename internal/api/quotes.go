package api

import (
	"errors"
	"net/http"
	"strconv"

	"code.streamgremlin.com/streamgremlin/internal/quotes"
)

func quoteError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, quotes.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, quotes.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, quotes.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such quote")
	default:
		internalError(w, r, d.Logger, err)
	}
}

// quoteID reads {id} as a quote number (5 or #5).
func quoteID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, ok := quotes.ParseNumber(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such quote")
	}
	return n, ok
}

// QuoteList is the body of GET /v1/quotes.
type QuoteList struct {
	Quotes []quotes.Quote `json:"quotes"`
	Count  int            `json:"count"` // all quotes, not just those listed
	Next   int64          `json:"next"`  // the number the next quote gets
}

func quoteRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/quotes",
			Summary:    "Quotes, newest first. ?q= finds text, who added it, the category, or a number; ?limit= 1-1000 (default 500).",
			Permission: PermQuotesRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 500, 1, 1000)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 1000")
					return
				}
				list, err := d.Quotes.List(r.Context(), r.URL.Query().Get("q"), limit)
				if err != nil {
					quoteError(w, r, d, err)
					return
				}
				count, err := d.Quotes.Count(r.Context())
				if err != nil {
					quoteError(w, r, d, err)
					return
				}
				next, err := d.Quotes.NextNumber(r.Context())
				if err != nil {
					quoteError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, QuoteList{Quotes: list, Count: count, Next: next})
			},
		},
		{
			Method: "POST", Path: "/v1/quotes",
			Summary: "Add a quote: {text, added_by, category, request_id}. Repeating a request_id returns the first quote (200) " +
				"instead of adding another (201).",
			Permission: PermQuotesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in quotes.New
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.AddedBy == "" {
					in.AddedBy = "you"
				}
				q, created, err := d.Quotes.Add(r.Context(), in, d.Now())
				if err != nil {
					quoteError(w, r, d, err)
					return
				}
				code := http.StatusOK
				if created {
					code = http.StatusCreated
					d.Logger.Info("quote added", "quote", q.ID, "by", "you")
				}
				writeJSON(w, code, q)
			},
		},
		{
			Method: "PUT", Path: "/v1/quotes/{id}",
			Summary:    "Change a quote: {text, added_by, category}. Its number and date stay. Repeating it is harmless.",
			Permission: PermQuotesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				id, ok := quoteID(w, r)
				if !ok {
					return
				}
				var in struct {
					Text     string `json:"text"`
					AddedBy  string `json:"added_by"`
					Category string `json:"category"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				q, err := d.Quotes.Edit(r.Context(), id, in.Text, in.AddedBy, in.Category, d.Now())
				if err != nil {
					quoteError(w, r, d, err)
					return
				}
				d.Logger.Info("quote changed", "quote", id)
				writeJSON(w, http.StatusOK, q)
			},
		},
		{
			Method: "DELETE", Path: "/v1/quotes/{id}",
			Summary:    "Delete a quote. Later quotes keep their numbers, and its number isn't used again.",
			Permission: PermQuotesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				id, ok := quoteID(w, r)
				if !ok {
					return
				}
				deleted, err := d.Quotes.Delete(r.Context(), id)
				if err != nil {
					quoteError(w, r, d, err)
					return
				}
				if !deleted {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such quote")
					return
				}
				d.Logger.Info("quote deleted", "quote", strconv.FormatInt(id, 10))
				w.WriteHeader(http.StatusNoContent)
			},
		},
	}
}
