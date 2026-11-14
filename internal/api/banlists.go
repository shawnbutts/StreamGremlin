package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/banlists"
)

// Ban lists (M8 slice 2, D-106, docs/adr/0049-ban-lists.md).

// BanListView is one list with how far it's got.
type BanListView struct {
	banlists.List
	// Words says the phase in words; Counts how many names are in each
	// state; Left how many bans and unbans are still to send.
	Words    string            `json:"words"`
	Counts   map[string]int    `json:"counts"`
	Left     int               `json:"left"`
	Progress banlists.Progress `json:"progress"`
}

// BanListImport is the body of POST /v1/ban-lists.
type BanListImport struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`
	// Copy starts the list from Twitch's own ban list instead of a file.
	Copy bool `json:"copy,omitempty"`
}

// MaxBanListFile is the most entries GET /v1/ban-lists/{id}/file returns.
const MaxBanListFile = banlists.MaxLines

func banListError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, banlists.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), "banlists: invalid: "))
	case errors.Is(err, banlists.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such ban list")
	case errors.Is(err, banlists.ErrBusy):
		writeError(w, http.StatusConflict, CodeConflict, strings.TrimPrefix(err.Error(), "banlists: the list is busy: "))
	default:
		internalError(w, r, d.Logger, err)
	}
}

func banListView(r *http.Request, d Deps, l banlists.List) (BanListView, error) {
	c, err := d.BanLists.Counts(r.Context(), l.ID)
	if err != nil {
		return BanListView{}, err
	}
	left := banlists.Left(l.Phase, c)
	return BanListView{List: l, Words: banlists.Describe(l), Counts: c, Left: left, Progress: d.BanLists.ProgressOf(l.ID, left)}, nil
}

func banListRoutes(d Deps) []Route {
	if d.BanLists == nil {
		return nil
	}
	one := func(w http.ResponseWriter, r *http.Request, l banlists.List, err error) {
		if err != nil {
			banListError(w, r, d, err)
			return
		}
		v, err := banListView(r, d, l)
		if err != nil {
			internalError(w, r, d.Logger, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
	action := func(path, summary string, f func(*http.Request, string) (banlists.List, error)) Route {
		return Route{Method: "POST", Path: "/v1/ban-lists/{id}/" + path, Summary: summary, Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				l, err := f(r, r.PathValue("id"))
				one(w, r, l, err)
			}}
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/ban-lists",
			Summary: "Your ban lists, newest first: each with its phase (reading, checking, ready, banning, undoing, copying, comparing, " +
				"done), paused and why, how many names are in each state, how many bans are left, the pace and the estimated finish, " +
				"and whether it waits until you're offline.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				lists, err := d.BanLists.All(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				out := []BanListView{}
				for _, l := range lists {
					v, err := banListView(r, d, l)
					if err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
					out = append(out, v)
				}
				writeJSON(w, http.StatusOK, map[string]any{"lists": out})
			},
		},
		{
			Method: "POST", Path: "/v1/ban-lists",
			Summary: "Import a ban list from a file on this computer {name, path (absolute), reason?}: one Twitch login or user id a line, " +
				"or a CSV with a login or id column, up to 5,000,000 lines. A list with that name is updated: only new names are " +
				"checked, and names gone from the file are offered for unbanning. Or {name, copy: true} copies Twitch's own ban list. " +
				"It's read and checked in the background; nothing is banned until it's confirmed.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in BanListImport
				if !decodeJSON(w, r, &in) {
					return
				}
				var l banlists.List
				var err error
				if in.Copy {
					l, err = d.BanLists.Copy(r.Context(), in.Name)
				} else {
					l, err = d.BanLists.Import(r.Context(), in.Name, in.Path, in.Reason)
				}
				one(w, r, l, err)
			},
		},
		{
			Method: "PATCH", Path: "/v1/ban-lists/{id}",
			Summary:    "Change a list's reason, whether it waits while you're live, and whether viewers with points are left alone: {reason?, pause_live?, keep_points?}.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in banlists.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				l, err := d.BanLists.Set(r.Context(), r.PathValue("id"), in)
				one(w, r, l, err)
			},
		},
		{
			Method: "DELETE", Path: "/v1/ban-lists/{id}",
			Summary:    "Forget a ban list. Nobody is unbanned: undo it first for that.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.BanLists.Delete(r.Context(), r.PathValue("id")); err != nil {
					banListError(w, r, d, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "POST", Path: "/v1/ban-lists/{id}/confirm",
			Summary: "Start banning a checked list {unban_removed?, reban_by_hand?}: the names waiting are banned one at a time " +
				"at Twitch's pace; with unban_removed, names this list banned that aren't in the newer file are unbanned; with " +
				"reban_by_hand, names unbanned on Twitch by hand are banned again.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in banlists.ConfirmOptions
				if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
					return
				}
				l, err := d.BanLists.Confirm(r.Context(), r.PathValue("id"), in)
				one(w, r, l, err)
			},
		},
		action("pause", "Pause a ban list after the request in flight.", func(r *http.Request, id string) (banlists.List, error) {
			return d.BanLists.SetPaused(r.Context(), id, true)
		}),
		action("resume", "Carry on with a paused ban list.", func(r *http.Request, id string) (banlists.List, error) {
			return d.BanLists.SetPaused(r.Context(), id, false)
		}),
		action("undo", "Unban everything this list banned, at Twitch's pace, and ban nothing more. Bans made elsewhere are never lifted.",
			func(r *http.Request, id string) (banlists.List, error) { return d.BanLists.Undo(r.Context(), id) }),
		action("compare", "Check the list's bans are still on Twitch: any lifted are marked unbanned by hand, and left alone.",
			func(r *http.Request, id string) (banlists.List, error) { return d.BanLists.Compare(r.Context(), id) }),
		{
			Method: "GET", Path: "/v1/ban-lists/{id}/entries",
			Summary: "A list's names: ?state= one or more (comma-separated) of new, not_found, kept, already, waiting, sent, banned, " +
				"refused, by_hand, removed, unban, unban_sent, unbanned, stopped; ?after= a name, to page; ?limit= 1-500 (default 50).",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 50, 1, 500)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 500")
					return
				}
				var states []string
				if s := r.URL.Query().Get("state"); s != "" {
					states = strings.Split(s, ",")
				}
				list, err := d.BanLists.Entries(r.Context(), r.PathValue("id"), states, r.URL.Query().Get("after"), limit)
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"entries": list})
			},
		},
		{
			Method: "GET", Path: "/v1/ban-lists/{id}/file",
			Summary: "A list's names as a file to save, one login (or user id) a line: ?state= as for entries (all when empty). " +
				"Importing it again gives the same list.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				l, err := d.BanLists.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					banListError(w, r, d, err)
					return
				}
				var states []string
				if s := r.URL.Query().Get("state"); s != "" {
					states = strings.Split(s, ",")
				}
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write([]byte("# " + l.Name + ", saved from StreamGremlin " + time.Now().UTC().Format(time.RFC3339) + "\n"))
				after, n := "", 0
				for n < MaxBanListFile {
					page, err := d.BanLists.Entries(r.Context(), l.ID, states, after, 5000)
					if err != nil || len(page) == 0 {
						return
					}
					var b strings.Builder
					for _, e := range page {
						if e.Login != "" {
							b.WriteString(e.Login)
						} else {
							b.WriteString(strings.TrimPrefix(e.Name, "id:"))
						}
						b.WriteByte('\n')
					}
					if _, err := w.Write([]byte(b.String())); err != nil {
						return
					}
					after, n = page[len(page)-1].Name, n+len(page)
				}
			},
		},
	}
}
