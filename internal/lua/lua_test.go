package lua

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets this test binary be the worker, as the real binaries are.
// The env switches make misbehaving workers for the engine's watchdog.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WorkerArg {
		switch {
		case os.Getenv("SG_TEST_WORKER") == "hang":
			// Reads the run and never answers, as a worker stuck in a
			// long call into Go would.
			_, _ = readFrame(os.Stdin)
			time.Sleep(time.Hour)
		case os.Getenv("SG_TEST_WORKER") == "exit":
			_, _ = readFrame(os.Stdin)
			fmt.Fprintln(os.Stderr, "panic: something broke")
			os.Exit(2)
		case os.Getenv("SG_TEST_WORKER") == "env":
			// Says what environment it got, as a log line.
			f, _ := readFrame(os.Stdin)
			_ = writeFrame(os.Stdout, frame{Type: "log", ID: f.ID, Message: strings.Join(os.Environ(), ";")})
			_ = writeFrame(os.Stdout, frame{Type: "done", ID: f.ID})
			os.Exit(0)
		}
	}
	RunWorkerIfAsked()
	os.Exit(m.Run())
}

func testLogger(w *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

func newRunner(t *testing.T, opts Options) *Runner {
	t.Helper()
	r := NewRunner(opts)
	t.Cleanup(r.Close)
	return r
}

func run(t *testing.T, r *Runner, job Job) (Result, *Error) {
	t.Helper()
	res, err := r.Run(context.Background(), job)
	if err == nil {
		return res, nil
	}
	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("Run: %v (not a script error)", err)
	}
	return res, le
}

func mustRun(t *testing.T, r *Runner, job Job) Result {
	t.Helper()
	res, le := run(t, r, job)
	if le != nil {
		t.Fatalf("Run: %s: %s", le.Kind, le.Message)
	}
	return res
}

func TestValuesInAndOut(t *testing.T) {
	r := newRunner(t, Options{})
	res := mustRun(t, r, Job{
		Script: `
local total = 0
for _, n in ipairs(sg.args.rolls) do total = total + n end
return {total = total, who = sg.event.actor.name, half = total / 2, list = {"a", "b"}, empty = {}, nothing = nil, yes = true}`,
		Args:  map[string]any{"rolls": []any{float64(3), float64(4)}},
		Event: map[string]any{"type": "chat.message", "actor": map[string]any{"name": "Fan"}},
	})
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", res.Value)
	}
	want := map[string]any{"total": int64(7), "who": "Fan", "half": 3.5, "yes": true}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %#v, want %#v", k, m[k], v)
		}
	}
	if fmt.Sprint(m["list"]) != "[a b]" || fmt.Sprint(m["empty"]) != "map[]" {
		t.Errorf("list %v, empty %v", m["list"], m["empty"])
	}
	if _, ok := m["nothing"]; ok {
		t.Errorf("a nil field came back")
	}
	// A plain value, and nothing.
	if res := mustRun(t, r, Job{Script: `return "hi " .. sg.args.who`, Args: map[string]any{"who": "you"}}); res.Value != "hi you" {
		t.Errorf("plain value = %#v", res.Value)
	}
	if res := mustRun(t, r, Job{Script: `local x = 1`}); res.Value != nil {
		t.Errorf("no return = %#v", res.Value)
	}
	// Mixed tables come back with text keys.
	res = mustRun(t, r, Job{Script: `return {"first", [5] = "fifth", name = "n"}`})
	if fmt.Sprint(res.Value) != "map[1:first 5:fifth name:n]" {
		t.Errorf("mixed = %v", res.Value)
	}
}

// Whole numbers come back as integers, so later steps print "7", not "7.0".
func TestWholeNumbers(t *testing.T) {
	r := newRunner(t, Options{})
	res := mustRun(t, r, Job{Script: `return 3 + 4`})
	if res.Value != int64(7) {
		t.Errorf("3 + 4 = %#v", res.Value)
	}
}

