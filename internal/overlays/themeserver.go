package overlays

import (
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
)

// ThemeServer serves wheel looks' files, and nothing else, from its own
// loopback origin (docs/adr/0036-packages-plugins-and-themes.md). The
// overlay page shows them in a sandboxed frame. Every answer carries a
// policy that sandboxes the page even if it's opened directly, and allows
// no network: a theme can only load its own files.
type ThemeServer struct {
	log *slog.Logger

	mu        sync.RWMutex
	self      string   // this server's origin, like http://127.0.0.1:50123
	ancestors []string // the overlay pages' origins, which may frame a theme
}

// NewThemeServer returns a ThemeServer. Call SetOrigins once it listens.
func NewThemeServer(log *slog.Logger) *ThemeServer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ThemeServer{log: log.With("component", "themes")}
}

// SetOrigins says where this server is and where overlay pages are.
func (t *ThemeServer) SetOrigins(self string, overlays []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.self = self
	t.ancestors = append([]string(nil), overlays...)
}

// ThemeCSP is the policy on every theme file: sandboxed with scripts only
// (no same origin, so no storage and no reading the overlay page), its
// own files only, no connections, frames, workers, forms or base URL, and
// framed only by overlay pages and the app's previews.
func (t *ThemeServer) ThemeCSP() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	own := "'self'"
	if t.self != "" {
		own += " " + t.self
	}
	return "sandbox allow-scripts; default-src 'none'; script-src " + own + "; style-src " + own + "; img-src " + own + " data:; font-src " + own +
		"; media-src " + own + "; connect-src 'none'; frame-src 'none'; worker-src 'none'; manifest-src 'none'; base-uri 'none'; form-action 'none'" +
		"; frame-ancestors " + strings.Join(append(append([]string(nil), t.ancestors...), DesktopOrigins...), " ")
}

// themeTypes are the files a theme may have, by extension.
var themeTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".json": "application/json", ".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif", ".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".otf": "font/otf",
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".txt": "text/plain; charset=utf-8",
}

var themeFile = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// ServeHTTP serves /t/builtin/<page version>/<look or shared>/<file>.
// Anything else is 404 with nothing in it.
func (t *ThemeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	// A sandboxed page's origin is opaque, so its scripts (modules) and
	// fonts are cross-origin requests. Nothing here is private.
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	if !loopbackHost(r.Host) {
		http.Error(w, "", http.StatusMisdirectedRequest)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/t/builtin/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/t/builtin/") || len(parts) != 3 || parts[0] != PageVersion || !themeFile.MatchString(parts[2]) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	dir, file := parts[1], parts[2]
	known := dir == "shared"
	for _, th := range Themes {
		known = known || th.dir == dir
	}
	ctype, ok := themeTypes[path.Ext(file)]
	if !known || !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, err := themeFiles.ReadFile("themes/" + dir + "/" + file)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Security-Policy", t.ThemeCSP())
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b) //nolint:gosec // a built-in look's own file, embedded in the build; the request only picks which
}
