package api

import (
	"errors"
	"net/http"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/discord"
)

// Discord webhooks (D-089). A webhook's link holds Discord's token, so it's
// taken in, kept in the secret store, and never answered or logged.

// DiscordWebhookAdd is the body of PUT /v1/discord/webhooks/{name}.
type DiscordWebhookAdd struct {
	Link string `json:"link"`
}

func discordError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	msg := strings.TrimPrefix(err.Error(), "discord: ")
	msg = strings.TrimPrefix(msg, "invalid: ")
	switch {
	case errors.Is(err, discord.ErrInvalid), errors.Is(err, discord.ErrWebhookGone):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, msg)
	case errors.Is(err, discord.ErrNoWebhook):
		writeError(w, http.StatusNotFound, CodeNotFound, msg)
	case errors.Is(err, discord.ErrOutcomeUnknown):
		writeError(w, http.StatusBadGateway, CodeUnknownOutcome, msg)
	case errors.Is(err, discord.ErrRefused), strings.HasPrefix(err.Error(), "discord: Discord"):
		writeError(w, http.StatusBadGateway, CodeUnavailable, msg)
	default:
		internalError(w, r, d.Logger, err)
	}
}

func discordRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/discord/webhooks",
			Summary: "The Discord webhooks workflows can post to: each one's name here, Discord's id for it, its name in " +
				"Discord, its channel id, and when it was added; never the link. With the adapter's capability manifest.",
			Permission: PermConnectionsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				ws, err := d.Discord.Webhooks(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"webhooks": ws, "manifest": discord.DiscordManifest()})
			},
		},
		{
			Method: "PUT", Path: "/v1/discord/webhooks/{name}",
			Summary: "Add a Discord webhook under a name (lower-case letters, digits and dashes), or replace one: {link}, the " +
				"webhook link Discord copies. It's checked with Discord first, and kept with your passwords.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in DiscordWebhookAdd
				if !decodeJSON(w, r, &in) {
					return
				}
				wh, err := d.Discord.Add(r.Context(), r.PathValue("name"), in.Link)
				if err != nil {
					discordError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, wh)
			},
		},
		{
			Method: "DELETE", Path: "/v1/discord/webhooks/{name}",
			Summary:    "Forget a Discord webhook here. It stays in Discord; delete it there to stop it for good.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.Discord.Remove(r.Context(), r.PathValue("name")); err != nil {
					discordError(w, r, d, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "POST", Path: "/v1/discord/webhooks/{name}/test",
			Summary:    "Post a short test message through a webhook, to see it arrive in Discord. It notifies nobody.",
			Permission: PermConnectionsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				sent, err := d.Discord.Send(r.Context(), r.PathValue("name"),
					discord.Message{Content: "StreamGremlin can post here. 👋"})
				if err != nil {
					discordError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, sent)
			},
		},
	}
}
