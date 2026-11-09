# ADR-0017: Alert overlays

- **Status:** Accepted
- **Date:** 2026-11-09
- **Based on:** StreamWisp ADR-0020 (the overlay server), with its amendments D-123 (a look, profile pictures, previews) and D-124 (slide or pop), and the picture part of its ADR-0074. Changed: only the alert kind is built; a page answers when an alert *starts* as well as when it ends, and alerts on one overlay take turns.

> **Amended by D-038 (2026-11-09):** an alert's sound plays in the overlay page, not the app's window, so OBS hears it through the browser source: with "Control audio via OBS" ticked, it has the source's own mixer bar and reaches the stream and recordings. (Found in the live check: on macOS, OBS doesn't capture the computer's sound by default, so a sound played in the app reached the monitor but not the mixer.)
> - **The alert carries `sound {name, volume}`.** The page fetches `/o/<token>/sound/<name>` (the overlay server serves the library under a working link only, with ranges; the page's CSP gains `media-src 'self'`), plays it at the volume as the alert starts, and answers `sound_started` or `sound_error` with the browser's reason.
> - **The step waits up to 5 seconds for that answer.** If every page refused, it fails ("the alert showed, but its sound didn't play: NotAllowedError"); if none answers, it fails with an unknown outcome, so a trace never claims a sound nobody heard. A retried step shows nothing again (the alert's idempotency key), so the sound doesn't repeat either.
> - **Silent where it should be:** previews never play it, and a test only says it would.
> - **"Play a sound" on its own** still plays in the app's window.
>
> **Amended by D-035 (2026-11-09):** a look has a `pace`: how long animations take, as a percent of the usual (50 to 300, default 100). The page multiplies every transition's duration and delay by it, and waits the matching time before reporting an alert done. The reduced-motion fallback isn't affected. Looks saved before get 100.
>
> **Amended by D-034 (2026-11-09):** a third animation, `unfold` ("Picture, then words"). The viewer's picture slides in from the nearest edge, then the words unfold from behind it, touching it. Leaving reverses it: the words fold back, then the picture goes. On right-hand positions it mirrors, so the words open toward the middle. Without a picture (tests, samples, anonymous events, a picture that won't load), a circle with the viewer's initial, or ★ without a viewer, keeps the shape. So alerts carry the viewer's `name` (from the event's actor; "Sample" for samples). The other animations are unchanged and never show an initial.
>
> **Amended by D-029 (2026-11-09):** a look also has, for the box, a gradient (a second color and an angle), corner roundness, an outline (width and color) and a drop shadow (strength and color); and for the words, a font (six lists most computers have, or any font installed on this computer by name), a size (50–200%), a title color, a letter outline (width and color) and a text shadow. Fonts are never downloaded: the page's CSP stays as it is, and OBS runs on the same computer, so it has the same fonts. Every value is checked by the engine and again by the page before it becomes CSS; a font's name is limited to letters, digits, spaces and `. _ & ' -`, so it can't end the CSS value. Looks saved before keep their appearance: the new fields default to what the page drew before, and a title keeps the text's color. "Preview an alert" can send an unsaved look to the previews only (`POST …/test?preview=1 {look}`, `Hub.PreviewLook`); OBS keeps the saved look.

## Context

F2 needs alerts on stream: a follow, a sub, a raid shows a box in OBS (D-004; FEATURES "Alert overlays with slide or pop", "Profile pictures for overlays"). Streamers coming from Streamer.bot are used to adding a browser source per alert overlay and pointing it at a local page.

**OBS's browser source** is Chromium (CEF). It loads a web address, at a size the streamer chooses, and keeps running in scenes that aren't showing, unless "Shutdown source when not visible" is ticked. Several scenes can share one source (Paste (Reference)), or nest one scene in others.

**The user's choice (2026-11-09):** grouping several overlays into one "master" page, so a scene needs one browser source, was discussed and turned down: "i think it does overly complicates the setup. let's stick to the orginal plan". Each overlay stays one browser source, placed in OBS.

## Decision

### The overlay server (`internal/overlays`)

- **A listener of its own,** separate from the API: `127.0.0.1:7431` by default (`brand.DefaultOverlayAddr`, `engine -overlay-addr`, `off` for none).
  - If that address is taken, a free loopback port is used until restart. The engine logs a warning, and the Overlays screen and `streamgremlin overlays` say that links pasted before won't work meanwhile.
  - The API token is never accepted here, and the overlay links are never accepted by the API.
- **Links:** `/o/<token>/`, where the token is 256 random bits (base64url).
  - The database keeps only its SHA-256 (migration `0011_overlays.sql`), so a backup never carries a working link. The token itself is in the secret store (`overlay/<name>`), so the app can show the link again.
  - A link opens one overlay and lets its page say how alerts went. Nothing else.
  - **New link:** the hash is replaced first, so the old link fails at once even if saving the new one fails. Pages using the old link are disconnected.
  - **Deleting** an overlay ends its link and closes its pages.
  - **A link that isn't saved on this computer** (forgotten in Settings, or a backup restored elsewhere) is shown as such, with "make a new link".
  - Links are never logged, never in `GET /v1/overlays`, and never on screen in the app unless the clipboard refuses them.
- **Requests:**
  - the `Host` must name this computer (`localhost` or a loopback IP), against DNS rebinding;
  - WebSockets must come from the page's own origin, or send none (coder/websocket's check);
  - a link that doesn't work answers 404 with an empty body: OBS shows the page to viewers, so an error never appears on stream.
- **The page** (`page/index.html`, `overlay.js`, `overlay.css`, embedded) is plain HTML, CSS and JavaScript with no build step.
  - CSP: `default-src 'none'; script-src 'self'; style-src 'self'; img-src https://static-cdn.jtvnw.net; connect-src 'self' ws://127.0.0.1:* ws://localhost:*; base-uri 'none'; form-action 'none'; frame-ancestors wails://localhost http://wails.localhost`.
  - `frame-ancestors` names the desktop app's origins (Wails v3: `wails://localhost` on macOS and Linux, `http://wails.localhost` on Windows, read from its source) so the app can preview it; nothing else may frame it.
  - Plus `Referrer-Policy: no-referrer`, `Cache-Control: no-store` and `nosniff`.
  - Viewer text is only ever set with `textContent`. Pictures are used only from `https://static-cdn.jtvnw.net/`, checked by the engine and again by the page.
  - The page is transparent. It shows a "Not connected" note only in previews or with `?status=1`.
  - It honours reduced motion.

### Protocol (WebSocket `/o/<token>/ws`, subprotocol `streamgremlin.overlay.v1`, 4 KiB read limit)

- **Engine to page:**
  - `hello {protocol: 1, overlay, preview}`, at connect and whenever the look changes;
  - `alert {alert: {id, title?, text, picture?, seconds}, ms_left}`.
- **Page to engine:** `ack {id, status: started | done | error, detail?}`. The detail is cut to 200 characters.
- **Keeping it working:**
  - pings every 20 seconds;
  - a page that can't keep up (64 waiting messages) is disconnected, and reconnects;
  - pages reconnect from 1 to 10 seconds apart;
  - a page that connects while an alert is on screen gets it with the time left; pages skip alerts they've already played.

### Showing an alert (`Hub.Show`)

- **Alerts on one overlay take turns.** The next starts when the page says the one before is `done`, or after its time plus 5 seconds if no page says so.
  - Up to 25 may wait; more are refused ("too many alerts are waiting").
  - An alert that has waited 2 minutes is dropped and says so.
  - A cancelled run gives up its place.
- **Honest results.** A step waits up to 5 seconds for a page to say it started:

| What happened | The step says |
|---|---|
| a page started it | shown, with how many pages and previews had it |
| every page failed | failed, with the page's reason |
| no page has the overlay open | "isn't open in OBS: add its link as a browser source", at once (503 from the API) |
| no page answered | "was sent the alert but didn't say whether it showed it" (504, `unknown_outcome`) |

- **"Shown" means a page in OBS (or a browser) started it.** OBS doesn't tell a page whether its scene is on air, so an overlay in a hidden scene that's still running counts as shown. The Overlays screen says how many pages have each overlay open.
- **Retries:** a retried step with the same idempotency key doesn't show again for an hour, including when no page answered (it may have shown). A failed one isn't remembered, so a retry tries again.
- **Previews are never an audience.** The app's preview connects with `?preview=1`. It gets the same messages, but its answers are ignored, and an overlay open only in the preview counts as not open in OBS.

### The step: "Show an alert" (`overlays.alert`)

- **Parameters:** `overlay` (literal), `text`, `title?`, `seconds?` (2–60; the overlay's own time by default), `picture?` (the viewer's profile picture), `sound?` (from the library, ADR-0016).
- **Long text is shortened, not refused:** a title to 100 characters and the text to 500, with an ellipsis, so a long chat line never stops an alert.
- **A Preview action** (new: `workflow.Action.Preview`). It's External, but it runs for test events too, told it's a dry run. Then it sends the alert to the overlay's previews only, never waits, never looks up a picture, and plays no sound.
  - The trace says it never reaches OBS. Its output says whether a preview showed it: "shown in 1 preview(s) in the app", or "nobody saw it: no preview of alerts was open".
  - **Test on the Workflows screen** opens a preview of each overlay the workflow shows alerts on (named in "Show an alert" steps, anywhere in it), right there. It waits up to 4 seconds for the previews to connect before sending the test event, and says so if they don't. (Added after the live check: Test sent the alert to previews only, and none was open.)
- **The picture** comes from Twitch's Get Users by the event viewer's id: no scope, kept 30 minutes, and only from `static-cdn.jtvnw.net`. An event with no Twitch viewer, such as an anonymous gift, has none.
  - A picture that can't be found never stops the alert; the trace says why it's missing.
  - Workflow calls now carry the event's actor (`workflow.Call.Actor`).
- **The sound** plays once a page says the alert started, so it's in time with alerts that waited their turn.
  - Its idempotency key is the step's plus `/sound`.
  - If the alert shows but the sound doesn't play, the step fails and says so.

### API, CLI and screen

- **API,** with new permissions `overlays.read` and `overlays.write`:
  - `GET /v1/overlays`: the overlays with their open pages and previews, the server's address and any note, and the positions and animations offered;
  - `POST /v1/overlays {name, kind: alert, look?}`: answers with the link; a taken name is refused (409);
  - `PUT /v1/overlays/{name} {look}`: open pages change at once;
  - a look given in part keeps the rest: the defaults when adding, the current look when changing;
  - `DELETE /v1/overlays/{name}`;
  - `GET` and `POST /v1/overlays/{name}/link`: the link, and a new link;
  - `POST /v1/overlays/{name}/test`: a sample alert in OBS, or with `?preview=1` in the previews only.
- **A look,** checked on save:
  - a position: a corner, top or bottom middle, or center;
  - slide or pop;
  - text and background colors (`#RRGGBB`);
  - the background's opacity (0–100);
  - seconds on screen (2–60).

  The default is top middle, slide, white on the brand's dark blue at 90%, for 6 seconds.
- **CLI:** `streamgremlin overlays`, `overlays add NAME`, `link`, `new-link`, `look NAME [-position …]`, `test` and `delete`.
- **The Overlays screen** (Ctrl/⌘+8):
  - each overlay, with whether OBS has it open;
  - Edit, Copy link, Sample in OBS, New link and Delete (the last two in the app's dialog);
  - the look, with a live preview: the overlay page itself in a frame (`frame-src http://127.0.0.1:*` in the app's CSP), scaled from 1920×1080 onto a checkerboard;
  - Preview an alert;
  - adding an overlay.

  The editor's "Show an alert" picks from your overlays.
- **A template:** "Show an alert for new followers", with the follower's name and picture.
- **Navigation:** Variables moves to Ctrl/⌘+9, and Help to Ctrl/⌘+/.

## Alternatives considered

- **Grouping overlays into one page per scene:** turned down by the user (see Context). It would need layout tools in StreamGremlin, and "show or hide a widget" steps to replace OBS's own visibility.
- **Waiting for each alert to finish before the step returns (StreamWisp):** a burst of follows would hold each run for the whole queue. Waiting until it starts tells the trace what it needs, and the queue keeps them in order.
- **Overlays on the API's listener with a scoped token:** it mixes credentials on one listener, and a route-scoping bug would expose the API to a browser source.
- **A token in a query string or cookie:** OBS handles cookies awkwardly, and a path token with `no-referrer` does the same job.
- **Server-Sent Events:** one-way, so answers would need a second channel.
- **A Svelte-built page:** a second build for a few hundred lines; plain files keep the CSP strict, and the app previews the same page OBS shows.
- **The token in the database:** backups would carry working links.

## Consequences

- **OBS setup:** each overlay is one browser source, sized 1920×1080 (the page scales with it). Copy the link from the Overlays screen.
- **Backups** carry overlays and their looks, but not their links: after restoring on another computer, make a new link and paste it into OBS.
- **The engine serves a second port.** Another program on 7431 moves overlays to another port until restart, and the app says so.
- **Not built:** other overlay kinds (counters, goals, the F3 giveaway overlay), theme packs, and overlays on another computer (they would need authenticated remote hosting).
- **Confirmed on real systems** (the live check, 2026-11-09, macOS and Windows): OBS's browser source, the app's preview frame in WKWebView and WebView2, and Twitch's real profile pictures.
