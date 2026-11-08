package obs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// State is the OBS connection's state. Only Connected means StreamGremlin
// can see and change OBS right now.
type State string

const (
	StateOff          State = "off"          // not turned on
	StateConnecting   State = "connecting"   // trying
	StateConnected    State = "connected"    // identified; scenes are known
	StateDisconnected State = "disconnected" // lost or unreachable; retrying
	StateNeedsAction  State = "needs_action" // wrong password, kicked, or too old: not retrying
)

// pendingWindow is how long a switch StreamGremlin asked for may take to
// show up as OBS's scene-changed event and still count as ours.
const pendingWindow = 10 * time.Second

const (
	settingsKey = "obs"
	passwordKey = "obs/password"
)

// ErrNotConnected: no live session, so nothing was sent. Never queued.
var ErrNotConnected = errors.New("obs: not connected to OBS")

// ErrInvalid wraps bad input, so the API can answer 400.
var ErrInvalid = errors.New("obs: invalid")

// Config is what the streamer sets on the Connections screen.
type Config struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// Status describes the connection. It never holds the password.
type Status struct {
	Config
	HasPassword        bool       `json:"has_password"`
	State              State      `json:"state"`
	Detail             string     `json:"detail,omitempty"`
	Since              time.Time  `json:"since"`
	OBSVersion         string     `json:"obs_version,omitempty"`
	WebSocketVersion   string     `json:"websocket_version,omitempty"`
	Scene              string     `json:"scene,omitempty"` // only while connected
	SceneSetBy         string     `json:"scene_set_by,omitempty"`
	LastManualChangeAt *time.Time `json:"last_manual_change_at,omitempty"`
	Manifest           Manifest   `json:"manifest"`
}

// Options configure a Service.
type Options struct {
	DB       *storage.DB
	Settings *settings.Store
	Secrets  *secrets.Store
	Inbox    *events.Inbox
	Logger   *slog.Logger
}

// sceneControl is who set OBS's current program scene.
type sceneControl struct {
	known    bool
	scene    string
	setBy    string // "" when changed outside StreamGremlin
	manualAt time.Time
	pending  []pendingSwitch
	lastSeen string // survives disconnects, to spot changes made meanwhile
}

type pendingSwitch struct {
	scene, by string
	at        time.Time
}

// Service keeps one OBS session open while turned on.
type Service struct {
	db       *storage.DB
	settings *settings.Store
	secrets  *secrets.Store
	inbox    *events.Inbox
	log      *slog.Logger
	now      func() time.Time
	notify   *events.Notifier

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
	kick chan struct{} // settings changed: reconnect now

	mu     sync.Mutex
	status Status
	cli    *client
	ctl    sceneControl

	// backoffMin is the first retry delay; tests shorten it.
	backoffMin time.Duration

	snapshots *Keeper
}

// New returns a Service. Start begins connecting if OBS is turned on.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Service{
		db: opts.DB, settings: opts.Settings, secrets: opts.Secrets, inbox: opts.Inbox,
		log: opts.Logger.With("component", "obs"), now: time.Now, notify: events.NewNotifier(),
		ctx: ctx, stop: stop, kick: make(chan struct{}, 1), backoffMin: time.Second,
	}
	s.status = Status{Config: Config{URL: DefaultURL}, State: StateOff, Since: s.now()}
	s.snapshots = &Keeper{svc: s}
	return s
}

// Snapshots returns the scene snapshot keeper.
func (s *Service) Snapshots() *Keeper { return s.snapshots }

// Start loads the settings, settles snapshots left open by an earlier run,
// and connects in the background.
func (s *Service) Start(ctx context.Context) error {
	cfg := s.config(ctx)
	_, err := s.secrets.Get(ctx, passwordKey)
	s.mu.Lock()
	s.status.Config = cfg
	s.status.HasPassword = err == nil
	s.mu.Unlock()
	if err := s.snapshots.settleAfterRestart(ctx); err != nil {
		return err
	}
	s.wg.Add(1)
	go s.run()
	return nil
}

// Close disconnects and stops.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

// Changes is closed at the next change of the connection's state.
func (s *Service) Changes() <-chan struct{} { return s.notify.C() }

// Status returns the connection's state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Manifest = OBSManifest()
	if s.status.State == StateConnected && s.ctl.known {
		st.Scene, st.SceneSetBy = s.ctl.scene, s.ctl.setBy
	}
	if !s.ctl.manualAt.IsZero() {
		t := s.ctl.manualAt
		st.LastManualChangeAt = &t
	}
	return st
}

func (s *Service) setState(st State, detail string) {
	s.mu.Lock()
	if st == s.status.State && detail == s.status.Detail {
		s.mu.Unlock()
		return
	}
	if st != s.status.State {
		s.status.Since = s.now()
		s.log.Info("obs state", "state", string(st), "detail", detail)
	}
	s.status.State, s.status.Detail = st, detail
	if st != StateConnected {
		s.status.OBSVersion, s.status.WebSocketVersion = "", ""
	}
	s.mu.Unlock()
	s.notify.Notify()
}

