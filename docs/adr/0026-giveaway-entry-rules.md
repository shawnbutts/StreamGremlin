# ADR-0026: Giveaway entry rules, and entering by chatting

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0075 (entry rules) with its amendment D-127 (subscriber rules). Changed:
  - no watch-time rule: StreamGremlin doesn't see silent viewers yet (Get Chatters isn't built), and "time in chat" belongs to the points slice (F4);
  - no subscription-tier rule;
  - the messages rule counts this stream, since StreamGremlin sees when the stream starts;
  - no `quiet_refusals`, since refusals are already batched in chat;
  - recent wins count by chat account, as there are no linked accounts before YouTube.

## Context

Giveaways (ADR-0025) took anyone in chat, or only subscribers. Streamers coming from chat bots expect to limit giveaways to followers, older accounts, regular chatters, or people who haven't just won. AGENTS.md says never to imply requirements are fair unless verified, and never to guess what can't be known.

**What Twitch documents** (checked 2026-11-10):
- **Get Channel Followers**, with `user_id`, needs a user token with `moderator:read:followers`, from the broadcaster or a moderator. It gives `followed_at`, and "the data field is an empty array, which means the user doesn't follow". Without the scope, data is empty too, or the answer is 401. StreamGremlin already asks for this scope for follow events.
- **Get Users** gives an account's `created_at`, with no scope.
- **The badges in a chat message:** "info" carries metadata only for subscriber badges: the months subscribed. Founder badges carry none.

## Decision

### Rules

They are set when a giveaway opens and fixed after (a trigger). Each is off at zero:

| Rule | Checked against | Twitch only |
|---|---|---|
| `followers`, `follow_days` (1 to 3650; implies followers) | Get Channel Followers | yes |
| `account_days` (1 to 3650) | Get Users | yes |
| `sub_months` (1 to 120; implies subscribers only) | the subscriber badge's info on the entering message | yes |
| `messages` (1 to 1000) | chat messages (not `!` commands) from that account this stream, before the one that entered them | no |
| `win_cooldown_days` (1 to 365) | wins (`won` or `claimed`) in another giveaway since then, by chat account; real giveaways count real wins, practice counts practice | no |

- **"This stream"** means since the newest stream start StreamGremlin saw (Twitch going live, or OBS starting to stream) in the last 48 hours. Without one, it means the last 12 hours.
- **Every entry still has the same chance.** Rules decide who may enter, never how likely anyone is to win. The form and the opening message say so.

### Checking

- **When someone enters, the cheap checks go first:**
  1. subscribers;
  2. months;
  3. recent wins;
  4. messages;
  5. Twitch, one call per rule, in turn.
- **No lock is held while Twitch answers.** The candidate is checked outside the service's lock and the database, then recorded under the lock if the same giveaway is still taking entries.
- **Nothing is cached or guessed.** When Twitch can't be asked (it's down, not signed in, or the permission is missing), the viewer is turned away with "Twitch couldn't be asked just now… type !join to try again".
- **Who's checked again:**
  - **The entry command always checks again,** so someone who follows afterwards gets in. Their refusal is then removed.
  - **With entering by chatting, chat messages aren't checked again** for someone already turned away, so a busy chat doesn't ask Twitch for every line. The exception is the messages rule, which needs only the database.
- **A practice giveaway never asks Twitch.** Test chat carries the viewer's facts: the Events screen's chat message and command forms gain "Following for (days, -1 for not following)", "Account age (days)" and "Subscribed for (months)", sent as `chatter.practice` and a subscriber badge. A test chatter without them is turned away as unchecked.

### Refusals in chat

- **The reason is recorded** per person and giveaway. Chat hears it once, batched by reason, and again only when the reason changes.
- **Each reason says what's needed:**
  - "is for followers. Follow, then type !join.";
  - "is for followers of 7 days or more.";
  - "needs 3 messages in chat this stream first. Type !join to try again.";
  - "you won a giveaway in the last 7 days…".

### Entering by chatting

- **`enter_by_chat`:** any chat message enters, as well as the command.
- **The opening says "Say anything in chat (or type !join) to enter".**
- **No message enters twice.** The command's own message is left to the command event, and the claim command never enters.

### The record (migration 0015)

- **`giveaways.rules`** (JSON) and **`giveaways.enter_by_chat`**, fixed once open.
- **`giveaway_entries.qualified`** (JSON): what the entry was checked against (the follow date, the account date, months, messages). It's fixed, and it's not part of the entry list's fingerprint, which is unchanged.

### Opening and the inbox

- **A giveaway records the inbox's newest event when it opens.** Chat stored up to then never enters, even in the same millisecond.
- **The opening time is stored to the millisecond,** as chat's arrival time is. This fixes a slice-1 race: a `!join` in the same millisecond as the opening could be dropped.

### Screen, API, CLI

- **The form gains:**
  - "Anyone who chats enters";
  - a "Who can enter" group: subscribers only, followers only, and the five numbers, each with "0: off" and where it's checked.
- **The running giveaway** says who can enter, lists those turned away with the reason in words, and shows what each entry was checked against.
- **API:** `POST /v1/giveaways` takes `rules` and `enter_by_chat`.
- **CLI:** `giveaways open` takes `-chat`, `-followers`, `-follow-days`, `-account-days`, `-sub-months`, `-messages` and `-no-win-days`. `show` says who can enter and how many were turned away.

## Alternatives considered

- **Caching follow answers:** saves calls, but someone who follows after being turned away would still be refused. Not done.
- **Holding an entry until Twitch answers, retrying in the background:** this leaves viewers unsure whether they're in. Turning them away with "try again" is simpler and honest.
- **Watch time:** needs the chatter list. Left for the points slice.
- **Weighted entries:** extra chances are slice 3, announced in chat.

## Consequences

- **Each new entrant under a follow or account rule costs one or two Twitch calls,** made in turn, so a very busy enter-by-chat giveaway enters people a little later.
- **The "Open a giveaway" workflow step doesn't take rules yet.** Rules are set from the screen or the CLI.
- **Founders' badges carry no months,** so a founder is turned away by a months rule. The wording says what's needed.
