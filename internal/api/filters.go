package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/filters"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
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
	// Platform is twitch or youtube; Note says why YouTube got a delete
	// instead (D-100).
	Platform string `json:"platform"`
	Note     string `json:"note,omitempty"`
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
			Note    string `json:"note"`
		}
		_ = json.Unmarshal(rec.Payload, &p)
		c := FilterCatch{At: rec.OccurredAt, Rule: p.Rule, Action: p.Action, Seconds: p.Seconds, State: p.State, Answer: p.Answer, Text: p.Text,
			Platform: rec.Source, Note: p.Note}
		if a := rec.Actor; a != nil {
			c.UserID, c.Login, c.Name = a.ID, a.Login, a.DisplayName
		}
		v.Recent = append(v.Recent, c)
	}
	return v, nil
}

// BlockedTermsView is GET /v1/filters/blocked-terms: Twitch's list, or
// why it's unknown (D-088).
type BlockedTermsView struct {
	Terms   []twitch.BlockedTerm `json:"terms"`
	Problem string               `json:"problem,omitempty"`
}

// BlockedTermsAdd is the body of POST /v1/filters/blocked-terms.
type BlockedTermsAdd struct {
	Texts []string `json:"texts"`
}

// BlockedTermResult is Twitch's answer to adding one term: done (added, or
// there already), refused (with Twitch's words), or unknown.
type BlockedTermResult struct {
	Text   string              `json:"text"`
	State  string              `json:"state"`
	Answer string              `json:"answer,omitempty"`
	Term   *twitch.BlockedTerm `json:"term,omitempty"`
}

// MaxBlockedTermsAdd is the most terms one request adds.
const MaxBlockedTermsAdd = 100

func blockedTermsAnswer(err error) (state, answer string) {
	switch {
	case errors.Is(err, twitch.ErrOutcomeUnknown):
		return "unknown", "Twitch didn't answer, so it may or may not be on the list. Adding it again is safe."
	case errors.Is(err, twitch.ErrNeedsPermission):
		return "refused", "Sign in to Twitch again on Connections and allow StreamGremlin to manage blocked terms."
	case errors.Is(err, twitch.ErrNoClientID):
		return "refused", "Twitch isn't set up: add a Client ID on Connections."
	case twitch.IsSignInError(err):
		return "refused", "Sign in with your Twitch account first."
	}
	return "refused", strings.TrimPrefix(strings.TrimPrefix(err.Error(), "twitch: invalid: "), "twitch: ")
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
			Method: "GET", Path: "/v1/filters/blocked-terms",
			Summary: "Twitch's Blocked Terms for your channel, newest first: Twitch holds these messages back before anyone sees " +
				"them. Each has an id, text, created_at, and expires_at when AutoMod blocked it for a while. Unknown, with why, " +
				"when Twitch can't be asked.",
			Permission: PermModerationRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if d.Twitch == nil {
					writeJSON(w, http.StatusOK, BlockedTermsView{Terms: []twitch.BlockedTerm{}, Problem: "Twitch isn't set up."})
					return
				}
				terms, err := d.Twitch.BlockedTerms(r.Context())
				if err != nil {
					_, why := blockedTermsAnswer(err)
					writeJSON(w, http.StatusOK, BlockedTermsView{Terms: []twitch.BlockedTerm{}, Problem: why})
					return
				}
				writeJSON(w, http.StatusOK, BlockedTermsView{Terms: terms})
			},
		},
		{
			Method: "POST", Path: "/v1/filters/blocked-terms",
			Summary: "Add terms to Twitch's Blocked Terms: {texts: [...]}, up to 100, each 2 to 500 characters; a * goes at a word's " +
				"start or end. One answer per term: done (added, or already there), refused with Twitch's words, or unknown. " +
				"Adding a term again is safe.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in BlockedTermsAdd
				if !decodeJSON(w, r, &in) {
					return
				}
				if len(in.Texts) == 0 || len(in.Texts) > MaxBlockedTermsAdd {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "texts is 1 to 100 terms")
					return
				}
				if d.Twitch == nil {
					writeError(w, http.StatusConflict, CodeNotConnected, "Twitch isn't set up.")
					return
				}
				out := make([]BlockedTermResult, 0, len(in.Texts))
				for _, text := range in.Texts {
					res := BlockedTermResult{Text: strings.TrimSpace(text)}
					term, err := d.Twitch.AddBlockedTerm(r.Context(), text)
					if err == nil {
						res.State, res.Term = "done", &term
					} else {
						res.State, res.Answer = blockedTermsAnswer(err)
					}
					d.Logger.Info("blocked term added", "state", res.State)
					out = append(out, res)
				}
				writeJSON(w, http.StatusOK, map[string]any{"results": out})
			},
		},
		{
			Method: "DELETE", Path: "/v1/filters/blocked-terms/{id}",
			Summary:    "Remove a term from Twitch's Blocked Terms by its id. Removing one Twitch doesn't have is fine.",
			Permission: PermModerationWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if d.Twitch == nil {
					writeError(w, http.StatusConflict, CodeNotConnected, "Twitch isn't set up.")
					return
				}
				err := d.Twitch.RemoveBlockedTerm(r.Context(), r.PathValue("id"))
				if err != nil {
					state, why := blockedTermsAnswer(err)
					if state == "unknown" {
						writeError(w, http.StatusBadGateway, CodeUnknownOutcome, why)
					} else {
						writeError(w, http.StatusConflict, CodeConflict, why)
					}
					return
				}
				d.Logger.Info("blocked term removed", "id", r.PathValue("id"))
				w.WriteHeader(http.StatusNoContent)
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
