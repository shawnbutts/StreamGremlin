# ADR-0008: Workflows

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0006 (definitions and queues), ADR-0007 (expressions and the interpreter), ADR-0008 (traces) and ADR-0015 (dry runs).

> **Amended by D-036 (2026-11-09):** a message parameter (`Editor: "message"`) may be given as several ways to say it, `{"one_of": ["…", "…"]}`: 1 to 50 texts, none empty, each checked as a template. When the step runs, one is picked with a secure random source and then filled in as usual. The trace's input shows the version used. Only message parameters take it, and sound parameters (`Editor: "sound"`, added the same day: one of several sounds, each checked as a sound's name). A plain string is unchanged. A listed sound that's later deleted from the library fails the run that picks it, naming the sound; another isn't picked in its place.

## Context

Workflows are what Streamer.bot users come for: when something happens (a chat command, an OBS scene change), do things (answer, switch scenes, show a source). They run unattended during a live stream, so they must never:
- run twice for one event;
- hang the engine;
- hide why they did or didn't run;
- let a test move the streamer's OBS or post in their chat.

## Decision

**Definitions** (`internal/workflow`, format `streamgremlin.workflow/v1`)
- **Format:** JSON, decoded strictly (unknown fields are errors), at most 256 KiB. JSON only, because the plain-words editor (slice 9) writes it and nobody needs to hand-write YAML. A YAML reader can come with the Streamer.bot import if wanted.
- **Versions:** every save that changes the content makes a new version; versions never change and are never deleted. The same content saved again keeps its version.
- **On or off** is the workflow's state, not its content. **New workflows start off**, so a pasted or imported workflow never runs before the streamer has looked at it.
- **Checked when saved, all problems at once,** each at its JSON path (`$.steps[2].with.message`):
  - every expression compiles;
  - every action exists, with its required parameters and no unknown ones;
  - limits: nesting 8 deep, 256 steps, `timeout` up to 1 hour (default 5 minutes), `max_steps` up to 10,000 (default 1,000).

**Triggers.** A trigger matches when all its set fields do:
- `type`: exact, `*` for one segment, or a final `**` for the rest;
- `sources`;
- `delivery_quality`;
- `where`: exact equality of payload paths (`chatter.role`) to JSON values, with no type conversion. A list matches any of its values ([ADR-0010](0010-editor-run-delete.md)).

Unset `delivery_quality` accepts test events too, which then run as dry runs.

