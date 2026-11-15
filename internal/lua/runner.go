package lua

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Time limits.
const (
	DefaultTimeLimit = time.Second
	MinTimeLimit     = 100 * time.Millisecond
	MaxTimeLimit     = 30 * time.Second
	// grace is how long past its time limit a worker has to answer before
	// it's killed.
	grace = 250 * time.Millisecond
)

// Pool sizes.
const (
	maxBusy = 4
	maxIdle = 2
)

// Job is one script to run.
type Job struct {
	Script string
	Args   map[string]any
	Event  map[string]any
	// TimeLimit is how long it may run; 0 means DefaultTimeLimit. The
	// context's deadline, when sooner, wins.
	TimeLimit time.Duration
	// Who is for the log: the workflow, run and step, or "try".
	Who []any
}

// Result is how a script ended well.
type Result struct {
	Value      any
	Log        []string
	LogDropped int
	Took       time.Duration

	// done: the worker answered (a plugin's timers are then current).
	done   bool
	timers []timerDue
}

// Error is how a script ended badly. Log has what it wrote first.
type Error struct {
	Kind       string
	Message    string
	Log        []string
	LogDropped int
}

func (e *Error) Error() string { return e.Message }

// Options configure a Runner.
type Options struct {
	// Command is the worker's program and arguments before WorkerArg;
	// empty means this executable.
	Command []string
	// Env is added to the worker's otherwise empty environment.
	Env []string
	// Memory is the worker's heap cap; 0 means DefaultMemory.
	Memory int64
	Logger *slog.Logger
}

// Runner runs scripts in worker processes, killing any that overstays.
type Runner struct {
	opts  Options
	log   *slog.Logger
	slots chan struct{}

	mu     sync.Mutex
	idle   []*proc
	closed bool
	nextID uint64
}

// NewRunner returns a Runner. Workers start when first needed.
func NewRunner(opts Options) *Runner {
	if opts.Memory == 0 {
		opts.Memory = DefaultMemory
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runner{opts: opts, log: log, slots: make(chan struct{}, maxBusy)}
}

var errClosed = errors.New("lua: StreamGremlin is stopping")

// Close kills idle workers and refuses new runs. Runs in progress finish or
// are cancelled by their callers.
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	idle := r.idle
	r.idle = nil
	r.mu.Unlock()
	for _, p := range idle {
		p.kill()
	}
}

// CheckTimeLimit says whether d is a time limit a step may have.
func CheckTimeLimit(d time.Duration) error {
	if d < MinTimeLimit || d > MaxTimeLimit {
		return fmt.Errorf("the time limit is 0.1 to 30 seconds")
	}
	return nil
}