func (s *Service) config(ctx context.Context) Config {
	cfg := Config{URL: DefaultURL}
	_, _ = s.settings.Load(ctx, settingsKey, &cfg)
	if cfg.URL == "" {
		cfg.URL = DefaultURL
	}
	return cfg
}

// CheckURL accepts ws:// and wss:// addresses with a host.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: the address looks like ws://127.0.0.1:4455", ErrInvalid)
	}
	if u.Port() != "" {
		if _, err := net.LookupPort("tcp", u.Port()); err != nil {
			return fmt.Errorf("%w: the port isn't a number", ErrInvalid)
		}
	}
	return nil
}

// Configure saves the address and on/off switch, and the password when one
// is given (nil keeps it, "" clears it). It reconnects at once.
func (s *Service) Configure(ctx context.Context, cfg Config, password *string) error {
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.URL == "" {
		cfg.URL = DefaultURL
	}
	if err := CheckURL(cfg.URL); err != nil {
		return err
	}
	if password != nil {
		if *password == "" {
			if err := s.secrets.Delete(ctx, passwordKey); err != nil {
				return err
			}
		} else if err := s.secrets.Set(ctx, passwordKey, secrets.NewValue(*password)); err != nil {
			return err
		}
	}
	if err := s.settings.Save(ctx, settingsKey, cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.status.Config = cfg
	if password != nil {
		s.status.HasPassword = *password != ""
	}
	s.mu.Unlock()
	s.log.Info("obs settings changed", "enabled", cfg.Enabled, "password_changed", password != nil)
	select {
	case s.kick <- struct{}{}:
	default:
	}
	s.notify.Notify()
	return nil
}

// run keeps a session open while OBS is turned on.
func (s *Service) run() {
	defer s.wg.Done()
	backoff := s.backoffMin
	for s.ctx.Err() == nil {
		cfg := s.config(s.ctx)
		if !cfg.Enabled {
			s.setState(StateOff, "")
			if !s.waitKick(0) {
				return
			}
			continue
		}
		s.setState(StateConnecting, "")
		password := ""
		if v, err := s.secrets.Get(s.ctx, passwordKey); err == nil {
			password = v.Reveal()
		}
		start := s.now()
		err := s.session(cfg.URL, password)
		if s.ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, ErrAuthFailed):
			s.setState(StateNeedsAction, "OBS refused the password. Check it under Tools → WebSocket Server Settings in OBS, then save it here.")
			if !s.waitKick(0) {
				return
			}
			continue
		case errors.Is(err, ErrKicked):
			s.setState(StateNeedsAction, "OBS disconnected StreamGremlin (Kick, in OBS's WebSocket settings). Save the settings here to connect again.")
			if !s.waitKick(0) {
				return
			}
			continue
		case errors.Is(err, ErrUnsupported):
			s.setState(StateNeedsAction, ErrUnsupported.Error()+". Update OBS, then save the settings here.")
			if !s.waitKick(0) {
				return
			}
			continue
		}
		if s.now().Sub(start) > time.Minute {
			backoff = s.backoffMin
		}
		detail := "Can't reach OBS"
		if err != nil {
			detail = err.Error()
		}
		s.setState(StateDisconnected, strings.TrimPrefix(detail, "obs: ")+". Is OBS open, with its WebSocket server turned on? Trying again.")
		wait := backoff/2 + rand.N(backoff/2+1) //nolint:gosec // reconnect jitter, not a secret
		backoff = min(backoff*2, 30*time.Second)
		if !s.waitKick(wait) {
			return
		}
	}
}

// waitKick waits for a settings change, or d (0 waits for the change only).
// It reports false when the service is closing.
func (s *Service) waitKick(d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-s.ctx.Done():
		return false
	case <-s.kick:
		return true
	case <-timer:
		return true
	}
}

// session runs one connection until it ends.
func (s *Service) session(url, password string) error {
	c, err := dial(s.ctx, url, password)
	if err != nil {
		return err
	}
	defer c.close()
	var cur struct {
		SceneName string `json:"sceneName"`
		SceneUUID string `json:"sceneUuid"`
	}
	if err := c.request(s.ctx, "GetCurrentProgramScene", nil, &cur); err != nil {
		return err
	}
	s.mu.Lock()
	s.cli = c
	s.status.OBSVersion, s.status.WebSocketVersion = c.hello.OBSStudioVersion, c.hello.OBSWebSocketVersion
	prev := s.ctl.lastSeen
	s.ctl.known, s.ctl.scene, s.ctl.pending = true, cur.SceneName, nil
	changedMeanwhile := prev != "" && prev != cur.SceneName
	if changedMeanwhile || prev == "" {
		s.ctl.setBy = "" // nobody here set it
	}
	if changedMeanwhile {
		s.ctl.manualAt = s.now()
	}
	s.ctl.lastSeen = cur.SceneName
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cli = nil
		s.ctl.known = false
		s.mu.Unlock()
	}()
	if changedMeanwhile {
		s.emit(s.ctx, "broadcast.scene.changed", map[string]any{
			"scene_name": cur.SceneName, "scene_uuid": cur.SceneUUID, "previous_scene_name": prev,
			"changed_by": "external", "observed_on_reconnect": true,
		})
	}
	s.setState(StateConnected, "")

	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-s.kick:
			// Settings changed: reconnect with them.
			select {
			case s.kick <- struct{}{}:
			default:
			}
			return errors.New("obs: the settings changed")
		case <-c.done:
			return c.err
		case ev := <-c.events:
			if ev.Type == "ExitStarted" {
				return errors.New("obs: OBS is closing")
			}
			s.handle(s.ctx, ev)
		}
	}
}

