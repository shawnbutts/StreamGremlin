# ADR-0022: Hype Trains

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** the Hype Train part of StreamWisp ADR-0060 (D-086 there): the event names and the payload kept close to Twitch's.

## Context

FEATURES lists Hype Trains for F2. They need an Affiliate or Partner channel. The user's isn't one yet, so by D-039 this is built and checked against mocktwitch, and M2 closes with that recorded.

**Twitch's docs** (read 2026-11-09):
- **The subscription types table** lists only version **2** of `channel.hype_train.begin`, `progress` and `end`. They need `channel:read:hype_train`; the condition is `broadcaster_user_id`.
- **The events carry:**
  - `id`, `level` and `total`;
  - `progress` and `goal` (not at the end);
  - `type` (`regular`, `treasure`, `golden_kappa`);
  - `top_contributions` (`user_id`, `user_login`, `user_name`, `type` bits, subscription or other, and `total`);
  - `is_shared_train`, with the shared participants;
  - `started_at`, then `expires_at` or `ended_at` and `cooldown_ends_at`;
  - at the beginning, `all_time_high_level` and `all_time_high_total`.
- **Progress** comes "for each contribution that caused the Hype Train to begin", and delivery order isn't guaranteed.
- **Not stated in the pages read:** whether a non-Affiliate's subscription is refused with 403, and whether v1 is deprecated.

## Decision

- **Events, source `twitch`, confirmed, no actor** (the train is the channel's, not one viewer's):
  - `monetization.hype_train.started`;
  - `monetization.hype_train.updated` (every progress, including going up a level: a workflow compares `level`);
  - `monetization.hype_train.ended`.
- **Payload:**
  - `hype_train_id`, `level`, `total`, `train_type`, `is_shared`, `started_at`;
  - `top_contributions[{user_id, login, name, type, total}]`, and `top_contributor`: the one with the largest total;
  - `progress`, `goal` and `expires_at` while running; `ended_at` and `cooldown_ends_at` at the end;
  - `all_time_high_level` and `all_time_high_total` when Twitch sends them.

  Redelivery is stored once (the EventSub message id).
- **Subscribed with the other channel events** (ADR-0015). Each type is listed on Connections with ✓, or why not. A refusal never stops chat.
- **The sign-in also asks for `channel:read:hype_train`,** so sign in to Twitch again once (Home says so).
- **Triggers:** "A Hype Train starts", "The Hype Train moves" (under More) and "A Hype Train ends". Their values are `{level}`, `{total}`, `{top_contributor}`, and while running `{goal}` and `{progress}`.
- **Test events** for each, with a level, a total and a test top contributor. Events and Home show them in words.
- **Template:** "Thank the Hype Train" (Thanks group): at the end, one of two thank-yous with the level and the top contributor.
- **mocktwitch** accepts the subscriptions and sends trains (`HypeTrain(channel, phase, …)`).

## Alternatives considered

- **A separate level-up event:** `updated` carries the level (StreamWisp's choice too).
- **The top contributor as the actor:** the train belongs to the channel; `top_contributor` names them without making a contribution look like their own event.

## Consequences

- **Not checked on real Twitch** until the channel is an Affiliate (D-039).
- **Shared trains** (several channels) are flagged with `is_shared`; their participants aren't listed yet.
