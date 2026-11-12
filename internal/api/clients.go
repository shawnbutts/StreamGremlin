package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/clients"
)

// Paired clients (M7 slice 4; docs/adr/0042-paired-clients.md).

// PairResult is the body of POST /v1/pair: the new client and its token,
// shown this once.
type PairResult struct {
	Client      clients.Client `json:"client"`
	Token       string         `json:"token"`
	Permissions []string       `json:"permissions"`
}

// PairingView is the body of GET /v1/clients/pairing.
type PairingView struct {
	clients.State
	// Address is where the engine listens, for a plugin to reach it.
	Address string `json:"address,omitempty"`
}

// WhoAmI is the body of GET /v1/whoami.
type WhoAmI struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	ClientID    string   `json:"client_id,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

func clientError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, clients.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, clients.ErrWrongCode):
		writeError(w, http.StatusForbidden, CodeForbidden, "that code isn't the one StreamGremlin shows; check it and try again")
	case errors.Is(err, clients.ErrNoPairing):
		writeError(w, http.StatusConflict, CodeConflict, "StreamGremlin isn't waiting for a code: start pairing on its Connections screen, then type the new code")
	case errors.Is(err, clients.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

func clientRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/clients", Summary: "The paired clients (Stream Decks): name, when paired and last used. Never their tokens.",
			Permission: PermClientsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Clients.List(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"clients": list})
			},
		},
		{
			Method: "DELETE", Path: "/v1/clients/{id}", Summary: "Forget a paired client: its token is refused from now on.",
			Permission: PermClientsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				existed, err := d.Clients.Forget(r.Context(), r.PathValue("id"))
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				if !existed {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such client")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "GET", Path: "/v1/clients/pairing",
			Summary:    "The pairing in progress: none, waiting (with the code), paired, expired or locked; and the address a plugin reaches the engine at.",
			Permission: PermClientsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				addr, _ := os.ReadFile(filepath.Join(d.DataDir, "api-addr"))
				writeJSON(w, http.StatusOK, PairingView{State: d.Clients.State(), Address: strings.TrimSpace(string(addr))})
			},
		},
		{
			Method: "POST", Path: "/v1/clients/pairing",
			Summary:    "Start pairing {kind: stream_deck}: answers a one-time code to type into the plugin, good for 5 minutes. Replaces any code shown.",
			Permission: PermClientsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Kind string `json:"kind"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				p, err := d.Clients.StartPairing(in.Kind)
				if err != nil {
					clientError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusCreated, p)
			},
		},
		{
			Method: "DELETE", Path: "/v1/clients/pairing", Summary: "Stop pairing: the code shown no longer works.",
			Permission: PermClientsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				d.Clients.Cancel()
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "POST", Path: "/v1/pair", Public: true,
			Summary: "Finish pairing, from the plugin: {code, name, request_id?} answers the client and its own token, once. " +
				"No token needed; works only while StreamGremlin shows a code, and 5 wrong codes end the pairing.",
			CLIOnly: "called by the Stream Deck plugin with the code the app shows; the app starts pairing",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Code      string `json:"code"`
					Name      string `json:"name"`
					RequestID string `json:"request_id"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				c, token, err := d.Clients.Exchange(r.Context(), in.Code, in.Name, in.RequestID)
				if err != nil {
					clientError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusCreated, PairResult{Client: c, Token: token, Permissions: clientPrincipal(c).Permissions()})
			},
		},
		{
			Method: "GET", Path: "/v1/whoami", Summary: "Who this token is: admin, or a paired client with its name and permissions.",
			Permission: PermSelfRead,
			CLIOnly:    "lets a paired client (the Stream Deck plugin) check its token and what it may do",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				p := principalOf(r)
				writeJSON(w, http.StatusOK, WhoAmI{Kind: p.Kind, Name: p.Name, ClientID: p.ClientID, Permissions: p.Permissions()})
			},
		},
	}
}