// handle turns OBS events into StreamGremlin events. They're facts about
// OBS, whoever made the change, so they're confirmed.
func (s *Service) handle(ctx context.Context, ev Event) {
	var d map[string]any
	_ = json.Unmarshal(ev.Data, &d)
	str := func(k string) string { v, _ := d[k].(string); return v }
	b := func(k string) bool { v, _ := d[k].(bool); return v }
	switch ev.Type {
	case "CurrentProgramSceneChanged":
		s.sceneChanged(ctx, str("sceneName"), str("sceneUuid"))
	case "SceneItemEnableStateChanged":
		s.emit(ctx, "broadcast.scene_item.visibility_changed", map[string]any{
			"scene_name": str("sceneName"), "scene_item_id": d["sceneItemId"], "visible": b("sceneItemEnabled"),
		})
	case "SourceFilterEnableStateChanged":
		s.emit(ctx, "broadcast.filter.enabled_changed", map[string]any{
			"source_name": str("sourceName"), "filter_name": str("filterName"), "enabled": b("filterEnabled"),
		})
	case "InputMuteStateChanged":
		s.emit(ctx, "broadcast.audio.mute_changed", map[string]any{"source_name": str("inputName"), "muted": b("inputMuted")})
	case "MediaInputPlaybackStarted":
		s.emit(ctx, "broadcast.media.started", map[string]any{"source_name": str("inputName")})
	case "MediaInputPlaybackEnded":
		s.emit(ctx, "broadcast.media.ended", map[string]any{"source_name": str("inputName")})
	case "StreamStateChanged":
		switch str("outputState") {
		case "OBS_WEBSOCKET_OUTPUT_STARTED":
			s.emit(ctx, "broadcast.stream.started", map[string]any{})
		case "OBS_WEBSOCKET_OUTPUT_STOPPED":
			s.emit(ctx, "broadcast.stream.stopped", map[string]any{})
		}
	case "RecordStateChanged":
		switch str("outputState") {
		case "OBS_WEBSOCKET_OUTPUT_STARTED":
			s.emit(ctx, "broadcast.recording.started", map[string]any{})
		case "OBS_WEBSOCKET_OUTPUT_STOPPED":
			s.emit(ctx, "broadcast.recording.stopped", map[string]any{"output_path": str("outputPath")})
		}
	case "ReplayBufferSaved":
		s.emit(ctx, "broadcast.replay.saved", map[string]any{"path": str("savedReplayPath")})
	}
}

// sceneChanged records who changed the scene: StreamGremlin when it asked
// for this scene within the last few seconds, otherwise someone else. A
// change by someone else is what makes scene snapshots stand down.
func (s *Service) sceneChanged(ctx context.Context, scene, uuid string) {
	now := s.now()
	s.mu.Lock()
	prev := s.ctl.scene
	by := ""
	keep := s.ctl.pending[:0]
	for _, p := range s.ctl.pending {
		if now.Sub(p.at) > pendingWindow {
			continue
		}
		if by == "" && p.scene == scene {
			by = p.by
			continue
		}
		keep = append(keep, p)
	}
	s.ctl.pending = keep
	s.ctl.scene, s.ctl.setBy, s.ctl.lastSeen = scene, by, scene
	if by == "" {
		s.ctl.manualAt = now
	}
	s.mu.Unlock()
	s.notify.Notify()
	p := map[string]any{"scene_name": scene, "scene_uuid": uuid, "previous_scene_name": prev, "changed_by": "external"}
	if by != "" {
		p["changed_by"], p["requested_by"] = "streamgremlin", by
	}
	s.emit(ctx, "broadcast.scene.changed", p)
}

func (s *Service) emit(ctx context.Context, typ string, payload map[string]any) {
	if s.inbox == nil {
		return
	}
	raw, _ := json.Marshal(payload)
	if _, err := s.inbox.Accept(ctx, events.Envelope{Type: typ, Source: "obs", Payload: raw, DeliveryQuality: events.Confirmed}); err != nil {
		s.log.Warn("obs: couldn't store an event", "type", typ, "err", err)
	}
}

// session returns the live client, or ErrNotConnected.
func (s *Service) live() (*client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cli == nil || s.status.State != StateConnected {
		return nil, fmt.Errorf("%w (%s)", ErrNotConnected, s.status.State)
	}
	return s.cli, nil
}