func TestLog(t *testing.T) {
	r := newRunner(t, Options{})
	res := mustRun(t, r, Job{Script: `print("a", 1, true) sg.log("b") for i = 1, 150 do sg.log("line " .. i) end sg.log(string.rep("x", 2000))`})
	if len(res.Log) != MaxLogLines || res.Log[0] != "a\t1\ttrue" || res.Log[1] != "b" || res.Log[99] != "line 98" {
		t.Errorf("log = %d lines, %q", len(res.Log), res.Log[:3])
	}
	if res.LogDropped != 53 {
		t.Errorf("dropped = %d", res.LogDropped)
	}
	res = mustRun(t, r, Job{Script: `sg.log(string.rep("é", 1000))`})
	if len(res.Log[0]) > MaxLogLine+len("…") || !strings.HasSuffix(res.Log[0], "…") {
		t.Errorf("long line kept %d bytes", len(res.Log[0]))
	}
	// Lines written before a failure are kept.
	_, le := run(t, r, Job{Script: `sg.log("before") error("boom")`})
	if le == nil || le.Kind != KindScript || len(le.Log) != 1 || le.Log[0] != "before" {
		t.Errorf("failure = %+v", le)
	}
}

func TestScriptErrors(t *testing.T) {
	r := newRunner(t, Options{})
	for _, c := range []struct{ script, want string }{
		{"local x = \nreturn (", "line 2, column 6 near 'return': syntax error"},
		{"x(", "at the end of the script: syntax error"},
		{"local a\nlocal b = a + 1", "line 2: "},
		{"error('no luck')", "line 1: no luck"},
		{"error({})", "table"},
		{"local function f() return f() + 1 end return f()", "stack overflow"},
	} {
		_, le := run(t, r, Job{Script: c.script})
		if le == nil || le.Kind != KindScript || !strings.Contains(le.Message, c.want) {
			t.Errorf("%q: %+v, want %q", c.script, le, c.want)
		}
		if le != nil && strings.Contains(le.Message, "stack traceback") {
			t.Errorf("%q: the trace is in the message: %s", c.script, le.Message)
		}
	}
}

func TestSandbox(t *testing.T) {
	r := newRunner(t, Options{})
	res := mustRun(t, r, Job{Script: `
local gone = {}
for _, name in ipairs({"io", "debug", "package", "coroutine", "channel", "require", "module", "dofile", "loadfile",
  "load", "loadstring", "collectgarbage", "getfenv", "setfenv", "newproxy", "_printregs"}) do
  if _G[name] ~= nil then table.insert(gone, name) end
end
for _, name in ipairs({"execute", "exit", "getenv", "remove", "rename", "tmpname", "setenv", "setlocale", "difftime"}) do
  if os[name] ~= nil then table.insert(gone, "os." .. name) end
end
if string.dump ~= nil then table.insert(gone, "string.dump") end
return {left = gone, time = type(os.time()), date = os.date("%Y") ~= nil, clock = type(os.clock())}`})
	m := res.Value.(map[string]any)
	if left, _ := m["left"].(map[string]any); m["left"] != nil && len(left) != 0 {
		t.Errorf("still there: %v", m["left"])
	}
	if l, ok := m["left"].([]any); ok && len(l) > 0 {
		t.Errorf("still there: %v", l)
	}
	if m["time"] != "number" || m["date"] != true || m["clock"] != "number" {
		t.Errorf("os = %v", m)
	}
	// Each run starts fresh: globals don't carry over.
	mustRun(t, r, Job{Script: `leftover = 1`})
	if res := mustRun(t, r, Job{Script: `return leftover`}); res.Value != nil {
		t.Errorf("a global carried over: %v", res.Value)
	}
}

func TestRandom(t *testing.T) {
	r := newRunner(t, Options{})
	res := mustRun(t, r, Job{Script: `
math.randomseed(42)
local seen = {}
for i = 1, 2000 do
  local d = math.random(6)
  if d < 1 or d > 6 or d ~= math.floor(d) then error("out of range: " .. d) end
  seen[d] = true
  local e = math.random(-2, 2)
  if e < -2 or e > 2 then error("out of range: " .. e) end
  local f = math.random()
  if f < 0 or f >= 1 then error("out of range: " .. f) end
end
local n = 0 for _ in pairs(seen) do n = n + 1 end
return n`})
	if res.Value != int64(6) {
		t.Errorf("faces seen = %v", res.Value)
	}
	// randomseed does nothing: two seeded runs differ.
	a := mustRun(t, r, Job{Script: `math.randomseed(1) local s = "" for i = 1, 20 do s = s .. math.random(9) end return s`})
	b := mustRun(t, r, Job{Script: `math.randomseed(1) local s = "" for i = 1, 20 do s = s .. math.random(9) end return s`})
	if a.Value == b.Value {
		t.Errorf("seeded runs repeat: %v", a.Value)
	}
	if _, le := run(t, r, Job{Script: `return math.random(5, 1)`}); le == nil || !strings.Contains(le.Message, "empty") {
		t.Errorf("empty interval: %+v", le)
	}
}

