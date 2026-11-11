// Package plugins runs installed Lua plugins (docs/adr/0038-lua-plugins.md):
// their steps in workflows, one worker for each that's on, restarting one
// that dies, doing what each asks within its approved permissions, and
// recording every call it makes.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/packages"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Limits on what a plugin asks for.
const (
	MaxChatRunes    = 500
	ChatPerMinute   = 20
	EventsPerSecond = 10
	MaxCrashes      = 5
	CrashWindow     = 10 * time.Minute
	activityPerRun  = lua.MaxLogLines
)

// Chat sends to the streamer's chat (the Twitch service, or a stand-in).
type Chat interface {
	SendChat(ctx context.Context, req twitch.SendRequest) (twitch.SendResult, error)
}

// Workflows is what the manager needs from the workflow engine.
type Workflows interface {
	UsingActions(ctx context.Context, prefix string) ([]workflow.Summary, error)
	UsingTriggers(ctx context.Context, prefix string) ([]workflow.Summary, error)
	SetEnabled(ctx context.Context, id string, on bool) error
}

// Inbox takes the events plugins send (the event inbox).
type Inbox interface {
	Accept(ctx context.Context, env events.Envelope) (events.Result, error)
}

// Options configure a Manager.
type Options struct {
	Store   *packages.Store
	Actions *workflow.Registry
	Chat    Chat
	// Inbox takes plugins' events (D-073); without it they're refused.
	Inbox Inbox
	// Lua configures the workers (tests point Command at themselves).
	Lua    lua.Options
	Logger *slog.Logger
	Now    func() time.Time
}

// Manager runs the installed plugins.
type Manager struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	running map[string]*running
	actions map[string][]string // registered step names, by plugin
	closed  bool
}

// running is a plugin that's on.
type running struct {
	mu      sync.Mutex // held while (re)starting
	spec    lua.PluginSpec
	plugin  *lua.Plugin
	crashes []time.Time
	chat    []time.Time // sends in the last minute
	emits   []time.Time // events in the last second
	events  map[string]bool
	timer   *time.Timer // for the plugin's next timer
	stopped bool
}

// New returns a Manager. Start registers the plugins' steps.
func New(opts Options) *Manager {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	opts.Lua.Logger = log
	return &Manager{opts: opts, log: log, running: map[string]*running{}, actions: map[string][]string{}}
}

// Errors.
var (
	// ErrOff: the plugin is off, failed or not installed.
	ErrOff = errors.New("plugins: off")
	// ErrLoad: turning it on failed: its script didn't load.
	ErrLoad = errors.New("plugins: didn't load")
)

// Start registers every installed plugin's steps, before workflows load,
// and starts those left on. One that won't load is turned off, saying why.
func (m *Manager) Start(ctx context.Context) error {
	ps, ms, err := m.opts.Store.Plugins(ctx)
	if err != nil {
		return err
	}
	for i, p := range ps {
		m.register(p, ms[i])
	}
	for _, p := range ps {
		if p.Plugin.Enabled {
			if err := m.start(ctx, p.ID); err != nil {
				m.log.Warn("plugin left off: it didn't load", "plugin", p.ID, "err", err)
			}
		}
	}
	return nil
}

// Close stops every worker.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	all := m.running
	m.running = map[string]*running{}
	m.mu.Unlock()
	for _, r := range all {
		r.stop()
	}
}

func (r *running) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	if r.timer != nil {
		r.timer.Stop()
	}
	if r.plugin != nil {
		r.plugin.Close()
	}
}

// Installed registers a newly installed plugin's steps. It's off.
func (m *Manager) Installed(ctx context.Context, id string) error {
	p, man, err := m.opts.Store.Plugin(ctx, id)
	if err != nil {
		return err
	}
	m.register(p, man)
	return nil
}

