# ADR-0040: Buttons and the Live screen

- **Status:** Accepted
- **Date:** 2026-11-11
- **Based on:** D-075 (M7, Live); [ADR-0013](0013-timers.md) (timers: a thing the streamer makes, whose event workflows match by id); [ADR-0004](0004-events.md) (the inbox and its deduplication). New in StreamGremlin: StreamWisp had no Live screen.

## Context

D-075 asks for a Live screen: Twitch chat in the middle, and buttons, down the right or along the top, that run workflows. Later in M7 a Stream Deck presses the same buttons. Streamer.bot does this with actions run from its own dashboard and the Stream Deck. StreamGremlin's workflows already start from events, so a button should be one more source of an event, not a second way to run steps.

## Decision

### A button is a named source of an event

`internal/buttons`, migration `0027_buttons.sql`: a `buttons` table with an id, a label (up to 40 characters), a colour (none, or one of eight named ones), the order, and when it was made and changed. Up to 100 buttons. A button holds no steps.

**Pressing** one records `buttons.button.pressed` (source `buttons`, confirmed, no actor) with `{button_id, label, from}`. `from` is `live`, `cli` or, later, `stream_deck`. Workflows start from it with the trigger **You press a button** (category Buttons), matching `button_id`. With no button picked, it matches any button. A press has no viewer, like a timer, so a step that needs one is refused as it is for timers.

**Exactly once:** a press may carry a `request_id` (up to 64 letters, digits, `-` and `_`). The event's source id is the button's id, when it was made, and the request id. The same request again returns the stored press and runs nothing twice, even when it comes later. A button deleted and made again under the same id is a new button, so an old request id doesn't swallow its first press. The app sends a new request id with each click. A press without one is always new.

**The press answers what it started:** the running workflows whose triggers match the event, worked out by the engine (`workflow.Engine.Starting`).

### Deleting and ordering

Deleting a button keeps the workflows that start from it; they never start from it again, as with timers. The order is changed by naming every button once.

### API, CLI and screen

- `GET /v1/buttons` (buttons.read): the buttons, in order, each with the running workflows a press starts.
- `PUT /v1/buttons/{id}`, `DELETE /v1/buttons/{id}`, `PUT /v1/button-order` (buttons.write).
- `POST /v1/buttons/{id}/press` (buttons.press): `{request_id?, from?: live or cli}`. Only a Stream Deck's own token (M7 slice 4) will say `stream_deck`. Pressing has its own permission so that token can press without editing.
- CLI: `streamgremlin buttons`, `buttons press|delete ID`, `buttons set ID -label TEXT [-color NAME]`, `buttons move ID...`.
- **Live**, second in the sidebar (Ctrl/⌘+2), never moved under More…:
  - **Chat:** chat follows `chat.message.received` by cursor, like Events. Each line shows the time, a swatch of the chatter's Twitch colour (never the text's colour, for contrast), their name and badges in words, and Test or Shared chat when it applies. A scrolled-up log stays put and counts new messages.
  - **Sending:** the send box sends as you, or as the bot when one is signed in. Nothing is retried.
  - **Buttons:** right or top, saved in the appearance settings as `live_buttons`. A press says what it started. **Edit buttons** makes, changes, orders and deletes buttons, the last after asking and naming the workflows that stay. **Make a workflow** opens the editor on a starter workflow for the button.
  - **Unknown stays unknown:** when the engine stops answering, or chat's state can't be read, the screen says so instead of looking live.

### Icons (added 2026-11-12, D-076)

Migration `0029_button_icons.sql` adds two columns. `icon` is `''` or `tabler:NAME`, an icon from [Tabler Icons](https://tabler.io/icons) (MIT), which the app bundles: the outline pack, about 5,200 icons, including Twitch, Discord and YouTube logos. `hide_label` (0 or 1) shows only the icon, and needs one. The label stays required: it's the button's name for screen readers, its tooltip, and what a press records.

- **The engine checks only the form of the name** (`^tabler:[a-z0-9]+(-[a-z0-9]+)*$`, up to 72 characters). The pack lives in the app, so the engine doesn't carry a list. A screen that doesn't know an icon shows the label, even when it's hidden, so a button never goes blank.
- **Offline:** the pack is part of the app's build, loaded the first time a button has an icon or the editor opens. Nothing is fetched from the internet.
- **The `tabler:` prefix** leaves room for another pack later without changing what's saved.
- **Nerd Fonts, a second set** (added the same day, D-076): `nf:NAME`, with Nerd Fonts' own names (`md-microphone_off`, `fa-twitch`), so the name pattern also allows `nf:` with underscores. The app bundles Nerd Fonts 3.5.1's Symbols Only font, cut down by `cmd/desktop/frontend/tools/nerd-symbols.py` to Material Design, Font Awesome (and Extension), Devicons, Codicons, Octicons, Weather Icons and Linux logos: 10,709 icons, a 958 KB WOFF2 font and a 279 KB list of names, both loaded only when used. Powerline and other terminal glyphs, and Seti's file-type icons, are left out. Font Awesome's icons and Codicons are CC BY 4.0, so they're credited in Help (Icon credits) and `THIRD-PARTY-NOTICES.md`.
- **Icon-only buttons are small squares** that share rows, in the buttons' order; a labelled button takes its own row on the right, or at least 7.5rem along the top.
- **API and CLI:** `PUT /v1/buttons/{id}` takes `icon` and `hide_label`; `buttons set ID -icon NAME|none [-hide-label]`.

## Alternatives considered

- **An icon font (Font Awesome, Material Symbols) loaded from a CDN:** it needs the internet while live, and a CDN sees every launch. Rejected.
- **Font Awesome or Material Symbols on their own:** Nerd Fonts already holds Font Awesome and Material Design, with their names, in one font, so it covers both. Attribution for the CC BY sets is a credit in Help and the notices file.
- **The whole Nerd Fonts symbols font (2.5 MB TTF):** most of what's left out is for terminals.

- **A button that holds steps:** a second kind of workflow, with its own editor, runs and traces. Rejected: workflows already do this, and a Stream Deck should press the same thing.
- **Pressing by running a workflow directly:** it would bypass triggers, queues and "who can use it", and couldn't start several workflows at once. An event goes through the same path as everything else, and shows on Events.
- **Twitch name colours as text colours:** many fail contrast on one theme or another. A swatch keeps the colour without making names hard to read.

## Consequences

- Every Ctrl/⌘+number shortcut after Home moves down one, and Viewers is now reached from the sidebar. Help lists the current shortcuts.
- Presses are events, kept and swept like any other.
- The Stream Deck (M7 slices 4 and 5) presses buttons through the same route, with a token that can only read and press them.
