// Package desktop is the desktop app's side of the engine: it serves the UI
// and hands the UI's /v1/* requests to the API in-process, adding the token.
// It has no Wails code, so it builds and tests without cgo; cmd/desktop wires
// it into Wails (docs/adr/0002-local-api.md).
package desktop

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
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
// remote resources, no frames or forms.
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Handler serves the UI from assets (the built frontend) and bridges /v1/*.
func Handler(assets fs.FS, state *State) http.Handler {
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
