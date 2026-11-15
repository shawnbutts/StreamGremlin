package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/followbots"
)

// The follow-bot review (M8 slice 5, D-109).

func followBotRoutes(d Deps) []Route {
	if d.FollowBots == nil {
		return nil
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/follow-bots",
			Summary: "Twitch follows that look like follow bots, newest first: from accounts younger than ?days= (7) when they followed, " +
				"that never chatted, within the last ?hours= (24); in_burst when ?burst= (5) or more came within ?minutes= (10). Asks " +
				"Twitch each follower's age, 100 at a time. Nothing is done: block or ban them with POST /v1/ban-lists.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				o := followbots.Defaults()
				q := r.URL.Query()
				for k, p := range map[string]*int{"hours": &o.Hours, "days": &o.Days, "burst": &o.Burst, "minutes": &o.Minutes} {
					if v := q.Get(k); v != "" {
						n, err := strconv.Atoi(v)
						if err != nil {
							writeError(w, http.StatusBadRequest, CodeInvalidRequest, k+" is a number")
							return
						}
						*p = n
					}
				}
				rv, err := followbots.Find(r.Context(), *d.FollowBots, o)
				switch {
				case errors.Is(err, followbots.ErrInvalid):
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), "followbots: invalid: "))
				case err != nil:
					writeError(w, http.StatusConflict, CodeConflict, "Twitch couldn't be asked: "+chatModesProblem(err))
				default:
					writeJSON(w, http.StatusOK, rv)
				}
			},
		},
	}
}
