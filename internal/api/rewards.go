package api

import (
	"errors"
	"net/http"
	"slices"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Reward states, for GET /v1/rewards: why there's no list, in a word.
const (
	RewardsOK             = "ok"
	RewardsNotSignedIn    = "not_signed_in"
	RewardsNeedsSignIn    = "needs_permission"
	RewardsNoChannelPoint = "no_channel_points"
	RewardsUnknown        = "unknown"
)

// RewardList is the body of GET /v1/rewards.
type RewardList struct {
	// State is ok, or why the rewards aren't listed: not_signed_in,
	// needs_permission, no_channel_points or unknown (Twitch didn't answer).
	State   string          `json:"state"`
	Detail  string          `json:"detail,omitempty"`
	Rewards []twitch.Reward `json:"rewards"`
	// CanManage: the sign-in allows making and changing rewards.
	CanManage bool `json:"can_manage"`
	Max       int  `json:"max"`
}

func rewardState(err error) string {
	switch {
	case err == nil:
		return RewardsOK
	case errors.Is(err, twitch.ErrNotSignedIn), errors.Is(err, twitch.ErrNoClientID):
		return RewardsNotSignedIn
	case errors.Is(err, twitch.ErrNeedsManage):
		return RewardsNeedsSignIn
	case errors.Is(err, twitch.ErrNoChannelPoints):
		return RewardsNoChannelPoint
	}
	return RewardsUnknown
}

func rewardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twitch.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, twitch.ErrRewardNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, twitch.ErrNotManaged):
		writeError(w, http.StatusForbidden, CodeForbidden, err.Error())
	case errors.Is(err, twitch.ErrNotSignedIn), errors.Is(err, twitch.ErrNoClientID):
		writeError(w, http.StatusConflict, CodeNotConnected, "Sign in with your Twitch account first.")
	case errors.Is(err, twitch.ErrNeedsManage), errors.Is(err, twitch.ErrNoChannelPoints), errors.Is(err, twitch.ErrAlreadyHandled):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		// Twitch didn't answer clearly: it may or may not have happened.
		writeError(w, http.StatusBadGateway, CodeUnknownOutcome, err.Error())
	}
}

func rewardRoutes(d Deps) []Route {
	canManage := func() bool {
		a, _ := d.Twitch.Account(twitch.RoleStreamer)
		return slices.Contains(a.Status().Scopes, twitch.ManageScope)
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/rewards",
			Summary: "Your channel's channel point rewards, from Twitch, each marked managed (made in StreamGremlin) or not. " +
				"When there's no list, state says why: not signed in, a permission to allow, no channel points (not Affiliate or Partner), or unknown.",
			Permission: PermRewardsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Twitch.Rewards(r.Context())
				out := RewardList{State: rewardState(err), Rewards: list, Max: twitch.MaxRewards, CanManage: canManage()}
				if out.Rewards == nil {
					out.Rewards = []twitch.Reward{}
				}
				if err != nil {
					out.Detail = err.Error()
				}
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "POST", Path: "/v1/rewards",
			Summary: "Make a reward StreamGremlin manages {title, cost, prompt?, background_color?, enabled?, paused?, input_required?, " +
				"skip_queue?, max_per_stream?, max_per_user_per_stream?, cooldown_seconds?}. Repeating it is harmless (200).",
			Permission: PermRewardsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in twitch.RewardChange
				if !decodeJSON(w, r, &in) {
					return
				}
				rw, created, err := d.Twitch.CreateReward(r.Context(), in)
				if err != nil {
					rewardError(w, err)
					return
				}
				status := http.StatusOK
				if created {
					status = http.StatusCreated
				}
				writeJSON(w, status, rw)
			},
		},
		{
			Method: "PUT", Path: "/v1/rewards/{id}",
			Summary:    "Change a reward StreamGremlin manages; fields left out stay as they are. Repeating it is harmless.",
			Permission: PermRewardsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in twitch.RewardChange
				if !decodeJSON(w, r, &in) {
					return
				}
				rw, err := d.Twitch.UpdateReward(r.Context(), r.PathValue("id"), in)
				if err != nil {
					rewardError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, rw)
			},
		},
		{
			Method: "DELETE", Path: "/v1/rewards/{id}",
			Summary:    "Delete a reward StreamGremlin manages. Twitch marks its waiting redemptions done. Repeating it is harmless.",
			Permission: PermRewardsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				ok, err := d.Twitch.DeleteReward(r.Context(), r.PathValue("id"))
				if err != nil {
					rewardError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]bool{"deleted": ok})
			},
		},
		{
			Method: "GET", Path: "/v1/rewards/{id}/redemptions",
			Summary:    "A managed reward's redemptions waiting to be done or refunded, oldest first (up to 50).",
			Permission: PermRewardsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Twitch.Redemptions(r.Context(), r.PathValue("id"))
				if err != nil {
					rewardError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"redemptions": list})
			},
		},
		{
			Method: "POST", Path: "/v1/rewards/{id}/redemptions/{redemption}",
			Summary: "Complete {action: done} or refund {action: refund} a redemption of a managed reward. " +
				"Repeating it is harmless; the other one after it's handled is refused (409).",
			Permission: PermRewardsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Action string `json:"action"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.Action != "done" && in.Action != "refund" {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "action is done or refund")
					return
				}
				rd, repeated, err := d.Twitch.SetRedemption(r.Context(), r.PathValue("id"), r.PathValue("redemption"), in.Action == "done")
				if err != nil {
					rewardError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"redemption": rd, "repeated": repeated})
			},
		},
	}
}
