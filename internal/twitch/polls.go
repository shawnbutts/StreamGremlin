package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Polls and predictions (docs/adr/0023-polls-and-predictions.md): started
// and ended as the streamer. Twitch allows one of each at a time, and only
// on Affiliate or Partner channels. Limits from Twitch's API reference
// (read 2026-11-09).
const (
	PollScope       = "channel:manage:polls"
	PredictionScope = "channel:manage:predictions"

	MaxPollTitle          = 60
	MinPollChoices        = 2
	MaxPollChoices        = 5
	MaxChoiceTitle        = 25
	MinPollSeconds        = 15
	MaxPollSeconds        = 1800
	MaxPointsPerVote      = 1_000_000
	MaxPredictionTitle    = 45
	MinOutcomes           = 2
	MaxOutcomes           = 10
	MaxOutcomeTitle       = 25
	MinPredictionSeconds  = 30
	MaxPredictionSeconds  = 1800
	predictionResolveTime = 24 * time.Hour
)

// Errors about polls and predictions.
var (
	ErrPollRunning        = errors.New("twitch: a poll is already running; end it first")
	ErrNoPoll             = errors.New("twitch: no poll is running")
	ErrPredictionRunning  = errors.New("twitch: a prediction is already running; end it first")
	ErrNoPrediction       = errors.New("twitch: no prediction is running or waiting for its result")
	ErrNeedsPermission    = errors.New("twitch: sign in to Twitch again and allow it")
	errPollsSignIn        = signInError{"twitch: sign in with your Twitch account to run polls and predictions"}
	errNotPartnerFriendly = "Polls and predictions need an Affiliate or Partner channel; Twitch says yours isn't one yet"
)

// PollChoice is one choice and its votes.
type PollChoice struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Votes              int    `json:"votes"`
	ChannelPointsVotes int    `json:"channel_points_votes"`
}

// Poll is a poll, running or ended.
type Poll struct {
	ID                   string       `json:"id"`
	Title                string       `json:"title"`
	Choices              []PollChoice `json:"choices"`
	Status               string       `json:"status"` // active, completed, terminated, archived, moderated, invalid
	Seconds              int          `json:"seconds"`
	ChannelPointsPerVote int          `json:"channel_points_per_vote"` // 0: off
	StartedAt            time.Time    `json:"started_at"`
	EndedAt              *time.Time   `json:"ended_at,omitempty"`
}

// NewPoll is a poll to start.
type NewPoll struct {
	Title                string   `json:"title"`
	Choices              []string `json:"choices"`
	Seconds              int      `json:"seconds"`
	ChannelPointsPerVote int      `json:"channel_points_per_vote"` // 0: one free vote each
}

func invalidf(f string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...))
}

