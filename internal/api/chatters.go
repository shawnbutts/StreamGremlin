package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// The Live screen's viewer card (D-078): what Twitch says about a chatter,
// asked fresh each time, with StreamGremlin's own record of them.

// ChatterCard is the body of GET /v1/chatters/{platform}/{user_id}.
type ChatterCard struct {
	Platform string `json:"platform"`
	UserID   string `json:"user_id"`
	// Twitch is what Twitch says; nil when it wasn't asked (a test chatter).
	Twitch *twitch.Card `json:"twitch"`
	// Viewer is StreamGremlin's record and points; nil when there's none.
	Viewer   *ViewerView `json:"viewer"`
	Currency string      `json:"currency"`
	// Messages are their newest chat messages still kept, oldest first.
	Messages []events.Record `json:"messages"`
}

// cardMessages is how many recent messages a card shows.
const cardMessages = 10

func chatterRoutes(d Deps) []Route {
	return []Route{{
		Method: "GET", Path: "/v1/chatters/{platform}/{user_id}",
		Summary: "A viewer card for a chatter: what Twitch says now (account, follow, subscription, VIP, moderator, banned; " +
			"each fact it couldn't read is listed as unknown, with why), StreamGremlin's record and points, and their newest " +
			"10 chat messages. ?test=1 is for a test chatter: Twitch isn't asked and test messages are listed.",
		Permission: PermViewersRead,
		Handler: func(w http.ResponseWriter, r *http.Request) {
			platform, id := r.PathValue("platform"), r.PathValue("user_id")
			if platform != "twitch" {
				writeError(w, http.StatusBadRequest, CodeInvalidRequest, "platform must be twitch")
				return
			}
			if id == "" || len(id) > 64 {
				writeError(w, http.StatusBadRequest, CodeInvalidRequest, "user_id must be 1 to 64 characters")
				return
			}
			test := r.URL.Query().Get("test") == "1"
			out := ChatterCard{Platform: platform, UserID: id, Messages: []events.Record{}}
			if !test {
				if d.Twitch == nil {
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "Twitch isn't set up")
					return
				}
				c, err := d.Twitch.ViewerCard(r.Context(), id)
				if errors.Is(err, twitch.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
					return
				} else if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				out.Twitch = &c
			}
			if !test && d.Viewers != nil {
				v, err := d.Viewers.ByAccount(r.Context(), platform, id)
				switch {
				case err == nil:
					b, err := d.Points.Balance(r.Context(), v.ID, "")
					if err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
					out.Viewer = &ViewerView{Viewer: v, Balance: b}
				case !errors.Is(err, viewers.ErrNotFound):
					internalError(w, r, d.Logger, err)
					return
				}
			}
			if d.Points != nil {
				cur, _ := d.Points.Currency(r.Context(), "")
				out.Currency = cur.Name
			}
			msgs, err := d.Events.ChatFrom(r.Context(), platform, id, cardMessages, test)
			if err != nil {
				internalError(w, r, d.Logger, err)
				return
			}
			out.Messages = msgs
			writeJSON(w, http.StatusOK, out)
		},
	}}
}
