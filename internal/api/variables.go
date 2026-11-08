package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/variables"
)

func variableError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, variables.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, variables.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, variables.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such variable")
	default:
		internalError(w, r, d.Logger, err)
	}
}

func keyFromQuery(r *http.Request) variables.Key {
	q := r.URL.Query()
	return variables.Key{Scope: variables.Scope(q.Get("scope")), Owner: q.Get("owner"), Name: q.Get("name")}
}

func variableRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/variables",
			Summary:    "Saved variables, by scope then owner then name. ?scope=global|viewer|workflow, ?owner=, ?q= (part of a name or viewer), ?limit= 1-1000 (default 500).",
			Permission: PermVariablesRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				limit, err := intParam(q.Get("limit"), 500, 1, 1000)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 1000")
					return
				}
				scope := variables.Scope(q.Get("scope"))
				if scope != "" && scope != variables.Global && scope != variables.Viewer && scope != variables.Workflow {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "scope is global, viewer or workflow")
					return
				}
				list, err := d.Variables.List(r.Context(), variables.Filter{Scope: scope, Owner: q.Get("owner"), Query: q.Get("q"), Limit: limit})
				if err != nil {
					variableError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"variables": list})
			},
		},
		{
			Method: "PUT", Path: "/v1/variables",
			Summary:    "Save one variable: {scope, owner, name, value}. Owner is '' for global, <platform>:<user id> for a viewer, or the workflow's id. Repeating it is harmless.",
			Permission: PermVariablesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					variables.Key
					Value json.RawMessage `json:"value"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if len(in.Value) == 0 || bytes.Equal(in.Value, []byte("null")) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "a value is required; to remove a variable, delete it")
					return
				}
				dec := json.NewDecoder(bytes.NewReader(in.Value))
				dec.UseNumber()
				var value any
				if err := dec.Decode(&value); err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "value must be JSON")
					return
				}
				if _, err := d.Variables.Set(r.Context(), in.Key, variables.Normalize(value), variables.Change{By: "you"}); err != nil {
					variableError(w, r, d, err)
					return
				}
				v, err := d.Variables.Get(r.Context(), in.Key)
				if err != nil {
					variableError(w, r, d, err)
					return
				}
				d.Logger.Info("variable changed", "variable", in.String(), "by", "you")
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "DELETE", Path: "/v1/variables",
			Summary:    "Delete one variable: ?scope=&owner=&name=. Deleting one that isn't there answers 404.",
			Permission: PermVariablesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				k := keyFromQuery(r)
				existed, err := d.Variables.Delete(r.Context(), k)
				if err != nil {
					variableError(w, r, d, err)
					return
				}
				if !existed {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such variable")
					return
				}
				d.Logger.Info("variable deleted", "variable", k.String(), "by", "you")
				w.WriteHeader(http.StatusNoContent)
			},
		},
	}
}
