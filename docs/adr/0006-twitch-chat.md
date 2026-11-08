# ADR-0006: Twitch chat

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0012 (Twitch chat) and ADR-0057 (bot account). Twitch's docs were re-read on 2026-11-08; the details are in the adapter's manifest (`internal/twitch/manifest.go`).

## Context

Chat is the first real stream of events, and the place StreamGremlin answers viewers. A desktop app has no public HTTPS address, so Twitch's webhooks are out. Chat is also the riskiest output: a message posted twice, or posted too fast, is visible to every viewer, and over Twitch's rate limit the account is ignored for an hour.

## Decision

**Reading** (EventSub over WebSocket, your account's token)
- **One session:** while your account is signed in with `user:read:chat`, the engine opens one session at `wss://eventsub.wss.twitch.tv/ws`, asking for a 30-second keepalive. Within 10 seconds of the welcome it subscribes to `channel.chat.message` v1 for your channel (`broadcaster_user_id` = `user_id` = you). A 409 "already exists" counts as subscribed.
- **Losing the connection:** a close, or no message within the keepalive plus 20% plus a second, means the connection is lost. The engine reconnects with backoff (1 second doubling to a minute, with jitter) and subscribes again.
  - The state says "Twitch doesn't resend messages missed while disconnected", because it doesn't.
  - Twitch's close codes 4000 to 4007 are shown in words.
- **`session_reconnect`:** the engine dials Twitch's URL as given and keeps reading the old connection until the new one's welcome, then closes the old one. It doesn't resubscribe, because subscriptions carry over.
- **Revocation and 401s:** revocation ends the session and checks your sign-in, which ends it if Twitch revoked it. A 401 while subscribing does the same check (one refresh, then end).
- **Account changes:** signing out, or signing in as another account, ends the session at once.

**What becomes an event** (source `twitch`, `confirmed`)
- **`chat.message.received`**, with `source_event_id` = Twitch's `message_id`, so the inbox drops repeats. Twitch doesn't say whether a message can arrive twice; the reconnect handover can overlap.
  - Payload: `{message_id, text, chatter: {id, login, display_name, badges, role, is_broadcaster, is_moderator, is_vip, is_subscriber}, message_type, color, bits?, reply_parent_message_id?, channel_points_custom_reward_id?, source_broadcaster_id?}`.
  - `role` is the highest of broadcaster, moderator, vip, subscriber and viewer.
  - Test events use the same shape.
- **`chat.command.received`** for `!name args`. The name is 1 to 32 of `a-z 0-9 _`, matched without regard to case (`internal/chat.ParseCommand`, shared with later platforms).
  - Payload: the message's, plus `command`, `args` and `arg_list`.
  - `source_event_id` is `cmd:<message_id>`, and the causation is the message event.
  - Messages from another channel in a shared chat never make commands.
- **The bot's own messages aren't viewer chat:** they're read for the bot's moderator or VIP badge, then dropped. StreamGremlin never answers itself, pays it points or enters it in giveaways. Your own messages are real chat.

**Sending** (Helix Send Chat Message)
- **Who sends:** `POST /v1/chat/messages {text, as?, reply_to?}`. `as` is `bot` by default when a bot is signed in, otherwise you; `streamer` always sends as you. The answer says who sent it, with a note when it fell back.
- **Limits:** 1 to 500 characters, counted as characters rather than bytes. Local limits stay under Twitch's:
  - you, or a bot that moderates your channel: 90 per 30 seconds;
  - an ordinary bot: 18 per 30 seconds, and one per second. A send waits up to three seconds for its slot, then fails.
- **Never retried.** Twitch has no idempotency key for chat, so a retry after an unclear answer could post twice. Failures are reported plainly:
  - `not_sent` (422) when Twitch answers `is_sent: false`, with its drop reason, such as AutoMod;
  - `rate_limited` (429);
  - `unavailable` (502) for network trouble, where the message may or may not have been posted.
- **A refused bot:** if Twitch refuses the bot's token, nothing was posted, so the message goes out as you, the answer says so, and the bot's sign-in is checked. This is the only fallback.
- **Moderator check:** the engine asks Get Moderated Channels whether the bot is a moderator when chat connects, when the bot signs in, and hourly. The badges on the bot's own messages also say so. The Connections screen shows `/mod <bot>` to copy when it isn't one.
- **`for_source_only` is never sent.** It needs an app token, and its default changed in 2025.

**Screen:** Connections → Twitch → Chat shows whether chat is read, why not when it isn't, and a box to send as the bot or as you. Messages show live on Events. CLI: `streamgremlin chat send`, and `events watch -types chat.*` to read.

**Testing:** `mocktwitch` serves the three Helix endpoints and the EventSub WebSocket. Tests can type chat as anyone, make a moderator, hold the next message, force a reconnect, revoke, drop every connection, or stop keepalives. Its `/chat` page lets a person type chat in standalone use.

## Alternatives considered

- **IRC:** Twitch points bots at EventSub and the API now, and IRC lacks structured badges and replies.
- **Conduits or webhooks:** need a public HTTPS endpoint and an app token.
- **Queueing sends while Twitch is unreachable:** a message minutes late is worse than a clear "not sent".
- **Falling back to you when the bot is rate limited:** chat would show a mix of senders. The fix is making the bot a moderator, which the screen says.

## Consequences

- Chat sent while StreamGremlin is closed or disconnected is never seen. Workflows can't assume they saw every message.
- One of the three WebSocket connections Twitch allows per token is used. Later Twitch events (follows, subs, raids) join the same session.
- The inbox holds chat text. Retention is decided in slice 11.

## Open questions

- An opt-in test against the real Twitch, beyond the live check.
