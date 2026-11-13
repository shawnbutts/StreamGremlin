package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Twitch's Blocked Terms (D-088): the channel's own list, which Twitch
// holds back before anyone sees the message, StreamGremlin included. Read,
// added to and removed from as the streamer (moderator_id = broadcaster).

// BlockedTermsScope reads and changes the list.
const BlockedTermsScope = "moderator:manage:blocked_terms"

// MaxBlockedTerm is the reference's longest term.
const MaxBlockedTerm = 500

// BlockedTerm is one term on Twitch's list.
type BlockedTerm struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is set on a term AutoMod blocked for a while; nil for one
	// added by hand or blocked for good.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type blockedTermData struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at"`
}

func (d blockedTermData) term() BlockedTerm {
	t := BlockedTerm{ID: d.ID, Text: d.Text}
	t.CreatedAt, _ = time.Parse(time.RFC3339, d.CreatedAt)
	if d.ExpiresAt != nil && *d.ExpiresAt != "" {
		if at, err := time.Parse(time.RFC3339, *d.ExpiresAt); err == nil {
			t.ExpiresAt = &at
		}
	}
	return t
}

// blockedTermPages bounds a read: 100 a page, so 5000 terms.
const blockedTermPages = 50

// BlockedTerms reads the whole list, newest first, as Twitch orders it.
func (s *Service) BlockedTerms(ctx context.Context) ([]BlockedTerm, error) {
	me := s.streamer.Status().UserID
	out := []BlockedTerm{}
	after := ""
	for range blockedTermPages {
		q := url.Values{"moderator_id": {me}, "first": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var page struct {
			Data       []blockedTermData `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		if err := s.streamerRaw(ctx, BlockedTermsScope, "GET", "/moderation/blocked_terms", q, nil, &page); err != nil {
			return nil, blockedTermsError(err)
		}
		for _, d := range page.Data {
			out = append(out, d.term())
		}
		// A full page carries a cursor even when nothing is left; the
		// page after it is then empty.
		if page.Pagination.Cursor == "" || len(page.Data) == 0 {
			return out, nil
		}
		after = page.Pagination.Cursor
	}
	return out, nil
}

// blockedTermRetries are the waits before trying again when Twitch says
// the list is busy with another change, as it asks.
var blockedTermRetries = []time.Duration{300 * time.Millisecond, time.Second}

// AddBlockedTerm adds a term. Twitch answers a term that's already there
// with the one it has, so adding is safe to repeat.
func (s *Service) AddBlockedTerm(ctx context.Context, text string) (BlockedTerm, error) {
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n < 2 || n > MaxBlockedTerm {
		return BlockedTerm{}, fmt.Errorf("%w: a blocked term is 2 to %d characters", ErrInvalid, MaxBlockedTerm)
	}
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}}
	for i := 0; ; i++ {
		var out struct {
			Data []blockedTermData `json:"data"`
		}
		err := s.streamerRaw(ctx, BlockedTermsScope, "POST", "/moderation/blocked_terms", q, struct {
			Text string `json:"text"`
		}{text}, &out)
		if busy(err) && i < len(blockedTermRetries) {
			select {
			case <-ctx.Done():
				return BlockedTerm{}, ctx.Err()
			case <-time.After(blockedTermRetries[i]):
			}
			continue
		}
		if err != nil {
			return BlockedTerm{}, blockedTermsError(err)
		}
		if len(out.Data) == 0 {
			return BlockedTerm{}, errors.New("twitch: an unreadable answer to adding a blocked term")
		}
		return out.Data[0].term(), nil
	}
}

// RemoveBlockedTerm removes a term by its id. Twitch answers the same for
// an id it doesn't have, so removing is safe to repeat.
func (s *Service) RemoveBlockedTerm(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: which term?", ErrInvalid)
	}
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}, "id": {id}}
	return blockedTermsError(s.streamerRaw(ctx, BlockedTermsScope, "DELETE", "/moderation/blocked_terms", q, nil, nil))
}

// busy is Twitch's "another change is pending, try again" (409, or 400
// naming a conflict).
func busy(err error) bool {
	var he *helixError
	return errors.As(err, &he) && (he.Status == 409 || (he.Status == 400 && strings.Contains(strings.ToLower(he.Message), "conflict")))
}

func blockedTermsError(err error) error {
	var he *helixError
	if errors.As(err, &he) && he.Status == 403 {
		return fmt.Errorf("twitch: Twitch refused it: %s", he.Message)
	}
	return channelError(err, "manage blocked terms")
}