// Run runs job in a worker. A script error, a time limit or memory cap, or a
// worker that stopped is an *Error; a cancelled ctx is ctx's error.
func (r *Runner) Run(ctx context.Context, job Job) (Result, error) {
	if len(job.Script) > MaxScriptBytes {
		return Result{}, &Error{Kind: KindScript, Message: "the script is over 64 KiB"}
	}
	limit := job.TimeLimit
	if limit == 0 {
		limit = DefaultTimeLimit
	}
	if err := CheckTimeLimit(limit); err != nil {
		return Result{}, &Error{Kind: KindScript, Message: err.Error()}
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < limit {
		limit = time.Until(dl)
		if limit <= 0 {
			return Result{}, ctx.Err()
		}
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	defer func() { <-r.slots }()

	sum := sha256.Sum256([]byte(job.Script))
	digest := hex.EncodeToString(sum[:])
	log := r.log.With(append([]any{"script", digest[:12]}, job.Who...)...)
	p, err := r.take()
	if err != nil {
		log.Error("lua worker didn't start", "err", err)
		return Result{}, &Error{Kind: KindCrashed, Message: "the Lua worker didn't start: " + err.Error()}
	}
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.mu.Unlock()

	start := time.Now()
	res, err := p.run(ctx, frame{Type: "run", ID: id, Script: job.Script, Digest: digest, Args: job.Args, Event: job.Event,
		TimeoutMS: max(limit.Milliseconds(), 1)}, limit, log)
	res.Took = time.Since(start)
	var le *Error
	switch {
	case err == nil:
		r.give(p)
		log.Info("lua script ran", "took", res.Took.Round(time.Microsecond))
		return res, nil
	case errors.As(err, &le) && le.Kind == KindScript, errors.As(err, &le) && le.Kind == KindOutput:
		r.give(p) // the worker is fine; the next run gets a fresh state
		log.Info("lua script failed", "kind", le.Kind, "err", le.Message, "took", res.Took.Round(time.Microsecond))
		return res, err
	case errors.As(err, &le) && le.Kind == KindTimeout && !p.dead():
		// It stopped itself in time; it can run again.
		r.give(p)
		le.Message = fmt.Sprintf("the script ran longer than its %s time limit", limitText(job.TimeLimit, limit))
		log.Warn("lua script timed out", "limit", limit, "took", res.Took.Round(time.Millisecond))
		return res, err
	default:
		p.kill()
		if errors.Is(err, context.DeadlineExceeded) {
			// The step's deadline beat the worker stopping itself: the
			// same time limit, said the same way.
			err = &Error{Kind: KindTimeout, Log: res.Log, LogDropped: res.LogDropped}
		}
		if errors.As(err, &le) {
			if le.Kind == KindTimeout {
				le.Message = fmt.Sprintf("the script ran longer than its %s time limit", limitText(job.TimeLimit, limit))
			}
			log.Warn("lua worker killed", "kind", le.Kind, "err", le.Message, "took", res.Took.Round(time.Millisecond))
		} else {
			log.Info("lua script cancelled", "err", err)
		}
		return res, err
	}
}

// limitText says the limit the script had: its own, or what was left of the
// step's or workflow's time when that was sooner.
func limitText(asked, had time.Duration) string {
	if asked == 0 {
		asked = DefaultTimeLimit
	}
	if had < asked {
		return had.Round(10*time.Millisecond).String() + " (what was left of the step's time)"
	}
	return asked.String()
}

// take returns an idle worker or starts one.
func (r *Runner) take() (*proc, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errClosed
	}
	for len(r.idle) > 0 {
		p := r.idle[len(r.idle)-1]
		r.idle = r.idle[:len(r.idle)-1]
		if !p.dead() {
			r.mu.Unlock()
			return p, nil
		}
	}
	r.mu.Unlock()
	return r.start()
}

// give puts a healthy worker back, or ends it when enough are idle.
func (r *Runner) give(p *proc) {
	r.mu.Lock()
	if !r.closed && len(r.idle) < maxIdle && !p.dead() {
		r.idle = append(r.idle, p)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	p.kill()
}

func (r *Runner) start() (*proc, error) { return startProc(r.opts) }

// startProc starts a worker; extra arguments go after its memory cap.
func startProc(opts Options, extra ...string) (*proc, error) {
	argv := opts.Command
	if len(argv) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		argv = []string{exe}
	}
	args := append(append(append([]string{}, argv[1:]...), WorkerArg, "-memory", strconv.FormatInt(opts.Memory, 10)), extra...)
	cmd := exec.Command(argv[0], args...) //nolint:gosec // our own executable
	// Never the engine's environment: it may hold credentials.
	cmd.Env = append(baseEnv(), opts.Env...)
	cmd.Dir = os.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &proc{cmd: cmd, in: stdin, memoryCap: opts.Memory, frames: make(chan frame, 16), exited: make(chan struct{})}
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go p.read(stdout)
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

func baseEnv() []string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SYSTEMROOT"); v != "" {
			return []string{"SYSTEMROOT=" + v}
		}
	}
	return []string{}
}

// proc is one worker process.
type proc struct {
	cmd       *exec.Cmd
	in        io.WriteCloser
	frames    chan frame
	exited    chan struct{}
	stderr    capped
	memory    bool // it said it passed its heap cap
	memoryCap int64
	killed    atomic.Bool
}

