package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/filters"
)

// Chat filters (D-085).

// FiltersView is the body of GET and PUT /v1/filters.
type FiltersView struct {
	Settings filters.Settings `json:"settings"`
	// Recent is what the filters caught lately, newest first.
	Recent []FilterCatch `json:"recent"`
}

// FilterCatch is one message a filter caught.
type FilterCatch struct {
	At      time.Time `json:"at"`
	Rule    string    `json:"rule"`
	Action  string    `json:"action"`
	Seconds int       `json:"seconds,omitempty"`
	// State is Twitch's answer: done, refused, unknown, or test.
	State  string `json:"state"`
	Answer string `json:"answer,omitempty"`
	Text   string `json:"text"`
	UserID string `json:"user_id"`
	Login  string `json:"login"`
	Name   string `json:"name"`
}

// FilterCheck is the body of POST /v1/filters/check.
type FilterCheck struct {
	Text string `json:"text"`
	// Role is viewer (the default), subscriber, vip, moderator or broadcaster.
	Role string `json:"role,omitempty"`
	// Settings, when given, are judged instead of the saved ones.
	Settings *filters.Settings `json:"settings,omitempty"`
}

// FilterVerdict is POST /v1/filters/check's answer: the filter that would
// catch the message, or "".
type FilterVerdict struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason,omitempty"`
}

func filtersView(r *http.Request, d Deps, st filters.Settings) (FiltersView, error) {
	recs, err := d.Filters.Recent(r.Context(), 30)
	if err != nil {
		return FiltersView{}, err
	}
	v := FiltersView{Settings: st, Recent: []FilterCatch{}}
	for _, rec := range recs {
		var p struct {
			Rule    string `json:"rule"`
			Action  string `json:"action"`
			Seconds int    `json:"seconds"`
			State   string `json:"state"`
			Answer  string `json:"answer"`
			Text    string `json:"text"`
		}
		_ = json.Unmarshal(rec.Payload, &p)
		c := FilterCatch{At: rec.OccurredAt, Rule: p.Rule, Action: p.Action, Seconds: p.Seconds, State: p.State, Answer: p.Answer, Text: p.Text}
		if a := rec.Actor; a != nil {
			c.UserID, c.Login, c.Name = a.ID, a.Login, a.DisplayName
		}
		v.Recent = append(v.Recent, c)
	}
	return v, nil
}

var filterRoles = map[string]bool{"viewer": true, "subscriber": true, "vip": true, "moderator": true, "broadcaster": true}

func filterRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/filters",
			Summary: "The chat filters' settings (links, caps, symbols, emotes, repeats, words: each on or off, delete, timeout or warn, " +
				"and who's left alone), and the 30 messages they caught most lately, with Twitch's answer.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				st, err := d.Filters.Settings(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				v, err := filtersView(r, d, st)
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "PUT", Path: "/v1/filters",
			Summary: "Save the chat filters' settings, all at once (GET's settings, changed). They apply to the next message. " +
				"Words are saved in lower case; a * goes only at a word's start or end.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in filters.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				st, err := d.Filters.SetSettings(r.Context(), in)
				if errors.Is(err, filters.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), "filters: invalid: "))
					return
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				v, err := filtersView(r, d, st)
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		},
		{
			Method: "POST", Path: "/v1/filters/check",
			Summary: "Which filter would catch a message: {text, role?, settings?}. Judged as if the filters were on, with the rules that " +
				"are on; repeats need several messages, so it never answers repeats. Nothing is sent or stored.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in FilterCheck
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.Role == "" {
					in.Role = "viewer"
				}
				if !filterRoles[in.Role] || len(in.Text) > 2000 {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "role is viewer, subscriber, vip, moderator or broadcaster, and text up to 2000 bytes")
					return
				}
				m := filters.Message{Platform: "twitch", UserID: "check", Login: "check", Role: in.Role, Text: in.Text}
				rule, err := d.Filters.Try(r.Context(), in.Settings, m)
				if errors.Is(err, filters.ErrInvalid) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), "filters: invalid: "))
					return
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, FilterVerdict{Rule: rule, Reason: filters.Reason(rule)})
			},
		},
	}
}