**Steps.** Each is exactly one of:
- `action` + `with`, with an optional `timeout`;
- `set` (variables from expressions);
- `do` (with `else` when there's an `if`);
- `foreach` (a list, at most `max`: default 100, cap 1,000; a longer list is an error, never cut short);
- `choose` (weighted branches picked with `crypto/rand`; the roll is in the trace);
- `wait`.

Any step may have `id` (for `steps.<id>.status|output|error`), `if` and `on_error: continue`. `finally` steps always run, with their own 10-second budget, so a failed or cancelled run can still clean up.

**Expressions** are CEL (`cel.dev/cel-go` v0.32.0).
- **Variables:** `event` (type, source, actor, payload, delivery_quality…), `vars`, `steps`, `job`, `each`, `saved` (variables kept between runs; [ADR-0009](0009-variables.md)) and `args` (what a calling workflow passed; [ADR-0010](0010-editor-run-delete.md)).
- **Extensions:** strings, optional fields (`event.payload.?bits.orValue(0)`), and comparisons across number types.
- **Limits:** cost-limited (100,000 per evaluation) and interruptible. An `if` that isn't true or false is an error, never "false".
- **Templates in `with`:**
  - `"${expr}"` alone keeps its type;
  - `${…}` inside text is formatted;
  - `$${` is a literal `${`;
  - a result is at most 64 KiB.
- **Numbers:** whole numbers in payloads are integers.

**Actions** (`internal/actions`, a registry; listed at `GET /v1/actions`)
- **General:** `note`.
- **Twitch:** `chat.send`.
- **OBS:**
  - `obs.scene.switch`, `obs.source.set_visible`, `obs.filter.set`, `obs.audio.set_muted`, `obs.text.set`, `obs.media.control`;
  - `obs.recording.control`, `obs.replay.save`, `obs.screenshot`;
  - `obs.scene.snapshot_and_switch`, `obs.scene.restore` (answers `none` when nothing is held, not an error), `obs.scene.release`;
  - `obs.scene.current`.
- **Variables** ([ADR-0009](0009-variables.md)): `vars.set`, `vars.add`, `vars.delete`. They're `SavesState` actions: a test event runs them for that run only, never saving.
- **External actions,** everything reaching Twitch or OBS, never run for test (`synthetic`) events. The step is recorded as skipped "dry run", with its evaluated input, and the run continues. `note` and `obs.scene.current` aren't External: they change nothing.
- **Idempotency key:** each action gets `job id/step path`. Nothing is retried automatically: chat can't be deduplicated by Twitch, and OBS toggles aren't safe to repeat.

**Dispatch and jobs**
- **The dispatcher** reads the inbox from its own saved cursor, in order. For each event, in one transaction with the cursor's move, it records a job row for every enabled workflow that matches, including the ones that won't run (`dropped`, `rejected`, `superseded`), with the reason.
- **One job per workflow and event** (`UNIQUE(workflow_id, event_id)`), so a crash or a rewound cursor never runs one twice.
- **The very first start begins at the newest event,** so old events don't suddenly run.
- **Each job is pinned to the version current when it was queued.**
- **Queues:**

| Policy | At once | A new event while busy |
|---|---|---|
| `serial` (default) | 1 | waits, oldest first |
| `parallel` | `concurrency` (2 to 64) | waits |
| `drop` | 1 | recorded `dropped` |
| `replace` | 1 | waiting runs `superseded`, the running one cancelled |

  `max_pending` (default 100) caps waiting runs. Past it, `on_overflow` is `reject` (default; the new run is recorded `rejected`) or `drop_oldest`. At most 32 runs go at once in total.
- **Statuses:** `pending → running → succeeded | failed | cancelled | interrupted`. Finished rows never change (trigger).
- **Turning a workflow off** cancels its waiting runs; running ones finish.
- **Restart:** runs that were running are marked `interrupted` and **never re-run on their own**: they may already have posted in chat or switched OBS.
- **Traces:** one append-only row per step that ran or was skipped, with its path, status, reason, error, input, output and times.
  - Inputs are redacted: values under keys like `token`, `secret` or `password` are hidden, strings are cut at 256 bytes, and inputs are capped at 4 KiB. Outputs are capped at 8 KiB.
  - A failed trace write is logged and counted, and never changes what the workflow does.

**API** (`workflows.read`, `workflows.write`)
- Workflows: `GET /v1/workflows`, `GET`/`PUT /v1/workflows/{id}` (saving answers 400 with `problems` at JSON paths), `PUT /v1/workflows/{id}/enabled`.
- `GET /v1/actions`.
- Runs: `GET /v1/jobs?workflow=`, `GET /v1/jobs/{id}` (with the trace), `POST /v1/jobs/{id}/cancel`.

**Screen:** Workflows (Ctrl/⌘+4).
- Each workflow with its trigger in words and On, Test (sends a matching test event) and Runs buttons.
- "Add a workflow" (paste JSON, starting from an example, with problems listed at their paths, and the actions you can use).
- Recent runs with why they ran or didn't, and each run's trace.

The plain-words editor is slice 9. Settings moves to Ctrl/⌘+5 and is also Ctrl/⌘+comma; from now on Settings is always last, and comma always reaches it.

**CLI:** `streamgremlin workflows [show | save FILE | on | off]` and `streamgremlin runs [show | cancel]`.

**Examples:** `examples/workflows/` holds hello, be-right-back, back and dice. An acceptance test keeps them valid.

## Alternatives considered

- **YAML (StreamWisp):** another parser and its traps (aliases, implicit types) for files nobody hand-writes now.
- **Re-running interrupted runs:** they may have done things outside; a streamer can start them again.
- **Retrying failed actions:** chat has no idempotency key, and toggles aren't safe to repeat.
- **A `coalesce` queue (StreamWisp):** no M1 workflow needs it; it can come back with a user.

## Consequences

- Runs and traces grow without limit for now; retention comes with backups (slice 11).
- A run waiting in a queue when StreamGremlin stops runs after the restart, since it hadn't started.
- Ownership of OBS scenes is per workflow: `obs.scene.switch` from a workflow is tagged `workflow:<id>`, so a snapshot sees it as StreamGremlin's own switch (superseded), not the streamer's.
