package mockyoutube

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// M6 slice 7: videos.list and videos.update for a broadcast's video,
// subscriptions.list with myRecentSubscribers, and polls (pollEvent
// messages, liveChatMessages.transition, the stream's active poll).

type subscriber struct {
	owner, channel, name string
	at                   time.Time
}

// VideoUpdate is one videos.update the stand-in accepted.
type VideoUpdate struct {
	ID, Title, Description, CategoryID string
	Tags                               []string
}

// SetViewers sets a broadcast's concurrent viewers; a negative count
// makes YouTube not say (no viewers, or a hidden count).
func (s *Server) SetViewers(broadcastID string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.broadcasts {
		if b.ID == broadcastID {
			if n < 0 {
				b.Viewers = nil
			} else {
				b.Viewers = &n
			}
		}
	}
}

// SetVideo changes a broadcast's title and description, as YouTube Studio
// would.
func (s *Server) SetVideo(broadcastID, title, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.broadcasts {
		if b.ID == broadcastID {
			b.Title, b.Description = title, description
		}
	}
}

// Subscribe makes a channel subscribe publicly to owner's channel at a
// time.
func (s *Server) Subscribe(owner, channel, name string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, subscriber{owner, channel, name, at})
}

// VideoUpdates lists the videos.update calls accepted, oldest first.
func (s *Server) VideoUpdates() []VideoUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.videoUpdates)
}