func TestFormat(t *testing.T) {
	r := newRunner(t, Options{})
	res := mustRun(t, r, Job{Script: `return {
  string.format("%5.2f|%d|%s|%-4s|%x|%X|%o|%c|%%|%e|%g|%i|%+d|%05d", 3.14159, 42, "hi", "ab", 255, 255, 8, 65, 12345.678, 0.5, 7, 3, 42),
  string.format("%q", 'say "hi"\n'),
  string.format("%s and %s", 1, true),
  ("x"):rep(3),
}`})
	got := res.Value.([]any)
	want := []any{` 3.14|42|hi|ab  |ff|FF|10|A|%|1.234568e+04|0.5|7|+3|00042`, "\"say \\\"hi\\\"\\\n\"", "1 and true", "xxx"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("format %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, bad := range []string{`string.format("%100d", 1)`, `string.format("%.100f", 1)`, `string.format("%v", 1)`, `string.format("%d")`,
		`string.rep("x", 2000000)`, `string.rep("ab", 600000)`} {
		if _, le := run(t, r, Job{Script: "return " + bad}); le == nil || le.Kind != KindScript {
			t.Errorf("%s: %+v", bad, le)
		}
	}
}

func TestOutputLimits(t *testing.T) {
	r := newRunner(t, Options{})
	for _, c := range []struct{ script, want string }{
		{`return function() end`, "function"},
		{`return {f = print}`, "function"},
		{`return 0/0`, "finite"},
		{`return 1/0`, "finite"},
		{`local t = {} t.self = t return t`, "deep"},
		{`local t = {} for i = 1, 10001 do t[i] = i end return t`, "values"},
		{`local t = {} for i = 1, 100 do t[i] = string.rep("x", 1000) end return t`, "64 KiB"},
		{`return {[true] = 1}`, "key"},
	} {
		_, le := run(t, r, Job{Script: c.script})
		if le == nil || le.Kind != KindOutput || !strings.Contains(le.Message, c.want) {
			t.Errorf("%s: %+v, want %q", c.script, le, c.want)
		}
	}
	if _, le := run(t, r, Job{Script: strings.Repeat("-", MaxScriptBytes+1)}); le == nil || !strings.Contains(le.Message, "64 KiB") {
		t.Errorf("long script: %+v", le)
	}
	for _, d := range []time.Duration{time.Millisecond, 31 * time.Second} {
		if _, le := run(t, r, Job{Script: "return 1", TimeLimit: d}); le == nil || !strings.Contains(le.Message, "0.1 to 30") {
			t.Errorf("limit %s: %+v", d, le)
		}
	}
}

// A loop stops at the time limit, inside the worker, which is kept.
func TestTimeLimit(t *testing.T) {
	r := newRunner(t, Options{})
	start := time.Now()
	_, le := run(t, r, Job{Script: `sg.log("start") while true do end`, TimeLimit: 200 * time.Millisecond})
	took := time.Since(start)
	if le == nil || le.Kind != KindTimeout || le.Message != "the script ran longer than its 200ms time limit" {
		t.Fatalf("loop: %+v", le)
	}
	if took > 200*time.Millisecond+grace+time.Second {
		t.Errorf("took %s", took)
	}
	if len(le.Log) != 1 {
		t.Errorf("log before the loop = %v", le.Log)
	}
	r.mu.Lock()
	idle := len(r.idle)
	r.mu.Unlock()
	if idle != 1 {
		t.Errorf("the worker that stopped itself wasn't kept: %d idle", idle)
	}
	// The step's own deadline, when sooner, wins.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, Job{Script: `while true do end`, TimeLimit: 5 * time.Second})
	if !errors.As(err, &le) || le.Kind != KindTimeout || !strings.Contains(le.Message, "what was left of the step's time") {
		t.Errorf("deadline: %v", err)
	}
}

// A worker that never answers is killed at the limit and grace.
func TestWatchdogKillsAHungWorker(t *testing.T) {
	r := newRunner(t, Options{Env: []string{"SG_TEST_WORKER=hang"}})
	start := time.Now()
	_, le := run(t, r, Job{Script: `return 1`, TimeLimit: 200 * time.Millisecond})
	took := time.Since(start)
	if le == nil || le.Kind != KindTimeout {
		t.Fatalf("hung: %+v", le)
	}
	if took < 200*time.Millisecond+grace || took > 200*time.Millisecond+grace+time.Second {
		t.Errorf("killed after %s", took)
	}
	r.mu.Lock()
	idle := len(r.idle)
	r.mu.Unlock()
	if idle != 0 {
		t.Errorf("a killed worker was kept")
	}
}

func TestMemoryCap(t *testing.T) {
	r := newRunner(t, Options{Memory: 32 << 20})
	_, le := run(t, r, Job{Script: `local t = {} for i = 1, 1e9 do t[i] = string.rep("x", 1000) .. i end`, TimeLimit: 20 * time.Second})
	if le == nil || le.Kind != KindMemory || le.Message != "the script used more than its 32 MiB of memory" {
		t.Fatalf("memory: %+v", le)
	}
	// The next run gets a new worker.
	if res := mustRun(t, r, Job{Script: `return 1`}); res.Value != int64(1) {
		t.Errorf("after: %v", res.Value)
	}
}

func TestCancel(t *testing.T) {
	r := newRunner(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := r.Run(ctx, Job{Script: `while true do end`, TimeLimit: 10 * time.Second})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Errorf("cancel: %v after %s", err, time.Since(start))
	}
}

func TestCrashedWorker(t *testing.T) {
	var logs bytes.Buffer
	r := newRunner(t, Options{Env: []string{"SG_TEST_WORKER=exit"}, Logger: testLogger(&logs)})
	_, le := run(t, r, Job{Script: `return 1`})
	if le == nil || le.Kind != KindCrashed || le.Message != "the Lua worker stopped unexpectedly" {
		t.Fatalf("crash: %+v", le)
	}
	if !strings.Contains(logs.String(), "something broke") {
		t.Errorf("the worker's last words weren't logged: %s", logs.String())
	}
}

// The worker never sees the engine's environment.
func TestWorkerEnvironment(t *testing.T) {
	t.Setenv("STREAMGREMLIN_TEST_SECRET", "hunter2")
	r := newRunner(t, Options{Env: []string{"SG_TEST_WORKER=env"}})
	res := mustRun(t, r, Job{Script: `return 1`})
	if len(res.Log) != 1 || strings.Contains(res.Log[0], "hunter2") || strings.Contains(res.Log[0], "HOME=") || strings.Contains(res.Log[0], "PATH=") {
		t.Errorf("worker environment: %q", res.Log)
	}
}

func TestManyAtOnce(t *testing.T) {
	r := newRunner(t, Options{})
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.Run(context.Background(), Job{Script: `return sg.args.n * 2`, Args: map[string]any{"n": float64(i)}})
			if err != nil {
				errs <- err
				return
			}
			if res.Value != int64(i*2) {
				errs <- fmt.Errorf("%d: got %v", i, res.Value)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	r.mu.Lock()
	idle := len(r.idle)
	r.mu.Unlock()
	if idle > maxIdle {
		t.Errorf("%d idle workers kept", idle)
	}
}

func TestClosed(t *testing.T) {
	r := NewRunner(Options{})
	mustRun(t, r, Job{Script: `return 1`})
	r.Close()
	if _, le := run(t, r, Job{Script: `return 1`}); le == nil || le.Kind != KindCrashed {
		t.Errorf("after Close: %+v", le)
	}
}

func TestWorkerChecksDigest(t *testing.T) {
	var out bytes.Buffer
	w := &worker{out: &out}
	done := w.run(frame{Type: "run", ID: 1, Script: "return 1", Digest: "00", TimeoutMS: 1000})
	if done.Kind != KindCrashed || !strings.Contains(done.Error, "whole") {
		t.Errorf("digest: %+v", done)
	}
}

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	if err := writeFrame(&b, frame{Type: "log", Message: strings.Repeat("x", MaxFrame)}); !errors.Is(err, errFrameTooBig) {
		t.Errorf("big frame written: %v", err)
	}
	b.Reset()
	b.Write([]byte{0x7f, 0, 0, 0})
	if _, err := readFrame(&b); !errors.Is(err, errFrameTooBig) {
		t.Errorf("big frame read: %v", err)
	}
	b.Reset()
	b.Write([]byte{0, 0, 0, 2, '{', 'x'})
	if _, err := readFrame(&b); err == nil {
		t.Errorf("bad JSON read")
	}
}
