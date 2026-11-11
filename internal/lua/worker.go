package lua

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	glua "github.com/yuin/gopher-lua"
)

// RunWorkerIfAsked makes this process a Lua worker, and never returns, when
// its first argument is WorkerArg. Every binary that runs the engine calls it
// first thing in main (and test binaries that run scripts, in TestMain).
func RunWorkerIfAsked() {
	if len(os.Args) > 1 && os.Args[1] == WorkerArg {
		os.Exit(workerMain(os.Args[2:], os.Stdin, os.Stdout))
	}
}

// workerMain serves runs from in until it closes.
func workerMain(args []string, in io.Reader, out io.Writer) int {
	fs := flag.NewFlagSet(WorkerArg, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	memory := fs.Int64("memory", DefaultMemory, "heap cap in bytes")
	if err := fs.Parse(args); err != nil || *memory < 1<<20 {
		fmt.Fprintln(os.Stderr, "lua worker: bad arguments")
		return 2
	}
	w := &worker{out: out}
	debug.SetMemoryLimit(*memory / 4 * 3)
	go w.watchMemory(*memory)
	for {
		f, err := readFrame(in)
		if errors.Is(err, io.EOF) {
			return 0
		}
		if err != nil || f.Type != "run" {
			fmt.Fprintln(os.Stderr, "lua worker: protocol error:", err, f.Type)
			return 1
		}
		if err := w.send(w.run(f)); err != nil {
			return 1
		}
	}
}

type worker struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *worker) send(f frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeFrame(w.out, f)
}

// watchMemory ends the worker once its heap passes limit. The Go runtime's
// soft limit makes it collect hard before that; this is the hard stop.
func (w *worker) watchMemory(limit int64) {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		metrics.Read(sample)
		if sample[0].Value.Kind() == metrics.KindUint64 && sample[0].Value.Uint64() > uint64(limit) { //nolint:gosec // limit is positive
			w.mu.Lock() // no frame half-written; nothing more after this
			_ = writeFrame(w.out, frame{Type: "memory"})
			os.Exit(memoryExit)
		}
	}
}

// run runs one script and says how it ended.
func (w *worker) run(f frame) (done frame) {
	done = frame{Type: "done", ID: f.ID}
	sum := sha256.Sum256([]byte(f.Script))
	if hex.EncodeToString(sum[:]) != f.Digest {
		done.Kind, done.Error = KindCrashed, "the script didn't arrive whole"
		return done
	}
	lines := 0
	logf := func(s string) {
		lines++
		if lines > MaxLogLines {
			done.LogDropped++
			return
		}
		_ = w.send(frame{Type: "log", ID: f.ID, Message: cut(s, MaxLogLine)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.TimeoutMS)*time.Millisecond)
	defer cancel()
	defer func() {
		// A panic inside the interpreter ends this run, not the worker.
		if p := recover(); p != nil {
			done.Result, done.Kind, done.Error = nil, KindScript, fmt.Sprintf("the interpreter failed: %v", p)
		}
	}()
	L := newState(logf)
	defer L.Close()
	sg := L.GetGlobal("sg").(*glua.LTable)
	sg.RawSetString("args", toLua(L, orEmpty(f.Args)))
	sg.RawSetString("event", toLua(L, orEmpty(f.Event)))
	fn, err := L.Load(strings.NewReader(f.Script), "script")
	if err != nil {
		done.Kind, done.Error = KindScript, friendly(err)
		return done
	}
	L.SetContext(ctx)
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		if ctx.Err() != nil {
			done.Kind, done.Error = KindTimeout, "the script ran longer than its time limit"
			return done
		}
		done.Kind, done.Error = KindScript, friendly(err)
		return done
	}
	L.RemoveContext()
	c := &fromLua{}
	v, err := c.value(L.Get(-1), 0)
	if err != nil {
		done.Kind, done.Error = KindOutput, "the script returned "+strings.TrimPrefix(err.Error(), "output: ")
		return done
	}
	if b, _ := json.Marshal(v); len(b) > MaxResultBytes {
		done.Kind, done.Error = KindOutput, "the script returned more than 64 KiB"
		return done
	}
	done.Result = v
	return done
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

var (
	reChunk  = regexp.MustCompile(`(?m)^script:(\d+):\s*`)
	reSyntax = regexp.MustCompile(`script line:(\d+)\(column:(\d+)\)`)
	reSpaces = regexp.MustCompile(`:\s{2,}`)
)

// friendly turns gopher-lua's error into "line 3: …", without its trace.
func friendly(err error) string {
	msg := err.Error()
	var api *glua.ApiError
	if errors.As(err, &api) && api.Object != nil && api.Type != glua.ApiErrorSyntax {
		msg = api.Object.String()
	}
	if i := strings.Index(msg, "\nstack traceback:"); i >= 0 {
		msg = msg[:i]
	}
	msg = reChunk.ReplaceAllString(msg, "line $1: ")
	msg = reSyntax.ReplaceAllString(msg, "line $1, column $2")
	msg = strings.Replace(msg, "script at EOF", "at the end of the script", 1)
	msg = reSpaces.ReplaceAllString(msg, ": ")
	return cut(strings.TrimSpace(msg), MaxLogLine)
}

// cut shortens s to at most n bytes, on a character boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
