// Package mockgithub is a stand-in for the parts of GitHub's REST API that
// StreamGremlin's update check uses (D-120): listing a repository's
// releases, and downloading their files. It's for tests, and for trying
// updates with cmd/mockgithub.
package mockgithub

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/updates"
)

// Release is one release and its files.
type Release struct {
	Tag        string
	Draft      bool
	Prerelease bool
	Notes      string
	Files      map[string][]byte
}

// Server serves releases. Base must be set to where it's reachable before
// releases are listed, so download links point back at it.
type Server struct {
	mu        sync.Mutex
	Base      string
	releases  []Release
	limitTill time.Time
	lists     int
	downloads map[string]int
	// Corrupt names a file served with one byte changed.
	corrupt string
}

// New returns an empty Server.
func New() *Server { return &Server{downloads: map[string]int{}} }

// Add adds a release, newest last.
func (s *Server) Add(r Release) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, r)
}

// RateLimit makes listing answer 403, as GitHub does, until t.
func (s *Server) RateLimit(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limitTill = t
}

// Corrupt serves name with a changed byte, as if damaged on the way.
func (s *Server) Corrupt(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.corrupt = name
}

// Lists is how many times releases were listed.
func (s *Server) Lists() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

// Downloads is how many times name was downloaded.
func (s *Server) Downloads(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.downloads[name]
}

// Signed adds SHA256SUMS.txt for files and, with priv, its signature, as a
// release made with `make release-sign` carries.
func Signed(files map[string][]byte, priv ed25519.PrivateKey) map[string][]byte {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	var sums strings.Builder
	out := map[string][]byte{}
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), n)
		out[n] = files[n]
	}
	out[updates.SumsFile] = []byte(sums.String())
	if priv != nil {
		out[updates.SigFile] = updates.Sign(priv, out[updates.SumsFile])
	}
	return out
}

type asset struct {
	Name  string `json:"name"`
	URL   string `json:"browser_download_url"`
	Size  int    `json:"size"`
	State string `json:"state"`
}

type release struct {
	Tag         string  `json:"tag_name"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Body        string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Assets      []asset `json:"assets"`
}

// ServeHTTP answers GET /repos/{owner}/{repo}/releases and
// GET /download/{tag}/{name}.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method != http.MethodGet:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	case r.Header.Get("User-Agent") == "":
		// GitHub refuses requests without one.
		http.Error(w, "a User-Agent is required", http.StatusForbidden)
	case len(parts) == 4 && parts[0] == "repos" && parts[3] == "releases":
		s.lists++
		if time.Now().Before(s.limitTill) {
			w.Header().Set("X-Ratelimit-Remaining", "0")
			w.Header().Set("X-Ratelimit-Reset", strconv.FormatInt(s.limitTill.Unix(), 10))
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		out := []release{}
		// Newest first, as GitHub shows them.
		for i := len(s.releases) - 1; i >= 0; i-- {
			rel := s.releases[i]
			if rel.Draft { // drafts are only listed for people with push access
				continue
			}
			o := release{Tag: rel.Tag, Prerelease: rel.Prerelease, Body: rel.Notes,
				HTMLURL: "https://github.com/" + parts[1] + "/" + parts[2] + "/releases/tag/" + rel.Tag, PublishedAt: "2026-11-16T12:00:00Z", Assets: []asset{}}
			names := make([]string, 0, len(rel.Files))
			for n := range rel.Files {
				names = append(names, n)
			}
			slices.Sort(names)
			for _, n := range names {
				o.Assets = append(o.Assets, asset{Name: n, URL: s.Base + "/download/" + rel.Tag + "/" + n, Size: len(rel.Files[n]), State: "uploaded"})
			}
			out = append(out, o)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	case len(parts) == 3 && parts[0] == "download":
		for _, rel := range s.releases {
			if b, ok := rel.Files[parts[2]]; ok && rel.Tag == parts[1] && !rel.Draft {
				s.downloads[parts[2]]++
				if s.corrupt == parts[2] && len(b) > 0 {
					b = append([]byte{b[0] ^ 1}, b[1:]...)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				_, _ = w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}
