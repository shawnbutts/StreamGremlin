# Architecture

StreamWisp's architecture is kept. It held up for seven milestones without a rewrite. This page summarizes it and lists the changes to make from the start. The details are in StreamWisp's `docs/architecture/` and the ADRs named below.

## Shape

```text
Desktop app (Wails v3 + Svelte 5) ──┐
CLI ────────────────────────────────┼── versioned local API ── Go application services
Headless server (optional) ─────────┘                               │
            ┌──────────────────────┬────────────────────────────────┼──────────────────┐
     event inbox + bus      workflow engine                      modules           integrations
            │               (queues, CEL, traces)        (ledger, giveaways, …)   (Twitch, OBS)
            └────────────── SQLite (WAL, STRICT, migrations) ───────┘
                                    │
               Lua workers and plugins (separate processes)
                                    │
                 overlay server (own loopback listener) ── OBS browser sources
```

## Kept as is

- **Go modular monolith.** One process, explicit package boundaries, modules behind narrow interfaces. No microservices, brokers or cloud services (D-001, D-005).
- **One engine, three front ends.** The desktop app, CLI and headless server all reach the engine through the same authenticated API. The desktop app bridges `/v1/*` in-process. No Wails-only business logic (D-026).
- **Wails code is fenced in.** All of it lives in `cmd/desktop` and `internal/desktop`, a separate Go module, so the server and CLI build without cgo or Wails (D-020, D-026).
- **SQLite** through pure-Go `modernc.org/sqlite`: WAL, STRICT tables, UTC millisecond text timestamps, checksummed migrations with a backup before each change (D-013, D-016).
- **Events.** `<domain>.<entity>.<verb>` types, provider in `source`, a durable inbox deduplicating by event ID and provider ID, a non-blocking bus where a slow subscriber resumes from the inbox (D-004, D-015).
- **Workflows.** Immutable versions, strict YAML/JSON decoding, cost-limited CEL, persisted queues with explicit policies, step budgets and timeouts, idempotency keys per step, append-only redacted traces. Imports start disabled (D-017, D-018).
- **Dry runs.** Steps marked External or Persistent never run for simulated events. They're recorded as skipped with their inputs.
- **Secrets** in the OS keyring or an AES-256-GCM file, chosen once per data folder (D-021).
- **Adapters** declare a capability manifest and one connection state. Only `connected` means connected.
- **Overlays** on their own loopback listener, one rotatable token per browser source, snapshots and resync, acknowledgements with a timeout. The server decides outcomes (D-033).
- **Lua and plugins** in supervised separate processes, with limits and approved permissions (D-014, D-065).
- **Mocks** of every outside service, usable from tests and standalone (`mocktwitch`, `mockobs`).
- **Acceptance tests** through the real binaries, including crashes and redelivery.
- **Tooling.** Make targets, pinned tools in `./bin/tools`, a pinned CI container, reproducible release builds with SBOMs.

## Changes from the start

1. **The name lives in one place.** Module path, binary names, data folder, environment variable prefix and format names (`<name>.workflow/v1`) come from one constant or are derived from it. StreamWisp's rename touched every format name (D-079).
2. **One baseline migration before the first public release.** During development the schema may be rewritten. Migrations freeze at the first public release.
3. **Built-in Twitch Client ID from the start.** Injected at build time from CI secrets, never committed. Paste-your-own stays as an override (D-131).
4. **Platform-neutral where it's free, Twitch-only where it isn't.** Keep `source` on events and the chat router's interface, but build only the Twitch adapter until a second platform is decided.
5. **Screens are part of each feature.** Keep StreamWisp's test that fails on any API route the app doesn't call (D-083).
6. **Plugin ownership is designed before plugins.** What a plugin owns (commands, workflows, settings, points rounds), and what install, update and uninstall do.
7. **Persistent variables are designed early.** Streamer.bot users rely on globals and per-viewer values. StreamWisp never built them.
8. **Fewer ADRs.** Write an ADR only for storage, protocols, file formats and security. The rest goes in the decision log.

## Known traps

Things StreamWisp found the hard way:

- macOS's webview ignores `window.confirm` (answers no) and `<a download>`. Use the app's own dialog and a native save dialog (D-099, D-115).
- Twitch turns "B)", "R)" and ":D" into emotes. Write choices as "[B] Mercury" (D-105).
- Twitch's chat limit is 500 characters. Split long messages at word boundaries (D-063).
- Homebrew's Node 26.13 ships an npm that fails with `ERR_INVALID_PACKAGE_CONFIG`. Use Node 22 or 24.
- Streamer.bot's export format is undocumented. Type numbers and OBS states come from real exports, and a state is the option's position in the docs' dropdown list (D-119, D-121).
- The API server's 30-second write timeout limits how long a request may wait on Twitch lookups; batch and limit concurrency (D-130).
