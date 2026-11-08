package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

// Role says which account a sign-in is for.
type Role string

const (
	// RoleStreamer is the channel's own account: it reads chat and events.
	RoleStreamer Role = "streamer"
	// RoleBot is an optional second account that sends chat.
	RoleBot Role = "bot"
)

// State is one account's connection state. Only Connected means Twitch
// confirmed the sign-in recently; Unknown never counts as signed in.
type State string

const (
	StateNotConfigured State = "not_configured" // no Client ID yet
	StateSignedOut     State = "signed_out"
	StateSigningIn     State = "signing_in"    // waiting for the code to be entered
	StateConnected     State = "connected"     // Twitch confirmed the token
	StateNeedsSignIn   State = "needs_sign_in" // the sign-in ended; sign in again
	StateUnknown       State = "unknown"       // couldn't ask Twitch
)

// refreshAhead refreshes an access token this long before it expires.
const refreshAhead = 10 * time.Minute

// PendingSignIn is what the streamer needs to finish a sign-in.
type PendingSignIn struct {
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// AccountStatus describes one account. It never holds tokens.
type AccountStatus struct {
	Role          Role           `json:"role"`
	State         State          `json:"state"`
	Login         string         `json:"login,omitempty"`
	UserID        string         `json:"user_id,omitempty"`
	Scopes        []string       `json:"scopes"`
	MissingScopes []string       `json:"missing_scopes"`
	Detail        string         `json:"detail,omitempty"`
	Since         time.Time      `json:"since"`
	CheckedAt     *time.Time     `json:"checked_at,omitempty"`
	SignIn        *PendingSignIn `json:"sign_in,omitempty"`
}

// tokens is the secret saved for a signed-in account, as one JSON value so
// a refresh replaces it in one write.
type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes"`
	UserID       string    `json:"user_id"`
	Login        string    `json:"login"`
	ClientID     string    `json:"client_id"`
	SignedInAt   time.Time `json:"signed_in_at"`
}

// LogValue keeps tokens out of logs even if one is passed by mistake.
func (t tokens) LogValue() slog.Value {
	return slog.GroupValue(slog.String("login", t.Login), slog.String("user_id", t.UserID))
}

// Account is one Twitch sign-in. Network calls happen outside mu; op keeps
// sign-in, refresh, validation and sign-out from interleaving.
type Account struct {
	role   Role
	scopes []string
	svc    *Service

	op sync.Mutex

	mu      sync.Mutex
	status  AccountStatus
	tok     *tokens
	cancel  context.CancelFunc // a pending sign-in
	pending uint64             // which sign-in is current
}

func newAccount(svc *Service, role Role, scopes []string) *Account {
	a := &Account{role: role, scopes: scopes, svc: svc}
	a.status = AccountStatus{Role: role, State: StateSignedOut, Scopes: []string{}, MissingScopes: []string{}, Since: svc.now()}
	return a
}

func (a *Account) secretKey() string { return "twitch/" + string(a.role) + "/oauth" }

// Status returns the account's state.
func (a *Account) Status() AccountStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.status
	s.Scopes = slices.Clone(s.Scopes)
	s.MissingScopes = slices.Clone(s.MissingScopes)
	return s
}

// setStatus changes the state, keeping Since when the state is unchanged.
// Call with mu held.
func (a *Account) setStatus(st State, detail string) {
	if st != a.status.State {
		a.status.Since = a.svc.now()
		a.svc.log.Info("twitch account state", "role", string(a.role), "state", string(st), "detail", detail)
	}
	a.status.State = st
	a.status.Detail = detail
	if st != StateSigningIn {
		a.status.SignIn = nil
	}
	a.svc.changed()
}

func (a *Account) setTokens(t *tokens) {
	a.tok = t
	if t == nil {
		a.status.Login, a.status.UserID = "", ""
		a.status.Scopes, a.status.MissingScopes = []string{}, []string{}
		return
	}
	a.status.Login, a.status.UserID = t.Login, t.UserID
	a.status.Scopes = slices.Clone(t.Scopes)
	a.status.MissingScopes = missing(a.scopes, t.Scopes)
}

func missing(want, have []string) []string {
	out := []string{}
	for _, s := range want {
		if !slices.Contains(have, s) {
			out = append(out, s)
		}
	}
	return out
}

