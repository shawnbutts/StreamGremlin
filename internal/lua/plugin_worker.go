package lua

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	glua "github.com/yuin/gopher-lua"
)

// pluginWorker is a worker running one plugin (docs/adr/0038-lua-plugins.md):
// one Lua state for as long as it lives, one call at a time.
type pluginWorker struct {
	*worker
	in       io.Reader
	L        *glua.LState
	handlers map[string]*glua.LFunction
	declared []string
	loading  bool
	callID   uint64 // the call in progress, for its log and host frames
	inCall   bool
	lines    int
	dropped  int
	// timers waiting (D-073), by id.
	timers    map[int]*waiting
	lastTimer int
}

// waiting is a timer the plugin set.
type waiting struct {
	fn  *glua.LFunction
	due time.Time
}

// Timers' limits.
const (
	MaxTimers       = 100
	MinTimerSeconds = 1
	MaxTimerSeconds = 24 * 60 * 60
)

// servePlugin loads the plugin from the first frame, then answers calls
// until in closes.
func (w *worker) servePlugin(in io.Reader) int {
	f, err := readFrame(in)
	if err != nil || f.Type != "load" {
		fmt.Fprintln(stderr(), "lua worker: protocol error: the first frame isn't load")
		return 1
	}
	p := &pluginWorker{worker: w, in: in, handlers: map[string]*glua.LFunction{}, timers: map[int]*waiting{}}
	done := p.load(f)
	done.Timers = p.due()
	if err := w.send(done); err != nil || done.Kind != "" {
		return 0
	}
	for {
		f, err := readFrame(in)
		if errors.Is(err, io.EOF) {
			return 0
		}
		var done frame
		switch {
		case err != nil:
			fmt.Fprintln(stderr(), "lua worker: protocol error:", err)
			return 1
		case f.Type == "call":
			done = p.call(f)
		case f.Type == "fire":
			done = p.fire(f)
		default:
			fmt.Fprintln(stderr(), "lua worker: protocol error:", f.Type)
			return 1
		}
		done.Timers = p.due()
		if err := w.send(done); err != nil {
			return 1
		}
	}
}

func (p *pluginWorker) logf(s string) {
	p.lines++
	if p.lines > MaxLogLines {
		p.dropped++
		return
	}
	_ = p.send(frame{Type: "log", ID: p.callID, Message: cut(s, MaxLogLine)})
}

func (p *pluginWorker) load(f frame) (done frame) {
	done = frame{Type: "done", ID: f.ID}
	defer func() {
		if r := recover(); r != nil {
			done.Kind, done.Error = KindScript, fmt.Sprintf("the interpreter failed: %v", r)
		}
	}()
	sum := sha256.Sum256([]byte(f.Script))
	if hex.EncodeToString(sum[:]) != f.Digest {
		done.Kind, done.Error = KindCrashed, "the script didn't arrive whole"
		return done
	}
	p.callID = f.ID
	p.declared = f.Actions
	p.L = newState(p.logf)
	L := p.L
	sg := L.GetGlobal("sg").(*glua.LTable)
	sg.RawSetString("plugin", toLua(L, f.Plugin))
	sg.RawSetString("settings", toLua(L, orEmpty(f.Settings)))
	perms := L.NewTable()
	for _, x := range f.Permissions {
		perms.Append(glua.LString(x))
	}
	sg.RawSetString("permissions", perms)
	sg.RawSetString("action", L.NewFunction(p.luaAction))
	sg.RawSetString("chat", L.NewFunction(p.luaChat))
	sg.RawSetString("emit", L.NewFunction(p.luaEmit))
	sg.RawSetString("after", L.NewFunction(p.luaAfter))
	sg.RawSetString("cancel", L.NewFunction(p.luaCancel))
	pts := L.NewTable()
	pts.RawSetString("balance", L.NewFunction(func(L *glua.LState) int { return p.ask(L, "points.balance", map[string]any{}) }))
	sg.RawSetString("points", pts)
	round := L.NewTable()
	round.RawSetString("open", L.NewFunction(func(L *glua.LState) int {
		return p.ask(L, "round.open", map[string]any{"title": L.CheckString(1), "rules": L.CheckString(2), "id": L.OptString(3, "")})
	}))
	round.RawSetString("stake", L.NewFunction(func(L *glua.LState) int {
		return p.ask(L, "round.stake", map[string]any{"round": L.CheckString(1), "amount": float64(L.CheckNumber(2))})
	}))
	round.RawSetString("roll", L.NewFunction(func(L *glua.LState) int {
		return p.ask(L, "round.roll", map[string]any{"round": L.CheckString(1), "label": L.OptString(2, ""), "sides": float64(L.CheckNumber(3))})
	}))
	round.RawSetString("settle", L.NewFunction(func(L *glua.LState) int {
		c := &fromLua{}
		v, err := c.table(L.OptTable(2, L.NewTable()), 0)
		if err != nil {
			L.Push(glua.LNil)
			L.Push(glua.LString("the payouts can't be sent: " + strings.TrimPrefix(err.Error(), "output: ")))
			return 2
		}
		if list, isList := v.([]any); isList && len(list) == 0 {
			v = map[string]any{}
		}
		return p.ask(L, "round.settle", map[string]any{"round": L.CheckString(1), "payouts": v})
	}))
	round.RawSetString("cancel", L.NewFunction(func(L *glua.LState) int {
		return p.ask(L, "round.cancel", map[string]any{"round": L.CheckString(1), "reason": L.OptString(2, "")})
	}))
	sg.RawSetString("round", round)
	fn, err := L.Load(strings.NewReader(f.Script), "script")
	if err != nil {
		done.Kind, done.Error = KindScript, friendly(err)
		return done
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.TimeoutMS)*time.Millisecond)
	defer cancel()
	p.loading = true
	L.SetContext(ctx)
	L.Push(fn)
	err = L.PCall(0, 0, nil)
	L.RemoveContext()
	p.loading = false
	switch {
	case err != nil && ctx.Err() != nil:
		done.Kind, done.Error = KindTimeout, "loading the script took longer than its time limit"
	case err != nil:
		done.Kind, done.Error = KindScript, friendly(err)
	}
	done.LogDropped = p.dropped
	return done
}

