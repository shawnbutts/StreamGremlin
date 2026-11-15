package mocktwitch

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Polls and predictions, as Twitch's API reference describes them (read
// 2026-11-09): Affiliates and Partners only; one poll and one prediction
// at a time; a poll ends TERMINATED or ARCHIVED; a prediction locks,
// resolves (with a winner) or cancels. Each change sends its EventSub event.

type mockPoll struct {
	ID, Title, Status string
	Choices           []map[string]any // id, title, votes, channel_points_votes, bits_votes
	Duration, PerVote int
	Started           time.Time
	Ended             *time.Time
	broadcaster       string
}

type mockPrediction struct {
	ID, Title, Status, Winner string
	Outcomes                  []map[string]any // id, title, color, users, channel_points
	Window                    int
	Created                   time.Time
	Ended, Locked             *time.Time
	broadcaster               string
}

type pollState struct {
	polls       []*mockPoll // newest last
	predictions []*mockPrediction
}

func (p *mockPoll) json() map[string]any {
	m := map[string]any{"id": p.ID, "broadcaster_id": p.broadcaster, "title": p.Title, "choices": p.Choices, "status": p.Status,
		"duration": p.Duration, "channel_points_voting_enabled": p.PerVote > 0, "channel_points_per_vote": p.PerVote,
		"bits_voting_enabled": false, "bits_per_vote": 0, "started_at": p.Started.Format(time.RFC3339Nano), "ended_at": nil}
	if p.Ended != nil {
		m["ended_at"] = p.Ended.Format(time.RFC3339Nano)
	}
	return m
}

func (p *mockPrediction) json() map[string]any {
	m := map[string]any{"id": p.ID, "broadcaster_id": p.broadcaster, "title": p.Title, "outcomes": p.Outcomes, "status": p.Status,
		"prediction_window": p.Window, "created_at": p.Created.Format(time.RFC3339Nano), "winning_outcome_id": nil, "ended_at": nil, "locked_at": nil}
	if p.Winner != "" {
		m["winning_outcome_id"] = p.Winner
	}
	if p.Ended != nil {
		m["ended_at"] = p.Ended.Format(time.RFC3339Nano)
	}
	if p.Locked != nil {
		m["locked_at"] = p.Locked.Format(time.RFC3339Nano)
	}
	return m
}

// pollAuth checks the token's scope and that the channel has the feature.
func (s *Server) pollAuth(w http.ResponseWriter, r *http.Request, scopes ...string) (*grant, bool) {
	g, ok := s.helixAuth(w, r, "")
	if !ok {
		return nil, false
	}
	allowed := false
	for _, sc := range scopes {
		allowed = allowed || contains(g.scopes, sc)
	}
	if !allowed {
		writeErr(w, 401, "The user access token is missing the "+scopes[len(scopes)-1]+" scope.")
		return nil, false
	}
	s.rewardsInit()
	if s.rw.notAffiliate[g.userID] {
		writeErr(w, 403, "The broadcaster is not a partner or affiliate.")
		return nil, false
	}
	return g, true
}

func (s *Server) latestPollLocked(b string) *mockPoll {
	for i := len(s.pl.polls) - 1; i >= 0; i-- {
		if s.pl.polls[i].broadcaster == b {
			return s.pl.polls[i]
		}
	}
	return nil
}

func (s *Server) latestPredictionLocked(b string) *mockPrediction {
	for i := len(s.pl.predictions) - 1; i >= 0; i-- {
		if s.pl.predictions[i].broadcaster == b {
			return s.pl.predictions[i]
		}
	}
	return nil
}

// notifyLocked sends an event while s.mu is held.
func (s *Server) notifyLocked(b, typ string, ev map[string]any) {
	s.mu.Unlock()
	defer s.mu.Lock()
	s.Notify(s.loginOf(b), typ, ev, "")
}

func (s *Server) loginOf(userID string) string {
	return s.logins[userID]
}

