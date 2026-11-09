package twitch

// Poll and prediction events (docs/adr/0023-polls-and-predictions.md), as
// Twitch's EventSub reference shows them (v1), in StreamGremlin's words.

type pollEvent struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Choices []struct {
		ID                 string `json:"id"`
		Title              string `json:"title"`
		Votes              int    `json:"votes"`
		ChannelPointsVotes int    `json:"channel_points_votes"`
	} `json:"choices"`
	ChannelPointsVoting struct {
		Enabled bool `json:"is_enabled"`
		Amount  int  `json:"amount_per_vote"`
	} `json:"channel_points_voting"`
	Status    string `json:"status"` // at the end: completed, archived or terminated
	StartedAt string `json:"started_at"`
	EndsAt    string `json:"ends_at"`
	EndedAt   string `json:"ended_at"`
}

// payload: the poll, with its leader: the choice with the most votes, or
// "" with tied set when several share the most.
func (e pollEvent) payload() map[string]any {
	choices := []map[string]any{}
	best, leaders, total := -1, []string{}, 0
	for _, c := range e.Choices {
		choices = append(choices, map[string]any{"id": c.ID, "title": c.Title, "votes": c.Votes, "channel_points_votes": c.ChannelPointsVotes})
		total += c.Votes
		switch {
		case c.Votes > best:
			best, leaders = c.Votes, []string{c.Title}
		case c.Votes == best:
			leaders = append(leaders, c.Title)
		}
	}
	p := map[string]any{"poll_id": e.ID, "title": e.Title, "choices": choices, "votes": total,
		"channel_points_voting": e.ChannelPointsVoting.Enabled, "channel_points_per_vote": e.ChannelPointsVoting.Amount,
		"started_at": e.StartedAt}
	p["leader"], p["tied"] = "", len(leaders) > 1
	if len(leaders) == 1 && total > 0 {
		p["leader"] = leaders[0]
	}
	if e.Status != "" {
		p["status"], p["ended_at"] = e.Status, e.EndedAt
	} else {
		p["ends_at"] = e.EndsAt
	}
	return p
}

type predictionEvent struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Outcomes []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Color         string `json:"color"`
		Users         int    `json:"users"`
		ChannelPoints int    `json:"channel_points"`
		Top           []struct {
			UserID    string `json:"user_id"`
			UserLogin string `json:"user_login"`
			UserName  string `json:"user_name"`
			Won       *int   `json:"channel_points_won"`
			Used      int    `json:"channel_points_used"`
		} `json:"top_predictors"`
	} `json:"outcomes"`
	WinningOutcomeID string `json:"winning_outcome_id"`
	Status           string `json:"status"` // at the end: resolved or canceled
	StartedAt        string `json:"started_at"`
	LocksAt          string `json:"locks_at"`
	LockedAt         string `json:"locked_at"`
	EndedAt          string `json:"ended_at"`
}

// payload: the prediction, with the winning outcome's title at the end.
func (e predictionEvent) payload() map[string]any {
	outcomes := []map[string]any{}
	winner := ""
	for _, o := range e.Outcomes {
		top := []map[string]any{}
		for _, t := range o.Top {
			x := map[string]any{"user_id": t.UserID, "login": t.UserLogin, "name": t.UserName, "points_used": t.Used}
			if t.Won != nil {
				x["points_won"] = *t.Won
			}
			top = append(top, x)
		}
		outcomes = append(outcomes, map[string]any{"id": o.ID, "title": o.Title, "color": o.Color, "users": o.Users,
			"channel_points": o.ChannelPoints, "top_predictors": top})
		if o.ID != "" && o.ID == e.WinningOutcomeID {
			winner = o.Title
		}
	}
	p := map[string]any{"prediction_id": e.ID, "title": e.Title, "outcomes": outcomes, "started_at": e.StartedAt}
	switch {
	case e.Status != "":
		p["status"], p["ended_at"], p["winning_outcome_id"], p["winner"] = e.Status, e.EndedAt, e.WinningOutcomeID, winner
	case e.LockedAt != "":
		p["locked_at"] = e.LockedAt
	default:
		p["locks_at"] = e.LocksAt
	}
	return p
}