// luaAction is sg.action(name, fn): only while loading, only declared names.
func (p *pluginWorker) luaAction(L *glua.LState) int {
	name := L.CheckString(1)
	fn := L.CheckFunction(2)
	if !p.loading {
		L.RaiseError("sg.action works only while the script loads")
	}
	if !slices.Contains(p.declared, name) {
		L.RaiseError("sg.action: %q isn't one of the actions in streamgremlin.json", name)
	}
	if _, dup := p.handlers[name]; dup {
		L.RaiseError("sg.action: %q is handled twice", name)
	}
	p.handlers[name] = fn
	return 0
}

// luaChat is sg.chat(message) → ok, reason. It never raises: the engine
// decides, and says why not.
func (p *pluginWorker) luaChat(L *glua.LState) int {
	msg := L.CheckString(1)
	ok, reason := p.host(frame{Op: "chat", Message: msg})
	L.Push(glua.LBool(ok))
	if ok {
		L.Push(glua.LNil)
	} else {
		L.Push(glua.LString(reason))
	}
	return 2
}

// luaEmit is sg.emit(name, payload [, id]) → ok, reason: one of the
// plugin's events, to start workflows. Like sg.chat, it never raises.
func (p *pluginWorker) luaEmit(L *glua.LState) int {
	name := L.CheckString(1)
	t := L.OptTable(2, L.NewTable())
	id := L.OptString(3, "")
	fail := func(why string) int {
		L.Push(glua.LFalse)
		L.Push(glua.LString(why))
		return 2
	}
	c := &fromLua{}
	v, err := c.table(t, 0)
	if err != nil {
		return fail("the payload can't be sent: " + strings.TrimPrefix(err.Error(), "output: "))
	}
	payload, isMap := v.(map[string]any)
	if !isMap {
		return fail("the payload is a table of names and values, like {winner = \"Fan\"}")
	}
	ok, reason := p.host(frame{Op: "emit", Name: name, Payload: payload, Key: id})
	L.Push(glua.LBool(ok))
	if ok {
		L.Push(glua.LNil)
	} else {
		L.Push(glua.LString(reason))
	}
	return 2
}

// ask is a host call that gives back a value: the value (true when there's
// none), or nil and why not. Like sg.chat, it never raises.
func (p *pluginWorker) ask(L *glua.LState, op string, args map[string]any) int {
	reply := p.hostReply(frame{Op: op, Payload: args})
	if !reply.OK {
		L.Push(glua.LNil)
		L.Push(glua.LString(reply.Reason))
		return 2
	}
	if reply.Result == nil {
		L.Push(glua.LTrue)
	} else {
		L.Push(toLua(L, reply.Result))
	}
	return 1
}

// luaAfter is sg.after(seconds, fn) → id: fn runs once, that many seconds
// later, while the plugin stays on.
func (p *pluginWorker) luaAfter(L *glua.LState) int {
	secs := float64(L.CheckNumber(1))
	fn := L.CheckFunction(2)
	if secs < MinTimerSeconds || secs > MaxTimerSeconds {
		L.RaiseError("sg.after: seconds is %d to %d", MinTimerSeconds, MaxTimerSeconds)
	}
	if len(p.timers) >= MaxTimers {
		L.RaiseError("sg.after: a plugin has up to %d timers waiting", MaxTimers)
	}
	p.lastTimer++
	p.timers[p.lastTimer] = &waiting{fn: fn, due: time.Now().Add(time.Duration(secs * float64(time.Second)))}
	L.Push(glua.LNumber(p.lastTimer))
	return 1
}

