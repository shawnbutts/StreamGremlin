# ADR-0015: Twitch channel events

- **Status:** Accepted
- **Date:** 2026-11-08
- **Based on:** StreamWisp ADR-0018 (the Twitch channel events part; its points rules are M3) and the raid part of its ADR-0060. Adapted as noted below.

## Context

F2 starts with what happens on the channel: follows, subs, bits, raids and channel point redemptions (D-004, FEATURES "Follows, subs, bits, channel point redeems"). Streamers coming from Streamer.bot expect a trigger for each. Until now StreamGremlin read only chat.

**Twitch's docs** (checked 2026-11-08: EventSub subscription types and reference, Managing Subscriptions, Handling WebSocket Events):

| Type | Version | Condition | Needs |
|---|---|---|---|
| `channel.follow` | 2 | `broadcaster_user_id` and `moderator_user_id` | `moderator:read:followers` |
| `channel.subscribe` | 1 | `broadcaster_user_id` | `channel:read:subscriptions` |
| `channel.subscription.message` | 1 | `broadcaster_user_id` | `channel:read:subscriptions` |
| `channel.subscription.gift` | 1 | `broadcaster_user_id` | `channel:read:subscriptions` |
| `channel.cheer` | 1 | `broadcaster_user_id` | `bits:read` |
| `channel.raid` | 1 | `to_broadcaster_user_id` | nothing |
| `channel.channel_points_custom_reward_redemption.add` | 1 | `broadcaster_user_id` | `channel:read:redemptions` or `channel:manage:redemptions` |
| `stream.online`, `stream.offline` | 1 | `broadcaster_user_id` | nothing |
| `channel.update` | 2 | `broadcaster_user_id` | nothing |

- **Notes from the docs:**
  - `channel.subscribe` doesn't include resubs.
  - Anonymous gifts and cheers have null user fields.
  - A WebSocket session may hold 300 enabled subscriptions, at a total cost of 10. Subscriptions that need the user's authorization cost 0; the four that don't cost 1 each.
- **Not in the pages read:**
  - the status codes for a missing scope or a duplicate;
  - any affiliate rule for these types.
- **Read only from a summary:** the resub, gift, redemption and stream payload tables. The capability manifest records this, and the live check against real Twitch confirms them.

## Decision

### Asking for the events

- **The streamer's sign-in also asks for** `moderator:read:followers`, `channel:read:subscriptions`, `bits:read` and `channel:read:redemptions`.
  - An older sign-in lacks them. Twitch shows its usual "Sign in again to allow" message, Home's Needs you lists the events that are waiting, and chat keeps working.
- **On the chat session,** right after chat is subscribed, each type the sign-in allows is subscribed.
- **Each type has a status** (`ChatStatus.feeds`): ✓, or why not, in words:
  - a permission to allow by signing in again;
  - Twitch's refusal (403), with a note that subs, bits and channel points need an affiliate or partner channel;
  - the request failing.

  A refusal never stops chat.
- **A revocation of one type** stops only that type, and says so. A revocation of chat still ends the session, as before.
- **The title and category** are read once per session with Get Channel Information (no scope), and updated by `channel.update`. `Service.Channel()` gives them to quotes, so "Add a quote" fills in the category by itself.

### The events (source `twitch`, confirmed)

| Twitch | StreamGremlin event | Payload | Actor |
|---|---|---|---|
| `channel.follow` | `audience.follow.created` | `followed_at` | the follower |
| `channel.subscribe` | `monetization.subscription.started` | `tier` (1–3), `is_gift` | the subscriber |
| `channel.subscription.message` | `monetization.subscription.renewed` | `tier`, `months`, `streak_months` (when shared), `duration_months`, `text` | the subscriber |
| `channel.subscription.gift` | `monetization.subscription.gifted` | `tier`, `total`, `cumulative_total` (when shared), `is_anonymous`, `from` | the gifter, or none |
| `channel.cheer` | `monetization.bits.cheered` | `bits`, `text`, `is_anonymous`, `from` | the cheerer, or none |
| `channel.raid` | `audience.raid.received` | `viewers` | the raiding channel |
| redemption `.add` | `rewards.channel_points.redeemed` | `redemption_id`, `reward {id, title, cost, prompt}`, `user_input`, `status`, `redeemed_at` | the viewer |
| `stream.online` | `broadcast.channel.online` | `stream_type`, `started_at` | none |
| `stream.offline` | `broadcast.channel.offline` | (none) | none |
| `channel.update` | `broadcast.channel.updated` | `title`, `category`, `category_id`, `language`, `labels` | none |

