package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

// SecretStoreInfo says where secrets are kept.
type SecretStoreInfo struct {
	Kind  secrets.Kind `json:"kind"`
	Label string       `json:"label"`
}

// SecretItem is one saved secret, never with its value. State and Detail are
// set only when the list was asked to check.
type SecretItem struct {
	secrets.Entry
	State  secrets.CheckState `json:"state,omitempty"`
	Detail string             `json:"detail,omitempty"`
}

// SecretList is the body of GET /v1/secrets.
type SecretList struct {
	Store   SecretStoreInfo `json:"store"`
	Checked bool            `json:"checked"`
	Secrets []SecretItem    `json:"secrets"`
}

// checkBudget keeps a check inside the server's write timeout, even if the
// keyring waits on an unlock prompt.
const checkBudget = 25 * time.Second

func secretRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/secrets",
			Summary:    "Where secrets are kept and which are saved; with ?check=true, also whether each can be read. Never returns values.",
			Permission: PermSecretsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				out, err := listSecrets(r.Context(), d.Secrets, r.URL.Query().Get("check") == "true")
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "DELETE", Path: "/v1/secrets/{key...}", Summary: "Forget one secret. Forgetting one that isn't saved is harmless.",
			Permission: PermSecretsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				err := d.Secrets.Delete(r.Context(), r.PathValue("key"))
				switch {
				case err == nil:
					w.WriteHeader(http.StatusNoContent)
				case errors.Is(err, secrets.ErrInvalid):
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "that isn't a secret's name")
				case errors.Is(err, secrets.ErrUnavailable):
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, err.Error())
				case errors.Is(err, secrets.ErrDamaged):
					writeError(w, http.StatusConflict, CodeConflict, err.Error())
				default:
					internalError(w, r, d.Logger, err)
				}
			},
		},
	}
}

func listSecrets(ctx context.Context, s *secrets.Store, check bool) (SecretList, error) {
	out := SecretList{Store: SecretStoreInfo{Kind: s.Kind(), Label: s.Label()}, Checked: check, Secrets: []SecretItem{}}
	entries, err := s.List(ctx)
	if err != nil {
		return out, err
	}
	var results map[string]secrets.CheckResult
	if check {
		cctx, cancel := context.WithTimeout(ctx, checkBudget)
		defer cancel()
		res, err := s.Check(cctx)
		if err != nil {
			return out, err
		}
		results = make(map[string]secrets.CheckResult, len(res))
		for _, c := range res {
			results[c.Key] = c
		}
	}
	for _, e := range entries {
		item := SecretItem{Entry: e}
		if c, ok := results[e.Key]; ok {
			item.State, item.Detail = c.State, c.Detail
		}
		out.Secrets = append(out.Secrets, item)
	}
	return out, nil
}
