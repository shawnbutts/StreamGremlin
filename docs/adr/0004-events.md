# ADR-0004: Events, the inbox, and reading it live

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0004 (event naming), ADR-0013 (live stream), and D-004 and D-015 (inbox).

## Context

Twitch, OBS and later plugins send events. Workflows (slice 7), the desktop app and the CLI all react to them. Platforms redeliver: EventSub resends after a reconnect, and OBS repeats state after a reconnect. Events also arrive out of order. Workflows must never act twice on one event, and nobody may miss an event because they were slow. Streamer.bot users test triggers with fake events, so simulated events are needed from the start, and they must never cause real outside actions (D-027 in StreamWisp).

## Decision

**Envelope** (`internal/events`, format version 1)

| Field | Meaning |
|---|---|
| `event_id` | A random UUID (v4, from `crypto/rand`), assigned when the event is received. |
| `type` | `<domain>.<entity>[.<more>].<verb>`: lowercase, the verb last in the past tense, such as `chat.message.received`. Platform names (`twitch.`, `obs.`, `youtube.`, `kick.`, `provider.`) are refused as a prefix. At most 128 characters. |
| `schema_version` | The payload's version for this type, starting at 1. |
| `source` | Where the event came from: `twitch`, `obs`, `core`, and later `plugin:<id>`. |
| `source_event_id` | The platform's own id, used for deduplication. |
| `occurred_at`, `received_at` | When it happened, and when the engine got it. UTC, to the millisecond. |
| `channel_id`, `actor` | The channel, and who did it (platform, id, login, display name). |
| `payload` | A JSON object, at most 64 KiB. |
| `correlation_id`, `causation_id` | A chain of related events. The correlation id defaults to the event's own id. |
| `delivery_quality` | `confirmed`, when the platform or OBS said so; `estimated`, when worked out locally; `synthetic`, for a test event. |

**Inbox**
- **Storage:** one `events` table. `seq` is assigned in arrival order and has no gaps; it is the cursor every reader uses. Rows are append-only: a trigger refuses updates.
- **Accepting an event** happens in one write transaction:
  1. Look for an earlier event with the same `event_id`, or the same `source` and `source_event_id`.
  2. If there is one with the same content (a hash of everything except `event_id` and `received_at`), it's a duplicate: the original is returned, and nothing new happens.
  3. If there is one with different content, the new event is refused (`ErrConflict`) and logged without its payload. The first event wins.
  4. Otherwise, the event is stored.
- **Out of order:** an older event that arrives later is stored after newer ones, by arrival. Readers that care about order use `occurred_at`.
- **No bus that drops readers.** Every reader keeps its own `seq` cursor and reads from the inbox. A `Notifier` only wakes waiting readers after a commit; it never blocks the writer. A slow reader is late, never missing events, and a restart resumes from the cursor.

**Reading it live: long polling, not a WebSocket**
- `GET /v1/events?after=SEQ&wait=SECONDS` answers at once if anything is newer than `SEQ`. Otherwise it waits up to `wait` seconds (at most 20, inside the API's 30-second write timeout).
- Without `after`, it returns the newest `limit` events (1 to 200, default 50).
- `types=chat.*,broadcast.scene.changed` filters on the server. Each entry is a type, or a prefix ending in `.*`. Chat text therefore goes only to readers that ask for it.
- Every answer carries `head`, the newest `seq`. A cursor past `head` means the data folder changed, so the reader starts again.
- Permission: `events.read`.
- This works unchanged through the desktop app's in-process bridge, where Wails serves each request on its own goroutine, and through the CLI (`streamgremlin events watch`). It needs no tickets and no hijacked connections.

**Test events**
- `GET /v1/events/catalog` lists the types this build knows. Each has a label, a description, its sources, whether it's sensitive (it carries viewers' words), and the fields of its test form.
- `POST /v1/events/simulate {type, fields, request_id}` (permission `events.simulate`) builds the payload on the engine from the catalog. It stores the event with:
  - `source` set to the platform it imitates;
  - `delivery_quality: synthetic`;
  - `source_event_id: test:<request_id>`.

  Repeating a `request_id` returns the first event, so a retried click never makes two.
- **`source` is the imitated platform**, not a separate `simulator`. That way, a workflow that filters on `source: twitch` reacts to a test chat message just as to a real one. `delivery_quality` is what tells them apart, and the workflow engine (slice 7) skips outside actions for `synthetic` events.
- The catalog holds only the types M1 sends: `chat.message.received` and `chat.command.received` from Twitch, and `broadcast.scene.changed` from OBS. A type's payload shape isn't frozen until the slice that receives the real event (lesson 3: design from real data).

## Alternatives considered

- **StreamWisp's WebSocket stream with tickets:** it needs a ticket endpoint, hijacked connections the HTTP server doesn't track, and a separate path in the Wails bridge. Long polling gives the same guarantees, with no gaps and resuming by cursor, at local event rates. The overlay server (F2) has its own listener and can use WebSockets there.
- **A fan-out bus that drops slow subscribers (StreamWisp `ErrLagged`):** every subscriber still needed to resume from the inbox, so the in-memory copy added a failure mode without saving work.
- **`INSERT OR IGNORE` for duplicates:** simpler, but SQLite's AUTOINCREMENT then skips numbers, and the conflict check needs a second query anyway.
- **A separate `simulator` source:** workflows filtering on `twitch` wouldn't see test events, which defeats testing them.

## Consequences

- The inbox grows without limit for now. A retention rule, such as dropping or redacting chat payloads after some days, is decided with backups in slice 11, once real streams show how fast it grows.
- Readers that start without a cursor see only the newest page; older events are read by paging with `after`.
- Event types are a contract: workflows and Streamer.bot imports will key on them. Renaming one later needs a migration of stored workflows.

## Open questions

- Retention and redaction of chat text (slice 11).
- Whether plugins (M5) may publish into the inbox, with which types and rate limits.
