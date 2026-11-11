package api

import (
	"context"
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/buttons"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

func buttonError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, buttons.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, buttons.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, buttons.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such button")
	default:
		internalError(w, r, d.Logger, err)
	}
}

// ButtonInfo is a button and the running workflows a press starts.
type ButtonInfo struct {
	buttons.Button
	Starts []workflow.Summary `json:"starts"`
}

// ButtonList is the body of GET /v1/buttons.
type ButtonList struct {
	Buttons []ButtonInfo `json:"buttons"`
}

// PressResult is the body of POST /v1/buttons/{id}/press.
type PressResult struct {
	Button    buttons.Button     `json:"button"`
	Event     events.Record      `json:"event"`
	Duplicate bool               `json:"duplicate"`
	Started   []workflow.Summary `json:"started"`
}

// starts lists the running workflows a press of b would start.
func starts(ctx context.Context, d Deps, b buttons.Button, from string) ([]workflow.Summary, error) {
	rec, err := buttons.Sample(b, from)
	if err != nil {
		return nil, err
	}
	return d.Workflows.Starting(ctx, rec)
}

func buttonList(ctx context.Context, d Deps) (ButtonList, error) {
	list, err := d.Buttons.List(ctx)
	if err != nil {
		return ButtonList{}, err
	}
	out := ButtonList{Buttons: []ButtonInfo{}}
	for _, b := range list {
		s, err := starts(ctx, d, b, buttons.FromLive)
		if err != nil {
			return ButtonList{}, err
		}
		out.Buttons = append(out.Buttons, ButtonInfo{Button: b, Starts: s})
	}
	return out, nil
}

func buttonRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/buttons",
			Summary:    "Your buttons, in order, each with the running workflows a press starts.",
			Permission: PermButtonsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				out, err := buttonList(r.Context(), d)
				if err != nil {
					buttonError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "PUT", Path: "/v1/buttons/{id}",
			Summary: "Create a button, last in the order, or change one: {label, color}. Colors: red, orange, yellow, green, teal, " +
				"blue, purple, pink, or \"\". Repeating a request is harmless.",
			Permission: PermButtonsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in buttons.Button
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.ID != "" && in.ID != r.PathValue("id") {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the id in the body doesn't match the path")
					return
				}
				in.ID = r.PathValue("id")
				b, created, err := d.Buttons.Save(r.Context(), in)
				if err != nil {
					buttonError(w, r, d, err)
					return
				}
				code := http.StatusOK
				if created {
					code = http.StatusCreated
					d.Logger.Info("button created", "button", b.ID)
				}
				writeJSON(w, code, b)
			},
		},
		{
			Method: "DELETE", Path: "/v1/buttons/{id}",
			Summary:    "Delete a button. Workflows that start from it are kept, and never start from it again.",
			Permission: PermButtonsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				id := r.PathValue("id")
				existed, err := d.Buttons.Delete(r.Context(), id)
				if err != nil {
					buttonError(w, r, d, err)
					return
				}
				if !existed {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such button")
					return
				}
				d.Logger.Info("button deleted", "button", id)
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "PUT", Path: "/v1/button-order",
			Summary:    "Put the buttons in a new order: {order: [ids]}, naming each once. Answers the list.",
			Permission: PermButtonsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Order []string `json:"order"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if err := d.Buttons.Move(r.Context(), in.Order); err != nil {
					buttonError(w, r, d, err)
					return
				}
				out, err := buttonList(r.Context(), d)
				if err != nil {
					buttonError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "POST", Path: "/v1/buttons/{id}/press",
			Summary: "Press a button: the workflows that start from it run for real. {request_id?, from?: live or cli}. " +
				"The same request_id again is the same press and runs nothing twice. Answers the event and the workflows it started.",
			Permission: PermButtonsPress,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				in := struct {
					RequestID string `json:"request_id"`
					From      string `json:"from"`
				}{From: buttons.FromLive}
				if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
					return
				}
				// Only a Stream Deck's own token says it's the Stream Deck.
				if in.From != buttons.FromLive && in.From != buttons.FromCLI {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "from is live or cli")
					return
				}
				b, rec, dup, err := d.Buttons.Press(r.Context(), r.PathValue("id"), in.From, in.RequestID)
				if err != nil {
					buttonError(w, r, d, err)
					return
				}
				started, err := d.Workflows.Starting(r.Context(), rec)
				if err != nil {
					buttonError(w, r, d, err)
					return
				}
				if !dup {
					d.Logger.Info("button pressed", "button", b.ID, "from", in.From, "workflows", len(started))
				}
				writeJSON(w, http.StatusOK, PressResult{Button: b, Event: rec, Duplicate: dup, Started: started})
			},
		},
	}
}
