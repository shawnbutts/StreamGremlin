package api

import (
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/pretzel"
)

// Pretzel, the music app (D-090): what it's playing, read from the
// address Pretzel documents for !song commands. Nothing to set up: it
// uses the streamer's Twitch account.

// PretzelView is GET /v1/pretzel.
type PretzelView struct {
	// Song is the latest check, or unknown if there's been none.
	Song     pretzel.Song     `json:"song"`
	Manifest pretzel.Manifest `json:"manifest"`
}

func pretzelRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/pretzel",
			Summary: "What Pretzel was playing at the latest check, without asking it again; with the adapter's capability " +
				"manifest. Pretzel is asked only while a Now playing overlay is open, by a workflow step, or by POST /v1/pretzel/check.",
			Permission: PermConnectionsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, PretzelView{Song: d.Pretzel.Last(), Manifest: pretzel.PretzelManifest()})
			},
		},
		{
			Method: "POST", Path: "/v1/pretzel/check",
			Summary: "Ask Pretzel what's playing now (a check moments after another reuses it). Its state is playing, other " +
				"(Pretzel said something else), not_using (Pretzel doesn't know your Twitch account), not_signed_in or unknown.",
			Permission: PermConnectionsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, d.Pretzel.Now(r.Context()))
			},
		},
	}
}
