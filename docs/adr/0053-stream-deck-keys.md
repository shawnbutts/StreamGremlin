# ADR-0053: Stream Deck keys beyond buttons, and what a deck may use

- **Status:** Accepted
- **Date:** 2026-11-16
- **Based on:** D-123; [ADR-0042](0042-paired-clients.md) (paired clients and the deck's own token); [ADR-0040](0040-buttons.md) (presses that run once).

## Context

The Stream Deck plugin's token can list and press buttons, and nothing else (ADR-0042). It's kept in the plugin's global settings, which anything running as the user can read, so its reach is the harm a copied token could do.

D-123 adds counter keys (a variable's value, changed by a press), status keys (live, chat, OBS, the chat lock) and switch keys (a workflow, a timer or the chat lock, on or off). Giving the deck `variables.write`, `workflows.write`, `timers.write` and `moderation.write` would let a copied token rewrite any variable, turn off any workflow, delete timers and moderate anyone.

## Decision

### The streamer chooses what a deck may use

A setting, `streamdeck` in the settings store, lists:
- `counters`: up to 20 global variable names;
- `switches`: up to 20 of `{kind, id}`, where kind is `workflow` or `timer` with its id, or `chat_lock` with no id.

It's empty at first. Connections → Stream Deck edits it (`GET` and `PUT /v1/settings/streamdeck`, admin only). It applies to every paired deck. Every request is checked against the setting as it is then, so un-ticking something refuses the next press.

### Two new permissions for the `stream_deck` kind

| Kind | Permissions |
|---|---|
| `stream_deck` | `self.read`, `buttons.read`, `buttons.press`, `deck.read`, `deck.use` |

They reach only these routes:

- `GET /v1/deck` (`deck.read`): the status, and each chosen counter and switch with its value or state:
  ```json
  {"status": {"live": true, "chat": "connected", "obs": "connected", "chat_locked": false},
   "counters": [{"name": "deaths", "value": 7}],
   "switches": [{"kind": "workflow", "id": "wf_…", "label": "Hello", "on": true},
                {"kind": "chat_lock", "label": "Chat lock", "on": null}]}
  ```
  `live` and `chat_locked` are `null` when unknown: live from the newest real stream-started, stream-stopped, online or offline event (as timers use); the lock from StreamGremlin's own moderation record, and unknown while the streamer's Twitch account isn't signed in. A counter that isn't set yet is `null`, shown as 0; one holding something other than a number is shown as "?" and refused. A switch whose workflow or timer was deleted is listed with `"on": null` and `"gone": true`.
- `POST /v1/deck/counters/{name}` (`deck.use`): `{"change": "add" | "take" | "reset", "request_id"}`. Adds one, takes one but never below 0, or sets 0, atomically. The same request id within 10 minutes answers the first result again without changing anything (kept in memory; a restart forgets them, and the plugin retries only within seconds). A name that isn't a chosen counter is refused (403 `forbidden`), as is one whose value isn't a number (409).
- `PUT /v1/deck/switches/{kind}/{id}` (`deck.use`): `{"on": true | false}`, idempotent by its nature. `{id}` is `-` for `chat_lock`. Not chosen: 403. A workflow is turned on or off as the app does (refused, as there, when its command clashes). A timer keeps everything but `enabled`. The chat lock: when it's already as asked, nothing is sent; otherwise it locks or unlocks through moderation, recorded `from: stream_deck`, and Twitch's answer (done, refused, unknown) is returned. A retry after a lost answer finds it already as asked.

The admin token can call all three, so the app can show what the deck sees.

### What the token still can't do

Read or change any variable, workflow or timer that isn't chosen; list workflows, timers or variables; moderate anything but the chat lock (moderation itself refuses any other action recorded as from `stream_deck`); send chat; read events, viewers or chat. Status holds no names, messages or records.

### Recording

A counter change is saved `updated_by: "stream_deck:<client id>"`; switch changes and locks are logged with the deck's client id; a lock is in the moderation record with `from: stream_deck`. Values and tokens are never logged.

## Consequences

- An existing paired deck gets the new permissions at once (permissions come from its kind), but can use nothing until the streamer chooses.
- A copied token could now flip the chosen switches and counters, and lock or unlock chat if the streamer chose it. That's what the streamer allowed for the deck; Forget refuses it at once.
- The plugin polls `GET /v1/deck` with the buttons every 3 seconds while such keys show, and after each press.
