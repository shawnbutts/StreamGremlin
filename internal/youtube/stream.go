package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// The minute check (M6 slice 7, D-102). Google pushes none of this, so
// while your broadcast's chat is being read StreamGremlin asks each minute:
// videos.list (1 unit) for the viewer count, title and description, and
// subscriptions.list with myRecentSubscribers (1 unit) for new public
// subscribers. About 120 units an hour.

// Quota costs of the minute check and of changing the title.
const (
	CostVideosList        = 1
	CostVideosUpdate      = 50
	CostSubscriptionsList = 1
)

// CheckEvery is how often the viewer count, title and subscribers are
// asked for while your broadcast's chat is read.
const CheckEvery = time.Minute

// Limits on the title and description (Google's videos resource).
const (
	MaxTitleRunes       = 100
	MaxDescriptionBytes = 5000
)

// Event types from the minute check.
const (
	// TypeViewers is the concurrent viewer count changing, on Twitch or
	// YouTube.
	TypeViewers = "broadcast.viewers.changed"
	typeUpdated = "broadcast.channel.updated"
	typeFollow  = "audience.follow.created"
)

// StreamInfo is what the minute check last learned about your live
// broadcast. Viewers is nil when unknown: not asked yet, YouTube didn't
// say (no viewers, or the count is hidden), or it couldn't be asked.
type StreamInfo struct {
	Viewers     *int64    `json:"viewers"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	// Problem says why the last check failed, if it did.
	Problem string `json:"problem,omitempty"`
}

// ViewersChanged reports whether a count moved enough since the last one
// announced to be worth an event: by a tenth, and by at least one.
func ViewersChanged(last, now int64) bool {
	d := now - last
	if d < 0 {
		d = -d
	}
	return d >= max(1, last/10)
}

// watch runs the minute check until ctx ends (readChat's end).
func (s *Service) watch(ctx context.Context, b Broadcast, channel string) {
	var announced *int64 // the viewer count last announced
	first := true
	for {
		s.checkStream(ctx, b, channel, &announced, first)
		s.checkSubscribers(ctx, b, channel)
		first = false
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.checkEvery):
		}
	}
}

// videoSnippet is the part of videos.list's snippet StreamGremlin keeps:
// the title and description it shows, and the fields videos.update would
// otherwise delete.
type videoSnippet struct {
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	CategoryID           string   `json:"categoryId"`
	Tags                 []string `json:"tags,omitempty"`
	DefaultLanguage      string   `json:"defaultLanguage,omitempty"`
	DefaultAudioLanguage string   `json:"defaultAudioLanguage,omitempty"`
}

// video asks videos.list for your broadcast (1 unit): a broadcast is one
// video, with the same id.
func (s *Service) video(ctx context.Context, id string) (videoSnippet, *int64, error) {
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return videoSnippet{}, nil, err
	}
	var out struct {
		Items []struct {
			Snippet videoSnippet `json:"snippet"`
			Live    struct {
				// Google's JSON gives an unsigned long as a string.
				Viewers json.RawMessage `json:"concurrentViewers"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	q := url.Values{"part": {"snippet,liveStreamingDetails"}, "id": {id}}
	if err := s.data.get(ctx, tok.Reveal(), "/videos", q, CostVideosList, &out); err != nil {
		if errors.Is(err, errAuth) {
			go s.streamer.Validate(s.ctx)
		}
		return videoSnippet{}, nil, err
	}
	if len(out.Items) == 0 {
		return videoSnippet{}, nil, errors.New("youtube: YouTube didn't find your broadcast's video")
	}
	it := out.Items[0]
	var viewers *int64
	if raw := strings.Trim(string(it.Live.Viewers), `"`); raw != "" && raw != "null" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
			viewers = &n
		}
	}
	return it.Snippet, viewers, nil
}

