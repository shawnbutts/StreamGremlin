# ADR-0041: Moderating from chat, and its record

- **Status:** Accepted
- **Date:** 2026-11-11
- **Based on:** D-075 (M7, Live: "moderating is Twitch's… records each one"); D-079 (slice 3); [ADR-0024](0024-ads.md) (an outside request with no idempotency key: sent once, an unclear answer never retried). New in StreamGremlin: StreamWisp didn't moderate.

## Context

Slice 3 lets the streamer moderate from the Live screen and the CLI: delete a message, time out, ban, unban, warn, shoutout, add and remove VIPs and moderators, change how Twitch treats a suspicious user, and change the chat modes. Each is one of Twitch's documented Helix requests. None takes an idempotency key, and a ban or a timeout sent twice isn't harmless: a second timeout restarts the clock, and a second ban is refused in confusing words. Moderating is also something a streamer may need to answer for later, so each request should be kept with Twitch's answer.

## Decision

### Sent once, as the streamer

- **Who:** every request uses the streamer's token, with the streamer as `moderator_id`. The bot doesn't moderate in this slice: its token would need the same scopes, and two identities would make the record harder to read. Streamer.bot users are used to their broadcaster account doing this.
- **Scopes added to the streamer's sign-in:** `moderator:manage:banned_users`, `moderator:manage:chat_messages`, `moderator:manage:warnings`, `moderator:manage:shoutouts`, `channel:manage:vips`, `channel:manage:moderators`, `moderator:manage:chat_settings`, `moderator:read:suspicious_users` and `moderator:manage:suspicious_users`. Until the streamer signs in again, each action says to.
- **Checked first:** a timeout is 1 second to 2 weeks; a reason is up to 500 characters, required for a warning, and only for a ban, timeout or warning; a delete needs a message id; a follow time is 0 to 90 days; slow mode is 3 to 120 seconds. A bad request is refused before anything is recorded or sent.

### The record

Migration `0028_moderation.sql`: a `moderation` table, one row per request, never deleted. A row holds:

- the request id (unique);
- the action;
- the platform, and the viewer's id and login;
- the message id, the length and the reason;
- for chat modes, the change asked for, as JSON;
- where it came from (`live` or `cli`);
- the state and Twitch's answer in words;
- when it was asked and when it was answered.

- **Exactly once:** the row is inserted as `pending` before Twitch is asked, under its request id. The same request id again returns the row and sends nothing, however late it comes. The same id with a different request is refused (409). Two copies at once: one inserts and sends, the other gets the row.
- **States:** `done`; `refused` (Twitch answered 4xx, or the sign-in or permission is missing: nothing happened, with why); `unknown` (no answer, a 5xx or a broken connection: it may or may not have happened). An unknown request is never retried. A row a crash left `pending` becomes `unknown` at the next start.
- **The answer is recorded even if the caller goes away:** the request to Twitch isn't cancelled with the API call.
- **Logged:** each request's action, the viewer's id and login, where it came from and the state. Tokens never appear.

### Suspicious users are events

`channel.suspicious_user.message` and `channel.suspicious_user.update` (v1, condition `broadcaster_user_id` and `moderator_user_id`, both the streamer) arrive on the existing EventSub session and are stored in the inbox:

- `moderation.suspicious_user.message`, keyed by the chat message's id, with the message id, text, treatment (`low_trust_status`), `types`, `ban_evasion` and `shared_ban_channel_ids`;
- `moderation.suspicious_user.updated`, with the treatment and the moderator.

The viewer is the actor. Twitch has no request to read a viewer's flags, so the card shows the newest flag StreamGremlin heard, and says so. Before any flag, it doesn't say "not suspicious".

### API, CLI and screen

- `POST /v1/moderation` (`moderation.write`): `{request_id, action, platform, user_id?, login?, message_id?, seconds?, reason?, chat_modes?, from}`. It answers `{record, duplicate}` with 200 whenever the request was handled, including refusals and unknowns. 400 for bad input, 409 for a reused request id.
- `GET /v1/moderation?platform=&user_id=&limit=` (`moderation.read`): the record, newest first.
- `GET /v1/chat/settings` (`moderation.read`): the chat modes, or why they're unknown.
- `GET /v1/chatters/{platform}/{user_id}` adds the viewer's newest 10 records and their suspicious flag.
- `streamgremlin mod …`: timeout, ban (only with `-yes`), unban, warn, delete, shoutout, vip, unvip, mod, unmod, monitor, restrict, unsuspect, modes, history.
- **Live:**
  - resting the pointer on a message shows Delete, a 10-minute timeout and Ban (asked first);
  - the viewer card has every action, with a reason and a length, and the viewer's record;
  - **Chat modes** turns modes on and off;
  - **Done from StreamGremlin** lists the record;
  - suspicious users' messages are marked, merged with the chat message by its id in either order, or shown on their own when chat didn't carry them.

## Alternatives considered

- **Retrying an unclear answer:** a timeout sent twice restarts its clock. Rejected, as for ads.
- **Recording moderation as inbox events:** the inbox is for what happened on the platform, and workflows react to it. A request and its answer is the streamer's own action, changes state after it's stored, and needs a unique request id. A table fits it. Twitch's own `channel.moderate` events could be added later, for actions taken elsewhere.
- **Moderating as the bot:** decided against for now (above). It can come later behind a choice, with the bot's own scopes.
- **Third-party bot lists** (CommanderRoot, TwitchInsights) for spotting bots: no documented API, they send viewers' names to someone else, and they list wanted bots too. Twitch's own suspicious-user flags are used instead (D-079).

## Consequences

- The streamer signs in to Twitch again once for slice 3's scopes.
- Every moderating request from StreamGremlin can be looked up later with Twitch's answer, from the app or `streamgremlin mod history`.
- An action Twitch never answered stays unknown. The streamer checks chat, and can send it again as a new request.
- Moderating done on Twitch's site or by mods isn't in this record; their effect shows on the viewer card, which asks Twitch fresh.
