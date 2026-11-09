package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Channel point rewards (docs/adr/0018-channel-point-rewards.md). Twitch
// is the only record: StreamGremlin keeps no copy. Only the app (Client
// ID) that made a reward may change or delete it, or complete or refund
// its redemptions, so rewards made on Twitch's site are "trigger only".

// ManageScope lets StreamGremlin make and change rewards and redemptions.
const ManageScope = "channel:manage:redemptions"

// Errors about rewards.
var (
	// ErrNoChannelPoints: Twitch gives channel points to Affiliates and
	// Partners only.
	ErrNoChannelPoints = errors.New("twitch: channel points need an Affiliate or Partner channel; Twitch says yours isn't one yet")
	// ErrNotManaged: the reward was made elsewhere (on Twitch's site, or by
	// another app), so Twitch lets only that app change it.
	ErrNotManaged = errors.New("twitch: only rewards made in StreamGremlin can be changed, deleted, completed or refunded here; " +
		"this one was made on Twitch or by another app")
	// ErrRewardNotFound: no such reward (deleted on Twitch?).
	ErrRewardNotFound = errors.New("twitch: there's no such reward on your channel")
	// ErrNeedsManage: the sign-in doesn't allow managing rewards.
	ErrNeedsManage = errors.New("twitch: sign in to Twitch again and allow managing channel point rewards (" + ManageScope + ")")
	// ErrAlreadyHandled: the redemption was completed or refunded already,
	// the other way, so it can't be changed now.
	ErrAlreadyHandled = errors.New("twitch: that redemption was already handled")
)

// errRewardsSignIn is ErrNotSignedIn, said for rewards.
var errRewardsSignIn = signInError{"twitch: sign in with your Twitch account to use channel point rewards"}

type signInError struct{ msg string }

func (e signInError) Error() string { return e.msg }
func (e signInError) Unwrap() error { return ErrNotSignedIn }

// Limits from Twitch's API reference (checked 2026-11-09).
const (
	MaxRewards          = 50
	MaxRewardTitle      = 45
	MaxRewardPrompt     = 200
	MaxRewardCooldown   = 604800 // seconds (Update Custom Reward's stated maximum)
	maxRewardCostWeTake = 1_000_000_000
)

var rewardColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// Reward is one channel point reward.
type Reward struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	Cost            int    `json:"cost"`
	BackgroundColor string `json:"background_color"`
	Enabled         bool   `json:"enabled"`
	Paused          bool   `json:"paused"`
	InStock         bool   `json:"in_stock"`
	InputRequired   bool   `json:"input_required"`
	// SkipQueue: redemptions are done at once, so they can't be refunded.
	SkipQueue bool `json:"skip_queue"`
	// Limits: 0 is none.
	MaxPerStream        int `json:"max_per_stream"`
	MaxPerUserPerStream int `json:"max_per_user_per_stream"`
	CooldownSeconds     int `json:"cooldown_seconds"`
	// Managed: made by StreamGremlin's Client ID, so StreamGremlin may
	// change it and complete or refund its redemptions.
	Managed            bool       `json:"managed"`
	RedeemedThisStream *int       `json:"redeemed_this_stream,omitempty"`
	CooldownUntil      *time.Time `json:"cooldown_until,omitempty"`
}

// RewardChange is what to set on a reward; nil fields stay as they are.
// Creating needs Title and Cost.
type RewardChange struct {
	Title               *string `json:"title,omitempty"`
	Cost                *int    `json:"cost,omitempty"`
	Prompt              *string `json:"prompt,omitempty"`
	BackgroundColor     *string `json:"background_color,omitempty"`
	Enabled             *bool   `json:"enabled,omitempty"`
	Paused              *bool   `json:"paused,omitempty"`
	InputRequired       *bool   `json:"input_required,omitempty"`
	SkipQueue           *bool   `json:"skip_queue,omitempty"`
	MaxPerStream        *int    `json:"max_per_stream,omitempty"`          // 0 turns it off
	MaxPerUserPerStream *int    `json:"max_per_user_per_stream,omitempty"` // 0 turns it off
	CooldownSeconds     *int    `json:"cooldown_seconds,omitempty"`        // 0 turns it off
}

