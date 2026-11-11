package lua

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
)

// LoadTimeLimit is how long a plugin's script has to load.
const LoadTimeLimit = 5 * time.Second

// PluginSpec is a plugin to run (docs/adr/0038-lua-plugins.md).
type PluginSpec struct {
	ID, Name, Version string
	Script            string
	Settings          map[string]any
	// Permissions are the approved ones; Actions those the manifest
	// declares.
	Permissions []string
	Actions     []string
	// Timeout is how long a call may take.
	Timeout time.Duration
}

// HostFunc does what a plugin asks (op "chat": send message), saying
// whether it did and, if not, why. It's called during the plugin's call,
// with that call's context.
type HostFunc func(ctx context.Context, op, message string) (ok bool, reason string)

// Plugin is a running plugin's worker: one Lua state, kept between calls,
// one call at a time.
type Plugin struct {
	spec   PluginSpec
	host   HostFunc
	log    *slog.Logger
	slot   chan struct{}
	proc   *proc
	mu     sync.Mutex
	nextID uint64
}

// StartPlugin starts a worker and loads the plugin's script. A script that
// fails to load is an *Error, with what it logged.
func StartPlugin(opts Options, spec PluginSpec, host HostFunc) (*Plugin, Result, error) {
	if opts.Memory == 0 {
		opts.Memory = DefaultMemory
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	log = log.With("plugin", spec.ID, "version", spec.Version)
	p := &Plugin{spec: spec, host: host, log: log, slot: make(chan struct{}, 1)}
	pr, err := startProc(opts, "-plugin")
	if err != nil {
		return nil, Result{}, &Error{Kind: KindCrashed, Message: "the Lua worker didn't start: " + err.Error()}
	}
	p.proc = pr
	sum := sha256.Sum256([]byte(spec.Script))
	plugin := map[string]any{"id": spec.ID, "name": spec.Name, "version": spec.Version}
	start := time.Now()
	res, err := pr.exchange(context.Background(), frame{Type: "load", ID: p.id(), Script: spec.Script, Digest: hex.EncodeToString(sum[:]),
		Plugin: plugin, Settings: spec.Settings, Permissions: spec.Permissions, Actions: spec.Actions,
		TimeoutMS: LoadTimeLimit.Milliseconds()}, LoadTimeLimit, log, nil)
	res.Took = time.Since(start)
	if err != nil {
		pr.kill()
		var le *Error
		if errors.As(err, &le) && le.Kind == KindTimeout {
			le.Message = "loading the script took longer than 5 seconds"
		}
		log.Warn("lua plugin didn't load", "err", err)
		return nil, res, err
	}
	log.Info("lua plugin loaded", "took", res.Took.Round(time.Millisecond))
	return p, res, nil
}

func (p *Plugin) id() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	return p.nextID
}

// Dead reports whether the worker has stopped: a call killed it, or it
// crashed. A dead plugin is started again by whoever runs it.
func (p *Plugin) Dead() bool { return p.proc.dead() }

// Close stops the worker. Its state is gone.
func (p *Plugin) Close() { p.proc.kill() }

// Call runs one of the plugin's actions, after any call in progress. A
// script error, time limit or worker that stopped is an *Error.
func (p *Plugin) Call(ctx context.Context, action string, args, event map[string]any, key string) (Result, error) {
	select {
	case p.slot <- struct{}{}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	defer func() { <-p.slot }()
	if p.proc.dead() {
		return Result{}, &Error{Kind: KindCrashed, Message: "the plugin's worker had stopped"}
	}
	limit := p.spec.Timeout
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < limit {
		limit = time.Until(dl)
		if limit <= 0 {
			return Result{}, ctx.Err()
		}
	}
	cctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	host := func(g frame) frame {
		ok, reason := p.host(cctx, g.Op, g.Message)
		return frame{Type: "reply", ID: g.ID, OK: ok, Reason: reason}
	}
	log := p.log.With("action", action)
	start := time.Now()
	res, err := p.proc.exchange(ctx, frame{Type: "call", ID: p.id(), Action: action, Args: args, Event: event, Key: key,
		TimeoutMS: max(limit.Milliseconds(), 1)}, limit, log, host)
	res.Took = time.Since(start)
	if err == nil {
		res.Value = wholeNumbers(res.Value)
		log.Info("lua plugin call", "took", res.Took.Round(time.Microsecond))
		return res, nil
	}
	var le *Error
	if errors.As(err, &le) {
		if le.Kind == KindTimeout {
			le.Message = "the plugin ran longer than its " + limit.Round(10*time.Millisecond).String() + " time limit"
		}
		log.Warn("lua plugin call failed", "kind", le.Kind, "err", le.Message, "worker_stopped", p.proc.dead())
	}
	return res, err
}
