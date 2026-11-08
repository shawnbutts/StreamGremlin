package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// WorkflowDetail is the body of GET /v1/workflows/{id}.
type WorkflowDetail struct {
	Workflow   workflow.Summary     `json:"workflow"`
	Definition *workflow.Definition `json:"definition"`
}

// JobDetail is the body of GET /v1/jobs/{id}: the run and its trace.
type JobDetail struct {
	Job   workflow.Job          `json:"job"`
	Steps []workflow.StepRecord `json:"steps"`
}

func workflowError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var inv *workflow.InvalidError
	switch {
	case errors.As(err, &inv):
		// The problems, each at its place in the definition, for the editor.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":    map[string]string{"code": CodeInvalidRequest, "message": "the workflow has problems"},
			"problems": inv.Problems,
		})
	case errors.Is(err, workflow.ErrDeleted):
		writeError(w, http.StatusConflict, CodeDeleted, err.Error())
	case errors.Is(err, workflow.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such workflow or run")
	case errors.Is(err, workflow.ErrFinished):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

func workflowRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/workflows",
			Summary:    "Every workflow, with its triggers, whether it's on, and its last run. ?deleted=true lists the deleted ones instead, with deleted_at.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Workflows.List(r.Context(), r.URL.Query().Get("deleted") == "true")
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"workflows": list})
			},
		},
		{
			Method: "GET", Path: "/v1/workflows/{id}", Summary: "One workflow and its current definition.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				s, err := d.Workflows.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				def, _, err := d.Workflows.Definition(r.Context(), s.ID)
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, WorkflowDetail{Workflow: s, Definition: def})
			},
		},
		{
			Method: "PUT", Path: "/v1/workflows/{id}",
			Summary:    "Save a definition (streamgremlin.workflow/v1). The same content keeps its version; a new workflow starts off.",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body over 1 MiB")
					return
				}
				var probe struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(raw, &probe)
				if probe.ID != r.PathValue("id") {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the definition's id must match the address")
					return
				}
				s, changed, err := d.Workflows.Save(r.Context(), raw)
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"workflow": s, "changed": changed})
			},
		},
		{
			Method: "DELETE", Path: "/v1/workflows/{id}",
			Summary:    "Delete a workflow: it turns off, leaves the list, its waiting runs are cancelled and running ones stopped. Versions, runs and its own saved variables are kept for restore. 404 when there's nothing to delete.",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.Workflows.Delete(r.Context(), r.PathValue("id")); err != nil {
					workflowError(w, r, d, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "POST", Path: "/v1/workflows/{id}/restore", Summary: "Bring a deleted workflow back, turned off. 404 when it isn't deleted.",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.Workflows.Restore(r.Context(), r.PathValue("id")); err != nil {
					workflowError(w, r, d, err)
					return
				}
				s, err := d.Workflows.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, s)
			},
		},
		{
			Method: "PUT", Path: "/v1/workflows/{id}/enabled", Summary: "Turn a workflow on or off. Off cancels its waiting runs.",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Enabled bool `json:"enabled"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if err := d.Workflows.SetEnabled(r.Context(), r.PathValue("id"), in.Enabled); err != nil {
					workflowError(w, r, d, err)
					return
				}
				s, err := d.Workflows.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, s)
			},
		},
		{
			Method: "GET", Path: "/v1/actions", Summary: "The actions workflows can use, with their parameters.",
			Permission: PermWorkflowsRead,
			CLIOnly:    "for scripts; the app reads the actions from /v1/palette",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"actions": d.Workflows.Actions().List()})
			},
		},
		{
			Method: "GET", Path: "/v1/palette",
			Summary:    "What the workflow editor offers, in plain words: triggers with what they can be limited to and the values messages can include, and the actions with their parameters.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, d.Workflows.Palette())
			},
		},
		{
			Method: "GET", Path: "/v1/jobs", Summary: "The newest runs, of ?workflow= or all; ?limit= 1-200 (default 50). Includes runs that were dropped or rejected, and why.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 50, 1, 200)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 200")
					return
				}
				jobs, err := d.Workflows.Jobs(r.Context(), r.URL.Query().Get("workflow"), limit)
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
			},
		},
		{
			Method: "GET", Path: "/v1/jobs/{id}", Summary: "One run and its trace: every step, its input (secrets hidden), output and time.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				j, steps, err := d.Workflows.Trace(r.Context(), r.PathValue("id"))
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, JobDetail{Job: j, Steps: steps})
			},
		},
		{
			Method: "POST", Path: "/v1/jobs/{id}/cancel", Summary: "Stop a waiting or running run; a running one runs its finally steps first.",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.Workflows.Cancel(r.Context(), r.PathValue("id")); err != nil {
					workflowError(w, r, d, err)
					return
				}
				w.WriteHeader(http.StatusAccepted)
			},
		},
	}
}
