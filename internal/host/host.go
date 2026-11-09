// Package host starts and stops the engine: data folder lock, database,
// services and the local API. The desktop app and the dev `engine` command
// both use it, so they run identical services.
package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/actions"
	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/backup"
	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/buildinfo"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/maintenance"
	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/quotes"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/sounds"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/timers"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Options configure Start.
type Options struct {
	DataDir string
	// Addr is the loopback address to listen on. Empty means
	// brand.DefaultAPIAddr, falling back to a free port if that is taken.
	Addr string
	// SecretStore is the secret store asked for. Empty means auto: keep the
	// one this data folder uses, choosing on first start.
	SecretStore secrets.Kind
	// TwitchAuthURL replaces id.twitch.tv, for mocktwitch. It must be a
	// loopback http URL; the app says it's a test server.
	TwitchAuthURL string
	// OverlayAddr is where overlay pages are served for OBS. Empty means
	// brand.DefaultOverlayAddr, falling back to a free port if that is
	// taken; "off" serves none.
	OverlayAddr string
	Logger      *slog.Logger
}

// Host is a running engine.
type Host struct {
	dataDir   string
	lock      *datadir.Lock
	db        *storage.DB
	api       *api.Server
	srv       *http.Server
	ln        net.Listener
	token     string
	twitch    *twitch.Service
	obs       *obs.Service
	workflows *workflow.Engine
	timers    *timers.Service
	maint     *maintenance.Service
	hub       *overlays.Hub
	ovSrv     *http.Server
	ovLn      net.Listener
	ovNote    string
	log       *slog.Logger
	done      chan error
}

