# ADR-0050: Security and load testing

- **Status:** Accepted
- **Date:** 2026-11-15
- **Based on:** D-110 (M9, an unsigned wider beta), D-111 (slice 1). StreamWisp's ADR-0050 was read for the design; nothing was copied.

## Context

Before more streamers use StreamGremlin, the feature cut asks for "fuzzing, load tests, route guard sweep". The API already had per-route permissions, loopback-only listening, Host and Origin checks and a test that the app calls every route, but nothing proved every route was guarded. Parsers of outside input (chat, EventSub, YouTube's chat stream, add-on packages and their messages, imported files) had example tests only. Nothing measured how the engine holds up when chat surges.

## Decision

### The route sweep

`TestEveryRouteIsGuarded` (internal/api) walks the route table and, for every route:

- refuses a foreign `Host` and a foreign `Origin` (403), public or not;
- except the two public routes (`GET /v1/health`, `POST /v1/pair`), refuses no token, a wrong one, a lengthened one and a differently cased one (401);
- refuses a paired Stream Deck's token (403) unless the route's permission is one a Stream Deck holds;
- fails a route that changes something (not GET) but needs only a `.read` permission, unless it's listed with why. Four are: backup verify, filter check, gives preview, and the window's sound play reports.

The public routes are an exact list: adding one fails the test until it's added on purpose.

### Fuzz targets

Each parser of outside input has a Go fuzz target that checks a property, not just "no panic":

| Target | Input from | Property |
|---|---|---|
| `chat.FuzzParseCommand` | chat | the name is valid, arguments trimmed, and it reads back the same |
| `twitch.FuzzChannelEnvelope` | EventSub | every event made is one the inbox accepts |
| `livechat.FuzzParseResponse` | YouTube's chat stream | what's decoded, written again, decodes the same |
| `lua.FuzzReadFrame` | an add-on's worker | frames stay within 1 MiB and read back the same |
| `packages.FuzzReadZip` | an add-on or theme ZIP | only clean relative paths, a hash for each file, sizes that add up |
| `templates.FuzzParseAddOn` | an add-on's templates | a valid id, plain text, blanks listed |
| `workflow.FuzzParse` | imports, templates, add-ons | the stored form is accepted again unchanged |
| `banlists.FuzzParseName`, `FuzzRead` | ban list files | only valid logins and ids; never more names than lines |
| `timers.FuzzParseCron` | schedules | the next run is later, on a minute, and stable |
| `discord.FuzzParseLink` | a pasted webhook link | only a numeric id and a token of Discord's characters |
| `filters.FuzzJudge` | chat | a known rule or none; the streamer is never caught |

- `make test-fuzz` runs each for `FUZZTIME` (15 s by default). A failure is kept in the package's `testdata/fuzz` and runs in `make test` from then on.
- When added, each ran for at least 20 s with no failure, and the two that had failed ran for a minute each after their fixes.
- Two real problems were found and fixed:
  - A `channel.raid` (or other) event missing its account made an event the inbox refused. Such events are now dropped as unreadable.
  - The stand-in YouTube's encoder dropped text on unknown message types, so its stream didn't match what was decoded.

### The load test

`make test-load` runs `TestLoadChatSurge` (build tags `acceptance load`) through the real engine and the stand-in Twitch:

- 200 viewers each type `!hype` 10 times at once; one message in ten is delivered again, exactly as before, as Twitch may.
- Four API clients read status, viewers, variables and points throughout, as the app does.
- The workflow (parallel, 8 at once, 2,000 may wait) gives a point and adds to a global and a per-viewer count.
- **It checks** that the global count is exactly 2,000, every viewer's is exactly 10, no redelivery was refused, the ledger audits clean, and no API request failed.
- **Budgets,** five or more times the baseline:

  | Measure | Baseline (Apple silicon laptop) | Budget |
  |---|---|---|
  | Runs finished per second | 45 | at least 8 |
  | Every run finished after the surge | 44 s | 250 s |
  | API p99 during the surge | 72 ms | 1 s |
  | API errors | 0 | 0 |
  | Engine resident memory | 64 MB | 400 MB |

- `STREAMGREMLIN_LOAD_REPORT=FILE` writes the measurements as JSON.
- It isn't part of `make verify` (it takes about a minute). It's run before each release (slice 5's checklist).

The load test found two problems, fixed:

- **The engine read Twitch's chat 64 messages ahead,** so a surge backed up into the connection. The stand-in Twitch gives up on a client that doesn't read for 5 seconds (Twitch doesn't document how long it waits), and the rest of the surge was lost on reconnecting. The engine now reads up to 4,096 messages ahead (about 4 MB at most), so a surge waits in the engine, not the connection.
- **The stand-in Twitch dropped messages without a word** when its own buffer filled, though its comment said it closed the connection. It now closes it, with a buffer of 8,192.

A chat redelivery whose delivery time differs from the first is now treated as the duplicate it is, without a second warning, like Twitch's other events.

### Vulnerability scans

`make vuln` (needs the network) runs govulncheck v1.9.0 on both Go modules (it reports only vulnerable code that's called) and `npm audit --audit-level=moderate` on the app's and the Stream Deck plugin's packages.

- At adoption: golang.org/x/net had two advisories in its HTML parser, not called; it was updated to v0.62.0 (19 days old) anyway. npm reports two low advisories in `deepmerge`, used by Svelte's Vite plugin while building, with no fix yet; nothing of it ships.

### SECURITY.md

How to report a problem privately (`security@streamgremlin.com`, or the person who invited you during the beta), what's supported (the newest beta), and what StreamGremlin does and doesn't protect against.

## Consequences

- A new route can't skip the token, permission, Host or Origin checks unnoticed, nor become public by accident.
- A new parser of outside input gets a fuzz target as part of being done.
- About 45 workflow runs a second, with every write durable, is the engine's measured pace with this workflow. That's far above what the streamers StreamGremlin is for see in chat; a much busier channel would queue, not lose messages.
- **Not measured:** a multi-hour soak, and ingest alone without workflows.
- **No CI** (D-007): `make test-fuzz`, `make test-load` and `make vuln` run by hand before a release.

## Alternatives considered

- **Budgets equal to the baseline:** a slower Windows laptop would fail on noise.
- **Dropping the 5-second timeout from the stand-in Twitch:** that would hide a client that falls behind. Twitch doesn't document how long it waits, so the stand-in stays strict.
- **Batching inbox writes for speed:** more complex, and the measured pace is enough. Revisit if a real stream shows queuing.