// luaCancel is sg.cancel(id) → whether that timer was waiting.
func (p *pluginWorker) luaCancel(L *glua.LState) int {
	n, ok := L.Get(1).(glua.LNumber)
	_, waiting := p.timers[int(n)]
	if ok && waiting {
		delete(p.timers, int(n))
	}
	L.Push(glua.LBool(ok && waiting))
	return 1
}

// due is every timer waiting, for the engine.
func (p *pluginWorker) due() []timerDue {
	now := time.Now()
	out := make([]timerDue, 0, len(p.timers))
	for id, w := range p.timers {
		out = append(out, timerDue{ID: id, InMS: max(w.due.Sub(now).Milliseconds(), 0)})
	}
	slices.SortFunc(out, func(a, b timerDue) int { return a.ID - b.ID })
	return out
}

// fire runs a timer the engine says is due. One cancelled meanwhile does
// nothing.
func (p *pluginWorker) fire(f frame) (done frame) {
	done = frame{Type: "done", ID: f.ID}
	p.callID, p.lines, p.dropped = f.ID, 0, 0
	defer func() {
		if r := recover(); r != nil {
			done.Kind, done.Error = KindScript, fmt.Sprintf("the interpreter failed: %v", r)
		}
		done.LogDropped = p.dropped
	}()
	w, ok := p.timers[f.Timer]
	if !ok {
		return done
	}
	delete(p.timers, f.Timer)
	L := p.L
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.TimeoutMS)*time.Millisecond)
	defer cancel()
	p.inCall = true
	L.SetContext(ctx)
	L.Push(w.fn)
	err := L.PCall(0, 0, nil)
	L.RemoveContext()
	p.inCall = false
	L.SetTop(0)
	switch {
	case err != nil && ctx.Err() != nil:
		done.Kind, done.Error = KindTimeout, "the timer ran longer than its time limit"
	case err != nil:
		done.Kind, done.Error = KindScript, friendly(err)
	}
	return done
}

// host asks the engine to act and waits for its answer.
func (p *pluginWorker) host(g frame) (bool, string) {
	r := p.hostReply(g)
	return r.OK, r.Reason
}

func (p *pluginWorker) hostReply(g frame) frame {
	if !p.inCall {
		return frame{Reason: "StreamGremlin can't be asked to do anything while the script loads"}
	}
	g.Type, g.ID = "host", p.callID
	if err := p.send(g); err != nil {
		return frame{Reason: "StreamGremlin didn't answer"}
	}
	reply, err := readFrame(p.in)
	if err != nil || reply.Type != "reply" || reply.ID != p.callID {
		// The engine is gone, or broke the protocol: nothing more to do.
		fmt.Fprintln(stderr(), "lua worker: protocol error waiting for a reply")
		exit(1)
	}
	return reply
}

func (p *pluginWorker) call(f frame) (done frame) {
	done = frame{Type: "done", ID: f.ID}
	p.callID, p.lines, p.dropped = f.ID, 0, 0
	defer func() {
		if r := recover(); r != nil {
			done.Result, done.Kind, done.Error = nil, KindScript, fmt.Sprintf("the interpreter failed: %v", r)
		}
		done.LogDropped = p.dropped
	}()
	fn, ok := p.handlers[f.Action]
	if !ok {
		done.Kind, done.Error = KindScript, fmt.Sprintf("the plugin doesn't handle %q", f.Action)
		return done
	}
	L := p.L
	call := L.NewTable()
	call.RawSetString("args", toLua(L, orEmpty(f.Args)))
	call.RawSetString("event", toLua(L, orEmpty(f.Event)))
	call.RawSetString("key", glua.LString(f.Key))
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.TimeoutMS)*time.Millisecond)
	defer cancel()
	p.inCall = true
	L.SetContext(ctx)
	L.Push(fn)
	L.Push(call)
	err := L.PCall(1, 1, nil)
	L.RemoveContext()
	p.inCall = false
	if err != nil {
		L.SetTop(0)
		if ctx.Err() != nil {
			done.Kind, done.Error = KindTimeout, "the plugin ran longer than its time limit"
			return done
		}
		done.Kind, done.Error = KindScript, friendly(err)
		return done
	}
	c := &fromLua{}
	v, err := c.value(L.Get(-1), 0)
	L.SetTop(0)
	if err != nil {
		done.Kind, done.Error = KindOutput, "the plugin returned "+strings.TrimPrefix(err.Error(), "output: ")
		return done
	}
	if b, _ := json.Marshal(v); len(b) > MaxResultBytes {
		done.Kind, done.Error = KindOutput, "the plugin returned more than 64 KiB"
		return done
	}
	done.Result = v
	return done
}