// register adds a plugin's steps to the workflow registry.
func (m *Manager) register(p packages.Package, man packages.Manifest) {
	m.unregister(p.ID)
	var names []string
	for _, a := range man.Lua.Actions {
		name := p.ID + "." + a.Name
		m.opts.Actions.Add(workflow.Action{
			Name: name, Label: a.Label, Group: p.Name, External: true, AddOn: p.ID,
			Description: fmt.Sprintf("%s From %s, published by %s (not verified).", strings.TrimSpace(a.Description), p.Name, p.Publisher),
			Params:      params(a.Params),
			Run:         m.step(p.ID, p.Name, a),
		})
		names = append(names, name)
	}
	var triggers []workflow.TriggerEntry
	for _, e := range man.Lua.Events {
		t := workflow.TriggerEntry{Type: packages.EventType(p.ID, e.Name), Label: e.Label, Category: p.Name,
			Help: fmt.Sprintf("%s From %s, published by %s (not verified).", strings.TrimSpace(e.Description), p.Name, p.Publisher)}
		for _, v := range e.Values {
			t.Values = append(t.Values, workflow.Value{Token: v.Name, Label: v.Label, Expr: "event.payload." + v.Name})
		}
		triggers = append(triggers, t)
	}
	m.opts.Actions.SetTriggers(p.ID, triggers)
	m.mu.Lock()
	m.actions[p.ID] = names
	m.mu.Unlock()
}

func (m *Manager) unregister(id string) {
	m.mu.Lock()
	names := m.actions[id]
	delete(m.actions, id)
	m.mu.Unlock()
	for _, n := range names {
		m.opts.Actions.Remove(n)
	}
	m.opts.Actions.SetTriggers(id, nil)
}

// params are a plugin action's values as workflow parameters: text can
// use {viewer} and the rest, like a message.
func params(ps []packages.Param) []workflow.Param {
	out := []workflow.Param{}
	for _, p := range ps {
		wp := workflow.Param{Name: p.Name, Label: p.Label, Required: p.Required, Default: p.Default, DefaultHint: p.Hint}
		switch p.Kind {
		case "text":
			wp.Type, wp.Editor = "text", "message"
		case "number":
			// Text, so it can come from chat ({first_word}); the plugin's
			// own range is checked when the step runs.
			wp.Type = "text"
			if wp.DefaultHint == "" {
				wp.DefaultHint = "A number" + numberRange(p)
			}
		case "switch":
			wp.Type = "bool"
		case "choice":
			wp.Type, wp.Choices = "choice", p.Options
			if !p.Required && wp.DefaultHint == "" {
				wp.DefaultHint = "Leave it to the plugin"
			}
		}
		out = append(out, wp)
	}
	return out
}

