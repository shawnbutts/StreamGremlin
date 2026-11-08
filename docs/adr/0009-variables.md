# ADR-0009: Saved variables

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** nothing in StreamWisp, which never built them. The scopes are the user's choice (D-015). Streamer.bot's [variables guide](https://docs.streamer.bot/guide/variables) was read for what switching streamers expect.

## Context

Streamers coming from Streamer.bot keep counters and per-viewer values: deaths, a viewer's lurks, whether a giveaway is open. Streamer.bot has global variables and per-user variables (stored per platform), each either saved or temporary. Workflows in StreamGremlin already have `vars`, which last for one run. They need values that outlive the run and a restart, which the streamer can see and fix by hand, and which a test never changes.

## Decision

**Scopes.** Every variable is in exactly one:

| Scope | Owner | Seen by | Read as |
|---|---|---|---|
| `global` | none | every workflow | `saved.global.<name>` |
| `viewer` | `<platform>:<user id>`, such as `twitch:12345` | every workflow, for the viewer who caused the event | `saved.viewer.<name>` |
| `workflow` | the workflow's id | that workflow only | `saved.workflow.<name>` |

- **Viewers are keyed by platform user id, never by login,** so a rename keeps their values. The last name seen is kept beside them for the screen.
- **No shared sets of workflows,** and no "per viewer within one workflow". Use a global or viewer variable with a name prefix (`giveaway_open`, `hug_last`). Sets can come later without breaking anything (D-015).
- **Workflow variables are kept by the workflow's id.** Deleting and restoring a workflow (slice 9) keeps them with it. Deleting a workflow never removes global or viewer variables.

**Names and values**
- **Names:** `^[a-z][a-z0-9_]{0,63}$`. No dots, so `saved.global.deaths` is always one variable.
- **Values:** any JSON value except null: number, text, true/false, list or map. At most 16 KiB as JSON. Whole numbers stay integers.
- **At most 1,000 names per set:** the global set, each viewer's set, and each workflow's set.

**Storage** (`internal/variables`, migration `0005_variables.sql`)
- One table, keyed by (scope, owner, name), with the value as JSON.
- Each row also records when it was changed and by whom (`workflow:<id>` or `you`).
- **Adding is atomic.** It reads and writes in one immediate transaction, so runs counting at once never lose a count.

**In workflows**
- **Reading:** when a run starts, it reads the global set, its own workflow set, and the event's viewer set (empty when the event has no viewer). Expressions see them as `saved`, and the run's own saves update that view.
  - Saves by other runs during this run show up at the next run.
  - `vars.add` always adds to the stored value and returns the new one, so counters stay exact.
  - A missing name is a CEL error. Use `saved.global.?deaths.orValue(0)` or `has(saved.global.deaths)`.
- **Actions,** in the Variables group:

| Action | With | Output |
|---|---|---|
| `vars.set` | `scope`, `name`, `value` | `value`, `previous`, `saved` |
| `vars.add` | `scope`, `name`, `amount` (default 1; negative subtracts) | `value`, `previous`, `saved` |
| `vars.delete` | `scope`, `name` | `existed`, `saved` |

  - A viewer action on an event with no viewer fails, saying so.
  - Adding to something that isn't a number fails. So does a result too big for an integer.
- **Mistakes show when saving:** a plain-text `scope` or `name` is checked when the workflow is saved, at its JSON path. A name built from an expression is checked when it runs.
  - This check is general. Any action parameter can now declare a `pattern`, and plain-text choices are checked too.
- **Test events never save variables.** The variable actions are marked `SavesState`: for a test event they still run, so the rest of the run behaves the same, but the change is kept only for that run. The trace says "test run: changed for this run only, not saved", and the output has `saved: false`.
- **Duplicates and restarts:** one job per workflow and event (ADR-0008) means a redelivered event never counts twice. An interrupted run is never re-run, so a count is never repeated.

**API** (`variables.read`, `variables.write`)
- `GET /v1/variables?scope=&owner=&q=&limit=`
- `PUT /v1/variables` with `{scope, owner, name, value}`: retry-safe, and strict about fields.
- `DELETE /v1/variables?scope=&owner=&name=`: 404 when it isn't there.
- Values never hold secrets by design, but logs record only which variable changed, never its value.

**Screen:** Variables (Ctrl/⌘+5). Settings moves to Ctrl/⌘+6 and stays on Ctrl/⌘+comma.
- **Showing them:** All / Global / Per viewer / Per workflow, and a search box for a name or a viewer. Each row shows the value, whose it is (a viewer's name, a workflow's name), and when and by whom it was last changed.
- **Changing them:** Change (Number, Text, True or false, or JSON). Delete asks first, in the app's own dialog. Add creates a global or workflow variable. Viewer variables appear once a workflow saves one.

**CLI:** `streamgremlin vars [-q TEXT] [-json] [WHERE]`, `vars set WHERE NAME VALUE` and `vars delete WHERE NAME`.
- WHERE is `global`, `viewer:<platform>:<id>` or `workflow:<id>`.
- VALUE is read as JSON, or else saved as text.

**Examples:** `examples/workflows/deaths.json` (`!death` and `!deaths`) and `lurk.json` (per viewer).

## Alternatives considered

- **Temporary (unsaved) variables across runs, as in Streamer.bot:** a run's own `vars` covers one run. Nothing in M1 needs a value shared between runs but lost at a restart. It can come with a user who does.
- **Reading each variable from the database when an expression asks:** always fresh, but it needs a custom CEL map type, and a run's view would change under it mid-run. Loading at the start is simpler and consistent, and `vars.add` keeps counting exact.
- **Saving test runs' changes:** a test during a stream would corrupt real counters.
- **Shared sets of workflows:** more to name, show and explain, for what a name prefix already does.
- **Dots in names (`giveaway.open`):** they would read as nested maps in expressions.

## Consequences

- A run loads every global variable when it starts. Even with 1,000 names at 16 KiB each, that stays bounded, and realistic sets are tiny.
- Viewer variables grow with the audience. Retention, and export with backups, come in slice 11.
- Edits made by hand on the screen and a workflow's save at the same moment: the last write wins.
