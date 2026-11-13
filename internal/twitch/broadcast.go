package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The channel's own requests (D-086): the title and category, clips,
// stream markers, the stream itself, and when a viewer followed. Each is
// one of Twitch's documented Helix requests, as the streamer.

// Scopes.
const (
	// BroadcastScope changes the title and category, and makes markers.
	BroadcastScope = "channel:manage:broadcast"
	// ClipsScope makes clips.
	ClipsScope = "clips:edit"
)

// Limits from Twitch's reference.
const (
	MaxTitle       = 140
	MaxMarker      = 140
	MinClipSeconds = 5.0
	MaxClipSeconds = 60.0
)

// Errors.
var (
	// ErrNotLive: Twitch said the channel isn't live, so nothing was made.
	ErrNotLive = errors.New("twitch: you're not live (or past broadcasts are off), so Twitch made nothing")
	// ErrNoCategory: no category matches the name.
	ErrNoCategory = errors.New("twitch: no Twitch category matches that name")
	// ErrClipNotMade: Twitch took the request but never showed the clip.
	ErrClipNotMade = errors.New("twitch: Twitch didn't make the clip: it never appeared within a minute, which Twitch says means it failed")
	// ErrOutcomeUnknown: Twitch didn't answer, so whether it happened is
	// unknown. It's never sent again.
	ErrOutcomeUnknown = errors.New("twitch: Twitch didn't answer, so it may or may not have happened; it wasn't sent again")
	// ErrNoSuchUser: Twitch has no account by that name.
	ErrNoSuchUser = errors.New("twitch: Twitch has no account by that name")
)

// channelError puts a failed request in words: not signed in, a missing
// permission, Twitch's own refusal, or no answer.
func channelError(err error, what string) error {
	if err == nil {
		return nil
	}
	var he *helixError
	switch {
	case errors.Is(err, errStreamerSignIn), errors.Is(err, ErrNoClientID):
		return err
	case errors.Is(err, ErrNeedsPermission):
		return fmt.Errorf("%w to %s: sign in on Connections", ErrNeedsPermission, what)
	case errors.As(err, &he) && he.Status == 401:
		return fmt.Errorf("%w to %s: %s", ErrNeedsPermission, what, he.Message)
	case errors.As(err, &he) && he.Status == 404 && strings.Contains(strings.ToLower(he.Message), "live"):
		return ErrNotLive
	case errors.As(err, &he):
		return fmt.Errorf("twitch: Twitch refused it: %s", he.Message)
	case errors.Is(err, errUnreachable):
		return fmt.Errorf("%w (%w)", ErrOutcomeUnknown, err)
	}
	return err
}

// SetTitle changes the stream's title (Modify Channel Information).
func (s *Service) SetTitle(ctx context.Context, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > MaxTitle {
		return fmt.Errorf("%w: a title is 1 to %d characters", ErrInvalid, MaxTitle)
	}
	err := s.streamerRaw(ctx, BroadcastScope, "PATCH", "/channels", nil, struct {
		Title string `json:"title"`
	}{title}, nil)
	if err != nil {
		return channelError(err, "change your title")
	}
	s.log.Info("title changed")
	return nil
}

// Category is a Twitch category (a game, or Just Chatting and the like).
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Exact is true when the name matched exactly, false when it's
	// Twitch's search's first answer.
	Exact bool `json:"exact"`
}

// FindCategory finds a category by its exact name (Get Games), or else
// Twitch's search's first answer (Search Categories).
func (s *Service) FindCategory(ctx context.Context, name string) (Category, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Category{}, fmt.Errorf("%w: name a category, like Just Chatting", ErrInvalid)
	}
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, "", "GET", "/games", url.Values{"name": {name}}, nil, &out); err != nil {
		return Category{}, channelError(err, "find categories")
	}
	if len(out.Data) > 0 {
		return Category{ID: out.Data[0].ID, Name: out.Data[0].Name, Exact: true}, nil
	}
	if err := s.streamerRaw(ctx, "", "GET", "/search/categories", url.Values{"query": {name}, "first": {"5"}}, nil, &out); err != nil {
		return Category{}, channelError(err, "find categories")
	}
	for _, d := range out.Data { // an exact match in another case first
		if strings.EqualFold(d.Name, name) {
			return Category{ID: d.ID, Name: d.Name, Exact: true}, nil
		}
	}
	if len(out.Data) == 0 {
		return Category{}, ErrNoCategory
	}
	return Category{ID: out.Data[0].ID, Name: out.Data[0].Name}, nil
}

