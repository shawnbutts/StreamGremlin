package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// apiError is a 4xx answer from the Data API.
type apiError struct {
	Status  int
	Reason  string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("youtube: YouTube refused: %s (%s, %d)", e.Message, e.Reason, e.Status)
}

// errAuth means the access token was refused (401).
var errAuth = errors.New("youtube: YouTube refused the sign-in")

// data calls the Data API, counting each call's quota first.
type data struct {
	base  string
	http  *http.Client
	quota *quota
}

// get asks for path with q, spending cost units, as token's account.
func (d *data) get(ctx context.Context, token, path string, q url.Values, cost int, out any) error {
	if err := d.quota.spend(ctx, cost); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", d.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", errUnreachable, describeNetErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreachable, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errAuth
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: YouTube answered %d", errUnreachable, resp.StatusCode)
	case resp.StatusCode >= 400:
		var e struct {
			Error struct {
				Message string `json:"message"`
				Errors  []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		ae := &apiError{Status: resp.StatusCode, Message: e.Error.Message}
		if len(e.Error.Errors) > 0 {
			ae.Reason = e.Error.Errors[0].Reason
		}
		if ae.Reason == "quotaExceeded" || ae.Reason == "dailyLimitExceeded" {
			d.quota.exhausted(ctx)
			return ErrQuotaExhausted
		}
		return ae
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: an answer that isn't JSON", errUnreachable)
	}
	return nil
}

// Channel is a signed-in account's YouTube channel.
type Channel struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Handle string `json:"handle,omitempty"` // such as "@streamer"
}

// errNoChannel means the Google account (or brand account) picked has no
// YouTube channel.
var errNoChannel = errors.New("youtube: no channel")

// myChannel is channels.list with mine=true: the signed-in channel.
func (d *data) myChannel(ctx context.Context, token string) (Channel, error) {
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title     string `json:"title"`
				CustomURL string `json:"customUrl"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := d.get(ctx, token, "/channels", url.Values{"part": {"snippet"}, "mine": {"true"}}, CostChannelsList, &out); err != nil {
		return Channel{}, err
	}
	if len(out.Items) == 0 || out.Items[0].ID == "" {
		return Channel{}, errNoChannel
	}
	it := out.Items[0]
	return Channel{ID: it.ID, Title: it.Snippet.Title, Handle: it.Snippet.CustomURL}, nil
}

func newData(base string, q *quota) *data {
	return &data{base: base, http: &http.Client{Timeout: 15 * time.Second}, quota: q}
}

// Broadcast is the streamer's live broadcast, whose chat StreamGremlin reads.
type Broadcast struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	LiveChatID string    `json:"-"`
	StartedAt  time.Time `json:"started_at,omitzero"`
}

// activeBroadcasts is liveBroadcasts.list with broadcastStatus=active and
// broadcastType=all: the signed-in channel's live broadcasts, its default
// (persistent) one included. Google allows only one of mine, id and
// broadcastStatus; broadcastStatus asks about the signed-in channel.
func (d *data) activeBroadcasts(ctx context.Context, token string) ([]Broadcast, error) {
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title           string `json:"title"`
				LiveChatID      string `json:"liveChatId"`
				ActualStartTime string `json:"actualStartTime"`
				ActualEndTime   string `json:"actualEndTime"`
			} `json:"snippet"`
			Status struct {
				LifeCycleStatus string `json:"lifeCycleStatus"`
			} `json:"status"`
		} `json:"items"`
	}
	q := url.Values{"part": {"snippet,status"}, "broadcastStatus": {"active"}, "broadcastType": {"all"}, "maxResults": {"10"}}
	if err := d.get(ctx, token, "/liveBroadcasts", q, CostBroadcastsList, &out); err != nil {
		return nil, err
	}
	var bs []Broadcast
	for _, it := range out.Items {
		// live, or liveStarting; never testing (a preview nobody sees) or ended.
		live := it.Status.LifeCycleStatus == "live" || it.Status.LifeCycleStatus == "liveStarting"
		if !live || it.Snippet.LiveChatID == "" || it.Snippet.ActualEndTime != "" {
			continue
		}
		b := Broadcast{ID: it.ID, Title: it.Snippet.Title, LiveChatID: it.Snippet.LiveChatID}
		if t, err := time.Parse(time.RFC3339, it.Snippet.ActualStartTime); err == nil {
			b.StartedAt = t
		}
		bs = append(bs, b)
	}
	return bs, nil
}
