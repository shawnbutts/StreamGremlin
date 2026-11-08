# ADR-0010: The plain-words editor, running another workflow, and deleting workflows

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0062 (a workflow editor in plain words), ADR-0073 (running another workflow) and D-103 (delete and restore). Streamer.bot's Run Action sub-action and its categorised trigger and sub-action menus were the model for what switching streamers expect.

## Context

Until now, workflows were pasted as JSON. That is fine for examples, and unusable for the streamers StreamGremlin is for (D-001). Streamers also need to:
- limit a command to mods without writing an expression;
- share steps between workflows, like Streamer.bot's Run Action;
- remove a workflow without losing its history.

This changes the workflow format (additively), the storage and the API.

## Decision

### Format additions (`streamgremlin.workflow/v1`, additive)

- **A `where` value may be a list,** matching any of its values: 1 to 20 strings, numbers, true/false or null. "Mods and you" is `"chatter.role": ["moderator", "broadcaster"]`.
  - Chat events already carry `chatter.role`, the chatter's highest role: viewer, subscriber, vip, moderator or broadcaster.
  - Older builds of StreamGremlin refuse workflows that use a list.
- **`args`** joins the expression variables: what a calling workflow passed, and empty otherwise.
- **Action parameters can be `literal`:** written out, never computed with `${…}`. A computed value is refused when the workflow is saved.

### Running another workflow: `workflow.run`

The step runs another workflow's steps **now, inside the current run** (Streamer.bot's Run Action with "Run Immediately").
- **With:**
  - `workflow`: a literal id, so chat can never choose which workflow runs.
  - `args`: a map of values the called workflow reads as `args.<name>`.
- **Same event:** `${event…}` means the same in both.
- **Own variables:** the called workflow gets its own `vars` and step results. Its `saved.workflow` is its own set; global and viewer variables are shared with the caller. The step's output is `{workflow, vars}`.
- **The caller's limits and rules apply:** step budget, time limit, cancellation and the dry-run rule. The called workflow's own triggers, queue, timeout, step budget and final steps apply only when it runs by itself.
- **Trace:** its steps appear in the caller's trace under the calling step, as `steps[1]>helper.id:steps[0]`. Idempotency keys include that path.
- **Failures:** a failing called step fails the calling step, and the caller's `on_error` decides. Cancelling, timing out or running over the step budget stops the whole run.
- **It must be turned on.** "Off" means off, called or not. A helper with no trigger that fires can be on harmlessly.
- **Loops are refused when the run reaches them,** naming the chain (`a → b → a`). Chains go at most 4 workflows below the job's own. A loop can form later, when either workflow is edited, so checking only when saving wouldn't be enough.
- **The current version runs at each call,** so editing a helper changes every caller.

### Deleting and restoring (migration `0006_workflow_delete.sql`: `workflows.deleted_at`)

**Deleting:**
- turns the workflow off and marks it deleted;
- cancels its waiting runs, and stops its running one (whose final steps run);
- takes it out of the list, out of `GET /v1/workflows/{id}` and out of `workflow.run`, and it never runs again.

**What's kept:** its versions, runs and traces, which are immutable and referenced, and its own saved variables (ADR-0009).

**Restoring** brings it back, turned off.

**Its id stays reserved:** saving over it is refused with 409 `deleted`, and the app says to restore it or pick another id.

**API:**
- `DELETE /v1/workflows/{id}` and `POST /v1/workflows/{id}/restore`, each 404 when there's nothing to do;
- `GET /v1/workflows?deleted=true` lists deleted workflows, with `deleted_at`.

**CLI:** `workflows delete`, `workflows deleted` and `workflows restore`.

### The palette: `GET /v1/palette`

The palette describes what the editor offers, and the engine never reads it.
- **Triggers:**
  - each event type in plain words ("A viewer types a command"), with a category and help;
  - what it can be limited to: command, who, scene, source, on/off or text;
  - the values its messages can include, such as `{viewer}` for `event.actor.display_name`;
  - rarely used ones are marked `more`.
- **Actions:** the registry's list. Parameters now say how the editor shows them (`editor`: message, scene, source, workflow, variable or value), and rarely used actions are marked `more`.
- **Kept honest by tests:**
  - every catalogued event has a palette entry;
  - every match path exists in its event's payload;
  - every value compiles and evaluates against a test event;
  - every registered action has a label, a description, a known group and parameters the editor can show.
- **`GET /v1/actions` stays, for scripts.** The app now reads actions from the palette.

### The editor (Workflows → New workflow, or Edit)

The editor works **on the definition itself**, so anything it can't show is kept unchanged and can be edited on the JSON tab.

- **When cards:**
  - the event, chosen by its words from a grouped list;
  - its matches as controls, such as the command without "!" and "Who can use it": Everyone; Subscribers, VIPs, mods and you; VIPs, mods and you; Mods and you; Only you.
  - Other matches are listed under "Also must match", and "Or when…" adds another trigger.
- **Then cards:** each step's parameters as controls:
  - messages with an "Insert a value" menu, offering the trigger's values and saved variables;
  - pickers for OBS scenes and sources (while OBS is connected) and for workflows (those off are marked);
  - rows for "Values to pass".
  - Cards can be moved and removed. "More options" holds the step's condition, "If it fails, carry on" and its name.
- **Flow steps:** Wait (seconds), Remember a value, If… otherwise…, and Pick one at random (with chances). Loops are shown as "a step the editor can't show yet" and kept.
- **Values in text:**
  - shown as `{viewer}` and stored as `${event.actor.display_name}`;
  - `{vars.x}`, `{steps.id.output.value}`, `{args.who}` and `{saved.global.deaths}` work too;
  - other `${…}` and braces that name nothing stay as typed.
- **Conditions:** a value, a comparison (is, is not, contains, is more than, is less than) and a text, stored as CEL the editor reads back:
  - `string(x) == "…"`;
  - `string(x).lowerAscii().contains("…")`;
  - `double(x) > n`.

  Any other condition is shown and edited as an expression. A Go test checks that these forms evaluate.
- **Advanced:**
  - the id, which follows the name for a new workflow and is fixed afterwards;
  - the description;
  - the queue in words;
  - final steps.
- **Saving:** problems show beside the card they belong to and in a list. Leaving with unsaved changes asks first, in the app's own dialog.
- **Test events can set a chatter's role** (a new `role` field with `path: chatter.role`), so a mods-only workflow can still be tested with the Test button.

**Not in this slice:** the template gallery (slice 11) and a guided Test that asks who's chatting.

## Alternatives considered

- **Queued calls (the called workflow as its own job):** the dispatcher makes one decision per workflow and event, so this needs a new event per call, which changes what `${event…}` means. It can come later as a separate action if streamers ask.
- **Running turned-off workflows when called:** handy, but "off" should mean off.
- **Removing deleted workflows' rows:** versions and runs reference them, and restore would be impossible.
- **Wording in a frontend file:** it couldn't be checked against the real catalog and registry.
- **A node graph or Streamer.bot's three-pane layout:** harder than a list for "when X, do A then B", and discovery through right-click menus is what makes Streamer.bot hard for beginners.
- **Conditions on triggers for "who":** a role field and any-of lists are simpler, and need no expressions at match time.

## Consequences

- New events and actions need palette wording in the same slice, or the tests fail.
- The editor never changes what it can't show: unknown matches, conditions and steps are kept as they are.
- Saved conditions in the editor's forms compare text with `string(…)` and numbers with `double(…)`. "Contains" ignores case.