// load reads the saved sign-in at start. It doesn't call Twitch.
func (a *Account) load(ctx context.Context) {
	a.op.Lock()
	defer a.op.Unlock()
	t, err := a.readSecret(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		a.setTokens(nil)
		a.setStatus(StateSignedOut, "")
	case err != nil:
		a.setTokens(nil)
		a.setStatus(StateUnknown, "Couldn't read the saved sign-in: "+err.Error())
	default:
		a.setTokens(t)
		a.setStatus(StateUnknown, "Checking with Twitch…")
	}
}

func (a *Account) readSecret(ctx context.Context) (*tokens, error) {
	v, err := a.svc.secrets.Get(ctx, a.secretKey())
	if err != nil {
		return nil, err
	}
	var t tokens
	if err := json.Unmarshal([]byte(v.Reveal()), &t); err != nil || t.AccessToken == "" {
		return nil, fmt.Errorf("the saved sign-in is damaged")
	}
	return &t, nil
}

func (a *Account) saveSecret(ctx context.Context, t *tokens) error {
	b, err := json.Marshal(t) //nolint:gosec // the tokens go straight into the secret store, never anywhere else
	if err != nil {
		return err
	}
	return a.svc.secrets.Set(ctx, a.secretKey(), secrets.NewValue(string(b)))
}

// Validate asks Twitch whether the saved sign-in still works. Twitch
// requires this at start and hourly. An expired access token is refreshed
// once; a sign-in Twitch refuses ends here.
func (a *Account) Validate(ctx context.Context) {
	a.op.Lock()
	defer a.op.Unlock()
	a.validateLocked(ctx)
}

func (a *Account) validateLocked(ctx context.Context) {
	a.mu.Lock()
	t := a.tok
	a.mu.Unlock()
	if t == nil {
		return
	}
	clientID, _ := a.svc.ClientID(ctx)
	if clientID == "" {
		a.mu.Lock()
		a.setStatus(StateNotConfigured, "Add a Twitch Client ID to use this sign-in.")
		a.mu.Unlock()
		return
	}
	if clientID != t.ClientID {
		a.endSession(ctx, t, "The Twitch Client ID changed, so this sign-in no longer works. Sign in again.")
		return
	}
	v, err := a.svc.oauth.validate(ctx, t.AccessToken)
	if isOAuth(err, 401, "") {
		nt, rerr := a.refreshLocked(ctx, t)
		if rerr != nil {
			return // refreshLocked has set the state
		}
		t = nt
		v, err = a.svc.oauth.validate(ctx, t.AccessToken)
		if isOAuth(err, 401, "") {
			a.endSession(ctx, t, "Twitch no longer accepts this sign-in. Sign in again.")
			return
		}
	}
	if err != nil {
		if ctx.Err() == nil {
			a.mu.Lock()
			a.setStatus(StateUnknown, "Couldn't check the sign-in with Twitch: "+err.Error())
			a.mu.Unlock()
		}
		return
	}
	if v.UserID != t.UserID || v.ClientID != t.ClientID {
		a.endSession(ctx, t, "Twitch says this sign-in belongs to another account or app. Sign in again.")
		return
	}
	if !slices.Equal(v.Scopes, t.Scopes) || v.Login != t.Login {
		nt := *t
		nt.Scopes, nt.Login = v.Scopes, v.Login
		_ = a.saveSecret(ctx, &nt) // best effort: the next validation corrects it again
		t = &nt
	}
	now := a.svc.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTokens(t)
	a.status.CheckedAt = &now
	detail := ""
	if len(a.status.MissingScopes) > 0 {
		detail = "Sign in again to allow: " + strings.Join(a.status.MissingScopes, ", ") + "."
	}
	a.setStatus(StateConnected, detail)
}

// AccessToken returns a token for calling Twitch, refreshing it first when
// it is about to expire. Never log it.
func (a *Account) AccessToken(ctx context.Context) (secrets.Value, error) {
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	t, st := a.tok, a.status.State
	a.mu.Unlock()
	if t == nil || (st != StateConnected && st != StateUnknown) {
		return secrets.Value{}, fmt.Errorf("twitch: the %s account isn't signed in", a.role)
	}
	if a.svc.now().Add(refreshAhead).After(t.ExpiresAt) {
		nt, err := a.refreshLocked(ctx, t)
		if err != nil {
			return secrets.Value{}, err
		}
		t = nt
	}
	return secrets.NewValue(t.AccessToken), nil
}

