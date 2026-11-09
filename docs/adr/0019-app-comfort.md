# ADR-0019: App comfort: More…, text size, spacing and search

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** the user's requests during M2 (D-031). StreamWisp had none of these.

## Context

The app has twelve screens, more than the nine shortcut numbers. Many streamers won't need some of them. During M2 the user asked for:
- font size and spacing settings;
- search on busy screens;
- a way to keep screens they don't use out of the way.

On that last point the user said: "maybe instead of hiding them, we could make a 'More...' menu item and let uses pick whatever they want to move there. Home, Events, Workflows, Settings, and Help should always be shown. I think Help should always be at the bottom."

## Decision

### The sidebar

- **Order:** the main screens, then **More…** (only when something is in it), then Settings, then Help, always last.
- **More… opens in place.** It stays open while one of its screens is shown, and doesn't remember being open otherwise.
- **Screens under More… keep working:** timers run and quote commands answer. Nothing is hidden or switched off.
- **Movable:** Connections, Timers, Quotes, Sounds, Overlays, Rewards and Variables. Home, Events, Workflows, Settings and Help never move.
- **On first run** Timers, Quotes and Rewards are under More… (the user's choice).
- **Shortcuts:**
  - Ctrl/⌘+1–9 go to the main screens in sidebar order, so they follow what's moved;
  - Settings is Ctrl/⌘+comma and Help Ctrl/⌘+/;
  - screens under More… have no number.

### Text size and spacing

- **Text size:** Small, Standard, Large or Larger. The root font size is 90%, 100%, 115% or 130%; the body is in rem, so all text and anything sized in rem follows.
  - It's not CSS `zoom`: in the webviews that would also scale viewport units and the overlay preview's frame.
  - Ctrl/⌘ with + (or =), − and 0 change it as in a browser, and save it. If saving fails, the size goes back.
- **Spacing:** Comfortable or Compact. Compact narrows the gaps and padding of the shell, cards, lists, forms and sidebar. The text stays the same.

### Settings

- **The appearance settings document** (`settings` key `ui`) gains `text_size`, `density` and `more`, beside `theme`.
- **Settings saved before** get the defaults for missing fields. A value that can't be used (an unknown size, or a screen that can't move) falls back field by field instead of failing.
- **`more`** is kept in sidebar order without repeats. An empty list is a choice (nothing under More…), different from missing.
- **`PUT /v1/settings/ui`** now changes only the fields given, so the theme radio and the screens list can't overwrite each other.
- **The Settings screen** has Theme, Text size and Spacing, and a **Screens** list with "Under More…" for each movable screen.

### Search

- **Workflows:** in the app, over name, id and what starts each workflow in words (which includes the command). Every word typed must match, ignoring case.
- **Events:** in the engine, because the history is too big to send to the app.
  - `GET /v1/events?q=TEXT` returns the newest events (up to `limit`) whose type, or a text value in the viewer or payload, contains it.
  - Field names never match, so "name" doesn't find every scene change. LIKE wildcards are escaped. It can't be combined with `after`.
  - The screen shows the matches, while the live list keeps following underneath and comes back when the search is cleared (Esc).
- **Quotes and Variables** already had search.
- **Ctrl/⌘+F** focuses the screen's search box, if it has one.

## Alternatives considered

- **Hiding screens:** a hidden screen is easy to forget, and switching it off would stop features silently. More… keeps everything one click away.
- **Fixed shortcut numbers per screen:** they'd run out at nine and leave gaps for screens under More….
- **CSS `zoom` for text size:** see above.
- **Searching events in the app:** it holds only the newest 200.

## Consequences

- **Shortcuts change** for anyone used to the old numbers: Timers and Quotes start under More…, so Sounds is 5, Overlays 6 and Variables 7. Help lists the current ones.
- **Workflow groups** (D-032) come next, in slice 8, with their own ADR.
- **Not yet:** search on Runs, and a jump-to box (Ctrl/⌘+K).
