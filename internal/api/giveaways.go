package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/commands"
	"code.streamgremlin.com/streamgremlin/internal/giveaways"
)

// GiveawaysView is the body of GET /v1/giveaways.
type GiveawaysView struct {
	// Current is the unfinished giveaway with everything recorded, or null.
	Current *giveaways.Detail    `json:"current"`
	Recent  []giveaways.Giveaway `json:"recent"`
}

func giveawayError(w http.ResponseWriter, err error) {
	var taken *commands.TakenError
	switch {
	case errors.Is(err, giveaways.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, giveaways.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "No such giveaway.")
	case errors.As(err, &taken):
		writeError(w, http.StatusConflict, CodeCommandTaken, err.Error())
	case errors.Is(err, giveaways.ErrState):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "The giveaway couldn't be changed.")
	}
}

func giveawayRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/giveaways",
			Summary:    "The running giveaway with its entries, draws, refusals and chat messages, and the recent ones.",
			Permission: PermGiveawaysRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v := GiveawaysView{}
				if g := d.Giveaways.Current(); g != nil {
					det, err := d.Giveaways.Get(r.Context(), g.ID)
					if err != nil {
						giveawayError(w, err)
						return
					}
					v.Current = &det
				}
				var err error
				if v.Recent, err = d.Giveaways.Recent(r.Context(), 20); err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "GET", Path: "/v1/giveaways/{id}",
			Summary: "One giveaway with everything recorded: the entry list in order with its SHA-256 fingerprint, and each draw's " +
				"place among the entries not yet drawn, so anyone can check it.",
			Permission: PermGiveawaysRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				det, err := d.Giveaways.Get(r.Context(), r.PathValue("id"))
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, det)
			},
		},
		{
			Method: "POST", Path: "/v1/giveaways",
			Summary: "Open a giveaway {title, command?, winners?, minutes?, claim_seconds?, claim_command?, subscribers_only?, enter_by_chat?, subscriber_chances?, from_chat_minutes?, practice?, " +
				"rules?: {followers, follow_days, account_days, sub_months, messages, win_cooldown_days}}. " +
				"With from_chat_minutes, it enters everyone who chatted then, closes and draws before answering. One at a time (409). 409 command_taken when a workflow that's on answers its command.",
			Permission: PermGiveawaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in giveaways.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				g, err := d.Giveaways.Open(r.Context(), in, "the app")
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusCreated, g)
			},
		},
		{
			Method: "POST", Path: "/v1/giveaways/{id}/close",
			Summary:    "Stop taking entries, and fix the entry list and its fingerprint. Closing twice is harmless.",
			Permission: PermGiveawaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				g, err := d.Giveaways.CloseEntries(r.Context(), r.PathValue("id"))
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, g)
			},
		},
		{
			Method: "POST", Path: "/v1/giveaways/{id}/draw",
			Summary: "Draw winners {count?: all still wanted}, closing entries first if they're open. Never more than the giveaway's " +
				"winners (409 when all are drawn).",
			Permission: PermGiveawaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Count int `json:"count"`
				}
				if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
					return
				}
				draws, err := d.Giveaways.Draw(r.Context(), r.PathValue("id"), in.Count)
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"draws": draws})
			},
		},
		{
			Method: "POST", Path: "/v1/giveaways/{id}/set-aside",
			Summary:    "Set a winner aside {draw_id} and draw a replacement, never someone drawn before.",
			Permission: PermGiveawaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					DrawID string `json:"draw_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				draws, err := d.Giveaways.SetAside(r.Context(), r.PathValue("id"), in.DrawID, "the app")
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"draws": draws})
			},
		},
		{
			Method: "POST", Path: "/v1/giveaways/{id}/end",
			Summary:    "End the giveaway and congratulate its winners in chat. 409 while entries are open or a winner can still claim.",
			Permission: PermGiveawaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				g, err := d.Giveaways.End(r.Context(), r.PathValue("id"))
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, g)
			},
		},
		{
			Method: "POST", Path: "/v1/giveaways/{id}/cancel",
			Summary:    "Cancel the giveaway: no winner, said in chat. Everything recorded is kept.",
			Permission: PermGiveawaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				g, err := d.Giveaways.Cancel(r.Context(), r.PathValue("id"))
				if err != nil {
					giveawayError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, g)
			},
		},
	}
}
