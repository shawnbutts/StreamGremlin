// Package twitch connects to Twitch: sign-in with the device code flow for
// the streamer's account and an optional bot account, keeping tokens fresh
// and valid (docs/adr/0005-twitch-sign-in.md). Only documented public
// endpoints are used; see Manifest.
package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAuthURL is Twitch's OAuth server.
const DefaultAuthURL = "https://id.twitch.tv"

// oauth calls id.twitch.tv as a public client: no client secret, ever.
type oauth struct {
	base string
	http *http.Client
}

func newOAuth(base string) *oauth {
	return &oauth{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

// errUnreachable wraps network failures and Twitch server errors: the
// token may still be fine, so state is unknown, not signed out.
var errUnreachable = errors.New("twitch: couldn't reach Twitch")

// oauthError is a 4xx answer from Twitch, such as {"status":400,"message":"authorization_pending"}.
type oauthError struct {
	Status  int
	Message string
}

func (e *oauthError) Error() string { return fmt.Sprintf("twitch: %s (%d)", e.Message, e.Status) }

func isOAuth(err error, status int, message string) bool {
	var oe *oauthError
	return errors.As(err, &oe) && (status == 0 || oe.Status == status) &&
		(message == "" || strings.EqualFold(oe.Message, message))
}

// deviceStart is Twitch's answer to POST /oauth2/device.
type deviceStart struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// tokenAnswer is Twitch's answer to POST /oauth2/token.
type tokenAnswer struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Scope        []string `json:"scope"`
}

// validation is Twitch's answer to GET /oauth2/validate.
type validation struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	UserID    string   `json:"user_id"`
	Scopes    []string `json:"scopes"`
	ExpiresIn int      `json:"expires_in"`
}

func (o *oauth) startDevice(ctx context.Context, clientID string, scopes []string) (deviceStart, error) {
	var out deviceStart
	err := o.form(ctx, "/oauth2/device", url.Values{
		"client_id": {clientID},
		"scopes":    {strings.Join(scopes, " ")},
	}, &out)
	if err == nil && (out.DeviceCode == "" || out.UserCode == "" || out.VerificationURI == "") {
		err = fmt.Errorf("%w: an incomplete answer to the sign-in request", errUnreachable)
	}
	return out, err
}

func (o *oauth) pollDevice(ctx context.Context, clientID string, scopes []string, deviceCode string) (tokenAnswer, error) {
	var out tokenAnswer
	err := o.form(ctx, "/oauth2/token", url.Values{
		"client_id":   {clientID},
		"scopes":      {strings.Join(scopes, " ")},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}, &out)
	return out, err
}

func (o *oauth) refresh(ctx context.Context, clientID, refreshToken string) (tokenAnswer, error) {
	var out tokenAnswer
	err := o.form(ctx, "/oauth2/token", url.Values{
		"client_id":     {clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, &out)
	if err == nil && (out.AccessToken == "" || out.RefreshToken == "") {
		err = fmt.Errorf("%w: an incomplete answer to a token refresh", errUnreachable)
	}
	return out, err
}

func (o *oauth) validate(ctx context.Context, accessToken string) (validation, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", o.base+"/oauth2/validate", nil)
	if err != nil {
		return validation{}, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)
	var out validation
	return out, o.do(req, &out)
}

func (o *oauth) revoke(ctx context.Context, clientID, accessToken string) error {
	return o.form(ctx, "/oauth2/revoke", url.Values{"client_id": {clientID}, "token": {accessToken}}, nil)
}

func (o *oauth) form(ctx context.Context, path string, v url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", o.base+path, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return o.do(req, out)
}

// do sends req. Errors never include the request, which holds tokens.
func (o *oauth) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := o.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		return fmt.Errorf("%w: %s", errUnreachable, describeNetErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreachable, err)
	}
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: Twitch answered %s", errUnreachable, resp.Status)
	case resp.StatusCode >= 400:
		var e struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return &oauthError{Status: resp.StatusCode, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: an answer that isn't JSON", errUnreachable)
	}
	return nil
}

// describeNetErr keeps the useful part of a network error without the URL,
// which for some requests could carry a token.
func describeNetErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
