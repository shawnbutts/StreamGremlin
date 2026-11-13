// Package desktop is the desktop app's side of the engine: it serves the UI
// and hands the UI's /v1/* requests to the API in-process, adding the token.
// It has no Wails code, so it builds and tests without cgo; cmd/desktop wires
// it into Wails (docs/adr/0002-local-api.md).
package desktop

import (
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"

	"code.streamgremlin.com/streamgremlin/internal/brand"
)

// Phase is the engine's state as the app sees it.
type Phase string

const (
	PhaseStarting Phase = "starting"
	PhaseReady    Phase = "ready"
	PhaseFailed   Phase = "failed"
)

// Engine is what the bridge needs from a running host.
type Engine interface {
	Handler() http.Handler
	Token() string
}

// State tracks the engine's start-up for the UI.
type State struct {
	mu      sync.RWMutex
	phase   Phase
	message string
	engine  Engine
}

// NewState starts in PhaseStarting.
func NewState() *State { return &State{phase: PhaseStarting} }

// Ready records a running engine.
func (s *State) Ready(e Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.message, s.engine = PhaseReady, "", e
}

// Starting records that the engine is (re)starting.
func (s *State) Starting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.message, s.engine = PhaseStarting, "", nil
}

// Failed records why the engine couldn't start, in words for the streamer.
func (s *State) Failed(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.message, s.engine = PhaseFailed, message, nil
}

func (s *State) snapshot() (Phase, string, Engine) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phase, s.message, s.engine
}

// Info is the body of GET /desktop/info.
type Info struct {
	Name    string `json:"name"`
	State   Phase  `json:"state"`
	Message string `json:"message,omitempty"`
}

// CSP allows only the app's own files. No inline scripts or styles, no
// remote resources or forms. Audio comes from the library through the
// bridge (media-src 'self'). The only frames are the overlay previews,
// served by the engine on this computer (docs/adr/0017-alert-overlays.md).
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self'; " +
	"font-src 'self'; connect-src 'self'; frame-src http://127.0.0.1:*; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Handler serves the UI from assets (the built frontend) and bridges /v1/*.
// open shows a web page in the system browser; POST /desktop/open-url uses
// it for allow-listed pages only (AllowedURL). restart stops and starts the
// engine, for POST /desktop/restart-engine: a staged restore is applied as
// it starts (docs/adr/0012-backups-and-retention.md).
// SaveFunc asks where to save a file with the system's own save dialog,
// suggesting name, and writes data there. It returns the path, or "" when
// the streamer cancelled.
type SaveFunc func(name string, data []byte) (string, error)

// ChooseFunc asks for a file with the system's own open dialog, titled
// title, offering files matching pattern ("*.jpg;*.png") under filter's
// name. It returns the path, or "" when the streamer cancelled.
type ChooseFunc func(title, filter, pattern string) (string, error)

// Dialogs are the system's own dialogs, for POST /desktop/save-file and
// POST /desktop/choose-file.
type Dialogs struct {
	Save   SaveFunc
	Choose ChooseFunc
}

func Handler(assets fs.FS, state *State, open func(url string) error, restart func() error, dialogs ...Dialogs) http.Handler {
	var dl Dialogs
	if len(dialogs) > 0 {
		dl = dialogs[0]
	}
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		switch {
		case r.URL.Path == "/desktop/info":
			phase, msg, _ := state.snapshot()
			h.Set("Content-Type", "application/json; charset=utf-8")
			h.Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(Info{Name: brand.Name, State: phase, Message: msg})
		case r.URL.Path == "/desktop/open-url":
			openURL(w, r, open)
		case r.URL.Path == "/desktop/restart-engine":
			restartEngine(w, r, restart)
		case r.URL.Path == "/desktop/save-file":
			saveFile(w, r, dl.Save)
		case r.URL.Path == "/desktop/choose-file":
			chooseFile(w, r, dl.Choose)
		case strings.HasPrefix(r.URL.Path, "/v1/"):
			bridge(w, r, state)
		default:
			h.Set("Content-Security-Policy", CSP)
			// A path without a file extension is a screen: serve the app.
			if path.Ext(r.URL.Path) == "" {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
			files.ServeHTTP(w, r)
		}
	})
}

func bridge(w http.ResponseWriter, r *http.Request, state *State) {
	phase, msg, engine := state.snapshot()
	if engine == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		code := "engine_" + string(phase)
		if msg == "" {
			msg = brand.Name + " is starting"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
		return
	}
	in := r.Clone(r.Context())
	in.Host = "127.0.0.1"
	in.RequestURI = ""
	// The webview's own headers say nothing the API should trust.
	for _, k := range []string{"Origin", "Referer", "Cookie", "Authorization"} {
		in.Header.Del(k)
	}
	in.Header.Set("Authorization", "Bearer "+engine.Token())
	in.Header.Set(brand.APIVersionHeader, brand.APIVersion)
	engine.Handler().ServeHTTP(w, in)
}

