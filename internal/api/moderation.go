package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Moderating from chat (M7 slice 3; D-079, ADR-0041).

// ModerationResult is the body of POST /v1/moderation.
type ModerationResult struct {
	Record moderation.Record `json:"record"`
	// Duplicate is true when the request id was seen before: nothing was
	// sent again, and Record is how it went the first time.
	Duplicate bool `json:"duplicate"`
}

// ChatModesView is the body of GET /v1/chat/settings.
type ChatModesView struct {
	// Settings are nil when Twitch couldn't be asked; Problem says why.
	Settings *twitch.ChatSettings `json:"settings"`
	Problem  string               `json:"problem,omitempty"`
	// Shield is Shield Mode, nil when unknown (ShieldProblem says why);
	// Lock is the lock in place from StreamGremlin, if any (D-105).
	Shield        *twitch.Shield     `json:"shield"`
	ShieldProblem string             `json:"shield_problem,omitempty"`
	Lock          *moderation.Record `json:"lock"`
}

// Suspicion is what Twitch last said about a suspicious chatter, while
// StreamGremlin was listening.
type Suspicion struct {
	// Treatment is none, active_monitoring or restricted.
	Treatment  string   `json:"treatment"`
	BanEvasion string   `json:"ban_evasion,omitempty"` // likely, possible, unknown or ""
	Types      []string `json:"types"`
	SharedBans int      `json:"shared_bans"`
	At         string   `json:"at"`
	// Seq is the newest event it comes from.
	Seq int64 `json:"seq"`
}

// suspicion reads the newest suspicious-user events for one account.
func suspicion(ctx context.Context, in *events.Inbox, platform, userID string) (*Suspicion, error) {
	msg, okMsg, err := in.LatestFrom(ctx, platform, userID, events.SuspiciousMessage)
	if err != nil {
		return nil, err
	}
	upd, okUpd, err := in.LatestFrom(ctx, platform, userID, events.SuspiciousUpdated)
	if err != nil || (!okMsg && !okUpd) {
		return nil, err
	}
	s := &Suspicion{Types: []string{}}
	if okMsg {
		var p struct {
			Treatment  string   `json:"low_trust_status"`
			BanEvasion string   `json:"ban_evasion"`
			Types      []string `json:"types"`
			Shared     []string `json:"shared_ban_channel_ids"`
		}
		_ = json.Unmarshal(msg.Payload, &p)
		s.Treatment, s.BanEvasion, s.SharedBans, s.At, s.Seq = p.Treatment, p.BanEvasion, len(p.Shared), msg.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), msg.Seq
		if p.Types != nil {
			s.Types = p.Types
		}
	}
	if okUpd && (!okMsg || upd.Seq > msg.Seq) {
		var p struct {
			Treatment string `json:"low_trust_status"`
		}
		_ = json.Unmarshal(upd.Payload, &p)
		s.Treatment, s.At, s.Seq = p.Treatment, upd.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), upd.Seq
	}
	return s, nil
}

func moderationRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "POST", Path: "/v1/moderation",
			Summary: "Moderate on Twitch or YouTube as you, once: {request_id, action, platform, user_id, login?, message_id?, seconds?, " +
				"reason?, chat_modes?, from}. Twitch: delete (message_id), timeout (seconds 1-1209600, reason?), ban (reason?), unban, warn " +
				"(reason), shoutout, vip, unvip, mod, unmod, monitor, restrict, unsuspect, chat_modes, shield_mode (on: true or false), lock " +
				"(Shield Mode, emote-only and followers-only on, recording what was there) and unlock (puts back what the lock changed). YouTube (user_id is the channel id; " +
				"quota units in brackets): delete (message_id) [20], timeout (seconds, reason? kept here only) [200], ban [200], unban, only " +
				"of a ban made from StreamGremlin [200], mod [200], unmod [200, +50 to look them up]. Recorded with the answer: done, " +
				"refused (with why) or unknown (no answer: never sent again). The same request_id again returns the record and sends nothing.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var req moderation.Request
				if !decodeJSON(w, r, &req) {
					return
				}
				if req.From == moderation.FromFilter || req.From == moderation.FromWorkflow {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "from is live or cli: only the chat filters and workflows act as filter and workflow")
					return
				}
				rec, dup, err := d.Moderation.Do(r.Context(), req)
				switch {
				case errors.Is(err, moderation.ErrInvalid):
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
				case errors.Is(err, moderation.ErrConflict):
					writeError(w, http.StatusConflict, CodeConflict, err.Error())
				case err != nil:
					internalError(w, r, d.Logger, err)
				default:
					writeJSON(w, http.StatusOK, ModerationResult{Record: rec, Duplicate: dup})
				}
			},
		},
		{
			Method: "GET", Path: "/v1/moderation",
			Summary:    "Moderating done from StreamGremlin, newest first: everyone's, or ?platform=twitch|youtube&user_id= one account's. ?limit= 1-500 (default 50).",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 50, 1, 500)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1 to 500")
					return
				}
				list, err := d.Moderation.List(r.Context(), r.URL.Query().Get("platform"), r.URL.Query().Get("user_id"), limit)
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"moderation": list})
			},
		},
		{
			Method: "GET", Path: "/v1/chat/settings",
			Summary: "Your Twitch chat modes now: emote-only, followers-only (and for how long), slow (and the wait), subscribers-only, " +
				"unique chat; Shield Mode; and the lock in place from StreamGremlin, with what it changed. Unknown, with why, when Twitch " +
				"can't be asked.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if d.Twitch == nil {
					writeJSON(w, http.StatusOK, ChatModesView{Problem: "Twitch isn't set up."})
					return
				}
				var v ChatModesView
				if cs, err := d.Twitch.ChatSettings(r.Context()); err != nil {
					v.Problem = chatModesProblem(err)
				} else {
					v.Settings = &cs
				}
				if sh, err := d.Twitch.ShieldMode(r.Context()); err != nil {
					v.ShieldProblem = chatModesProblem(err)
				} else {
					v.Shield = &sh
				}
				if d.Moderation != nil {
					lock, err := d.Moderation.Locked(r.Context())
					if err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
					v.Lock = lock
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
	}
}

func chatModesProblem(err error) string {
	switch {
	case errors.Is(err, twitch.ErrNeedsPermission):
		return "Sign in to Twitch again and allow it."
	case errors.Is(err, twitch.ErrNoClientID):
		return "Twitch isn't set up: add a Client ID on Connections."
	}
	if twitch.IsSignInError(err) {
		return "Sign in with your Twitch account first."
	}
	return "Twitch didn't answer."
}