- **Redelivery:** each event's source event id is the EventSub message id, so a redelivery is stored once. A redemption's is `redemption:<id>`, stable however it's delivered.
- **Tiers:** Twitch's `1000`/`2000`/`3000` become 1/2/3. Prime subs arrive as 1000.
- **`from`:** gifts and cheers carry `from`, the giver's name or "Anonymous", so a message works when the giver is hidden (`{viewer}` maps to it).
- **Going live on Twitch is its own event,** not `broadcast.stream.started`. The two mean different things: OBS starts sending, then Twitch says the channel is live. They also have their own triggers, and a workflow made for "The stream starts" isn't suddenly started twice.
  - Timers' "only while live" (ADR-0013) takes the newest of the four: OBS started or stopped, and Twitch online or offline.
- **Test events** exist for every type. A test viewer is the actor; gifts and cheers name them in `from`.

### In the app

- **Editor triggers:**
  - in a Twitch category: Someone follows, subscribes, resubscribes, gifts subs, cheers bits, raids you, and redeems channel points (limited to one reward by its title);
  - in Stream: You go live on Twitch, You go offline on Twitch, and (under More) Your title or category changes.

  Each has its values, such as `{months}`, `{bits}`, `{raider_login}` and `{input}`.
- **Templates** (new category, Twitch events):
  - Thank new followers;
  - Thank subs and resubs (gifted subs excepted);
  - Thank gifted subs;
  - Thank cheers;
  - Welcome raiders, with a link to the raiding channel.
- **Connections:** the list of events from your channel (✓, or why not), and your channel's title and category.
- **Events and Home:** each event in words ("Fan resubscribed: 14 months", "Anonymous gifted 3 subs").
- **CLI:** `streamgremlin twitch` lists the same.
- **mocktwitch:**
  - accepts these subscriptions, checking scopes and conditions as described;
  - can refuse or revoke a type;
  - sends each event (`Follow`, `Subscribe`, `Resub`, `Gift`, `Cheer`, `Raid`, `Redeem`, `GoLive`, `GoOffline`, `UpdateChannel`, or `Notify` with a message id to redeliver);
  - answers Get Channel Information;
  - its `/chat` page sends events for trying them by hand.

## Alternatives considered

- **A second WebSocket session for channel events:** Twitch allows three per user, but one session with chat keeps one connection to watch and reconnect. These 11 subscriptions are far under the limits.
- **Stopping chat when an event is refused:** a non-affiliate can never have bits or channel points, and chat matters more.
- **`broadcast.stream.started` from Twitch too (StreamWisp):** existing workflows would run twice per stream, once for OBS and once for Twitch.
- **Get Streams at connect, to know whether you're live already:** not built. Live state comes from events. After a restart mid-stream, Twitch's last online event is still the newest, so "only while live" is right. A stream that started while StreamGremlin was closed shows as not live until OBS or Twitch says otherwise.

## Consequences

- **Sign in again once:** streamers sign in to Twitch again to get follows, subs, bits and redemptions. Home says so.
- **Not built yet:**
  - fulfilling or refunding redemptions, and managing rewards (F2, a later slice);
  - Hype Trains, polls, predictions, First Words and ads (later F2 slices).
- **Profile pictures:** the events carry ids, so profile pictures for overlays can come with alerts.