// AllowedURL reports whether the app may open u in the system browser:
// Twitch's activation page and developer console, or a loopback test
// server's /activate page (mocktwitch). Nothing else, so a compromised page
// can't send the streamer anywhere.
func AllowedURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.User != nil || p.Fragment != "" {
		return false
	}
	switch {
	case p.Scheme == "https" && p.Host == "www.twitch.tv" && p.Path == "/activate":
		return true
	case p.Scheme == "https" && p.Host == "dev.twitch.tv" && (p.Path == "/console" || strings.HasPrefix(p.Path, "/console/") ||
		strings.HasPrefix(p.Path, "/docs/")):
		return true
	case p.Scheme == "http" && p.Path == "/activate":
		ip := net.ParseIP(p.Hostname())
		return ip != nil && ip.IsLoopback() && p.Port() != ""
	}
	return false
}

func restartEngine(w http.ResponseWriter, r *http.Request, restart func() error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case r.Method != http.MethodPost:
		w.WriteHeader(http.StatusMethodNotAllowed)
	case restart == nil:
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unavailable", "message": "can't restart the engine here"}})
	default:
		if err := restart(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unavailable", "message": err.Error()}})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func openURL(w http.ResponseWriter, r *http.Request, open func(string) error) {
	reply := func(status int, code, msg string) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if code != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
		}
	}
	if r.Method != http.MethodPost {
		reply(http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || !AllowedURL(in.URL) {
		reply(http.StatusBadRequest, "invalid_request", "that page can't be opened from the app")
		return
	}
	if open == nil {
		reply(http.StatusServiceUnavailable, "unavailable", "can't open the browser here")
		return
	}
	if err := open(in.URL); err != nil {
		reply(http.StatusServiceUnavailable, "unavailable", "couldn't open the browser: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MaxSave is the largest file the app saves (an export).
const MaxSave = 16 << 20

var saveName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,99}$`)

// saveFile saves an export where the streamer chooses, with the system's
// own save dialog (the rule: never <a download>). The UI sends the
// suggested name and the text; it never chooses the path.
func saveFile(w http.ResponseWriter, r *http.Request, save SaveFunc) {
	reply := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	fail := func(status int, code, msg string) {
		reply(status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
	}
	if r.Method != http.MethodPost {
		fail(http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	var in struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxSave+4096)).Decode(&in); err != nil || len(in.Text) > MaxSave {
		fail(http.StatusBadRequest, "invalid_request", "that can't be saved")
		return
	}
	if !saveName.MatchString(in.Name) || strings.Contains(in.Name, "..") {
		fail(http.StatusBadRequest, "invalid_request", "a file name is letters, digits, spaces and . _ -")
		return
	}
	if save == nil {
		fail(http.StatusServiceUnavailable, "unavailable", "can't save files here")
		return
	}
	path, err := save(in.Name, []byte(in.Text))
	if err != nil {
		fail(http.StatusServiceUnavailable, "unavailable", "couldn't save it: "+err.Error())
		return
	}
	if path == "" {
		reply(http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	reply(http.StatusOK, map[string]any{"saved": path})
}

// Choosable are the kinds of file the app may ask for, each with its
// dialog's title and filter: a page can name a kind, never a pattern or
// a folder. Only the path comes back; nothing is read.
var Choosable = map[string]struct{ Title, Filter, Pattern string }{
	// A Now playing overlay's files, as Pretzel writes them (D-091).
	"song_text": {"Choose the song's text file", "Text files", "*.txt"},
	"song_art":  {"Choose the album art", "Pictures", "*.jpg;*.jpeg;*.png"},
}

func chooseFile(w http.ResponseWriter, r *http.Request, choose ChooseFunc) {
	reply := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	fail := func(status int, code, msg string) {
		reply(status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
	}
	if r.Method != http.MethodPost {
		fail(http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	var in struct {
		Kind string `json:"kind"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() {
		fail(http.StatusBadRequest, "invalid_request", "say which kind of file to choose")
		return
	}
	k, ok := Choosable[in.Kind]
	if !ok {
		fail(http.StatusBadRequest, "invalid_request", "that kind of file can't be chosen")
		return
	}
	if choose == nil {
		fail(http.StatusServiceUnavailable, "unavailable", "choosing a file only works in the desktop app")
		return
	}
	p, err := choose(k.Title, k.Filter, k.Pattern)
	if err != nil {
		fail(http.StatusInternalServerError, "internal", "the file dialog failed")
		return
	}
	reply(http.StatusOK, map[string]string{"path": p})
}