func (p *mockPoll) event(end bool) map[string]any {
	choices := []map[string]any{}
	for _, c := range p.Choices {
		choices = append(choices, map[string]any{"id": c["id"], "title": c["title"], "bits_votes": 0,
			"channel_points_votes": c["channel_points_votes"], "votes": c["votes"]})
	}
	e := map[string]any{"id": p.ID, "title": p.Title, "choices": choices, "bits_voting": map[string]any{"is_enabled": false, "amount_per_vote": 0},
		"channel_points_voting": map[string]any{"is_enabled": p.PerVote > 0, "amount_per_vote": p.PerVote},
		"started_at":            p.Started.Format(time.RFC3339Nano)}
	if end {
		e["status"] = strings.ToLower(p.Status)
		e["ended_at"] = p.Ended.Format(time.RFC3339Nano)
	} else {
		e["ends_at"] = p.Started.Add(time.Duration(p.Duration) * time.Second).Format(time.RFC3339Nano)
	}
	return e
}

func (p *mockPrediction) event(phase string) map[string]any {
	outcomes := []map[string]any{}
	for _, o := range p.Outcomes {
		x := map[string]any{"id": o["id"], "title": o["title"], "color": strings.ToLower(o["color"].(string)),
			"users": o["users"], "channel_points": o["channel_points"], "top_predictors": o["top"]}
		outcomes = append(outcomes, x)
	}
	e := map[string]any{"id": p.ID, "title": p.Title, "outcomes": outcomes, "started_at": p.Created.Format(time.RFC3339Nano)}
	switch phase {
	case "end":
		e["status"], e["winning_outcome_id"] = strings.ToLower(p.Status), p.Winner
		e["ended_at"] = p.Ended.Format(time.RFC3339Nano)
	case "lock":
		e["locked_at"] = p.Locked.Format(time.RFC3339Nano)
	default:
		e["locks_at"] = p.Created.Add(time.Duration(p.Window) * time.Second).Format(time.RFC3339Nano)
	}
	return e
}

