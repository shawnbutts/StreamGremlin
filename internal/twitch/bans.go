package twitch

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ban lists (M8 slice 2, D-106, docs/adr/0049-ban-lists.md): looking
// accounts up 100 at a time, and banning and unbanning one at a time at
// Twitch's pace, as the streamer. Twitch has no bulk ban.

// MaxLookup is the most users one Get Users, or one user_id filter, takes.
const MaxLookup = 100

// RateInfo is Twitch's rate-limit bucket after a request, from its
// Ratelimit-* headers; Known is false when Twitch didn't send them.
type RateInfo struct {
	Known     bool
	Limit     int
	Remaining int
	Reset     time.Time
}

type rateKey struct{}

// noteRate fills the RateInfo a ban-list call put in its context.
func noteRate(ctx context.Context, resp *http.Response) {
	ri, ok := ctx.Value(rateKey{}).(*RateInfo)
	if !ok {
		return
	}
	lim, err1 := strconv.Atoi(resp.Header.Get("Ratelimit-Limit"))
	rem, err2 := strconv.Atoi(resp.Header.Get("Ratelimit-Remaining"))
	reset, err3 := strconv.ParseInt(resp.Header.Get("Ratelimit-Reset"), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	*ri = RateInfo{Known: true, Limit: lim, Remaining: rem, Reset: time.Unix(reset, 0)}
}

func withRate(ctx context.Context) (context.Context, *RateInfo) {
	ri := &RateInfo{}
	return context.WithValue(ctx, rateKey{}, ri), ri
}

// User is an account Twitch found.
type User struct {
	ID    string `json:"id"`
	Login string `json:"login"`
	// CreatedAt is when the account was made (the raid guard, D-107).
	CreatedAt time.Time `json:"created_at"`
}

// LookUpUsers finds up to MaxLookup accounts by login and id together.
// Deleted and suspended accounts, and names that don't exist, aren't in
// the answer.
func (s *Service) LookUpUsers(ctx context.Context, logins, ids []string) ([]User, RateInfo, error) {
	ctx, ri := withRate(ctx)
	if len(logins)+len(ids) == 0 {
		return nil, *ri, nil
	}
	if len(logins)+len(ids) > MaxLookup {
		return nil, *ri, ErrInvalid
	}
	q := url.Values{}
	for _, l := range logins {
		q.Add("login", l)
	}
	for _, id := range ids {
		q.Add("id", id)
	}
	var out struct {
		Data []User `json:"data"`
	}
	err := s.streamerUsers(ctx, q, &out)
	return out.Data, *ri, err
}

// streamerUsers is Get Users as the streamer, without the broadcaster_id
// streamerRaw adds.
func (s *Service) streamerUsers(ctx context.Context, q url.Values, out any) error {
	st := s.streamer.Status()
	if st.UserID == "" || (st.State != StateConnected && st.State != StateUnknown) {
		return errStreamerSignIn
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return err
	}
	clientID, _ := s.ClientID(ctx)
	if clientID == "" {
		return ErrNoClientID
	}
	return s.helix.call(ctx, "GET", "/users?"+q.Encode(), clientID, tok.Reveal(), nil, out)
}

// BannedAmong asks which of the ids are banned for good.
func (s *Service) BannedAmong(ctx context.Context, ids []string) (map[string]bool, RateInfo, error) {
	ctx, ri := withRate(ctx)
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, *ri, nil
	}
	var resp struct {
		Data []struct {
			UserID    string `json:"user_id"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	if err := s.streamerRaw(ctx, BansScope, "GET", "/moderation/banned", url.Values{"user_id": ids}, nil, &resp); err != nil {
		return nil, *ri, err
	}
	for _, d := range resp.Data {
		if d.ExpiresAt == "" {
			out[d.UserID] = true
		}
	}
	return out, *ri, nil
}

// RolesAmong asks which of the ids are your moderators and VIPs.
func (s *Service) RolesAmong(ctx context.Context, ids []string) (mods, vips map[string]bool, ri RateInfo, err error) {
	mods, vips = map[string]bool{}, map[string]bool{}
	if len(ids) == 0 {
		return mods, vips, ri, nil
	}
	for _, x := range []struct {
		scope, path string
		into        map[string]bool
	}{{ModerationReadScope, "/moderation/moderators", mods}, {VIPsReadScope, "/channels/vips", vips}} {
		c, r := withRate(ctx)
		var resp struct {
			Data []struct {
				UserID string `json:"user_id"`
			} `json:"data"`
		}
		if err := s.streamerRaw(c, x.scope, "GET", x.path, url.Values{"user_id": ids}, nil, &resp); err != nil {
			return nil, nil, *r, err
		}
		for _, d := range resp.Data {
			x.into[d.UserID] = true
		}
		ri = *r
	}
	return mods, vips, ri, nil
}

// BannedUser is one ban on Twitch's list.
type BannedUser struct {
	UserID string `json:"user_id"`
	Login  string `json:"user_login"`
}

// BansPage reads a page of your channel's permanent bans (100 at most);
// next is "" after the last page.
func (s *Service) BansPage(ctx context.Context, after string) ([]BannedUser, string, RateInfo, error) {
	ctx, ri := withRate(ctx)
	q := url.Values{"first": {"100"}}
	if after != "" {
		q.Set("after", after)
	}
	var resp struct {
		Data []struct {
			BannedUser
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
		Pagination struct {
			Cursor string `json:"cursor"`
		} `json:"pagination"`
	}
	if err := s.streamerRaw(ctx, BansScope, "GET", "/moderation/banned", q, nil, &resp); err != nil {
		return nil, "", *ri, err
	}
	out := []BannedUser{}
	for _, d := range resp.Data {
		if d.ExpiresAt == "" {
			out = append(out, d.BannedUser)
		}
	}
	return out, resp.Pagination.Cursor, *ri, nil
}

// ListOutcome is how one ban-list request went.
type ListOutcome string

const (
	ListDone      ListOutcome = "done"
	ListAlready   ListOutcome = "already"    // banned already: nothing to do
	ListNotBanned ListOutcome = "not_banned" // an unban of someone not banned
	ListRefused   ListOutcome = "refused"    // Twitch refused this account
	ListStop      ListOutcome = "stop"       // a refusal that would refuse every account: sign-in, permission
	ListWait      ListOutcome = "wait"       // too many requests: wait until Reset
	ListUnknown   ListOutcome = "unknown"    // no answer: it may or may not have happened
)

func listOutcome(err error) (ListOutcome, string) {
	if err == nil {
		return ListDone, ""
	}
	switch {
	case errors.Is(err, errStreamerSignIn), errors.Is(err, ErrNeedsPermission), errors.Is(err, ErrNoClientID):
		return ListStop, modOutcome(err).Detail
	}
	var he *helixError
	if errors.As(err, &he) {
		low := strings.ToLower(he.Message)
		switch {
		case he.Status == 429:
			return ListWait, "Twitch says to wait: " + he.Message
		case he.Status == 401 || he.Status == 403:
			return ListStop, modOutcome(err).Detail
		case strings.Contains(low, "already banned"):
			return ListAlready, ""
		case strings.Contains(low, "not banned"):
			return ListNotBanned, ""
		}
		return ListRefused, he.Message
	}
	return ListUnknown, "Twitch didn't answer, so it may or may not have happened."
}

// ListBan bans an account for good with a reason, once (never retried).
func (s *Service) ListBan(ctx context.Context, userID, reason string) (ListOutcome, string, RateInfo) {
	ctx, ri := withRate(ctx)
	if len([]rune(reason)) > 500 {
		reason = string([]rune(reason)[:500])
	}
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}}
	body := map[string]any{"data": map[string]any{"user_id": userID, "reason": reason}}
	err := s.streamerRawQuery(ctx, BansScope, "POST", "/moderation/bans", q, body, nil)
	o, d := listOutcome(err)
	return o, d, *ri
}

// ListUnban lifts an account's ban, once.
func (s *Service) ListUnban(ctx context.Context, userID string) (ListOutcome, string, RateInfo) {
	ctx, ri := withRate(ctx)
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}, "user_id": {userID}}
	err := s.streamerRawQuery(ctx, BansScope, "DELETE", "/moderation/bans", q, nil, nil)
	o, d := listOutcome(err)
	return o, d, *ri
}

// streamerRawQuery is streamerRaw with broadcaster_id always in the query,
// whatever the body.
func (s *Service) streamerRawQuery(ctx context.Context, scope, method, path string, q url.Values, body, out any) error {
	st := s.streamer.Status()
	if st.UserID == "" || (st.State != StateConnected && st.State != StateUnknown) {
		return errStreamerSignIn
	}
	if scope != "" && !containsScope(st.Scopes, scope) {
		return ErrNeedsPermission
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return err
	}
	clientID, _ := s.ClientID(ctx)
	if clientID == "" {
		return ErrNoClientID
	}
	q.Set("broadcaster_id", st.UserID)
	return s.helix.call(ctx, method, path+"?"+q.Encode(), clientID, tok.Reveal(), body, out)
}

func containsScope(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// StreamerID is your channel's user id, when signed in.
func (s *Service) StreamerID() string { return s.streamer.Status().UserID }

// accountCache remembers when accounts were made (an account's age never
// changes), for the New accounts filter (D-108).
type accountCache struct {
	mu   sync.Mutex
	made map[string]time.Time
}

const maxAccountCache = 50_000

// AccountMade is AccountCreated asked once per account and remembered;
// false when Twitch couldn't say.
func (s *Service) AccountMade(ctx context.Context, userID string) (time.Time, bool) {
	s.madeCache.mu.Lock()
	t, ok := s.madeCache.made[userID]
	s.madeCache.mu.Unlock()
	if ok {
		return t, true
	}
	t, err := s.AccountCreated(ctx, userID)
	if err != nil {
		return time.Time{}, false
	}
	s.madeCache.mu.Lock()
	if s.madeCache.made == nil || len(s.madeCache.made) >= maxAccountCache {
		s.madeCache.made = map[string]time.Time{}
	}
	s.madeCache.made[userID] = t
	s.madeCache.mu.Unlock()
	return t, true
}

// BlockScope lets StreamGremlin block and unblock accounts as you (the
// follow-bot review, D-109).
const BlockScope = "user:manage:blocked_users"

// ListBlock blocks an account as you, once: they can't follow, whisper or
// chat with you. Blocking again is harmless (Twitch replaces the block).
// Whether a block removes a follow already made isn't documented.
func (s *Service) ListBlock(ctx context.Context, userID string) (ListOutcome, string, RateInfo) {
	ctx, ri := withRate(ctx)
	q := url.Values{"target_user_id": {userID}, "source_context": {"chat"}, "reason": {"spam"}}
	o, d := listOutcome(s.userCall(ctx, BlockScope, "PUT", "/users/blocks", q))
	return o, d, *ri
}

// ListUnblock lifts a block, once.
func (s *Service) ListUnblock(ctx context.Context, userID string) (ListOutcome, string, RateInfo) {
	ctx, ri := withRate(ctx)
	o, d := listOutcome(s.userCall(ctx, BlockScope, "DELETE", "/users/blocks", url.Values{"target_user_id": {userID}}))
	if o == ListNotBanned {
		o = ListDone
	}
	return o, d, *ri
}

// userCall is a request as you about your own account, without the
// broadcaster_id the moderation calls send.
func (s *Service) userCall(ctx context.Context, scope, method, path string, q url.Values) error {
	st := s.streamer.Status()
	if st.UserID == "" || (st.State != StateConnected && st.State != StateUnknown) {
		return errStreamerSignIn
	}
	if scope != "" && !containsScope(st.Scopes, scope) {
		return ErrNeedsPermission
	}
	tok, err := s.streamer.AccessToken(ctx)
	if err != nil {
		return err
	}
	clientID, _ := s.ClientID(ctx)
	if clientID == "" {
		return ErrNoClientID
	}
	return s.helix.call(ctx, method, path+"?"+q.Encode(), clientID, tok.Reveal(), nil, nil)
}
