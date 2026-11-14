// Package youtube connects to YouTube (M6, D-095, D-096): Google's sign-in
// for desktop apps for the streamer's channel and an optional bot channel,
// keeping tokens fresh, and counting the Data API's daily quota
// (docs/adr/0046-youtube-sign-in.md). Only documented public endpoints
// are used; see Manifest.
package youtube

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Google's endpoints for installed apps.
const (
	DefaultAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultTokenURL  = "https://oauth2.googleapis.com/token" //nolint:gosec // an endpoint, not a credential
	DefaultRevokeURL = "https://oauth2.googleapis.com/revoke"
	DefaultAPIURL    = "https://www.googleapis.com/youtube/v3"
)

// endpoints are where sign-in and the Data API are; a test server serves
// them all under one base.
type endpoints struct {
	auth, token, revoke, api string
}

func defaultEndpoints() endpoints {
	return endpoints{auth: DefaultAuthURL, token: DefaultTokenURL, revoke: DefaultRevokeURL, api: DefaultAPIURL}
}

func testEndpoints(base string) endpoints {
	base = strings.TrimRight(base, "/")
	return endpoints{auth: base + "/o/oauth2/v2/auth", token: base + "/token", revoke: base + "/revoke", api: base + "/youtube/v3"}
}

// errUnreachable wraps network failures and Google's server errors: the
// sign-in may still be fine, so its state is unknown, not signed out.
var errUnreachable = errors.New("youtube: couldn't reach Google")

// oauthError is a 4xx answer from Google's token server, such as
// {"error":"invalid_grant","error_description":"Token has been expired or revoked."}.
type oauthError struct {
	Status      int
	Code        string
	Description string
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("youtube: %s: %s (%d)", e.Code, e.Description, e.Status)
	}
	return fmt.Sprintf("youtube: %s (%d)", e.Code, e.Status)
}

func isOAuth(err error, code string) bool {
	var oe *oauthError
	return errors.As(err, &oe) && (code == "" || oe.Code == code)
}

// tokenAnswer is Google's answer from its token endpoint. Scope is
// space-separated; RefreshToken comes only with a code's answer.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (t tokenAnswer) scopes() []string { return strings.Fields(t.Scope) }

// pkce is a sign-in's proof key (RFC 7636, S256), as Google asks of
// desktop apps.
type pkce struct{ verifier, challenge string }

func newPKCE() pkce {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	v := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(v))
	return pkce{verifier: v, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}
}

func randomState() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// client is who StreamGremlin is to Google: the client ID, and the secret
// Google gives desktop apps (not truly secret, but kept like one).
type client struct {
	ID     string
	Secret string
}

type oauth struct {
	ep   endpoints
	http *http.Client
}

func newOAuth(ep endpoints) *oauth {
	return &oauth{ep: ep, http: &http.Client{Timeout: 15 * time.Second}}
}

// authURL is the page the streamer signs in on.
func (o *oauth) authURL(c client, scopes []string, redirect, state string, p pkce) string {
	v := url.Values{
		"client_id":             {c.ID},
		"redirect_uri":          {redirect},
		"response_type":         {"code"},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {p.challenge},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		// Always the account chooser and consent, so a second sign-in
		// (the bot) can pick another account or brand channel, and a
		// fresh refresh token is given.
		"prompt": {"select_account consent"},
	}
	return o.ep.auth + "?" + v.Encode()
}

func (o *oauth) exchange(ctx context.Context, c client, code, verifier, redirect string) (tokenAnswer, error) {
	v := url.Values{
		"client_id":     {c.ID},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirect},
	}
	if c.Secret != "" {
		v.Set("client_secret", c.Secret)
	}
	var out tokenAnswer
	err := o.form(ctx, o.ep.token, v, &out)
	if err == nil && (out.AccessToken == "" || out.RefreshToken == "") {
		err = fmt.Errorf("%w: an incomplete answer to the sign-in", errUnreachable)
	}
	return out, err
}

func (o *oauth) refresh(ctx context.Context, c client, refreshToken string) (tokenAnswer, error) {
	v := url.Values{
		"client_id":     {c.ID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if c.Secret != "" {
		v.Set("client_secret", c.Secret)
	}
	var out tokenAnswer
	err := o.form(ctx, o.ep.token, v, &out)
	if err == nil && out.AccessToken == "" {
		err = fmt.Errorf("%w: an incomplete answer to a token refresh", errUnreachable)
	}
	return out, err
}

// revoke ends the whole sign-in at Google: revoking either token does.
func (o *oauth) revoke(ctx context.Context, token string) error {
	return o.form(ctx, o.ep.revoke, url.Values{"token": {token}}, nil)
}

func (o *oauth) form(ctx context.Context, endpoint string, v url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", errUnreachable, describeNetErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreachable, err)
	}
	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: Google answered %d", errUnreachable, resp.StatusCode)
	case resp.StatusCode >= 400:
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &oauthError{Status: resp.StatusCode, Code: e.Error, Description: e.Description}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: an answer that isn't JSON", errUnreachable)
	}
	return nil
}

// describeNetErr drops the request (which may hold a token) from an error.
func describeNetErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
