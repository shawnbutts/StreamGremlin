# ADR-0049: Ban lists from files, applied to Twitch at its pace

- **Status:** Accepted
- **Date:** 2026-11-14
- **Based on:** D-104 (M8: "a "bulk ban" sort of function and let the user import their ban list from a file"), D-106 (slice 2).

## Context

Streamers share lists of bot and hate-raid accounts, and want them on their own Twitch ban list. StreamGremlin keeps no list of its own (D-104): the lists are the streamer's, from files. Twitch has no bulk ban: Ban User takes one account a request, and every request comes out of a per-minute bucket shared with everything else StreamGremlin does as the streamer (chat, moderating, workflows). A list can have a million names, so applying it takes hours and has to survive restarts, crashes and lost answers without banning anyone twice or banning someone the streamer unbanned on purpose.

## Decision

### The file format

- One Twitch login or numeric user id a line, or a CSV (comma, semicolon or tab). In a CSV, a header row names the column: `login`, `user_login`, `username`, `user_name`, `name`, `user`, `id` or `user_id`, preferring a login. Without a header, the first column is read.
- Blank lines and lines starting with `#` are skipped. A leading `@` and surrounding quotes are dropped, and names are lower-cased.
- A value that isn't a login (`[a-z0-9_]{1,25}`) or an id (digits) is counted as skipped, never sent anywhere.
- At most 5,000,000 lines, and 64 KiB per line. The file is read a line at a time; memory stays flat.
- Saving a list writes a `#` heading line, then one login (or id, when Twitch gave no login) a line, so a saved list imports again as the same list.

### Storage

Migration `0035_ban_lists.sql`:

- `ban_lists`: one row per list. It holds the name (unique, any case), the reason sent with each ban, the phase, whether it's paused (and why, in `problem`), whether it waits while you're live, whether viewers with points are left alone, the file being read, the generation (which import names were last seen in), and a cursor for copying or comparing.
- `ban_list_entries`: one row per name, keyed by (list, name), where a name is a login or `id:<user id>`. It holds Twitch's user id and login once looked up, a state, a detail, and the generation.

Entry states: `new` (read), `not_found`, `kept` (you, a moderator, a VIP, a viewer with points), `already` (banned, not by this list), `waiting`, `sent`, `banned` (by this list), `refused`, `by_hand` (banned by this list, then unbanned on Twitch), `removed` (banned by this list, not in the newer file), `unban`, `unban_sent`, `unbanned`, `stopped` (waiting when the list was undone).

### Phases

`reading` → `checking` → `ready` → (confirm) `banning` → `done`. Also `undoing`, `copying` (Twitch's ban list into a new list) and `comparing` (the list's bans against Twitch's). One runner goroutine works on one list at a time, oldest first, one batch or one request per step. Each step re-reads the list, so a pause or a change applies at the next request.

- **Reading** upserts names in batches of 1,000 with the new generation, so reading again after a crash is harmless. At the end:
  - names from older generations that the list didn't ban are deleted;
  - `banned` ones become `removed`;
  - unanswered requests are kept until they're settled.
- **Checking** takes 100 `new` names: Get Users (logins and ids together), then Get Banned Users, Get Moderators and Get VIPs filtered by those ids (100 each), and the ledger for points holders. You (the broadcaster) are always kept. Timeouts don't count as banned.
- **Banning** first settles `sent` and `unban_sent` names from Get Banned Users (100 at a time):
  - a ban that happened becomes `banned`;
  - one that didn't goes back to `waiting`;
  - while undoing, a ban that happened becomes `unban`, and one that didn't becomes `stopped`.

  Then it sends one unban (removed names the streamer chose to unban), or else one ban. The state is saved as `sent` before the request and as its outcome after, so a crash leaves at most one unsettled name, which is checked rather than sent again.

### Twitch's answers

- Done → `banned` or `unbanned`.
- "Already banned" → `already` (not this list's ban, so undo never lifts it).
- "Not banned" on unban → `unbanned`.
- Another 4xx → `refused` with Twitch's words; an unban refused stays `banned`.
- **401, 403, a missing sign-in or permission:** the list pauses with the problem in words, and the name goes back to waiting. Every request would fail the same way, so no name is marked refused.
- **429:** the name goes back, and the runner waits until `Ratelimit-Reset`, or 30 seconds without it.
- **No answer:** the name stays `sent`, the runner waits 5 seconds, and the name is settled from Twitch's list next.

### Pace

- Every ban-list request reads Twitch's `Ratelimit-Remaining` and `Ratelimit-Reset`. When the remaining points fall to the spare (200 points), the runner waits for the reset.
- The rest of the bucket is left for chat, moderating and workflows, and Twitch never has to say "too many" in normal use.
- The pace is measured over the last 5 minutes and gives the estimated finish.
- While you're live on Twitch, lists wait, so a raid's moderating is never slowed (on by default, per list). Reading a file, which asks Twitch nothing, goes on.

### Syncing and hand unbans

- **Importing a newer file under the same name** bumps the generation:
  - names already known keep their state and aren't asked about again, so re-importing a million names with 2,000 new ones costs the 2,000;
  - new names are checked;
  - names this list banned that are gone from the file become `removed`, and are unbanned only if the streamer ticks that when confirming.
- **Hand unbans win.** StreamGremlin never bans a name twice: `banned` names aren't sent again. **Check against Twitch** compares the list's `banned` names with Twitch's list, and any that were lifted become `by_hand`. They are banned again only if the streamer ticks that when confirming. No EventSub ban events are needed.
- **Undo** turns `banned` and `removed` into `unban`, and `waiting` into `stopped`. Bans made elsewhere (`already`) are never lifted. Forgetting a list unbans nobody.

### Not done

- **The bot sharing the work.** If the bot is a moderator, its own token has its own bucket and could roughly halve the time. It needs the bot to sign in again with `moderator:manage:banned_users` and a second runner.
- **Lists from a link.** A list that updates from a URL someone publishes needs a decision (D-104), since it means downloading from the internet.

## Consequences

- A million names takes about a day at Twitch's default pace, and the screen says so. The common case, a curated list of thousands, takes minutes.
- An unanswered request is never sent again before Twitch's own list is read, at the cost of one Get Banned Users per 100 unsettled names.
- The app saves a list through the native save dialog, up to 16 MiB of text (about a million names). The CLI streams any size.
- Whether Twitch caps the number of bans a channel holds isn't documented; the live check and use will tell.
