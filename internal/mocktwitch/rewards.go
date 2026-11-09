package mocktwitch

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Channel point rewards, as Twitch's API reference describes them
// (checked 2026-11-09): only Affiliates and Partners have them; up to 50
// per channel; titles unique; only the app (Client ID) that made a reward
// may change, delete, or list and update its redemptions; a redemption
// moves only from UNFULFILLED, to FULFILLED or CANCELED (a refund).

// dashboardClient marks rewards made on Twitch itself.
const dashboardClient = "twitch-dashboard"

type mockReward struct {
	ID                  string `json:"id"`
	Title               string `json:"title"`
	Prompt              string `json:"prompt"`
	Cost                int    `json:"cost"`
	BackgroundColor     string `json:"background_color"`
	IsEnabled           bool   `json:"is_enabled"`
	IsPaused            bool   `json:"is_paused"`
	IsInStock           bool   `json:"is_in_stock"`
	IsUserInputRequired bool   `json:"is_user_input_required"`
	SkipQueue           bool   `json:"should_redemptions_skip_request_queue"`
	maxPerStream        int
	maxPerUser          int
	cooldown            int
	clientID            string
	broadcaster         string // user id
}

type mockRedemption struct {
	ID, RewardID, UserID, UserLogin, UserName, Input, Status, RedeemedAt string
	broadcaster                                                          string
}

type rewardState struct {
	rewards      map[string]*mockReward // by id
	redemptions  map[string]*mockRedemption
	notAffiliate map[string]bool // by user id
}

func (s *Server) rewardsInit() {
	if s.rw.rewards == nil {
		s.rw.rewards = map[string]*mockReward{}
		s.rw.redemptions = map[string]*mockRedemption{}
		s.rw.notAffiliate = map[string]bool{}
	}
}

// SetAffiliate says whether a channel has channel points (default yes).
func (s *Server) SetAffiliate(channelLogin string, yes bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewardsInit()
	s.rw.notAffiliate[s.userIDLocked(channelLogin)] = !yes
}

// AddDashboardReward adds a reward as if made on Twitch's own site: no app
// may manage it. Returns its id.
func (s *Server) AddDashboardReward(channelLogin, title string, cost int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewardsInit()
	r := &mockReward{ID: random(12), Title: title, Cost: cost, IsEnabled: true, IsInStock: true, BackgroundColor: "#9147FF",
		clientID: dashboardClient, broadcaster: s.userIDLocked(channelLogin)}
	s.rw.rewards[r.ID] = r
	return r.ID
}

// RedemptionStatus is a redemption's status (UNFULFILLED, FULFILLED or
// CANCELED), or "" when there's none.
func (s *Server) RedemptionStatus(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewardsInit()
	if r := s.rw.redemptions[id]; r != nil {
		return r.Status
	}
	return ""
}

// RewardTitles lists a channel's rewards' titles, for tests.
func (s *Server) RewardTitles(channelLogin string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewardsInit()
	id := s.userIDLocked(channelLogin)
	var out []string
	for _, r := range s.rw.rewards {
		if r.broadcaster == id {
			out = append(out, r.Title)
		}
	}
	slices.Sort(out)
	return out
}

// findRewardLocked finds a channel's reward by title.
func (s *Server) findRewardLocked(broadcaster, title string) *mockReward {
	for _, r := range s.rw.rewards {
		if r.broadcaster == broadcaster && strings.EqualFold(r.Title, title) {
			return r
		}
	}
	return nil
}

func (r *mockReward) json() map[string]any {
	b, _ := json.Marshal(r)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["broadcaster_id"] = r.broadcaster
	m["max_per_stream_setting"] = map[string]any{"is_enabled": r.maxPerStream > 0, "max_per_stream": r.maxPerStream}
	m["max_per_user_per_stream_setting"] = map[string]any{"is_enabled": r.maxPerUser > 0, "max_per_user_per_stream": r.maxPerUser}
	m["global_cooldown_setting"] = map[string]any{"is_enabled": r.cooldown > 0, "global_cooldown_seconds": r.cooldown}
	m["redemptions_redeemed_current_stream"] = nil
	m["cooldown_expires_at"] = nil
	return m
}

