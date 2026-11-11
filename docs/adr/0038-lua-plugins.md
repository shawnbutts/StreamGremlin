# ADR-0038: Lua plugins

- **Status:** Accepted
- **Date:** 2026-11-11

> **Amended by D-073 (2026-11-11), slice 5c:**
> - **Events.** A plugin declares its events in `lua.events` (name, label, description, `values` offered as `{tokens}`). It needs `events.publish`, "Start your workflows with its own events (at most 10 a second)". Each event is `plugin.<id>.<name>` (the id's `-` as `_`), source `plugin:<id>`, confirmed, caused by the event of the call that sent it.
> - **`sg.emit(name, payload [, id])` → ok, reason** never raises an error. It's refused without the permission, for an undeclared name, past 10 a second, for an id used before, and during a call started by one of the plugin's own events, so plugins can't loop. Every emit is in the activity.
> - **Its events are triggers** in the editor, after StreamGremlin's, marked "(add-on)" (`/v1/palette`'s `add_on`). Removing a plugin turns off the workflows its events start.
> - **Timers.** `sg.after(seconds, fn)` → id, with 1 to 86,400 seconds and up to 100 waiting; `sg.cancel(id)` → whether one was waiting. The worker reports the waiting timers in each `done` frame (`timers`), and the engine sends a `fire` frame when the next is due, one at a time with calls, within the plugin's timeout. Timers are lost with the plugin's memory.
> - The protocol gains `fire`, a `host` frame's `name` and `payload`, and `done`'s `timers`.

> **Amended by D-072 (2026-11-11), slice 5b:** plugins are updated and rolled back as looks are (ADR-0036, D-068). An update or roll back asks only when that version asks for a permission not approved now, and then for every one it asks for (`?permissions=` on `POST /v1/packages` and `POST /v1/packages/{id}/rollback`, 409 `confirm_permissions`). The check's `plugin_update`, and a plugin's `rollback`, name the new permissions, the steps that version doesn't have, the workflows using them (turned off when it's installed) and the settings that go back to their default. A plugin that's on is loaded again, fresh, as that version. Its events and timers move to slice 5c.
- **Based on:** [ADR-0036](0036-packages-plugins-and-themes.md) (what a package and a plugin own: install, settings, removal, the Add-ons screen); [ADR-0037](0037-lua-scripts.md) (the Lua sandbox and worker); StreamWisp's ADR-0041 (out-of-process plugins) and ADR-0067 (plugins in Lua), D-106, D-107 and D-111.

## Context

