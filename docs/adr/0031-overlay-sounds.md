# ADR-0031: Sounds on giveaway and wheel overlays

- **Status:** Accepted
- **Date:** 2026-11-10
- **Builds on:**
  - ADR-0017 with D-038 (an alert's sound plays in the overlay page, so OBS hears it through the browser source);
  - ADR-0029 (the giveaway overlay);
  - ADR-0030 (wheels).

## Context

After the wheels live check the user asked: "can we add sounds to the overlay?" Alert overlays already play a sound that the "Show an alert" step chooses. Giveaway and wheel overlays have no step; they play by themselves when a draw or spin comes.

## Decision

- **Two sounds per giveaway or wheel overlay, kept in its look:**
  - `start_sound` plays as a giveaway's roll or a wheel's spin starts;
  - `end_sound` plays as it lands on the winner or stops;
  - each has a volume from 0 to 100 (default 80);
  - looks saved before get no sounds.
- **The sounds come from the sound library, by name.**
  - The look's check accepts only a library name pattern.
  - The API refuses a name that isn't in the library, and refuses start or end sounds on an alert overlay, whose step chooses its sound.
- **The page plays them,** fetching `/o/<token>/sound/<name>` through the overlay's own link (the server already serves the library only under a working link, and the CSP allows `media-src 'self'`). OBS hears them through the browser source; "Control audio via OBS" gives it a mixer bar.
- **When they don't play:**
  - the app's previews never play them;
  - with reduced motion there's no roll or spin, so only the end sound plays;
  - a sound that won't play is skipped silently, since chat and the overlay still say everything and no step waits on it.
- **Screen and CLI:**
  - the Overlays screen has a Sounds group for giveaway and wheel overlays, with a picker from the library and a volume slider each;
  - `overlays look NAME -start-sound S -start-volume N -end-sound S -end-volume N` (`none` clears a sound).

## Alternatives considered

- **A tick for each wedge or name passing:** that needs a sound timed to the animation's slowing, and many short plays. A start sound (a drumroll, a wheel's clatter) covers it.
- **Bundled sounds:** StreamGremlin ships none, as for alerts. Streamers add their own on the Sounds screen.
- **Sounds per wheel instead of per overlay:** one overlay usually shows every wheel. Per overlay is simpler and matches where the sound plays.

## Consequences

- **The page's version changes,** so open overlay pages reload themselves once after the update.
