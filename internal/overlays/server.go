package overlays

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"

	"code.streamgremlin.com/streamgremlin/internal/sounds"
)

//go:embed page
var page embed.FS

//go:embed themes
var themeFiles embed.FS

// PageVersion identifies the page and theme files this build serves (a
// hash of them), so a page loaded from an older build can tell and reload.
var PageVersion = func() string {
	h := sha256.New()
	for _, fsys := range []embed.FS{page, themeFiles} {
		_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, _ := fsys.ReadFile(name)
			h.Write([]byte(name))
			h.Write(b)
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}()

// Subprotocol is the overlay pages' WebSocket protocol.
const Subprotocol = "streamgremlin.overlay.v1"

// DesktopOrigins may show overlays in a frame: the app's previews.
// Wails serves the app from wails://localhost on macOS and Linux, and
// http://wails.localhost on Windows.
var DesktopOrigins = []string{"wails://localhost", "http://wails.localhost"}

// CSP keeps the page to its own script and style, the platforms' picture
// host, its own connection, and a wheel look's sandboxed frame from the
// theme server (on this computer, at a port chosen as the engine starts),
// with the look's sounds from there too (D-065).
var CSP = "default-src 'none'; script-src 'self'; style-src 'self'; media-src 'self' http://127.0.0.1:*; img-src " + strings.Join(PictureHosts, " ") +
	"; connect-src 'self' ws://127.0.0.1:* ws://localhost:*; frame-src http://127.0.0.1:*; base-uri 'none'; form-action 'none'; frame-ancestors " +
	strings.Join(DesktopOrigins, " ")

// PictureHosts are where profile pictures may come from (Twitch's).
var PictureHosts = []string{"https://static-cdn.jtvnw.net"}

// readLimit caps what a page may send at once.
const readLimit = 4 << 10

// Sounds is where alerts' sounds come from: the sound library.
type Sounds interface {
	Audio(ctx context.Context, name string) (sounds.Sound, []byte, error)
}

// Server serves overlay pages at /o/<token>/.
type Server struct {
	store  *Store
	hub    *Hub
	sounds Sounds // nil: alerts have no sounds
	log    *slog.Logger
}

// NewServer returns a Server. snd may be nil.
func NewServer(store *Store, hub *Hub, snd Sounds, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{store: store, hub: hub, sounds: snd, log: log.With("component", "overlays")}
}

// loopbackHost reports whether a request's Host names this computer, so a
// web page elsewhere can't reach overlays by pointing a name at 127.0.0.1.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ServeHTTP serves a page, its files, and its connection. A link that
// doesn't work answers 404 with nothing in it: OBS shows the page to
// viewers, so it never shows an error on stream.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	if !loopbackHost(r.Host) {
		http.Error(w, "", http.StatusMisdirectedRequest)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/o/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	token, file, ok := strings.Cut(rest, "/")
	if !ok || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	o, err := s.store.ByToken(r.Context(), token)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("reading an overlay failed", "err", err)
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch file {
	case "", "index.html":
		s.file(w, "page/index.html", "text/html; charset=utf-8")
	case "overlay.js":
		s.file(w, "page/overlay.js", "text/javascript; charset=utf-8")
	case "overlay.css":
		s.file(w, "page/overlay.css", "text/css; charset=utf-8")
	case "giveaway.js":
		s.file(w, "page/giveaway.js", "text/javascript; charset=utf-8")
	case "wheel.js":
		s.file(w, "page/wheel.js", "text/javascript; charset=utf-8")
	case "themehost.js":
		s.file(w, "page/themehost.js", "text/javascript; charset=utf-8")
	case "song.js":
		s.file(w, "page/song.js", "text/javascript; charset=utf-8")
	case "ws":
		s.connect(w, r, o)
	default:
		// An alert's sound, played by the page so OBS hears it through the
		// browser source. Only under a working link.
		if name, ok := strings.CutPrefix(file, "sound/"); ok && s.sounds != nil && soundName.MatchString(name) {
			s.sound(w, r, name)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

var soundName = regexp.MustCompile(sounds.NamePattern)

func (s *Server) sound(w http.ResponseWriter, r *http.Request, name string) {
	snd, data, err := s.sounds.Audio(r.Context(), name)
	if err != nil {
		if !errors.Is(err, sounds.ErrNotFound) {
			s.log.Error("reading a sound failed", "sound", name, "err", err)
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", sounds.MediaType(snd.Format))
	http.ServeContent(w, r, "", snd.UpdatedAt, bytes.NewReader(data))
}

func (s *Server) file(w http.ResponseWriter, name, contentType string) {
	b, err := page.ReadFile(name)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", CSP)
	_, _ = w.Write(b)
}

// inbound is what a page sends.
type inbound struct {
	Type   string `json:"type"` // ack
	ID     string `json:"id"`
	Status string `json:"status"` // started, done or error
	Detail string `json:"detail"`
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request, o Overlay) {
	// The default check: a browser's Origin must be the page's own host.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		return // Accept has answered
	}
	if ws.Subprotocol() != Subprotocol {
		_ = ws.Close(websocket.StatusPolicyViolation, "use "+Subprotocol)
		return
	}
	ws.SetReadLimit(readLimit)
	preview := r.URL.Query().Get("preview") == "1"
	c := s.hub.join(o, preview)
	defer s.hub.leave(c)
	s.log.Info("overlay opened", "overlay", o.Name, "preview", preview)
	defer s.log.Info("overlay closed", "overlay", o.Name, "preview", preview)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			var in inbound
			_, b, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if json.Unmarshal(b, &in) != nil {
				continue
			}
			switch in.Type {
			case "ack":
				s.hub.ack(c, in.ID, in.Status, in.Detail)
			case "theme":
				// A wheel look said it's ready, or that a spin showed in
				// Classic. Only logged; it changes nothing.
				detail := in.Detail
				if len(detail) > 200 {
					detail = detail[:200]
				}
				s.log.Info("wheel look", "overlay", o.Name, "preview", preview, "status", in.Status, "detail", detail)
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b := <-c.send:
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			err := ws.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-c.kick:
			_ = ws.Close(websocket.StatusGoingAway, "reconnect")
			return
		case <-ctx.Done():
			_ = ws.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}
