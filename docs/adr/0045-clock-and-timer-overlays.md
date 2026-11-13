# ADR-0045: Clock and timer overlays, and their looks

- **Status:** Accepted
- **Date:** 2026-11-13
- **Based on:** D-092 (clocks, countdowns and stopwatches on overlays); ADR-0036 (packages and themes), which this extends; ADR-0017 (alert overlays).

## Context

The streamer asked for "example plugins for time": a clock, a countdown and a stopwatch, so overlays can show the time and a "starting soon" countdown. Plugins can't draw: Lua plugins have no screen, and themes could only restyle wheels (ADR-0036, `theme.for: ["wheel"]`). The streamer chose a built-in overlay with theme support, and an example look.

## Decision

### The overlay

- **A fifth kind of overlay, `timer`** (migration 0032 rebuilds the table's CHECK, as 0018, 0019 and 0031 did). Its look says what it shows (`timer_mode`: `clock`, `countdown` or `stopwatch`), a label, a countdown's length in seconds (`timer_length`, up to 24 hours; 5 minutes if unset) and its text at zero, and how a clock reads (`clock_24h`, `clock_seconds`, `clock_date`: none, `short` or `long`). These are refused on any other kind.
- **A clock needs nothing from the engine.** The page shows the time on the computer running OBS's browser source, which is this computer: overlays are served on loopback only.

### State

- **A countdown's or stopwatch's state is a settings document,** `overlays.timer.<name>`: `{running, base_ms, since, ended}`. A countdown had `base_ms` left at `since` and counts down while running; a stopwatch had counted `base_ms` and counts up. No state saved (`null`) means at its start: a countdown at its length, a stopwatch at zero. A table wasn't needed: there's one small document per overlay, it rides in backups with the other settings, and it's forgotten when the overlay is deleted or its mode changes.
- **Changes:** start (from where it is; a countdown at zero starts from its length; or a new length; or until a time today, tomorrow if it's passed), pause, reset, add (a countdown, up to 24 hours in all). Through the API (`POST /v1/overlays/{name}/timer`), the Overlays screen, the CLI and workflow steps, so buttons and Stream Deck keys can.
- **A countdown that reaches zero** is saved as ended and sends `overlays.timer.ended` (`overlay`, `label`) once, keyed by when it started, so a workflow can switch scenes. One that ended while StreamGremlin was off is ended at the next start without the event: a scene switch hours late would be wrong.

- **Starting on load** (`timer_auto_start`): the page sends `{"type":"loaded"}` once per page load, never on a reconnect. For an OBS page (never the app's preview) of a countdown or stopwatch with it set, the engine starts it afresh: a countdown from its length, a stopwatch from zero. Loads within 3 seconds of each other start it once.

### Protocol to the page

- `timer` messages carry the state and the engine's time (`running`, `base_ms`, `since_ms`, `now_ms`, `ended`), or `null` at its start. The page counts the seconds itself from them, correcting by `now_ms`, and redraws four times a second. Every page of the overlay, and a page that opens later, gets the latest.

### Looks

- **`theme.for` may now be `["timer"]`** as well as `["wheel"]`: one kind per theme. The Overlays screen offers a theme only to its kind, and the engine refuses a look on the wrong kind. Wheel themes are unchanged. Sounds (D-065) stay wheels' only.
- **The same sandbox as wheel looks** (ADR-0036): a frame from the theme server, `sandbox="allow-scripts"`, no referrer, no network; an add-on only at OBS's "No access" page permission; messages from the frame checked for source, size and rate.
- **Messages to the look:** `timer.snapshot` `{settings, reducedMotion, timer}` once it says it's ready; `timer.updated` `{timer}` on each change of state or look; `theme.settings.updated`. `timer` is `{mode, label, endText, clock: {hour24, seconds, date}, lengthMs, valueMs, running, direction, atMs}`: it was `valueMs` at `atMs` on this computer's clock, moving by `direction` (-1, 0 or 1). The look counts from there; it's never told the overlay's link or anything private.
- **From the look:** only `theme.ready` and `theme.error`. Anything else is dropped.
- **Classic never stops counting underneath.** It shows until the look is ready, and again for good if the look reports an error, so OBS never shows a blank timer.
- **The example look, Flip clock** (`examples/themes/flip-clock`), shows all three modes as split-flap cards. Tests read it as a package and install it, zipped, through the CLI.

## Consequences

- A countdown or stopwatch keeps going across restarts and OBS reloads, and shows the same on every page.
- Add-on authors can restyle timers with the same rules and limits as wheels, and the install dialog says what a timer look is told.
- What the frame is told is only what the stream shows: the time, a label, and where the timer is.

## Alternatives considered

- **Add-on overlays (packages providing whole overlay kinds) first, timers as a package:** more general, but a new package kind and security surface before the timers themselves. The streamer chose the built-in overlay.
- **Timers as Lua plugins:** they can't draw, and a plugin's worker isn't on every stream.
- **The page keeping a countdown's state:** it'd be lost on a reload, and every page would count differently.