// Start brings the engine up. If another process holds the data folder the
// error matches datadir.ErrLocked.
func Start(ctx context.Context, opts Options) (_ *Host, err error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := datadir.Ensure(opts.DataDir); err != nil {
		return nil, err
	}
	lock, err := datadir.Acquire(opts.DataDir)
	if err != nil {
		return nil, err
	}
	h := &Host{dataDir: opts.DataDir, lock: lock, log: log, done: make(chan error, 1)}
	defer func() {
		if err != nil {
			h.cleanup()
		}
	}()

	// A restore chosen in the app is applied now, before anything opens the
	// database (docs/adr/0012-backups-and-retention.md).
	if r := backup.ApplyPending(ctx, opts.DataDir, buildinfo.Version, time.Now()); r != nil {
		if r.OK {
			log.Info("restored a backup", "from", r.From, "pre_restore", r.PreRestore, "kept_aside", r.KeptAside)
		} else {
			log.Error("restoring a backup failed; nothing was changed", "from", r.From, "err", r.Error)
		}
	}

	h.db, err = storage.Open(ctx, datadir.DatabasePath(opts.DataDir))
	if err != nil {
		return nil, err
	}
	res, err := h.db.Migrate(ctx, datadir.BackupDir(opts.DataDir))
	if err != nil {
		return nil, err
	}
	if res.From != res.To {
		log.Info("database migrated", "from", res.From, "to", res.To, "backup", res.Backup)
	}

	set := settings.NewStore(h.db)
	sec, err := secrets.Open(ctx, h.db, secrets.Options{DataDir: opts.DataDir, Want: opts.SecretStore, Logger: log})
	if err != nil {
		return nil, err
	}

	authURL, err := twitchAuthURL(opts.TwitchAuthURL)
	if err != nil {
		return nil, err
	}
	inbox := events.NewInbox(h.db, log)
	h.twitch = twitch.New(twitch.Options{Secrets: sec, Settings: set, AuthURL: authURL, Inbox: inbox, Logger: log})
	h.twitch.Start(ctx)
	h.obs = obs.New(obs.Options{DB: h.db, Settings: set, Secrets: sec, Inbox: inbox, Logger: log})
	if err := h.obs.Start(ctx); err != nil {
		return nil, err
	}
	qs := quotes.NewStore(h.db)
	lib, player := sounds.NewStore(h.db), sounds.NewPlayer(nil)
	ovs := overlays.NewStore(h.db, sec)
	h.hub = overlays.NewHub(nil)
	if err := h.serveOverlays(opts.OverlayAddr, ovs); err != nil {
		return nil, err
	}
	h.timers = timers.New(timers.Options{DB: h.db, Inbox: inbox, Logger: log})
	reg := workflow.NewRegistry()
	actions.Register(reg, actions.Services{Twitch: h.twitch, OBS: h.obs, Timers: h.timers, Quotes: qs, Sounds: lib, Player: player,
		Overlays: ovs, Hub: h.hub})
	vars := variables.New(h.db)
	h.workflows = workflow.New(workflow.Options{DB: h.db, Inbox: inbox, Actions: reg, Variables: vars, Logger: log})
	if err := h.workflows.Start(ctx); err != nil {
		return nil, err
	}
	h.timers.Start(context.Background())
	h.maint = maintenance.New(maintenance.Options{DB: h.db, Settings: set, DataDir: opts.DataDir, AppVersion: buildinfo.Version, Logger: log})
	h.maint.Start(context.Background())

	h.token, err = api.LoadOrCreateToken(datadir.TokenPath(opts.DataDir))
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	h.api = api.NewServer(api.Options{Token: h.token, Logger: log}, api.Routes(api.Deps{
		DataDir:   opts.DataDir,
		StartedAt: startedAt,
		DB:        h.db,
		Settings:  set,
		Secrets:   sec,
		Events:    inbox,
		Twitch:    h.twitch,
		OBS:       h.obs,
		Workflows: h.workflows,
		Variables: vars,
		Timers:    h.timers,
		Quotes:    qs,
		Sounds:    lib,
		Player:    player,
		Overlays:  ovs,
		Hub:       h.hub,
		OverlayServer: func() (string, string) {
			return h.OverlayURL(), h.ovNote
		},
		Maintenance: h.maint,
		Logger:      log,
	}))

	h.ln, err = listen(opts.Addr)
	if err != nil {
		return nil, err
	}
	h.srv = &http.Server{
		Handler:           h.api,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	if err := writePrivate(datadir.AddrPath(opts.DataDir), h.URL()+"\n"); err != nil {
		return nil, err
	}
	go func() {
		err := h.srv.Serve(h.ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		h.done <- err
	}()
	log.Info("engine started", "api", h.URL(), "overlays", h.OverlayURL(), "data_dir", opts.DataDir, "schema", res.To, "secret_store", string(sec.Kind()))
	return h, nil
}

func listen(addr string) (net.Listener, error) {
	fallback := addr == ""
	if fallback {
		addr = brand.DefaultAPIAddr
	}
	if err := api.RequireLoopback(addr); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && fallback && errors.Is(err, syscall.EADDRINUSE) {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, fmt.Errorf("host: listen on %s: %w", addr, err)
	}
	return ln, nil
}

// serveOverlays starts the overlay server for OBS (docs/adr/0017-alert-overlays.md).
func (h *Host) serveOverlays(addr string, store *overlays.Store) error {
	if addr == "off" {
		return nil
	}
	fallback := addr == ""
	if fallback {
		addr = brand.DefaultOverlayAddr
	}
	if err := api.RequireLoopback(addr); err != nil {
		return fmt.Errorf("host: overlays: %w", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && fallback && errors.Is(err, syscall.EADDRINUSE) {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			h.ovNote = "The usual address, " + brand.DefaultOverlayAddr + ", is taken by another program, so overlays are at " +
				"http://" + ln.Addr().String() + " until StreamGremlin restarts. Links pasted into OBS before won't work meanwhile."
			h.log.Warn("the usual overlay address is taken; using another until restart", "usual", brand.DefaultOverlayAddr, "now", ln.Addr().String())
		}
	}
	if err != nil {
		return fmt.Errorf("host: overlays: listen on %s: %w", addr, err)
	}
	h.ovLn = ln
	h.ovSrv = &http.Server{
		Handler:           overlays.NewServer(store, h.hub, h.log),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(h.log.Handler(), slog.LevelWarn),
	}
	go func() { _ = h.ovSrv.Serve(ln) }()
	return nil
}

// OverlayURL is where overlay pages are served, or "" when that's off.
func (h *Host) OverlayURL() string {
	if h.ovLn == nil {
		return ""
	}
	return "http://" + h.ovLn.Addr().String()
}

// URL is the API's base URL, for example http://127.0.0.1:7430.
func (h *Host) URL() string { return "http://" + h.ln.Addr().String() }

// Handler is the API handler, for the desktop app's in-process bridge.
func (h *Host) Handler() http.Handler { return h.api }

// Token is the API token, for in-process callers only. Never log it.
func (h *Host) Token() string { return h.token }

// Done reports when the API server stops on its own.
func (h *Host) Done() <-chan error { return h.done }

// Close stops the engine cleanly and releases the data folder.
func (h *Host) Close(ctx context.Context) error {
	var err error
	if h.srv != nil {
		err = h.srv.Shutdown(ctx)
	}
	h.cleanup()
	h.log.Info("engine stopped")
	return err
}

func (h *Host) cleanup() {
	if h.maint != nil {
		h.maint.Close() // a backup in progress finishes first
	}
	if h.timers != nil {
		h.timers.Close() // before workflows: no new turns while runs finish
	}
	if h.workflows != nil {
		h.workflows.Close() // first: running jobs still need Twitch and OBS for their finally steps
	}
	if h.obs != nil {
		h.obs.Close()
	}
	if h.twitch != nil {
		h.twitch.Close()
	}
	if h.hub != nil {
		h.hub.CloseAll() // pages hold connections the server doesn't track
	}
	if h.ovSrv != nil {
		_ = h.ovSrv.Close()
	}
	if h.ln != nil {
		_ = os.Remove(datadir.AddrPath(h.dataDir))
		_ = h.ln.Close()
	}
	if h.db != nil {
		_ = h.db.Close()
	}
	_ = h.lock.Release()
}

// writePrivate writes a 0600 file atomically.
func writePrivate(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// twitchAuthURL checks a replacement for id.twitch.tv: only a loopback
// http server such as mocktwitch, so nothing can point sign-in elsewhere.
func twitchAuthURL(raw string) (string, error) {
	if raw == "" {
		return twitch.DefaultAuthURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("host: the Twitch test server must be like http://127.0.0.1:7441, not %q", raw)
	}
	if err := api.RequireLoopback(u.Host); err != nil {
		return "", fmt.Errorf("host: the Twitch test server must be on this computer: %w", err)
	}
	return strings.TrimRight(raw, "/"), nil
}
