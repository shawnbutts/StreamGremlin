package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/templates"
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
	var taken *commands.TakenError
	switch {
	case errors.As(err, &taken):
		// Which commands, and what answers each now, for the screens.
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     map[string]string{"code": CodeCommandTaken, "message": taken.Error()},
			"conflicts": taken.Conflicts,
		})
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
	case errors.Is(err, workflow.ErrFinished), errors.Is(err, workflow.ErrGroupExists):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, workflow.ErrGroupNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no workflow is in a group by that name")
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
			Summary:    "Save a definition (streamgremlin.workflow/v1). The same content keeps its version; a new workflow starts off. A change to a workflow that's on is refused (409 command_taken) when it newly overlaps a chat command another workflow that's on answers.",
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
			Method: "PUT", Path: "/v1/workflows/{id}/enabled", Summary: "Turn a workflow on or off. Off cancels its waiting runs. On is refused (409 command_taken) when another workflow that's on answers one of its chat commands, unless their chats or roles never meet.",
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
			Method: "GET", Path: "/v1/workflow-groups",
			Summary:    "Workflow groups in use, each with its switch and how many workflows it has and has on.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				gs, err := d.Workflows.Groups(r.Context())
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"groups": gs})
			},
		},
		{
			Method: "PUT", Path: "/v1/workflow-groups/{name}/enabled",
			Summary: "Switch a group on or off {enabled}. Each workflow's own switch is kept; a workflow runs only when both are on. " +
				"On is refused (409 command_taken) when it would make two workflows answer one chat command.",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Enabled bool `json:"enabled"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if err := d.Workflows.SetGroupEnabled(r.Context(), r.PathValue("name"), in.Enabled); err != nil {
					workflowError(w, r, d, err)
					return
				}
				gs, err := d.Workflows.Groups(r.Context())
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"groups": gs})
			},
		},
		{
			Method: "PUT", Path: "/v1/workflow-groups/{name}",
			Summary: "Rename a group {name}. Each workflow in it gets a new version; the switch is kept. " +
				"Another group's name is refused (409).",
			Permission: PermWorkflowsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Name string `json:"name"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				name, err := d.Workflows.RenameGroup(r.Context(), r.PathValue("name"), in.Name)
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"name": name})
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
			Method: "GET", Path: "/v1/commands",
			Summary:    "Every chat command a workflow answers, with each workflow (on or off, for which chats and roles) and whether two that are on overlap.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Workflows.Commands(r.Context())
				if err != nil {
					workflowError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"commands": list})
			},
		},
		{
			Method: "GET", Path: "/v1/templates",
			Summary: "Ready-made workflows for the gallery: each with its title, category, the blanks to fill in, and the workflow. " +
				"Installed add-ons' templates follow StreamGremlin's own, each add-on its own category (its name), with add_on saying " +
				"whose they are and whether it's on. Data only; nothing is added until saved.",
			Permission: PermWorkflowsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := templates.All()
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				categories := append([]string{}, templates.Categories...)
				if d.Packages != nil {
					extra, err := d.Packages.AddOnTemplates(r.Context())
					if err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
					for _, t := range extra {
						if !slices.Contains(categories, t.Category) {
							categories = append(categories, t.Category)
						}
					}
					list = append(list, extra...)
				}
				writeJSON(w, http.StatusOK, map[string]any{"templates": list, "categories": categories})
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