func (d *mockRedemption) json(r *mockReward) map[string]any {
	return map[string]any{"broadcaster_id": d.broadcaster, "id": d.ID, "user_id": d.UserID, "user_login": d.UserLogin,
		"user_name": d.UserName, "user_input": d.Input, "status": d.Status, "redeemed_at": d.RedeemedAt,
		"reward": map[string]any{"id": r.ID, "title": r.Title, "prompt": r.Prompt, "cost": r.Cost}}
}

// rewardsAuth checks what every reward endpoint checks: the token (with one
// of scopes), broadcaster_id matching it, and an Affiliate or Partner.
func (s *Server) rewardsAuth(w http.ResponseWriter, r *http.Request, scopes ...string) (*grant, bool) {
	g, ok := s.helixAuth(w, r, "")
	if !ok {
		return nil, false
	}
	allowed := false
	for _, sc := range scopes {
		allowed = allowed || contains(g.scopes, sc)
	}
	if !allowed {
		writeErr(w, 401, "Missing scope: "+scopes[0])
		return nil, false
	}
	if b := r.URL.Query().Get("broadcaster_id"); b == "" || b != g.userID {
		writeErr(w, 400, "The broadcaster_id query parameter is required and must match the user ID in the access token.")
		return nil, false
	}
	if s.rw.notAffiliate[g.userID] {
		writeErr(w, 403, "The broadcaster is not a partner or affiliate.")
		return nil, false
	}
	return g, true
}

// rewardBody is a Create or Update body; nil fields aren't given.
type rewardBody struct {
	Title               *string `json:"title"`
	Cost                *int    `json:"cost"`
	Prompt              *string `json:"prompt"`
	BackgroundColor     *string `json:"background_color"`
	IsEnabled           *bool   `json:"is_enabled"`
	IsPaused            *bool   `json:"is_paused"`
	IsUserInputRequired *bool   `json:"is_user_input_required"`
	SkipQueue           *bool   `json:"should_redemptions_skip_request_queue"`
	MaxPerStreamOn      *bool   `json:"is_max_per_stream_enabled"`
	MaxPerStream        *int    `json:"max_per_stream"`
	MaxPerUserOn        *bool   `json:"is_max_per_user_per_stream_enabled"`
	MaxPerUser          *int    `json:"max_per_user_per_stream"`
	CooldownOn          *bool   `json:"is_global_cooldown_enabled"`
	Cooldown            *int    `json:"global_cooldown_seconds"`
}

// apply checks a body against Twitch's documented limits and applies it.
func (b rewardBody) apply(r *mockReward) string {
	if b.Title != nil {
		if n := utf8.RuneCountInString(*b.Title); n < 1 || n > 45 {
			return "The title must be 1 to 45 characters."
		}
		r.Title = *b.Title
	}
	if b.Cost != nil {
		if *b.Cost < 1 {
			return "The cost must be at least 1."
		}
		r.Cost = *b.Cost
	}
	if b.Prompt != nil {
		if utf8.RuneCountInString(*b.Prompt) > 200 {
			return "The prompt is limited to a maximum of 200 characters."
		}
		r.Prompt = *b.Prompt
	}
	if b.BackgroundColor != nil {
		r.BackgroundColor = *b.BackgroundColor
	}
	for _, f := range []struct {
		v   *bool
		dst *bool
	}{{b.IsEnabled, &r.IsEnabled}, {b.IsPaused, &r.IsPaused}, {b.IsUserInputRequired, &r.IsUserInputRequired}, {b.SkipQueue, &r.SkipQueue}} {
		if f.v != nil {
			*f.dst = *f.v
		}
	}
	limit := func(on *bool, v *int, dst *int, max int, what string) string {
		if on == nil {
			return ""
		}
		if !*on {
			*dst = 0
			return ""
		}
		if v == nil || *v < 1 || (max > 0 && *v > max) {
			return "The " + what + " is out of range."
		}
		*dst = *v
		return ""
	}
	if e := limit(b.MaxPerStreamOn, b.MaxPerStream, &r.maxPerStream, 0, "max_per_stream"); e != "" {
		return e
	}
	if e := limit(b.MaxPerUserOn, b.MaxPerUser, &r.maxPerUser, 0, "max_per_user_per_stream"); e != "" {
		return e
	}
	return limit(b.CooldownOn, b.Cooldown, &r.cooldown, 604800, "global_cooldown_seconds")
}

