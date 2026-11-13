package mocktwitch

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Twitch's Blocked Terms (D-088): Get, Add and Remove Blocked Term, as the
// reference documents them.

type blockedState struct {
	terms map[string][]BlockedTerm // by channel login, newest first
	next  int
	busy  int // answer this many adds with 409, as when another change is pending
	adds  int
}

// BlockedTerm is a term on a channel's list.
type BlockedTerm struct {
	ID, Text  string
	CreatedAt time.Time
	ExpiresAt time.Time // zero: added by hand
}

// BlockedTerms returns a channel's terms, newest first.
func (s *Server) BlockedTerms(login string) []BlockedTerm {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]BlockedTerm(nil), s.bt.terms[strings.ToLower(login)]...)
}

// BlockTerm puts a term on a channel's list, as if added on Twitch; a
// nonzero expires is a term AutoMod blocked for a while.
func (s *Server) BlockTerm(login, text string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addTermLocked(strings.ToLower(login), text, expires)
}

// SetBlockedTermsBusy answers the next n adds with 409.
func (s *Server) SetBlockedTermsBusy(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bt.busy = n
}

// BlockedTermAdds counts Add Blocked Term requests answered 200.
func (s *Server) BlockedTermAdds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bt.adds
}

func (s *Server) addTermLocked(channel, text string, expires time.Time) BlockedTerm {
	if s.bt.terms == nil {
		s.bt.terms = map[string][]BlockedTerm{}
	}
	for _, t := range s.bt.terms[channel] {
		if strings.EqualFold(t.Text, text) {
			return t
		}
	}
	s.bt.next++
	t := BlockedTerm{ID: "bt-" + strconv.Itoa(s.bt.next), Text: text, CreatedAt: s.now().UTC(), ExpiresAt: expires}
	s.bt.terms[channel] = append([]BlockedTerm{t}, s.bt.terms[channel]...)
	return t
}

func (s *Server) termJSON(channel string, t BlockedTerm) map[string]any {
	id := s.userIDLocked(channel)
	var exp any
	if !t.ExpiresAt.IsZero() {
		exp = t.ExpiresAt.UTC().Format(time.RFC3339)
	}
	at := t.CreatedAt.Format(time.RFC3339)
	return map[string]any{"broadcaster_id": id, "moderator_id": id, "id": t.ID, "text": t.Text,
		"created_at": at, "updated_at": at, "expires_at": exp}
}

// blockedTermsLocked answers the three requests; false when r isn't one.
func (s *Server) blockedTermsLocked(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/helix/moderation/blocked_terms" {
		return false
	}
	scope := "moderator:manage:blocked_terms"
	g, ok := s.helixAuth(w, r, "")
	if !ok {
		return true
	}
	if !contains(g.scopes, scope) && (r.Method != "GET" || !contains(g.scopes, "moderator:read:blocked_terms")) {
		writeErr(w, 401, "Missing scope: "+scope)
		return true
	}
	q := r.URL.Query()
	switch {
	case q.Get("broadcaster_id") == "", q.Get("moderator_id") == "":
		writeErr(w, 400, "The broadcaster_id and moderator_id query parameters are required.")
		return true
	case q.Get("moderator_id") != g.userID:
		writeErr(w, 401, "The moderator_id query parameter must match the user ID in the access token.")
		return true
	case q.Get("broadcaster_id") != g.userID:
		writeErr(w, 403, "The user in moderator_id is not one of the broadcaster's moderators.")
		return true
	}
	channel := g.login
	switch r.Method {
	case "GET":
		first := 20
		if f := q.Get("first"); f != "" {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > 100 {
				writeErr(w, 400, "first must be 1 to 100")
				return true
			}
			first = n
		}
		start := 0
		if a := q.Get("after"); a != "" {
			n, err := strconv.Atoi(a)
			if err != nil || n < 0 {
				writeErr(w, 400, "The cursor in the after query parameter is not valid.")
				return true
			}
			start = n
		}
		all := s.bt.terms[channel]
		data := []any{}
		for i := start; i < len(all) && i < start+first; i++ {
			data = append(data, s.termJSON(channel, all[i]))
		}
		page := map[string]any{}
		if len(data) == first { // as documented: a full page has a cursor, even at the end
			page["cursor"] = strconv.Itoa(start + first)
		}
		writeJSON(w, map[string]any{"data": data, "pagination": page})
	case "POST":
		var in struct {
			Text string `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeErr(w, 400, "The text field is required.")
			return true
		}
		n := utf8.RuneCountInString(strings.ReplaceAll(strings.ReplaceAll(in.Text, "*", ""), " ", ""))
		if n < 2 || (strings.Contains(in.Text, "*") && n < 3) || utf8.RuneCountInString(in.Text) > 500 {
			writeErr(w, 400, "The length of the term in the text field is either too short or too long.")
			return true
		}
		if s.bt.busy > 0 {
			s.bt.busy--
			writeErr(w, 409, "The blocked term is currently being updated. Please retry your request.")
			return true
		}
		s.bt.adds++
		writeJSON(w, map[string]any{"data": []any{s.termJSON(channel, s.addTermLocked(channel, in.Text, time.Time{}))}})
	case "DELETE":
		id := q.Get("id")
		if id == "" {
			writeErr(w, 400, "The id query parameter is required.")
			return true
		}
		list := s.bt.terms[channel]
		for i, t := range list {
			if t.ID == id {
				s.bt.terms[channel] = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent) // also when Twitch has no such id
	default:
		writeErr(w, 405, "Method not allowed")
	}
	return true
}
