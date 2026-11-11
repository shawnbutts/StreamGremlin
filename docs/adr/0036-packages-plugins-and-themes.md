# ADR-0036: Packages: what plugins and overlay themes own, and installing, updating and removing them

- **Status:** Accepted
- **Date:** 2026-11-10
- **Based on:** D-062 and D-063 (the user's requests); StreamWisp's ADR-0041 (plugins), ADR-0067 (Lua plugins), D-094, D-106, D-107 and D-111 (what plugins own, decided there only after something broke); the overlay theme concept in `tmp/streamgremlin-theme-starter` (its protocol and packaging notes).

> **Amended by D-066 (2026-11-10), slice 3a:**
> - the manifest names its format in a `format` field (`"streamgremlin.package/v1"`); versions have no build metadata (`+…`), so a version is one path segment;
> - a ZIP made by compressing a package's folder (everything in one folder, the manifest inside it) is read as that folder; Finder's `__MACOSX/` and `.DS_Store` are skipped;
> - the built-in looks' names (`classic`, `workshop`, `neon-arcade`, `selector-board`) are reserved ids too, as the CLI takes them as they are;
> - installing asks with the check's digest: `POST /v1/packages?digest=` installs only the package that was shown, sent again;
> - updating and rolling back come in slice 3b; until then, installing another version of an installed id is refused, saying to remove it first; the publisher-change confirmation comes with updates, as only an installed id can have one;
> - settings: number settings are whole numbers; a theme with `start.wav`, `end.wav` or `cheer.wav` beside its page gets StreamGremlin's "Play the look's sounds" setting (D-065), so a manifest can't name a setting `sounds`;
> - `package_settings` is created with Lua plugins (slice 5), which have one set each; a theme's settings are each overlay's, in its look;
> - an installed theme's files are served only while that version is the one installed; the theme server checks the id, version and digest against what's installed;
> - the Add-ons screen and `streamgremlin packages [install FILE [-yes] | remove ID [-yes] | history]`; `overlays look NAME -look ID -set KEY=VALUE`; `GET /v1/packages/audit` shows the audit.

> **Amended by D-068 (2026-11-11), slice 3b:**
> - installing another version of an installed id updates it (`POST /v1/packages`, answering `from` and, per overlay, the settings reset); a version already kept is reused, one kept with other files refused;
> - kept: two versions besides the installed one, the highest below it first, then the lowest above it; **Roll back** (`POST /v1/packages/{id}/rollback`, `packages rollback ID`) goes to the highest kept below the installed one, with that version's publisher, keeping the one rolled back from;
> - another publisher's version: `?publisher=` with the new publisher's name, exactly, or 409 `confirm_publisher`; the app asks for it typed, the CLI for `-publisher NAME`;
> - overlays fit each new version: settings it still takes are kept, the rest reset and named; a spin on screen finishes in the old version;
> - a look is told the drawn result but draws what its maker chose: the wording "it only shows the result StreamGremlin drew" is replaced, and the screens say the real result is always in chat;
> - the hostile test look, `tests/testdata/hostile-wheel`, is installed and served by the acceptance tests and driven against the overlay page in the unit tests.

> **Amended by D-067 (2026-11-11):** OBS's page API (`window.obsstudio`) reaches a look's sandboxed frame; the live check showed it. A look reports the level OBS gives its frame (`obsLevel` in `theme.ready`, from `getControlLevel`). The overlay page asks OBS its own level and frames an add-on look (`add_on` in the hello, or any link off the built-in path) only at level 0, "No access to OBS"; at a higher level, an unknown one, or while asking, Classic shows, and why is reported once. Built-in looks load at any level. See "OBS's own page API" below.

> **Amended by D-065 (2026-11-10):** looks can have their own sounds, played by the overlay page; a theme's policy now has `media-src 'none'`. See "Sounds play in the overlay page" below.

## Context

M5 brings code that StreamGremlin didn't write: Lua plugins, and overlay themes that draw the wheel (D-062). StreamWisp settled what a plugin owns one surprise at a time: shipped workflows (D-106), what uninstalling does (D-107), and who answers a command (D-094, D-111). LESSONS-LEARNED item 11 says to decide all of it first. That's this ADR. It covers both kinds, because they share installing, identity, versions, settings and removal, and differ only in how they run.

AGENTS.md:
- plugins are untrusted: separate processes, time and memory limits, approved permissions, audited calls;
- secret tokens never appear anywhere a package can reach;
- winners are chosen on the server; never imply odds are fair unless verified;
- an overlay alone is never enough: chat carries everything.

## Decision

### A package

One ZIP with a manifest, `streamgremlin.json`, at its root. Two kinds:

| Kind | Runs | Built in |
|---|---|---|
| `theme` | in the OBS browser source (and the app's previews), in a sandboxed frame with no network | M5 slices 2 and 3 |
| `lua` | in a separate StreamGremlin worker process, in the Lua sandbox | M5 slice 5 (its own ADR for the runtime) |

Program plugins (any language) and the Go SDK stay Later (FEATURES).

**The manifest** (`streamgremlin.package/v1`, strict: unknown fields are refused):
- `id`: 3 to 40 characters, lowercase letters, digits and `-`, starting with a letter. Not one of StreamGremlin's own namespaces (`chat`, `obs`, `twitch`, `points`, `wheels`, `giveaways`, `workflow`, `builtin`, and the rest of the action groups). It names the package's actions and events (`<id>.<name>`), so it never changes.
- `name`, `version` (semver), `publisher`, optional `description`, `license` and `homepage`.
- `kind` and `api` (`"1"`). A package for an API this build doesn't have is refused, naming both.
- For a theme: `theme.for` (`["wheel"]` for now; giveaways can be added later), `theme.entry` (an HTML file in the package), `theme.settings` (below).
- For a Lua plugin: its script, permissions, actions and events, defined in slice 5's ADR.

**The publisher is as stated, not verified.** There's no signing. The app always says "published by X (not verified)".

### Reading a ZIP safely

Before anything is stored:
- the ZIP is at most 16 MiB, its files at most 32 MiB unpacked, at most 200 files, each at most 8 MiB, each no more than 100 times its packed size;
- only stored or deflated entries;
- paths are relative, at most 200 characters, of letters, digits, `.`, `_`, `-` and `/`; no `..`, no leading `/`, no backslashes, no drive letters, no NULs;
- regular files only: no symlinks, no devices; folders are implied;
- no two paths equal ignoring case (macOS and Windows would merge them);
- `__MACOSX/` and `.DS_Store` are skipped, as Finder adds them;
- the manifest is at most 64 KiB;
- for a theme, only these types: `.html`, `.js`, `.css`, `.json`, `.svg`, `.png`, `.jpg`/`.jpeg`, `.webp`, `.gif`, `.woff2`, `.woff`, `.ttf`, `.otf`, `.mp3`, `.ogg`, `.wav`, `.txt`, `.md`;
- nothing in the package runs, compiles or is fetched while installing.

A refusal names the first problem and stores nothing.

### Stored in the database

Each installed version's files go in the database with their SHA-256, as sounds do (ADR-0016). So:
- backups carry packages with no change to the backup format (ADR-0012);
- there's no folder on disk for something else to change: what runs is what was checked. A Lua worker gets its script from the engine and checks its digest before loading it.

A version's digest is the SHA-256 of its sorted paths and file digests. **An id and version are one fixed thing:** installing the same version with a different digest is refused ("this is a different package with the same version: ask its publisher for a new version").

### Installing

1. The app reads the ZIP, as for sounds, and the engine checks it (above). Nothing is stored yet.
2. The app shows what it is and asks:
   - its name, version and kind, and "published by X (not verified)";
   - for a theme: "This look runs its own code in OBS and the app's previews. It can't reach the internet, your accounts or StreamGremlin's settings. It's told the result StreamGremlin drew, but what it draws is up to whoever made it: the real result is always in chat." (D-068: an earlier wording, "it only shows the result StreamGremlin drew", claimed what can't be checked);
   - for a Lua plugin: each permission it asks for, ticked one by one, and the workflows it offers (D-106, below).
3. Confirmed, it's stored and audited.

**Another publisher's id:** installing a package whose id is installed from a different publisher is refused unless the streamer confirms replacing that publisher, by name.

**Installed, nothing changes yet.** A theme is used only when an overlay is set to it. A Lua plugin is installed off.

### What a package owns

| | Theme | Lua plugin |
|---|---|---|
| Files | its version's files, served only to the sandbox | its script, given only to its worker |
| Settings | one set per overlay using it (two wheel overlays can show different titles) | one set for the plugin |
| Actions and events | none | `<id>.<action>` steps and `<id>.<event>` events (source `plugin:<id>`) |
| Workflows | none | the ones the streamer added from its `workflows/` folder, recorded as coming from it |
| Chat commands | none | only those its own switched-on workflows answer (D-111), under one answer per command (ADR-0011) |
| Points | none | rounds it holds in escrow, refunded when it stops (slice 6's ADR) |
| Audit | install, update, rollback, removal, refusals | the same, plus its calls (slice 5) |

**Workflows from a plugin are offered, never added unasked** (StreamWisp D-106): after installing, the streamer ticks which to add. Ones that use only the plugin's own actions start ticked; one that uses other actions names them and starts unticked. Each added one is saved like an import, recorded with `from_package`, and turned on by itself; one whose command is taken is saved off, saying why. StreamWisp couldn't tell which workflows came from a plugin; StreamGremlin records it.

### Settings

A manifest's settings are typed: `text` (up to `max`, at most 200 characters), `number` (`min`, `max`), `switch`, `colour` (`#RRGGBB`), `choice` (from a list), each with a label and a default. The engine checks every value; a package never sees a value outside its own schema. Settings never hold secrets: a setting named like one (`token`, `password`, `key`, `secret`) is refused in the manifest.

**On update,** a value whose setting still exists with the same type, and is still valid, is kept; the rest take the new default, and the update names them.

### Updating and rolling back

- **An update is installing a newer or different version of an installed id,** with the same checks and the same publisher (or a confirmed change).
- **It asks again if it asks for more:** new permissions, or (for a plugin) removing actions that the streamer's workflows use. Those workflows are named before confirming and turned off after.
- **Overlays and the plugin switch to the new version together,** when it's stored. An overlay showing a spin finishes that spin first.
- **The two previous versions are kept,** so **Roll back** returns to the one before, with the same checks in reverse. Older ones are removed.

### Turning off and removing

- **Turning a plugin off** stops its worker. Its steps fail at once, saying it's off. Its workflows stay on (StreamWisp D-107: they work again when it's back). Its points rounds are refunded.
- **Removing a package** asks first, naming what it affects:
  - a theme: every overlay using it goes back to the Classic look, so OBS never shows a blank wheel;
  - a plugin: every workflow using its actions is turned off, not deleted, and named (StreamWisp D-107), whoever added it; rounds are refunded;
  - its files, versions and settings are deleted; its audit stays.

### How a theme runs

**The overlay page stays StreamGremlin's own** (`/o/<token>/`). For a themed wheel overlay it embeds the theme and talks to it; the theme never talks to the engine.

- **A second origin.** The overlay server listens on a second loopback port (any free one; the page is told it in its hello) that serves only theme files, at `/t/<id>/<version>/<digest>/<path>` (built-in looks at `/t/builtin/<page version>/<look>/<file>`, with the files they share in `shared/`), and nothing else: no overlay links, no WebSocket, no sounds. The same loopback Host check as overlays. Every answer also carries `sandbox allow-scripts` in its policy, so a look opened outside its frame is sandboxed too, and `Access-Control-Allow-Origin: *`, because a sandboxed page's own scripts and fonts are cross-origin requests (nothing there is private).
- **A sandboxed frame:** `<iframe sandbox="allow-scripts" referrerpolicy="no-referrer">`. Without `allow-same-origin` the theme gets an opaque origin: no cookies or storage, and it can't read the overlay page. No popups, forms, top navigation, downloads or modals. No referrer, so the theme never learns the overlay's private link.
- **Its policy:** `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; media-src 'none'; connect-src 'none'; frame-src 'none'; worker-src 'none'; base-uri 'none'; form-action 'none'`, `frame-ancestors` the overlay origin and the app's, `X-Content-Type-Options: nosniff`, and each file's type from its extension. No network: a theme can't fetch, open a WebSocket or load anything from outside its own package.
- **Messages** are the concept's protocol (`protocolVersion` 1), adapted:
  - **to the theme:** `session.snapshot` (title, choices with label, weight, colour and exact chance as text; the theme's settings; reduced motion), `entries.updated`, `spin.start` (`spinId`, the frozen choices, the winner's index, the drawn number, how long to spin, from the wheel), `spin.cancel`, `theme.settings.updated`, `winner.reveal`;
  - **from the theme:** only `theme.ready`, `spin.animation.completed` and `theme.error`, from the frame's own window (`event.source`), at most 16 KiB and 20 a second, checked against a schema; anything else is dropped and logged;
  - the host posts with target `*`, since the theme's origin is opaque; nothing it sends is secret: it's what the stream shows.
- **The engine stays in charge** (ADR-0030):
  - the spin settles on the engine's clock, never on the theme's word;
  - `spin.animation.completed` is logged, never acted on;
  - chat's announcement doesn't wait on the theme.
- **A broken theme never leaves the stream blank.** A spin that arrives before the theme says it's ready is shown in Classic. A theme that reports an error for the spin on screen is hidden, and Classic shows that spin stopped at once. A theme not ready within 5 seconds is reported. The app's preview (and OBS with `?status=1`) says which, and the engine logs it.
- **Sounds play in the overlay page,** not the theme (ADR-0031), so they stay in time with the engine. A theme may ship its own sounds for the three cues, as `start.wav`, `end.wav` and `cheer.wav` beside its page (D-065): the hello lists their links on the theme server when the theme's **sounds** setting is on, and the overlay page plays one for any cue the streamer left without a sound, at that cue's volume. The overlay page's policy allows `media-src` from `http://127.0.0.1:*` for this, and plays only `.wav` links on the theme server. The theme's own policy has `media-src 'none'`: a theme can't play sound itself, so the streamer's volumes and the preview's silence hold.
- **OBS's own page API.** OBS's browser source gives pages `window.obsstudio`, with more power as the source's **Page permissions** setting rises ([obs-browser](https://github.com/obsproject/obs-browser)): level 0 "No access" offers only `getControlLevel`; up to level 5, "Full access", which starts and stops the stream. Its docs don't say whether a sandboxed frame gets it; the live check (D-067) showed it does. OBS's source (`browser-app.cpp`) gives every frame's context the object, sends each call with no note of its frame, checks it against the source's level, and runs the answer in the main frame, so a look's calls act with the source's level though it gets no answers (as the live check found). An add-on look is framed only at level 0: the overlay page asks `getControlLevel` first, and shows Classic at any other level, an unknown one, or while waiting (2 seconds at most). The built-in looks never call it, and load at any level. StreamGremlin's own pages need no OBS access: OBS is driven through obs-websocket.

### Odds

StreamGremlin can't check how an installed theme draws a wheel: it could draw equal wedges for unequal odds.
- **The built-in looks draw wedges to their real size,** checked by tests, and say so.
- **An installed theme is labelled** "this look's drawing isn't checked: the real odds are on the Wheels screen and in “Say a wheel's odds”". A manifest can't claim otherwise.
- The real chances are always sent to the theme, so an honest theme can show them.

### Built-in looks

Workshop, Neon arcade and Selector board are built into StreamGremlin, rebuilt in its own code (D-048), and run through the same sandbox and protocol as installed themes, so the sandbox is exercised on every stream that uses them. Classic (today's wheel) stays drawn by the overlay page itself, because it's the fallback when a theme fails. None of them are packages: they can't be removed or replaced, and their ids (`builtin:…`) can't be taken.

### Screens, API and CLI (built in the slices that need them)

- **An Add-ons screen:** installed themes and plugins with version, publisher (not verified), kind, on or off; Install, Update, Roll back, Remove (each asks first); for plugins, the workflows it offers.
- **The Overlays screen** picks a wheel overlay's look and its settings, with the label above.
- **API:** `packages.read` and `packages.write`; `GET /v1/packages`, `POST /v1/packages/check` (reads a ZIP, stores nothing), `POST /v1/packages` (installs what was checked, with the confirmations), `POST /v1/packages/{id}/rollback`, `DELETE /v1/packages/{id}`.
- **CLI:** `streamgremlin packages`, `packages install FILE [-yes]` (shows what it asks without `-yes`), `packages rollback ID`, `packages remove ID [-yes]`.
- **Audit:** `package_audit`, append-only by triggers, kept when a package is removed.

## Alternatives considered

- **Packages as folders on disk:** another program could change them after they were checked, and backups would need a new format. The database already carries sounds.
- **Theme files from the overlay's own origin, with the sandbox alone:** a mistake in the sandbox attributes would put the theme in the same origin as the overlay page and its WebSocket. A second port keeps them apart even then.
- **Letting a theme decide when a spin has ended:** a hung or hostile theme could hold a result. The engine's clock decides (ADR-0030).
- **Declarative-only themes (CSS and SVG, no scripts):** safer, but they can't animate to a result with a variable number of wedges. The sandbox makes scripts acceptable; a declarative kind can come later.
- **Trusting a manifest's "draws real odds":** it can't be checked.
- **Signed packages:** there's no publisher infrastructure yet. "Not verified" is honest; signing can come with a package library (Later).
- **Removing workflows with their plugin:** the streamer may have changed them; turning off can be undone (StreamWisp D-107).

## Consequences

- One install path, one store and one audit for both kinds; slices 3 and 5 build on them.
- A theme is limited to what a sandboxed page with no network can do. It can't use web fonts or images from the internet; everything ships in the package.
- Migration (slice 3): `packages`, `package_versions`, `package_files`, `package_settings`, `package_audit`; slice 2 adds the overlay's look without new package tables.
- **Checked in slice 2 and 3a's live checks:** OBS's browser source runs the sandboxed frame with this policy on macOS and Windows; `window.obsstudio` reaches it (D-067).
