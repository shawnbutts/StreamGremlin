# ADR-0021: First Words

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** StreamWisp ADR-0059 (First Words) and D-085 there. Changed: StreamGremlin has no viewers table, so it judges from the stored chat. Going live is placed by order in the inbox, not by time. A reset by hand is built.

## Context

Streamers coming from Streamer.bot use its **First Words** trigger to welcome viewers: it fires "when someone sends their first message of the stream" and resets after 12 hours (StreamWisp read the docs on 2026-10-28; Streamer.bot's own page is still the reference). FEATURES lists it for F2. It needs no Affiliate, so the user's channel can check it fully.

Twitch's own "first message" flag marks a viewer's first message ever in the channel, which isn't what streamers mean here. It isn't used.

The user agreed to (2026-11-09): "stream start or 12 quiet hours", as Streamer.bot does, with no setting yet; and that the streamer's and the bot's own messages never count. Their words: "proceed".

## Decision

### The event

- **`chat.first_words.received`** is a copy of the chat message's event:
  - the same source, actor, channel, payload and delivery quality;
  - `causation_id` is the message;
  - `source_event_id` is `first:` plus the message's, so a redelivered message never makes two.
- **The message still arrives** as `chat.message.received`, and as a command when it is one.
- **It's derived from stored events** (`internal/firstwords`), so it works for every platform and for test messages.

### When a message is someone's first

A message is a viewer's first of the stream when they have no earlier message since the stream began. The stream began at the latest of:
- **going live:** `broadcast.stream.started` (OBS) or `broadcast.channel.online` (Twitch). This is placed by order in the inbox, not by time, so a message stored just before going live, even in the same millisecond, counts as before it;
- **a reset by hand** ("Start over", the step "Start First Words over", or `POST /v1/first-words/reset`). It's saved in settings, so a restart keeps it;
- **12 hours before the message:** twelve quiet hours count as a new stream.

Who never counts:
- **The streamer's account** (role broadcaster). That includes what StreamGremlin sends as the streamer.
- **The bot's messages,** which are never stored.

### Test chat

- **Test chat is judged apart** from real chat, by its own viewers.
- **A real stream start** starts both over; **a test start** only test chat.
- So a test chat message from the Events screen makes test first words, and its workflows run as tests.

### Restarts

- **Nothing new is stored.** On start the service reads the last 12 hours of stored chat again, to know who has spoken.
- **Where to start reading** is found with the real clock, because arrival times are stamped with it.
- **Messages that arrived in the 5 minutes before a start** get first words then, in case the engine stopped just after storing them. A redelivery can't make two (`first:` id). Older messages don't: they were judged before, or are too late to greet.
- **Memory is kept small:** at most about 20,000 viewers, dropping the ones silent for 12 hours.

### In the app

- **The trigger** "A viewer chats for the first time this stream" (Chat), with the same values as a chat message.
- **On the Events screen:**
  - the event in words ("Fan's first words this stream: hi");
  - a test event;
  - a **First words** card saying since when it counts and how many viewers have spoken, with **Start over** (after a confirmation).
- **The step "Start First Words over"** (under More steps). A test run only says what it would do.
- **API:**
  - `GET /v1/first-words` (`events.read`);
  - `POST /v1/first-words/reset` (new permission `first_words.reset`).
- **CLI:** `streamgremlin first-words` and `first-words reset`.
- **Templates,** in a new **Welcome** category and group:
  - "Welcome first-time chatters": a chat welcome; a starter, with no blanks;
  - "Welcome first-time chatters on screen": the alert overlay with their name and profile picture.

## Alternatives considered

- **Twitch's first-message flag:** it's first ever, Twitch only.
- **A viewers table with each viewer's last message:** a migration for what the stored chat already says.
- **Asking the database for each message:** it scans the stream's chat on every message.
- **Going live by time:** messages and the stream start can share a millisecond (found in the acceptance test), so order decides.
- **A setting for the 12 hours:** left until someone asks (Streamer.bot's default).

## Consequences

- **Platforms are separate:** a viewer on Twitch and on YouTube (later) has first words on each, because viewers are per platform account.
- **OBS counts:** going live in OBS starts first words over even when Twitch hasn't said it's live yet.
- **Messages deleted by retention** are gone from what's read again. Retention keeps far more than 12 hours, so it doesn't matter.
