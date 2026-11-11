package overlays

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
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
	themes    *Catalogue
	files     PackageFiles
}

// PackageFiles reads one file of an installed package's version.
type PackageFiles func(ctx context.Context, id, version, path string) ([]byte, error)

// UseThemes serves installed themes' files: those of the versions c
// lists, read with files.
func (t *ThemeServer) UseThemes(c *Catalogue, files PackageFiles) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.themes, t.files = c, files
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
// own files only, no connections, frames, workers, forms or base URL, no
// sound of its own (the overlay page plays a look's sounds, D-065), and
// framed only by overlay pages and the app's previews.
func (t *ThemeServer) ThemeCSP() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	own := "'self'"
	if t.self != "" {
		own += " " + t.self
	}
	return "sandbox allow-scripts; default-src 'none'; script-src " + own + "; style-src " + own + "; img-src " + own + " data:; font-src " + own +
		"; media-src 'none'; connect-src 'none'; frame-src 'none'; worker-src 'none'; manifest-src 'none'; base-uri 'none'; form-action 'none'" +
		"; frame-ancestors " + strings.Join(append(append([]string(nil), t.ancestors...), DesktopOrigins...), " ")
}

// themeTypes are the files a theme may have, by extension.
var themeTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".json": "application/json", ".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif", ".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".otf": "font/otf",
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".txt": "text/plain; charset=utf-8",
	".md": "text/plain; charset=utf-8",
}

var (
	packageVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]{1,40})?$`)
	packageDigest  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	packagePath    = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

var themeFile = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// ServeHTTP serves /t/builtin/<page version>/<look or shared>/<file>, and
// installed themes' files at /t/<id>/<version>/<digest>/<path>. Anything
// else is 404 with nothing in it.
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
	if strings.HasPrefix(r.URL.Path, "/t/builtin/") {
		t.serveBuiltin(w, r)
		return
	}
	t.serveInstalled(w, r)
}

func (t *ThemeServer) serveBuiltin(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/t/builtin/"), "/")
	if len(parts) != 3 || parts[0] != PageVersion || !themeFile.MatchString(parts[2]) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	dir, file := parts[1], parts[2]
	known := dir == "shared"
	for _, th := range Themes {
		known = known || th.dir == dir
	}
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, err := themeFiles.ReadFile("themes/" + dir + "/" + file)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	t.write(w, r, file, b)
}

// serveInstalled serves /t/<id>/<version>/<digest>/<path>: a file of an
// installed theme's version, only while it's the one installed.
func (t *ThemeServer) serveInstalled(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/t/"), "/", 4)
	t.mu.RLock()
	themes, files := t.themes, t.files
	t.mu.RUnlock()
	if !strings.HasPrefix(r.URL.Path, "/t/") || len(parts) != 4 || files == nil || !installedID.MatchString(parts[0]) ||
		!packageVersion.MatchString(parts[1]) || !packageDigest.MatchString(parts[2]) || len(parts[3]) > 200 ||
		!packagePath.MatchString(parts[3]) || !themes.serves(parts[0], parts[1], parts[2]) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	for _, seg := range strings.Split(parts[3], "/") {
		if seg == "." || seg == ".." {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}
	b, err := files(r.Context(), parts[0], parts[1], parts[3])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	t.write(w, r, parts[3], b)
}

// write answers with a theme's file, its type from its extension.
func (t *ThemeServer) write(w http.ResponseWriter, r *http.Request, name string, b []byte) {
	ctype, ok := themeTypes[strings.ToLower(path.Ext(name))]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Security-Policy", t.ThemeCSP())
	// With its length, and ranges, which audio players ask for.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}