// step runs a plugin's action for a workflow.
func (m *Manager) step(id, name string, a packages.Action) func(ctx context.Context, c workflow.Call) (any, error) {
	return func(ctx context.Context, c workflow.Call) (any, error) {
		args := map[string]any{}
		for _, p := range a.Params {
			v, ok := c.Args[p.Name]
			if s, isText := v.(string); isText && p.Kind != "text" && strings.TrimSpace(s) == "" {
				ok = false // nothing typed after the command: the default
			}
			if !ok || v == nil {
				if p.Default != nil {
					args[p.Name] = p.Default
				}
				continue
			}
			v = typed(p, v)
			if err := p.Check(v); err != nil {
				return nil, errors.New(strings.TrimPrefix(err.Error(), packages.ErrInvalid.Error()+": "))
			}
			args[p.Name] = v
		}
		res, err := m.Call(ctx, id, a.Name, args, c.Event, c.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		return res.Value, nil
	}
}

// typed turns text a step gave (a number typed as text, "true") into the
// value's kind, as the editor's values arrive.
func typed(p packages.Param, v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch p.Kind {
	case "number":
		var f float64
		if _, err := fmt.Sscan(strings.TrimSpace(s), &f); err == nil {
			return f
		}
	case "switch":
		switch s {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return v
}

// SetEnabled turns a plugin on (loading it: a script that won't load is
// ErrLoad, and it's left off saying why) or off.
func (m *Manager) SetEnabled(ctx context.Context, id string, on bool) error {
	p, _, err := m.opts.Store.Plugin(ctx, id)
	if err != nil {
		return err
	}
	if !on {
		m.stop(id)
		return m.opts.Store.SetEnabled(ctx, id, false, m.opts.Now())
	}
	if p.Plugin.Enabled && m.isRunning(id) {
		return nil
	}
	if err := m.start(ctx, id); err != nil {
		return err
	}
	return m.opts.Store.SetEnabled(ctx, id, true, m.opts.Now())
}

func (m *Manager) isRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.running[id]
	return ok
}

func (m *Manager) stop(id string) {
	m.mu.Lock()
	r := m.running[id]
	delete(m.running, id)
	m.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

// start loads a plugin into a new worker. If it won't load, it's turned
// off, saying why.
func (m *Manager) start(ctx context.Context, id string) error {
	p, man, err := m.opts.Store.Plugin(ctx, id)
	if err != nil {
		return err
	}
	script, err := m.opts.Store.File(ctx, id, p.Version, man.Lua.Script)
	if err != nil {
		return err
	}
	approved, err := m.opts.Store.Approved(ctx, id)
	if err != nil {
		return err
	}
	var actions []string
	for _, a := range man.Lua.Actions {
		actions = append(actions, a.Name)
	}
	spec := lua.PluginSpec{ID: id, Name: p.Name, Version: p.Version, Script: string(script), Settings: p.Plugin.Values,
		Permissions: approved, Actions: actions, Timeout: time.Duration(man.Lua.TimeoutSeconds()) * time.Second}
	r := &running{spec: spec, events: map[string]bool{}}
	for _, e := range man.Lua.Events {
		r.events[e.Name] = true
	}
	plugin, res, err := lua.StartPlugin(m.opts.Lua, spec, m.host(id, p.Version, r))
	m.logLines(ctx, id, p.Version, res.Log, res.LogDropped)
	if err != nil {
		why := "it didn't load: " + err.Error()
		_ = m.opts.Store.Fail(ctx, id, why, m.opts.Now())
		return fmt.Errorf("%w: %s", ErrLoad, why)
	}
	r.plugin = plugin
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		plugin.Close()
		return ErrOff
	}
	old := m.running[id]
	m.running[id] = r
	m.mu.Unlock()
	if old != nil {
		old.stop()
	}
	m.schedule(id, r)
	return nil
}

// Restart loads a running plugin again (its settings changed): its state
// starts over.
func (m *Manager) Restart(ctx context.Context, id string) error {
	if !m.isRunning(id) {
		return nil
	}
	return m.start(ctx, id)
}

// Call runs a plugin's action. A worker that died is started again, fresh;
// MaxCrashes in CrashWindow turn the plugin off as failed.
func (m *Manager) Call(ctx context.Context, id, action string, args, event map[string]any, key string) (lua.Result, error) {
	m.mu.Lock()
	r := m.running[id]
	m.mu.Unlock()
	if r == nil {
		p, err := m.opts.Store.Get(ctx, id)
		switch {
		case errors.Is(err, packages.ErrNotFound):
			return lua.Result{}, fmt.Errorf("%w: the plugin %s isn't installed", ErrOff, id)
		case err != nil:
			return lua.Result{}, err
		case p.Plugin != nil && p.Plugin.Error != "":
			return lua.Result{}, fmt.Errorf("%w: %s is off: %s. Turn it on again on Add-ons", ErrOff, p.Name, p.Plugin.Error)
		}
		return lua.Result{}, fmt.Errorf("%w: %s is off: turn it on on Add-ons", ErrOff, p.Name)
	}
	plugin, err := m.alive(ctx, id, r)
	if err != nil {
		return lua.Result{}, err
	}
	res, err := plugin.Call(ctx, action, args, event, key)
	m.schedule(id, r)
	m.logLines(ctx, id, r.spec.Version, res.Log, res.LogDropped)
	var le *lua.Error
	if errors.As(err, &le) {
		m.logLines(ctx, id, r.spec.Version, le.Log, le.LogDropped)
		if plugin.Dead() {
			m.crashed(ctx, id, r, le.Message)
		}
		return res, fmt.Errorf("%s: %s", r.spec.Name, le.Message)
	}
	return res, err
}

// schedule sets the plugin's next timer going, replacing the one before:
// the worker says which are waiting after each call.
func (m *Manager) schedule(id string, r *running) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if r.stopped || r.plugin == nil {
		return
	}
	tid, at, ok := r.plugin.NextTimer()
	if !ok {
		return
	}
	r.timer = time.AfterFunc(max(time.Until(at), 0), func() { m.fire(id, r, tid) })
}

// fire runs a plugin's timer that's due, one at a time with its calls. A
// timer that fails is in its activity; one that stops its worker counts as
// a crash.
func (m *Manager) fire(id string, r *running, timer int) {
	ctx := context.Background()
	plugin, err := m.alive(ctx, id, r)
	if err != nil {
		return
	}
	res, err := plugin.Fire(ctx, timer)
	m.logLines(ctx, id, r.spec.Version, res.Log, res.LogDropped)
	var le *lua.Error
	if errors.As(err, &le) {
		m.logLines(ctx, id, r.spec.Version, le.Log, le.LogDropped)
		m.activity(ctx, id, r.spec.Version, "timer", "failed", le.Message)
		m.log.Warn("plugin timer failed", "plugin", id, "err", le.Message)
		if plugin.Dead() {
			m.crashed(ctx, id, r, le.Message)
		}
	}
	m.schedule(id, r)
}

// alive returns the plugin's worker, starting it again if it died.
func (m *Manager) alive(ctx context.Context, id string, r *running) (*lua.Plugin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, fmt.Errorf("%w: %s was turned off", ErrOff, r.spec.Name)
	}
	if !r.plugin.Dead() {
		return r.plugin, nil
	}
	plugin, res, err := lua.StartPlugin(m.opts.Lua, r.spec, m.host(id, r.spec.Version, r))
	m.logLines(ctx, id, r.spec.Version, res.Log, res.LogDropped)
	if err != nil {
		return nil, fmt.Errorf("%s: its worker didn't start again: %w", r.spec.Name, err)
	}
	r.plugin = plugin
	m.log.Info("plugin worker started again", "plugin", id)
	return plugin, nil
}

