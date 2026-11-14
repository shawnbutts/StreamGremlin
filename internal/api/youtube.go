package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

// YouTube sign-ins (M6 slice 1, D-096). The client secret is taken in,
// kept in the secret store, and never answered or logged.

// YouTubeClientRequest is the body of PUT /v1/connections/youtube/client.
type YouTubeClientRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func youtubeRoutes(d Deps) []Route {
	account := func(w http.ResponseWriter, r *http.Request) (*youtube.Account, bool) {
		a, ok := d.YouTube.Account(youtube.Role(r.PathValue("account")))
		if !ok {
			writeError(w, http.StatusNotFound, CodeNotFound, "the account is streamer or bot")
		}
		return a, ok
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/connections/youtube",
			Summary: "YouTube: whose Google project signs in, both sign-ins and their channels, today's quota as " +
				"StreamGremlin counts it, and the adapter's capability manifest. Never returns tokens or the client secret.",
			Permission: PermConnectionsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, d.YouTube.Status(r.Context()))
			},
		},
		{
			Method: "PUT", Path: "/v1/connections/youtube/client",
			Summary: "Use your own Google project: {client_id, client_secret}; the secret is optional and kept with your " +
				"passwords. An empty client_id goes back to the built-in project. Sign-ins made with another ID end.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in YouTubeClientRequest
				if !decodeJSON(w, r, &in) {
					return
				}
				err := d.YouTube.SetClient(r.Context(), in.ClientID, secrets.NewValue(in.ClientSecret))
				if errors.Is(err, youtube.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
					return
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, d.YouTube.Status(r.Context()))
			},
		},
		{
			Method: "POST", Path: "/v1/connections/youtube/{account}/sign-in",
			Summary: "Start signing in (streamer or bot): returns Google's page to open, which comes back to this " +
				"computer once. Replaces a pending sign-in.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				a, ok := account(w, r)
				if !ok {
					return
				}
				p, err := a.StartSignIn(r.Context())
				switch {
				case errors.Is(err, youtube.ErrNoClient):
					writeError(w, http.StatusConflict, CodeConflict, "Add your Google project's client ID first.")
				case err != nil:
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, err.Error())
				default:
					writeJSON(w, http.StatusOK, p)
				}
			},
		},
		{
			Method: "DELETE", Path: "/v1/connections/youtube/{account}/sign-in",
			Summary: "Stop a pending YouTube sign-in. Harmless when none is pending.", Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if a, ok := account(w, r); ok {
					a.CancelSignIn()
					w.WriteHeader(http.StatusNoContent)
				}
			},
		},
		{
			Method: "POST", Path: "/v1/connections/youtube/{account}/sign-out",
			Summary:    "Sign out of YouTube: end the sign-in at Google (best effort) and forget it. Harmless when signed out.",
			Permission: PermConnectionsWrite,
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
