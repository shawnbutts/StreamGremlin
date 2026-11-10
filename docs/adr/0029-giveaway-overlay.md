# ADR-0029: The giveaway overlay

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0076 (giveaway draws on an overlay), with D-126 there. Changed:
  - the overlays table is rebuilt for the new kind (0018), as StreamWisp's 0023 did;
  - the winner's picture is sent after the roll starts, not waited for;
  - the roll's names are capped at 24, the winner last, as there.

## Context

Giveaways tell chat everything (ADR-0025 to ADR-0028). Streamers also want the giveaway on stream: how to enter, the count, and a moment when the winner is revealed. AGENTS.md:
- chat alone carries everything a viewer needs, and an overlay alone is never enough;
- winners are drawn on the server;
- unknown states aren't shown as confirmed.

Overlays (ADR-0017) already have:
- one browser source per overlay, the user's choice;
- private links;
- live updates over a WebSocket;
- the app's previews;
- looks;
- a page that reloads itself after an update.

**A theme-system starter was considered** (a ChatGPT concept the user kept in `tmp/streamgremlin-theme-starter`): imported, scripted themes, wheels and a selector board. The user chose to keep this plan for the giveaway overlay and to use the wheel concept for a separate wheel feature (D-048). Its safeguards are used here:
- the overlay never picks;
- the winner is always where the roll lands;
- names are text, never markup;
- the state is sent again to a page that reconnects.

## Decision

- **A second kind of overlay, `giveaway`** (migration 0018 rebuilds the overlays table to allow it). It uses an overlay's look: position, colours, font, outline, shadow and pace. Seconds on screen don't apply.
  - **Added on the Overlays screen** ("Kind": alerts or giveaways), or with `overlays add -giveaway NAME`.
  - **"Show an alert" refuses a giveaway overlay.**
- **What it's told** (public only, as chat was):
  - **State, after every change:** open, each entry, close, each draw, claims, forfeits, set aside, end and cancel. It holds the title, how to enter, who can enter, the odds in words, the entry count, when entries close, the winners, and when it ended. The hub keeps the newest state for pages that open later.
  - **Each winner, after the draw commits:** the winner's name and up to 24 names to roll through, other candidates shuffled for show (it decides nothing, but uses `crypto/rand` all the same) with the winner last. Also the claim line.
  - **The winner's Twitch profile picture,** asked after the roll has started and sent when known, only from Twitch's picture host.
  - **Practice giveaways go only to the app's previews,** never to OBS.
- **The page** (`page/giveaway.js`):
  - **The panel:** the title, how to enter, the count and a countdown, who can enter, the odds, and the winners so far.
  - **On a draw:** names roll for 3.5 seconds, slowing, and always land on the winner it was sent, even if the roll's list disagrees. Then the winner shows for 5 seconds with their picture (or initial) popping in and the claim line.
  - **Winners of one draw play one after another.**
  - **An ended giveaway** shows its winners for 20 seconds; a cancelled one goes at once.
  - **Reduced motion** shows the winner without the roll.
  - **Text is set with `textContent`.**
- **Chat waits for the roll, only when it can be seen:**
  - **When a giveaway overlay is open in OBS** (not just the app's preview) at the draw, each winner's message gets `send_after`: draw time plus 4 seconds, plus 8.5 seconds for each earlier winner of the same draw. Later messages wait behind it, so chat keeps its order.
  - **It's a time, not the page's answer,** so a closed or stuck overlay delays chat by seconds at most. With no overlay open, nothing waits.
  - **Lateness** (a message not sent within 2 minutes) counts from `send_after`.
- **The `giveaways.winner.drawn` event stays immediate,** so a workflow's sound or scene change happens at the draw.
- **Samples:** "Preview a draw" (the app's preview) and "Sample in OBS" show a made-up giveaway and draw on that overlay without touching the running giveaway's state.

## Alternatives considered

- **Waiting for the page's answer before chat:** a hung page would hold chat. A fixed time is enough.
- **A wheel:** with hundreds of entrants, or unequal chances, a wheel can't show the real odds. A roll can, and a wheel is now a feature of its own (D-048).
- **Imported, scripted themes:** they would run third-party code in OBS. That needs a decision of its own, alongside content packs.

## Consequences

- **A winner who must claim has about 4 seconds less** after chat names them, because the claim time starts at the draw (the recorded time). The Overlays screen says so.
- **Each live draw makes one Get Users call** for the picture (cached for 30 minutes).
- **The overlay page's version changes,** so open pages reload themselves once after the update.
