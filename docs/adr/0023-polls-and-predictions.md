# ADR-0023: Polls and predictions

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** StreamWisp ADR-0060 (the events) and ADR-0061 (starting them), with D-086 and D-087 there. Changed: StreamGremlin's events are Twitch-only for now, so YouTube's poll shape isn't mixed in. A Polls screen is built, and predictions can be locked, resolved and canceled from workflows.

## Context

FEATURES lists polls and predictions, events and starting them, for F2. Both need an Affiliate or Partner channel, so by D-039 they're built and checked against mocktwitch.

**Twitch's docs** (read 2026-11-09; the API reference's sections were read in full):
- **EventSub v1:**
  - `channel.poll.begin`, `progress` and `end` (`channel:read:polls` or `manage`): `choices` with votes and channel points votes; at the end, `status` `completed`, `archived` or `terminated`.
  - `channel.prediction.begin`, `progress`, `lock` and `end` (`channel:read:predictions` or `manage`): `outcomes` with users, channel points and `top_predictors`; at the end, `winning_outcome_id` and `status` `resolved` or `canceled`.
- **Create Poll** (`channel:manage:polls`):
  - title up to 60 characters;
  - 2 to 5 choices of up to 25;
  - 15 to 1800 seconds;
  - optional channel points voting, 1 to 1,000,000 per extra vote;
  - one at a time ("The broadcaster already has a poll that's running", 400).
- **End Poll** (`PATCH`, JSON body): `TERMINATED` (results stay visible) or `ARCHIVED` (removed). Only an active poll.
- **Create Prediction** (`channel:manage:predictions`):
  - title up to 45;
  - 2 to 10 outcomes of up to 25;
  - 30 to 1800 seconds;
  - one at a time.
- **End Prediction:**
  - `LOCKED` (only from active), `RESOLVED` with `winning_outcome_id`, or `CANCELED` (refunds), the last two from active or locked;
  - unresolved after 24 hours, Twitch cancels it.
- **Affiliate or Partner only,** per the polls and predictions guides. The reference lists no status code for it.
- **Not stated:** the order of Get Polls and Get Predictions. The first is taken as the newest.

## Decision

### Events (source `twitch`, confirmed, no actor)

- **`chat.poll.started`, `updated` and `ended`:**
  - `poll_id`, `title`, and `choices[{id, title, votes, channel_points_votes}]`;
  - `votes` (the total);
  - `leader`: the choice with the most votes, or "" when tied (`tied`) or empty;
  - the channel points voting settings;
  - `ends_at`, or at the end `status` and `ended_at`.
- **`chat.prediction.started`, `updated`, `locked` and `ended`:**
  - `prediction_id`, `title`;
  - `outcomes[{id, title, color, users, channel_points, top_predictors[{user_id, login, name, points_used, points_won?}]}]`;
  - `locks_at` or `locked_at`;
  - at the end, `status`, `winning_outcome_id`, `winner` (its title) and `ended_at`.

### Starting and ending, as the streamer

- **New permissions** in the sign-in: `channel:manage:polls` and `channel:manage:predictions`. Each covers the reading too.
- **Checked before asking Twitch:** the documented limits, in words. Blank choices and outcomes are dropped.
- **Repeats are safe:**
  - Starting the same poll or prediction again while it runs returns it ("repeated").
  - A different one is refused while one runs.
  - Ending a poll that isn't running says so.
  - Resolving to the same winner again, or locking a locked one, is a repeat.
  - Another ending after it ended is refused, naming how it ended.
- **The winner** is an outcome's words (any case) or its number from 1, so `!win 2` in chat can resolve one.

### Steps (External: a test event never starts or ends one)

- "Start a poll" (question, choices 1 to 5, seconds, channel points per extra vote) and "End the poll" (show results or remove; with none running it's not a failure);
- "Start a prediction" (question, outcomes 1 to 5 in the step; Twitch allows 10, and the screen and API take 10) and "End the prediction" (lock, resolve with a winner, or cancel).

### Screen, API, CLI

- **API**, with permissions `polls.read` and `polls.write`:
  - `GET /v1/polls`: the newest poll and prediction, with `state` as for rewards;
  - `POST /v1/polls`, `POST /v1/polls/end {archive}`;
  - `POST /v1/predictions`, `POST /v1/predictions/end {how, winner}`.
- **The Polls screen,** under More… by default:
  - why there's nothing, when there isn't;
  - the poll with a bar of each choice's share, End and show results, and End and remove;
  - the prediction with each outcome's viewers and points, Lock bets, Resolve, and Cancel and refund (both in the app's dialog);
  - forms to start either, offered only when none is running.

  It refreshes every 3 seconds.
- **CLI:** `streamgremlin polls`, `polls start`, `end`, `predict`, `lock`, `resolve WINNER` and `cancel`.
- **Templates** (Polls group):
  - "Announce poll results": the winner or a tie, when a poll completes;
  - "Announce prediction results": when one resolves.
- **mocktwitch** follows the documented rules, and adds `Vote`, `Bet` and `CompletePoll`.

## Alternatives considered

- **Mixing in YouTube's poll shape now (StreamWisp):** there's no YouTube here yet; the payload can grow when there is.
- **Choices as one comma-separated field:** a field per choice is clearer, as the user asked for message versions.

## Consequences

- **Not checked on real Twitch** until the channel is an Affiliate (D-039).
- **Sign in again once** for the new permissions.
- **A prediction left unresolved for 24 hours is canceled by Twitch.** The screen shows its state when it next loads.