func (p *proc) read(r io.Reader) {
	defer close(p.frames)
	for {
		f, err := readFrame(r)
		if err != nil {
			return
		}
		p.frames <- f
	}
}

func (p *proc) dead() bool {
	if p.killed.Load() {
		return true
	}
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

func (p *proc) kill() {
	p.killed.Store(true)
	_ = p.in.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// run sends f and waits for its done, the time limit and grace, or ctx.
func (p *proc) run(ctx context.Context, f frame, limit time.Duration, log *slog.Logger) (Result, error) {
	return p.exchange(ctx, f, limit, log, nil)
}

// exchange sends f and waits for its done, answering the worker's host
// calls with host (a plugin's; nil refuses them as a protocol error).
func (p *proc) exchange(ctx context.Context, f frame, limit time.Duration, log *slog.Logger, host func(frame) frame) (Result, error) {
	var res Result
	fail := func(kind, msg string) (Result, error) {
		return res, &Error{Kind: kind, Message: msg, Log: res.Log, LogDropped: res.LogDropped}
	}
	if err := writeFrame(p.in, f); err != nil {
		return fail(KindCrashed, "the Lua worker stopped unexpectedly")
	}
	watchdog := time.NewTimer(limit + grace)
	defer watchdog.Stop()
	done := ctx.Done()
	var late <-chan time.Time
	for {
		select {
		case <-done:
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				// The callers shorten limit to their deadline, so the worker
				// is stopping the script about now. Give it grace to answer
				// rather than kill a worker that may keep a plugin's state.
				done, late = nil, time.After(grace)
				continue
			}
			p.kill()
			return res, ctx.Err()
		case <-late:
			p.kill()
			return res, context.DeadlineExceeded
		case <-watchdog.C:
			p.kill()
			return fail(KindTimeout, "the script ran longer than its time limit")
		case g, ok := <-p.frames:
			if !ok {
				// The worker's output closed: wait for its exit, to learn why.
				select {
				case <-p.exited:
				case <-time.After(grace):
					p.kill()
					<-p.exited
				}
				if p.memory || p.cmd.ProcessState != nil && p.cmd.ProcessState.ExitCode() == memoryExit {
					return fail(KindMemory, fmt.Sprintf("the script used more than its %d MiB of memory", p.memoryCap>>20))
				}
				if tail := p.stderr.String(); tail != "" {
					log.Warn("lua worker said", "stderr", tail)
				}
				return fail(KindCrashed, "the Lua worker stopped unexpectedly")
			}
			switch {
			case g.Type == "memory":
				p.memory = true
			case g.ID != f.ID:
				p.kill()
				return fail(KindCrashed, "the Lua worker broke the protocol")
			case g.Type == "host" && host != nil:
				if err := writeFrame(p.in, host(g)); err != nil {
					p.kill()
					return fail(KindCrashed, "the Lua worker stopped unexpectedly")
				}
			case g.Type == "log":
				res.Log = append(res.Log, cut(g.Message, MaxLogLine))
				if len(res.Log) > MaxLogLines {
					p.kill()
					return fail(KindCrashed, "the Lua worker broke the protocol")
				}
				log.Info("lua log", "line", g.Message)
			case g.Type == "done":
				res.LogDropped = g.LogDropped
				res.done, res.timers = true, g.Timers
				if g.Kind != "" {
					return fail(g.Kind, g.Error)
				}
				res.Value = wholeNumbers(g.Result)
				return res, nil
			default:
				p.kill()
				return fail(KindCrashed, "the Lua worker broke the protocol")
			}
		}
	}
}

// capped keeps the last 4 KiB written: a worker's panic, say.
type capped struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capped) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Write(b)
	if c.buf.Len() > 4<<10 {
		rest := c.buf.Bytes()[c.buf.Len()-4<<10:]
		c.buf.Reset()
		c.buf.Write(rest)
	}
	return len(b), nil
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// wholeNumbers makes whole numbers integers again after JSON, as the worker
// gave them.
func wholeNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
	case []any:
		for i := range x {
			x[i] = wholeNumbers(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = wholeNumbers(x[k])
		}
	}
	return v
}