// refreshLocked swaps the refresh token for a new pair. The old refresh
// token is spent, so the new pair is saved before it is used. Call with op held.
func (a *Account) refreshLocked(ctx context.Context, t *tokens) (*tokens, error) {
	ans, err := a.svc.oauth.refresh(ctx, t.ClientID, t.RefreshToken)
	if isOAuth(err, 400, "") || isOAuth(err, 401, "") {
		a.endSession(ctx, t, "Twitch ended this sign-in (refresh tokens from a public app last up to 30 days). Sign in again.")
		return nil, fmt.Errorf("twitch: the %s sign-in ended: %w", a.role, err)
	}
	if err != nil {
		a.mu.Lock()
		if ctx.Err() == nil {
			a.setStatus(StateUnknown, "Couldn't refresh the sign-in with Twitch: "+err.Error())
		}
		a.mu.Unlock()
		return nil, err
	}
	nt := *t
	nt.AccessToken, nt.RefreshToken = ans.AccessToken, ans.RefreshToken
	nt.ExpiresAt = a.svc.now().Add(time.Duration(ans.ExpiresIn) * time.Second)
	if len(ans.Scope) > 0 {
		nt.Scopes = ans.Scope
	}
	saveErr := a.saveSecret(ctx, &nt)
	a.mu.Lock()
	a.setTokens(&nt)
	if saveErr != nil {
		a.setStatus(StateUnknown, "Refreshed the sign-in, but couldn't save it: "+saveErr.Error()+
			". It works until StreamGremlin closes.")
	}
	a.mu.Unlock()
	a.svc.log.Info("twitch token refreshed", "role", string(a.role))
	return &nt, nil
}

// endSession forgets a sign-in Twitch no longer accepts. Everything using
// it must stop (slice 5 listens through Service.Changes).
func (a *Account) endSession(ctx context.Context, t *tokens, reason string) {
	if err := a.svc.secrets.Delete(ctx, a.secretKey()); err != nil {
		a.svc.log.Warn("couldn't delete an ended twitch sign-in", "role", string(a.role), "err", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok == t || a.tok == nil {
		a.setTokens(nil)
		a.setStatus(StateNeedsSignIn, reason)
	}
}

// StartSignIn begins the device code flow and returns the code for the
// streamer to enter. A new sign-in replaces a pending one; an existing
// sign-in stays in use until the new one succeeds.
func (a *Account) StartSignIn(ctx context.Context) (PendingSignIn, error) {
	clientID, _ := a.svc.ClientID(ctx)
	if clientID == "" {
		return PendingSignIn{}, ErrNoClientID
	}
	ds, err := a.svc.oauth.startDevice(ctx, clientID, a.scopes)
	if err != nil {
		return PendingSignIn{}, fmt.Errorf("twitch: couldn't start signing in: %w", err)
	}
	if ds.Interval < 1 {
		ds.Interval = 5
	}
	p := PendingSignIn{
		UserCode:        ds.UserCode,
		VerificationURI: ds.VerificationURI,
		ExpiresAt:       a.svc.now().Add(time.Duration(ds.ExpiresIn) * time.Second),
	}
	pctx, cancel := context.WithDeadline(a.svc.ctx, p.ExpiresAt)
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.pending++
	id := a.pending
	a.cancel = cancel
	prev := a.status.State
	if prev == StateSigningIn {
		prev = a.prevStateLocked("")
	}
	a.setStatus(StateSigningIn, "")
	a.status.SignIn = &p
	a.mu.Unlock()
	go a.poll(pctx, cancel, id, clientID, ds, prev)
	return p, nil
}

// CancelSignIn stops a pending sign-in. Harmless when none is pending.
func (a *Account) CancelSignIn() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		return
	}
	a.cancel()
	a.cancel = nil
	a.pending++
	a.setStatus(a.prevStateLocked(""), "")
}

func (a *Account) poll(ctx context.Context, cancel context.CancelFunc, id uint64, clientID string, ds deviceStart, prev State) {
	defer cancel()
	interval := time.Duration(ds.Interval) * time.Second
	if a.svc.pollEvery > 0 {
		interval = a.svc.pollEvery
	}
	finish := func(detail string) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.pending != id {
			return // replaced or cancelled
		}
		a.cancel = nil
		a.setStatus(a.prevStateLocked(prev), detail)
	}
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finish("The sign-in code expired before it was entered. Start again.")
			}
			return
		case <-time.After(interval):
		}
		ans, err := a.svc.oauth.pollDevice(ctx, clientID, a.scopes, ds.DeviceCode)
		switch {
		case isOAuth(err, 0, "authorization_pending"):
			continue
		case isOAuth(err, 0, "slow_down"):
			interval += 5 * time.Second
			continue
		case errors.Is(err, errUnreachable):
			continue // keep trying until the code expires
		case ctx.Err() != nil:
			continue // the select above reports it
		case err != nil:
			var oe *oauthError
			msg := err.Error()
			if errors.As(err, &oe) {
				msg = oe.Message
			}
			finish("Twitch didn't complete the sign-in: " + msg + ". Start again.")
			return
		}
		a.complete(ctx, id, clientID, ans, prev)
		return
	}
}

