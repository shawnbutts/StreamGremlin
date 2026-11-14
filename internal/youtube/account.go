package youtube

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

// Role says which account a sign-in is for.
type Role string

const (
	// RoleStreamer is the channel's own account.
	RoleStreamer Role = "streamer"
	// RoleBot is an optional second channel that sends chat.
	RoleBot Role = "bot"
)

// State is one account's state. Only Connected means Google confirmed the
// sign-in recently; Unknown never counts as signed in.
type State string

const (
	StateNotConfigured State = "not_configured" // no client ID yet
	StateSignedOut     State = "signed_out"
	StateSigningIn     State = "signing_in"    // waiting for the browser
	StateConnected     State = "connected"     // Google confirmed it
	StateNeedsSignIn   State = "needs_sign_in" // the sign-in ended; sign in again
	StateUnknown       State = "unknown"       // couldn't ask Google
)

// refreshAhead refreshes an access token this long before it expires.
const refreshAhead = 10 * time.Minute

// PendingSignIn is what the streamer needs to finish a sign-in: Google's
// page to open. It returns to this computer when they're done.
type PendingSignIn struct {
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AccountStatus describes one account. It never holds tokens.
type AccountStatus struct {
	Role          Role           `json:"role"`
	State         State          `json:"state"`
	Channel       *Channel       `json:"channel,omitempty"`
	Scopes        []string       `json:"scopes"`
	MissingScopes []string       `json:"missing_scopes"`
	Detail        string         `json:"detail,omitempty"`
	Since         time.Time      `json:"since"`
	CheckedAt     *time.Time     `json:"checked_at,omitempty"`
	SignIn        *PendingSignIn `json:"sign_in,omitempty"`
}

// tokens is the secret saved for a signed-in account, one JSON value so
// a refresh replaces it in one write.
type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes"`
	Channel      Channel   `json:"channel"`
	ClientID     string    `json:"client_id"`
	SignedInAt   time.Time `json:"signed_in_at"`
}

// LogValue keeps tokens out of logs even if one is passed by mistake.
func (t tokens) LogValue() slog.Value {
	return slog.GroupValue(slog.String("channel", t.Channel.ID))
}

// Account is one YouTube sign-in. Network calls happen outside mu; op
// keeps sign-in, refresh and sign-out from interleaving.
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

func (a *Account) secretKey() string { return "youtube/" + string(a.role) + "/oauth" }

// Status returns the account's state.
func (a *Account) Status() AccountStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.status
	s.Scopes = slices.Clone(s.Scopes)
	s.MissingScopes = slices.Clone(s.MissingScopes)
	if s.Channel != nil {
		c := *s.Channel
		s.Channel = &c
	}
	return s
}

// setStatus changes the state, keeping Since when it's unchanged. Call
// with mu held.
func (a *Account) setStatus(st State, detail string) {
	if st != a.status.State {
		a.status.Since = a.svc.now()
		a.svc.log.Info("youtube account state", "role", string(a.role), "state", string(st), "detail", detail)
	}
	a.status.State = st
	a.status.Detail = detail
	if st != StateSigningIn {
		a.status.SignIn = nil
	}
	a.svc.changed()
}

// setTokens is called with mu held.
func (a *Account) setTokens(t *tokens) {
	a.tok = t
	if t == nil {
		a.status.Channel = nil
		a.status.Scopes, a.status.MissingScopes = []string{}, []string{}
		return
	}
	c := t.Channel
	a.status.Channel = &c
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

// load reads the saved sign-in at start. It doesn't call Google.
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
		a.setStatus(StateUnknown, "Checking with Google…")
	}
}

