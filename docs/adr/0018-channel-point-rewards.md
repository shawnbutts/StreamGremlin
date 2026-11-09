# ADR-0018: Channel point rewards

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** FEATURES "Channel point reward management: create, pause, change cost, fulfil or refund. Only for rewards StreamGremlin created; others are 'trigger only', with an offer to create a managed copy." StreamWisp had no ADR for it (its rewards shop, ADR-0019, is for its own points, F4 here).

## Context

Since slice 3, redemptions start workflows (ADR-0015). Streamers also expect to make rewards, pause them when they're not live, and mark redemptions done or refund them, as Streamer.bot's channel point sub-actions do.

**Twitch's API reference** (checked 2026-11-09):
- **Endpoints:** Create, Get, Update and Delete Custom Reward; Get Custom Reward Redemption; Update Redemption Status. All need `channel:manage:redemptions`; the two reads also accept `channel:read:redemptions`.
- **Affiliate or Partner only:** every endpoint answers 403 "The broadcaster is not a partner or affiliate."
- **"The app used to create the reward is the only app that may"** update it, delete it, or get and update its redemptions (403 for any other Client ID). Get Custom Reward's `only_manageable_rewards=true` lists the ones the calling app may manage.
- **Limits:**
  - up to 50 rewards per channel, on or off;
  - titles 1–45 characters and unique;
  - prompts up to 200 characters;
  - cost at least 1;
  - per-stream limits at least 1;
  - a cooldown of 1 to 604,800 seconds (the maximum is stated on Update only).
- **A redemption moves only from UNFULFILLED,** to FULFILLED or CANCELED. "Setting the status to CANCELED refunds the user's channel points." Rewards that skip the queue are FULFILLED at once. Deleting a reward marks its waiting redemptions FULFILLED.
- **Not stated:** rate limits for these endpoints, a maximum cost or per-stream limit, a default color, and how several ids answer in one status update.

**The user's channel** is too new to be an Affiliate (2026-11-09). The slice is built and tested against mocktwitch, which follows the rules above. The live check confirms the refusal is shown as such, and the rest waits for an Affiliate channel.

## Decision

### Twitch is the only record

- **StreamGremlin stores no rewards.** The list is read from Twitch each time: all rewards, then `only_manageable_rewards`, to mark which StreamGremlin made ("managed"). Nothing can drift, there's no migration, and a reward deleted on Twitch is simply gone.
- **Making a reward again after an unclear answer is harmless:** if Twitch says the title is taken by a managed reward with the same cost, that reward is the answer.
- **Changing a reward** sends only the fields given. A limit of 0 turns it off.
- **Deleting** one that's gone counts as done.
- **Completing or refunding** a redemption that's no longer waiting reads its status:
  - the same as asked: harmless, marked as a repeat;
  - the other way: refused, saying "already handled: it's done" (or "refunded");
  - gone (Twitch keeps handled redemptions only a few days): says so.

  So a retried step never refunds after a completion, or the other way round.

### In words, never codes

| Twitch's answer | StreamGremlin says |
|---|---|
| 403 partner or affiliate | Channel points need an Affiliate or Partner channel; Twitch says yours isn't one yet |
| 403 Client ID | Only rewards made in StreamGremlin can be changed, deleted, completed or refunded here |
| a missing permission | Sign in to Twitch again and allow managing channel point rewards |
| 400 duplicate title | Another reward on your channel has that title |
| 404 | No such reward, or no such redemption waiting |
| anything unclear | the outcome is unknown (502, `unknown_outcome`) |

### Asking for it

The streamer's sign-in also asks for `channel:manage:redemptions`. An older sign-in still lists rewards (with the read permission), and asks to sign in again before changing anything. Home's Needs you says so, as for any missing permission.

### Steps

- **"Complete or refund the redemption"** (`rewards.redemption.set {action: done | refund}`) acts on the redemption that started the run. Steps can now read the triggering event (`workflow.Call.Event`). On any other event it fails and says it needs a redemption. For a reward made on Twitch it fails and says why.
- **"Change a reward"** (`rewards.update {reward, state?: on | off | pause | resume, cost?}`) takes a reward by title (literal), and only a managed one. It's safe to repeat.
- **Both are External:** a test event never completes, refunds or changes anything.

### API, CLI and screen

- **API,** with new permissions `rewards.read` and `rewards.write`:
  - `GET /v1/rewards`, with `state` (`ok`, `not_signed_in`, `needs_permission`, `no_channel_points` or `unknown`), so the app shows why there's no list instead of an error;
  - `POST /v1/rewards`;
  - `PUT` and `DELETE /v1/rewards/{id}`;
  - `GET /v1/rewards/{id}/redemptions` (waiting, oldest first, up to 50);
  - `POST /v1/rewards/{id}/redemptions/{redemption} {action}`.
- **CLI:** `streamgremlin rewards`, `rewards add -cost N … TITLE`, `set`, `queue`, `done`, `refund` and `delete`.
- **The Rewards screen** (in the sidebar; the nine shortcut numbers are taken):
  - why there's no list, when there isn't one;
  - each reward with its cost, state, limits and who made it;
  - for managed ones: Edit, Pause or Resume, Turn on or off, Queue (with Done and Refund for each viewer) and Delete (in the app's dialog);
  - for ones made on Twitch: "Make a copy", which fills the form in (Twitch needs a different title, or the original deleted first);
  - a form to make or edit one.

  The editor's "Change a reward" picks from managed rewards.
- **mocktwitch** keeps rewards and redemptions by those rules:
  - it can play a non-Affiliate channel (`SetAffiliate`) and add rewards "made on Twitch" (`AddDashboardReward`);
  - `Redeem` uses a channel's reward by title when it has one.

## Alternatives considered

- **Keeping a copy of rewards in the database:** it could disagree with Twitch, and gives nothing Twitch doesn't.
- **Changing rewards made on Twitch:** Twitch doesn't allow it.
- **Making a managed copy automatically, and turning the original off:** that changes the streamer's channel without asking. The screen fills the form in, and they decide.
- **Refunding by default when a workflow fails:** a step does exactly what it says; a workflow can refund where it wants to.

## Consequences

- **Sign in again once** for the new permission.
- **Not Affiliate yet:** the Rewards screen says so, and workflows for redemptions wait for the day it is.
- **Not confirmed on real Twitch:** making, changing and refunding. The user's channel can't yet. The refusal message is checked live.
- **A later "app comfort" slice** may give Rewards a shortcut, with search.
