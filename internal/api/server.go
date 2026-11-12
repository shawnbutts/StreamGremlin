// Package api is the authenticated local API. The desktop app and the CLI
// both reach the engine only through it (docs/adr/0002-local-api.md).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/clients"
)

// maxBody caps request bodies, unless a route sets its own MaxBody.
// maxAnyBody caps every request before routing.
const (
	maxBody    = 1 << 20
	maxAnyBody = 8 << 20
)

// Route is one API endpoint.
type Route struct {
	Method  string
	Path    string
	Summary string
	// Permission the caller needs. Empty only with Public.
	Permission Permission
	// Public routes need no token: GET /v1/health, and POST /v1/pair,
	// which swaps a pairing code for a token (ADR-0042).
	Public bool
	// CLIOnly, when set, is the reason the desktop app doesn't call this
	// route. Every other route must be called by the app (AGENTS.md).
	CLIOnly string
	// MaxBody, when set, replaces the 1 MiB body limit (up to 8 MiB).
	MaxBody int64
	Handler func(w http.ResponseWriter, r *http.Request)
}

// Options configure a Server.
type Options struct {
	Token string
	// Clients checks paired clients' tokens (ADR-0042); nil when none.
	Clients ClientTokens
	Logger  *slog.Logger
	// AllowedOrigins may send an Origin header. None are needed yet: the
	// desktop app's bridge is in-process and sends no Origin.
	AllowedOrigins []string
}

// Server serves the routes.
type Server struct {
	opts    Options
	routes  []Route
	handler http.Handler
}

// NewServer builds a Server from routes. It panics on a route without a
// permission (other than the health route), because that's a programming error.
func NewServer(opts Options, routes []Route) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{opts: opts, routes: routes}
	mux := http.NewServeMux()
	byPath := map[string][]string{}
	for _, rt := range routes {
		if rt.Permission == "" && !rt.Public || rt.Permission != "" && rt.Public {
			panic("api: route " + rt.Method + " " + rt.Path + " needs a permission, or to be public")
		}
		rt := rt
		mux.HandleFunc(rt.Method+" "+rt.Path, func(w http.ResponseWriter, r *http.Request) {
			s.serveRoute(rt, w, r)
		})
		byPath[rt.Path] = append(byPath[rt.Path], rt.Method)
	}
	// Other methods on a route's path are 405, saying which it takes.
	// They're registered method by method, so /v1/x/check beside
	// /v1/x/{id} doesn't clash.
	for path, methods := range byPath {
		allow := strings.Join(sorted(methods), ", ")
		for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			if slices.Contains(methods, m) {
				continue
			}
			mux.HandleFunc(m+" "+path, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Allow", allow)
				writeError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "use "+allow)
			})
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such route")
	})
	s.handler = s.guard(mux)
	return s
}

// Routes lists the server's routes, for documentation and coverage tests.
func (s *Server) Routes() []Route { return s.routes }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// guard applies the checks every request goes through, before routing.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h := rec.Header()
		h.Set(brand.APIVersionHeader, brand.APIVersion)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		defer func() {
			// Never log headers or bodies: they can carry the token.
			s.opts.Logger.Debug("api request", "method", r.Method, "path", r.URL.Path,
				"status", rec.status, "ms", time.Since(start).Milliseconds())
		}()

		if v := r.Header.Get(brand.APIVersionHeader); v != "" && v != brand.APIVersion {
			writeError(rec, http.StatusBadRequest, CodeUnsupportedAPIVersion,
				fmt.Sprintf("this engine speaks API version %s", brand.APIVersion))
			return
		}
		if !loopbackHost(r.Host) {
			writeError(rec, http.StatusForbidden, CodeForbidden, "requests must be addressed to localhost")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
			writeError(rec, http.StatusForbidden, CodeForbidden, "origin not allowed")
			return
		}
		r.Body = http.MaxBytesReader(rec, r.Body, maxAnyBody)
		next.ServeHTTP(rec, r)
	})
}

func (s *Server) originAllowed(o string) bool {
	for _, a := range s.opts.AllowedOrigins {
		if o == a {
			return true
		}
	}
	return false
}

func (s *Server) serveRoute(rt Route, w http.ResponseWriter, r *http.Request) {
	if !rt.Public {
		tok := bearer(r)
		p, ok := Admin, tok != "" && tokenMatches(tok, s.opts.Token)
		if !ok && tok != "" && s.opts.Clients != nil {
			var c clients.Client
			if c, ok = s.opts.Clients.Authenticate(r.Context(), tok); ok {
				p = clientPrincipal(c)
			}
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "missing or wrong API token")
			return
		}
		if !p.Can(rt.Permission) {
			writeError(w, http.StatusForbidden, CodeForbidden, "missing permission "+string(rt.Permission))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
	}
	limit := int64(maxBody)
	if rt.MaxBody > 0 {
		limit = min(rt.MaxBody, maxAnyBody)
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	rt.Handler(w, r)
}

// decodeJSON reads a JSON body strictly: unknown fields and trailing data are
// errors. It writes the error response itself and returns false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body over 1 MiB")
			return false
		}
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "body must be a JSON object: "+err.Error())
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "body must hold one JSON object")
		return false
	}
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
