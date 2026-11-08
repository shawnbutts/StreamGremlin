package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Connections is the body of GET /v1/connections. Tokens never appear in it.
type Connections struct {
	Twitch twitch.Status `json:"twitch"`
	OBS    obs.Status    `json:"obs"`
}

// ClientIDRequest is the body of PUT /v1/connections/twitch/client-id.
type ClientIDRequest struct {
	ClientID string `json:"client_id"`
}

func chatError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var drop *twitch.DropError
	switch {
	case errors.Is(err, twitch.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, twitch.ErrNotSignedIn):
		writeError(w, http.StatusConflict, CodeConflict, "Sign in with your Twitch account to send chat.")
	case errors.Is(err, twitch.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, CodeRateLimited, "Too many chat messages just now; nothing was sent.")
	case errors.As(err, &drop):
		writeError(w, http.StatusUnprocessableEntity, CodeNotSent, err.Error())
	default:
		// Network trouble or a Twitch refusal: the message may or may not
		// have been posted, so it isn't retried (ADR-0006).
		writeError(w, http.StatusBadGateway, CodeUnavailable, err.Error())
	}
}

func connectionRoutes(d Deps) []Route {
	account := func(w http.ResponseWriter, r *http.Request) (*twitch.Account, bool) {
		a, ok := d.Twitch.Account(twitch.Role(r.PathValue("account")))
		if !ok {
			writeError(w, http.StatusNotFound, CodeNotFound, "the account is streamer or bot")
		}
		return a, ok
	}
	return []Route{
		{
			Method: "POST", Path: "/v1/chat/messages",
			Summary:    "Send a chat message to your channel, as the bot when one is signed in (as: bot or streamer). Never retried.",
			Permission: PermChatSend,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in twitch.SendRequest
				if !decodeJSON(w, r, &in) {
					return
				}
				res, err := d.Twitch.SendChat(r.Context(), in)
				if err != nil {
					chatError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, res)
			},
		},
		{
			Method: "GET", Path: "/v1/connections",
			Summary:    "Twitch (Client ID, sign-ins, chat) and OBS. Never returns tokens or passwords.",
			Permission: PermConnectionsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, Connections{Twitch: d.Twitch.Status(r.Context()), OBS: d.OBS.Status()})
			},
		},
		{
			Method: "PUT", Path: "/v1/connections/twitch/client-id",
			Summary:    "Use your own Twitch app's Client ID; empty goes back to the built-in one. Sign-ins made with another ID end.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in ClientIDRequest
				if !decodeJSON(w, r, &in) {
					return
				}
				err := d.Twitch.SetClientID(r.Context(), in.ClientID)
				if errors.Is(err, twitch.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
					return
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, Connections{Twitch: d.Twitch.Status(r.Context()), OBS: d.OBS.Status()})
			},
		},
		{
			Method: "POST", Path: "/v1/connections/twitch/{account}/sign-in",
			Summary:    "Start signing in (streamer or bot) and get the code to enter at Twitch. Replaces a pending sign-in.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				a, ok := account(w, r)
				if !ok {
					return
				}
				p, err := a.StartSignIn(r.Context())
				switch {
				case errors.Is(err, twitch.ErrNoClientID):
					writeError(w, http.StatusConflict, CodeConflict, "Add a Twitch Client ID first.")
				case err != nil:
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, err.Error())
				default:
					writeJSON(w, http.StatusOK, p)
				}
			},
		},
		{
			Method: "DELETE", Path: "/v1/connections/twitch/{account}/sign-in",
			Summary: "Stop a pending sign-in. Harmless when none is pending.", Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if a, ok := account(w, r); ok {
					a.CancelSignIn()
					w.WriteHeader(http.StatusNoContent)
				}
			},
		},
		{
			Method: "POST", Path: "/v1/connections/twitch/{account}/sign-out",
			Summary: "Sign out: revoke the token at Twitch (best effort) and forget it. Harmless when signed out.", Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				a, ok := account(w, r)
				if !ok {
					return
				}
				if err := a.SignOut(r.Context()); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
	}
}
