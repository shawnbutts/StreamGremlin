// Package client calls the local API from the command line. It finds the
// running engine through the api-addr and api-token files in the data folder.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
)

// ErrNotRunning means no engine is serving this data folder.
var ErrNotRunning = errors.New(brand.Name + " isn't running")

// Client talks to one engine.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// ForDataDir connects to the engine using the data folder's files.
func ForDataDir(dir string) (*Client, error) {
	addr, err := os.ReadFile(datadir.AddrPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, err
	}
	tok, err := os.ReadFile(datadir.TokenPath(dir))
	if err != nil {
		return nil, fmt.Errorf("read the API token: %w", err)
	}
	return &Client{
		base:  strings.TrimSpace(string(addr)),
		token: strings.TrimSpace(string(tok)),
		http:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// APIError is an error answer from the engine.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Problems lists what's wrong with a saved workflow, at JSON paths.
	Problems []struct {
		Path    string `json:"path"`
		Message string `json:"message"`
	}
}

func (e *APIError) Error() string { return fmt.Sprintf("%s (%s)", e.Message, e.Code) }

// Do sends a request with an optional JSON body and decodes a JSON answer into out.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = b
	}
	return c.DoRaw(ctx, method, path, raw, out)
}

// DoRaw sends a request whose body is already encoded: JSON, or a
// sound's audio for POST /v1/sounds/{name}.
func (c *Client) DoRaw(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set(brand.APIVersionHeader, brand.APIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			// Nothing is listening: the engine stopped without removing
			// api-addr, for example after a crash. (Checked this way because
			// "connection refused" has a different errno on Windows.)
			return ErrNotRunning
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var eb struct {
			api.ErrorBody
			Problems []struct {
				Path    string `json:"path"`
				Message string `json:"message"`
			} `json:"problems"`
		}
		if json.NewDecoder(resp.Body).Decode(&eb) == nil && eb.Error.Code != "" {
			return &APIError{Status: resp.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message, Problems: eb.Problems}
		}
		return &APIError{Status: resp.StatusCode, Code: "unknown", Message: resp.Status}
	}
	if w, ok := out.(io.Writer); ok {
		// A text answer, such as a ban list's file, copied as it comes.
		_, err := io.Copy(w, resp.Body)
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