// Check reports what's wrong with a change, in words.
func (c RewardChange) Check() error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }
	switch {
	case c.Title != nil && (strings.TrimSpace(*c.Title) == "" || utf8.RuneCountInString(*c.Title) > MaxRewardTitle):
		return bad("a reward's title is 1 to %d characters", MaxRewardTitle)
	case c.Cost != nil && (*c.Cost < 1 || *c.Cost > maxRewardCostWeTake):
		return bad("a reward costs at least 1 point")
	case c.Prompt != nil && utf8.RuneCountInString(*c.Prompt) > MaxRewardPrompt:
		return bad("a reward's description is up to %d characters", MaxRewardPrompt)
	case c.BackgroundColor != nil && !rewardColor.MatchString(*c.BackgroundColor):
		return bad("a reward's color is like #9147FF")
	case c.MaxPerStream != nil && *c.MaxPerStream < 0, c.MaxPerUserPerStream != nil && *c.MaxPerUserPerStream < 0:
		return bad("a limit is 0 (none) or more")
	case c.CooldownSeconds != nil && (*c.CooldownSeconds < 0 || *c.CooldownSeconds > MaxRewardCooldown):
		return bad("a cooldown is 0 (none) to %d seconds (a week)", MaxRewardCooldown)
	}
	return nil
}

// body is the change as Twitch's Create and Update bodies.
func (c RewardChange) body() map[string]any {
	b := map[string]any{}
	if c.Title != nil {
		b["title"] = strings.TrimSpace(*c.Title)
	}
	if c.Cost != nil {
		b["cost"] = *c.Cost
	}
	if c.Prompt != nil {
		b["prompt"] = *c.Prompt
	}
	if c.BackgroundColor != nil {
		b["background_color"] = *c.BackgroundColor
	}
	for k, v := range map[string]*bool{"is_enabled": c.Enabled, "is_paused": c.Paused,
		"is_user_input_required": c.InputRequired, "should_redemptions_skip_request_queue": c.SkipQueue} {
		if v != nil {
			b[k] = *v
		}
	}
	limit := func(on, val string, v *int) {
		if v != nil {
			b[on] = *v > 0
			if *v > 0 {
				b[val] = *v
			}
		}
	}
	limit("is_max_per_stream_enabled", "max_per_stream", c.MaxPerStream)
	limit("is_max_per_user_per_stream_enabled", "max_per_user_per_stream", c.MaxPerUserPerStream)
	limit("is_global_cooldown_enabled", "global_cooldown_seconds", c.CooldownSeconds)
	return b
}

// helixReward is a reward as Twitch answers it.
type helixReward struct {
	ID                  string `json:"id"`
	Title               string `json:"title"`
	Prompt              string `json:"prompt"`
	Cost                int    `json:"cost"`
	BackgroundColor     string `json:"background_color"`
	IsEnabled           bool   `json:"is_enabled"`
	IsPaused            bool   `json:"is_paused"`
	IsInStock           bool   `json:"is_in_stock"`
	IsUserInputRequired bool   `json:"is_user_input_required"`
	SkipQueue           bool   `json:"should_redemptions_skip_request_queue"`
	MaxPerStream        struct {
		IsEnabled    bool `json:"is_enabled"`
		MaxPerStream int  `json:"max_per_stream"`
	} `json:"max_per_stream_setting"`
	MaxPerUser struct {
		IsEnabled           bool `json:"is_enabled"`
		MaxPerUserPerStream int  `json:"max_per_user_per_stream"`
	} `json:"max_per_user_per_stream_setting"`
	Cooldown struct {
		IsEnabled             bool `json:"is_enabled"`
		GlobalCooldownSeconds int  `json:"global_cooldown_seconds"`
	} `json:"global_cooldown_setting"`
	RedeemedThisStream *int       `json:"redemptions_redeemed_current_stream"`
	CooldownExpiresAt  *time.Time `json:"cooldown_expires_at"`
}

func (h helixReward) reward(managed bool) Reward {
	r := Reward{ID: h.ID, Title: h.Title, Prompt: h.Prompt, Cost: h.Cost, BackgroundColor: h.BackgroundColor, Enabled: h.IsEnabled,
		Paused: h.IsPaused, InStock: h.IsInStock, InputRequired: h.IsUserInputRequired, SkipQueue: h.SkipQueue, Managed: managed,
		RedeemedThisStream: h.RedeemedThisStream, CooldownUntil: h.CooldownExpiresAt}
	if h.MaxPerStream.IsEnabled {
		r.MaxPerStream = h.MaxPerStream.MaxPerStream
	}
	if h.MaxPerUser.IsEnabled {
		r.MaxPerUserPerStream = h.MaxPerUser.MaxPerUserPerStream
	}
	if h.Cooldown.IsEnabled {
		r.CooldownSeconds = h.Cooldown.GlobalCooldownSeconds
	}
	return r
}

// Redemption is one viewer's redemption of a reward.
type Redemption struct {
	ID         string    `json:"id"`
	RewardID   string    `json:"reward_id"`
	Reward     string    `json:"reward"` // its title
	Cost       int       `json:"cost"`
	UserID     string    `json:"user_id"`
	UserLogin  string    `json:"user_login"`
	UserName   string    `json:"user_name"`
	Input      string    `json:"input"`
	Status     string    `json:"status"` // unfulfilled, fulfilled or canceled
	RedeemedAt time.Time `json:"redeemed_at"`
}

