package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHub's REST API for releases, as documented at
// https://docs.github.com/en/rest/releases/releases (checked 2026-11-16):
//
//   - GET /repos/{owner}/{repo}/releases lists published releases,
//     pre-releases included; drafts only for people with push access, so
//     never here. per_page is at most 100 (default 30). The order isn't
//     documented, so every release is compared by version.
//   - A User-Agent is required; unauthenticated calls get 60 an hour per
//     IP address. The app checks once a day.
//   - Release assets are fetched from browser_download_url, which may
//     redirect.
const (
	DefaultAPI = "https://api.github.com"
	apiVersion = "2026-03-10"
	// perPage is how many recent releases are looked at.
	perPage = 30
	// maxListBytes caps the list of releases read.
	maxListBytes = 4 << 20
)

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
	// State is "uploaded", or "open" while an upload is unfinished.
	State string `json:"state"`
}

type ghRelease struct {
	Tag         string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

// errRateLimited means GitHub asked the app to wait.
type errRateLimited struct{ until time.Time }

func (e errRateLimited) Error() string {
	if e.until.IsZero() {
		return "GitHub asked StreamGremlin to wait before checking again"
	}
	return "GitHub asked StreamGremlin to wait until " + e.until.Local().Format("15:04") + " before checking again"
}

// listReleases fetches recent releases from the API at base.
func (s *Service) listReleases(ctx context.Context) ([]ghRelease, error) {
	u := s.opts.API + "/repos/" + s.opts.Repo + "/releases?per_page=" + strconv.Itoa(perPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", s.userAgent())
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach GitHub: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		var until time.Time
		if n, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			until = time.Unix(n, 0)
		}
		return nil, errRateLimited{until: until}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var out []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("GitHub's list of releases couldn't be read: %w", err)
	}
	return out, nil
}

func (s *Service) userAgent() string {
	// The app's name and version only: nothing about the streamer.
	return "StreamGremlin/" + s.opts.Version
}

// unwrapURL drops net/url's "Get <url>:" prefix, which says nothing useful
// to a streamer.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// tagVersion reads a release's tag, "v" and a version.
func tagVersion(tag string) (Version, bool) {
	rest, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return Version{}, false
	}
	return ParseVersion(rest)
}
