package mocktwitch

import (
	"net/http"
	"sort"
	"strings"
)

// Block User and Unblock User (M8 slice 5): the signed-in user's block
// list. Whether a block removes a follow isn't documented, so here it
// doesn't.

// blocksLocked answers PUT and DELETE /helix/users/blocks; ok is false for
// other paths.
func (s *Server) blocksLocked(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/helix/users/blocks" || (r.Method != "PUT" && r.Method != "DELETE") {
		return false
	}
	g, ok := s.helixAuth(w, r, "user:manage:blocked_users")
	if !ok {
		return true
	}
	q := r.URL.Query()
	target := s.loginOfLocked(q.Get("target_user_id"))
	if target == "" {
		writeErr(w, 400, "The ID in target_user_id is not valid.")
		return true
	}
	if r.Method == "PUT" {
		switch q.Get("source_context") {
		case "", "chat", "whisper":
		default:
			writeErr(w, 400, "The value in source_context is not valid.")
			return true
		}
		if s.blocked == nil {
			s.blocked = map[string]map[string]bool{}
		}
		if s.blocked[g.login] == nil {
			s.blocked[g.login] = map[string]bool{}
		}
		s.blocked[g.login][target] = true
	} else {
		delete(s.blocked[g.login], target)
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// Blocked lists whom user has blocked, by login.
func (s *Server) Blocked(user string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for v := range s.blocked[strings.ToLower(user)] {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