type helixRedemption struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	UserLogin  string    `json:"user_login"`
	UserName   string    `json:"user_name"`
	UserInput  string    `json:"user_input"`
	Status     string    `json:"status"`
	RedeemedAt time.Time `json:"redeemed_at"`
	Reward     struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Cost  int    `json:"cost"`
	} `json:"reward"`
}

func (h helixRedemption) redemption() Redemption {
	return Redemption{ID: h.ID, RewardID: h.Reward.ID, Reward: h.Reward.Title, Cost: h.Reward.Cost, UserID: h.UserID, UserLogin: h.UserLogin,
		UserName: h.UserName, Input: h.UserInput, Status: strings.ToLower(h.Status), RedeemedAt: h.RedeemedAt}
}

// rewardsCall makes a reward request as the streamer, after checking the
// sign-in allows it, and puts Twitch's refusals in words.
func (s *Service) rewardsCall(ctx context.Context, needManage bool, method, path string, q url.Values, body, out any) error {
	st := s.streamer.Status()
	if st.UserID == "" || (st.State != StateConnected && st.State != StateUnknown) {
		return errRewardsSignIn
	}
	if !slices.Contains(st.Scopes, ManageScope) && (needManage || !slices.Contains(st.Scopes, "channel:read:redemptions")) {
		return ErrNeedsManage
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
	q.Set("broadcaster_id", st.UserID)
	err = s.helix.call(ctx, method, path+"?"+q.Encode(), clientID, tok.Reveal(), body, out)
	var he *helixError
	if errors.As(err, &he) {
		switch {
		case he.Status == 403 && strings.Contains(strings.ToLower(he.Message), "partner or affiliate"):
			return ErrNoChannelPoints
		case he.Status == 403:
			return ErrNotManaged
		case he.Status == 401 && strings.Contains(strings.ToLower(he.Message), "scope"):
			return ErrNeedsManage
		case he.Status == 400:
			return fmt.Errorf("%w: Twitch refused it: %s", ErrInvalid, rewardWords(he.Message))
		}
	}
	return err
}

// rewardWords turns Twitch's codes into words.
func rewardWords(msg string) string {
	if strings.Contains(msg, "DUPLICATE_REWARD") {
		return "another reward on your channel has that title"
	}
	return msg
}

// Rewards lists the channel's rewards, marking the ones StreamGremlin
// manages, by title.
func (s *Service) Rewards(ctx context.Context) ([]Reward, error) {
	var all, mine struct {
		Data []helixReward `json:"data"`
	}
	if err := s.rewardsCall(ctx, false, "GET", "/channel_points/custom_rewards", nil, nil, &all); err != nil {
		return nil, err
	}
	if err := s.rewardsCall(ctx, false, "GET", "/channel_points/custom_rewards", url.Values{"only_manageable_rewards": {"true"}}, nil, &mine); err != nil {
		return nil, err
	}
	managed := map[string]bool{}
	for _, r := range mine.Data {
		managed[r.ID] = true
	}
	out := make([]Reward, 0, len(all.Data))
	for _, r := range all.Data {
		out = append(out, r.reward(managed[r.ID]))
	}
	slices.SortFunc(out, func(a, b Reward) int { return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)) })
	return out, nil
}

// Reward finds one reward by id or, failing that, by title (any case).
func (s *Service) Reward(ctx context.Context, idOrTitle string) (Reward, error) {
	list, err := s.Rewards(ctx)
	if err != nil {
		return Reward{}, err
	}
	for _, r := range list {
		if r.ID == idOrTitle {
			return r, nil
		}
	}
	for _, r := range list {
		if strings.EqualFold(r.Title, strings.TrimSpace(idOrTitle)) {
			return r, nil
		}
	}
	return Reward{}, ErrRewardNotFound
}

// CreateReward makes a reward StreamGremlin manages. Asking again after an
// unclear answer is harmless: a reward StreamGremlin made with that title
// and cost is the one asked for (created is false).
func (s *Service) CreateReward(ctx context.Context, c RewardChange) (r Reward, created bool, err error) {
	if c.Title == nil || c.Cost == nil {
		return Reward{}, false, fmt.Errorf("%w: a reward needs a title and a cost", ErrInvalid)
	}
	if err := c.Check(); err != nil {
		return Reward{}, false, err
	}
	var out struct {
		Data []helixReward `json:"data"`
	}
	err = s.rewardsCall(ctx, true, "POST", "/channel_points/custom_rewards", nil, c.body(), &out)
	if errors.Is(err, ErrInvalid) && strings.Contains(err.Error(), "has that title") {
		if same, ferr := s.Reward(ctx, *c.Title); ferr == nil && same.Managed && same.Cost == *c.Cost {
			return same, false, nil
		}
	}
	if err != nil {
		return Reward{}, false, err
	}
	if len(out.Data) == 0 {
		return Reward{}, false, fmt.Errorf("%w: Twitch didn't return the reward", errUnreachable)
	}
	s.log.Info("twitch reward created", "reward", out.Data[0].ID)
	return out.Data[0].reward(true), true, nil
}