func (s *Server) customRewards(w http.ResponseWriter, r *http.Request) {
	s.rewardsInit()
	q := r.URL.Query()
	switch r.Method {
	case "GET":
		g, ok := s.rewardsAuth(w, r, "channel:read:redemptions", "channel:manage:redemptions")
		if !ok {
			return
		}
		ids, only := q["id"], q.Get("only_manageable_rewards") == "true"
		var list []*mockReward
		for _, x := range s.rw.rewards {
			if x.broadcaster != g.userID || (only && x.clientID != g.clientID) || (len(ids) > 0 && !contains(ids, x.ID)) {
				continue
			}
			list = append(list, x)
		}
		if len(ids) > 0 && len(list) == 0 {
			writeErr(w, 404, "All of the custom rewards specified using the id query parameter were not found.")
			return
		}
		slices.SortFunc(list, func(a, b *mockReward) int { return strings.Compare(a.ID, b.ID) })
		data := []map[string]any{}
		for _, x := range list {
			data = append(data, x.json())
		}
		writeJSON(w, map[string]any{"data": data})
	case "POST":
		g, ok := s.rewardsAuth(w, r, "channel:manage:redemptions")
		if !ok {
			return
		}
		var b rewardBody
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Title == nil || b.Cost == nil {
			writeErr(w, 400, "The title and cost are required.")
			return
		}
		n := 0
		for _, x := range s.rw.rewards {
			if x.broadcaster == g.userID {
				n++
			}
		}
		if n >= 50 {
			writeErr(w, 400, "The broadcaster has reached the maximum number of custom rewards.")
			return
		}
		if s.findRewardLocked(g.userID, *b.Title) != nil {
			writeErr(w, 400, "CREATE_CUSTOM_REWARD_DUPLICATE_REWARD")
			return
		}
		x := &mockReward{ID: random(12), IsEnabled: true, IsInStock: true, BackgroundColor: "#9147FF", clientID: g.clientID, broadcaster: g.userID}
		if msg := b.apply(x); msg != "" {
			writeErr(w, 400, msg)
			return
		}
		s.rw.rewards[x.ID] = x
		writeJSON(w, map[string]any{"data": []any{x.json()}})
	case "PATCH", "DELETE":
		g, ok := s.rewardsAuth(w, r, "channel:manage:redemptions")
		if !ok {
			return
		}
		x := s.rw.rewards[q.Get("id")]
		if x == nil || x.broadcaster != g.userID {
			writeErr(w, 404, "The custom reward specified in the id query parameter was not found.")
			return
		}
		if x.clientID != g.clientID {
			writeErr(w, 403, "The ID in the Client-Id header must match the client ID used to create the custom reward.")
			return
		}
		if r.Method == "DELETE" {
			for _, d := range s.rw.redemptions {
				if d.RewardID == x.ID && d.Status == "UNFULFILLED" {
					d.Status = "FULFILLED"
				}
			}
			delete(s.rw.rewards, x.ID)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var b rewardBody
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, 400, "The body isn't JSON.")
			return
		}
		if b.Title != nil {
			if other := s.findRewardLocked(g.userID, *b.Title); other != nil && other != x {
				writeErr(w, 400, "CREATE_CUSTOM_REWARD_DUPLICATE_REWARD")
				return
			}
		}
		next := *x
		if msg := b.apply(&next); msg != "" {
			writeErr(w, 400, msg)
			return
		}
		*x = next
		writeJSON(w, map[string]any{"data": []any{x.json()}})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) customRedemptions(w http.ResponseWriter, r *http.Request) {
	s.rewardsInit()
	q := r.URL.Query()
	scopes := []string{"channel:manage:redemptions"}
	if r.Method == "GET" {
		scopes = append([]string{"channel:read:redemptions"}, scopes...)
	}
	g, ok := s.rewardsAuth(w, r, scopes...)
	if !ok {
		return
	}
	x := s.rw.rewards[q.Get("reward_id")]
	if q.Get("reward_id") == "" {
		writeErr(w, 400, "The reward_id query parameter is required.")
		return
	}
	if x == nil || x.broadcaster != g.userID {
		writeErr(w, 404, "The reward_id was not found.")
		return
	}
	if x.clientID != g.clientID {
		writeErr(w, 403, "The ID in the Client-Id header must match the client ID used to create the custom reward.")
		return
	}
	ids := q["id"]
	switch r.Method {
	case "GET":
		status := q.Get("status")
		if len(ids) == 0 && status != "UNFULFILLED" && status != "FULFILLED" && status != "CANCELED" {
			writeErr(w, 400, "The status query parameter is required and must be CANCELED, FULFILLED or UNFULFILLED.")
			return
		}
		var list []*mockRedemption
		for _, d := range s.rw.redemptions {
			if d.RewardID == x.ID && (len(ids) == 0 || contains(ids, d.ID)) && (status == "" || d.Status == status) {
				list = append(list, d)
			}
		}
		if len(ids) > 0 && len(list) == 0 {
			writeErr(w, 404, "All of the redemptions specified using the id query parameter were not found.")
			return
		}
		slices.SortFunc(list, func(a, b *mockRedemption) int { return strings.Compare(a.RedeemedAt, b.RedeemedAt) })
		if q.Get("sort") == "NEWEST" {
			slices.Reverse(list)
		}
		data := []map[string]any{}
		for _, d := range list {
			data = append(data, d.json(x))
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	case "PATCH":
		var b struct {
			Status string `json:"status"`
		}
		if len(ids) == 0 || json.NewDecoder(r.Body).Decode(&b) != nil || (b.Status != "FULFILLED" && b.Status != "CANCELED") {
			writeErr(w, 400, "The id query parameter and a status of CANCELED or FULFILLED are required.")
			return
		}
		data := []map[string]any{}
		for _, id := range ids {
			if d := s.rw.redemptions[id]; d != nil && d.RewardID == x.ID && d.Status == "UNFULFILLED" {
				d.Status = b.Status
				data = append(data, d.json(x))
			}
		}
		if len(data) == 0 {
			writeErr(w, 404, "The redemptions weren't found or their statuses weren't marked as UNFULFILLED.")
			return
		}
		writeJSON(w, map[string]any{"data": data})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// redeemLocked records a redemption of a channel's reward, if it has one
// by that title, and returns the reward's id, cost and the redemption's
// status. Without one it makes up an id, as before rewards existed here.
func (s *Server) redeemLocked(channelLogin, viewer, title, id, input string, cost int) (rewardID string, rewardCost int, status string) {
	s.rewardsInit()
	b := s.userIDLocked(channelLogin)
	x := s.findRewardLocked(b, title)
	if x == nil {
		return "reward-" + strings.ToLower(strings.ReplaceAll(title, " ", "-")), cost, "unfulfilled"
	}
	st := "UNFULFILLED"
	if x.SkipQueue {
		st = "FULFILLED"
	}
	s.rw.redemptions[id] = &mockRedemption{ID: id, RewardID: x.ID, UserID: s.userIDLocked(viewer), UserLogin: strings.ToLower(viewer),
		UserName: viewer, Input: input, Status: st, RedeemedAt: s.now().UTC().Format(time.RFC3339Nano), broadcaster: b}
	return x.ID, x.Cost, strings.ToLower(st)
}
