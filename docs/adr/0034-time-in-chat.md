# ADR-0034: Time in chat

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0018's "watch time", renamed "time in chat" (the feature cut: not "watch time"). Changed:
  - it has its own switch, off by default, so a streamer who already turned earning on isn't paid out of the blue after updating;
  - who chatted recently is kept per account as chat is read, rather than counted from the inbox at each tick;
  - a tick that fires more than a minute late (the computer slept) isn't paid.

## Context

F4 slice 3 (D-055). Viewers who stay in chat, not only those who type, should earn. AGENTS.md: never invent passive viewer presence; unknown is shown as unknown.

Twitch's Get Chatters (`GET /helix/chat/chatters`, `moderator:read:chatters`, read 2026-11-10) lists the users connected to the broadcaster's chat:
- in pages of up to 1,000;
- with a delay: "There is a delay between when users join and leave a chat and when the list is updated accordingly";
- it doesn't say whether viewers who aren't logged in are listed.

It's the only documented source of who's in chat. It says who's connected to chat, not who's watching.

## Decision

- **At the end of each interval while live** (default every 5 minutes, aligned to the clock):
  - viewers who chatted in the last N minutes (default 10) earn the chatting amount (10);
  - others on Twitch's chatter list earn the quiet amount (3);
  - a viewer earns once per interval, the chatting amount if both.
- **"Chatted"** is real chat only: never test chat, the streamer's, or the bot's (which is never stored). It's kept in `earning_active` (migration 0022) as chat is read, even while earning is off, so turning it on mid-stream knows who has been talking.
- **The chatter list** leaves out the streamer and the bot. Each listed viewer gets a viewer record (`Ensure`).
- **Nothing is invented:**
  - when you're offline, or it's unknown whether you're live, nothing is paid and the tick says why;
  - when the list can't be read (no permission yet, Twitch refusing or down), chatters are still paid and the tick says why the quiet ones weren't;
  - an interval that ended while StreamGremlin was closed, or more than a minute late, is never paid.
- **Never twice:** each payment's operation id is `earn:time:<interval end>:<viewer>`. A repeated tick is answered from `earning_ticks`, which keeps each tick (when, how many, why not) for 30 days.
- **The rules** are part of the `earning` settings document (`time`: on, every_minutes 1–60, active, active_minutes 1–120, quiet). `GET /v1/points/earning` adds `last_tick`; `streamgremlin points earning set time=yes time-every=… time-active=… time-window=… time-quiet=…`; "Time in chat" on the Points screen.
- **The sign-in asks for `moderator:read:chatters`.** Until the streamer signs in again, Connections shows the missing permission, chat keeps working, and only chatters are paid.
- **For tests only,** `engine -test-minute` shortens time in chat's minute, and only with a stand-in Twitch (`-twitch-auth-url`).

## Alternatives considered

- **Paying the list without "live":** the list includes people idling in chat while the streamer is offline.
- **Counting viewers from Get Streams' viewer count:** a number, not people. Nobody can be paid from it.
- **Backfilling missed intervals:** who was there can't be known afterwards.

## Consequences

- A big chat means many ledger credits per interval: 1,000 chatters is 1,000 small transactions every 5 minutes. That's well within SQLite's reach; the list is capped at 50,000.
- Bots other than StreamGremlin's (Nightbot, say) are in the chatter list and earn. Leaving names out could come later.
