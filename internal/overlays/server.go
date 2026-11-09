package overlays

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

//go:embed page
var page embed.FS

// Subprotocol is the overlay pages' WebSocket protocol.
const Subprotocol = "streamgremlin.overlay.v1"

// DesktopOrigins may show overlays in a frame: the app's previews.
// Wails serves the app from wails://localhost on macOS and Linux, and
// http://wails.localhost on Windows.
var DesktopOrigins = []string{"wails://localhost", "http://wails.localhost"}

// CSP keeps the page to its own script and style, the platforms' picture
// host, and its own connection.
var CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src " + strings.Join(PictureHosts, " ") +
	"; connect-src 'self' ws://127.0.0.1:* ws://localhost:*; base-uri 'none'; form-action 'none'; frame-ancestors " +
	strings.Join(DesktopOrigins, " ")

// PictureHosts are where profile pictures may come from (Twitch's).
var PictureHosts = []string{"https://static-cdn.jtvnw.net"}

// readLimit caps what a page may send at once.
const readLimit = 4 << 10

// Server serves overlay pages at /o/<token>/.
type Server struct {
	store *Store
	hub   *Hub
	log   *slog.Logger
}

// NewServer returns a Server.
func NewServer(store *Store, hub *Hub, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{store: store, hub: hub, log: log.With("component", "overlays")}
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
	case "ws":
		s.connect(w, r, o)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
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
			if json.Unmarshal(b, &in) == nil && in.Type == "ack" {
				s.hub.ack(c, in.ID, in.Status, in.Detail)
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
