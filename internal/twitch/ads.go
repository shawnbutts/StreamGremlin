package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Ads (docs/adr/0024-ads.md): the schedule, starting a commercial and
// snoozing the next ad, as the streamer. Twitch offers no "ad ended" event,
// no eligibility check and no cancel. A request has three outcomes, never
// mixed up: accepted (Twitch took it: no ad is running yet), refused
// (nothing happened) and unknown (it may or may not have started). Neither
// request is ever retried: a second could start a second break.

// Ad scopes.
const (
	AdsReadScope        = "channel:read:ads"
	CommercialScope     = "channel:edit:commercial"
	AdsManageScope      = "channel:manage:ads"
	MaxCommercialLength = 180
)

// ErrAdOutcomeUnknown: the request was sent, and no clear answer came.
var ErrAdOutcomeUnknown = errors.New("twitch: Twitch didn't answer clearly, so the ad may or may not have started")

// AdRefusedError: Twitch answered and did nothing.
type AdRefusedError struct {
	Reason  string // cooldown, no_snoozes, not_live, not_eligible or refused
	Message string // Twitch's
}

func (e *AdRefusedError) Error() string {
	words := map[string]string{
		"cooldown":     "Twitch needs a break between ads",
		"no_snoozes":   "you have no snoozes left",
		"not_live":     "you need to be live",
		"not_eligible": "ads need an Affiliate or Partner channel",
	}[e.Reason]
	if words == "" {
		words = "Twitch refused it"
	}
	if e.Message != "" {
		return fmt.Sprintf("twitch: %s (%s)", words, e.Message)
	}
	return "twitch: " + words
}

// AdSchedule is Get Ad Schedule's answer. Times are nil when there's none
// (or the channel isn't live).
type AdSchedule struct {
	SnoozeCount     int        `json:"snooze_count"`
	SnoozeRefreshAt *time.Time `json:"snooze_refresh_at,omitempty"`
	NextAdAt        *time.Time `json:"next_ad_at,omitempty"`
	Duration        int        `json:"duration"`
	LastAdAt        *time.Time `json:"last_ad_at,omitempty"`
	PrerollFree     int        `json:"preroll_free_seconds"`
}

// adTime reads every documented shape of a time: RFC3339, Unix seconds as a
// number or a string, and "" or 0 for none. Anything else is an error.
func adTime(raw json.RawMessage) (*time.Time, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "0" || s == "null" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.UTC()
		return &t, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		t := time.Unix(n, 0).UTC()
		return &t, nil
	}
	return nil, fmt.Errorf("twitch: an ad time Twitch sent can't be read: %q", s)
}

// adInt reads a number or a numeric string.
func adInt(raw json.RawMessage) (int, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

func decodeSchedule(raw map[string]json.RawMessage) (AdSchedule, error) {
	var sc AdSchedule
	var err error
	if sc.SnoozeCount, err = adInt(raw["snooze_count"]); err != nil {
		return sc, err
	}
	if sc.Duration, err = adInt(raw["duration"]); err != nil {
		return sc, err
	}
	if sc.PrerollFree, err = adInt(raw["preroll_free_time"]); err != nil {
		return sc, err
	}
	for k, dst := range map[string]**time.Time{"snooze_refresh_at": &sc.SnoozeRefreshAt, "next_ad_at": &sc.NextAdAt, "last_ad_at": &sc.LastAdAt} {
		if *dst, err = adTime(raw[k]); err != nil {
			return sc, err
		}
	}
	return sc, nil
}

// Ads returns the ad schedule.
func (s *Service) Ads(ctx context.Context) (AdSchedule, error) {
	var out struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	err := s.streamerRaw(ctx, AdsReadScope, "GET", "/channels/ads", nil, nil, &out)
	if err != nil {
		return AdSchedule{}, adError(err, false)
	}
	if len(out.Data) == 0 {
		return AdSchedule{}, nil
	}
	return decodeSchedule(out.Data[0])
}

// CommercialResult is an accepted commercial: Twitch took it. It isn't
// "viewers are seeing an ad".
type CommercialResult struct {
	Length     int    `json:"length"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after"`
}

// StartCommercial asks Twitch for an ad break of length seconds (1 to 180).
// Sent once: never retried.
func (s *Service) StartCommercial(ctx context.Context, length int) (CommercialResult, error) {
	if length < 1 || length > MaxCommercialLength {
		return CommercialResult{}, fmt.Errorf("%w: an ad break is 1 to %d seconds", ErrInvalid, MaxCommercialLength)
	}
	var out struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	err := s.streamerRaw(ctx, CommercialScope, "POST", "/channels/commercial", nil, map[string]any{"length": length}, &out)
	if err != nil {
		return CommercialResult{}, adError(err, true)
	}
	if len(out.Data) == 0 {
		return CommercialResult{}, ErrAdOutcomeUnknown // an unreadable 200
	}
	d := out.Data[0]
	r := CommercialResult{}
	var e1, e2 error
	r.Length, e1 = adInt(d["length"])
	r.RetryAfter, e2 = adInt(d["retry_after"])
	_ = json.Unmarshal(d["message"], &r.Message)
	if e1 != nil || e2 != nil {
		return CommercialResult{}, ErrAdOutcomeUnknown
	}
	s.log.Info("twitch commercial accepted", "length", r.Length, "retry_after", r.RetryAfter)
	return r, nil
}

// SnoozeAd pushes the next automatic ad back 5 minutes, if a snooze is
// left. Sent once.
func (s *Service) SnoozeAd(ctx context.Context) (AdSchedule, error) {
	var out struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	err := s.streamerRaw(ctx, AdsManageScope, "POST", "/channels/ads/schedule/snooze", nil, nil, &out)
	if err != nil {
		return AdSchedule{}, adError(err, false)
	}
	if len(out.Data) == 0 {
		return AdSchedule{}, ErrAdOutcomeUnknown
	}
	sc, derr := decodeSchedule(out.Data[0])
	if derr != nil {
		return AdSchedule{}, ErrAdOutcomeUnknown
	}
	s.log.Info("twitch ad snoozed", "snoozes_left", sc.SnoozeCount)
	return sc, nil
}

// adError sorts an answer into refused (Twitch did nothing) or unknown.
// Errors from before the request left (sign-in, permission, a bad length)
// pass through: nothing was sent.
func adError(err error, commercial bool) error {
	var he *helixError
	if errors.As(err, &he) {
		msg := strings.ToLower(he.Message)
		r := &AdRefusedError{Message: he.Message, Reason: "refused"}
		switch {
		case he.Status == 429 && commercial:
			r.Reason = "cooldown"
		case he.Status == 429:
			r.Reason = "no_snoozes"
		case strings.Contains(msg, "live"):
			r.Reason = "not_live"
		case he.Status == 403 || strings.Contains(msg, "partner") || strings.Contains(msg, "affiliate"):
			r.Reason = "not_eligible"
		case strings.Contains(msg, "cooldown") || strings.Contains(msg, "wait"):
			r.Reason = "cooldown"
		case he.Status == 401:
			return err
		}
		if he.Status >= 400 && he.Status < 500 {
			return r
		}
	}
	if errors.Is(err, errUnreachable) || errors.Is(err, context.DeadlineExceeded) {
		return ErrAdOutcomeUnknown
	}
	return err
}
