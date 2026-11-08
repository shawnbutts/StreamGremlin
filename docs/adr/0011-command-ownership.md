# ADR-0011: One answer per chat command

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0068 (one answer per chat command), as amended by its D-111. StreamGremlin has no plugins, games or giveaways yet, so only the workflow part applies now.

## Context

Nothing stopped two workflows that are on from answering the same `!command`. Both would reply, and later both would take a bet or spend points. Streamer.bot allows the same overlap, and streamers coming from it hit it when they import or copy commands. A streamer can't see from chat which one answered, and the harm (a double reply now, a double charge later) happens before anyone notices.

## Decision

**What answers a command:** a workflow that's on answers the commands its `chat.command.received` triggers name in `where.command` (one, or a list).
- A trigger type pattern that reaches commands (`chat.**`) counts too.
- A trigger that names no command sees every command and answers none, so a workflow that logs all commands claims nothing.
- Each claim carries its limits: the chats (`sources`, none meaning every chat) and the chatters (`where["chatter.role"]`, none meaning everyone).

**Two claims conflict** when they name the same command and both their chats and their chatters can meet. So these can be two workflows:
- `!so` for mods and you, and `!so` for viewers;
- a Twitch-only `!raid` and a YouTube-only one.

Any other `where` (say, on the command's arguments) counts as overlapping, which errs on the safe side.

**When it's checked**, under one lock so two changes can't both take a command:
- **Turning a workflow on:** each of its claims against every other workflow that's on.
- **Saving a workflow that's on:** only the overlaps the change creates, whether a new command or a command opened to more chatters or chats. An existing overlap doesn't block an unrelated edit.
- **Not checked:** saving or restoring a workflow that's off. Restored workflows come back off, so they're checked when turned on.

**Refusal:**
- 409 `command_taken`, with `conflicts` (command, owner and name) and a message naming each one: "!hello is already answered by “Say hello back”. Turn that off first, or limit who can use each so they never overlap: one answer per command."
- The CLI prints the message and exits 2.

**Existing overlaps** (from before this check, or a database edited by hand) stay until one is turned off or changed to add the command. They're shown as conflicts.

**What answers what:** `GET /v1/commands` lists each command with its workflows (on or off, with their chats and chatters), and whether two that are on overlap. `streamgremlin commands` prints the same.

**On screen:**
- **Workflows → Chat commands:** each command, what answers it ("for mods and you", "(off)"), and conflicts flagged with "Turn one off".
- **Turning on:** a refused turn-on shows the reason, and the switch shows what the engine says, not the click.
- **The editor:** warns as soon as a command is typed that another workflow that's on already answers for chatters who meet. Saving is still allowed.
- **"Who can use it" gains "Everyone except mods and you"** (viewer, subscriber, vip). Every other option includes the streamer, so without it two workflows could never split a command in the editor. It pairs with "Mods and you".

**Where it lives:** `internal/commands` holds the rules as plain functions over claims: overlap, conflicts and the table. `internal/workflow` turns definitions into claims and checks them when turning on and saving.

**Later:** plugins, games and giveaways will hold commands too (StreamWisp ADR-0068, D-111). They'll add claims to the same check when they arrive.

## Alternatives considered

- **A warning instead of a refusal:** a double reply now, and a double bet later, is the harm; a warning doesn't prevent it.
- **Checking only when saving:** a workflow is saved off, and the conflict happens when it's on.
- **Comparing every `where` for overlap:** conditions can be any payload path. Chats and roles cover the common split, and anything else counts as overlapping.
- **Picking one answer at run time (first, or newest):** surprising, and the streamer couldn't tell which ran.

## Consequences

- A workflow whose command overlaps another can be built and saved off, and the editor says why it can't be turned on yet.
- Templates (slice 11) and imports must leave a workflow off, or be checked, when its command is taken.
