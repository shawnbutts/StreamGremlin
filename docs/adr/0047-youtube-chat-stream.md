# ADR-0047: Reading YouTube chat

- **Status:** Accepted
- **Date:** 2026-11-14 (sending added the same day, D-098)
- **Based on:** D-095 (M6, one combined chat); D-097 (slice 2); ADR-0046 (YouTube sign-in and quota); ADR-0004 (the event inbox).

## Context

YouTube chat belongs to a live broadcast: each has its own `liveChatId`. Google gives no push for "you went live", so StreamGremlin has to ask. For reading chat, Google recommends `liveChatMessages.streamList`, a gRPC server stream that sends messages as they're posted, over polling `liveChatMessages.list`. Both cost 5 units a call.

## Decision

### Finding the broadcast

- `liveBroadcasts.list?part=snippet,status&broadcastStatus=active&broadcastType=all` (1 unit), as your channel. Google allows only one of `mine`, `id` and `broadcastStatus`. `broadcastType=all` includes the default (persistent) broadcast.
- A broadcast counts when `status.lifeCycleStatus` is `live` or `liveStarting`, it has a `snippet.liveChatId` and no `snippet.actualEndTime`. `testing` (a preview nobody sees) doesn't count.
- When to ask:
  - every 2 minutes while not live (at most 720 units a day);
  - at once when your channel signs in, when Twitch says your stream started (a real `broadcast.stream.started` or `broadcast.channel.online` from Twitch, not a test), and on **Find my stream**;
  - never while chat is being read.
- A chat that ended or is turned off is skipped until StreamGremlin restarts, so it isn't opened again and again.

### The stream

- gRPC to `youtube.googleapis.com:443` over TLS, method `/youtube.api.v3.V3DataLiveChatMessageService/StreamList`. The access token goes in the `authorization` metadata as `Bearer …`. A keepalive ping every 2 minutes finds connections that died without saying so.
- **No generated code.** `internal/youtube/livechat` reads and writes only the fields StreamGremlin uses, by the field numbers in Google's published `stream_list.proto` (checked 2026-11-14). Unknown fields are skipped. A test builds messages field by field from those numbers, independently of the encoder. gRPC is given these bytes by a codec named `proto`, so the content type is the normal `application/grpc+proto`.
- The request asks for `id`, `snippet` and `authorDetails`, with 88-pixel pictures (Google's default).
- **Each connection counts 5 units** before it opens. Google's table doesn't say whether a long connection costs more.
- **Resuming:** every response carries `nextPageToken`. A new connection passes the last one, so nothing is missed or repeated after a drop. It's saved in settings `youtube.chat` (`{live_chat_id, page_token}`) at most every 5 seconds and when a connection ends, so a restart resumes too. A connection without a page token first gets recent history, and those messages are marked `backlog`. A refused page token (`INVALID_ARGUMENT`) starts again without one. The inbox stores each message once, by its YouTube ID.
- **When it ends**, by gRPC status:

  | Status | What StreamGremlin does |
  |---|---|
  | `OK`, `CANCELED`, `UNAVAILABLE` and others | Reconnect after 1 second. If connections keep ending within 30 seconds, the wait doubles each time, up to a minute. |
  | `FAILED_PRECONDITION` | The chat ended or is turned off (Google can't say which). Look for the next broadcast. |
  | `NOT_FOUND` | Look for the next broadcast. |
  | `UNAUTHENTICATED` | Check the sign-in, then retry. |
  | `RESOURCE_EXHAUSTED` | Too fast: wait at least 10 seconds. If the message mentions quota, the day's quota is marked as spent. |

  A `chatEndedEvent` or an `offlineAt` in a response also ends the broadcast's chat.
- **Your channel signing out or changing** closes the stream at once.

### Events

- A text message becomes `chat.message.received` with source `youtube`.
  - Actor: platform `youtube`, the channel ID and display name. YouTube has no logins.
  - The payload's `chatter` has the same shape as Twitch's. The role is `broadcaster`, `moderator`, `member` (a channel member) or `viewer`. It also has `is_member`, `is_verified`, `channel_url` and `profile_image_url`.
  - The payload also carries `live_chat_id`, `broadcast_id`, and `backlog` when it's true.
- Messages from the bot's channel are stored as `chat.message.sent` with `sent_by: bot`, never as viewer chat (D-087).
- Other message types aren't stored yet:
  - Super Chats, Super Stickers, memberships and gifts come in slice 4;
  - deletions and bans come in slice 5.
- **Log only, at first.** In slice 2, `events.LogOnly` (true for source `youtube`) kept everything but the event log away from YouTube chat.
  - Since slice 3 (D-098), workflows, commands, first words, timers' chat count and Live's chat take it. `events.Backlog` keeps messages from before StreamGremlin was reading from starting anything.
  - Chat filters, points, giveaways, viewers and the Discord summary still skip it until their slices.
- **Commands:** from slice 3, a `!command` said while reading also becomes `chat.command.received`. Channel members get the role `subscriber`.

### State

Chat's state can be:
- off (your channel isn't signed in);
- looking (with the next check's time);
- connecting;
- connected (with the broadcast);
- disconnected (unknown: trying again);
- quota (until midnight Pacific).

`GET /v1/connections/youtube` returns it as `chat`. `POST /v1/connections/youtube/chat/find` is Find my stream.

### Sending (slice 3, D-098)

- `POST /youtube/v3/liveChat/messages?part=snippet` with `{"snippet":{"liveChatId":…,"type":"textMessageEvent","textMessageDetails":{"messageText":…}}}`. It costs 20 units, counted before the call. It sends as the bot channel when one is signed in, otherwise yours. If YouTube refuses the bot's sign-in, the message goes as you, and the bot's sign-in is checked.
- It always goes to the chat being read. It's refused, with a reason, when you're not live, not signed in, or out of quota.
- **Length:** Google's reference states no limit, and YouTube's chat box takes 200 characters. Messages up to 500 characters are split at a space into at most 3 parts, each counted. If a part fails, the result says how many were sent.
- **Not retried:** YouTube has no way to recognise a repeat.
- **The echo:** a workflow's message sent as you comes back on the stream. Before the send, it's expected by its text; once YouTube answers, by its message ID. It's then stored as `chat.message.sent` with `sent_by: workflow`, so no workflow answers it. The tracker is `internal/chat.OwnSends`, shared with Twitch. The bot's messages are always stored as sent.
- **Replies:** YouTube has none. An answer to a YouTube message starts with `@name`.

## Consequences

- One new dependency, `google.golang.org/grpc`, for the stream. No protobuf code is generated or committed.
- `mockyoutube` serves gRPC on the same loopback address as everything else (HTTP/2 without TLS), so tests and the standalone stand-in need no certificates. Its `/mock` page goes live, chats, ends a broadcast and drops streams.
- If Google changes the proto's field numbers, the reader would see empty fields rather than crash. The live check is what catches that.
- Checking every 2 minutes while not live costs up to 720 units a day of the 10,000. The quota line on Connections shows it.