// crashed counts a worker that died; too many and the plugin's off.
func (m *Manager) crashed(ctx context.Context, id string, r *running, why string) {
	now := m.opts.Now()
	r.mu.Lock()
	keep := r.crashes[:0]
	for _, t := range r.crashes {
		if now.Sub(t) < CrashWindow {
			keep = append(keep, t)
		}
	}
	r.crashes = append(keep, now)
	n := len(r.crashes)
	r.mu.Unlock()
	m.log.Warn("plugin worker stopped", "plugin", id, "why", why, "in_the_last_10_minutes", n)
	if n >= MaxCrashes {
		m.stop(id)
		_ = m.opts.Store.Fail(ctx, id, fmt.Sprintf("its worker stopped %d times in 10 minutes (last: %s)", n, why), now)
	}
}

// logLines keeps a call's log lines in the plugin's activity.
func (m *Manager) logLines(ctx context.Context, id, version string, lines []string, dropped int) {
	for i, l := range lines {
		if i >= activityPerRun {
			break
		}
		m.activity(ctx, id, version, "log", "ok", l)
	}
	if dropped > 0 {
		m.activity(ctx, id, version, "log", "ok", fmt.Sprintf("… %d more lines not kept", dropped))
	}
}

func (m *Manager) activity(ctx context.Context, id, version, call, outcome, detail string) {
	if err := m.opts.Store.AddActivity(context.WithoutCancel(ctx), id, version, packages.Activity{At: m.opts.Now(), Call: call, Outcome: outcome, Detail: detail}); err != nil {
		m.log.Error("plugin activity not recorded", "plugin", id, "err", err)
	}
}

// host does what a plugin asks, within its approved permissions, and
// records it.
func (m *Manager) host(id, version string, r *running) lua.HostFunc {
	return func(ctx context.Context, c lua.HostCall) (bool, string) {
		switch c.Op {
		case "emit":
			ok, why := m.emit(ctx, id, r, c)
			outcome := "ok"
			switch {
			case !ok && strings.HasPrefix(why, "refused: "):
				outcome, why = "refused", strings.TrimPrefix(why, "refused: ")
			case !ok:
				outcome = "failed"
			}
			b, _ := json.Marshal(c.Payload)
			detail := c.Name + " " + string(b)
			if why != "" {
				detail += " (" + why + ")"
			}
			m.activity(ctx, id, version, packages.PermEvents, outcome, detail)
			if outcome != "ok" {
				m.log.Warn("plugin event not sent", "plugin", id, "event", c.Name, "why", why)
			} else {
				m.log.Info("plugin sent an event", "plugin", id, "event", c.Name)
			}
			return ok, why
		case "chat":
			message := c.Message
			ok, why := m.chat(ctx, id, r, message)
			outcome := "ok"
			switch {
			case !ok && strings.HasPrefix(why, "refused: "):
				outcome, why = "refused", strings.TrimPrefix(why, "refused: ")
			case !ok:
				outcome = "failed"
			}
			detail := message
			if why != "" {
				detail = message + " (" + why + ")"
			}
			m.activity(ctx, id, version, packages.PermChatSend, outcome, detail)
			if outcome != "ok" {
				m.log.Warn("plugin chat not sent", "plugin", id, "why", why)
			} else {
				m.log.Info("plugin sent chat", "plugin", id)
			}
			return ok, why
		}
		m.activity(ctx, id, version, c.Op, "refused", "StreamGremlin has no such call")
		return false, "StreamGremlin has no call " + c.Op
	}
}

