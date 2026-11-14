package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// YouTube polls (M6 slice 7, D-102). A poll is a chat message of type
// pollEvent: liveChatMessages.insert starts one (20 units) and
// liveChatMessages.transition closes it (20 units). The chat stream says
// one was made (a pollEvent message) and, in each answer while it runs,
// the active poll with its tallies (active_poll_item); tallies are only
// given to the chat's owner, so StreamGremlin reads as your channel.
// Polls have no length: one runs until it's closed. Events use Twitch's
// poll types and payload, from source youtube.

// Poll limits. Google states 2 to 4 options; it gives no length for the
// question or an option, so these are StreamGremlin's.
const (
	MinPollOptions   = 2
	MaxPollOptions   = 4
	MaxQuestionRunes = 100
	MaxOptionRunes   = 35
	CostPollClose    = 20
)

// Poll event types (Twitch's).
const (
	TypePollStarted = "chat.poll.started"
	TypePollUpdated = "chat.poll.updated"
	TypePollEnded   = "chat.poll.ended"
)

var (
	// ErrPollRunning: YouTube runs one poll at a time.
	ErrPollRunning = errors.New("youtube: a poll is already running in your YouTube chat; end it first")
	// ErrNoPoll: there's no poll running to end.
	ErrNoPoll = errors.New("youtube: no poll is running in your YouTube chat")
)

// PollChoice is one option and its votes.
type PollChoice struct {
	Title string `json:"title"`
	Votes int64  `json:"votes"`
}

// PollInfo is the poll running in, or last closed in, the chat being read.
type PollInfo struct {
	ID       string       `json:"id"`
	Question string       `json:"question"`
	Choices  []PollChoice `json:"choices"`
	// Status is active or closed.
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at,omitzero"`
}

func (p *PollInfo) clone() *PollInfo {
	if p == nil {
		return nil
	}
	c := *p
	c.Choices = append([]PollChoice(nil), p.Choices...)
	return &c
}

// Poll returns the chat's poll, or nil when none has been seen.
func (s *Service) Poll() *PollInfo {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.poll.clone()
}

