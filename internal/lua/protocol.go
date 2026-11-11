// Package lua runs streamers' Lua scripts in separate worker processes,
// with time and memory limits (docs/adr/0037-lua-scripts.md).
//
// The worker is the same binary started with WorkerArg as its first
// argument; main calls RunWorkerIfAsked first thing. The engine side is
// Runner.
package lua

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// WorkerArg, as a binary's first argument, makes it a Lua worker.
const WorkerArg = "__lua-worker"

// MaxFrame is the largest frame either side sends or accepts.
const MaxFrame = 1 << 20

// Limits on what a script is and gives back.
const (
	MaxScriptBytes = 64 << 10
	MaxLogLines    = 100
	MaxLogLine     = 1000
	MaxResultBytes = 64 << 10
	MaxResultDepth = 32
	MaxResultCount = 10000
	// DefaultMemory is the worker's heap cap.
	DefaultMemory = 64 << 20
	// memoryExit is the worker's exit code past its heap cap.
	memoryExit = 3
)

// Error kinds.
const (
	KindScript  = "script"  // the script raised an error, or didn't load
	KindTimeout = "timeout" // it ran past its time limit
	KindMemory  = "memory"  // it used more than the worker's heap cap
	KindOutput  = "output"  // what it returned can't be given back
	KindCrashed = "crashed" // the worker stopped or broke the protocol
)

// frame is one message either way. Only the fields of its Type are set.
type frame struct {
	Type string `json:"type"` // run, load, call, log, host, reply, done, memory
	ID   uint64 `json:"id,omitempty"`

	// run
	Script    string         `json:"script,omitempty"`
	Digest    string         `json:"digest,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Event     map[string]any `json:"event,omitempty"`
	TimeoutMS int64          `json:"timeout_ms,omitempty"`

	// log
	Message string `json:"message,omitempty"`

	// load and call, for plugins (docs/adr/0038-lua-plugins.md)
	Plugin      map[string]any `json:"plugin,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
	Permissions []string       `json:"permissions,omitempty"`
	Actions     []string       `json:"actions,omitempty"`
	Action      string         `json:"action,omitempty"`
	Key         string         `json:"key,omitempty"`

	// host (the worker asks the engine to act) and reply
	Op     string `json:"op,omitempty"`
	OK     bool   `json:"ok,omitempty"`
	Reason string `json:"reason,omitempty"`

	// done
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	Kind       string `json:"kind,omitempty"`
	LogDropped int    `json:"log_dropped,omitempty"`
}

var errFrameTooBig = errors.New("lua: a frame is larger than 1 MiB")

func writeFrame(w io.Writer, f frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(b) > MaxFrame {
		return errFrameTooBig
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b))) //nolint:gosec // at most MaxFrame
	copy(buf[4:], b)
	_, err = w.Write(buf)
	return err
}

func readFrame(r io.Reader) (frame, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n > MaxFrame {
		return frame{}, errFrameTooBig
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return frame{}, err
	}
	var f frame
	if err := json.Unmarshal(b, &f); err != nil {
		return frame{}, fmt.Errorf("lua: a frame isn't JSON: %w", err)
	}
	return f, nil
}
