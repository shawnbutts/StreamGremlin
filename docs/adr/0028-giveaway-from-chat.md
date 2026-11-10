# ADR-0028: Drawing a giveaway from recent chat

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** StreamWisp ADR-0078 (drawing from recent chat). Changed: no entry cost to refuse (there's none yet); the window ends at the inbox's newest event when the giveaway opened, not a time; Twitch checks run four at a time, as there, and finish even if the caller stops waiting.

## Context

Streamers ask for a quick "pick someone from chat" with no entry period. AGENTS.md: winners drawn on the server with `crypto/rand` and recorded for audit; never imply odds; never invent capabilities. StreamGremlin knows only the chat it received, not who is watching.

## Decision

- **A giveaway setting, not a new tool:** `from_chat_minutes`, 1 to 60, fixed once open (migration 0017). Opening such a giveaway:
  1. announces it ("🎲 Drawing T now from everyone who chatted in the last N minutes", then who can enter, the odds and the winners);
  2. enters everyone whose chat message or command arrived in the window and before the opening, once each (their first message), in chat order, through the ordinary entry checks;
  3. closes and fingerprints the list, and draws the winners.
  The record, the checks, claims and setting aside are a giveaway's, unchanged.
- **The same rules and chances** apply (ADR-0026, ADR-0027). A practice draw takes test chat; a live one takes real chat. The streamer's own chat never enters.
- **The messages rule** counts messages up to the opening.
- **No consent, so no noise:** nobody hears "you're in" or why they were turned away. The screen lists both, and says those turned away weren't told.
- **No entry command:**
  - the giveaway's command is empty, so it holds no command against workflows (a claim command, if any, is still held);
  - chat after the opening never enters;
  - "Close entries after" and "Anyone who chats enters" don't apply, and are refused.
- **Bounded work:**
  - Twitch checks run four at a time, then everyone is entered at once under the lock;
  - the work finishes even if the caller stops waiting;
  - if it fails part way, the giveaway stays open for the streamer to draw or cancel.
- **No one to draw:** chat hears why ("no one chatted…", or "no one who chatted… can enter"), and it's cancelled.
- **Where the window starts:** it ends at the inbox's newest event when the giveaway opened. It starts at the newest event before the window among those, so a later event carrying an earlier time can't push the start past the end.

### Screen, API, CLI

- **The form's "How viewers enter":** "They type a command" or "Draw now from everyone who chatted recently", with the minutes. The hint says viewers who didn't chat can't be included, and the button says "Draw now".
- **The giveaway** says "Drawn from everyone who chatted in the last N minutes".
- **API:** `from_chat_minutes` on `POST /v1/giveaways`, which answers after the draw.
- **CLI:** `giveaways open -from-chat N TITLE` prints the result. `show` says "from chat of the last N minutes" in place of the command.

## Alternatives considered

- **Get Chatters (everyone connected to chat):** this includes lurkers, but it isn't built, it's Twitch only, and it's a different promise. It may come later, labelled as its own source.
- **A "random chatter" tool with no record:** quicker, but it couldn't be checked afterwards.
- **Telling everyone left out why:** they didn't ask to enter. That would be noise.

## Consequences

- **Silent viewers can't win a draw from chat,** and the form says so.
- **A long window over a busy chat with follow or account rules** makes one or two Twitch calls per chatter, so the request can take a while. The button shows it's busy.
