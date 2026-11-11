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
}

// servePlugin loads the plugin from the first frame, then answers calls
// until in closes.
func (w *worker) servePlugin(in io.Reader) int {
	f, err := readFrame(in)
	if err != nil || f.Type != "load" {
		fmt.Fprintln(stderr(), "lua worker: protocol error: the first frame isn't load")
		return 1
	}
	p := &pluginWorker{worker: w, in: in, handlers: map[string]*glua.LFunction{}}
	done := p.load(f)
	if err := w.send(done); err != nil || done.Kind != "" {
		return 0
	}
	for {
		f, err := readFrame(in)
		if errors.Is(err, io.EOF) {
			return 0
		}
		if err != nil || f.Type != "call" {
			fmt.Fprintln(stderr(), "lua worker: protocol error:", err, f.Type)
			return 1
		}
		if err := w.send(p.call(f)); err != nil {
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
	ok, reason := p.host("chat", msg)
	L.Push(glua.LBool(ok))
	if ok {
		L.Push(glua.LNil)
	} else {
		L.Push(glua.LString(reason))
	}
	return 2
}

// host asks the engine to act and waits for its answer.
func (p *pluginWorker) host(op, message string) (bool, string) {
	if !p.inCall {
		return false, "StreamGremlin can't be asked to do anything while the script loads"
	}
	if err := p.send(frame{Type: "host", ID: p.callID, Op: op, Message: message}); err != nil {
		return false, "StreamGremlin didn't answer"
	}
	reply, err := readFrame(p.in)
	if err != nil || reply.Type != "reply" || reply.ID != p.callID {
		// The engine is gone, or broke the protocol: nothing more to do.
		fmt.Fprintln(stderr(), "lua worker: protocol error waiting for a reply")
		exit(1)
	}
	return reply.OK, reply.Reason
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
