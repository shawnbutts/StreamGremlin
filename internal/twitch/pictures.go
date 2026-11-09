package twitch

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Profile pictures for overlays (docs/adr/0017-alert-overlays.md). Get
// Users answers with profile_image_url and needs no scope. Only addresses
// on Twitch's picture host are used, even though they came from Twitch.

// PictureHost is where Twitch serves profile pictures.
const PictureHost = "static-cdn.jtvnw.net"

const (
	pictureTTL      = 30 * time.Minute
	pictureCacheMax = 500
)

var userIDPattern = func(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type pictureCache struct {
	mu   sync.Mutex
	urls map[string]cachedPicture
}

type cachedPicture struct {
	url string
	at  time.Time
}

// GoodPicture reports whether an address is a Twitch profile picture.
func GoodPicture(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == PictureHost && u.User == nil && strings.HasPrefix(u.Path, "/")
}

func (h *helix) userPicture(ctx context.Context, clientID, token, userID string) (string, error) {
	var out struct {
		Data []struct {
			ProfileImageURL string `json:"profile_image_url"`
		} `json:"data"`
	}
	if err := h.call(ctx, "GET", "/users?id="+url.QueryEscape(userID), clientID, token, nil, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", nil
	}
	return out.Data[0].ProfileImageURL, nil
}

// ProfilePicture returns a Twitch user's profile picture address, by their
// id, or "" when they have none Twitch serves from its picture host.
func (s *Service) ProfilePicture(ctx context.Context, userID string) (string, error) {
	if !userIDPattern(userID) {
		return "", fmt.Errorf("%w: a Twitch user id is digits", ErrInvalid)
	}
	now := s.now()
	s.pictures.mu.Lock()
	if c, ok := s.pictures.urls[userID]; ok && now.Sub(c.at) < pictureTTL {
		s.pictures.mu.Unlock()
		return c.url, nil
	}
	s.pictures.mu.Unlock()
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	clientID, _ := s.ClientID(ctx)
	if clientID == "" {
		return "", ErrNoClientID
	}
	pic, err := s.helix.userPicture(ctx, clientID, tok.Reveal(), userID)
	if err != nil {
		return "", fmt.Errorf("twitch: reading the profile picture: %w", err)
	}
	if !GoodPicture(pic) {
		pic = ""
	}
	s.pictures.mu.Lock()
	defer s.pictures.mu.Unlock()
	if s.pictures.urls == nil || len(s.pictures.urls) >= pictureCacheMax {
		s.pictures.urls = map[string]cachedPicture{}
	}
	s.pictures.urls[userID] = cachedPicture{url: pic, at: now}
	return pic, nil
}
