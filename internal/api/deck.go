package api

import (
	"errors"
	"net/http"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/deck"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/variables"
)

// Stream Deck keys beyond buttons (D-123, ADR-0053): counters, status and
// switches, limited to what the streamer chose.

func deckError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var taken *commands.TakenError
	msg := func(prefix string) string { return strings.TrimPrefix(err.Error(), prefix) }
	switch {
	case errors.Is(err, deck.ErrNotAllowed):
		writeError(w, http.StatusForbidden, CodeForbidden, "that isn't chosen for the Stream Deck; choose it on Connections → Stream Deck")
	case errors.Is(err, deck.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, msg("deck: invalid: "))
	case errors.Is(err, variables.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, msg("variables: invalid: "))
	case errors.Is(err, deck.ErrNotNumber):
		writeError(w, http.StatusConflict, CodeConflict, "that variable doesn't hold a number, so it can't be counted")
	case errors.Is(err, variables.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, deck.ErrGone):
		writeError(w, http.StatusNotFound, CodeNotFound, "that workflow or timer was deleted")
	case errors.Is(err, deck.ErrRefused):
		writeError(w, http.StatusConflict, CodeConflict, "Twitch said no: "+msg("deck: refused: "))
	case errors.Is(err, deck.ErrUnknown):
		writeError(w, http.StatusConflict, CodeUnknownOutcome, "unknown: "+msg("deck: unknown: "))
	case errors.Is(err, overlays.ErrNotTimer):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.As(err, &taken):
		writeError(w, http.StatusConflict, CodeCommandTaken, taken.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

// who is the caller, for records: the paired client's id, or "you".
func who(r *http.Request) string {
	if p := principalOf(r); p.ClientID != "" {
		return p.ClientID
	}
	return "you"
}

func deckRoutes(d Deps) []Route {
	if d.Deck == nil {
		return nil
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/deck",
			Summary: "What a Stream Deck's keys show: the status (live and chat_locked true, false or null when unknown; chat and obs " +
				"as their connections), and each counter and switch chosen for it, with its value or whether it's on (null: unknown).",
			Permission: PermDeckRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v, err := d.Deck.View(r.Context())
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "POST", Path: "/v1/deck/counters/{name}",
			Summary: "Change a counter chosen for the Stream Deck by one: {change: add, take (never below 0) or reset, request_id}. " +
				"The same request_id within 10 minutes answers the first result again.",
			Permission: PermDeckUse,
			CLIOnly:    "pressed from the Stream Deck plugin's counter keys; the app changes variables on Variables",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Change    string `json:"change"`
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				c, err := d.Deck.Count(r.Context(), r.PathValue("name"), in.Change, in.RequestID, who(r))
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, c)
			},
		},
		{
			Method: "PUT", Path: "/v1/deck/switches/{kind}/{id}",
			Summary: "Turn a switch chosen for the Stream Deck on or off: {on}. kind is workflow, timer or chat_lock (id -). " +
				"Already as asked, nothing is sent.",
			Permission: PermDeckUse,
			CLIOnly:    "pressed from the Stream Deck plugin's switch keys; the app turns these on and off on their own screens",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					On *bool `json:"on"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.On == nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "on is true or false")
					return
				}
				id := r.PathValue("id")
				if id == "-" {
					id = ""
				}
				st, err := d.Deck.Turn(r.Context(), r.PathValue("kind"), id, *in.On, who(r))
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, st)
			},
		},
		{
			Method: "POST", Path: "/v1/deck/runs/{id}",
			Summary: "Run a workflow chosen for the Stream Deck now, whatever its trigger, if it's on: {request_id}. It records a " +
				"streamdeck.workflow.run event that only that workflow starts from; the same request_id again is the same run.",
			Permission: PermDeckUse,
			CLIOnly:    "pressed from the Stream Deck plugin's run keys; the app runs workflows from buttons on Live",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				st, dup, err := d.Deck.Run(r.Context(), r.PathValue("id"), in.RequestID, who(r))
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"run": st, "duplicate": dup})
			},
		},
		{
			Method: "POST", Path: "/v1/deck/scene",
			Summary:    "Switch OBS to a scene from the Stream Deck, when scenes are chosen for it: {scene}. Safe to repeat.",
			Permission: PermDeckUse,
			CLIOnly:    "pressed from the Stream Deck plugin's scene keys; the app switches scenes on Live",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in obsSwitch
				if !decodeJSON(w, r, &in) {
					return
				}
				sc, err := d.Deck.Scene(r.Context(), in.Scene, who(r))
				if err != nil {
					if errors.Is(err, deck.ErrNotAllowed) {
						deckError(w, r, d, err)
					} else {
						obsError(w, r, d, err)
					}
					return
				}
				writeJSON(w, http.StatusOK, sc)
			},
		},
		{
			Method: "POST", Path: "/v1/deck/countdowns/{name}",
			Summary: "Start, pause or reset a clock overlay's countdown or stopwatch chosen for the Stream Deck: {action}. " +
				"Starting one that's running, or pausing one that's paused, changes nothing.",
			Permission: PermDeckUse,
			CLIOnly:    "pressed from the Stream Deck plugin's countdown keys; the app does this in the overlay's settings",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Action string `json:"action"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				c, err := d.Deck.Countdown(r.Context(), r.PathValue("name"), in.Action, who(r))
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, c)
			},
		},
		{
			Method: "POST", Path: "/v1/deck/ads/snooze",
			Summary:    "Snooze Twitch's next automatic ad from the Stream Deck, when ads are chosen for it: {request_id}, once per id.",
			Permission: PermDeckUse,
			CLIOnly:    "pressed from the Stream Deck plugin's ad key; the app snoozes on Live",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				a, err := d.Deck.Snooze(r.Context(), in.RequestID, who(r))
				if err != nil {
					if errors.Is(err, deck.ErrNotAllowed) || errors.Is(err, deck.ErrInvalid) {
						deckError(w, r, d, err)
					} else {
						adsError(w, err)
					}
					return
				}
				writeJSON(w, http.StatusOK, a)
			},
		},
		{
			Method: "GET", Path: "/v1/settings/streamdeck",
			Summary:    "What a paired Stream Deck may use beyond buttons: {counters: [variable names], switches: [{kind, id}]}.",
			Permission: PermSettingsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				s, err := d.Deck.Settings(r.Context())
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, s)
			},
		},
		{
			Method: "PUT", Path: "/v1/settings/streamdeck",
			Summary: "Choose what a paired Stream Deck may use: {counters: up to 20 global variable names, switches: up to 20 " +
				"{kind: workflow or timer, id} or {kind: chat_lock}, runs: up to 20 workflow ids, scenes: true or false, " +
				"countdowns: up to 20 clock overlay names, ads: true or false}. Applies to every paired deck at once.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in deck.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				s, err := d.Deck.SetSettings(r.Context(), in)
				if err != nil {
					deckError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, s)
			},
		},
	}
}
