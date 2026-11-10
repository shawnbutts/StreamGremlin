# ADR-0033: Earning rules

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0018 (points rules). Changed:
  - Twitch already sends the events (follows, subs, resubs, gifts, cheers, online and offline), so nothing new is subscribed and no new scope is asked for;
  - time in chat (its "watch time") and channel point redemptions aren't in this slice. Time in chat is F4 slice 3;
  - the rules are a settings document, as other settings are, rather than a new table;
  - "only while live" is a choice for chat, read from the stream events in order.

## Context

F4 slice 2 (D-053). Viewers earn points by taking part. AGENTS.md:
- duplicate delivery never pays twice;
- ledgers are append-only;
- unknown state is never presented as confirmed.

Workflows could give points, but a per-viewer wait between chat awards and a once-ever follow bonus have to be exact and survive restarts, and an edited workflow could pay twice.

## Decision

- **`internal/earning` follows the inbox with its own cursor** (migration 0021, `earning_cursor`).
  - The first time it starts, the cursor is set to the newest event. Nothing that happened before pays, even once earning is turned on.
  - While earning is off, the cursor still moves. Turning it on never pays for what happened while it was off.
- **Only real events from a platform count:**
  - never test events;
  - only `confirmed` delivery;
  - never core events;
  - never the streamer's own (the actor is the channel, or chat says the broadcaster). The bot's messages are never stored, so they never count.
- **The rules,** with an amount of 0 turning one off:

  | Rule | Operation id | Amount |
  |---|---|---|
  | Chatting | `earn:chat:<event>` | each message, at most once per wait per viewer (`earning_chat` keeps the last award, so the wait survives a restart); optionally only while live |
  | A first follow | `earn:follow:<viewer>` | once per viewer, ever |
  | A sub or resub | `earn:sub:<event>` | by tier; a gifted sub earns nothing for its recipient |
  | Gifting subs | `earn:gift:<event>` | per sub, to the gifter; anonymous gifts have no one to pay |
  | Cheering | `earn:bits:<event>` | per whole 100 bits |

  Defaults (off until turned on): chat 1 at most every 60 seconds, only while live; follow 50; subs 500, 1000 and 2500; each gifted sub 250; each 100 bits 10.
- **Awards are ledger credits** (ADR-0032), by "earning rules", with a reason ("chatting", "gifting 3 subs"…). Reading the same events again pays nothing more. A rule changed later doesn't pay again for what was already paid: an operation id already used at another amount keeps the original.
- **Live** is the newest real stream event (OBS starting or stopping the stream, Twitch saying the channel went online or offline), in inbox order. When nothing has said, it's unknown, and chat that earns only while live earns nothing. On a restart it's read again from the events up to the cursor.
- **The rules** are the settings document `earning`:
  - `GET` and `PUT /v1/points/earning` (`points.read` and `points.write`);
  - `streamgremlin points earning [on|off|set NAME=N …]`;
  - the "How viewers earn" section on the Points screen.

## Alternatives considered

- **Rules as workflows:** see Context.
- **Paying for past events when turned on:** surprising, and it could pay thousands at once.
- **Reading live state from Twitch at each message:** a network call per message, and unknown while disconnected anyway. The events in order give the right answer for each message, even when catching up.

## Consequences

- Subs, gifts and bits need an Affiliate or Partner channel to be tested for real. They're checked against mocktwitch until then.
- A viewer earns before they have a viewer record from the recorder: earning makes the record (ADR-0032's `Ensure`), and the recorder fills it in.
- Time in chat, for viewers who are in chat but quiet, needs Twitch's chatter list and a new scope (F4 slice 3).
