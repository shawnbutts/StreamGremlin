package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// PollsView is the body of GET /v1/polls: the newest poll and prediction.
type PollsView struct {
	// State is ok, or why there's nothing to show: not_signed_in,
	// needs_permission, no_channel_points (not Affiliate) or unknown.
	State      string             `json:"state"`
	Detail     string             `json:"detail,omitempty"`
	Poll       *twitch.Poll       `json:"poll"`
	Prediction *twitch.Prediction `json:"prediction"`
}

func pollState(err error) string {
	switch {
	case err == nil:
		return RewardsOK
	case errors.Is(err, twitch.ErrNotSignedIn), errors.Is(err, twitch.ErrNoClientID):
		return RewardsNotSignedIn
	case errors.Is(err, twitch.ErrNeedsPermission):
		return RewardsNeedsSignIn
	case errors.Is(err, twitch.ErrNoChannelPoints):
		return RewardsNoChannelPoint
	}
	return RewardsUnknown
}

func pollError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twitch.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, twitch.ErrNotSignedIn), errors.Is(err, twitch.ErrNoClientID):
		writeError(w, http.StatusConflict, CodeNotConnected, "Sign in with your Twitch account first.")
	case errors.Is(err, twitch.ErrPollRunning), errors.Is(err, twitch.ErrNoPoll), errors.Is(err, twitch.ErrPredictionRunning),
		errors.Is(err, twitch.ErrNoPrediction), errors.Is(err, twitch.ErrNoChannelPoints), errors.Is(err, twitch.ErrNeedsPermission):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		// Twitch didn't answer clearly: it may or may not have happened.
		writeError(w, http.StatusBadGateway, CodeUnknownOutcome, err.Error())
	}
}

func pollRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/polls",
			Summary: "The newest poll and prediction, from Twitch, with votes and bets. When there's nothing, state says why: " +
				"not signed in, a permission to allow, not Affiliate, or unknown.",
			Permission: PermPollsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var v PollsView
				p, err := d.Twitch.LatestPoll(r.Context())
				if err == nil {
					v.Poll = p
					v.Prediction, err = d.Twitch.LatestPrediction(r.Context())
				}
				v.State = pollState(err)
				if err != nil {
					v.Detail = err.Error()
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "POST", Path: "/v1/polls",
			Summary: "Start a poll {title, choices (2-5), seconds (15-1800), channel_points_per_vote?}. One at a time (409 when one is " +
				"running). Repeating it is harmless (200).",
			Permission: PermPollsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in twitch.NewPoll
				if !decodeJSON(w, r, &in) {
					return
				}
				p, created, err := d.Twitch.StartPoll(r.Context(), in)
				if err != nil {
					pollError(w, err)
					return
				}
				status := http.StatusOK
				if created {
					status = http.StatusCreated
				}
				writeJSON(w, status, p)
			},
		},
		{
			Method: "POST", Path: "/v1/polls/end",
			Summary:    "End the running poll {archive?}: results stay in chat a while, or with archive it's removed. 409 when none is running.",
			Permission: PermPollsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Archive bool `json:"archive"`
				}
				if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
					return
				}
				p, err := d.Twitch.EndPoll(r.Context(), in.Archive)
				if err != nil {
					pollError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, p)
			},
		},
		{
			Method: "POST", Path: "/v1/predictions",
			Summary:    "Start a prediction {title, outcomes (2-10), seconds (30-1800)}. One at a time (409). Repeating it is harmless (200).",
			Permission: PermPollsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in twitch.NewPrediction
				if !decodeJSON(w, r, &in) {
					return
				}
				p, created, err := d.Twitch.StartPrediction(r.Context(), in)
				if err != nil {
					pollError(w, err)
					return
				}
				status := http.StatusOK
				if created {
					status = http.StatusCreated
				}
				writeJSON(w, status, p)
			},
		},
		{
			Method: "POST", Path: "/v1/predictions/end",
			Summary: "Lock, resolve or cancel the newest prediction {how: lock|resolve|cancel, winner?: an outcome's words or number}. " +
				"Repeating the same is harmless; another after it ended is refused (409).",
			Permission: PermPollsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					How    string `json:"how"`
					Winner string `json:"winner"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				p, repeated, err := d.Twitch.EndPrediction(r.Context(), in.How, in.Winner)
				if err != nil {
					pollError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"prediction": p, "repeated": repeated})
			},
		},
	}
}
