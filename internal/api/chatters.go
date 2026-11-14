package api

import (
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
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
	// Moderation is what was done to them from StreamGremlin, newest first.
	Moderation []moderation.Record `json:"moderation"`
	// Suspicious is Twitch's newest flag on them while StreamGremlin was
	// listening; nil when Twitch hasn't flagged them in that time.
	Suspicious *Suspicion `json:"suspicious"`
	// YouTubeBan is the ban or timeout made on a YouTube viewer from
	// StreamGremlin and not undone from here: only it can be undone
	// (D-100). nil for Twitch, or when there's none.
	YouTubeBan *moderation.Record `json:"youtube_ban,omitempty"`
}

// cardMessages is how many recent messages a card shows.
const cardMessages = 10

func chatterRoutes(d Deps) []Route {
	return []Route{{
		Method: "GET", Path: "/v1/chatters/{platform}/{user_id}",
		Summary: "A viewer card for a chatter: what Twitch says now (account, follow, subscription, VIP, moderator, banned; " +
			"each fact it couldn't read is listed as unknown, with why), StreamGremlin's record and points, and their newest " +
			"10 chat messages, what was done to them from StreamGremlin, and Twitch's newest suspicious-user flag on them. ?test=1 is for a test chatter: Twitch isn't asked and test messages are listed. " +
			"For platform youtube: their newest messages, what was done to them from StreamGremlin, and youtube_ban, the ban or " +
			"timeout made from StreamGremlin that can be undone (YouTube lists no bans).",
		Permission: PermViewersRead,
		Handler: func(w http.ResponseWriter, r *http.Request) {
			platform, id := r.PathValue("platform"), r.PathValue("user_id")
			if platform != "twitch" && platform != events.SourceYouTube {
				writeError(w, http.StatusBadRequest, CodeInvalidRequest, "platform must be twitch or youtube")
				return
			}
			// A YouTube chatter's card has their newest messages and what was
			// done from StreamGremlin (D-100); YouTube has no facts to ask
			// about one viewer. Points come in M6 slice 6.
			yt := platform == events.SourceYouTube
			if id == "" || len(id) > 64 {
				writeError(w, http.StatusBadRequest, CodeInvalidRequest, "user_id must be 1 to 64 characters")
				return
			}
			test := r.URL.Query().Get("test") == "1"
			out := ChatterCard{Platform: platform, UserID: id, Messages: []events.Record{}}
			if !test && !yt {
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
			if !test && !yt && d.Viewers != nil {
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
			out.Moderation = []moderation.Record{}
			if !test && d.Moderation != nil {
				if out.Moderation, err = d.Moderation.List(r.Context(), platform, id, 10); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				if yt {
					if out.YouTubeBan, err = d.Moderation.YouTubeBan(r.Context(), id); err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
				}
			}
			if !test && !yt {
				if out.Suspicious, err = suspicion(r.Context(), d.Events, platform, id); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
			}
			writeJSON(w, http.StatusOK, out)
		},
	}}
}
