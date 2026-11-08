# ADR-0014: Quotes

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp's quotes (part of its counters and quotes module; the steps `quotes.add`, `quotes.get` and `quotes.delete`), and Streamer.bot's Quotes. StreamWisp's separate counters aren't carried over (D-024).

## Context

F1 includes quotes (D-004). Streamer.bot's docs (checked 2026-11-08):
- **The Quotes guide** is a stub ("Documentation Needed").
- **The sub-actions:** Add Quote, Get Quote (random, or by its id), Delete Quote and Get Quote Count.
- **What a quote stores:** its id, the text, who added it (name and id), the platform, the category set on the channel then, and the time.
- **Numbering:** deleting "does not change the Quote ID of following quotes". Get Quote Count "is not necessarily the same as the last Quote ID". A Re-index menu item renumbers, and "can not be undone".
- **The built-in `!quote` command was removed in v1.0.0.** Streamers build `!quote`, `!quoteadd` and `!quotedel` from the sub-actions, following an example page. That example limits adding and deleting to moderators.

## Decision

### A quote (`internal/quotes`, migration `0009_quotes.sql`)

| Field | Meaning |
|---|---|
| `id` | its number: counts up and is never reused (SQLite AUTOINCREMENT) |
| `text` | 1 to 500 characters; runs of spaces become one |
| `added_by` | a name to show (up to 100) |
| `added_by_id` | `<platform>:<user id>` when a viewer added it from chat |
| `platform` | where it was added |
| `category` | optional, up to 140 |
| `created_at`, `updated_at` | when it was added, and last changed |

- **No re-index:** it can't be undone, and it breaks every number people have shared. Gaps are fine.
- **Up to 100,000 quotes.**
- **Adding is safe to repeat:**
  - an add can carry a request key (unique);
  - repeating it, or racing it, returns the first quote;
  - the workflow step uses its idempotency key, so a retried run adds once;
  - the app sends a fresh id with each add.
- **Numbers as chat types them:** `5` or `#5`.

### Workflow steps (group "Quotes")

| Step | With | Gives back |
|---|---|---|
| `quotes.add` "Add a quote" | `text`; `by` (default: the viewer behind the event, or "you"); `category` | `id`, `text`, `by`, `category`, `date`, `count` |
| `quotes.get` "Pick a quote" | `number` (empty: at random) | `found`; the same fields as Add; `count` |
| `quotes.delete` "Delete a quote" | `number` | `deleted`, `id` |

- **Not found is an answer, not a failure:** `found: false` or `deleted: false`, with `count` (and `error` when chat typed something that isn't a number), so the workflow can say so in chat.
- **Add and Delete change StreamGremlin's own data.** For a test event they only say what they would do: Add gives the number the quote would get, and Delete whether there's such a quote. Nothing changes.
- **Pick at random** uses `crypto/rand`.
- **The step can read the event's viewer** through `Call.Viewer()`.

### Chat commands

Three templates, in a new **Quotes** category:
- `!quote`: anyone; `!quote 5`, or at random.
- `!quoteadd <text>`: mods and you.
- `!quotedel <n>`: mods and you.

They are ordinary workflows: editable, checked for one answer per command, and runnable as tests. The Quotes screen adds all three and turns them on in one step, saying which turn-on was refused (a taken command, say).

### API, CLI and screen

- **API**, with new permissions `quotes.read` and `quotes.write`:
  - `GET /v1/quotes?q=&limit=`: newest first, searching text, who added it, the category or a number, with `count` and `next`.
  - `POST /v1/quotes {text, added_by, category, request_id}`: 201, or 200 for a repeat.
  - `PUT /v1/quotes/{id}`: text, who added it and category; the number and date stay.
  - `DELETE /v1/quotes/{id}`.

  `added_by_id` and `platform` can't be set through the API: only a viewer's own chat sets them.
- **CLI:** `streamgremlin quotes [-q] [-json]`, `quotes add [-by] TEXT`, `quotes show N`, `quotes edit N TEXT` and `quotes delete N`.
- **The Quotes screen** (Ctrl/⌘+6):
  - "Add the quote commands" while none exist;
  - search;
  - each quote with its number, who added it, category and date, with Edit in place and Delete (in the app's dialog, which says numbers aren't reused);
  - an Add form showing the number it will get.

## Alternatives considered

- **Built-in quote commands, outside workflows:** Streamer.bot removed its own. Workflows can be changed, and they share command ownership and tests with everything else.
- **Reusing deleted numbers, or re-indexing:** shared numbers ("remember #12?") would point at a different quote.
- **The category from Twitch:** since ADR-0015, "Add a quote" fills in the channel's current category when the step doesn't set one and Twitch has said.
- **A "Quote added" trigger:** nothing uses one yet. It can come with overlays (F2).

## Consequences

- Quotes are in backups (they're in the database) and are never swept by retention.
- Quotes from chat keep the viewer's display name at the time: a later rename doesn't change it.
- The Streamer.bot import (M4) can carry quotes over with their numbers, through the same store.
