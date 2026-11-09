package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/ads"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// AdsView is the body of GET /v1/ads: Twitch's schedule now, and what
// StreamGremlin asked for and saw, kept apart.
type AdsView struct {
	// State is ok, or why there's no schedule: not_signed_in,
	// needs_permission, no_channel_points (not Affiliate) or unknown.
	State    string             `json:"state"`
	Detail   string             `json:"detail,omitempty"`
	Schedule *twitch.AdSchedule `json:"schedule"`
	Requests []ads.Request      `json:"requests"`
	// Breaks are the ones Twitch said began. Their ends are estimates.
	Breaks []ads.Break `json:"breaks"`
}

func adsState(err error) string {
	var re *twitch.AdRefusedError
	if errors.As(err, &re) && re.Reason == "not_eligible" {
		return RewardsNoChannelPoint
	}
	return pollState(err)
}

func adsError(w http.ResponseWriter, err error) {
	var re *ads.RefusedError
	switch {
	case errors.As(err, &re) && re.Reason == "invalid":
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.As(err, &re):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, ads.ErrUnknown):
		writeError(w, http.StatusBadGateway, CodeUnknownOutcome, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "the request couldn't be recorded")
	}
}

// opKey is the caller's key for a request, so sending it again is
// harmless; without one, every call is a new request.
func opKey(in string) string {
	if in == "" {
		return "api:" + events.NewID()
	}
	return "api:" + in
}

func adRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/ads",
			Summary: "Twitch's ad schedule (next ad, snoozes left), and the recent ad requests and breaks. A break is listed only " +
				"when Twitch said it began; its end is an estimate. State says why there's no schedule.",
			Permission: PermAdsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				v := AdsView{}
				sc, err := d.Ads.Schedule(r.Context())
				v.State = adsState(err)
				if err != nil {
					v.Detail = err.Error()
				} else {
					v.Schedule = &sc
				}
				v.Requests, v.Breaks, err = d.Ads.Recent(r.Context(), 20)
				if err != nil {
					writeError(w, http.StatusInternalServerError, CodeInternal, "the ad history couldn't be read")
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "POST", Path: "/v1/ads/commercial",
			Summary: "Ask Twitch for an ad break now {length (1-180 seconds), op_key?}. Sent once, never retried: 200 accepted " +
				"(Twitch took it; the break shows when Twitch says it began), 409 refused (nothing happened), 502 unknown (it may " +
				"have started; StreamGremlin checks). The same op_key returns the first answer without sending again.",
			Permission: PermAdsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Length int    `json:"length"`
					OpKey  string `json:"op_key"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				q, err := d.Ads.StartCommercial(r.Context(), in.Length, opKey(in.OpKey), "the app")
				if err != nil {
					adsError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, q)
			},
		},
		{
			Method: "POST", Path: "/v1/ads/snooze",
			Summary:    "Push Twitch's next automatic ad back 5 minutes {op_key?}. 409 when refused (no snoozes left, not live).",
			Permission: PermAdsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					OpKey string `json:"op_key"`
				}
				if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
					return
				}
				q, err := d.Ads.Snooze(r.Context(), opKey(in.OpKey), "the app")
				if err != nil {
					adsError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, q)
			},
		},
	}
}