func cleanList(list []string) []string {
	out := []string{}
	for _, x := range list {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Check reports what's wrong, in words, with Twitch's documented limits.
func (n NewPoll) Check() error {
	title, choices := strings.TrimSpace(n.Title), cleanList(n.Choices)
	switch {
	case title == "" || utf8.RuneCountInString(title) > MaxPollTitle:
		return invalidf("a poll's question is 1 to %d characters", MaxPollTitle)
	case len(choices) < MinPollChoices || len(choices) > MaxPollChoices:
		return invalidf("a poll has %d to %d choices", MinPollChoices, MaxPollChoices)
	case n.Seconds < MinPollSeconds || n.Seconds > MaxPollSeconds:
		return invalidf("a poll runs %d seconds to 30 minutes", MinPollSeconds)
	case n.ChannelPointsPerVote < 0 || n.ChannelPointsPerVote > MaxPointsPerVote:
		return invalidf("extra votes cost 1 to %d channel points, or 0 for none", MaxPointsPerVote)
	}
	for _, c := range choices {
		if utf8.RuneCountInString(c) > MaxChoiceTitle {
			return invalidf("each choice is up to %d characters: %q is longer", MaxChoiceTitle, c)
		}
	}
	return nil
}

// Outcome is one outcome of a prediction.
type Outcome struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Color         string `json:"color"`
	Users         int    `json:"users"`
	ChannelPoints int    `json:"channel_points"`
}

// Prediction is a prediction, running, locked or ended.
type Prediction struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Outcomes         []Outcome  `json:"outcomes"`
	Status           string     `json:"status"` // active, locked, resolved, canceled
	Seconds          int        `json:"seconds"`
	WinningOutcomeID string     `json:"winning_outcome_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	LockedAt         *time.Time `json:"locked_at,omitempty"`
}

// NewPrediction is a prediction to start.
type NewPrediction struct {
	Title    string   `json:"title"`
	Outcomes []string `json:"outcomes"`
	Seconds  int      `json:"seconds"`
}

// Check reports what's wrong, in words, with Twitch's documented limits.
func (n NewPrediction) Check() error {
	title, outcomes := strings.TrimSpace(n.Title), cleanList(n.Outcomes)
	switch {
	case title == "" || utf8.RuneCountInString(title) > MaxPredictionTitle:
		return invalidf("a prediction's question is 1 to %d characters", MaxPredictionTitle)
	case len(outcomes) < MinOutcomes || len(outcomes) > MaxOutcomes:
		return invalidf("a prediction has %d to %d outcomes", MinOutcomes, MaxOutcomes)
	case n.Seconds < MinPredictionSeconds || n.Seconds > MaxPredictionSeconds:
		return invalidf("predictions are open %d seconds to 30 minutes", MinPredictionSeconds)
	}
	for _, o := range outcomes {
		if utf8.RuneCountInString(o) > MaxOutcomeTitle {
			return invalidf("each outcome is up to %d characters: %q is longer", MaxOutcomeTitle, o)
		}
	}
	return nil
}

// errStreamerSignIn is ErrNotSignedIn for calls made as the streamer.
var errStreamerSignIn = signInError{"twitch: sign in with your Twitch account first"}

// streamerRaw calls the API as the streamer, with scope, and returns
// Twitch's answer as it is (a *helixError for a 4xx).
func (s *Service) streamerRaw(ctx context.Context, scope, method, path string, q url.Values, body, out any) error {
	st := s.streamer.Status()
	if st.UserID == "" || (st.State != StateConnected && st.State != StateUnknown) {
		return errStreamerSignIn
	}
	if scope != "" && !slices.Contains(st.Scopes, scope) {
		return fmt.Errorf("%w (%s)", ErrNeedsPermission, scope)
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return err
	}
	clientID, _ := s.ClientID(ctx)
	if clientID == "" {
		return ErrNoClientID
	}
	if q == nil {
		q = url.Values{}
	}
	if m, ok := body.(map[string]any); ok && method != "GET" {
		m["broadcaster_id"] = st.UserID
	} else {
		q.Set("broadcaster_id", st.UserID)
	}
	return s.helix.call(ctx, method, path+"?"+q.Encode(), clientID, tok.Reveal(), body, out)
}

// streamerCall is streamerRaw with Twitch's refusals put in words, for
// polls and predictions.
func (s *Service) streamerCall(ctx context.Context, scope, method, path string, q url.Values, body, out any) error {
	err := s.streamerRaw(ctx, scope, method, path, q, body, out)
	if errors.Is(err, errStreamerSignIn) {
		return errPollsSignIn
	}
	var he *helixError
	if errors.As(err, &he) {
		msg := strings.ToLower(he.Message)
		switch {
		case he.Status == 403 || strings.Contains(msg, "partner") || strings.Contains(msg, "affiliate"):
			return fmt.Errorf("%w: %s", ErrNoChannelPoints, errNotPartnerFriendly)
		case he.Status == 400 && strings.Contains(msg, "already has a poll"):
			return ErrPollRunning
		case he.Status == 400 && strings.Contains(msg, "already has a prediction"):
			return ErrPredictionRunning
		case he.Status == 400:
			return fmt.Errorf("%w: Twitch refused it: %s", ErrInvalid, he.Message)
		}
	}
	return err
}

type helixPoll struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Choices []struct {
		ID                 string `json:"id"`
		Title              string `json:"title"`
		Votes              int    `json:"votes"`
		ChannelPointsVotes int    `json:"channel_points_votes"`
	} `json:"choices"`
	ChannelPointsEnabled bool       `json:"channel_points_voting_enabled"`
	ChannelPointsPerVote int        `json:"channel_points_per_vote"`
	Status               string     `json:"status"`
	Duration             int        `json:"duration"`
	StartedAt            time.Time  `json:"started_at"`
	EndedAt              *time.Time `json:"ended_at"`
}

func (h helixPoll) poll() Poll {
	p := Poll{ID: h.ID, Title: h.Title, Status: strings.ToLower(h.Status), Seconds: h.Duration, StartedAt: h.StartedAt, EndedAt: h.EndedAt}
	if h.ChannelPointsEnabled {
		p.ChannelPointsPerVote = h.ChannelPointsPerVote
	}
	for _, c := range h.Choices {
		p.Choices = append(p.Choices, PollChoice{ID: c.ID, Title: c.Title, Votes: c.Votes, ChannelPointsVotes: c.ChannelPointsVotes})
	}
	return p
}

// LatestPoll returns the newest poll (running or ended), or nil.
func (s *Service) LatestPoll(ctx context.Context) (*Poll, error) {
	var out struct {
		Data []helixPoll `json:"data"`
	}
	if err := s.streamerCall(ctx, PollScope, "GET", "/polls", url.Values{"first": {"1"}}, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	p := out.Data[0].poll()
	return &p, nil
}

// StartPoll starts a poll. Asking again after an unclear answer is
// harmless: the same poll running is the one asked for (created false).
func (s *Service) StartPoll(ctx context.Context, n NewPoll) (Poll, bool, error) {
	if err := n.Check(); err != nil {
		return Poll{}, false, err
	}
	title, choices := strings.TrimSpace(n.Title), cleanList(n.Choices)
	body := map[string]any{"title": title, "duration": n.Seconds, "channel_points_voting_enabled": n.ChannelPointsPerVote > 0}
	list := []map[string]string{}
	for _, c := range choices {
		list = append(list, map[string]string{"title": c})
	}
	body["choices"] = list
	if n.ChannelPointsPerVote > 0 {
		body["channel_points_per_vote"] = n.ChannelPointsPerVote
	}
	var out struct {
		Data []helixPoll `json:"data"`
	}
	err := s.streamerCall(ctx, PollScope, "POST", "/polls", nil, body, &out)
	if errors.Is(err, ErrPollRunning) {
		if cur, gerr := s.LatestPoll(ctx); gerr == nil && cur != nil && cur.Status == "active" && samePoll(*cur, title, choices) {
			return *cur, false, nil
		}
	}
	if err != nil {
		return Poll{}, false, err
	}
	if len(out.Data) == 0 {
		return Poll{}, false, fmt.Errorf("%w: Twitch didn't return the poll", errUnreachable)
	}
	s.log.Info("twitch poll started", "choices", len(choices), "seconds", n.Seconds)
	return out.Data[0].poll(), true, nil
}

func samePoll(p Poll, title string, choices []string) bool {
	if !strings.EqualFold(p.Title, title) || len(p.Choices) != len(choices) {
		return false
	}
	for i, c := range p.Choices {
		if !strings.EqualFold(c.Title, choices[i]) {
			return false
		}
	}
	return true
}

// EndPoll ends the running poll: its results stay in chat for a while, or
// with archive it's removed at once. With no poll running it says so.
func (s *Service) EndPoll(ctx context.Context, archive bool) (Poll, error) {
	cur, err := s.LatestPoll(ctx)
	if err != nil {
		return Poll{}, err
	}
	if cur == nil || cur.Status != "active" {
		return Poll{}, ErrNoPoll
	}
	status := "TERMINATED"
	if archive {
		status = "ARCHIVED"
	}
	var out struct {
		Data []helixPoll `json:"data"`
	}
	err = s.streamerCall(ctx, PollScope, "PATCH", "/polls", nil, map[string]any{"id": cur.ID, "status": status}, &out)
	if errors.Is(err, ErrInvalid) && strings.Contains(strings.ToLower(err.Error()), "must be active") {
		return Poll{}, ErrNoPoll // it ended meanwhile
	}
	if err != nil {
		return Poll{}, err
	}
	if len(out.Data) == 0 {
		return Poll{}, fmt.Errorf("%w: Twitch didn't return the poll", errUnreachable)
	}
	return out.Data[0].poll(), nil
}

type helixPrediction struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Outcomes []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Color         string `json:"color"`
		Users         int    `json:"users"`
		ChannelPoints int    `json:"channel_points"`
	} `json:"outcomes"`
	WinningOutcomeID string     `json:"winning_outcome_id"`
	Status           string     `json:"status"`
	Window           int        `json:"prediction_window"`
	CreatedAt        time.Time  `json:"created_at"`
	EndedAt          *time.Time `json:"ended_at"`
	LockedAt         *time.Time `json:"locked_at"`
}

func (h helixPrediction) prediction() Prediction {
	p := Prediction{ID: h.ID, Title: h.Title, Status: strings.ToLower(h.Status), Seconds: h.Window, WinningOutcomeID: h.WinningOutcomeID,
		CreatedAt: h.CreatedAt, EndedAt: h.EndedAt, LockedAt: h.LockedAt}
	for _, o := range h.Outcomes {
		p.Outcomes = append(p.Outcomes, Outcome{ID: o.ID, Title: o.Title, Color: strings.ToLower(o.Color), Users: o.Users, ChannelPoints: o.ChannelPoints})
	}
	return p
}

// LatestPrediction returns the newest prediction, or nil.
func (s *Service) LatestPrediction(ctx context.Context) (*Prediction, error) {
	var out struct {
		Data []helixPrediction `json:"data"`
	}
	if err := s.streamerCall(ctx, PredictionScope, "GET", "/predictions", url.Values{"first": {"1"}}, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	p := out.Data[0].prediction()
	return &p, nil
}

// StartPrediction starts a prediction. Asking again after an unclear answer
// is harmless: the same one running is the one asked for (created false).
func (s *Service) StartPrediction(ctx context.Context, n NewPrediction) (Prediction, bool, error) {
	if err := n.Check(); err != nil {
		return Prediction{}, false, err
	}
	title, outcomes := strings.TrimSpace(n.Title), cleanList(n.Outcomes)
	list := []map[string]string{}
	for _, o := range outcomes {
		list = append(list, map[string]string{"title": o})
	}
	var out struct {
		Data []helixPrediction `json:"data"`
	}
	err := s.streamerCall(ctx, PredictionScope, "POST", "/predictions", nil,
		map[string]any{"title": title, "outcomes": list, "prediction_window": n.Seconds}, &out)
	if errors.Is(err, ErrPredictionRunning) {
		if cur, gerr := s.LatestPrediction(ctx); gerr == nil && cur != nil && cur.Status == "active" && samePrediction(*cur, title, outcomes) {
			return *cur, false, nil
		}
	}
	if err != nil {
		return Prediction{}, false, err
	}
	if len(out.Data) == 0 {
		return Prediction{}, false, fmt.Errorf("%w: Twitch didn't return the prediction", errUnreachable)
	}
	s.log.Info("twitch prediction started", "outcomes", len(outcomes), "seconds", n.Seconds)
	return out.Data[0].prediction(), true, nil
}

func samePrediction(p Prediction, title string, outcomes []string) bool {
	if !strings.EqualFold(p.Title, title) || len(p.Outcomes) != len(outcomes) {
		return false
	}
	for i, o := range p.Outcomes {
		if !strings.EqualFold(o.Title, outcomes[i]) {
			return false
		}
	}
	return true
}

// Prediction endings.
const (
	PredictionLock    = "lock"
	PredictionResolve = "resolve"
	PredictionCancel  = "cancel"
)

// EndPrediction locks the newest prediction, resolves it with a winner
// (an outcome's title, any case, or its number from 1), or cancels it and
// refunds everyone. Repeating the same ending is harmless (repeated);
// another one after it ended says so.
func (s *Service) EndPrediction(ctx context.Context, how, winner string) (Prediction, bool, error) {
	cur, err := s.LatestPrediction(ctx)
	if err != nil {
		return Prediction{}, false, err
	}
	if cur == nil {
		return Prediction{}, false, ErrNoPrediction
	}
	want := map[string]string{PredictionLock: "locked", PredictionResolve: "resolved", PredictionCancel: "canceled"}[how]
	if want == "" {
		return Prediction{}, false, invalidf("lock, resolve or cancel")
	}
	body := map[string]any{"id": cur.ID, "status": strings.ToUpper(want)}
	var winID string
	if how == PredictionResolve {
		winID = outcomeID(*cur, winner)
		if winID == "" {
			return Prediction{}, false, invalidf("the winner is one of the outcomes, by its words or its number: %q isn't", winner)
		}
		body["winning_outcome_id"] = winID
	}
	switch {
	case cur.Status == want && (how != PredictionResolve || cur.WinningOutcomeID == winID):
		return *cur, true, nil // already so
	case cur.Status == "resolved" || cur.Status == "canceled":
		return *cur, false, fmt.Errorf("%w: the newest prediction already ended (%s)", ErrNoPrediction, cur.Status)
	case how == PredictionLock && cur.Status != "active":
		return *cur, false, fmt.Errorf("%w: it's %s, so it can't be locked", ErrNoPrediction, cur.Status)
	}
	var out struct {
		Data []helixPrediction `json:"data"`
	}
	if err := s.streamerCall(ctx, PredictionScope, "PATCH", "/predictions", nil, body, &out); err != nil {
		return Prediction{}, false, err
	}
	if len(out.Data) == 0 {
		return Prediction{}, false, fmt.Errorf("%w: Twitch didn't return the prediction", errUnreachable)
	}
	s.log.Info("twitch prediction ended", "how", how)
	return out.Data[0].prediction(), false, nil
}

// outcomeID finds an outcome by its title (any case) or number (1-based).
func outcomeID(p Prediction, winner string) string {
	w := strings.TrimSpace(winner)
	if n, err := strconv.Atoi(strings.TrimPrefix(w, "#")); err == nil && n >= 1 && n <= len(p.Outcomes) {
		return p.Outcomes[n-1].ID
	}
	for _, o := range p.Outcomes {
		if strings.EqualFold(o.Title, w) {
			return o.ID
		}
	}
	return ""
}