// emit records a plugin's event (D-073): "refused: …" when it may not.
func (m *Manager) emit(ctx context.Context, id string, r *running, c lua.HostCall) (bool, string) {
	if !slices.Contains(r.spec.Permissions, packages.PermEvents) {
		return false, "refused: it doesn't have permission to send events"
	}
	if !r.events[c.Name] {
		return false, fmt.Sprintf("refused: %q isn't one of the events in its streamgremlin.json", c.Name)
	}
	if src, _ := c.Event["source"].(string); src == "plugin:"+id {
		return false, "refused: its own event started this, and an event it sends can't start another: that could go round forever"
	}
	now := m.opts.Now()
	r.mu.Lock()
	keep := r.emits[:0]
	for _, t := range r.emits {
		if now.Sub(t) < time.Second {
			keep = append(keep, t)
		}
	}
	r.emits = keep
	if len(r.emits) >= EventsPerSecond {
		r.mu.Unlock()
		return false, fmt.Sprintf("refused: at most %d events a second", EventsPerSecond)
	}
	r.emits = append(r.emits, now)
	r.mu.Unlock()
	if m.opts.Inbox == nil {
		return false, "events aren't available"
	}
	payload := c.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, "refused: the payload can't be sent"
	}
	env := events.Envelope{Type: packages.EventType(id, c.Name), Source: "plugin:" + id, Payload: raw, DeliveryQuality: events.Confirmed}
	if c.Key != "" {
		env.SourceEventID = c.Name + ":" + c.Key
	}
	if cause, _ := c.Event["id"].(string); cause != "" {
		env.CausationID = cause
	}
	res, err := m.opts.Inbox.Accept(ctx, env)
	switch {
	case errors.Is(err, events.ErrConflict):
		return false, "refused: an event was sent with that id already"
	case errors.Is(err, events.ErrInvalid):
		return false, "refused: " + strings.TrimPrefix(err.Error(), events.ErrInvalid.Error()+": ")
	case err != nil:
		return false, err.Error()
	case res.Duplicate:
		return false, "refused: an event was sent with that id already"
	}
	return true, ""
}