func (s *Server) videos(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := "videos.list"
	if r.Method == http.MethodPut {
		res = "videos.update"
	}
	t, ok := s.authed(w, r, res)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		s.updateVideo(w, r, t)
		return
	}
	q := r.URL.Query()
	items := []any{}
	for _, b := range s.broadcasts {
		if b.ID != q.Get("id") {
			continue
		}
		item := map[string]any{"kind": "youtube#video", "id": b.ID}
		parts := strings.Split(q.Get("part"), ",")
		if slices.Contains(parts, "snippet") {
			sn := map[string]any{"title": b.Title, "description": b.Description, "categoryId": b.CategoryID,
				"channelId": b.Channel, "publishedAt": b.Started.UTC().Format(time.RFC3339), "liveBroadcastContent": "live"}
			// Tags are only shown to the video's owner.
			if b.Channel == t.channel && len(b.Tags) > 0 {
				sn["tags"] = b.Tags
			}
			item["snippet"] = sn
		}
		if slices.Contains(parts, "liveStreamingDetails") {
			d := map[string]any{"actualStartTime": b.Started.UTC().Format(time.RFC3339)}
			if b.Viewers != nil && !b.ended {
				d["concurrentViewers"] = strconv.FormatInt(*b.Viewers, 10) // an unsigned long, as a string
			}
			item["liveStreamingDetails"] = d
		}
		items = append(items, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#videoListResponse", "items": items})
}

// updateVideo is videos.update with part=snippet: title and categoryId
// are required, and any other snippet field left out is deleted. Call with
// mu held.
func (s *Server) updateVideo(w http.ResponseWriter, r *http.Request, t token) {
	var in struct {
		ID      string `json:"id"`
		Snippet *struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			CategoryID  string   `json:"categoryId"`
			Tags        []string `json:"tags"`
		} `json:"snippet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || r.URL.Query().Get("part") != "snippet" || in.Snippet == nil {
		apiErr(w, http.StatusBadRequest, "invalidPart", "part=snippet and a snippet are required here")
		return
	}
	var b *Broadcast
	for _, x := range s.broadcasts {
		if x.ID == in.ID {
			b = x
		}
	}
	sn := in.Snippet
	switch {
	case b == nil:
		apiErr(w, http.StatusNotFound, "videoNotFound", "The video that you are trying to update cannot be found.")
		return
	case b.Channel != t.channel:
		apiErr(w, http.StatusForbidden, "forbidden", "The request cannot access the video.")
		return
	case sn.Title == "" || len([]rune(sn.Title)) > 100 || strings.ContainsAny(sn.Title, "<>"):
		apiErr(w, http.StatusBadRequest, "invalidTitle", "The request metadata specifies an invalid video title.")
		return
	case sn.CategoryID == "":
		apiErr(w, http.StatusBadRequest, "invalidCategoryId", "The snippet.categoryId property specifies an invalid category ID.")
		return
	case len(sn.Description) > 5000 || strings.ContainsAny(sn.Description, "<>"):
		apiErr(w, http.StatusBadRequest, "invalidDescription", "The request metadata specifies an invalid video description.")
		return
	}
	b.Title, b.Description, b.CategoryID, b.Tags = sn.Title, sn.Description, sn.CategoryID, sn.Tags
	s.videoUpdates = append(s.videoUpdates, VideoUpdate{ID: b.ID, Title: sn.Title, Description: sn.Description, CategoryID: sn.CategoryID,
		Tags: slices.Clone(sn.Tags)})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#video", "id": b.ID,
		"snippet": map[string]any{"title": b.Title, "description": b.Description, "categoryId": b.CategoryID, "tags": b.Tags}})
}

func (s *Server) subscriptions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.authed(w, r, "subscriptions.list")
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("myRecentSubscribers") != "true" {
		apiErr(w, http.StatusBadRequest, "invalidFilters", "only myRecentSubscribers=true is served here")
		return
	}
	max := 5
	if n, err := strconv.Atoi(q.Get("maxResults")); err == nil && n >= 0 && n <= 50 {
		max = n
	}
	var mine []subscriber
	for _, sub := range s.subscribers {
		if sub.owner == t.channel {
			mine = append(mine, sub)
		}
	}
	slices.SortStableFunc(mine, func(a, b subscriber) int { return b.at.Compare(a.at) }) // newest first
	items := []any{}
	for i, sub := range mine {
		if i == max {
			break
		}
		items = append(items, map[string]any{"kind": "youtube#subscription", "id": "sub-" + sub.channel,
			"snippet":           map[string]any{"publishedAt": sub.at.UTC().Format(time.RFC3339), "resourceId": map[string]any{"channelId": t.channel}},
			"subscriberSnippet": map[string]any{"channelId": sub.channel, "title": sub.name, "description": ""}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#subscriptionListResponse", "items": items})
}

type pollDetails struct {
	Metadata struct {
		QuestionText string `json:"questionText"`
		Options      []struct {
			OptionText string `json:"optionText"`
		} `json:"options"`
	} `json:"metadata"`
}

func clonePoll(m livechat.Message) livechat.Message {
	if m.Snippet.Poll != nil {
		p := *m.Snippet.Poll
		p.Options = slices.Clone(p.Options)
		m.Snippet.Poll = &p
	}
	return m
}

func pollJSON(m livechat.Message) map[string]any {
	p := m.Snippet.Poll
	opts := []any{}
	for _, o := range p.Options {
		opts = append(opts, map[string]any{"optionText": o.Text, "tally": strconv.FormatInt(o.Tally, 10)})
	}
	st := "active"
	if p.Status == livechat.PollClosed {
		st = "closed"
	}
	return map[string]any{"kind": "youtube#liveChatMessage", "id": m.ID,
		"snippet": map[string]any{"type": "pollEvent", "liveChatId": m.Snippet.LiveChatID, "authorChannelId": m.Snippet.AuthorChannelID,
			"publishedAt": m.Snippet.PublishedAt,
			"pollDetails": map[string]any{"metadata": map[string]any{"questionText": p.Question, "options": opts, "status": st}}}}
}

// insertPoll is liveChatMessages.insert with type pollEvent: only the
// chat's owner, 2 to 4 options, one poll at a time.
func (s *Server) insertPoll(w http.ResponseWriter, r *http.Request, chatID string, d pollDetails) {
	s.mu.Lock()
	t, ok := s.authed(w, r, "liveChatMessages.insert")
	if !ok {
		s.mu.Unlock()
		return
	}
	c := s.chats[chatID]
	switch {
	case c == nil:
		s.mu.Unlock()
		apiErr(w, http.StatusNotFound, "liveChatNotFound", "The live chat could not be found.")
		return
	case c.ended:
		s.mu.Unlock()
		apiErr(w, http.StatusForbidden, "liveChatEnded", "The specified live chat is no longer live.")
		return
	case s.ownerOf(chatID) != t.channel:
		s.mu.Unlock()
		apiErr(w, http.StatusForbidden, "forbidden", "You don't have the permissions required to create the specified message.")
		return
	case d.Metadata.QuestionText == "" || len(d.Metadata.Options) < 2 || len(d.Metadata.Options) > 4:
		s.mu.Unlock()
		apiErr(w, http.StatusBadRequest, "invalidPollDetails", "A poll needs a question and 2 to 4 options.")
		return
	case c.poll != nil:
		s.mu.Unlock()
		apiErr(w, http.StatusBadRequest, "preconditionCheckFailed", "A pinned active poll already exists.")
		return
	}
	p := &livechat.Poll{Question: d.Metadata.QuestionText, Status: livechat.PollActive}
	for _, o := range d.Metadata.Options {
		p.Options = append(p.Options, livechat.PollOption{Text: o.OptionText})
	}
	s.mu.Unlock()
	id := s.add(chatID, livechat.Message{Snippet: livechat.Snippet{Type: livechat.TypePoll, AuthorChannelID: t.channel, Poll: p},
		Author: livechat.Author{ChannelID: t.channel, IsChatOwner: true}})
	s.mu.Lock()
	var m livechat.Message
	for _, x := range c.msgs {
		if x.ID == id {
			m = clonePoll(x)
		}
	}
	c.poll = &m
	s.pollChangedLocked(c)
	out := pollJSON(m)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) pollChangedLocked(c *chat) {
	c.pollVer++
	close(c.wake)
	c.wake = make(chan struct{})
}

// transition is liveChatMessages.transition: only status=closed, only for
// an active poll, by the chat's owner. The closed poll leaves the stream's
// active poll.
func (s *Server) transition(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "methodNotAllowed", "POST only")
		return
	}
	t, ok := s.authed(w, r, "liveChatMessages.transition")
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("status") != "closed" {
		apiErr(w, http.StatusBadRequest, "invalidStatus", "You can only transition to closed.")
		return
	}
	for chatID, c := range s.chats {
		if c.poll == nil || c.poll.ID != q.Get("id") {
			continue
		}
		if s.ownerOf(chatID) != t.channel {
			apiErr(w, http.StatusForbidden, "forbidden", "Only the chat's owner can close a poll.")
			return
		}
		m := clonePoll(*c.poll)
		m.Snippet.Poll.Status = livechat.PollClosed
		c.poll = nil
		s.pollChangedLocked(c)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pollJSON(m))
		return
	}
	apiErr(w, http.StatusNotFound, "liveChatMessageNotFound", "The poll could not be found, or it isn't active.")
}

// Vote adds a vote for an option (from 0) of a chat's active poll.
func (s *Server) Vote(chatID string, option int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[chatID]
	if c == nil || c.poll == nil || option < 0 || option >= len(c.poll.Snippet.Poll.Options) {
		return
	}
	m := clonePoll(*c.poll)
	m.Snippet.Poll.Options[option].Tally++
	c.poll = &m
	s.pollChangedLocked(c)
}

// ClosePoll closes a chat's active poll, as YouTube's own page would.
func (s *Server) ClosePoll(chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chats[chatID]; c != nil && c.poll != nil {
		c.poll = nil
		s.pollChangedLocked(c)
	}
}

// ActivePoll returns a chat's active poll's question and tallies.
func (s *Server) ActivePoll(chatID string) (question string, tallies []int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[chatID]
	if c == nil || c.poll == nil {
		return "", nil, false
	}
	for _, o := range c.poll.Snippet.Poll.Options {
		tallies = append(tallies, o.Tally)
	}
	return c.poll.Snippet.Poll.Question, tallies, true
}