func (a *Account) readSecret(ctx context.Context) (*tokens, error) {
	v, err := a.svc.secrets.Get(ctx, a.secretKey())
	if err != nil {
		return nil, err
	}
	var t tokens
	if err := json.Unmarshal([]byte(v.Reveal()), &t); err != nil || t.RefreshToken == "" {
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

// Validate checks the saved sign-in with Google by refreshing it: access
// tokens last an hour, and a refresh Google refuses means the sign-in
// ended. It costs no quota.
func (a *Account) Validate(ctx context.Context) {
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	t := a.tok
	a.mu.Unlock()
	if t == nil {
		return
	}
	c, _ := a.svc.client(ctx)
	if c.ID == "" {
		a.mu.Lock()
		a.setStatus(StateNotConfigured, "Add your Google project's client ID to use this sign-in.")
		a.mu.Unlock()
		return
	}
	if c.ID != t.ClientID {
		a.endSession(ctx, t, "The Google client ID changed, so this sign-in no longer works. Sign in again.")
		return
	}
	nt, err := a.refreshLocked(ctx, t)
	if err != nil {
		return // refreshLocked has set the state
	}
	now := a.svc.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTokens(nt)
	a.status.CheckedAt = &now
	detail := ""
	if len(a.status.MissingScopes) > 0 {
		detail = "Sign in again and allow StreamGremlin to manage your YouTube account: it's needed for chat."
	}
	a.setStatus(StateConnected, detail)
}

// AccessToken returns a token for calling YouTube, refreshing it first
// when it's about to expire. Never log it.
func (a *Account) AccessToken(ctx context.Context) (secrets.Value, error) {
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	t, st := a.tok, a.status.State
	a.mu.Unlock()
	if t == nil || (st != StateConnected && st != StateUnknown) {
		return secrets.Value{}, fmt.Errorf("youtube: the %s account isn't signed in", a.role)
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

// refreshLocked gets a new access token. Google keeps the refresh token
// unless it sends a new one. Call with op held.
func (a *Account) refreshLocked(ctx context.Context, t *tokens) (*tokens, error) {
	c, _ := a.svc.client(ctx)
	ans, err := a.svc.oauth.refresh(ctx, c, t.RefreshToken)
	switch {
	case isOAuth(err, "invalid_grant"):
		a.endSession(ctx, t, "Google ended this sign-in. That happens when access is removed in your Google account, "+
			"or after 7 days while your Google project's publishing status is Testing: publish it (see the guide). Sign in again.")
		return nil, fmt.Errorf("youtube: the %s sign-in ended: %w", a.role, err)
	case isOAuth(err, "invalid_client"), isOAuth(err, "unauthorized_client"):
		// The sign-in may be fine; the client ID or secret isn't. Keep it,
		// so fixing them brings it back.
		a.mu.Lock()
		a.setStatus(StateUnknown, "Google refused your project's client ID or secret. Check them on Connections.")
		a.mu.Unlock()
		return nil, err
	case err != nil:
		a.mu.Lock()
		if ctx.Err() == nil {
			a.setStatus(StateUnknown, "Couldn't check the sign-in with Google: "+err.Error())
		}
		a.mu.Unlock()
		return nil, err
	}
	nt := *t
	nt.AccessToken = ans.AccessToken
	if ans.RefreshToken != "" {
		nt.RefreshToken = ans.RefreshToken
	}
	nt.ExpiresAt = a.svc.now().Add(time.Duration(ans.ExpiresIn) * time.Second)
	if s := ans.scopes(); len(s) > 0 {
		nt.Scopes = s
	}
	saveErr := a.saveSecret(ctx, &nt)
	a.mu.Lock()
	a.setTokens(&nt)
	if saveErr != nil {
		a.setStatus(StateUnknown, "Refreshed the sign-in, but couldn't save it: "+saveErr.Error()+
			". It works until StreamGremlin closes.")
	}
	a.mu.Unlock()
	a.svc.log.Debug("youtube token refreshed", "role", string(a.role))
	return &nt, nil
}

// endSession forgets a sign-in Google no longer accepts.
func (a *Account) endSession(ctx context.Context, t *tokens, reason string) {
	if err := a.svc.secrets.Delete(ctx, a.secretKey()); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		a.svc.log.Warn("couldn't delete an ended youtube sign-in", "role", string(a.role), "err", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok == t || a.tok == nil {
		a.setTokens(nil)
		a.setStatus(StateNeedsSignIn, reason)
	}
}

// StartSignIn opens a one-time address on this computer for Google to
// return to, and gives the page to open. A new sign-in replaces a pending
// one; an existing sign-in stays in use until the new one succeeds.
func (a *Account) StartSignIn(ctx context.Context) (PendingSignIn, error) {
	c, _ := a.svc.client(ctx)
	if c.ID == "" {
		return PendingSignIn{}, ErrNoClient
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return PendingSignIn{}, fmt.Errorf("youtube: couldn't open an address for Google to return to: %w", err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)
	state, proof := randomState(), newPKCE()
	p := PendingSignIn{
		AuthURL:   a.svc.oauth.authURL(c, a.scopes, redirect, state, proof),
		ExpiresAt: a.svc.now().Add(a.svc.signInFor),
	}
	pctx, cancel := context.WithTimeout(a.svc.ctx, a.svc.signInFor)
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

	var once sync.Once
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Type", "text/html; charset=utf-8")
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/" ||
			subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(signInPage("This link isn't for the sign-in StreamGremlin is waiting for. Start again from StreamGremlin.")))
			return
		}
		ran := false
		var result string
		once.Do(func() {
			ran = true
			if e := q.Get("error"); e != "" {
				result = "Google didn't complete the sign-in (" + e + "). Start again from StreamGremlin."
				a.finish(id, prev, result)
			} else {
				result = a.complete(context.WithoutCancel(pctx), id, c, q.Get("code"), proof.verifier, redirect, prev)
			}
			cancel()
		})
		if !ran {
			result = "This sign-in is already finished. Check StreamGremlin."
		}
		_, _ = w.Write([]byte(signInPage(result)))
	})
	go func() { _ = srv.Serve(ln) }()
	go func() { //nolint:gosec // runs after the sign-in's context is done, so shutting down needs its own
		<-pctx.Done()
		if errors.Is(pctx.Err(), context.DeadlineExceeded) {
			a.finish(id, prev, "The sign-in wasn't finished in time. Start again.")
		}
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = srv.Shutdown(sctx) // lets the last page finish
	}()
	return p, nil
}

func signInPage(msg string) string {
	return "<!doctype html><meta charset=\"utf-8\"><title>StreamGremlin</title><h1>StreamGremlin</h1><p>" +
		html.EscapeString(msg) + "</p><p>You can close this tab.</p>"
}

// finish ends a pending sign-in that didn't succeed.
func (a *Account) finish(id uint64, prev State, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending != id || a.status.State != StateSigningIn {
		return // replaced, cancelled or done
	}
	a.cancel = nil
	a.setStatus(a.prevStateLocked(prev), detail)
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

// complete swaps the code for tokens, finds the channel, and saves the
// sign-in. It returns what the browser page says.
func (a *Account) complete(ctx context.Context, id uint64, c client, code, verifier, redirect string, prev State) string {
	a.op.Lock()
	defer a.op.Unlock()
	fail := func(detail, token string) string {
		if token != "" {
			_ = a.svc.oauth.revoke(ctx, token)
		}
		a.finish(id, prev, detail)
		return detail
	}
	ans, err := a.svc.oauth.exchange(ctx, c, code, verifier, redirect)
	if err != nil {
		if isOAuth(err, "invalid_client") || isOAuth(err, "unauthorized_client") {
			return fail("Google refused your project's client ID or secret. Check them on Connections, then start again.", "")
		}
		return fail("Google didn't complete the sign-in: "+strings.TrimPrefix(err.Error(), "youtube: ")+". Start again.", "")
	}
	ch, err := a.svc.data.myChannel(ctx, ans.AccessToken)
	switch {
	case errors.Is(err, errNoChannel):
		return fail("That Google account has no YouTube channel. Sign in again and pick the account, or brand account, "+
			"that owns your channel.", ans.RefreshToken)
	case err != nil:
		return fail("Signed in, but couldn't ask YouTube which channel it is: "+strings.TrimPrefix(err.Error(), "youtube: ")+
			". Start again.", ans.RefreshToken)
	}
	if other := a.svc.other(a.role).Status(); other.Channel != nil && other.Channel.ID == ch.ID {
		// Not revoked: Google ends a whole grant, which would end the
		// other account's sign-in too.
		if a.role == RoleBot {
			return fail("That's your own channel ("+ch.Title+"). Sign the bot in with another channel: "+
				"pick another account or brand account in Google's list.", "")
		}
		return fail("That's the bot's channel ("+ch.Title+"). Sign in with your own channel.", "")
	}
	now := a.svc.now()
	t := &tokens{
		AccessToken: ans.AccessToken, RefreshToken: ans.RefreshToken,
		ExpiresAt: now.Add(time.Duration(ans.ExpiresIn) * time.Second),
		Scopes:    ans.scopes(), Channel: ch, ClientID: c.ID, SignedInAt: now,
	}
	a.mu.Lock()
	old := a.tok
	current := a.pending == id && a.status.State == StateSigningIn
	a.mu.Unlock()
	if !current {
		if old == nil || old.Channel.ID != ch.ID {
			_ = a.svc.oauth.revoke(ctx, ans.RefreshToken)
		}
		return "This sign-in was cancelled in StreamGremlin."
	}
	if err := a.saveSecret(ctx, t); err != nil {
		return fail("Signed in, but couldn't save the sign-in: "+err.Error(), ans.RefreshToken)
	}
	if old != nil && old.RefreshToken != t.RefreshToken && old.Channel.ID != ch.ID {
		// Another channel's sign-in is replaced: end it at Google. The same
		// channel's would end this new one too (Google ends the grant).
		_ = a.svc.oauth.revoke(ctx, old.RefreshToken)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancel = nil
	a.setTokens(t)
	a.status.CheckedAt = &now
	detail := ""
	if len(a.status.MissingScopes) > 0 {
		detail = "Google didn't grant managing your YouTube account, which chat needs. Sign in again and allow it."
	}
	a.setStatus(StateConnected, detail)
	a.svc.log.Info("youtube signed in", "role", string(a.role), "channel", ch.ID)
	if detail != "" {
		return "Signed in as " + ch.Title + ", but " + strings.ToLower(detail[:1]) + detail[1:]
	}
	return "Signed in as " + ch.Title + ". Back to StreamGremlin."
}

// prevStateLocked is the state to return to when a sign-in doesn't
// finish. Call with mu held.
func (a *Account) prevStateLocked(prev State) State {
	switch {
	case a.tok != nil:
		return StateConnected
	case prev == StateNeedsSignIn || prev == StateNotConfigured:
		return prev
	}
	return StateSignedOut
}

// SignOut ends the sign-in at Google (best effort) and forgets it.
func (a *Account) SignOut(ctx context.Context) error {
	a.CancelSignIn()
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	t := a.tok
	a.mu.Unlock()
	if t != nil {
		if err := a.svc.oauth.revoke(ctx, t.RefreshToken); err != nil {
			a.svc.log.Warn("youtube sign-out: couldn't revoke the sign-in", "role", string(a.role), "err", err)
		}
	}
	if err := a.svc.secrets.Delete(ctx, a.secretKey()); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setTokens(nil)
	a.status.CheckedAt = nil
	a.setStatus(StateSignedOut, "")
	a.svc.log.Info("youtube signed out", "role", string(a.role))
	return nil
}