// chat sends a plugin's message: "refused: …" when it may not.
func (m *Manager) chat(ctx context.Context, id string, r *running, message string) (bool, string) {
	allowed := false
	for _, p := range r.spec.Permissions {
		allowed = allowed || p == packages.PermChatSend
	}
	if !allowed {
		return false, "refused: it doesn't have permission to send chat"
	}
	msg := strings.TrimSpace(message)
	if msg == "" || utf8.RuneCountInString(msg) > MaxChatRunes {
		return false, fmt.Sprintf("refused: a message is 1 to %d characters", MaxChatRunes)
	}
	now := m.opts.Now()
	r.mu.Lock()
	keep := r.chat[:0]
	for _, t := range r.chat {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	r.chat = keep
	if len(r.chat) >= ChatPerMinute {
		r.mu.Unlock()
		return false, fmt.Sprintf("refused: at most %d messages a minute", ChatPerMinute)
	}
	r.chat = append(r.chat, now)
	r.mu.Unlock()
	if m.opts.Chat == nil {
		return false, "chat isn't available"
	}
	_, err := m.opts.Chat.SendChat(ctx, twitch.SendRequest{Text: msg, As: twitch.SendAuto, Workflow: "plugin:" + id})
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

// using are the workflows using a plugin's steps or started by its events.
func using(ctx context.Context, wf Workflows, id string) ([]workflow.Summary, error) {
	steps, err := wf.UsingActions(ctx, id+".")
	if err != nil {
		return nil, err
	}
	triggered, err := wf.UsingTriggers(ctx, packages.EventType(id, ""))
	if err != nil {
		return nil, err
	}
	for _, w := range triggered {
		if !slices.ContainsFunc(steps, func(x workflow.Summary) bool { return x.ID == w.ID }) {
			steps = append(steps, w)
		}
	}
	return steps, nil
}

// Removing stops a plugin, turns off the workflows that use its steps or
// its events (naming them) and takes its steps out. The caller then
// removes it from the store.
func (m *Manager) Removing(ctx context.Context, id string, wf Workflows) ([]string, error) {
	m.stop(id)
	names := []string{}
	if wf != nil {
		using, err := using(ctx, wf, id)
		if err != nil {
			return nil, err
		}
		for _, w := range using {
			if w.Enabled {
				if err := wf.SetEnabled(ctx, w.ID, false); err != nil {
					return nil, err
				}
			}
			names = append(names, w.Name)
		}
	}
	m.unregister(id)
	return names, nil
}

// Switched moves a plugin to the version now installed, after an update
// or roll back (D-072): workflows using steps it no longer has are turned
// off (never deleted) and named, its steps are registered again, and if
// it's on it's loaded again, fresh. A version that won't load is left off,
// saying why (ErrLoad).
func (m *Manager) Switched(ctx context.Context, id string, wf Workflows) ([]string, error) {
	p, man, err := m.opts.Store.Plugin(ctx, id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	old := m.actions[id]
	m.mu.Unlock()
	var gone []string
	for _, name := range old {
		if !slices.ContainsFunc(man.Lua.Actions, func(a packages.Action) bool { return id+"."+a.Name == name }) {
			gone = append(gone, name)
		}
	}
	off := []string{}
	if wf != nil && len(gone) > 0 {
		using, err := UsingSteps(ctx, wf, gone)
		if err != nil {
			return nil, err
		}
		for _, w := range using {
			if w.Enabled {
				if err := wf.SetEnabled(ctx, w.ID, false); err != nil {
					return nil, err
				}
			}
			off = append(off, w.Name)
		}
	}
	m.register(p, man)
	m.log.Info("plugin switched version", "plugin", id, "version", p.Version, "steps_removed", gone, "workflows_off", off)
	if m.isRunning(id) {
		if err := m.start(ctx, id); err != nil {
			return off, err
		}
	}
	return off, nil
}

// UsingSteps are the workflows using any of these steps, each once.
func UsingSteps(ctx context.Context, wf Workflows, steps []string) ([]workflow.Summary, error) {
	out := []workflow.Summary{}
	seen := map[string]bool{}
	for _, s := range steps {
		using, err := wf.UsingActions(ctx, s)
		if err != nil {
			return nil, err
		}
		for _, w := range using {
			if !seen[w.ID] {
				seen[w.ID] = true
				out = append(out, w)
			}
		}
	}
	return out, nil
}

// Using names the workflows that use a plugin's steps or its events.
func Using(ctx context.Context, wf Workflows, id string) ([]string, error) {
	using, err := using(ctx, wf, id)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, w := range using {
		names = append(names, w.Name)
	}
	return names, nil
}

func numberRange(p packages.Param) string {
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	switch {
	case p.Min != nil && p.Max != nil:
		return " from " + f(*p.Min) + " to " + f(*p.Max)
	case p.Min != nil:
		return " of at least " + f(*p.Min)
	case p.Max != nil:
		return " of at most " + f(*p.Max)
	}
	return ""
}

// CheckTemplates checks a plugin's templates as the workflow editor would
// save them (D-071): each may use StreamGremlin's own steps and this
// plugin's, not another add-on's. Problems in a blank (left empty for the
// streamer) are skipped. It's the store's extra check (packages.SetCheck).
func (m *Manager) CheckTemplates(r *packages.Read) error {
	man := r.Manifest
	if man.Lua == nil {
		return nil
	}
	ts := r.Templates()
	if len(ts) == 0 {
		return nil
	}
	reg := workflow.NewRegistry()
	for _, a := range m.opts.Actions.List() {
		if a.AddOn == "" {
			reg.Add(*a)
		}
	}
	for _, a := range man.Lua.Actions {
		reg.Add(workflow.Action{Name: man.ID + "." + a.Name, Label: a.Label, Group: man.Name, External: true, AddOn: man.ID, Params: params(a.Params)})
	}
	for _, t := range ts {
		_, err := workflow.Parse(t.Workflow, reg)
		var inv *workflow.InvalidError
		if err == nil {
			continue
		}
		if !errors.As(err, &inv) {
			return fmt.Errorf("%w: template %q: %s", packages.ErrInvalid, t.ID, err.Error())
		}
		for _, pr := range inv.Problems {
			blank := false
			for _, b := range t.Blanks {
				blank = blank || pr.Path == b.Path || strings.HasPrefix(pr.Path, b.Path+".")
			}
			if !blank {
				return fmt.Errorf("%w: template %q: %s: %s", packages.ErrInvalid, t.ID, pr.Path, pr.Message)
			}
		}
	}
	return nil
}
