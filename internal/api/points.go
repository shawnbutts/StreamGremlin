package api

import (
	"errors"
	"net/http"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/earning"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// Viewers and points (docs/adr/0032-viewers-and-points.md).

func pointsError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var short *points.InsufficientError
	switch {
	case errors.As(err, &short):
		writeError(w, http.StatusConflict, CodeConflict, short.Error())
	case errors.Is(err, points.ErrInvalid), errors.Is(err, viewers.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, points.ErrNotFound), errors.Is(err, viewers.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, points.ErrConflict), errors.Is(err, points.ErrReversed):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

// ViewerView is a viewer with their balance.
type ViewerView struct {
	viewers.Viewer
	Balance int64 `json:"balance"`
}

// ViewerDetail is a viewer with their balance and points history.
type ViewerDetail struct {
	ViewerView
	History []points.Transaction `json:"history"`
}

// PointsView is the body of GET /v1/points.
type PointsView struct {
	Currency points.Currency `json:"currency"`
	Summary  points.Summary  `json:"summary"`
	Top      []ViewerView    `json:"top"`
}

// ViewerExport is everything StreamGremlin keeps about a viewer.
type ViewerExport struct {
	Schema     string             `json:"schema"`
	ExportedAt time.Time          `json:"exported_at"`
	Viewer     viewers.Viewer     `json:"viewer"`
	Points     ViewerExportPoints `json:"points"`
}

// ViewerExportPoints is a viewer's points in an export.
type ViewerExportPoints struct {
	Currency string               `json:"currency"`
	Balance  int64                `json:"balance"`
	History  []points.Transaction `json:"history"`
}

// EarningView is the body of GET /v1/points/earning.
type EarningView struct {
	Rules earning.Rules `json:"rules"`
	Live  string        `json:"live"`
	// LastTick is the newest time-in-chat tick, if any.
	LastTick *earning.Tick `json:"last_tick,omitempty"`
}

func pointsRoutes(d Deps) []Route {
	view := func(r *http.Request, v viewers.Viewer) ViewerView {
		b, _ := d.Points.Balance(r.Context(), v.ID, "")
		return ViewerView{Viewer: v, Balance: b}
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/viewers",
			Summary:    "Viewers whose name starts with ?q= (all when empty), most recently seen first, with their points. ?limit= 1-500 (default 100).",
			Permission: PermViewersRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 100, 1, 500)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 500")
					return
				}
				list, err := d.Viewers.Search(r.Context(), r.URL.Query().Get("q"), limit)
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				out := []ViewerView{}
				for _, v := range list {
					out = append(out, view(r, v))
				}
				writeJSON(w, http.StatusOK, map[string]any{"viewers": out})
			},
		},
		{
			Method: "GET", Path: "/v1/viewers/{id}",
			Summary:    "One viewer, with their points and their newest 100 points transactions.",
			Permission: PermViewersRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v, err := d.Viewers.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				hist, err := d.Points.History(r.Context(), v.ID, 100)
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, ViewerDetail{ViewerView: view(r, v), History: hist})
			},
		},
		{
			Method: "GET", Path: "/v1/viewers/{id}/export",
			Summary:    "Everything kept about a viewer, as streamgremlin.viewer-export/v1: their record, points and full history.",
			Permission: PermViewersRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v, err := d.Viewers.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				hist, err := d.Points.History(r.Context(), v.ID, 1_000_000)
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				cur, _ := d.Points.Currency(r.Context(), "")
				b, _ := d.Points.Balance(r.Context(), v.ID, "")
				writeJSON(w, http.StatusOK, ViewerExport{Schema: "streamgremlin.viewer-export/v1", ExportedAt: d.Now(), Viewer: v,
					Points: ViewerExportPoints{Currency: cur.Name, Balance: b, History: hist}})
			},
		},
		{
			Method: "POST", Path: "/v1/viewers/{id}/forget",
			Summary: "Forget a viewer: their account id, names and activity are blanked. Their points history stays, unnamed, so the " +
				"ledger still adds up. If the account takes part again, it's a new viewer.",
			Permission: PermViewersWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v, err := d.Viewers.Forget(r.Context(), r.PathValue("id"))
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "POST", Path: "/v1/viewers/{id}/points",
			Summary: "Give (amount above 0) or take (below 0) a viewer's points by hand {amount, reason, request_id}. Not below zero (409). " +
				"Repeating a request_id does nothing more.",
			Permission: PermPointsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Amount    int64  `json:"amount"`
					Reason    string `json:"reason"`
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.RequestID == "" {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "request_id is required: a new id for each change")
					return
				}
				v, err := d.Viewers.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				res, err := d.Points.Adjust(r.Context(), points.Op{ID: "api:" + in.RequestID, Viewer: v.ID, Amount: in.Amount, Memo: in.Reason, By: "the app"})
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, res)
			},
		},
		{
			Method: "GET", Path: "/v1/points",
			Summary:    "The currency, how many points viewers hold, and the top 25.",
			Permission: PermPointsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				cur, err := d.Points.Currency(r.Context(), "")
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				sum, err := d.Points.Summarize(r.Context(), "")
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				top, err := d.Points.Top(r.Context(), "", 25)
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				out := PointsView{Currency: cur, Summary: sum, Top: []ViewerView{}}
				for _, h := range top {
					v, err := d.Viewers.Get(r.Context(), h.Viewer)
					if err != nil {
						v = viewers.Viewer{ID: h.Viewer, DisplayName: h.Viewer}
					}
					out.Top = append(out.Top, ViewerView{Viewer: v, Balance: h.Balance})
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "PUT", Path: "/v1/points/currency",
			Summary:    "Rename the points {name}, like Gold. Everything that uses them keeps working and shows the new name.",
			Permission: PermPointsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Name string `json:"name"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				c, err := d.Points.Rename(r.Context(), "", in.Name)
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, c)
			},
		},
		{
			Method: "GET", Path: "/v1/points/earning",
			Summary: "How viewers earn points {rules, live, last_tick}: chatting, time in chat, a first follow, subs by tier, gifted " +
				"subs and bits. live is live, offline or unknown, as far as earning has read; last_tick is the newest time-in-chat " +
				"payment, with a note when no one, or only chatters, were paid.",
			Permission: PermPointsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				rules, err := d.Earning.Rules(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				last, _ := d.Earning.LastTick(r.Context())
				writeJSON(w, http.StatusOK, EarningView{Rules: rules, Live: d.Earning.LiveState(), LastTick: last})
			},
		},
		{
			Method: "PUT", Path: "/v1/points/earning",
			Summary: "Change how viewers earn points (the rules, as GET returns them). They apply from the next event on; what " +
				"happened before never pays.",
			Permission: PermPointsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in earning.Rules
				if !decodeJSON(w, r, &in) {
					return
				}
				rules, err := d.Earning.SetRules(r.Context(), in)
				if errors.Is(err, earning.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
					return
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				last, _ := d.Earning.LastTick(r.Context())
				writeJSON(w, http.StatusOK, EarningView{Rules: rules, Live: d.Earning.LiveState(), LastTick: last})
			},
		},
		{
			Method: "POST", Path: "/v1/points/transactions/{id}/undo",
			Summary: "Undo a points transaction, once {request_id}: the viewer gets back what was taken, or loses what was given (not " +
				"below zero, 409). Already undone: 409.",
			Permission: PermPointsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.RequestID == "" {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "request_id is required")
					return
				}
				res, err := d.Points.Undo(r.Context(), r.PathValue("id"), points.Op{ID: "api:" + in.RequestID, By: "the app"})
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, res)
			},
		},
		{
			Method: "GET", Path: "/v1/points/audit",
			Summary:    "Check the ledger: every transaction balances and every balance matches its history. Lists what doesn't.",
			Permission: PermPointsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				bad, err := d.Points.Audit(r.Context())
				if err != nil {
					pointsError(w, r, d, err)
					return
				}
				if bad == nil {
					bad = []string{}
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": len(bad) == 0, "problems": bad, "checked_at": d.Now()})
			},
		},
	}
}