// checkStream asks for the viewer count, title and description, and
// records what changed. The first check of a broadcast only learns them.
func (s *Service) checkStream(ctx context.Context, b Broadcast, channel string, announced **int64, first bool) {
	if s.quota.status(ctx).Exhausted {
		return
	}
	sn, viewers, err := s.video(ctx, b.ID)
	if ctx.Err() != nil {
		return
	}
	now := s.now()
	s.chatMu.Lock()
	if s.stream == nil {
		s.stream = &StreamInfo{Title: b.Title}
	}
	st := s.stream
	st.CheckedAt = now
	if err != nil {
		st.Problem, st.Viewers = "Couldn't ask YouTube: "+describe(err), nil
		s.chatMu.Unlock()
		s.changed()
		return
	}
	st.Problem = ""
	changedInfo := !first && (sn.Title != st.Title || sn.Description != st.Description)
	st.Title, st.Description, st.Viewers = sn.Title, sn.Description, viewers
	s.chatMu.Unlock()
	s.changed()

	if changedInfo {
		s.storeUpdated(ctx, b, channel, sn.Title, sn.Description)
	}
	if viewers == nil {
		return
	}
	if *announced == nil {
		*announced = viewers // the first count is learned, not announced
		return
	}
	if !ViewersChanged(**announced, *viewers) {
		return
	}
	prev := **announced
	*announced = viewers
	s.storeViewers(ctx, b, channel, prev, *viewers)
}

