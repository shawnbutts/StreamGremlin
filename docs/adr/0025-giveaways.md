# ADR-0025: Giveaways

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** StreamWisp ADR-0045 (giveaways), with D-069 there. Changed: no entry cost, points prizes or refunds yet (the points ledger is F4); one entry per chat account, since there are no linked accounts before YouTube; one unfinished giveaway at a time; entrants hear they're in, a few names to a message; a practice giveaway instead of simulated sessions. Entry rules (ADR-0075 there), more chances (ADR-0077), drawing from recent chat (ADR-0078) and the overlay (ADR-0076) are later M3 slices.

## Context

F3 is giveaways (D-004, D-006). AGENTS.md: winners chosen on the server with a cryptographically sound random source and recorded for audit; odds never implied fair unless verified; chat alone carries everything a viewer needs; duplicates never count twice.

Streamer.bot has no built-in giveaway; its users add community scripts. Chat bots such as StreamElements use `!join`, so that's the default.

## Decision

### Rules

- **One entry per chat account** (platform and user id). A second `!join`, or a redelivered message, enters no one twice.
- **The broadcaster can't enter** their own giveaway.
- **Subscribers only** is optional, from the chat message's subscriber flag. Someone turned away is told once.
- **Every entry has the same chance,** and the opening message says so.
- **Entries count from when the giveaway opened** (the inbox's arrival time), and stop when entries close, by hand or after a set time (up to a day).
- **One unfinished giveaway at a time,** enforced by a unique index.
- **A practice giveaway** takes only test chat (the Events screen's test commands) and sends nothing: its messages are kept and shown as "would say". Its events are test events, so workflows never act outside for them. A live giveaway takes only real chat.

### Chat commands

- **A live giveaway holds its entry and claim commands** under "one answer per chat command" (ADR-0011). It won't open while a workflow that's on answers either; no workflow (or group) can be turned on to answer them while it runs. The command list shows the giveaway as their owner.
- **The engine reads the giveaway's claims without its lock** (an atomic snapshot), because the workflow engine asks while holding its own.

### The record (migration 0014)

- **`giveaways`:** settings, state (`open`, `closed`, `ended`, `cancelled`), who opened it, the times, the entry count and the fingerprint.
- **`giveaway_entries`:** sequence, platform, user id, names, the event that entered them, the time.
- **`giveaway_refusals`:** once per person and giveaway, with the reason.
- **`giveaway_draws`:** number, the entry drawn, `pick_index` among `candidates`, the outcome (`won`, `pending_claim`, `claimed`, `forfeited`, `set_aside`), the claim deadline, who settled it.
- **`giveaway_messages`:** the chat outbox.
- **Triggers:** nothing is deleted; entries are only added while open and never changed; the fingerprint and count are fixed once set; a finished giveaway never reopens; a draw never changes except its outcome, which only moves forward (`pending_claim` to `claimed`, `forfeited` or `set_aside`; `won` or `claimed` to `set_aside`).

### Drawing

- **Closing entries** fixes the list and records its SHA-256 over one line per entry, in order: `<seq>\t<platform>:<user id>\n`.
- **Each draw** picks uniformly with `crypto/rand` from the entries not yet drawn, in order, and records the pick's place and the count. Anyone with the list can recompute the fingerprint and check each winner's place: `streamgremlin giveaways verify`, and **Check the draws** in the app, which checks it itself rather than taking the engine's word. The randomness can't be replayed: `crypto/rand` has no public seed.
- **Draw** closes entries first if they're open, and draws only the winners still wanted, so repeating it draws no one extra. No one is drawn twice.
- **Claims:** with a claim time (15 to 600 seconds), a winner types `!claim` (or the claim command chosen) in time, judged by when the message arrived. Otherwise the win is forfeited, announced, and the streamer can draw again. Without a claim time, a draw wins at once.
- **Set aside:** the streamer sets a winner aside (recorded, with who) and a replacement is drawn.
- **End** congratulates the winners; it waits while a winner can still claim. **Cancel** says there's no winner. Both keep the record.

### Chat

- **Messages:** the opening (how to enter, one entry each, the same chance, how many winners, when entries close, how to claim); "you're in" for new entrants, every 5 seconds, a few names to a message under Twitch's 500 characters; "subscribers only" for those turned away, once; the close with the count; each winner; claims and forfeits; the end or cancellation.
- **An outbox:** each message is claimed (`sending`) before it's sent and never resent. After a stop, one left `sending` becomes `unknown`. Not signed in, refused or dropped by Twitch: `failed`, with why. Twitch's rate limit: tried again, since nothing was sent. A message not out within 2 minutes isn't sent late. The bot sends when one is signed in; its echo is never treated as chat.

### Events (source `core`)

`giveaways.giveaway.opened`, `.closed` (entries, fingerprint), `.ended` (status, winners), `giveaways.winner.drawn` (draw, pick_index, candidates, winner), `.claimed`, `.forfeited`, the winner as actor. Triggers for each (closed, claimed, forfeited and ended under More).

### Steps (External)

"Open a giveaway", "Draw giveaway winners", and under More, "Close giveaway entries" and "End the giveaway". Opening again from the same run step finds its own giveaway.

### Screen, API, CLI

- **API**, permissions `giveaways.read` and `giveaways.write`: `GET /v1/giveaways` (the running one in full, and recent ones), `GET /v1/giveaways/{id}`, `POST /v1/giveaways`, and `POST /v1/giveaways/{id}/close`, `/draw {count}`, `/set-aside {draw_id}`, `/end`, `/cancel`.
- **The Giveaways screen** (in the main list; it can be moved under More…):
  - a form to open one, with practice;
  - the running one: entries, Close entries, Draw, End, Cancel (asks first), each winner with how they were drawn and the claim countdown, Set aside and draw again (asks first), the fingerprint and Check the draws, and every chat message with whether it was sent;
  - earlier giveaways with their winners and the same check.
- **CLI:** `streamgremlin giveaways`, `open [flags] TITLE`, `close`, `draw [-n N]`, `set-aside DRAW`, `end`, `cancel`, `show ID [-entries]`, `verify ID`.

## Alternatives considered

- **Several giveaways at once (StreamWisp):** rarely wanted, and it makes "which one did !join enter?" a question. One at a time.
- **No "you're in" (StreamWisp):** a viewer couldn't tell from chat whether they entered. Batched, it stays quiet enough.
- **A public seed (commit-reveal):** checkable randomness, at the cost of explaining it. Possible later.
- **Drawing again automatically after a forfeit:** the streamer may prefer fewer winners.

## Consequences

- **Entry cost, points prizes and refunds** come with the points ledger (F4).
- **The entry list stores chat account ids,** which the record needs for checking. They stay with the giveaway.
- **A giveaway left open over a restart** carries on; commands from the last two minutes are read again, entering no one twice.
