# ADR-0020: Workflow groups

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** D-032, the user's design. Streamer.bot groups its actions too. StreamWisp had no groups.

> **Amended 2026-11-09 (after the live check; the user: "we need a way to collapse items in a group rather than show all workflows"):** on the Workflows screen, groups fold. Each group's name is a button. Groups start folded, while "No group" stays open. Open all and Fold all sit above the list. A folded group still shows its count and switch. The groups opened are remembered in the window's browser storage, a per-viewer convenience that falls back to folded if it's cleared. A search opens every group with a match.

## Context

As streamers add workflows, they want to switch related ones together. "Alerts" go off for a quiet stream, "Thanks" while testing. The user: "They could allow enable/disable all of them at the group level. We should also have the templates create groups where it makes sense."

Two choices were made with the user (D-032):
1. A group's switch leaves each workflow's own switch alone.
2. A workflow is in at most one group, like a folder.

The one-answer-per-command rule (ADR-0011) still holds.

## Decision

### Where the group lives

- **In the workflow's definition** (`"group": "Alerts"`, optional), so each version records it, and copies and templates carry it.
  - A name is 1 to 40 letters, digits, spaces and `- _ & '`. It starts with a letter or digit and doesn't end in a space.
  - Saving a workflow into "alerts" when "Alerts" exists uses "Alerts": names match in any case, and the first spelling is kept.
- **Migration `0012_workflow_groups.sql`** adds:
  - `workflows.grp`, a copy of the current version's group, for lookups;
  - `workflow_groups (name COLLATE NOCASE, enabled)`, one switch per group.
- **A group exists while a workflow is in it.** Its switch is made (on) when its first workflow arrives. It's forgotten when the last one leaves or is deleted, and restoring a workflow brings its group back.

### Running

- **A workflow runs when it's on and its group is on** (no group counts as on).
- **One place decides:** the engine's list of running workflows (`reload`) joins the group switches. Dispatch, timers and command checks all use that list, so nothing can run past a group that's off.
- **Turning a group off** cancels its workflows' waiting runs, as turning a workflow off does. Runs already going finish.
- **Each workflow keeps its own switch.** Turning a group off and on restores exactly the ones that were on. A workflow can be switched on or off while its group is off; it runs once the group comes on.
- **Home's "on" count, the commands table and Test** follow what runs. The list says "On, but its group is off, so it doesn't run". Test is offered only for workflows that run, because test events go to running workflows only.

### One answer per command

- **Turning a group on is refused** (409 `command_taken`, naming them) when its switched-on workflows would answer a command that something already running answers, or that another of them answers.
- **Switching on a workflow in a group that's off isn't checked,** because it won't run. The check happens when the group comes on.
- **Saving a workflow that's on** is checked only if it will run: moving it into a group that's on counts as new.

### Renaming

- **Renaming** gives each workflow in the group a new version naming the new group, and keeps the switch. The new name's switch is made first, as the old one was, so nothing starts or stops while they move.
- **Another group's name is refused** (409). Workflows move between groups one at a time, in the editor. A change of case only is allowed.

### Templates

Each template names its group, made when the first one is added:
- **Alerts:** the follower alert;
- **Thanks:** the follow, sub, gift, cheer and raid thank-yous;
- **Commands:** hello, lurk, shoutout, dice, hug, deaths;
- **Quotes:** !quote, !quoteadd, !quotedel;
- **Scenes:** brb and back.

"Say hi when the stream starts" has no group: it would be a group of one.

### API, CLI and screen

- **API:**
  - `GET /v1/workflow-groups`;
  - `PUT /v1/workflow-groups/{name}/enabled {enabled}`;
  - `PUT /v1/workflow-groups/{name} {name}`, to rename;
  - workflow summaries gain `group`, `group_enabled` and `running`.
- **CLI:**
  - `streamgremlin workflows groups`;
  - `workflows group NAME on|off`;
  - `workflows group NAME rename NEW`;
  - the list shows each workflow's group, and "on, group off".
- **The Workflows screen:**
  - sections by group, then "No group";
  - each group with its count, a "Group on" switch, Rename, and "This group is off" when it is;
  - with no groups at all, the list looks as before;
  - search works across groups.
- **The editor and the gallery:** the editor has a Group field that suggests the existing groups. The gallery says which group a template goes in.

## Alternatives considered

- **A group switch that turns every workflow on or off:** turning a group back on would lose which ones were off.
- **Several groups per workflow (tags):** a workflow could be in a group that's on and one that's off. Which wins would need a rule streamers must learn.
- **Groups in their own table only, not in the definition:** copies and templates couldn't carry them.
- **Merging groups on rename:** moving every workflow into a group whose switch differs could start or stop some at once. Refusing is clearer.

## Consequences

- **Workflow versions grow** when a group is renamed: one new version per workflow in it.
- **A group's switch is forgotten** with its last workflow. A group made again starts on.
- **Not built:** ordering groups by hand (they're by name), and switching groups from workflows.
