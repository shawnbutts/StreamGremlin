# ADR-0037: Lua scripts

- **Status:** Accepted
- **Date:** 2026-11-11
- **Based on:** D-006 and D-014 (Lua in supervised worker processes); [ADR-0036](0036-packages-plugins-and-themes.md) (slice 5's Lua plugins use this runtime); StreamWisp's ADR-0003 (Lua isolation) and its measurements; Streamer.bot's "Execute C# Code" sub-action, which switching streamers know.

## Context

Streamers coming from Streamer.bot write small pieces of code for what the built-in steps don't do: arithmetic, picking from a list, building a message. StreamGremlin's answer is Lua. AGENTS.md treats Lua as untrusted: separate processes, time and memory limits, audited calls.

An interpreter embedded in Go can be stopped between instructions, but it can't cap its heap, and it can't interrupt one long call into Go (a pathological pattern match, say). A crash or a memory bomb in the engine's own process takes down the stream's automation. So the script runs somewhere the engine can kill.

## Decision

**A step, "Run a Lua script" (`lua.run`).** Its parameters:
- `script`: the Lua source, written in the step. It's literal: `${…}` is refused when the workflow is saved and again before it runs, so chat and event data can never become code. At most 64 KiB.
- `args` ("Values to pass"): names and values, which may use `${…}`. This is how data reaches the script.
- `time_limit`: seconds, 0.1 to 30, default 1. Never later than the step's own timeout or the workflow's limit.

**What a script sees:** a fresh Lua 5.1 state for every run, with:
- `sg.args`: a copy of the values passed;
- `sg.event`: a copy of the event that started the run (its type, who it's about, its payload), as expressions see it;
- `sg.log(...)` and `print(...)`: a line for the run's trace and the engine's log;
- the `base`, `table`, `string` and `math` libraries, and `os.time`, `os.date` and `os.clock`.

Removed: `dofile`, `loadfile`, `load`, `loadstring`, `require`, `module`, `collectgarbage`, `getfenv`, `setfenv`, `newproxy`, the rest of `os`, and `io`, `debug`, `package`, `coroutine` and `channel`. gopher-lua never loads bytecode.

**What it gives back:** the step's output is `result` (what the script returned: a value, a list or a table with text keys), `log` (up to 100 lines, each up to 1,000 characters) and `log_dropped` when more were written. Later steps read `${steps.<id>.output.result…}`. A table with both list and text keys comes back with its numbers as text keys; an empty table comes back as `{}`. Functions and other Lua-only values are refused, as are numbers that aren't finite. The result is at most 32 levels deep, 10,000 values and 64 KiB.

**Random numbers are `crypto/rand`'s.** `math.random` draws from it, so a script picking a winner gets a sound random source (AGENTS.md). `math.randomseed` does nothing: scripts written elsewhere often call it, and its numbers can't be seeded.

**A script changes nothing outside itself** in this slice: no chat, no OBS, no points or variables. It computes, and the steps after it act. So a retried or duplicated run is harmless, and a test event runs it for real. Calls that change things come with plugins' approved permissions (slice 5).

### The worker

The script runs in a **separate worker process**: the same StreamGremlin binary (the desktop app or `streamgremlin`), started with a hidden first argument, `__lua-worker`. It talks to the engine over its standard input and output in frames: a 4-byte big-endian length, then JSON, at most 1 MiB.
- The engine sends `run` (an id, the script and its SHA-256, the values, the event, the time limit). The worker checks the digest before loading the script.
- The worker sends `log` lines as they're written, so they're kept even when the script then hangs, and `done` (the result, or an error and its kind: `script`, `timeout`, `output`).
- The worker never gets the engine's environment, which may hold credentials: only an explicit one (and `SYSTEMROOT` on Windows). Its working folder is the system's temporary folder. It exits when its input closes, so it never outlives the engine.

**Limits, two layers:**
1. In the worker: the time limit as the Lua state's context, checked between instructions; a call stack of 200 frames and a data stack of at most 256 Ki slots; `string.rep` results at most 1 MiB, `string.format` widths and precisions at most 99 (as in Lua). The heap is capped at 64 MiB: the Go runtime's soft limit is set to 75% of it, a watchdog samples `runtime/metrics` every 5 ms, and past the cap the worker says so and exits with code 3.
2. In the engine: a watchdog **kills the worker** when it hasn't answered 250 ms after the time limit, or when the run is cancelled. Killing is the only stop the engine relies on.

**Pool:** at most 4 workers run at once (more runs wait their turn, within their time), and at most 2 idle ones are kept. A worker that timed out, hit its memory cap, crashed or broke the protocol is killed, never reused. Every run has a fresh Lua state, so nothing carries over between runs; plugins (slice 5) get workers of their own.

**Logged:** every run, with its workflow, run and step, the script's digest (12 characters), how long it took and how it ended; every `sg.log` line; a worker killed, and why. The trace keeps the script as the step's input (cut at 4 KiB, like every input) and the output above.

**Trying a script:** `POST /v1/lua/try` (`workflows.write`: it runs code the streamer wrote) runs a script with values and a made-up test event and answers with the result, log and error. The step editor's **Try it** button and `streamgremlin lua try FILE` use it.

**Failures say what happened:** "line 3: attempt to add a nil value", "the script ran longer than its 1s time limit", "the script used more than its 64 MiB of memory", "the Lua worker stopped unexpectedly". The step fails, and "continue on error" applies as for any step.

## Alternatives considered

- **In-process gopher-lua only:** cheap to stop between instructions, but nothing caps its memory or stops a long call into Go, and a crash takes the engine with it. Kept as the first layer.
- **Lua 5.4 in pure Go (`github.com/arnodel/golua`):** per-runtime CPU and memory limits, but pre-1.0. It can replace gopher-lua behind the same protocol later, if it passes the same tests.
- **C Lua or LuaJIT through cgo:** a native crash, and the builds stop being cgo-free. Rejected.
- **Scripts as their own library (a Scripts screen, steps naming a script):** one more place to look, and Streamer.bot keeps the code in the sub-action. The script lives in the step; a template or a plugin can carry a shared one.
- **Running the script for test events as a dry run:** it has no outside effects to skip, so it runs for real.
- **A separate worker binary:** one more file to ship, sign and find on three systems. The app runs itself.

## Consequences

- Each run costs a process round trip, and a new worker costs a process start (tens of milliseconds); idle workers are kept warm.
- Values cross as JSON: numbers are Lua's (float64), so whole numbers beyond 2^53 lose precision.
- **Not claimed:** that this is a sandbox against a determined attacker on the same machine. The worker runs as the same user; it has no libraries for files, processes or the network, and the engine kills it at its limits. OS sandboxes (seccomp, the macOS sandbox, AppContainer) are future work.
- Backups carry scripts with no change: they're part of their workflow's definition. No migration.