func (s *Service) storeViewers(ctx context.Context, b Broadcast, channel string, prev, now int64) {
	at := s.now()
	raw, _ := json.Marshal(map[string]any{"viewers": now, "previous": prev, "change": now - prev, "broadcast_id": b.ID})
	env := events.Envelope{EventID: events.NewID(), Type: TypeViewers, Source: events.SourceYouTube,
		SourceEventID: fmt.Sprintf("viewers:%s:%d", b.ID, at.UnixNano()), OccurredAt: at, ChannelID: channel, Payload: raw,
		DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	s.storeEvent(ctx, env)
}

// storeUpdated records your title or description changing, from anywhere.
func (s *Service) storeUpdated(ctx context.Context, b Broadcast, channel, title, description string) {
	at := s.now()
	raw, _ := json.Marshal(map[string]any{"title": title, "description": description, "category": "", "broadcast_id": b.ID,
		"url": youtubeWatch + b.ID})
	env := events.Envelope{EventID: events.NewID(), Type: typeUpdated, Source: events.SourceYouTube,
		SourceEventID: fmt.Sprintf("updated:%s:%d", b.ID, at.UnixNano()), OccurredAt: at, ChannelID: channel, Payload: raw,
		DeliveryQuality: events.Confirmed}
	env.CorrelationID = env.EventID
	s.storeEvent(ctx, env)
}

// checkSubscribers asks for your newest public subscribers (1 unit) and
// records those who subscribed after the broadcast started, once each
// ever (by channel id). Private subscriptions are never listed, and Google
// says the list "might be limited".
func (s *Service) checkSubscribers(ctx context.Context, b Broadcast, channel string) {
	if s.quota.status(ctx).Exhausted || b.StartedAt.IsZero() {
		return
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return
	}
	var out struct {
		Items []struct {
			Snippet struct {
				PublishedAt string `json:"publishedAt"`
			} `json:"snippet"`
			Subscriber struct {
				ChannelID string `json:"channelId"`
				Title     string `json:"title"`
			} `json:"subscriberSnippet"`
		} `json:"items"`
	}
	q := url.Values{"part": {"snippet,subscriberSnippet"}, "myRecentSubscribers": {"true"}, "maxResults": {"50"}}
	if err := s.data.get(ctx, tok.Reveal(), "/subscriptions", q, CostSubscriptionsList, &out); err != nil {
		if ctx.Err() == nil {
			s.log.Info("youtube: couldn't ask for subscribers", "detail", describe(err))
		}
		return
	}
	// Newest first: store oldest first, so they're announced in order.
	for i := len(out.Items) - 1; i >= 0; i-- {
		it := out.Items[i]
		at, err := time.Parse(time.RFC3339, it.Snippet.PublishedAt)
		if err != nil || it.Subscriber.ChannelID == "" || at.Before(b.StartedAt) {
			continue
		}
		name := it.Subscriber.Title
		raw, _ := json.Marshal(map[string]any{"subscribed_at": at.UTC().Format(time.RFC3339), "public_only": true,
			"channel_url": "https://www.youtube.com/channel/" + it.Subscriber.ChannelID})
		env := events.Envelope{EventID: events.NewID(), Type: typeFollow, Source: events.SourceYouTube,
			SourceEventID: "subscribed:" + it.Subscriber.ChannelID, OccurredAt: at, ChannelID: channel,
			Actor:   &events.Actor{Platform: events.SourceYouTube, ID: it.Subscriber.ChannelID, Login: Login(name), DisplayName: name},
			Payload: raw, DeliveryQuality: events.Confirmed}
		env.CorrelationID = env.EventID
		s.storeEvent(ctx, env)
	}
}

// Stream returns what the minute check last learned, or nil while no
// broadcast's chat is read.
func (s *Service) Stream() *StreamInfo {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	if s.stream == nil {
		return nil
	}
	c := *s.stream
	if c.Viewers != nil {
		v := *c.Viewers
		c.Viewers = &v
	}
	return &c
}

// StreamChange is a new title or description; nil leaves one as it is.
type StreamChange struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ErrNotFound: YouTube doesn't have it, or no longer does.
var ErrNotFound = errors.New("youtube: YouTube doesn't have it")

// CheckStreamChange says what's wrong with a change, before anything is
// asked of YouTube.
func CheckStreamChange(c StreamChange) error {
	if c.Title == nil && c.Description == nil {
		return fmt.Errorf("%w: change the title, the description, or both", ErrInvalid)
	}
	if c.Title != nil {
		t := strings.TrimSpace(*c.Title)
		if t == "" || utf8.RuneCountInString(t) > MaxTitleRunes {
			return fmt.Errorf("%w: a YouTube title is 1 to %d characters", ErrInvalid, MaxTitleRunes)
		}
		if strings.ContainsAny(t, "<>") {
			return fmt.Errorf("%w: YouTube doesn't allow < or > in a title", ErrInvalid)
		}
	}
	if c.Description != nil && (len(*c.Description) > MaxDescriptionBytes || strings.ContainsAny(*c.Description, "<>")) {
		return fmt.Errorf("%w: a YouTube description is up to %d bytes, without < or >", ErrInvalid, MaxDescriptionBytes)
	}
	return nil
}

// SetStream changes your live broadcast's title or description: it reads
// the video's snippet (1 unit), then sends it back changed with
// videos.update (50 units), since Google deletes any snippet field an
// update leaves out. It's never retried.
func (s *Service) SetStream(ctx context.Context, c StreamChange) (StreamInfo, error) {
	if err := CheckStreamChange(c); err != nil {
		return StreamInfo{}, err
	}
	ready, channel := s.streamerReady()
	if !ready {
		return StreamInfo{}, ErrNotSignedIn
	}
	cs := s.ChatStatus()
	if cs.Broadcast == nil {
		return StreamInfo{}, ErrNotLive
	}
	b := *cs.Broadcast
	sn, viewers, err := s.video(ctx, b.ID)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("%w: %w", ErrNotChanged, err)
	}
	if c.Title != nil {
		sn.Title = strings.TrimSpace(*c.Title)
	}
	if c.Description != nil {
		sn.Description = *c.Description
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("%w: %w", ErrNotChanged, err)
	}
	body := map[string]any{"id": b.ID, "snippet": sn}
	var out struct {
		Snippet videoSnippet `json:"snippet"`
	}
	if err := s.data.call(ctx, "PUT", tok.Reveal(), "/videos", url.Values{"part": {"snippet"}}, body, CostVideosUpdate, &out); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Reason == "videoNotFound" {
			return StreamInfo{}, fmt.Errorf("%w: your broadcast's video", ErrNotFound)
		}
		if errors.As(err, &ae) && (ae.Reason == "invalidTitle" || ae.Reason == "invalidDescription") {
			return StreamInfo{}, fmt.Errorf("%w: %s", ErrInvalid, ae.Message)
		}
		return StreamInfo{}, err
	}
	if out.Snippet.Title != "" {
		sn = out.Snippet
	}
	s.chatMu.Lock()
	if s.stream == nil {
		s.stream = &StreamInfo{}
	}
	changed := s.stream.Title != sn.Title || s.stream.Description != sn.Description
	s.stream.Title, s.stream.Description, s.stream.Viewers, s.stream.CheckedAt = sn.Title, sn.Description, viewers, s.now()
	info := *s.stream
	s.chatMu.Unlock()
	s.changed()
	if changed {
		s.storeUpdated(context.WithoutCancel(ctx), b, channel, sn.Title, sn.Description)
	}
	s.log.Info("youtube stream info changed", "title", c.Title != nil, "description", c.Description != nil)
	return info, nil
}
