# ADR-0035: Giving to everyone, and exporting everyone

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** D-054 (the user's request). StreamWisp had no equivalent.

## Context

F4 slice 4 (D-054, D-056). The streamer wants to give points to a whole group at once, chosen by a rule, and to export every viewer's record. AGENTS.md:
- duplicate delivery never pays twice;
- ledgers are append-only, and nobody goes below zero;
- never invent viewer presence;
- exports go through the native save dialog.

Twitch's documented lists (read 2026-11-10):
- **Get Chatters** (ADR-0034): who's connected to chat, updated with a delay.
- **Get Broadcaster Subscriptions:**
  - `GET /helix/subscriptions`, needing `channel:read:subscriptions` (already asked for), in pages of up to 100;
  - "The list is empty if the broadcaster has no subscribers";
  - it doesn't say what a channel that isn't an Affiliate or Partner gets, or whether the broadcaster is listed.

## Decision

- **Three groups:**
  - **everyone in chat:** Twitch's chatter list;
  - **everyone who chatted in the last N minutes** (1–240): real chat, as time in chat keeps it;
  - **subscribers:** Twitch's subscriber list.

  The streamer and their bot are never in a group.
- **A preview** says how many, up to 10 names, and why a group is empty. It gives nothing.
- **A give works out the group once, and keeps it** (migration 0023: `points_gives` and `points_give_members`), then pays each member a ledger credit with the operation id `give:<give>:<viewer>`:
  - the same request id again finishes the same give and returns it, paying only those not yet paid, never anyone twice;
  - the same request id with different details is refused;
  - Twitch's list unreadable: nothing is given and no give is recorded (502); nobody in the group: the same (409);
  - the reason defaults to the group in words.
- **Undo applies to the whole give.** Each payment is reversed (ADR-0032), never below zero. Whoever has spent some keeps theirs and is named; undoing again later tries them again. The give is marked undone once everyone's payment is. A give undone before it was finished is never finished by a retry.
- **The step "Give points to everyone"** (External; operation `job:<key>`). Nobody in the group isn't a failure: the output says `viewers` 0, and why.
- **Exporting everyone:** `GET /v1/exports/viewers`, as `streamgremlin.viewers-export/v1`. Every viewer's record, balance and full points history, forgotten viewers unnamed. The app saves it through the native save dialog; `streamgremlin viewers export-all` prints it.
- **Routes:**
  - `POST /v1/points/gives/preview` (`points.read`);
  - `POST /v1/points/gives`, `GET /v1/points/gives`, `POST /v1/points/gives/{id}/undo` (`points.write`, the list `points.read`).
- **CLI:**
  - `streamgremlin points give-all chat|subs|recent=MINUTES N [-reason R] [-yes]`: without `-yes`, only a preview;
  - `points gives`;
  - `points undo-give ID`.

## Alternatives considered

- **A transaction kind for gives:** each payment is an ordinary credit, so balances, history, audit and undo all work as before. The give links them through their operation ids.
- **Subscribers from past sub events:** misses anyone who subscribed before StreamGremlin and counts anyone whose sub lapsed. Twitch's list is the record.
- **Giving to a smaller group when the list fails:** the streamer would think they'd given to everyone.

## Consequences

- A give to thousands is thousands of small transactions, made in the request: a big chat takes a few seconds.
- A channel that isn't an Affiliate has no subscribers; the screen says Twitch listed none.
