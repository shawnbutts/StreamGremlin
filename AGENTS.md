# Agent instructions

You are building **StreamGremlin**, a local-first streaming automation app for Twitch streamers, with YouTube in a later milestone (D-003). It is for streamers coming from Streamer.bot and for streamers with no bot yet, so onboarding and everyday use must be as simple as possible (D-001). It is a rebuild of an earlier project, StreamWisp, using its decisions and architecture but none of its code.

## Kickoff

The kickoff is complete: K-1 to K-7 are recorded in docs/DECISIONS.md as D-001 to D-007. Milestones are in D-006. Every commit carries a DCO `Signed-off-by` line (D-005).

## Reading order

1. docs/DECISIONS.md: what's decided, with the user's words.
2. `PRINCIPLES.md`: rules that hold for every feature.
3. `ARCHITECTURE.md`: the shape of the code and the known traps.
4. docs/FEATURES.md: the feature cut, and what's in the current milestone.
5. `LESSONS-LEARNED.md`: what went wrong last time.

StreamWisp's repository is reference material. Read its ADRs for a feature's design before building that feature again. Don't copy its code.

## Core constraints

- Go owns domain logic, networking, workflows, storage, integrations and security. The desktop app is Wails v3 with Svelte 5 and TypeScript, for presentation and state only.
- The desktop app, CLI and any headless server reach the same engine through the same authenticated API. No Wails-only business logic.
- Every streamer-facing feature has a screen in the desktop app, built in the same slice as its API. The command line is never the only way. A test fails on any API route the app doesn't call unless it's listed as CLI-only with a reason.
- Modular monolith. No microservices, message brokers, cloud services or abstractions built for a future that isn't decided.
- Only documented public platform APIs. Check current official docs. Record scopes, limits and uncertainties in the adapter's capability manifest. Never invent an outside API capability.
- Winners are chosen on the server with a cryptographically sound random source and recorded for audit. Never imply odds or requirements are fair unless verified.
- Lua and plugins are untrusted: separate processes, time and memory limits, approved permissions, audited calls.
- Secret tokens never appear in logs, exports, backups, bug reports or traces.
- Content packs contain data only and can never run code.
- Unknown and disconnected states are shown as unknown. Never invent Twitch ad end events, per-viewer ad visibility or passive viewer presence.
- Only build features in the decided feature cut. A new feature needs a decision first, naming who will use it.

## How to work

1. Pick one slice of the current milestone. Read the old ADR for it, and Streamer.bot's docs for how switching streamers expect it to behave.
2. Write tests first: unit, contract, and acceptance through the real binaries. Cover duplicates, retries, cancellation, restarts and out-of-order events. Every outside service has a mock usable from tests and standalone.
3. Build behind a narrow interface, in the package that owns it.
4. Build the screen in the same slice.
5. Run `make verify`. Run the desktop UI and accessibility checks when the UI changed.
6. Update the docs, the changelog and the decision log. Write an ADR only for storage, protocols, file formats or security.
7. Commit on a branch, fast-forward main, push, and report what changed and what was checked.

Ask the user only when a decision is irreversible, business-critical, or changes the feature cut. Otherwise choose the simplest secure, testable design and record it.

## Definition of done

A slice is done with: a screen, automated tests including an acceptance test, logs, safe retry and recovery, permission checks, docs, migration considerations, and platform capability notes.

A milestone is done when the user has used it on a real stream on macOS and Windows, and the result is written down. Linux is "lightly tested": it builds, passes CI and gets a smoke test before a release (D-003). Mocks passing isn't enough.

## Testing invariants

- Duplicate delivery never pays twice or repeats an outside action when an idempotency key exists.
- Balances never go negative unless the streamer allowed it. Ledgers are append-only.
- No ad announcement for a refused commercial request.
- A manual OBS scene change always defeats automatic scene restoration.
- Unknown or disconnected state is never presented as confirmed.
- Chat alone carries everything a viewer needs to play; overlays alone are never enough.
- Pack installs never traverse paths, download scripts, or replace another publisher's pack without an exact identity check.

## Rules carried over from StreamWisp

- Never commit the Twitch Client ID or any secret. Release builds get the Client ID at build time.
- Never run tests against the real OS keyring or the user's data folder.
- After the first public release, never edit an applied migration. Before it, keep one baseline.
- Use `crypto/rand` for anything a viewer could win; seeded random sources only in tests.
- In the desktop app, never use `window.confirm` or `<a download>`; use the app's dialog and native save dialog.
- Don't copy another product's design, wording or paid add-ons. Rebuild the behaviour from StreamGremlin's own parts.
- Keep user exports and real stream data out of the repository unless the user says a file may be committed.