func pollFrom(m livechat.Message) PollInfo {
	p := PollInfo{ID: m.ID, Choices: []PollChoice{}, Status: "active"}
	if d := m.Snippet.Poll; d != nil {
		p.Question = d.Question
		for _, o := range d.Options {
			p.Choices = append(p.Choices, PollChoice{Title: o.Text, Votes: o.Tally})
		}
		if d.Status == livechat.PollClosed {
			p.Status = "closed"
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, m.Snippet.PublishedAt); err == nil {
		p.StartedAt = t
	}
	return p
}

func sameTallies(a, b []PollChoice) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// trackPoll follows one stream answer: a poll message made in it, and the
// active poll, if any. A poll that was active and isn't any more ended.
func (s *Service) trackPoll(ctx context.Context, b Broadcast, channel string, items []livechat.Message, active *livechat.Message, backlog bool) {
	for _, m := range items {
		if m.Snippet.Type == livechat.TypePoll && m.Snippet.Poll != nil && m.ID != "" {
			s.notePoll(ctx, b, channel, pollFrom(m), backlog, true)
		}
	}
	if active != nil && active.Snippet.Poll != nil && active.ID != "" {
		s.notePoll(ctx, b, channel, pollFrom(*active), backlog, true)
		return
	}
	// Only a poll the stream has shown as running is ended by its absence:
	// one just started may not be in the stream's answers yet.
	s.chatMu.Lock()
	last := s.poll.clone()
	gone := last != nil && last.Status == "active" && s.pollSeen == last.ID
	if gone {
		s.poll.Status = "closed"
	}
	s.chatMu.Unlock()
	if gone {
		last.Status = "closed"
		s.changed()
		s.storePoll(ctx, b, channel, TypePollEnded, *last, backlog)
	}
}

// notePoll records what's new about a poll: started, votes, or closed.
func (s *Service) notePoll(ctx context.Context, b Broadcast, channel string, p PollInfo, backlog, inStream bool) {
	s.chatMu.Lock()
	if inStream {
		s.pollSeen = p.ID
	}
	last := s.poll.clone()
	if last != nil && last.ID == p.ID && p.StartedAt.IsZero() {
		p.StartedAt = last.StartedAt
	}
	if last != nil && last.ID == p.ID && last.Status == "closed" {
		s.chatMu.Unlock()
		return // already ended: a late answer changes nothing
	}
	s.poll = p.clone()
	s.chatMu.Unlock()

	if last != nil && last.ID != p.ID && last.Status == "active" {
		// Another poll replaced it: the old one ended.
		last.Status = "closed"
		s.storePoll(ctx, b, channel, TypePollEnded, *last, backlog)
	}
	switch {
	case last == nil || last.ID != p.ID:
		s.storePoll(ctx, b, channel, TypePollStarted, p, backlog)
		if p.Status == "closed" {
			s.storePoll(ctx, b, channel, TypePollEnded, p, backlog)
		}
	case p.Status == "closed":
		s.storePoll(ctx, b, channel, TypePollEnded, p, backlog)
	case !sameTallies(last.Choices, p.Choices):
		s.storePoll(ctx, b, channel, TypePollUpdated, p, backlog)
	default:
		return
	}
	s.changed()
}

// pollPayload is Twitch's poll payload, as far as YouTube has it.
func pollPayload(p PollInfo) map[string]any {
	choices := []map[string]any{}
	best, leaders, total := int64(-1), []string{}, int64(0)
	for i, c := range p.Choices {
		choices = append(choices, map[string]any{"id": fmt.Sprint(i + 1), "title": c.Title, "votes": c.Votes})
		total += c.Votes
		switch {
		case c.Votes > best:
			best, leaders = c.Votes, []string{c.Title}
		case c.Votes == best:
			leaders = append(leaders, c.Title)
		}
	}
	out := map[string]any{"poll_id": p.ID, "title": p.Question, "choices": choices, "votes": total, "leader": "",
		"tied": len(leaders) > 1 && total > 0}
	if len(leaders) == 1 && total > 0 {
		out["leader"] = leaders[0]
	}
	if !p.StartedAt.IsZero() {
		out["started_at"] = p.StartedAt.UTC().Format(time.RFC3339)
	}
	if p.Status == "closed" {
		out["status"] = "completed"
	}
	return out
}

func (s *Service) storePoll(ctx context.Context, b Broadcast, channel, typ string, p PollInfo, backlog bool) {
	pl := pollPayload(p)
	pl["broadcast_id"], pl["live_chat_id"] = b.ID, b.LiveChatID
	if backlog {
		pl["backlog"] = true
	}
	raw, _ := json.Marshal(pl)
	id := "poll-started:" + p.ID
	switch typ {
	case TypePollEnded:
		id = "poll-ended:" + p.ID
	case TypePollUpdated:
		id = fmt.Sprintf("poll-updated:%s:%d", p.ID, s.now().UnixNano())
	}
	env := events.Envelope{EventID: events.NewID(), Type: typ, Source: events.SourceYouTube, SourceEventID: id,
		OccurredAt: s.now(), ChannelID: channel, Payload: raw, DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	s.storeEvent(context.WithoutCancel(ctx), env)
}

// NewPoll is a poll to start.
type NewPoll struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// CheckPoll says what's wrong with a poll before anything is sent.
func CheckPoll(p NewPoll) (NewPoll, error) {
	out := NewPoll{Question: strings.TrimSpace(p.Question)}
	for _, o := range p.Options {
		if o = strings.TrimSpace(o); o != "" {
			out.Options = append(out.Options, o)
		}
	}
	switch {
	case out.Question == "" || utf8.RuneCountInString(out.Question) > MaxQuestionRunes:
		return out, fmt.Errorf("%w: a question is 1 to %d characters", ErrInvalid, MaxQuestionRunes)
	case len(out.Options) < MinPollOptions || len(out.Options) > MaxPollOptions:
		return out, fmt.Errorf("%w: a YouTube poll has %d to %d choices", ErrInvalid, MinPollOptions, MaxPollOptions)
	}
	for _, o := range out.Options {
		if utf8.RuneCountInString(o) > MaxOptionRunes {
			return out, fmt.Errorf("%w: a choice is up to %d characters", ErrInvalid, MaxOptionRunes)
		}
	}
	return out, nil
}

// pollTarget is the chat a poll goes to, as your channel.
func (s *Service) pollTarget() (Broadcast, string, error) {
	ready, channel := s.streamerReady()
	if !ready {
		return Broadcast{}, "", ErrNotSignedIn
	}
	c := s.ChatStatus()
	if c.Broadcast == nil || c.Broadcast.LiveChatID == "" {
		return Broadcast{}, "", ErrNotLive
	}
	return *c.Broadcast, channel, nil
}

func pollError(err error) error {
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.Reason {
		case "preconditionCheckFailed":
			return ErrPollRunning
		case "liveChatEnded", "liveChatDisabled", "liveChatNotFound":
			return fmt.Errorf("%w (%s)", ErrNotLive, ae.Reason)
		case "liveChatMessageNotFound":
			return ErrNoPoll
		}
	}
	return err
}

// StartPoll starts a poll in the chat being read, as your channel (20
// units). It's never retried.
func (s *Service) StartPoll(ctx context.Context, np NewPoll) (PollInfo, error) {
	np, err := CheckPoll(np)
	if err != nil {
		return PollInfo{}, err
	}
	b, channel, err := s.pollTarget()
	if err != nil {
		return PollInfo{}, err
	}
	if p := s.Poll(); p != nil && p.Status == "active" {
		return PollInfo{}, ErrPollRunning
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return PollInfo{}, fmt.Errorf("%w: %w", ErrNotChanged, err)
	}
	opts := []map[string]any{}
	for _, o := range np.Options {
		opts = append(opts, map[string]any{"optionText": o})
	}
	body := map[string]any{"snippet": map[string]any{"liveChatId": b.LiveChatID, "type": "pollEvent",
		"pollDetails": map[string]any{"metadata": map[string]any{"questionText": np.Question, "options": opts}}}}
	var out struct {
		ID string `json:"id"`
	}
	err = s.data.call(ctx, "POST", tok.Reveal(), "/liveChat/messages", url.Values{"part": {"snippet"}}, body, CostMessageInsert, &out)
	if errors.Is(err, errAuth) {
		go s.streamer.Validate(s.ctx)
	}
	if err != nil {
		return PollInfo{}, pollError(err)
	}
	p := PollInfo{ID: out.ID, Question: np.Question, Status: "active", StartedAt: s.now()}
	for _, o := range np.Options {
		p.Choices = append(p.Choices, PollChoice{Title: o})
	}
	if out.ID != "" {
		s.notePoll(ctx, b, channel, p, false, false)
	}
	s.log.Info("youtube poll started", "choices", len(p.Choices))
	return p, nil
}

// EndPoll closes the running poll (20 units). It's never retried.
func (s *Service) EndPoll(ctx context.Context) (PollInfo, error) {
	b, channel, err := s.pollTarget()
	if err != nil {
		return PollInfo{}, err
	}
	p := s.Poll()
	if p == nil || p.Status != "active" {
		return PollInfo{}, ErrNoPoll
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return PollInfo{}, fmt.Errorf("%w: %w", ErrNotChanged, err)
	}
	var out struct {
		ID      string `json:"id"`
		Snippet struct {
			PublishedAt string `json:"publishedAt"`
			Poll        struct {
				Metadata struct {
					Question string `json:"questionText"`
					Options  []struct {
						Text  string          `json:"optionText"`
						Tally json.RawMessage `json:"tally"`
					} `json:"options"`
				} `json:"metadata"`
			} `json:"pollDetails"`
		} `json:"snippet"`
	}
	q := url.Values{"id": {p.ID}, "status": {"closed"}, "part": {"snippet"}}
	err = s.data.call(ctx, "POST", tok.Reveal(), "/liveChat/messages/transition", q, nil, CostPollClose, &out)
	if errors.Is(err, errAuth) {
		go s.streamer.Validate(s.ctx)
	}
	if err != nil {
		return PollInfo{}, pollError(err)
	}
	ended := *p
	ended.Status = "closed"
	if opts := out.Snippet.Poll.Metadata.Options; len(opts) == len(ended.Choices) {
		for i, o := range opts {
			var n int64
			if _, err := fmt.Sscan(strings.Trim(string(o.Tally), `"`), &n); err == nil {
				ended.Choices[i].Votes = n
			}
		}
	}
	s.notePoll(ctx, b, channel, ended, false, false)
	s.log.Info("youtube poll ended")
	return ended, nil
}