// UpdateReward changes a reward StreamGremlin manages. Repeating a change
// is harmless.
func (s *Service) UpdateReward(ctx context.Context, id string, c RewardChange) (Reward, error) {
	if err := c.Check(); err != nil {
		return Reward{}, err
	}
	var out struct {
		Data []helixReward `json:"data"`
	}
	err := s.rewardsCall(ctx, true, "PATCH", "/channel_points/custom_rewards", url.Values{"id": {id}}, c.body(), &out)
	if isHelix(err, 404) {
		return Reward{}, ErrRewardNotFound
	}
	if err != nil {
		return Reward{}, err
	}
	if len(out.Data) == 0 {
		return Reward{}, fmt.Errorf("%w: Twitch didn't return the reward", errUnreachable)
	}
	return out.Data[0].reward(true), nil
}

// DeleteReward deletes a reward StreamGremlin manages. Deleting one that's
// gone is harmless (false). Twitch marks its waiting redemptions done.
func (s *Service) DeleteReward(ctx context.Context, id string) (bool, error) {
	err := s.rewardsCall(ctx, true, "DELETE", "/channel_points/custom_rewards", url.Values{"id": {id}}, nil, nil)
	if isHelix(err, 404) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.log.Info("twitch reward deleted", "reward", id)
	return true, nil
}

// Redemptions lists a managed reward's redemptions waiting in the queue,
// oldest first (up to 50).
func (s *Service) Redemptions(ctx context.Context, rewardID string) ([]Redemption, error) {
	var out struct {
		Data []helixRedemption `json:"data"`
	}
	q := url.Values{"reward_id": {rewardID}, "status": {"UNFULFILLED"}, "sort": {"OLDEST"}, "first": {"50"}}
	err := s.rewardsCall(ctx, false, "GET", "/channel_points/custom_rewards/redemptions", q, nil, &out)
	if isHelix(err, 404) {
		return nil, ErrRewardNotFound
	}
	if err != nil {
		return nil, err
	}
	list := make([]Redemption, 0, len(out.Data))
	for _, d := range out.Data {
		list = append(list, d.redemption())
	}
	return list, nil
}

// SetRedemption completes a redemption (done) or refunds it (the viewer
// gets the points back). Asking again for the same is harmless (repeated
// is true); asking for the other after it was handled says so.
func (s *Service) SetRedemption(ctx context.Context, rewardID, redemptionID string, done bool) (Redemption, bool, error) {
	want := "CANCELED"
	if done {
		want = "FULFILLED"
	}
	var out struct {
		Data []helixRedemption `json:"data"`
	}
	q := url.Values{"reward_id": {rewardID}, "id": {redemptionID}}
	err := s.rewardsCall(ctx, true, "PATCH", "/channel_points/custom_rewards/redemptions", q, map[string]string{"status": want}, &out)
	if isHelix(err, 404) {
		// Not waiting any more: see how it was handled.
		var now struct {
			Data []helixRedemption `json:"data"`
		}
		gerr := s.rewardsCall(ctx, false, "GET", "/channel_points/custom_rewards/redemptions", url.Values{"reward_id": {rewardID}, "id": {redemptionID}}, nil, &now)
		switch {
		case isHelix(gerr, 404) || (gerr == nil && len(now.Data) == 0):
			return Redemption{}, false, fmt.Errorf("twitch: there's no such redemption waiting (Twitch keeps handled ones only a few days)")
		case gerr != nil:
			return Redemption{}, false, gerr
		case now.Data[0].Status == want:
			return now.Data[0].redemption(), true, nil
		default:
			return now.Data[0].redemption(), false, fmt.Errorf("%w: it's %s", ErrAlreadyHandled, map[string]string{
				"FULFILLED": "done", "CANCELED": "refunded"}[now.Data[0].Status])
		}
	}
	if err != nil {
		return Redemption{}, false, err
	}
	if len(out.Data) == 0 {
		return Redemption{}, false, fmt.Errorf("%w: Twitch didn't return the redemption", errUnreachable)
	}
	s.log.Info("twitch redemption updated", "reward", rewardID, "status", want)
	return out.Data[0].redemption(), false, nil
}
