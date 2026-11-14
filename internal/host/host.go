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
	"code.streamgremlin.com/streamgremlin/internal/ads"
	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/audience"
	"code.streamgremlin.com/streamgremlin/internal/backup"
	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/buildinfo"
	"code.streamgremlin.com/streamgremlin/internal/buttons"
	"code.streamgremlin.com/streamgremlin/internal/clients"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/discord"
	"code.streamgremlin.com/streamgremlin/internal/earning"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/filters"
	"code.streamgremlin.com/streamgremlin/internal/firstwords"
	"code.streamgremlin.com/streamgremlin/internal/giveaways"
	"code.streamgremlin.com/streamgremlin/internal/gives"
	"code.streamgremlin.com/streamgremlin/internal/links"
	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/maintenance"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/obs"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/packages"
	"code.streamgremlin.com/streamgremlin/internal/plugins"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/quotes"
	"code.streamgremlin.com/streamgremlin/internal/rounds"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/sounds"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/timers"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
	"code.streamgremlin.com/streamgremlin/internal/wheels"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
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
	// DiscordAPIURL replaces discord.com, for mockdiscord. It must be a
	// loopback http URL.
	DiscordAPIURL string
	// YouTubeURL replaces Google's sign-in and YouTube's Data API, for
	// mockyoutube. It must be a loopback http URL; the app says it's a
	// test server.
	YouTubeURL string
	// OverlayAddr is where overlay pages are served for OBS. Empty means
	// brand.DefaultOverlayAddr, falling back to a free port if that is
	// taken; "off" serves none.
	OverlayAddr string
	// TestMinute shortens time in chat's minute, for tests against a
	// stand-in Twitch only (0: a real minute).
	TestMinute time.Duration
	Logger     *slog.Logger
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
	discord   *discord.Service
	youtube   *youtube.Service
	songs     *overlays.SongReader
	clocks    *overlays.Timers
	workflows *workflow.Engine
	timers    *timers.Service
	firsts    *firstwords.Service
	audience  *audience.Service
	filters   *filters.Service
	ads       *ads.Service
	giveaways *giveaways.Service
	wheels    *wheels.Service
	viewers   *viewers.Service
	links     *links.Service
	earning   *earning.Service
	gives     *gives.Service
	lua       *lua.Runner
	plugins   *plugins.Manager
	rounds    *rounds.Service
	maint     *maintenance.Service
	hub       *overlays.Hub
	ovSrv     *http.Server
	thSrv     *http.Server // wheel looks' own origin (docs/adr/0036-packages-plugins-and-themes.md)
	packages  *packages.Store
	themes    *overlays.Catalogue
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
	ytBase, err := testServerURL(opts.YouTubeURL, "", "YouTube")
	if err != nil {
		return nil, err
	}
	h.youtube = youtube.New(youtube.Options{Secrets: sec, Settings: set, Inbox: inbox, TestServer: ytBase, Logger: log})
	h.youtube.Start(ctx)
	h.obs = obs.New(obs.Options{DB: h.db, Settings: set, Secrets: sec, Inbox: inbox, Logger: log})
	if err := h.obs.Start(ctx); err != nil {
		return nil, err
	}
	qs := quotes.NewStore(h.db)
	lib, player := sounds.NewStore(h.db), sounds.NewPlayer(nil)
	ovs := overlays.NewStore(h.db, sec)
	h.hub = overlays.NewHub(nil)
	// Installed themes (docs/adr/0036-packages-plugins-and-themes.md): what
	// overlays may show, checked and served from the database.
	h.packages, h.themes = packages.NewStore(h.db), overlays.NewCatalogue()
	if themes, err := h.packages.Themes(ctx); err != nil {
		log.Error("installed wheel looks couldn't be read; wheels using them show Classic", "err", err)
	} else {
		h.themes.SetInstalled(themes)
	}
	ovs.UseThemes(h.themes)
	h.hub.UseThemes(h.themes)
	if err := h.serveOverlays(opts.OverlayAddr, ovs, lib); err != nil {
		return nil, err
	}
	// Now playing overlays' files are read while one is open (D-091).
	h.songs = overlays.NewSongReader(ovs, h.hub, log.With("component", "overlays"))
	h.songs.Start()
	// Countdowns and stopwatches on clock and timer overlays (D-092).
	h.clocks = overlays.NewTimers(ovs, h.hub, set, inbox, log.With("component", "overlays"))
	h.hub.OnTimerLoaded(func(name string) { h.clocks.Loaded(context.Background(), name) })
	h.timers = timers.New(timers.Options{DB: h.db, Inbox: inbox, Logger: log})
	h.firsts = firstwords.New(firstwords.Options{Inbox: inbox, Settings: set, Logger: log})
	h.ads = ads.New(ads.Options{DB: h.db, Twitch: h.twitch, Inbox: inbox, Logger: log})
	h.viewers = viewers.New(h.db, inbox, log)
	ledger := points.New(h.db, log)
	h.links = links.New(h.db, h.viewers, ledger, log)
	h.giveaways = giveaways.New(giveaways.Options{DB: h.db, Inbox: inbox, Twitch: h.twitch, Logger: log, Display: h.hub,
		YouTube: h.youtube, Partner: h.links.Partner})
	h.wheels = wheels.New(wheels.Options{DB: h.db, Inbox: inbox, Display: h.hub, Logger: log})
	h.earning = earning.New(earning.Options{DB: h.db, Inbox: inbox, Settings: set, Viewers: h.viewers, Points: ledger, Chatters: h.twitch, Logger: log})
	h.gives = gives.New(gives.Options{DB: h.db, Viewers: h.viewers, Points: ledger, Twitch: h.twitch, Recent: h.earning, Logger: log})
	if opts.TestMinute > 0 && opts.TwitchAuthURL != "" {
		h.earning.SetMinute(opts.TestMinute)
	}
	h.lua = lua.NewRunner(lua.Options{Logger: log})
	mods := moderation.New(h.db, h.twitch, log)
	if err := mods.Recover(ctx); err != nil {
		return nil, err
	}
	mods.UseYouTube(h.youtube)
	h.filters = filters.New(filters.Options{Inbox: inbox, Settings: set, Moderation: mods, Logger: log,
		BotID:            func() string { a, _ := h.twitch.Account(twitch.RoleBot); return a.Status().UserID },
		YouTubeQuotaLeft: h.youtube.QuotaLeft})
	discordBase, err := testServerURL(opts.DiscordAPIURL, discord.DefaultBase, "Discord")
	if err != nil {
		return nil, err
	}
	h.discord = discord.New(discord.Options{Settings: set, Secrets: sec, Logger: log.With("component", "discord"), Base: discordBase})
	reg := workflow.NewRegistry()
	actions.Register(reg, actions.Services{Filters: h.filters, Moderation: mods, Links: h.links, Lua: h.lua, Giveaways: h.giveaways, Wheels: h.wheels, Viewers: h.viewers, Points: ledger, Gives: h.gives, Ads: h.ads, FirstWords: h.firsts, Twitch: h.twitch, YouTube: h.youtube, OBS: h.obs, Timers: h.timers, Quotes: qs, Sounds: lib, Player: player,
		Overlays: ovs, Hub: h.hub, Discord: h.discord, Inbox: inbox, OverlayTimers: h.clocks})
	// Plugins' steps are registered before workflows load
	// (docs/adr/0038-lua-plugins.md).
	h.rounds = rounds.New(rounds.Options{DB: h.db, Ledger: ledger, Viewers: h.viewers, Logger: log})
	h.plugins = plugins.New(plugins.Options{Store: h.packages, Actions: reg, Chat: h.twitch, YouTube: h.youtube, Inbox: inbox, Rounds: h.rounds, Logger: log})
	if err := h.plugins.Start(ctx); err != nil {
		return nil, err
	}
	h.packages.SetCheck(h.plugins.CheckTemplates)
	vars := variables.New(h.db)
	h.workflows = workflow.New(workflow.Options{DB: h.db, Inbox: inbox, Actions: reg, Variables: vars, Logger: log})
	if err := h.workflows.Start(ctx); err != nil {
		return nil, err
	}
	// Once (D-099): workflows made before YouTube could go live keep their
	// go-live and go-offline triggers to Twitch.
	var liveMarked bool
	if ok, err := set.Load(ctx, "migrations.live_triggers_twitch_only", &liveMarked); err == nil && (!ok || !liveMarked) {
		if ids, err := h.workflows.MarkLiveTriggersTwitchOnly(ctx); err != nil {
			log.Warn("couldn't keep go-live triggers to Twitch; trying again at the next start", "err", err)
		} else {
			_ = set.Save(ctx, "migrations.live_triggers_twitch_only", true)
			if len(ids) > 0 {
				log.Info("go-live and go-offline triggers kept to Twitch", "workflows", len(ids))
			}
		}
	}
	// Once (D-102): workflows made before YouTube sent follows (public
	// subscribers), title changes and polls keep those triggers to Twitch.
	var s7Marked bool
	if ok, err := set.Load(ctx, "migrations.slice7_triggers_twitch_only", &s7Marked); err == nil && (!ok || !s7Marked) {
		if ids, err := h.workflows.MarkTriggersTwitchOnly(ctx, "audience.follow.created", "broadcast.channel.updated",
			"chat.poll.started", "chat.poll.updated", "chat.poll.ended"); err != nil {
			log.Warn("couldn't keep follow, title and poll triggers to Twitch; trying again at the next start", "err", err)
		} else {
			_ = set.Save(ctx, "migrations.slice7_triggers_twitch_only", true)
			if len(ids) > 0 {
				log.Info("follow, title and poll triggers kept to Twitch", "workflows", len(ids))
			}
		}
	}
	h.timers.Start(context.Background())
	// After workflows: active chatters and viewer counts are events they run on.
	h.audience = audience.New(audience.Options{Inbox: inbox, Logger: log,
		TwitchStream: func(ctx context.Context) (bool, int, error) {
			st, err := h.twitch.Stream(ctx)
			return st.Live, st.Viewers, err
		},
		YouTubeLive: func() bool { return h.youtube.ChatStatus().State == youtube.ChatConnected }})
	if err := h.audience.Start(ctx); err != nil {
		return nil, err
	}
	// After workflows: first words are stored as events, which they run on.
	if err := h.firsts.Start(ctx); err != nil {
		return nil, err
	}
	if err := h.ads.Start(ctx); err != nil {
		return nil, err
	}
	// After workflows: what filters catch is stored as events, which they run on.
	if err := h.filters.Start(ctx); err != nil {
		return nil, err
	}
	// A giveaway's commands and workflows' are one answer each.
	h.giveaways.SetCommands(h.workflows.CheckClaims)
	h.workflows.SetOtherClaims(h.giveaways.Claims)
	if err := h.giveaways.Start(ctx); err != nil {
		return nil, err
	}
	if err := h.wheels.Start(ctx); err != nil {
		return nil, err
	}
	// After workflows: a countdown's end is an event they run on.
	if err := h.clocks.Start(ctx); err != nil {
		return nil, err
	}
	if err := h.viewers.Start(ctx); err != nil {
		return nil, err
	}
	if err := h.earning.Start(ctx); err != nil {
		return nil, err
	}
	h.maint = maintenance.New(maintenance.Options{DB: h.db, Settings: set, DataDir: opts.DataDir, AppVersion: buildinfo.Version, Logger: log})
	h.maint.Start(context.Background())

	h.token, err = api.LoadOrCreateToken(datadir.TokenPath(opts.DataDir))
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	paired := clients.New(h.db, nil, log)
	h.api = api.NewServer(api.Options{Token: h.token, Clients: paired, Logger: log}, api.Routes(api.Deps{
		DataDir:       opts.DataDir,
		StartedAt:     startedAt,
		DB:            h.db,
		Settings:      set,
		Secrets:       sec,
		Events:        inbox,
		Twitch:        h.twitch,
		YouTube:       h.youtube,
		OBS:           h.obs,
		Workflows:     h.workflows,
		Variables:     vars,
		Timers:        h.timers,
		Quotes:        qs,
		Sounds:        lib,
		Player:        player,
		Overlays:      ovs,
		Hub:           h.hub,
		Packages:      h.packages,
		Themes:        h.themes,
		FirstWords:    h.firsts,
		Ads:           h.ads,
		Giveaways:     h.giveaways,
		Wheels:        h.wheels,
		Viewers:       h.viewers,
		Links:         h.links,
		Points:        ledger,
		Earning:       h.earning,
		Gives:         h.gives,
		Lua:           h.lua,
		Plugins:       h.plugins,
		Rounds:        h.rounds,
		Buttons:       buttons.New(h.db, inbox, nil),
		Clients:       paired,
		Moderation:    mods,
		Filters:       h.filters,
		Discord:       h.discord,
		Songs:         h.songs,
		OverlayTimers: h.clocks,
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
func (h *Host) serveOverlays(addr string, store *overlays.Store, lib *sounds.Store) error {
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
		Handler:           overlays.NewServer(store, h.hub, lib, h.log),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(h.log.Handler(), slog.LevelWarn),
	}
	go func() { _ = h.ovSrv.Serve(ln) }()
	// Wheel looks are served from a second origin, so a theme never shares
	// one with the overlay page and its connection. Any free port: the
	// page is told where in its hello.
	tln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		h.log.Warn("wheel looks other than Classic are off: couldn't listen for them", "err", err)
		return nil
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ts := overlays.NewThemeServer(h.log)
	ts.UseThemes(h.themes, h.packages.File)
	self := "http://" + tln.Addr().String()
	ts.SetOrigins(self, []string{"http://127.0.0.1:" + port, "http://localhost:" + port})
	h.hub.SetThemeOrigin(self)
	h.thSrv = &http.Server{
		Handler:           ts,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(h.log.Handler(), slog.LevelWarn),
	}
	go func() { _ = h.thSrv.Serve(tln) }()
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
	if h.songs != nil {
		h.songs.Close()
	}
	if h.clocks != nil {
		h.clocks.Close()
	}
	if h.maint != nil {
		h.maint.Close() // a backup in progress finishes first
	}
	if h.earning != nil {
		h.earning.Close()
	}
	if h.viewers != nil {
		h.viewers.Close()
	}
	if h.wheels != nil {
		h.wheels.Close() // before workflows: a spin left showing settles on the next start
	}
	if h.giveaways != nil {
		h.giveaways.Close() // before workflows: no new giveaway events while runs finish
	}
	if h.ads != nil {
		h.ads.Close() // before workflows: no new estimated ends while runs finish
	}
	if h.filters != nil {
		h.filters.Close() // before workflows: nothing more caught while runs finish
	}
	if h.firsts != nil {
		h.firsts.Close() // before workflows: no new first words while runs finish
	}
	if h.audience != nil {
		h.audience.Close() // before workflows: no new counts while runs finish
	}
	if h.timers != nil {
		h.timers.Close() // before workflows: no new turns while runs finish
	}
	if h.workflows != nil {
		h.workflows.Close() // first: running jobs still need Twitch and OBS for their finally steps
	}
	if h.lua != nil {
		h.lua.Close() // after workflows: their runs' scripts have finished
	}
	if h.plugins != nil {
		h.plugins.Close() // after workflows: their runs' calls have finished
	}
	if h.obs != nil {
		h.obs.Close()
	}
	if h.youtube != nil {
		h.youtube.Close()
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
	if h.thSrv != nil {
		_ = h.thSrv.Close()
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
// testServerURL checks a stand-in's address: a loopback http URL. Empty
// means def.
func testServerURL(raw, def, what string) (string, error) {
	if raw == "" {
		return def, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("host: the %s test server must be like http://127.0.0.1:7600, not %q", what, raw)
	}
	if err := api.RequireLoopback(u.Host); err != nil {
		return "", fmt.Errorf("host: the %s test server must be on this computer: %w", what, err)
	}
	return strings.TrimRight(raw, "/"), nil
}

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