ADR-0036 decided what a plugin owns and left its script, permissions, actions and events to this ADR. ADR-0037 runs a streamer's own script once per step, with a fresh state, changing nothing outside itself. A plugin is someone else's code that does more: it keeps state between calls (a game's scores), offers steps of its own, and asks StreamGremlin to act for it (say something in chat). So it needs a worker that stays up, a list of what it may ask for, approved by the streamer, and a record of every call it makes.

## Decision

### Built in two parts

- **5a:** the manifest's `lua` section; installing with each permission approved; on and off; settings; the plugin's steps; `chat.send`; the call log; removing (its workflows turned off); the Add-ons screen and the CLI; an example plugin.
- **Templates** (D-071), between the two: a plugin's `templates/` in the gallery, in the add-on's own section.
- **5b:** updates and roll back for plugins (asking again for more permissions, naming workflows that use removed steps). Built: D-072.
- **5c:** `events.publish` and its events as triggers; timers (`sg.after`). Built: D-073.
- Slice 6 adds points (ADR-0036).

Until 5b, installing another version of an installed plugin was refused, saying to remove it first (as themes were in 3a).

### The manifest

`kind: "lua"` and a `lua` section:

```json
"lua": {
  "script": "plugin.lua",
  "permissions": ["chat.send"],
  "timeout": 10,
  "actions": [
    { "name": "roll", "label": "Roll dice", "description": "Rolls dice and gives back the total.",
      "params": [ { "name": "dice", "label": "How many dice", "kind": "number", "default": 1, "min": 1, "max": 10 } ] }
  ],
  "settings": [ { "key": "sides", "label": "Sides on each die", "kind": "number", "default": 6, "min": 2, "max": 100 } ]
}
```

- `script`: one `.lua` file in the package, at most 256 KiB, UTF-8. No `require`: one file.
- `permissions`: from those this build knows. In 5a that's `chat.send`. An unknown one is refused, naming it.
- `timeout`: seconds a call may take, 1 to 30, default 10.
- `actions`: 1 to 50. A name is up to 32 lowercase letters, digits and `_`. Each has a label, a description, and up to 20 `params`: `text` (up to `max` characters, at most 500), `number` (`min`, `max`), `switch` or `choice` (`options`), each with a label, optional `default` and `required`. The step is `<id>.<name>`.
- `settings`: as a theme's (ADR-0036), one set for the plugin, never named like a secret.
- Files: `.lua`, `.json`, `.md`, `.txt` only.
- `templates/*.json`: up to 20 templates, below.

Actions are declared so StreamGremlin knows a plugin's steps without running it. The script then handles them. A declared action it doesn't handle fails when called; a handler for one not declared stops the script loading.

### Installing

As ADR-0036, and:
- the check lists each permission with what it lets the plugin do ("send messages in your chat, as the bot when one is signed in"), and each step it adds;
- **each permission is approved one by one.** The install is refused unless exactly the permissions asked for are approved (`?permissions=chat.send`, or 409 `confirm_permissions`). The app asks with a box for each; the CLI with `-allow chat.send`;
- the plugin is installed **off**.

### Running

**One worker per plugin that's on,** started when it's turned on (and when StreamGremlin starts, for those left on). It's the app's own binary started as `__lua-worker -plugin`, in the sandbox ADR-0037 describes, with the same empty environment, temporary folder, 64 MiB heap and frames. The engine sends it the script and its digest (checked before loading), the plugin's id, settings and approved permissions.

**The state stays between calls** while the worker is up: a game's scores live in the script's own variables. It's lost when the plugin is turned off, its settings change, it's updated, the worker is killed, or StreamGremlin stops. The Add-ons screen says so.

**Limits:**
- loading (the script's top level): 5 seconds;
- a call: the manifest's `timeout`, or what's left of the step's time if sooner; the worker stops the script at that, and the engine kills the worker 250 ms later;
- one call at a time: calls wait their turn, within their own time;
- a worker that's killed or crashes is started again for the next call, fresh. Five in 10 minutes and the plugin is stopped as **failed**, saying why, until it's turned on again.

**The Lua API:**

| | |
|---|---|
| `sg.plugin` | `{id, name, version}` |
| `sg.settings` | the plugin's settings, checked against its manifest |
| `sg.permissions` | the approved permissions |
| `sg.action(name, fn)` | handles a declared action, only while loading. `fn(call)` gets `call.args` (the step's values), `call.event` (the run's event, as in ADR-0037) and `call.key` (the step's idempotency key: the same when the same step runs again); what it returns is the step's output |
| `sg.log(...)`, `print(...)` | a line in StreamGremlin's log and the plugin's activity; up to 100 a call (or while loading), more are counted, not kept |
| `sg.emit(name, payload [, id])` → `ok, reason` | with `events.publish` (D-073): sends one of its declared events, `plugin.<id>.<name>`, to start workflows. At most 10 a second; never from a call its own event started; an `id` (such as `call.key`) sends it once. Never raises an error |
| `sg.after(seconds, fn)` → `id`, `sg.cancel(id)` → `bool` | a timer (D-073): `fn()` runs once, 1 to 86,400 seconds later, one at a time with calls, within the plugin's timeout; up to 100 waiting; gone with the plugin's memory |
| `sg.chat(message)` → `ok, reason` | with `chat.send`: sends to your chat, as the bot when one is signed in, else as you. At most 500 characters and 20 a minute. It never raises an error: it returns false and why (no permission, not signed in, too many, Twitch refused it) |

Everything else is ADR-0037's sandbox: no files, network, `os` beyond the clock, modules or bytecode; `math.random` from `crypto/rand`. Host calls aren't allowed while loading.

**Every call to StreamGremlin is logged** and kept in the plugin's **activity**: when, what (`chat.send` and the message, cut at 200 characters), and how it went (sent, refused and why). The activity keeps the last 1,000 entries per plugin, outlives nothing (it goes with the plugin), and is shown on Add-ons. Refusals are also in the engine's log as warnings.

### Its steps

- `<id>.<action>`, in the editor under the plugin's name, labelled "from <name> (published by <publisher>, not verified)". The step picker lists add-ons' groups after StreamGremlin's own, as "Dice tray (add-on)", each step as "Roll dice (add-on)", and a step's card says "from the Dice tray add-on" (the action's `add_on` names it). Text values can use `{viewer}` and the rest, like a message. Number values are taken as text too, so they can come from chat (`{first_word}`, as the built-in steps take numbers typed in chat); each value is checked against the manifest (type, range, options) when the step runs.
- **External:** a test event never runs them; the step is recorded as skipped with its values (ADR-0008). A plugin's effects are its own, so StreamGremlin can't run it "for this run only".
- A plugin that's off, failed or removed: its steps fail at once, saying so. Its workflows stay on while it's off (D-107: they work again when it's back).
- The output is what the handler returned: a value, list or table, with ADR-0037's limits.
- Registered when StreamGremlin starts, from every installed plugin's manifest, before workflows load; added on install, removed on removal.

### Templates (D-071)

A plugin brings ready-made workflows as templates, not workflows added for the streamer:
- one file each in `templates/`, in the gallery's format: `template` (`id`, `title`, `description`, `blanks`) and `workflow`. Strict: no category (the add-on is its section), never a starter, no `add_on`, each blank an empty text in the workflow;
- **checked when the plugin is checked**, as the editor would save each: StreamGremlin's own steps and the plugin's, not another add-on's. A problem at a blank (left empty for the streamer) is skipped. One that fails refuses the package, naming the template;
- **in the gallery**, each installed add-on is a section of its own, "Dice tray (add-on)", after StreamGremlin's, saying who published it (not verified) and, while it's off, to turn it on. `/v1/templates` gives them ids `<plugin>/<template>`, the add-on's name as category, and `add_on` (id, name, publisher, on);
- like any template, one opens in the editor turned off, and is saved only when the streamer saves it;
- removing the add-on takes its section away; workflows made from them use its steps, so they're turned off with the rest.

### Settings, on and off, removing

- **Settings** are edited on Add-ons (and `packages set ID KEY=VALUE`), checked by the engine. Saving restarts the worker, so the script reads them fresh.
- **On and off:** turning on loads the script; if it fails to load, it stays off and the screen says why ("line 4: …"). Off stops the worker.
- **Removing** asks first, naming the workflows that use its steps; it turns them off (never deletes them), stops the worker, and deletes its files, settings and activity. Its audit stays.
- The audit (ADR-0036) gains `on`, `off`, `settings` and `failed`.

### Storage

Migration 0025: `packages.enabled`, `packages.state_error`; `package_settings` (one row per plugin, its values as JSON); `package_permissions` (the approved permissions of the installed version); `package_activity` (the call log, trimmed to 1,000 per plugin); `package_audit` rebuilt with the new actions.

### Screens, API and CLI

- **Add-ons:** one list for looks and plugins (ADR-0036). A plugin shows its kind, on or off (a switch; failed with why), its permissions, its steps, its settings (with Save), its activity, and the workflows using it. Installing a plugin shows its permissions as boxes to tick and its steps, and says what a plugin is: "It runs its own Lua in a separate process. It can't reach your files, the internet or your accounts, only what you allow below. It keeps its own memory while it's on."
- **API:** `POST /v1/packages?digest=&permissions=`, `PUT /v1/packages/{id}/enabled`, `PUT /v1/packages/{id}/settings`, `GET /v1/packages/{id}/activity`; `GET /v1/packages` adds a plugin's state, permissions, steps, settings values and the workflows using it.
- **CLI:** `packages install FILE -yes -allow PERMS`, `packages on|off ID`, `packages set ID KEY=VALUE…`, `packages activity ID`.

## Alternatives considered

- **A plugin as `lua.run` steps with a fresh state each time (ADR-0037):** no memory between commands, so no games. Rejected.
- **A worker per call:** the same, and slower.
- **Calls in parallel:** a script would need locks Lua doesn't have. One at a time, as StreamWisp's ADR-0067.
- **Steps that run for test events, with host calls faked:** the plugin's own state would change for a test. Skipped as External, as StreamWisp.
- **Approving some permissions and not others:** the plugin would have to cope with any subset. All or nothing, as StreamWisp.
- **Keeping a plugin's state across restarts:** that's storage StreamGremlin would have to back up and migrate for someone else's code. Later, if a plugin needs it, as a permission.
- **A separate Plugins screen:** ADR-0036 chose one Add-ons screen; the user agreed (D-070).

## Consequences

- A plugin can't reach files or the network, but its worker runs as the streamer, like ADR-0037's: this isn't an operating-system sandbox.
- Calls to one plugin wait for each other; a slow call slows the plugin's other steps.
- A plugin's memory goes on restart: a game in progress ends. Slice 6 refunds points held in a round when that happens.
- Lua numbers are float64: values above 2^53 lose precision.
