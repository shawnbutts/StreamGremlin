# ADR-0027: More chances for subscribers

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0077 (more chances), with D-128 and D-129 there. Changed:
  - only subscribers' chances, from the chat message's subscriber flag;
  - no tier chances (another Twitch call per subscriber) and no watch-time chances (no chatter list yet; that belongs to the points slice);
  - no cap, since one number can't exceed 10.

## Context

Giveaways promise "every entry has the same chance" (ADR-0025). Streamers often reward subscribers with more chances. AGENTS.md says never to imply odds are fair unless verified, and draws stay server-side, use `crypto/rand`, and are recorded for audit.

## Decision

- **`subscriber_chances` on a giveaway, 1 to 10** (1, or unset, is everyone the same). A subscriber, by the chat message's flag, holds that many chances. It can't be combined with subscribers only (or a months rule), where everyone would hold the same.
- **Set at entry and kept:**
  - `giveaway_entries.chances` is fixed (a trigger), as is the giveaway's `subscriber_chances` (migration 0016);
  - "you're in" names a subscriber's chances: "@ann (3 chances)".
- **Said plainly:**
  - the opening's "every entry has the same chance" becomes "subscribers get 3 chances to everyone else's 1";
  - the app and `giveaways show` say the same;
  - the odds are never hidden or called equal.
- **Drawn as tickets:**
  - each entry not yet drawn holds its chances as tickets in a row, in entry order;
  - `crypto/rand` picks one ticket uniformly;
  - `pick_index` is the ticket and `candidates` the tickets in play, so the record keeps its shape and stays checkable. For an unweighted giveaway a ticket is an entry, as before.
- **Fingerprinted:**
  - a weighted giveaway's lines end with the chances: `<seq>\t<platform>:<user id>\t<chances>\n`, so chances can't change after closing;
  - unweighted lines are unchanged, so earlier giveaways still verify.
- **Checked:** `streamgremlin giveaways verify` and **Check the draws** in the app expand the tickets in entry order and check each pick's holder.
- **Screen, API, CLI:**
  - the form's "Chances for each subscriber", which says what it does;
  - a chances tag on entries;
  - draws as "ticket N of the T held by those not yet drawn";
  - `subscriber_chances` on `POST /v1/giveaways`;
  - `giveaways open -sub-chances N`.

## Alternatives considered

- **An entry row per chance:** this breaks one entry per person and the entry list.
- **Tier chances (Get Broadcaster Subscriptions):** a call for each subscriber who enters, and more to explain. Left until asked for.
- **Chances for bits:** a cheer would buy an advantage. Not done.
- **Chances worked out at draw time:** they would depend on when Draw is pressed, and couldn't be fingerprinted at closing.

## Consequences

- **Checking a weighted giveaway by hand needs one more step:** expand the tickets.
- **The "Open a giveaway" workflow step has no chances,** so a workflow's giveaway always gives everyone the same chance, and says so.