// complete checks a new token and saves it.
func (a *Account) complete(ctx context.Context, id uint64, clientID string, ans tokenAnswer, prev State) {
	a.op.Lock()
	defer a.op.Unlock()
	fail := func(detail string, revoke bool) {
		if revoke {
			_ = a.svc.oauth.revoke(context.WithoutCancel(ctx), clientID, ans.AccessToken)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.pending == id {
			a.cancel = nil
			a.setStatus(a.prevStateLocked(prev), detail)
		}
	}
	v, err := a.svc.oauth.validate(ctx, ans.AccessToken)
	if err != nil {
		fail("Signed in, but couldn't check the new sign-in with Twitch: "+err.Error()+". Start again.", !errors.Is(err, errUnreachable))
		return
	}
	if v.ClientID != clientID || v.UserID == "" {
		fail("Twitch gave a sign-in for another app. Start again.", true)
		return
	}
	if other := a.svc.other(a.role).Status(); other.UserID != "" && other.UserID == v.UserID {
		if a.role == RoleBot {
			fail("That's your own account ("+v.Login+"). Sign the bot in with a different Twitch account: "+
				"use a private browser window, or sign out of twitch.tv first.", true)
		} else {
			fail("That's the bot's account ("+v.Login+"). Sign in with your own channel's account.", true)
		}
		return
	}
	now := a.svc.now()
	t := &tokens{
		AccessToken: ans.AccessToken, RefreshToken: ans.RefreshToken,
		ExpiresAt: now.Add(time.Duration(ans.ExpiresIn) * time.Second),
		Scopes:    v.Scopes, UserID: v.UserID, Login: v.Login, ClientID: clientID, SignedInAt: now,
	}
	a.mu.Lock()
	old := a.tok
	current := a.pending == id
	a.mu.Unlock()
	if !current {
		_ = a.svc.oauth.revoke(context.WithoutCancel(ctx), clientID, ans.AccessToken)
		return
	}
	if err := a.saveSecret(ctx, t); err != nil {
		fail("Signed in, but couldn't save the sign-in: "+err.Error(), true)
		return
	}
	if old != nil && old.AccessToken != t.AccessToken {
		_ = a.svc.oauth.revoke(context.WithoutCancel(ctx), old.ClientID, old.AccessToken)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancel = nil
	a.setTokens(t)
	a.status.CheckedAt = &now
	detail := ""
	if len(a.status.MissingScopes) > 0 {
		detail = "Twitch didn't grant: " + strings.Join(a.status.MissingScopes, ", ") + ". Sign in again and allow them."
	}
	a.setStatus(StateConnected, detail)
	a.svc.log.Info("twitch signed in", "role", string(a.role), "login", t.Login)
}

// prevStateLocked is the state to return to when a sign-in doesn't
// finish: still connected if an earlier sign-in exists. Call with mu held.
func (a *Account) prevStateLocked(prev State) State {
	switch {
	case a.tok != nil:
		return StateConnected
	case prev == StateNeedsSignIn || prev == StateNotConfigured:
		return prev
	}
	return StateSignedOut
}

// SignOut revokes and forgets the sign-in. Harmless when signed out.
func (a *Account) SignOut(ctx context.Context) error {
	a.CancelSignIn()
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	t := a.tok
	a.mu.Unlock()
	if t != nil {
		// Best effort: the sign-in is forgotten here even if Twitch can't be reached.
		if err := a.svc.oauth.revoke(ctx, t.ClientID, t.AccessToken); err != nil {
			a.svc.log.Warn("twitch sign-out: couldn't revoke the token", "role", string(a.role), "err", err)
		}
	}
	if err := a.svc.secrets.Delete(ctx, a.secretKey()); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTokens(nil)
	a.status.CheckedAt = nil
	a.setStatus(StateSignedOut, "")
	a.svc.log.Info("twitch signed out", "role", string(a.role))
	return nil
}