func (s *Server) polls(w http.ResponseWriter, r *http.Request) {
	g, ok := s.pollAuth(w, r, "channel:read:polls", "channel:manage:polls")
	if !ok {
		return
	}
	switch r.Method {
	case "GET":
		if r.URL.Query().Get("broadcaster_id") != g.userID {
			writeErr(w, 400, "The broadcaster_id query parameter is required.")
			return
		}
		data := []any{}
		if p := s.latestPollLocked(g.userID); p != nil {
			data = append(data, p.json())
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	case "POST":
		if !contains(g.scopes, "channel:manage:polls") {
			writeErr(w, 401, "The user access token is missing the channel:manage:polls scope.")
			return
		}
		var in struct {
			BroadcasterID string `json:"broadcaster_id"`
			Title         string `json:"title"`
			Choices       []struct {
				Title string `json:"title"`
			} `json:"choices"`
			Duration int  `json:"duration"`
			CPOn     bool `json:"channel_points_voting_enabled"`
			PerVote  int  `json:"channel_points_per_vote"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.BroadcasterID != g.userID {
			writeErr(w, 400, "The broadcaster_id field is required.")
			return
		}
		switch {
		case in.Title == "" || len([]rune(in.Title)) > 60:
			writeErr(w, 400, "The poll's title is too long.")
			return
		case len(in.Choices) < 2 || len(in.Choices) > 5:
			writeErr(w, 400, "The number of choices in the poll may not be less than 2 or greater that 5.")
			return
		case in.Duration < 15 || in.Duration > 1800:
			writeErr(w, 400, "The value in duration is outside the allowed range of values.")
			return
		}
		if cur := s.latestPollLocked(g.userID); cur != nil && cur.Status == "ACTIVE" {
			writeErr(w, 400, "The broadcaster already has a poll that's running; you may not create another poll until the current poll completes.")
			return
		}
		p := &mockPoll{ID: random(12), Title: in.Title, Status: "ACTIVE", Duration: in.Duration, Started: s.now().UTC(), broadcaster: g.userID}
		if in.CPOn {
			p.PerVote = in.PerVote
		}
		for _, c := range in.Choices {
			if len([]rune(c.Title)) > 25 {
				writeErr(w, 400, "The choice's title is too long.")
				return
			}
			p.Choices = append(p.Choices, map[string]any{"id": random(8), "title": c.Title, "votes": 0, "channel_points_votes": 0, "bits_votes": 0})
		}
		s.pl.polls = append(s.pl.polls, p)
		s.notifyLocked(g.userID, "channel.poll.begin", p.event(false))
		writeJSON(w, map[string]any{"data": []any{p.json()}})
	case "PATCH":
		var in struct {
			BroadcasterID, ID, Status string
		}
		var raw map[string]string
		if json.NewDecoder(r.Body).Decode(&raw) != nil {
			writeErr(w, 400, "The id field is required.")
			return
		}
		in.BroadcasterID, in.ID, in.Status = raw["broadcaster_id"], raw["id"], raw["status"]
		p := s.latestPollLocked(g.userID)
		if in.BroadcasterID != g.userID || p == nil || p.ID != in.ID {
			writeErr(w, 400, "The id field is required.")
			return
		}
		if in.Status != "TERMINATED" && in.Status != "ARCHIVED" {
			writeErr(w, 400, "The value in the status field is not valid.")
			return
		}
		if p.Status != "ACTIVE" {
			writeErr(w, 400, "The poll must be active to terminate or archive it.")
			return
		}
		now := s.now().UTC()
		p.Status, p.Ended = in.Status, &now
		s.notifyLocked(g.userID, "channel.poll.end", p.event(true))
		writeJSON(w, map[string]any{"data": []any{p.json()}})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) predictions(w http.ResponseWriter, r *http.Request) {
	g, ok := s.pollAuth(w, r, "channel:read:predictions", "channel:manage:predictions")
	if !ok {
		return
	}
	switch r.Method {
	case "GET":
		data := []any{}
		if p := s.latestPredictionLocked(g.userID); p != nil {
			data = append(data, p.json())
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	case "POST":
		if !contains(g.scopes, "channel:manage:predictions") {
			writeErr(w, 401, "The user access token must include the channel:manage:predictions scope.")
			return
		}
		var in struct {
			BroadcasterID string `json:"broadcaster_id"`
			Title         string `json:"title"`
			Outcomes      []struct {
				Title string `json:"title"`
			} `json:"outcomes"`
			Window int `json:"prediction_window"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.BroadcasterID != g.userID {
			writeErr(w, 400, "The broadcaster_id field is required.")
			return
		}
		switch {
		case in.Title == "" || len([]rune(in.Title)) > 45:
			writeErr(w, 400, "The prediction's title is too long.")
			return
		case len(in.Outcomes) < 2 || len(in.Outcomes) > 10:
			writeErr(w, 400, "There must be 2 outcomes in the prediction.")
			return
		case in.Window < 30 || in.Window > 1800:
			writeErr(w, 400, "The value in prediction_window is outside the allowed range of values.")
			return
		}
		if cur := s.latestPredictionLocked(g.userID); cur != nil && (cur.Status == "ACTIVE" || cur.Status == "LOCKED") {
			writeErr(w, 400, "The broadcaster already has a prediction that's running; you may not create another prediction until the current prediction is resolved or canceled.")
			return
		}
		p := &mockPrediction{ID: random(12), Title: in.Title, Status: "ACTIVE", Window: in.Window, Created: s.now().UTC(), broadcaster: g.userID}
		for i, o := range in.Outcomes {
			color := "BLUE"
			if len(in.Outcomes) == 2 && i == 1 {
				color = "PINK"
			}
			p.Outcomes = append(p.Outcomes, map[string]any{"id": random(8), "title": o.Title, "color": color, "users": 0,
				"channel_points": 0, "top": []any{}})
		}
		s.pl.predictions = append(s.pl.predictions, p)
		s.notifyLocked(g.userID, "channel.prediction.begin", p.event("begin"))
		writeJSON(w, map[string]any{"data": []any{p.json()}})
	case "PATCH":
		var raw map[string]string
		if json.NewDecoder(r.Body).Decode(&raw) != nil {
			writeErr(w, 400, "The id field is required.")
			return
		}
		p := s.latestPredictionLocked(g.userID)
		if raw["broadcaster_id"] != g.userID || p == nil || p.ID != raw["id"] {
			writeErr(w, 404, "The prediction in the id field was not found.")
			return
		}
		now := s.now().UTC()
		switch raw["status"] {
		case "LOCKED":
			if p.Status != "ACTIVE" {
				writeErr(w, 400, "To update the prediction's status to LOCKED, its current status must be ACTIVE.")
				return
			}
			p.Status, p.Locked = "LOCKED", &now
			s.notifyLocked(g.userID, "channel.prediction.lock", p.event("lock"))
		case "RESOLVED", "CANCELED":
			if p.Status != "ACTIVE" && p.Status != "LOCKED" {
				writeErr(w, 400, "To update the prediction's status to RESOLVED or CANCELED, its current status must be ACTIVE or LOCKED.")
				return
			}
			if raw["status"] == "RESOLVED" {
				found := false
				for _, o := range p.Outcomes {
					found = found || o["id"] == raw["winning_outcome_id"]
				}
				if !found {
					writeErr(w, 404, "The outcome in the winning_outcome_id field was not found.")
					return
				}
				p.Winner = raw["winning_outcome_id"]
			}
			p.Status, p.Ended = raw["status"], &now
			s.notifyLocked(g.userID, "channel.prediction.end", p.event("end"))
		default:
			writeErr(w, 400, "The value in the status field is not valid.")
			return
		}
		writeJSON(w, map[string]any{"data": []any{p.json()}})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// Vote adds votes to the running poll's choice (0-based), as viewers would,
// and sends the progress event.
func (s *Server) Vote(channelLogin string, choice, votes int) {
	s.mu.Lock()
	b := s.userIDLocked(channelLogin)
	p := s.latestPollLocked(b)
	if p == nil || p.Status != "ACTIVE" || choice >= len(p.Choices) {
		s.mu.Unlock()
		return
	}
	p.Choices[choice]["votes"] = p.Choices[choice]["votes"].(int) + votes
	ev := p.event(false)
	s.mu.Unlock()
	s.Notify(channelLogin, "channel.poll.progress", ev, "")
}

// Bet has a viewer bet points on the running prediction's outcome
// (0-based), and sends the progress event.
func (s *Server) Bet(channelLogin, viewer string, outcome, points int) {
	s.mu.Lock()
	b := s.userIDLocked(channelLogin)
	p := s.latestPredictionLocked(b)
	if p == nil || p.Status != "ACTIVE" || outcome >= len(p.Outcomes) {
		s.mu.Unlock()
		return
	}
	o := p.Outcomes[outcome]
	o["users"] = o["users"].(int) + 1
	o["channel_points"] = o["channel_points"].(int) + points
	l := strings.ToLower(viewer)
	o["top"] = append(o["top"].([]any), map[string]any{"user_id": s.userIDLocked(l), "user_login": l, "user_name": viewer,
		"channel_points_used": points, "channel_points_won": nil})
	ev := p.event("progress")
	s.mu.Unlock()
	s.Notify(channelLogin, "channel.prediction.progress", ev, "")
}

// PollStatus and PredictionStatus are the newest one's status, for tests.
func (s *Server) PollStatus(channelLogin string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.latestPollLocked(s.userIDLocked(channelLogin)); p != nil {
		return p.Status
	}
	return ""
}

func (s *Server) PredictionStatus(channelLogin string) (status, winnerTitle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.latestPredictionLocked(s.userIDLocked(channelLogin))
	if p == nil {
		return "", ""
	}
	for _, o := range p.Outcomes {
		if o["id"] == p.Winner {
			winnerTitle = o["title"].(string)
		}
	}
	return p.Status, winnerTitle
}

// CompletePoll ends the running poll on schedule (COMPLETED), as its time
// running out would, and sends the end event.
func (s *Server) CompletePoll(channelLogin string) {
	s.mu.Lock()
	p := s.latestPollLocked(s.userIDLocked(channelLogin))
	if p == nil || p.Status != "ACTIVE" {
		s.mu.Unlock()
		return
	}
	now := s.now().UTC()
	p.Status, p.Ended = "COMPLETED", &now
	ev := p.event(true)
	s.mu.Unlock()
	s.Notify(channelLogin, "channel.poll.end", ev, "")
}