// SetCategory changes the stream's category to the one name finds.
func (s *Service) SetCategory(ctx context.Context, name string) (Category, error) {
	c, err := s.FindCategory(ctx, name)
	if err != nil {
		return Category{}, err
	}
	err = s.streamerRaw(ctx, BroadcastScope, "PATCH", "/channels", nil, struct {
		GameID string `json:"game_id"`
	}{c.ID}, nil)
	if err != nil {
		return Category{}, channelError(err, "change your category")
	}
	s.log.Info("category changed", "category", c.Name)
	return c, nil
}

// StreamInfo is the stream now: live or not, and the channel's title and
// category either way.
type StreamInfo struct {
	Live      bool       `json:"live"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	Title     string     `json:"title"`
	Category  string     `json:"category"`
	Viewers   int        `json:"viewers"`
}

// Stream reads Get Streams and Get Channel Information.
func (s *Service) Stream(ctx context.Context) (StreamInfo, error) {
	me := s.streamer.Status().UserID
	var out struct {
		Data []struct {
			Type      string    `json:"type"`
			Title     string    `json:"title"`
			GameName  string    `json:"game_name"`
			Viewers   int       `json:"viewer_count"`
			StartedAt time.Time `json:"started_at"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, "", "GET", "/streams", url.Values{"user_id": {me}}, nil, &out); err != nil {
		return StreamInfo{}, channelError(err, "read your stream")
	}
	for _, d := range out.Data {
		if d.Type == "live" && !d.StartedAt.IsZero() {
			at := d.StartedAt
			return StreamInfo{Live: true, StartedAt: &at, Title: d.Title, Category: d.GameName, Viewers: d.Viewers}, nil
		}
	}
	var ch struct {
		Data []struct {
			Title    string `json:"title"`
			GameName string `json:"game_name"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, "", "GET", "/channels", nil, nil, &ch); err != nil {
		return StreamInfo{}, channelError(err, "read your stream")
	}
	if len(ch.Data) == 0 {
		return StreamInfo{}, errors.New("twitch: Twitch didn't return your channel")
	}
	return StreamInfo{Title: ch.Data[0].Title, Category: ch.Data[0].GameName}, nil
}

// Marker is a stream marker Twitch made.
type Marker struct {
	ID              string `json:"id"`
	PositionSeconds int    `json:"position_seconds"`
	Description     string `json:"description"`
}

// CreateMarker marks this moment of the stream for its past broadcast.
// It's never retried.
func (s *Service) CreateMarker(ctx context.Context, description string) (Marker, error) {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > MaxMarker {
		return Marker{}, fmt.Errorf("%w: a marker's description is up to %d characters", ErrInvalid, MaxMarker)
	}
	var out struct {
		Data []Marker `json:"data"`
	}
	err := s.streamerRaw(ctx, BroadcastScope, "POST", "/streams/markers", nil, struct {
		UserID      string `json:"user_id"`
		Description string `json:"description,omitempty"`
	}{s.streamer.Status().UserID, description}, &out)
	if err != nil {
		return Marker{}, channelError(err, "add stream markers")
	}
	if len(out.Data) == 0 {
		return Marker{}, ErrOutcomeUnknown
	}
	s.log.Info("stream marker added", "position_seconds", out.Data[0].PositionSeconds)
	return out.Data[0], nil
}

// Clip is a clip Twitch made.
type Clip struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	EditURL string `json:"edit_url"`
}

// defaultClipChecks: Twitch makes a clip after answering, and says to
// assume it failed if Get Clips hasn't shown it after 60 seconds.
var defaultClipChecks = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second,
	5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second}

// CreateClip asks Twitch for a clip of the last seconds (5 to 60; 0 for
// Twitch's 30) with an optional title, then waits until Get Clips shows
// it. It's never sent twice.
func (s *Service) CreateClip(ctx context.Context, title string, seconds float64) (Clip, error) {
	title = strings.TrimSpace(title)
	if seconds != 0 && (seconds < MinClipSeconds || seconds > MaxClipSeconds) {
		return Clip{}, fmt.Errorf("%w: a clip is 5 to 60 seconds long", ErrInvalid)
	}
	if utf8.RuneCountInString(title) > 100 {
		return Clip{}, fmt.Errorf("%w: a clip's title is up to 100 characters", ErrInvalid)
	}
	q := url.Values{}
	if title != "" {
		q.Set("title", title)
	}
	if seconds != 0 {
		q.Set("duration", strconv.FormatFloat(seconds, 'f', 1, 64))
	}
	var out struct {
		Data []Clip `json:"data"`
	}
	if err := s.streamerRaw(ctx, ClipsScope, "POST", "/clips", q, nil, &out); err != nil {
		return Clip{}, channelError(err, "make clips")
	}
	if len(out.Data) == 0 || out.Data[0].ID == "" {
		return Clip{}, ErrOutcomeUnknown
	}
	c := out.Data[0]
	checks := s.clipChecks
	if checks == nil {
		checks = defaultClipChecks
	}
	for _, wait := range checks {
		select {
		case <-ctx.Done():
			return Clip{}, fmt.Errorf("%w: stopped waiting for clip %s", ErrOutcomeUnknown, c.ID)
		case <-time.After(wait):
		}
		var got struct {
			Data []struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := s.streamerRaw(ctx, "", "GET", "/clips", url.Values{"id": {c.ID}}, nil, &got); err != nil {
			continue // asked again at the next check
		}
		if len(got.Data) > 0 && got.Data[0].ID == c.ID {
			c.URL = got.Data[0].URL
			if c.URL == "" {
				c.URL = "https://clips.twitch.tv/" + c.ID
			}
			s.log.Info("clip made", "clip", c.ID)
			return c, nil
		}
	}
	return Clip{}, ErrClipNotMade
}

// FollowAge is whether a viewer named by login follows the channel.
type FollowAge struct {
	UserID      string    `json:"user_id"`
	Login       string    `json:"login"`
	DisplayName string    `json:"display_name"`
	Follows     bool      `json:"follows"`
	FollowedAt  time.Time `json:"followed_at,omitzero"`
}

// FollowerByLogin finds a viewer by name (Get Users), then whether and
// since when they follow (Get Channel Followers).
func (s *Service) FollowerByLogin(ctx context.Context, login string) (FollowAge, error) {
	login = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(login), "@"))
	if login == "" || len(login) > 25 || strings.ContainsAny(login, " /?&#") {
		return FollowAge{}, fmt.Errorf("%w: name one viewer, like @friend", ErrInvalid)
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, "", "GET", "/users", url.Values{"login": {login}}, nil, &out); err != nil {
		return FollowAge{}, channelError(err, "look up viewers")
	}
	if len(out.Data) == 0 {
		return FollowAge{}, ErrNoSuchUser
	}
	u := out.Data[0]
	f, err := s.Follower(ctx, u.ID)
	if err != nil {
		return FollowAge{}, err
	}
	return FollowAge{UserID: u.ID, Login: u.Login, DisplayName: u.DisplayName, Follows: f.Follows, FollowedAt: f.FollowedAt}, nil
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + one + "s"
}

// DurationWords says how long, in words: "2 hours 5 minutes".
func DurationWords(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	days, hours, mins := int(d/(24*time.Hour)), int(d/time.Hour)%24, int(d/time.Minute)%60
	var parts []string
	if days > 0 {
		parts = append(parts, plural(days, "day"))
	}
	if hours > 0 {
		parts = append(parts, plural(hours, "hour"))
	}
	if mins > 0 && days == 0 {
		parts = append(parts, plural(mins, "minute"))
	}
	return strings.Join(parts, " ")
}

// SinceWords says how long from at to now by the calendar: "2 years 3
// months", "20 days", or "today".
func SinceWords(at, now time.Time) string {
	months := 0
	for !at.AddDate(0, months+1, 0).After(now) {
		months++
	}
	if months == 0 {
		days := int(now.Sub(at) / (24 * time.Hour))
		if days == 0 {
			return "today"
		}
		return plural(days, "day")
	}
	var parts []string
	if y := months / 12; y > 0 {
		parts = append(parts, plural(y, "year"))
	}
	if m := months % 12; m > 0 {
		parts = append(parts, plural(m, "month"))
	}
	return strings.Join(parts, " ")
}
