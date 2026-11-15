# Decision log

Each entry records what was decided, when, and the user's own words. Kickoff discussions are listed in [KICKOFF.md](../KICKOFF.md).

## D-001. Audience: Streamer.bot switchers and streamers with no bot yet (K-1)

Decided 2026-11-07.

> "A" mostly. But we also have to concider streamers with no bot yet. So the onboarding and usage should be as simple as possible. While the "import from streamer.bot" features need to be there too.

**Decision:** the first release is for Twitch streamers in two groups:

1. **Streamers switching from Streamer.bot.** Import from Streamer.bot is part of the product, and features are studied against how Streamer.bot users expect them to work.
2. **Streamers with no bot yet.** Onboarding and everyday use must be as simple as possible. They shouldn't need to know Streamer.bot, workflows or OBS internals to get started.

The user's own stream is the first test bed (option A).

**Consequences:**

- Every feature has to work for both groups. Familiar to switchers is not enough if a new streamer can't use it without help.
- First-run setup, the Get set up checklist and the template gallery are judged by whether a streamer with no bot can get a working setup quickly.
- Import is a way in for switchers, not something a new streamer ever needs to see.

**Still open (to answer before K-4):**

- The user runs Streamer.bot on their stream today ("I run streamer.bot now."). Can a real export and event logs be shared? They stay out of the repository unless the user says otherwise.
- Which other streamers will try it in the first month, and which of them have no bot yet?
- Is anyone deliberately excluded, for example setups that depend on C# actions or StreamUP?

## D-002. Name: StreamGremlin (K-2, in progress)

Started 2026-11-07.

> i did register streamgremlin.com. when we are out of beta, the code will be at https://github.com/shawnbutts/StreamGremlin.

**Decided so far:**

- The name is **StreamGremlin**.
- The domain `streamgremlin.com` is registered and owned by the user.
- During beta the code lives on a private server. After beta it moves to `github.com/shawnbutts/StreamGremlin`.

> yes use the vanity path and those names for the CLI etc. For switch, https://www.twitch.tv/StreamGremlin seems to be taken. I did get https://www.twitch.tv/StreamGremlinBot.

- **Go module path:** `code.streamgremlin.com/streamgremlin`, a vanity path served from our domain, so the move from the private server to GitHub doesn't change any import.
- **Derived names:**

  | Item | Name |
  |---|---|
  | CLI and server binary | `streamgremlin` |
  | Desktop app | `StreamGremlin` |
  | Data folder | `StreamGremlin` on macOS and Windows, `streamgremlin` on Linux (XDG) |
  | Environment variable prefix | `STREAMGREMLIN_` |
  | Format names | `streamgremlin.<kind>/v1`, for example `streamgremlin.workflow/v1` |

  They all come from one constant.
- **Twitch:** `StreamGremlin` is taken. The user registered `StreamGremlinBot` and `StreamGremlinHQ`.

> i have StreamGremlinHQ now as well.

  - `StreamGremlinHQ` is the owner account. It registers the Twitch app (the built-in Client ID, K-5) and can post official announcements later. It has two-factor authentication, and is never signed in to the app or kept on a streaming machine.
  - `StreamGremlinBot` is the bot for the user's stream and for demos, screenshots and docs. It isn't a shared bot: every streamer signs in their own bot account locally, because a bot's token never leaves the machine it belongs to.

**Still open:**

- Trademark search, and checking the name in the stores we may ship to (K-5).

## D-003. Platforms: Twitch and YouTube; Windows, macOS and Linux desktop (K-3)

Decided 2026-11-07.

> 1) Twitch & Youtube. 2) Windows, MacOS, Linux. 3) Don't build headless. But, try to keep Headless Linux in mind as a possible option in the future

**Decision:**

- **Streaming platforms:** Twitch and YouTube. OBS (obs-websocket 5) stays as the broadcasting software.
- **Operating systems:** desktop app on Windows, macOS and Linux.
- **Headless server:** not built. The engine stays reachable only through the authenticated API, with no Wails-only logic, so a headless Linux build stays possible later.

**Consequences:**

- Every platform-specific capability, scope, limit and quota goes in that adapter's manifest. YouTube's daily API quota is a design limit, not a footnote. Check the current official docs before building it.
- Features that exist on only one platform, such as Twitch channel points, raids or polls, say so in the app, and are never faked on the other.
- Under the definition of done, a milestone is checked live on each supported OS, and on each platform the milestone touches.

> 1) a later milestone is fine. not sure who will do the yt stream tests. 2) I stream on MacOS. I have a Windows system for testing. lightly tested for linux is good for now

- **YouTube** gets its own milestone, after the Twitch spine and the switch features.
- **Live checks:**
  - **macOS:** the user's streaming machine. Live checks every milestone.
  - **Windows:** the user's test machine. Live checks every milestone.
  - **Linux:** "lightly tested". It builds and passes CI, and gets a smoke test before a release. No live stream check until a Linux streamer is involved.

**Still open:**

- Who will test YouTube on a real stream? The YouTube milestone can't be marked done until someone does.

## D-004. Feature cut (K-4)

Decided 2026-11-07. The full cut, with the milestone each row belongs to, is in [FEATURES.md](FEATURES.md).

### The switch features

> A, B, C, and D in that order. D is mostly so it is a native feature so plugins and just use it instead having to build such a thing themselves.

These come after the spine, built in this order:

1. **Chat commands, timers, counters and quotes.**
2. **Event → OBS / sound / alert automation:** channel point redeems, follows, subs, raids and bits switch scenes, show sources, play sounds and show alert overlays.
3. **Giveaways** with fair, checkable draws.
4. **Points ledger, currencies and rewards shop.** It's native so that plugins can use it instead of each building their own.

### How the cut is decided

> i'd rather do the discussions than provide and export. there is a lot testing and other nonsense in it that doesn't need to be worried about

The cut is settled by discussion, not from the user's Streamer.bot export. The Streamer.bot import milestone still needs real exports to test against (lesson 3). Where they come from is decided when that milestone starts.

### Engine and core

> agree with section 1.

| Feature | Cut |
|---|---|
| Event inbox, with deduplication and replay | Carry |
| Workflows: trigger → steps, queues, timeouts | Carry |
| Run another workflow | Carry |
| Traces and runs | Carry |
| Simulate and dry run | Carry |
| Plain-words When/Then editor | Carry |
| Secrets, in the keyring or an encrypted file | Carry |
| Local API and CLI | Carry |
| Headless server | Not built (D-003). The engine boundary is kept. |
| Backups and restore | Carry |
| Configuration export/import | Later |
| Delete and restore workflows | Carry |
| Chat command ownership | Carry |
| Persistent variables: global, per-viewer, per-workflow | Carry, designed in the spine |

### Desktop app

> agree with section 2

| Feature | Cut |
|---|---|
| Wails v3 + Svelte 5 shell, reaching the engine only through the API | Carry |
| Home: health, live events, Get set up checklist | Carry. The main onboarding path. |
| Settings: hide screens and connections you don't use | Carry |
| Help screen with built-in user docs | Carry |
| In-app ask dialogs, never `window.confirm` | Carry (rule) |
| Native save dialog for exports | Carry (rule) |
| Setup links open in the system browser, allowlisted | Carry |
| Accessibility: axe checks in all themes and text sizes, contrast tests | Carry |
| Twitch → OBS command wizard | Leave. The checklist and templates take over its job. |
| Gallery of templates | Carry, and expand: ready-made templates for each switch feature |
| Localization | Leave. English only. |

### Twitch

> agree with section 3, option 1

| Feature | Cut |
|---|---|
| Device-code sign-in, refresh, validation | Carry |
| Built-in Client ID in release builds (registered under `StreamGremlinHQ`) | Carry |
| Chat read (EventSub) and send (Helix) | Carry |
| Separate bot account for chat (optional) | Carry |
| Follows, subs, bits, channel point redeems, chatters | Carry |
| Raids, Hype Trains, polls, predictions (events, and starting them) | Carry |
| First Words | Carry |
| Profile pictures for overlays | Carry |
| Stream category for matching content packs | Decided with games |
| Ads | Decided in the Ads section |
| **New:** managing channel point rewards from the app (create, pause, change cost, fulfil or refund redemptions) | Carry, limited to rewards StreamGremlin creates. Twitch only lets an app manage rewards made with its own Client ID. Other rewards are labelled "trigger only", with an offer to create a managed copy. The rule goes in the adapter's capability manifest, checked against current Twitch docs before building. Who uses it: the user's stream, and switchers used to refund-on-failure. |

### OBS

> agree with section 4

| Feature | Cut |
|---|---|
| obs-websocket 5 connection, scene switch, source visibility | Carry |
| Scene snapshots, restored only while StreamGremlin still owns the scene | Carry |
| Filters, mute, text, media, recording, replay buffer, screenshots | Carry |
| Random color for color sources | Carry |

### Viewers and points

> agree with section 5

| Feature | Cut |
|---|---|
| Viewer records per platform account, with export and delete | Carry |
| Points ledger: append-only, double entry, no negative balances | Carry |
| Several currencies, rename, retire | Carry |
| Convert between currencies | Later |
| Earning rules: chat, follows, subs, bits, time in chat | Carry. "Time in chat", not "watch time": Twitch reports who is connected to chat, not who is watching. |
| Rewards shop and redemption queue | Carry |
| Counters and quotes | Carry |
| Account links proven in chat (`!link`) | Later, with the YouTube milestone |

### Engagement

> i agree

| Feature | Cut |
|---|---|
| Timers: timed actions, cron schedules | Carry |
| Alert overlays with slide or pop | Carry |
| Giveaways: equal entries, fingerprinted entry lists, checkable draws | Carry |
| Giveaway entry rules | Carry |
| Giveaway extra chances, announced in chat | Carry |
| Giveaway draw from recent chat | Carry |
| Giveaway overlay (never required: chat carries everything) | Carry |
| Wheels with audited server-side draws | Later. Reuses the giveaway draw and audit code. |
| Paid wheel spins | Leave. Added to the won't-do list: viewers never pay to play. |

### Games and content packs

> agree with section 7.

| Feature | Cut |
|---|---|
| Game runtime | Later |
| Trivia, word scramble, guess the number | Later |
| Community challenges | Later |
| Content packs | Later |
| Stream category for matching content packs | Later, with packs |
| Category-matched packs and games during breaks | Later |
| Pack catalogs | Leave |

Plugin games don't depend on the game runtime. They use the points API, which is settled under "Extending it".

### Ads and breaks

> agree with section 8

| Feature | Cut |
|---|---|
| "Ad break begins" trigger (with the announced length); get ad schedule, snooze next ad and start commercial steps; a "next ad / snooze" card on Home | Carry |
| Ad requests and confirmed breaks recorded separately | Carry |
| Intermissions (ad, BRB, technical) | Later |
| Ad policies: manual or suggest, limits, emergency stop | Later |
| Ad Break Director screen and banner | Later |

Twitch sends no event when an ad ends. A workflow may wait for the length Twitch announced, and the step is labelled that way. The app never shows "ad ended" as confirmed.

### Other platforms

> agree with section 9

| Feature | Cut |
|---|---|
| YouTube: sign-in, live chat, Super Chats, memberships | Later: the YouTube milestone |
| YouTube: cuepoints and retention | Later, decided at the YouTube milestone |
| Kick | Leave |
| Chat router across platforms | Later: the YouTube milestone. The single chat interface and `source` on events exist from the spine. |

### Extending it

> agree and option 2

| Feature | Cut |
|---|---|
| Lua scripts in supervised worker processes | Carry, after the four switch features |
| Plugins as separate processes | Carry, after Lua |
| Lua plugins | Carry: the first plugin kind |
| Go plugin SDK | Later |
| Plugins shipping workflows; uninstall turns them off | Carry, with plugins. Plugin ownership, install, update and uninstall are designed (as an ADR) before any plugin code. |
| Points API for plugins | Carry, with plugins. Option 2: see below. |
| Streamer.bot WebSocket gateway | Leave |
| Parity registry | Leave |

**Points API for plugins (option 2):**

- Plugins can award points, charge points and refund them.
- A plugin can also hold viewers' points in escrow while a round runs, then pay them out or refund them. Every round is recorded in the ledger.
- Odds are always shown as "stated by the plugin", and never called fair.
- The streamer approves the points permission when installing the plugin.
- StreamGremlin itself never runs a wager.

### Moving from Streamer.bot

> agreed

| Feature | Cut |
|---|---|
| Import of export strings, converting only what converts exactly; imports start disabled | Carry |
| Type mappings for triggers, sub-actions and OBS states, limited to this feature cut; everything else listed in the report | Carry |
| Import report with a C# inventory, pointing to Lua or templates | Carry |
| Live inventory over Streamer.bot's WebSocket | Later |
| Running or converting C# | Leave (won't do) |

### Desktop agent

> agree and option A

| Feature | Cut |
|---|---|
| Sounds, played by the desktop app (option A), with volume per sound. The engine decides; the app plays. OBS media sources stay available as a step. | Carry: feature 2 |
| Text to speech | Later |
| Global hotkeys | Later |
| MIDI, window events, clipboard | Later |

Choosing the audio output device on each OS is checked before building.

### AI and releasing

> agreed

| Feature | Cut |
|---|---|
| AI providers, spending ledger, chat personality | Leave |
| Release builds: macOS dmg, Windows installer, a Linux package, reproducible archives, SBOMs | Carry |
| OCI container image | Leave, while there's no headless server |
| Code signing and notarization | Decided in K-5 |
| Upgrade and install tests | Carry |
| Website built from the Help docs, on streamgremlin.com | Carry |
| Fuzzing, load tests, route guard sweep | Carry |

## D-005. Outside accounts and costs (K-5)

Decided 2026-11-07.

> agree with all, Apache-2.0 and DCO

1. **Twitch app:** registered now in the Twitch developer console, under `StreamGremlinHQ`. Name "StreamGremlin", client type Public (device-code sign-in, no client secret).
   - The redirect URL the console requires is `http://localhost` for now. It's confirmed against current Twitch docs when sign-in is built.
     **Update 2026-11-08:** the console refuses it: "Redirect URIs must use HTTPS protocol." Use `https://localhost`. The device code flow never uses it. Client type: **Public** (ADR-0005).
   - The Client ID is never committed. Release builds get it from a CI secret; dev builds use paste-your-own.
2. **Code signing:**
   - **macOS:** join the Apple Developer Program ($99 a year) for signing and notarization before anyone other than the user installs the app.
   - **Windows:** unsigned during beta, with instructions for the SmartScreen warning, while the user is the only tester. Signed before outside testers; current eligibility and prices are checked at that point.
   - **Linux:** signed checksums.
3. **Trademark:** the user does a free USPTO and EUIPO search for "StreamGremlin" now. No registration yet. No app store name checks, because releases are direct downloads.
4. **Website:** static hosting on `streamgremlin.com`, built from the Help docs. It also serves the `go-import` page for the `code.streamgremlin.com/streamgremlin` vanity path, needed by beta. Which host to use is still open: the user's own server, or a free static host.
5. **Security and support:** a `security@streamgremlin.com` address and a `SECURITY.md`. Support goes through the code host's issues (a private server during beta, GitHub after). A Discord or similar only once there are users.
6. **License and contributions:** Apache-2.0 (`LICENSE`). Contributions use the DCO: every commit carries a `Signed-off-by` line.

**Tasks for the user (outside the code):**

- [ ] Register the Twitch app under `StreamGremlinHQ`, with two-factor authentication on.
- [ ] USPTO and EUIPO trademark search.
- [ ] Create the `security@streamgremlin.com` address.
- [ ] Join the Apple Developer Program before the first outside tester.
- [ ] Choose the website host.

## D-006. Milestones (K-6)

Decided 2026-11-07.

> i agree with your recommendation

| Milestone | Contents |
|---|---|
| **M1. Spine** | Engine, Twitch sign-in and chat, OBS, workflows with the editor, traces, persistent variables, the desktop app on macOS and Windows |
| **M2. Everyday bot** | F1, chat commands, timers, counters and quotes; then F2, event → OBS / sound / alert automation |
| **M3. Giveaways and points** | F3, giveaways; then F4, the points ledger, currencies and rewards shop |
| **M4. Streamer.bot import** | Checked against real exports. Afterwards, the first outside beta, which needs macOS signing (D-005). |
| **M5. Lua & plugins** | Lua scripts, then Lua plugins with the points API, with plugin ownership designed first |
| **M6. YouTube** | Needs a named YouTube tester (D-003) |

- Within a milestone, features are built in the D-004 order, as thin slices, each with its screen.
- A milestone is done when the user has used it on a real stream on macOS and Windows and the result is written down. Linux gets a smoke test before a release (D-003).

## D-007. How we work (K-7)

Decided 2026-11-07.

> i agree

- **Git:** each slice goes on its own branch, is fast-forwarded onto main and pushed, with no merge commits. Push freely while the repo is internal on a private server. Every commit is signed off (DCO, D-005).
- **Records:** every decision goes in this log with the user's words. ADRs only for storage, protocols, file formats and security.
- **Live checks:** at the end of each milestone, on macOS and Windows, and whenever a slice first touches a real service.
- **Findings:** the user reports in the session or as an issue on the private server. Each live check is written up in `docs/milestones/M<n>.md`: what was tried, on which OS, and what broke.
- **StreamWisp data:** a fresh start. No StreamWisp importer, and StreamWisp's data folder is left alone.
- **Asking:** the user is asked when a decision is irreversible, business-critical or changes the feature cut. Otherwise the simplest secure, testable design is chosen and recorded here.

The kickoff (K-1 to K-7) is complete. Next: M1, the Spine.

## D-008. Choices made while building M1 slice 1 (skeleton)

Decided 2026-11-07, without asking the user, under D-007 ("otherwise choose the simplest secure, testable design and record it").

- **Storage** follows [ADR-0001](adr/0001-storage.md): one baseline migration until the first public release, no down migrations, and a backup before every schema change.
- **The local API and the desktop bridge** follow [ADR-0002](adr/0002-local-api.md).
- **Default API port 7430,** not StreamWisp's 7420, so both can run on one machine. A free port is used if 7430 is taken.
- **`streamgremlin engine`** runs the engine without a window, for development and acceptance tests. It isn't a supported server and isn't documented for streamers. D-003's "don't build headless" still holds; this only keeps the boundary tested.
- **Wails v3.0.0,** the first stable v3 release (2026-10-26). It's 12 days old, two days short of the two-week rule for new versions; it was chosen over the beta builds StreamWisp tracked (StreamWisp D-020). v3.1.0 (2026-11-01) is newer still, so it waits.
- **macOS 13 is the minimum,** because Wails v3's own objects are built for it. The desktop build passes `-mmacosx-version-min=13.0`.
- **Node 22 or 24** for the desktop UI. `make` uses Homebrew's `node@24` when the default `node` is another version, and the UI's `package.json` refuses Node 26 (the npm trap in ARCHITECTURE.md).
- **Theme setting:** System (the default, following the OS), Dark workshop, Light workshop or Midnight blue, from the brand kit. It's stored through the API (`/v1/settings/ui`), so slice 1 has a real write path to test.
- **Opening the app twice** brings the first window forward (Wails single-instance). If anything else holds the data folder, the app explains that instead of starting a second engine.
- **CI:** none, until much later. The user, 2026-11-08: "let's leave CI until much later." `make verify` runs locally before every push. Until CI exists, D-003's "Linux builds and passes CI" means the Linux build cross-compiles and `make verify` is run by hand on Linux before a release.

## D-009. Choices made while building M1 slice 2 (secrets)

Decided 2026-11-08, without asking the user, under D-007.

- **Secrets** follow [ADR-0003](adr/0003-secrets.md):
  - the OS keyring, or an AES-256-GCM file when the keyring doesn't answer on first start;
  - the store is chosen once per data folder, then fixed;
  - the database holds only an index of keys, never values.
- **`zalando/go-keyring` v0.2.8** (2026-03-23) for the keyring. It's pure Go, so the CLI and a future headless build stay cgo-free.
- **No API route returns or takes a secret value.** The app and CLI can list, check and forget secrets. Saving happens only inside the engine, in the slices that need it: Twitch sign-in (slice 4) and OBS (slice 6).
- **The screen is a "Passwords and sign-ins" section in Settings,** not a screen of its own. Until slice 4 it shows where secrets would be kept, and an empty list.
- **Forgetting asks first,** in the app's own dialog (never `window.confirm`), with focus on Cancel.
- **The baseline schema was rewritten** to add the `secret_store` and `secrets` tables (ADR-0001). A data folder from slice 1 fails the checksum check and has to be moved aside. It held only the theme setting.

## D-010. Choices made while building M1 slice 3 (events)

Decided 2026-11-08, without asking the user, under D-007.

- **Events** follow [ADR-0004](adr/0004-events.md):
  - StreamWisp's envelope and `<domain>.<entity>.<verb>` names;
  - an append-only inbox that deduplicates by event id and by the platform's id, where the first event wins a conflict;
  - every reader keeps its own cursor, with no bus that drops slow readers.
- **Live events reach the app and the CLI by long polling** (`GET /v1/events?after=&wait=`), not a WebSocket. It works through the desktop bridge as is, and never skips an event.
- **Test events** imitate their platform's `source` and are marked `synthetic`. Retrying one `request_id` makes one event. The engine builds the payload from a small catalog of the types M1 sends: chat message, chat command, OBS scene change.
- **A new Events screen** (Ctrl/⌘+2) shows live events, newest first, and a "Send a test event" form. Settings moves to Ctrl/⌘+3.
- **Migrations change from "rewrite one baseline" to numbered files during development, squashed into one baseline at the first public release** ([ADR-0001](adr/0001-storage.md) updated). Rewriting the baseline every slice would make the user move the data folder aside every time, and from slice 4 that means signing in to Twitch again. Slice 3 adds `0002_events.sql`, and the data folder from slice 2 keeps working.
- **Event retention** is left to slice 11 (backups), once real streams show the volume.

## D-011. Choices made while building M1 slice 4 (Twitch sign-in)

Decided 2026-11-08, without asking the user, under D-007.

- **Twitch sign-in** follows [ADR-0005](adr/0005-twitch-sign-in.md):
  - a public app with the device code flow, and no client secret;
  - one secret per account, refreshed one at a time and saved before use;
  - validated at start and hourly; a refused refresh ends the sign-in, and an unreachable Twitch is "unknown".
- **The Client ID** is built in at build time (`make TWITCH_CLIENT_ID=…`, never committed). A pasted Client ID on the Connections screen overrides it. Until the user registers StreamGremlinHQ's app, development builds use a pasted one.
- **Scopes:** only what M1 uses. The streamer asks for `user:read:chat user:write:chat`; the bot for `user:write:chat user:read:moderated_channels`. Later milestones ask the streamer to sign in again for more.
- **The bot must be a different Twitch account** from the streamer. A same-account sign-in is refused and its token revoked.
- **A Connections screen** (Ctrl/⌘+2) holds the Client ID and both sign-ins. Events moves to Ctrl/⌘+3 and Settings to Ctrl/⌘+4. This order is meant to stay.
- **Pages open in the system browser,** through an allow-list: twitch.tv/activate, dev.twitch.tv's console and docs, and a local test server's /activate.
- **`mocktwitch`** stands in for id.twitch.tv in tests and standalone. Using it is always shown as "Test mode", and it is accepted only on a loopback address.

## D-012. Choices made while building M1 slice 5 (Twitch chat)

Decided 2026-11-08, without asking the user, under D-007.

- **Chat** follows [ADR-0006](adr/0006-twitch-chat.md):
  - reading is one EventSub WebSocket session on your account, reconnecting by itself;
  - sending uses Helix, as the bot when one is signed in, otherwise as you.
- **Chat messages and `!commands` become events** (`chat.message.received`, `chat.command.received`). Test events have the same payload, so workflows (slice 7) treat them alike. The bot's own messages are dropped, so StreamGremlin never answers itself.
- **Sending is never retried**, and failures say whether Twitch held the message, the rate limit stopped it, or the outcome is unknown. Local limits stay under Twitch's: 18 per 30 seconds and one a second for an ordinary bot, 90 per 30 seconds for you or a moderator bot.
- **If Twitch refuses the bot's token**, the message goes out as you, and the answer says so. No other fallback.
- **The Connections screen gets a Chat section:** whether chat is read, why not, `/mod <bot>` when the bot isn't a moderator, and a send box.
- **`coder/websocket` v1.8.14** for the WebSocket. Wails already uses this exact version, so the desktop module gains nothing new.

## D-013. Choices made while building M1 slice 6 (OBS)

Decided 2026-11-08, without asking the user, under D-007. The user said "proceed" without naming an OBS version, so obs-websocket 5 (OBS 28 and newer) is assumed.

- **OBS** follows [ADR-0007](adr/0007-obs.md):
  - obs-websocket 5, with the password as a write-only secret;
  - reconnects by itself, except after a wrong password, a too-old OBS, or the streamer pressing Kick in OBS, which the protocol says not to undo.
- **Every OBS change becomes an event,** whoever made it: scene, source visibility, filters, mute, media, stream, recording, replay. Changes made while StreamGremlin couldn't see OBS are reported on reconnect.
- **Scene snapshots switch back only while StreamGremlin still owns the scene.** Any manual change since, even away and back, defeats restoration, and so does a restart. This is the AGENTS.md invariant.
- **Actions** cover scenes, sources, filters, mute, text, media, recording, replay buffer and screenshots. They need a live connection and are never queued. Starting or stopping the stream isn't built.
- **The Connections screen gets an OBS card:** settings, state, and while connected the scenes (Switch, "Switch for now", "Switch back", "Stay here") and their sources (Show or Hide). The other actions get their screen in the workflow editor (slice 9).
- **Screen order stays:** Home, Connections, Events, Settings.
- **The route-coverage test now accepts a quoted path followed by a query string,** such as `'/v1/obs/scene-items?scene='`.

## D-014. Choices made while building M1 slice 7 (workflows)

Decided 2026-11-08, without asking the user, under D-007.

- **Workflows** follow [ADR-0008](adr/0008-workflows.md):
  - versioned JSON definitions, checked when saved;
  - triggers on events;
  - steps: action, set, if/else, foreach, weighted choose with `crypto/rand`, wait, and finally;
  - CEL expressions with a cost limit;
  - one job per workflow and event, with queue policies;
  - a redacted trace of every step.
- **New workflows start off.** Test events run them as dry runs: steps that would reach Twitch or OBS are skipped and recorded with what they would have done.
- **Runs interrupted by a crash or a quit are never re-run on their own**, and failed actions are never retried. Each could post in chat twice or flip a toggle back.
- **The first start of the dispatcher begins at the newest event,** so stored test events don't suddenly run.
- **JSON only** for definitions (no YAML) until something needs it.
- **`cel.dev/cel-go` v0.32.0** (2026-08-19). The three newer releases are under two weeks old. It brings protobuf and ANTLR as indirect dependencies.
- **A Workflows screen** (Ctrl/⌘+4): paste and save, turn on and off, test, and traces. The plain-words editor is slice 9.
- **Settings moves to Ctrl/⌘+5, and is also Ctrl/⌘+comma.** New screens go before it from now on, and comma always reaches it.
- **Example workflows** in `examples/workflows/`, kept valid by an acceptance test.

## D-015. Saved variables: three scopes (M1 slice 8)

Decided 2026-11-08 by the user. The user asked whether variables should be per workflow, global, or shared by a set of workflows ("maybe that doesn't give enough value for complexity"). The recommendation was global, per viewer and per workflow, with no shared sets, and the user said "perfect. proceed".

- **Saved variables** follow [ADR-0009](adr/0009-variables.md), with three scopes:
  - **global** (every workflow);
  - **per viewer**, by platform user id so renames keep values;
  - **per workflow** (private, so templates and pasted workflows can't collide).
- **No shared sets of workflows,** and no "per viewer within one workflow". Use a name prefix instead (`giveaway_open`). Sets can be added later if streamers ask.
- **Deleting a workflow keeps its own variables with it,** for restore (slice 9). It never removes global or viewer ones.

Chosen while building, under D-007:
- **Workflows read them as `saved.global`, `saved.viewer` and `saved.workflow`,** and change them with `vars.set`, `vars.add` (atomic) and `vars.delete`.
- **Test events never save variables.** The change is kept for that run only, so the run behaves the same.
- **Names are lowercase letters, digits and `_`.**
  - Values are any JSON up to 16 KiB.
  - Each set holds at most 1,000 names.
- **A Variables screen** at Ctrl/⌘+5 lists, searches, changes, adds and deletes them. **Settings moves to Ctrl/⌘+6** and stays on Ctrl/⌘+comma.
- **Action parameters can declare a pattern,** and plain-text values are now checked against choices and patterns when a workflow is saved.
- **The mocktwitch `Down` switch became `SetDown()`,** fixing a data race in a Twitch test that `make verify` caught during this slice.

## D-016. Choices made while building M1 slice 9 (editor, run another workflow, delete and restore)

Decided 2026-11-08, without asking the user, under D-007. The user said "proceed" to the plan: the plain-words editor, running another workflow, and deleting and restoring. They follow StreamWisp's ADR-0062, ADR-0073 and D-103.

- **The editor, workflow.run, and delete and restore** follow [ADR-0010](adr/0010-editor-run-delete.md).
- **"Who can use it" is a list in `where`** (`chatter.role: [moderator, broadcaster]`), an additive change to `streamgremlin.workflow/v1`. Test events gain a Role field, so such workflows stay testable.
- **`workflow.run` runs the other workflow inside the current run,** with the same event and `args`. The workflow must be named literally (chat can never choose it) and must be turned on. Loops and chains more than 4 deep are refused when the run reaches them.
- **Deleting a workflow keeps its versions, runs and own saved variables** (D-015). Restoring brings it back turned off, and its id stays reserved.
- **The editor edits the definition itself,** so it never loses what it can't show; a JSON tab stays for that.
  - Loops (`foreach`) aren't in the editor.
  - Pasting JSON moved into the editor's JSON tab.
- **The palette** (`GET /v1/palette`) holds the wording. The app no longer calls `GET /v1/actions`, which stays for scripts.
- **Not in this slice:**
  - the template gallery, which is in slice 11's cut;
  - queued calls to another workflow.

## D-017. Choices made while building M1 slice 10 (chat command ownership)

Decided 2026-11-08, without asking the user, under D-007. The user said "proceed" to the plan, which follows StreamWisp's ADR-0068 and D-111.

- **One answer per chat command,** following [ADR-0011](adr/0011-command-ownership.md):
  - a workflow that's on answers the commands its triggers name;
  - turning one on, or saving one that's on so that it newly overlaps (a new command, or opened to more chatters or chats), is refused (409 `command_taken`) when another workflow that's on answers it;
  - the exception is when their chats or "Who can use it" never meet.
- **Only workflows for now.** Plugins, games and giveaways will add their claims to the same check when they're built.
- **Overlaps from before stay** until one is turned off or changed, and they're flagged on the Chat commands list.
- **The editor warns but still saves,** so a workflow can be built while its command is taken and turned on later.
- **The workflow switch shows the engine's answer,** not the click, so a refused turn-on doesn't look on.
- **"Who can use it" gains "Everyone except mods and you".** Every other option includes the streamer, so without it two workflows could never split a command in the editor.

## D-018. Home and the Get set up checklist (M1 slice 11a)

Decided 2026-11-08 under D-007. The user said "proceed" to splitting slice 11 into 11a (Home and setup), 11b (templates and help) and 11c (backups, retention, settings). This follows StreamWisp's D-109: a guided start without a wizard.

- **Home shows, in order:**
  - **Needs you:** problems, things that can't be checked right now (shown as unknown, never as fine), and tips. Each has a button to the screen that fixes it.
  - **Get set up**, until every step that isn't optional is done.
  - **Happening now:** the latest events, live, and the latest workflow runs.
  - **Connections:** you, the bot, chat and OBS, each in a few words, with unknown shown as unknown.
  - **Engine.**
- **The checklist is ticked from real state, never from clicks.** Its steps:
  1. sign in with your Twitch account (it says to paste a Client ID first when there is none);
  2. add a bot account (optional);
  3. connect OBS (optional);
  4. turn on a workflow;
  5. try it with a test event;
  6. see it answer in your chat (a real run that succeeded).

  A sign-in that can't be checked right now still counts as signed in, and Needs you says it can't be checked. The next step is highlighted.
- **"Hide the checklist" is remembered in browser storage,** like a per-viewer convenience. Losing it only shows the checklist again, and Needs you offers "Show the Get set up checklist".
- **Needs you lists:**
  - a sign-in Twitch ended, or one that can't be checked;
  - chat not being read;
  - the bot not being a moderator (a tip);
  - OBS needing you, or not connected while turned on (unknown);
  - a command answered by more than one workflow;
  - workflow runs that failed in the last 24 hours.
- **`GET /v1/overview`** (`health.read`) computes all of it in Go (`internal/overview`, a plain function of the services' states), so the app and `streamgremlin status` say the same. `status` now prints Needs you and, until set up, the checklist.
- **No wizard and no auto-added workflows:** a wizard blocks a returning streamer and duplicates Connections. Templates are offered in 11b, never added unasked.

## D-019. Templates and help (M1 slice 11b)

Decided 2026-11-08 under D-007. The user said "proceed" to the plan: a template gallery and help in the app. The gallery follows StreamWisp's ADR-0062 and D-109.

- **New workflow opens a gallery:** "Start from scratch", and templates by category.
  - **Chat commands:** Lurk, Say hello back, Hug (random replies), Shoutout (mods and you), Death counter (mods and you), Roll a die.
  - **Scenes:** Be right back (mods and you), Back from the break.
  - **Stream:** We're live.
- **A template is never added unasked.** It opens in the editor, turned off, and is saved only when the streamer saves. The gallery says when you already have it, or when its command is already answered.
- **Blanks:** a template marks the values to fill in, such as Be right back's break scene. The editor lists them, ticks each as it's filled, and refuses to save while one is empty, showing it at its step.
- **Templates are data in the build** (`internal/templates`, embedded JSON in the ordinary workflow format, with a title, category, description and blanks). They're served at `GET /v1/templates` and printed by `streamgremlin templates [show ID]`.
- **A test runs every template through the real engine:**
  - it's valid with its blanks filled;
  - each blank is an empty string at its path;
  - no two templates answer one command, so all can be on together;
  - each runs to the end on a matching test event.
- **A template keeps its own id** (`chat.brb`) while it's free; otherwise the id follows the name.
- **Help:**
  - **a Help screen** (Ctrl/⌘+6): getting started, how the parts fit (connections, events, workflows, Test, runs and traces, one answer per command, variables, "Unknown"), keyboard shortcuts, and where your data lives;
  - a one-line intro under each screen's title;
  - "How values and conditions work" in the editor.

  Help for a field stays beside the field.
- **Settings moves to Ctrl/⌘+7,** and keeps Ctrl/⌘+comma (D-014: Settings is always last).
- **The example workflows stay** in `examples/workflows/` for development and the CLI; the templates are what the app offers.

## D-020. Backups, restore and retention (M1 slice 11c)

Decided 2026-11-08 under D-007. The user said "proceed" to the plan, which follows StreamWisp's ADR-0047.

- **Backups, restore and retention** follow [ADR-0012](adr/0012-backups-and-retention.md).
- **A backup is a ZIP archive:** the database (a consistent copy) and a manifest with hashes. Sign-ins and passwords are never in one.
- **Automatic backups run daily by default,** keeping the newest 7. Backups you make, and ones taken before a restore or an update, are kept until you delete them.
- **Restoring is checked, then applied at the next start,** after backing up what it replaces.
  - The desktop app restarts its engine at once; the CLI says to restart.
  - A failed restore changes nothing.
  - A damaged current database is kept aside as files.
- **History is kept for 30 days of events and 90 days of finished runs,** both adjustable, or 0 for always. Workflows, versions, saved variables and settings are never swept.
- **The desktop bridge gains `POST /desktop/restart-engine`,** used after choosing a restore.
- **A confirm dialog bug was found and fixed:** in the new Delete-backup dialog, the backup was read after the dialog had forgotten it, so nothing was deleted. The handler now takes the backup first. The test caught it.

## D-021. Building for Windows from a Mac

Decided 2026-11-08 under D-007, after the user asked "how do i build for windows on mac?". Until then, Windows builds were made on the Windows computer.

- **`make windows`** cross-compiles the app and CLI from macOS or Linux into `bin/windows/app/StreamGremlin.exe` and `bin/windows/streamgremlin.exe`. Neither needs cgo or Windows tools on Windows.
  - `WINDOWS_ARCH=arm64` builds for Windows on ARM.
  - `TWITCH_CLIENT_ID` works as for the Mac build.
- **The app and CLI go in separate folders,** as on macOS: Windows file names ignore case, so the two `.exe` names are the same file. A first try into one folder showed this, with one build overwriting the other.
- **Not yet:** code signing and an icon embedded in the `.exe`. Those come with packaging for a release.
- **Checked 2026-11-08, the user:** a build from `make windows`, copied to the Windows computer, ran ("the windows version worked well").


## D-022. Timers (M2 slice 1)

Decided 2026-11-08 under D-007. The user said "proceed" to starting M2 with timers. They work as StreamWisp's after its D-096, which the user asked for there: "can we make changes to be more inline with how streamer.bot handles those?"

- **Timers follow [ADR-0013](adr/0013-timers.md):** every so often (optionally at random between two times) or on a cron schedule; wait for chat lines; only while live; run once; a count of how many times each has gone off; Run now.
- **A workflow starts from a timer** with "A timer goes off", and can turn one on, off or the other way with the step "Turn a timer on or off".
- **Chat lines count viewers' messages only:** not yours, not the bot's, not test events.
  - Without this, StreamGremlin's own reminder, sent as you when no bot is signed in, counted towards the timer's next turn.
  - The acceptance test found this.
- **"Live" is what OBS last said.** When OBS never said, "only while live" timers skip their turns.
- **A new Timers screen at Ctrl/⌘+5.** Variables moves to 6, Help to 7 and Settings to 8 (still also Ctrl/⌘+comma).
- **New API permissions:** `timers.read` and `timers.write`.

## D-023. Workflows never answer their own messages

Decided 2026-11-08. After timers, the agent reported that workflows could answer their own chat, and the user said: "do the fix. then counters".

- **The problem:** with no bot signed in, a workflow's chat message goes out as you, and Twitch echoes it back as your chat. A workflow that answers chat therefore answered itself. Through mock Twitch, one viewer message got 55 replies in 2 seconds.
- **The fix:** the echo of a message a workflow sent as you is dropped, as the bot's own messages always were ([ADR-0006](adr/0006-twitch-chat.md)).
- **Still your chat:** what you type in Twitch or in the app's chat box. You can still start a workflow from your own chat.
- **On the Events screen:** a workflow's own message no longer appears there. Its run's trace shows what was sent.

## D-024. No separate counters: saved variables are counters

Decided 2026-11-08 by the user, asked how counters should work.

> Skip; do quotes

- **Streamer.bot has no counters feature.** A death counter there is a persisted global variable with Increment. StreamGremlin's saved variables already do this (`vars.add`), and the Death counter template uses one.
- **Not built:**
  - a Counters screen or store;
  - Streamer.bot's per-command use counts (`%counter%`, `%userCounter%`).

  Either can be decided later if streamers ask.
- **F1's "counters"** is covered by saved variables, the Variables screen and the Death counter template.

## D-025. Quotes (M2 slice 2)

Decided 2026-11-08 under D-007, after D-024.

- **Quotes follow [ADR-0014](adr/0014-quotes.md):** numbered, never renumbered or reused; added, shown (at random or by number), edited and deleted.
- **Viewers use quotes through workflows,** from three templates in a new Quotes category: `!quote` for anyone, and `!quoteadd` and `!quotedel` for mods and you. The Quotes screen adds all three in one step. This matches Streamer.bot, which removed its built-in `!quote` in v1.0.0.
- **A quote added from chat remembers the viewer.** A retried add never adds twice.
- **A new Quotes screen at Ctrl/⌘+6.** Variables moves to 7, Help to 8 and Settings to 9 (still also Ctrl/⌘+comma).
- **New API permissions:** `quotes.read` and `quotes.write`.
- **`actions.Register` now takes a `Services` struct,** so each new service doesn't add another argument.

## D-026. Twitch channel events (M2 slice 3, F2 begins)

Decided 2026-11-08 under D-007. The user said "proceed" to the F2 order, starting with Twitch events.

- **Twitch channel events follow [ADR-0015](adr/0015-twitch-channel-events.md):** follows, new subs, resubs, gifted subs, cheers, raids, channel point redemptions, going live and offline on Twitch, and title or category changes. Each is a trigger in the editor.
- **The streamer's sign-in asks for four more permissions.** Existing sign-ins keep chat and show what's waiting until the streamer signs in again.
- **Each kind of event is listed on Connections,** as coming in or not, and why. One refused (for example, bits on a channel that isn't an affiliate) never stops chat.
- **Going live on Twitch is its own trigger,** not "The stream starts" (which is OBS), so existing workflows don't run twice. Timers' "only while live" listens to both.
- **Quotes take the channel's category** from Twitch when it's known.
- **Five templates in a new Twitch events category:** thank followers, subs and resubs, gifted subs and cheers, and welcome raiders.

## D-027. Sounds (M2 slice 4)

Decided 2026-11-08 under D-007. The user said "proceed" to sounds.

- **Sounds follow [ADR-0016](adr/0016-sounds.md):** a sound library, each sound with its own volume, played with the step "Play a sound".
- **The app's window plays them,** with its built-in audio, not each system's own player as StreamWisp did. This gives a volume and MP3, WAV, OGG and M4A on both macOS and Windows, which Windows' own player couldn't.
- **The window reports whether each sound started.** A trace says "played" only when it did, and says why when it didn't, including when no window is open.
- **Sounds are kept in the database,** so backups and restores carry them.
- **A new Sounds screen at Ctrl/⌘+7.** Variables moves to 8 and Help to 9. Settings is Ctrl/⌘+comma only, now that there are ten screens.
- **API routes can set their own body limit.** Only sound uploads (5 MB) use it so far.

## D-028. Alert overlays (M2 slice 5)

Decided 2026-11-09 under D-007. Before the slice, the user asked whether overlays should be grouped into "master" overlays, one browser source per scene, and then decided: "i think it does overly complicates the setup. let's stick to the orginal plan", then "proceed".

- **Overlays follow [ADR-0017](adr/0017-alert-overlays.md):** each overlay is one OBS browser source with its own private link, served on a second loopback port (7431). Grouping overlays is not built.
- **Links are capabilities, kept like secrets:** only a hash in the database (so backups carry no working link), the token in the secret store, never logged. A new link stops the old one at once.
- **"Show an alert"** shows a title and text, optionally the viewer's Twitch profile picture and a sound from the library. Alerts on one overlay take turns. The step returns when a page says the alert started, and says when no page has the overlay open or none answered.
- **Tests show alerts in the app's preview only,** never in OBS. Steps can now be Preview actions: External, but run in tests as dry runs.
- **Test opens the preview where it's pressed** (the user, 2026-11-09, after asking whether a test should show in OBS: "yes, do both"): Test on the Workflows screen shows a preview of each overlay the workflow uses, and the trace says whether a preview showed the alert, or that nobody saw it.
- **The app previews the overlay page itself** in a frame, so what you see is what OBS shows.
- **A new Overlays screen at Ctrl/⌘+8.** Variables moves to 9, and Help to Ctrl/⌘+/.

## D-029. More looks for alert overlays

Decided 2026-11-09 under D-007. During the slice 5 live check the user asked: "can we add a few more options for the alerts? graduated colors, drop shadow, outlines, font selection, etc".

- **The box:** a gradient (second color and direction), corner roundness, an outline, and a drop shadow, each with its color.
- **The words:** a font, a size, a title color, an outline around the letters, and a text shadow.
- **Fonts come from the computer, never downloaded:** six choices that look alike on macOS and Windows, or any installed font by name. The overlay page keeps loading nothing from outside. Bundling web fonts was left out: it adds files and licences to track, for what installed fonts already give.
- **Try before saving:** "Preview an alert" shows the unsaved look in the app's preview only; OBS changes on Save.
- **Saved looks keep their appearance** ([ADR-0017](adr/0017-alert-overlays.md), amended).

## D-030. Channel point rewards (M2 slice 6)

Decided 2026-11-09 under D-007. The user: "my account is too new for affiliate or partner. proceed".

- **Rewards follow [ADR-0018](adr/0018-channel-point-rewards.md):** make, change, pause, delete, and complete or refund redemptions, only for rewards StreamGremlin made. Rewards made on Twitch are trigger only, with "Make a copy" filling the form in.
- **Twitch is the only record:** StreamGremlin stores no rewards.
- **Retries are safe:** making again finds the reward already made; completing or refunding again is a repeat; the other way after it's handled is refused.
- **Two steps:** "Complete or refund the redemption" (the one that started the run) and "Change a reward" (on, off, pause, resume, cost).
- **A Rewards screen,** with no shortcut number (all nine are taken). The sign-in asks for `channel:manage:redemptions`.
- **Built against mocktwitch,** because the user's channel isn't an Affiliate yet. The live check confirms the refusal is shown plainly; the rest waits for an Affiliate channel.

## D-031. App comfort: More…, text size, spacing and search (M2 slice 7)

Decided 2026-11-09 by the user, over several messages:
- "we'll need to add some general setting for font size, spacing, etc to the theme. we should also discuss adding a search field at the top of at least a few of the tabs";
- "settings to disable/hide some tabs. i can see where many streamers won't need Rewards, Quotes, and maybe some of the others";
- "let's hide Rewards, Quotes, and Timers by default. but maybe instead of hiding them, we could make a 'More...' menu item and let uses pick whatever they want to move there. Home, Events, Workflows, Settings, and Help should always be shown. I think Help should always be at the bottom".

When asked, they agreed that Settings and Help sit at the bottom and that Connections can move.

- **The sidebar follows [ADR-0019](adr/0019-app-comfort.md):** main screens, More… (Timers, Quotes and Rewards at first), then Settings and Help, last. Screens under More… keep working.
- **Ctrl/⌘+1–9 follow the main screens in order.** Settings is Ctrl/⌘+comma and Help Ctrl/⌘+/.
- **Text size** (Small to Larger, and Ctrl/⌘ with + − 0) and **spacing** (Comfortable or Compact) in Settings.
- **Search on Workflows and Events** (Quotes and Variables had it), with Ctrl/⌘+F.

## D-032. Workflow groups (M2 slice 8)

Decided 2026-11-09 by the user: "I do think workflow groups are a good idea. They could allow enable/disable all of them at the group level. We should also have the templates create groups where it makes sense. e.g. all the alert templates can go in the Alerts group so all of them could be enabled/disabled quickly". To the proposal below: "1) good idea. 2) good idea as well. also, i agree with the 'one answer per chat command' rule."

1. **A group has its own switch that leaves each workflow's switch alone.** A workflow runs only when both are on, so turning a group off and on restores exactly which workflows were on. The list says so ("On, but its group is off").
2. **A workflow is in at most one group, like a folder.** The group is saved in the workflow, so exports and templates carry it. Others are under "No group".
- **Templates name their group,** created when needed: Alerts, Thanks, Commands, Quotes, Scenes.
- **One answer per chat command still holds:** turning a group on that would make two workflows answer one command is refused, naming them.

Built in slice 8 as [ADR-0020](adr/0020-workflow-groups.md). Choices made while building it (D-007):
- a group exists while a workflow is in it;
- renaming to another group's name is refused, not merged;
- "Say hi when the stream starts" stays without a group (a group of one).
- Groups fold on the Workflows screen (the user, after the live check: "i think we need a way to collapse items in a group rather than show all workflows"). Groups start folded and "No group" stays open. The open ones are remembered in the window's storage, and a search opens groups with matches.

## D-033. First Words (M2 slice 9)

Decided 2026-11-09 under D-007. The user said "proceed" to the plan: Streamer.bot's First Words, starting over on stream start or after 12 quiet hours (no setting yet), never counting the streamer's or the bot's messages, with a reset and welcome templates.

- **First Words follow [ADR-0021](adr/0021-first-words.md):** `chat.first_words.received`, derived from the stored chat for every platform, once per viewer per stream.
- **The stream starts over** on going live (OBS or Twitch, placed by inbox order), a reset by hand (saved), or 12 quiet hours.
- **Test chat is judged apart.**
- **A First words card on Events** with Start over; the step "Start First Words over"; `streamgremlin first-words`.
- **Welcome templates** in a new Welcome category and group: a chat welcome (a starter) and an on-screen welcome with the profile picture.

## D-034. "Picture, then words": a third alert animation

Decided 2026-11-09 under D-007. The user asked: "can we add a new option for alert animation? there is a widget that starts by sliding in viewer's profile image, then sliding in the first-words welcome message directly to the right of the image. The two are touching on screen. can we do something like that? maybe you have a better idea".

- **`unfold`, built from StreamGremlin's own parts,** not a copy of that widget's design: the picture slides in, then the words unfold from behind it, touching it, and it reverses on the way out.
- **It mirrors on the right,** so the words open toward the middle.
- **Without a picture, the viewer's initial (or ★) keeps the shape,** so tests and samples show it too. Alerts now carry the viewer's name for this.
- **It uses the overlay's look:** colours, gradient, outline, shadow, font and size ([ADR-0017](adr/0017-alert-overlays.md), amended).

## D-035. Animation speed for alert overlays

Decided 2026-11-09 under D-007. The user, about "Picture, then words": "can we make it the slide a little slower or maybe make it configurable?"

- **Each overlay's look has an Animation speed:** from twice as fast to three times as slow (`pace`, 50 to 300 percent of the usual time). It applies to every animation, coming in and leaving.
- **Existing overlays keep the usual speed** ([ADR-0017](adr/0017-alert-overlays.md), amended).

## D-036. Several ways to say a message

Decided 2026-11-09 under D-007. The user asked: "can we add a workflow 'text' option to randomly pick from a list of strings? e.g. change '{viewer} is here' to one of 'Hi there!', 'Good day to you sir and/or madam!', 'Welcome in!'. Ideally, this would be more user friendly than a CSV text box."

- **Any message field can have several versions:** "+ Another way to say it" adds a row, and each row can use values like `{viewer}`. One version is picked at random each time the step runs, and the trace shows which.
- **Stored as `{"one_of": [...]}`** on message parameters only ([ADR-0008](adr/0008-workflows.md), amended). Removing rows down to one makes it a plain message again.
- **Chosen over a separate "pick a random line" step:** it's where the message is written, with no step id to refer to.
- **The "Welcome first-time chatters" template** now says it one of three ways.
- **Sounds too** (the user: "what would it take to do the same for playing a random audio file?", then "proceed"): "Play a sound" and an alert's sound can be several sounds, with "+ Another sound", and one plays at random. A sound deleted from the library later fails the run that picks it and says which; no other is picked in its place, so a run reports what actually happened.

## D-037. Marking failed runs as seen

Decided 2026-11-09 under D-007. The user asked how to clear "3 workflow runs failed in the last 24 hours" on Home, then: "a 'mark as seen' option would be good".

- **Home's failed-runs notice has Mark as seen.** It then counts only failures after that moment, and says "since you marked them as seen" until 24 hours have passed. The runs and their traces are kept.
- **Saved in settings** (key `home`), so a restart keeps it. `POST /v1/overview/failures-seen` (settings.write). The time is the engine's real clock, the same one run times use.

## D-038. Alert sounds play in the overlay

Decided 2026-11-09 under D-007. The user, before a live check: "when using the OBS builtin browser source, the audio plays (i can hear it in the monitoring device) but the mixer bars don't move and there is no audio streamed/recorded. can we play the sound with alerts though the browswer source instead of the app?"

- **An alert's sound plays in the overlay page,** so OBS hears it through the browser source ("Control audio via OBS" gives it a mixer bar), in time with the alert. The app's window played it before, and on macOS OBS doesn't capture that by default.
- **The page says whether it started,** and the run says so ([ADR-0017](adr/0017-alert-overlays.md), amended). Previews and tests are silent.
- **"Play a sound" on its own still plays in the app's window.** Playing standalone sounds through an overlay is left for the user to decide.

## D-039. Closing M2 with the Affiliate features checked against mocktwitch only

Decided 2026-11-09 by the user, asked whether the real-stream check covered both systems and what to do with features that need an Affiliate channel: "Both macOS and Windows", and "Build now, close M2 anyway" (rather than moving them to a later milestone, or keeping M2 open until an Affiliate channel can check them).

- **The M2 real stream passed** on macOS and Windows ("m2 real-stream check is done and went well").
- **Hype Trains, polls and predictions, and ads are built now** as M2 slices 10 to 12, against mocktwitch, which follows Twitch's documented rules.
- **M2 closes when they're in.** M2.md records that these were checked against the stand-in only. Their live checks confirm only that a non-Affiliate channel is told plainly. A real check waits for an Affiliate channel, and is written down then.
- **This is an exception** to "mocks passing isn't enough" (AGENTS.md), made by the user for this milestone only.

## D-040. Hype Trains (M2 slice 10)

Decided 2026-11-09 under D-007, building on D-039.

- **Hype Trains follow [ADR-0022](adr/0022-hype-trains.md):** started, updated and ended events, with level, total, progress and the top contributor; three triggers; test events; and a "Thank the Hype Train" template.
- **The sign-in asks for `channel:read:hype_train`.**
- **Built and checked against mocktwitch only** (D-039).

## D-041. Polls and predictions (M2 slice 11)

Decided 2026-11-09 under D-007, building on D-039.

- **Polls and predictions follow [ADR-0023](adr/0023-polls-and-predictions.md):**
  - events;
  - four steps: start and end a poll; start a prediction, and lock, resolve or cancel it;
  - a Polls screen, under More… by default;
  - the CLI;
  - two result templates in a Polls group.
- **Twitch's documented limits are checked first,** and repeats are safe.
- **The sign-in asks for `channel:manage:polls` and `channel:manage:predictions`.**
- **Built and checked against mocktwitch only** (D-039).

## D-042. Ads (M2 slice 12)

Decided 2026-11-09 under D-007, building on D-039.

- **Ads follow [ADR-0024](adr/0024-ads.md):**
  - requests and breaks recorded apart; a break only from Twitch's event; its end an estimate;
  - requests sent once, never retried; an unclear one checked against the schedule;
  - nothing announced for a refused request;
  - two steps, "Run an ad break" and "Snooze the next ad", that fail when refused or unclear;
  - from chat, only the streamer's own message runs them;
  - an Ads card on Home, `streamgremlin ads`, and two templates in an Ads group.
- **The sign-in asks for `channel:read:ads`, `channel:edit:commercial` and `channel:manage:ads`.**
- **Built and checked against mocktwitch only** (D-039).

## D-043. Giveaways (M3 slice 1)

Decided 2026-11-09 under D-007. The user said "proceed" to the plan: basic giveaways first, then entry rules, extra chances, drawing from recent chat, and an overlay.

- **Giveaways follow [ADR-0025](adr/0025-giveaways.md):**
  - one entry per chat account; every entry has the same chance, said in chat;
  - the entry list fingerprinted when entries close; each draw picked with `crypto/rand` and recorded so the app and `streamgremlin giveaways verify` can check it;
  - optional claiming within a time, setting a winner aside, end and cancel;
  - chat hears everything, including "you're in", from an outbox that never resends;
  - a practice giveaway for test chat, sending nothing.
- **One unfinished giveaway at a time.**
- **A live giveaway holds its chat commands** against workflows (one answer per command).
- **No entry cost or points prizes yet:** they come with the points ledger (F4).
- **The Giveaways screen is in the main sidebar by default.** Variables' shortcut moves from Ctrl/⌘+7 to 8.

## D-044. Giveaway entry rules (M3 slice 2)

Decided 2026-11-10 under D-007, building on D-043. The user said "proceed" to slice 2.

- **Entry rules follow [ADR-0026](adr/0026-giveaway-entry-rules.md):**
  - followers, and for how long;
  - account age;
  - months subscribed, from the chat badge;
  - chat messages this stream;
  - no recent win;
  - entering by chatting.
- **Checked when someone enters,** outside the lock, and never cached or guessed: when Twitch can't be asked, the viewer is told to try again.
- **Refusals are said in chat once per reason.**
- **Rules never change anyone's chance.** The form and chat say so.
- **Left out:**
  - **watch time,** which needs the chatter list and fits the points slice;
  - **a subscription-tier rule;**
  - **rules on the "Open a giveaway" workflow step.**
- **Built and checked against mocktwitch only,** until the live check.

## D-045. More chances for subscribers (M3 slice 3)

Decided 2026-11-10 under D-007, building on D-043. The user said "proceed" to slice 3.

- **Extra chances follow [ADR-0027](adr/0027-giveaway-chances.md):**
  - subscribers can hold 2 to 10 chances, set at entry and kept;
  - chat and the app say the odds instead of "the same chance";
  - draws pick a ticket with `crypto/rand`;
  - weighted fingerprints include each entry's chances, and the checks expand tickets in entry order.
- **Not with subscribers only,** where everyone would hold the same.
- **Left out:** tier chances, watch-time chances, chances for bits, and chances on the "Open a giveaway" workflow step.
- **Built and checked against mocktwitch only,** until the live check.

## D-046. Drawing from recent chat (M3 slice 4)

Decided 2026-11-10 under D-007, building on D-043. The user said "proceed" to slice 4.

- **Drawing from recent chat follows [ADR-0028](adr/0028-giveaway-from-chat.md):**
  - a giveaway with a chat window of 1 to 60 minutes is entered, closed and drawn at once;
  - everyone who chatted in the window enters, once each, through the ordinary checks, with rules and chances;
  - nobody is told they were left out;
  - no entry command is held.
- **Only chat StreamGremlin received counts.** Silent viewers can't be included, and the form says so.
- **Built and checked against mocktwitch only,** until the live check.

## D-047. The giveaway overlay (M3 slice 5)

Decided 2026-11-10 under D-007, building on D-043.

The user was shown a theme-system concept (ChatGPT's, kept in `tmp/streamgremlin-theme-starter`) against the plan, and said: "let's stick with the existing plan for slice 5 and use the wheel theme for a dedicated wheel feature."

- **The giveaway overlay follows [ADR-0029](adr/0029-giveaway-overlay.md):**
  - a second kind of overlay on the existing overlay server;
  - the giveaway's public state after every change;
  - a roll that lands on the winner already drawn, with their picture;
  - practice only in the app's previews.
- **Chat waits about 4 seconds for the roll,** only while a giveaway overlay is open in OBS.
- **The concept's safeguards are kept:**
  - the overlay never picks;
  - it always lands on the winner sent;
  - names are text only;
  - the state is sent again on reconnect.
- **Its imported, scripted themes are not built.**
- **Built and checked against mocktwitch and a test page only,** until the live check.

## D-048. A dedicated wheel feature

Decided 2026-11-10. The user, on the theme-system concept: "use the wheel theme for a dedicated wheel feature".

- **Wheels move from Later into M3, after the giveaway slices and before F4.** The user can move them.
- **Who it's for:** streamers who spin a wheel of choices on stream (the next game, a challenge, a mystery pick).
- **The look starts from the concept's wheel** (`tmp/streamgremlin-theme-starter`: the workshop and neon wheels), rebuilt in StreamGremlin's own code, not copied.
- **Draws are server-side with `crypto/rand`,** recorded and checkable like giveaway draws (StreamWisp ADR-0021, D-034).
- **Wedges are sized to their real odds,** so the wheel never misstates them.
- **Designed in its own ADR** before building, including how spins are started (screen, CLI, workflow step, chat).

## D-049. Wheels (M3 slice 6)

Decided 2026-11-10 under D-007, building on D-048. The user said "proceed" to the plan.

- **Wheels follow [ADR-0030](adr/0030-wheels.md):**
  - named wheels of 2 to 50 weighted choices;
  - exact odds shown everywhere, with wedges drawn at their real size;
  - spins drawn with `crypto/rand` and recorded with the wheel as it was;
  - a third kind of overlay that stops on the drawn number;
  - the result announced only when the wheel stops (or at once, with none open).
- **"Spin a wheel" waits for the result,** so chat commands and channel point rewards can spin and reply through workflows.
- **The Wheels screen goes under More… by default.**
- **No costs or points prizes until F4.**
- **Built and checked against mocktwitch and a test page only,** until the live check.

## D-050. Sounds on giveaway and wheel overlays

Decided 2026-11-10 under D-007, building on D-038, D-047 and D-049. After the wheels live check the user asked: "can we add sounds to the overlay?"

- **Sounds follow [ADR-0031](adr/0031-overlay-sounds.md):**
  - a start sound and an end sound per giveaway or wheel overlay, each from the sound library with its own volume, kept in the overlay's look;
  - played by the overlay page, so OBS hears them;
  - never in the app's previews.
- **Alert overlays keep choosing their sound on the step.**
- **Checked against a test page only,** until the live check.

## D-051. A cheer when the wheel has stopped

Decided 2026-11-10, amending D-050. After its live check the user asked: "can we add a "cheer" sound on stop? so an "as a spin starts", "as it stops", and a "when stopped"."

- **Three sounds per giveaway or wheel overlay:**
  - as it starts;
  - as it stops or lands;
  - half a second later, as the result shows: the cheer.
  The pause before the result matches when the engine announces it, so the cheer and chat line up ([ADR-0031](adr/0031-overlay-sounds.md), amended).
- **With reduced motion, only the cheer plays.**

## D-052. Viewers and the points ledger (M3, F4 slice 1)

Decided 2026-11-10 under D-007. The user said "proceed" to the F4 plan:
1. viewers and the ledger;
2. earning rules;
3. time in chat;
4. more currencies;
5. the points shop;
6. points in giveaways and wheels.

- **Viewers and points follow [ADR-0032](adr/0032-viewers-and-points.md):**
  - viewer records from real events only, with export and forget;
  - an append-only, double-entry ledger with operation ids, no negatives and undo once;
  - one currency, `points`, renamable;
  - the steps "Give points", "Take points" and "Check points";
  - the Points screen, the API and the CLI.
- **The native save dialog is built for exports** (D-115's rule).
- **The Points screen goes in the main list after Giveaways.** Variables' shortcut moves to Ctrl/⌘+9.
- **Built and checked against mocktwitch only,** until the live check.

## D-053. Earning rules (M3, F4 slice 2)

Decided 2026-11-10 under D-007. The user said "proceed" to slice 2.

- **Earning follows [ADR-0033](adr/0033-earning-rules.md):** points for chatting, a first follow, subs and resubs by tier, gifted subs (to the gifter) and bits, as ledger credits that never pay twice.
- **Off until the streamer turns it on,** and it starts from that moment: nothing that happened before pays.
- **Chat earns only while live by default.** When it's unknown whether you're live, chat earns nothing, and the screen says so.
- **Defaults:**
  - chat 1, at most every 60 seconds;
  - a first follow 50;
  - subs 500, 1000 and 2500 by tier;
  - each gifted sub 250;
  - each 100 bits 10.
- **The rules are on the Points screen** ("How viewers earn"), in the API and in the CLI.
- **Subs, gifts and bits are checked against mocktwitch only,** because the user's channel isn't an Affiliate yet (D-030).

## D-054. Giving to everyone, and exporting everyone (F4, planned)

Decided 2026-11-10. The user: "at some point, i'd like to add an "export all" feature as well as a "give to all" with some rules. e.g. in chat viewer list, active chatter, sub, etc."

- **A new F4 slice after time in chat:**
  - **Give to everyone:** points to a group at once, chosen by a rule:
    - everyone on Twitch's chatter list;
    - everyone who chatted in the last N minutes;
    - subscribers.
  - **Export everyone:** every viewer's record and points history in one file, through the native save dialog.
- **Why after time in chat:**
  - Twitch's chatter list needs `moderator:read:chatters`, which slice 3 adds.
  - Knowing who subscribes needs Twitch's Get Broadcaster Subscriptions (`channel:read:subscriptions`, already asked for), not a guess from past sub events.
- **Each viewer is paid once per give,** with an operation id from the give and the viewer, so a retry never pays twice.
- **The streamer sees who's in the group and the total before confirming.**

## D-055. Time in chat (M3, F4 slice 3)

Decided 2026-11-10 under D-007. The user said "proceed" to the slice 3 plan.

- **Time in chat follows [ADR-0034](adr/0034-time-in-chat.md):**
  - every 5 minutes while live, 10 points to viewers who chatted in the last 10 minutes and 3 to quiet viewers on Twitch's chatter list (each can be changed);
  - nothing while offline or when it's unknown whether you're live;
  - only chatters when the list can't be read, saying why;
  - never for time while StreamGremlin was closed.
- **It has its own switch, off by default,** so turning earning on in slice 2 doesn't start paying it after the update.
- **The sign-in asks for `moderator:read:chatters`:** sign in to Twitch again once.
- **Other bots earn** like anyone in chat. Leaving names out isn't built.
- **A test-only engine flag, `-test-minute`,** shortens the minute against a stand-in Twitch, so the acceptance test needn't wait minutes.

## D-056. Giving to everyone, and exporting everyone (M3, F4 slice 4)

Decided 2026-11-10 under D-007, building D-054. The user said "proceed" to the slice 4 plan.

- **Giving follows [ADR-0035](adr/0035-giving-to-everyone.md):**
  - three groups: everyone in chat (Twitch's chatter list), everyone who chatted in the last N minutes, subscribers (Twitch's subscriber list);
  - a preview first, with how many, names and the total;
  - each viewer paid once, even when retried;
  - nothing given when a list can't be read.
- **Undo applies to the whole give**, never below zero; whoever has spent some keeps theirs and is named. A single viewer's payment can still be undone from their history.
- **A step, "Give points to everyone",** for workflows (a raid, say).
- **Export everyone** through the native save dialog, forgotten viewers unnamed.
- **Subscribers are checked against mocktwitch only,** because the user's channel isn't an Affiliate yet.

## D-057. More currencies move to Later (amends D-052)

Decided 2026-11-10. Offered the slice 5 plan for more currencies, the user said: "with further consideration, let's move the currency to a "Later" as well.  for now points can cover most of those usecases until a user asks for something more".

- **Several currencies, retiring one (with moving its balances) and letting balances go below zero move to Later.** They come back when a streamer asks for them, under a new decision naming who will use them.
- **What stays:** one currency, `points`, renamable (D-052). The ledger's schema already has a currency on every account and an `allow_negative` setting, so adding more later needs no change to what's built.
- **F4 continues with the points shop, then points in giveaways and wheels,** using `points` only.

## D-058. The points shop moves to Later (amends D-052)

Decided 2026-11-10. Offered the slice 6 plan for the points shop, the user said: "let's mote that to "later" as well."

- **The rewards shop and its redemption queue move to Later,** with the plan offered for it: items with cooldowns, stock and limits, `!shop` and `!buy`, a queue to mark done or refund, and workflow steps. They come back under a new decision when a streamer asks.
- **F4 continues with points in giveaways and wheels,** then M3 closes after a real stream on macOS and Windows.

## D-059. Points in giveaways and wheels move to Later (amends D-052)

Decided 2026-11-10. Offered slice 7 as points prizes only (a giveaway entry cost would be a wager, which StreamGremlin never runs, and paid wheel spins are already "won't do", D-031), the user said: "let's mote that to "later" as well."

- **Points prizes for giveaways and wheels move to Later.** A giveaway entry cost needs its own decision if it ever comes back, since it conflicts with "StreamGremlin itself never runs a wager".
- **F4 in M3 is slices 1 to 4:** viewers and the ledger, earning rules, time in chat, and giving to everyone with export. All four passed their live checks.
- **M3 closes after a real stream on macOS and Windows,** written in docs/milestones/M3.md.

## D-060. A Viewers screen of its own; M3 closes

Decided 2026-11-10 under D-007. With M3's real-stream check the user said: "live check looks good.  can we move the "views" to their own screen?  there might be things other than points we want to do with them".

- **M3 is done:** its closing check passed on macOS and Windows (docs/milestones/M3.md).
- **Viewers get their own screen,** after Points in the main list, with Ctrl/⌘+9. It holds finding a viewer, their record, their points (balance, give, take, history, undo), export and forget, and Export everyone.
- **Points keeps the points-wide parts:** who holds how many, rename, Check the ledger, Give to everyone and How viewers earn. A top viewer's name opens them on Viewers.
- **Variables loses its default shortcut,** being tenth in the main list; it's one click in the sidebar, and moving any screen under More… gives it one again.
- **Nothing changes in the engine:** same API, CLI and storage. What else viewers are for comes later, each with its own decision.

## D-061. Importing from Streamer.bot moves to Later (amends D-006)

Decided 2026-11-10. Offered the M4 plan (read and report on an export, import what converts exactly, more conversions, a C# report), the user said: "let's move the streamer.bot import to later as well".

- **M4, importing from Streamer.bot, moves to Later** with the plan offered for it, following StreamWisp's ADR-0044. It comes back under a new decision, checked against real exports.
- **Milestone numbers stay as they are,** so earlier references still hold: M5 is Lua and plugins, M6 YouTube.
- **The first outside beta was planned to follow M4 (D-006).** When it happens now is still to decide.

## D-062. M5 next, with custom wheel overlay themes (amends D-006, D-048)

Decided 2026-11-10. Offered M5 (Lua and plugins), the first outside beta or M6 (YouTube), the user said: "1 is a good starting point.  as part of this plugin work, i'd like us to add the custom wheel overlays that we looked at before in tmp".

- **M5, Lua and plugins, is next.** When the first outside beta happens is still open (D-061).
- **Custom wheel overlay themes join M5,** from the concept in `tmp/streamgremlin-theme-starter` (its workshop wheel, neon arcade and selector board, and its theme protocol). This reverses "imported, scripted themes are not built" (D-047, ADR-0030) for wheels.
- **Who it's for:** streamers who want their wheel to look like their stream, and people who make looks for others.
- **What carries over from D-048 and ADR-0030:**
  - the looks are rebuilt in StreamGremlin's own code, not copied;
  - the server draws; a theme only animates to the result it's sent and never decides, changes or announces one;
  - names reach a theme as text only, with no tokens, passwords or engine API.
- **Installed themes are untrusted code,** like plugins: installing one asks first, and it runs sandboxed in its own origin, with no network.
- **Installing, updating and removing themes is designed with plugins,** in the ownership ADR that comes before any plugin code (D-106, D-107 in the feature cut).

## D-063. Packages: what plugins and themes own (M5 slice 1)

Decided 2026-11-10 under D-007. The user said "good plan. proceed" to the M5 plan, whose first slice designs ownership before any plugin code.

- **Plugins and overlay themes are packages, following [ADR-0036](adr/0036-packages-plugins-and-themes.md):**
  - one ZIP format with a strict manifest, read with limits, stored in the database (backups carry it);
  - a fixed identity per id and version; the publisher shown as stated, not verified;
  - installing asks first and changes nothing until used;
  - updates keep valid settings, ask again for more, and keep two versions to roll back to;
  - removing a theme returns its overlays to Classic; removing a plugin turns off the workflows that use it (StreamWisp D-107), and plugin workflows are offered, never added unasked (StreamWisp D-106), now recorded as coming from the plugin.
- **Themes run sandboxed:** a second loopback origin, a sandboxed frame with no network, a checked message protocol, the engine's clock for results, and Classic for a spin a theme can't show.
- **Odds:** installed themes are labelled as not checked; built-in looks draw wedges to their real size, checked by tests.
- **To check in slice 2:** OBS's browser source with the sandbox on macOS and Windows, and whether `window.obsstudio` reaches a sandboxed frame. Until then the Overlays screen says to leave Page permissions at the lowest level for themed overlays.

## D-064. Built-in wheel looks (M5 slice 2)

Decided 2026-11-10 under D-007, building on D-062 and D-063. The user said "proceed" to the slice 2 plan.

- **A wheel overlay has a look,** following [ADR-0036](adr/0036-packages-plugins-and-themes.md): Classic (as before), Workshop, Neon arcade or Selector board, each with a title, whether to show it, and an accent colour.
- **The three new looks run through the theme sandbox** that installed themes will use: a second loopback origin with no network, a sandboxed frame, and the checked message protocol. The engine's clock decides when a spin ends.
- **Classic stays drawn by the overlay page** and shows any spin a look can't.
- **The looks are StreamGremlin's own code,** drawn from the concept's designs (D-048). The wheels draw wedges at their real size; the Selector board shows each choice's chance.
- **Whether OBS's page API reaches a sandboxed frame** is reported by the looks, in the app's preview and in OBS with `?status=1`, for the live check. The Overlays screen says to leave Page permissions at "No access to OBS".
- **Built and checked against a browser test page and the app's tests only,** until the live check.

## D-065. Sounds for the built-in wheel looks

Decided 2026-11-10 under D-007, building on D-051 and D-064. The user, after the slice 2 live check: "looks good.  can you add support for the including the sounds in the themes?  you'd have to create sounds for each theme to go with them."

- **Each built-in look has its own three sounds,** for the cues wheel overlays already have (D-051): as a spin starts, as it stops, and as the result shows.
  - Workshop: a wooden ratchet winding up, a wood block and brass thunk, three rising brass bells.
  - Neon arcade: a stepped square-wave power-up, two landing blips, a chiptune fanfare.
  - Selector board: relays clattering over a motor, a "ka-chunk" as it locks, a rising three-tone station chime.
- **They're synthesised by StreamGremlin's own code** (`internal/overlays/themesounds`), so every sample's origin is plain: no recordings or outside packs. A test checks the committed WAV files match what that code makes.
- **The streamer's own sound wins.** A look's sound plays only for a cue left without one, at that cue's volume. The look's **Play the look's sounds** setting (on by default) turns them all off. On the Overlays screen, an empty sound is shown as "Workshop's sound" (and so on) while they're on.
- **The overlay page plays them,** as it does every overlay sound (ADR-0031), so they keep to the engine's clock and the app's preview stays silent. Themes themselves can't play sound (`media-src 'none'`); the theme server serves the files with their length and ranges. The same rule will hold for installed themes in slice 3 ([ADR-0036](adr/0036-packages-plugins-and-themes.md)).

## D-066. Installed wheel looks, in two slices (M5 slice 3a)

Decided 2026-11-10 under D-007, building on D-062 and D-063. The user: "split it into 3a and 3b. proceed".

- **Slice 3 is split.**
  - 3a: check, install after asking, use on a wheel overlay with the look's own settings, remove (overlays back to Classic), the Add-ons screen, the CLI, and an example look.
  - 3b: update, roll back, another publisher's id, and a deliberately hostile test look.
- **Until 3b,** installing another version of an installed look is refused, saying to remove it first.
- **The Add-ons screen** comes after Variables in the sidebar, and can move under More….
- **Installing shows what it is first:** name, version, publisher "(not verified)", files and size, settings, its own sounds, and that it runs sandboxed with no network and can't have its odds checked. Install sends the ZIP again with the digest the streamer was shown, so what's installed is what they saw.
- **A ZIP of a look's folder works,** as Finder and Explorer make them, so a streamer never has to build one by hand.
- **An example look, Paper wheel,** is in `examples/themes/paper-wheel`: StreamGremlin's own code, for tests and the live check, and for anyone making a look.
  - **It has its own sounds** (1.1.0), made like the built-in looks' (D-065). The user: "the paper-wheel needs sounds. like paper rubbing on paper." Paper rubbing as it starts, a last rub and the pointer's paper tab patting down as it stops, and confetti with a paper party blower for the result.
- Details are in [ADR-0036](adr/0036-packages-plugins-and-themes.md)'s amendment.

## D-067. Add-on looks only load at OBS's "No access" (M5 slice 3b, part 1)

Decided 2026-11-11 under D-007, building on D-064 and D-066. The user's live check: with **Page permissions** raised, the status line said "Look: Workshop ready; OBS page API reachable". The user, on the proposal: "yes, go ahead with 1 to 3".

- **OBS gives its page API to a look's sandboxed frame.** OBS's docs say `window.obsstudio` exists at every level (at "No access to OBS" it only offers `getControlLevel`), and don't say whether a frame gets the source's level. So StreamGremlin assumes a look can do whatever the browser source's Page permissions allow: at "Advanced", switch scenes; at "Full", start and stop the stream.
- **Why a look's frame can act for the source:** OBS's source (`browser-app.cpp`) puts `window.obsstudio` in every frame's context, sends a call to OBS with no note of which frame made it, and checks it against the source's Page permissions; the answer is run in the main frame. The live check matched: the look got no answer to `getControlLevel`. By that code, a call that needs no answer (switch scene, stop streaming) still reaches OBS; that is read from the source, not tried, so add-ons stay blocked above level 0.
- **The looks report the level OBS gives their frame** (`getControlLevel`), or that OBS didn't tell them. With `?status=1` the status line says, for example, "OBS page API reachable, at level 1 (Read access to OBS status information)", and the engine logs it.
- **An add-on look only loads at level 0, "No access to OBS",** the level OBS starts with. The overlay page asks OBS its own level first:
  - level 0, or not in OBS (a browser, or the app's preview): the look loads;
  - a higher level: Classic shows instead, without the look's sounds. Why is said once: in the status line with `?status=1`, and in the engine's log;
  - no answer within 2 seconds, or an OBS too old to say: unknown, treated as not allowed;
  - while asking: Classic, and the look loads once OBS says 0.
- **The built-in looks still load at any level.** They're StreamGremlin's own code and never call OBS. A look is an add-on when the engine says so, or when its link isn't on the theme server's built-in path.
- **Overlays don't need OBS access themselves.** StreamGremlin drives OBS through obs-websocket, never through a page.
- **The screens say it:**
  - Overlays: leave Page permissions at "No access to OBS"; an add-on look only loads there, otherwise Classic shows;
  - Add-ons and `packages install`: when the look will load in OBS.
- Details are in [ADR-0036](adr/0036-packages-plugins-and-themes.md)'s amendment.

## D-068. Updating and rolling back installed looks (M5 slice 3b)

Decided 2026-11-11 under D-007, building on D-063 and D-066. The user, on the proposal for the rest of slice 3b: "proceed".

- **Installing another version of an installed look updates it.** The check shows "Paper wheel 1.1.0 → 1.2.0", and says when it's an older version than the installed one.
  - **The installed version is kept,** so it can be rolled back to. Installing a kept version again reuses its stored files. A kept version with other files is refused: a version is one fixed thing.
  - **Overlays using it switch together.** Each keeps the settings the new version still has and accepts; the rest go back to the new version's defaults, and the answer names them per overlay.
  - **A spin showing when the version changes finishes in the old one.** The next spin is in the new one, and spins waiting move with it.
- **Two versions are kept besides the installed one:** the highest below it first, then the lowest above it. Older ones are deleted.
- **Roll back goes to the highest kept version below the installed one,** with that version's publisher. The version rolled back from is kept, so updating to it again is possible. There's nothing to roll back to below the lowest. Roll back asks first: on Add-ons, a dialog naming the overlays that switch back; in the terminal, `packages rollback ID` without `-yes` only says what it would do.
- **Another publisher's version needs that publisher's name typed exactly** (AGENTS.md: never replace another publisher's pack without an exact identity check):
  - on Add-ons, the check names both publishers, says neither is verified and that someone else may have used the same id, and Update stays off until the name matches;
  - in the terminal, `-publisher NAME` with `-yes`;
  - the API answers 409 `confirm_publisher` without `?publisher=`. The audit records "the publisher changed from X, confirmed by name".
- **The wording about what a look shows is corrected.** "It only shows the result StreamGremlin drew" claimed what StreamGremlin can't check: a look is told the drawn result, but draws what its maker chose. The screens, the CLI and the ADR now say so, and that the real result is always in chat and on the Wheels screen.
- **A hostile test look** is in `tests/testdata/hostile-wheel` (test data, never an example). It tries what a look mustn't do and lists on screen what was blocked and what worked:
  - reach the internet or the engine;
  - open a WebSocket, read the overlay page or its link;
  - keep data, read cookies, open a window, use OBS's page API (read only).

  It also floods the page, sends what a look may not, claims reveals, finishes others' spins, and shows a made-up winner. A setting freezes it for 3 seconds as a spin starts. Tests check:
  - it's served sandboxed with scripts only, no connections, frames, workers or forms;
  - the real overlay page keeps to the engine's clock and the real result whatever it sends.

  What the browser's sandbox blocks is checked in OBS, in the live check.
- Details are in [ADR-0036](adr/0036-packages-plugins-and-themes.md)'s amendment.

## D-069. Lua scripts (M5 slice 4)

Decided 2026-11-11 under D-007, building on D-014. The user: "proceed with slice 4".

- **A step, "Run a Lua script" (`lua.run`),** under More steps. The script is written in the step, like Streamer.bot's "Execute C# Code", not kept in a library of its own. It's literal: `${…}` is refused on save, so chat can never become code. Values reach it through "Values to pass" (`sg.args`), with the event as `sg.event`.
- **What it returns is the step's result** (`${steps.<id>.output.result}`), beside its log lines (`sg.log` and `print`, up to 100).
- **A script changes nothing outside itself in this slice:** no chat, OBS, points or variables. The steps after it act on its result. So a retried or duplicated run is harmless, and a test event runs it for real. Calls that change things come with plugins' approved permissions in slice 5.
- **Lua 5.1 through gopher-lua v1.1.2,** as StreamWisp used (its ADR-0003). It runs in a worker process: the app's own binary, started with `__lua-worker`, never with the engine's environment. The engine kills a worker 250 ms past its time limit. A worker's heap is capped at 64 MiB.
- **Time limit:** 1 second unless the step says otherwise, from 0.1 to 30.
- **`math.random` uses `crypto/rand`,** since a script may pick a winner. `math.randomseed` does nothing.
- **`string.format` is StreamGremlin's own,** as Lua 5.1 has it, with widths of at most 99. gopher-lua's hands the format to Go's `fmt`, where a huge width allocates without limit.
- **Trying a script:** **Try it** in the step, `POST /v1/lua/try` (`workflows.write`) and `streamgremlin lua try FILE`. It runs with a made-up test chat message from TestViewer.
- **Parameters can carry a `max_length`,** checked when a workflow is saved, so an over-long script shows then, not mid-stream.
- Details are in [ADR-0037](adr/0037-lua-scripts.md).

## D-070. Lua plugins, in two parts (M5 slice 5)

Decided 2026-11-11 under D-007, building on D-063 and D-069. Asked whether plugins get their own screen, the answer was the one Add-ons screen ADR-0036 decided, with looks and plugins in one list. The user: "proceed".

- **Plugins are on the Add-ons screen,** beside looks: one install, one list, one history. A plugin adds an on/off switch, its permissions, its steps, its settings and what it has done.
- **Slice 5 is in two parts,** as slice 3 was:
  - **5a:** installing with each permission approved; on and off; settings; the plugin's steps in workflows; `sg.chat` with `chat.send`; the activity log; removing (its workflows turned off); the CLI; the example plugin **Dice tray**.
  - **5b:** updates and roll back for plugins, the workflows a plugin offers, `events.publish` and its events as triggers, and timers.
- **A plugin's state lives in its worker** while it's on: a game's scores are the script's own variables. They start over when it's turned off, its settings change, its worker dies, or StreamGremlin restarts. Keeping state across restarts would be storage StreamGremlin backs up and migrates for someone else's code; it waits until a plugin needs it.
- **Permissions are all or nothing:** exactly those asked for must be approved, as StreamWisp did.
- **A plugin's steps are External:** test events never run them, as its effects are its own.
- **One call at a time per plugin,** within the manifest's timeout (1 to 30 seconds, default 10). A worker that dies is started again, fresh; five in 10 minutes turn the plugin off as failed, saying why.
- **`sg.chat` sends as the bot when one is signed in,** else as the streamer, at most 20 a minute and 500 characters. It never raises: it returns false and why. Its echo is never taken as the streamer chatting.
- **Every call is in the plugin's activity:** chat messages (sent, refused and why, failed) and log lines. The last 1,000 are kept, and go with the plugin.
- **A plugin's number values are taken as text,** so they can come from chat, and are checked against the manifest when the step runs.
- Details are in [ADR-0038](adr/0038-lua-plugins.md).

## D-071. Add-ons mark their steps and bring templates (M5 slice 5)

Decided 2026-11-11. The user: "in the 'add a step' dropdown, can we added an '(add-on)' at the end of the name or some other indicator where it comes from? also, i think we should have support for add-ons like this to add their own templates. which one should have it's own section". Offered a section per add-on in the gallery, in place of ADR-0036's offered workflows, the user said: "proceed".

- **An add-on's steps are marked.** In **Add a step**, its group is "Dice tray (add-on)", after StreamGremlin's own groups, and each step reads "Roll dice (add-on)". A step's card says "from the Dice tray add-on". An action carries `add_on`, the plugin's id.
- **A plugin can bring templates,** in place of ADR-0036's workflows offered after install:
  - in a `templates/` folder, one JSON file each, in the gallery's own format (template and workflow), with no category (the add-on is its section), never a starter, at most 20;
  - checked when the plugin is checked and installed, as the editor would save them: they may use StreamGremlin's own steps and the plugin's, not another add-on's; a problem in a blank left for the streamer is skipped;
  - in the gallery, **each add-on has its own section,** "Dice tray (add-on)", after StreamGremlin's sections, saying who published it (not verified) and, while it's off, to turn it on;
  - in `/v1/templates` with ids `<plugin>/<template>`, the category the add-on's name and `add_on` saying whose they are and whether it's on; `streamgremlin templates` marks them;
  - the install check lists them; the add-on's card on Add-ons names them;
  - removing the add-on takes its section away. Workflows made from its templates are turned off like any using its steps.
- **Why a section per add-on,** not one "Add-ons" section: the gallery matches the step picker, and it says whose templates they are when several publishers' add-ons are installed.
- **A plugin's number left empty is its default:** `!roll` with nothing after it rolls one die.
- **Dice tray 1.1.0** brings `!roll` and `!best` templates.


## D-072. Updating and rolling back Lua plugins (M5 slice 5b)

Decided 2026-11-11, building slice 5b after the user's "proceed". ADR-0036 and ADR-0038 had already set the rules: an update asks again only when it asks for more, and names the workflows that use steps it removes. This records how they're built. Slice 5b is split in two: updates and roll back first; the plugin's own events and its timers come next, as slice 5c.

- **An update is installing another version of an installed plugin,** as for looks (D-068): the check shows it, the installed version is kept, and two earlier versions stay for **Roll back**.
- **Permissions:** an update or roll back asks only when that version asks for a permission that isn't approved now. Then every permission it asks for has to be approved, as at install (`?permissions=`, 409 `confirm_permissions`; `-allow` in the terminal). A version that asks for fewer drops the rest.
- **Steps it doesn't have:** the check names them and the workflows using them. Those workflows are turned off (never deleted) when it's installed, and named again in the answer. A step is found exactly: `UsingActions` matches a prefix only when it ends in ".".
- **Settings** stay where the new version still takes them. A value left at the old default takes the new default. Any other value that doesn't fit goes back to the default, and is named.
- **A plugin that's on is loaded again as the new version,** fresh: its memory starts over, and the screens say so before you confirm. One that won't load is left off, saying why, as when it's turned on. A failure from the version before is cleared.
- **Roll back** to a version that asks for more says so in its dialog, whose button reads **Allow and roll back**: confirming it is the approval. In the terminal it needs `-allow`.
- The audit records the permissions, the steps removed and the settings reset with each update and roll back.

## D-073. Lua plugins' events and timers (M5 slice 5c)

Decided 2026-11-11, building slice 5c after the user's "proceed". ADR-0036 had given plugins `<id>.<event>` events with source `plugin:<id>`, and ADR-0038 left `events.publish` and timers (`sg.after`) to this slice. StreamWisp's ADR-0041 and ADR-0067 had both. This records how they're built.

- **A plugin declares its events** in its manifest's `lua.events`: up to 20, each a name, a label, a description, and the payload's values that messages can include (`{winner}`). It needs the new **`events.publish`** permission, "Start your workflows with its own events", approved like `chat.send`. A plugin with events asks for it, and one without events can't.
- **The event type is `plugin.<id>.<name>`,** with the id's `-` as `_` (`plugin.dice_tray.round_ended`). Event types are three or more lowercase words, and a plugin's id can have `-`. Its source is `plugin:<id>`, it's recorded as confirmed, and its cause is the event of the call that sent it.
- **`sg.emit(name, payload [, id])` → ok, reason,** like `sg.chat`: it never raises an error. It's refused, and the refusal recorded in the plugin's activity, when:
  - the plugin lacks the permission;
  - the name isn't declared;
  - more than 10 a second are sent;
  - an id is used twice;
  - **the call was started by one of this plugin's own events.** A workflow started by a plugin's event that calls the same plugin could otherwise send events in a circle forever.

  The optional id makes a retried step send its event once: the example passes `call.key`.
- **In the editor,** a plugin's events are triggers under **When**, after StreamGremlin's own, in a group "Dice tray (add-on)", each marked "(add-on)", with their values as `{tokens}`. `/v1/palette` adds them with `add_on`. A workflow started only by an add-on's event can't be tested with a test event, and Workflows says to use the add-on.
- **Removing a plugin turns off the workflows its events start,** as well as those using its steps.
- **Timers: `sg.after(seconds, fn)` → id, `sg.cancel(id)` → whether it was waiting.**
  - Seconds are 1 to 86,400 (a day), with up to 100 waiting. A timer can be set while the script loads, in a call, or in another timer.
  - The worker tells the engine which timers are waiting after each call. The engine fires the next one due, one at a time with the plugin's calls, within the plugin's timeout.
  - A timer can send chat and events. Its log lines go in the activity, and so does a timer that fails. One that stops the worker counts as a crash.
  - Timers are the plugin's memory: they're gone when it's turned off, its settings change, it's updated, its worker restarts or StreamGremlin stops.
- **The protocol** gains a `fire` frame, `name` and `payload` in a `host` frame, and `timers` in a plugin's `done` frame.
- **Dice tray 1.2.0:**
  - **Open a dice round:** rolls in it count, and its timer closes it, saying who won;
  - two events: **A dice round ends** and **A new best roll**;
  - two templates: **!diceround** and **Cheer a dice round's winner**, which its event starts.

  The update test version is now `tests/testdata/dice-tray-update`, 2.0.0.

## D-074. Points for plugins: rounds (M5 slice 6)

Decided 2026-11-11, building slice 6 after the user's "proceed". The feature cut has "Points API for plugins: award, charge, refund, and escrow for rounds, with odds 'stated by the plugin'" (StreamWisp ADR-0054, D-081). The design is [ADR-0039](adr/0039-points-for-plugins.md). In short:

- **Awarding, charging and refunding happen only inside rounds,** as StreamWisp decided: a plugin can't give or take points any other way. A new permission, **`points.rounds`**, comes with limits in the manifest's `lua.points`:
  - the currency;
  - `max_stake` per viewer per round;
  - `max_multiplier`, the most a payout can be over the stake;
  - `max_round_seconds`.

  The install check states them in words, and says the odds are the plugin's own. An update that raises a limit asks again.
- **A stake is always the viewer behind the call:** the actor of the event that ran the step. One stake per viewer per round. A timer has no viewer, so it can't stake.
- **The ledger is the record.** Stakes, payouts and refunds each have an operation id made from the plugin, the round and the viewer, so nothing is taken or paid twice. Refunds undo the stakes the ledger holds.
- **Settling is recorded before paying,** and finished at startup if a stop interrupted it.
- **Open rounds are refunded:**
  - when the plugin's worker starts (its memory is gone);
  - when it's turned off, fails, is updated or removed;
  - past `max_round_seconds`, checked every 5 seconds;
  - at startup, for rounds open when StreamGremlin stopped.
- **Rolls are made by StreamGremlin** from `crypto/rand` and recorded, append-only. The rules are recorded as the plugin's own words. The screens never call a game fair.
- **Simpler than StreamWisp in one way:** a stake can't be changed once it's made.
- **Dice tray 1.3.0** adds **Gamble points** (`!gamble 50`, `!gamble all`) and its template. Each gamble is a round: it pays twice the stake at the winning roll (60 by default, a setting) or more.

## D-075. M7, Live: a Live screen, moderating from chat, and a Stream Deck plugin, before YouTube (amends D-006)

Decided 2026-11-11, after M5 closed. The user: "i do think we need to add a few more things to make streaming easier on twitch before youtube.  first, we need a "live" screen that a stream and see and control what they need while live without having to dig though menus, etc.  i'm thinking of a screen with the chat in the middle and programable buttons down the right side or along the top.  they buttons would trigger workflows.  the chat would need some details about the viewer when hovering over their name.  pretty much everything we can get from the api.  we should also have small buttons to ban, mute, etc (whatever we can do in the api) to control the viewer in chat.  second, we need to figure out stream deck integration so streamers that have it can control the workflows, etc."

Offered our own Stream Deck plugin, global hotkeys, or both, the user chose: "Our own Stream Deck plugin (Recommended)".

**Who it's for:** every streamer while live (the Live screen and moderating), and streamers with a Stream Deck, many of them coming from Streamer.bot (D-001).

- **A new milestone, M7. Live, comes before M6 (YouTube).** Numbers stay as they are (D-061). The plan is in [docs/milestones/M7.md](milestones/M7.md).
- **A Live screen:** Twitch chat in the middle, with sending; buttons that run workflows, down the right side or along the top (the streamer's choice).
- **A new trigger, "You press a button":** a button, on the Live screen or a Stream Deck, runs the workflows it starts. It has no viewer, like a timer.
- **A viewer card** on a chatter's name, with what Twitch's documented API gives and what StreamGremlin knows. Twitch's side includes their account, since when they follow, their subscription, and whether they're a VIP, moderator or banned. StreamGremlin's side is their record, points and recent messages here. Each piece needs its own scope, so signing in again asks for it. A piece Twitch doesn't give shows as unknown, never guessed.
- **Moderating from chat:** small buttons for what Twitch's documented moderation API allows. That means deleting a message, timing out (Twitch's "mute"), banning and unbanning, warning, a shoutout, VIP and moderator, and the chat modes. Each is checked against Twitch's docs when built, recorded, and refused ones say why. Banning asks first, in the app's dialog.
- **Our own Stream Deck plugin,** built with Elgato's documented SDK. It pairs once with the app, then each button is set to a StreamGremlin button or workflow, picked from a list, and it can show state. It reaches the engine through the same authenticated local API as the app and CLI, with its own narrow permissions. How it pairs is designed as an ADR first (security). It installs from a file. A Marketplace listing is a later decision.
- **The Streamer.bot WebSocket gateway stays Leave,** so Streamer.bot's Stream Deck buttons don't carry over. Streamers set their buttons again with ours. **Global hotkeys stay Later.**

## D-076. Buttons and the Live screen (M7 slice 1)

Decided 2026-11-11, building M7 slice 1 after the user's "proceed". The design is [ADR-0040](adr/0040-buttons.md). In short:

- **A button is a named source of an event,** `buttons.button.pressed`, not a second kind of workflow. Workflows start from it with the new trigger **You press a button**, matching the button. A press has no viewer, like a timer.
- **Exactly once:** a press carries a request id from the app. The same id again is the same press, and runs nothing twice.
- **A press says which workflows it started.**
- **Deleting a button keeps its workflows,** as with timers. They never start from it again.
- **Pressing has its own permission,** `buttons.press`, so the Stream Deck's token (slice 4) can press without editing.
- **Live is second in the sidebar** (Ctrl/⌘+2) and never goes under More…. Every later shortcut moves down one.
- **The buttons go right or top,** saved with the appearance settings as `live_buttons`, right by default.
- **Chatters' Twitch colours are a swatch,** never the colour of their name, for contrast. Badges are words.
- **The send box sends as you by default** on the Live screen, or as the bot.
- **Buttons can have an icon, and show only the icon** (added 2026-11-12, after M7 slice 3's live check). The user: "can we also add an option for the custom buttons to allow the user to set icons for those buttons and optionally hide the text. the icon can come from common icon font packs". The icons are Tabler Icons (MIT, about 5,200, with Twitch, Discord and YouTube logos), bundled so they work offline; the editor suggests common ones and searches by name. The label stays required as the button's name for screen readers and its tooltip. Then: "can we use/add nerd fonts (https://www.nerdfonts.com) for the icons? when using icons only, can we make the button area a grid to allow for more icons in less area?" Nerd Fonts is a second set beside Tabler (Material Design, Font Awesome, Codicons and more, about 10,700 icons, credited in Help and `THIRD-PARTY-NOTICES.md`), and icon-only buttons are small squares sharing rows, in order. See ADR-0040's Icons section. In the same change, the Buttons and Chat modes panels got padding (their headings touched the edge) and smaller controls, so the side stays compact.

## D-077. New workflows start on (amends ADR-0008)

Decided 2026-11-11, after M7 slice 1's live check. The user: "live check looks good. on thing that this pointed out, when creating a workflow (manually or from an addon), it should be enabled."

- **A new workflow starts on,** however it's made: the editor, a template (StreamGremlin's or an add-on's), the "Make a workflow" buttons on Timers and Live, or `streamgremlin workflows save`. ADR-0008 had new workflows start off.
- **Unless its command is taken.** When a workflow that's on already answers a chat command the new one answers, the new one is saved off, so chat never gets two replies. The save says why (`off_because`), and the screens and CLI show it.
- **Saving an existing workflow keeps its switch,** on or off, as before. A restored workflow still comes back off, and imports from Streamer.bot (Later) still start off.
- **The screens say where it stands after saving:** "It's on.", "It's on, but its group … is off.", or "It's off: …".

## D-078. The viewer card (M7 slice 2)

Decided 2026-11-11, building M7 slice 2 after the user's "proceed". The user, for D-075: "the chat would need some details about the viewer when hovering over their name. pretty much everything we can get from the api."

- **What Twitch's API gives about one viewer, read fresh:** the account (made when, Affiliate or Partner, staff, their bio) from Get Users; following and since when; subscribed, the tier, and whether it's a gift and from whom; VIP; moderator; banned or timed out, until when, by whom and why. One documented call each, at once, with that viewer's id. The engine keeps none of it; the app reuses a card for a minute, and **Refresh** asks again.
- **Two read scopes are added for the streamer:** `moderation:read` (moderators and bans) and `channel:read:vips`. The streamer signs in again once; until then those facts say so. Slice 3 adds the moderating scopes it needs.
- **Unknown, never "no":** a fact Twitch refuses, fails or isn't signed in for is listed as unknown with why. Only an answer Twitch gave is a yes or a no. Where the reference doesn't say what one id that isn't listed returns, an empty list is taken as no, and the manifest says so.
- **Not used:** Check User Subscription, which needs the viewer's own token, and anything Twitch doesn't document (watch time, a viewer's other channels).
- **StreamGremlin's own record** comes with it: first seen, messages, points, and their 10 newest chat messages still kept. A test chatter isn't looked up on Twitch.
- **On the Live screen** a chatter's name is a button. Resting the pointer on it opens the card; leaving closes it. Clicking it, or Enter, opens it to stay, with focus in it; Escape or Close shuts it and gives focus back to the name.
- **The route** is `GET /v1/chatters/{platform}/{user_id}` under `viewers.read`; `streamgremlin viewers card @NAME` prints the same. No new storage, so no ADR.

## D-079. Moderating from chat, chat modes, and Twitch's suspicious-user flags (M7 slice 3)

Decided 2026-11-11. The user asked whether external sites like CommanderRoot could help spot bots and scammers. I found no documented API for those lists, and recommended Twitch's own Suspicious User Detection instead. The user: "yes, fold it into slice 3 and proceed". The design is [ADR-0041](adr/0041-moderation-record.md). In short:

- **Moderating is sent once, as the streamer, and recorded** with Twitch's answer: done, refused (with why) or unknown (no answer, never retried). It covers delete a message, time out, ban, unban, warn, shoutout, VIP and moderator on and off, and the chat modes. A request id makes a repeat return the record instead of sending again.
- **Banning asks first** in the app (and, since 2026-11-12, everything else that can't be taken back: see below), and needs `-yes` in the CLI. A timeout from a chat line is 10 minutes. The card offers 1 minute to 1 week, with a reason.
- **Twitch's suspicious-user flags** (`channel.suspicious_user.message` and `.update`) are events:
  - **what they mark:** a likely or possible ban evader, someone banned in channels you share bans with, or someone marked by you or a mod; and whether Twitch monitors or restricts them;
  - **where they show:** on the chatter's messages on Live and on their card;
  - **a workflow trigger:** "A suspicious user chats";
  - **on the card:** Monitor, Restrict and Treat as usual change how Twitch treats them.
  - **unknown before a flag:** there's no request to read a viewer's flags, so before any flag the card says Twitch hasn't flagged them while StreamGremlin was listening, never "not suspicious".
- **No third-party bot lists:** CommanderRoot and TwitchInsights have no documented API, would send viewers' names to someone else, and list helpful bots too. If a "known bot" note is wanted later, it would be a list the streamer imports as a file.
- **The bot doesn't moderate yet.** D-075 allowed it; it waits until someone asks, with its own scopes.
- **Nine scopes are added** to the streamer's sign-in (ADR-0041). The streamer signs in again once.
- **Quick actions sit in a gear menu beside the name** (changed 2026-11-12 after the live check). They were at the end of the message, shown on hover. The user: "for really long messages, putting at that end seems clunky. can we move them into a small button (gear icon) as a dropdown to the right of the username?" The gear opens Delete message, Time out 10 minutes, Ban… and More in their card…. It can be reached with Tab, unlike the old hover buttons, and the menu works with arrow keys and Escape.
- **Anything that can't be taken back asks first** (changed 2026-11-12). The user: "let's make sure "delete", ban, and other difficult/impossible to revert action have a confirmation. we want to protect against mis-clicks". Asking first: deleting a message (can't be put back), a timeout (the viewer and chat see it at once), a ban, a warning and a shoutout (neither can be withdrawn), and making a moderator (they can moderate before it's undone). Not asking: unban, VIP on and off, removing a moderator, Monitor, Restrict, Treat as usual, and chat modes, which are one click to undo. Buttons that ask end in "…". The command line is unchanged: typing a command is deliberate, and ban still needs `-yes`.
- **A coloured button is filled with its colour** (changed 2026-11-12, after the Stream Deck live check), on Live and on Stream Deck keys alike, instead of a strip. The user: "can we make the button color the color of the whole button (instead of just a small part of it) on the live page and in the streamdeck?" Text and icons on a coloured button are near-black (#111114), at least 5:1 on all eight colours (white fails on every one), checked by a test. Uncoloured buttons look as before. On keys the label is drawn into the image, because a plugin can't set the colour of Stream Deck's own title.
- **Badges on a chat line are one letter** (changed 2026-11-12). The user: "the "moderator" label is a little big. what are the other possible labels there? could we reduce them to a single letter?" B broadcaster, M moderator, L lead moderator, V VIP, S subscriber, F founder, T Twitch staff, ✓ verified (V is VIP), A artist. The word stays on hover and for screen readers, and Help lists the letters. Test, Shared chat, Suspicious and Deleted stay words: they're about the message, not the chatter, and are rare.

## D-080. Pairing the Stream Deck, and paired clients' own tokens (M7 slice 4)

Decided 2026-11-12, building M7 slice 4 after the user's "proceed". It's a security design, so it's [ADR-0042](adr/0042-paired-clients.md). In short:

- **The Stream Deck plugin gets its own token, not the admin one.** It can only see and press buttons (`buttons.read`, `buttons.press`, and `self.read` to check itself). It can't change anything, read other records or the engine's status, moderate or send chat. A press with its token is always recorded as from the Stream Deck.
- **Pairing is a code shown in the app and typed into the plugin.** **Connections → Stream Deck → Pair a Stream Deck** shows a code like `K7QX-M3PA`. It works once, for 5 minutes; five wrong codes stop it. No program can start a pairing by itself.
- **Tokens are stored only as hashes.** Paired decks are listed with when they were last used. **Forget…** (asks first) refuses the token at once.
- **Why not the keyring in the plugin:** Elgato's current SDK keeps a user's token in the plugin's global settings, which it says users can read, and has no secrets API. Keeping the token narrow and easy to forget limits what a copied one could do.
- **CLI:** `streamgremlin streamdeck`, `streamdeck pair`, `streamdeck forget ID`.

## D-081. The Stream Deck plugin (M7 slice 5)

Decided 2026-11-12, building M7 slice 5 after the user's "proceed". The design is in [ADR-0042](adr/0042-paired-clients.md) ("The plugin"). In short:

- **One action, Press a button.** Each key presses one StreamGremlin button, with the button's colour, icon and label. D-075 also named "run a workflow"; that's left out, because a button already runs the workflows that start from it, and the Stream Deck's token can't reach workflows (D-080).
- **Keys never look working when they aren't:** Not running, Pair first, Pick a button, Button gone; dimmed when the button starts nothing that's on. A press shows a check mark when it started something, and a warning otherwise.
- **A press runs once,** even when an answer is lost and it's asked again.
- **Pairing is in the key's settings:** type the code from Connections.
- **Icons on keys are the app's icons,** Tabler and Nerd Fonts alike. First built with Nerd Fonts icons showing the label, as Stream Deck lacks the font. After the live check the user asked: "is it possible for the button on the streamdeck to show the same icon as in the app by default?" Now the plugin ships each Nerd Fonts icon's outline, made from the same font, and draws it.
- **One installer for macOS and Windows,** `bin/streamdeck/com.streamgremlin.streamdeck.streamDeckPlugin`, built by `make streamdeck` (part of `make verify`). It installs with a double-click, outside Elgato's Marketplace. Stream Deck 7.1 or later.

## D-082. The window remembers its size and place, and Live stretches with it

Decided 2026-11-12. The user passed on a streamer's request: "can we support dynamtic resizing of the window? the chat screen made it clear that being able to resize and remembering the size & location of the window would be very useful. in the case of the chat, the left and right "menu bars" should be pinned to those side and the chat section should resize sideways".

- **Who it's for:** every streamer who keeps StreamGremlin open beside OBS while live, often on a second screen.
- **The window opens where it was,** at the size it was, and maximised if it was. Its place is saved half a second after moving or resizing stops, and when it closes. It's saved to `window.json` in the data folder, as the desktop shell's own state: the engine never reads it.
- **Never off-screen:** at start the window goes back on the screen it most overlaps. It's kept whole and at least its minimum size. When too little of it would show on any screen (a monitor that's been unplugged), it opens centred instead. A minimised window isn't saved; a maximised one keeps the size to come back to.
- **Live uses the whole width.** The sidebar stays 220 px on the left and the buttons and chat modes 16rem on the right; chat takes everything between and resizes sideways with the window. Only a window narrower than about 760 px stacks the buttons above chat.
- **Fixed after the user's check** (2026-11-12): "there seems to be a scroll bar in the right and bottom of the window that can't be removed. no matter how large i make the window in any direction, the scroll bar shows and shows there is more of the window than is being displayed. also, the resize seems to only work ont he live screen and not the others".
  - **No more page scroll bars:** the page is now exactly the window and never scrolls as a whole. Each screen scrolls inside its own area, and only when its content is taller than the window. Live's chat had been sized from the window's height minus a fixed amount that was too small, so the page always overflowed.
  - **Every screen uses the whole width,** not just Live (they had been capped at 900 px).
  - **On Live:** chat fills the height that's left, and the buttons column scrolls by itself when it has more than fits.
  - **Switching screens:** each screen opens at its top.
- **Live check passed** (2026-11-12), after the fix: "live check looks good."

## D-083. The full licence list, and zod 4 in the Stream Deck plugin

Decided 2026-11-12. The user: "update the license list. can we manually upgrade to a later version of zod?"

- **Who it's for:** every streamer who installs StreamGremlin, and anyone redistributing it. MIT and BSD licences ask that their notice and text travel with the program.
- **The licence list:**
  - `THIRD-PARTY-NOTICES.md` now names every Go module built into the app or the command line on macOS, Windows or Linux: its licence, copyright, what it's used for, and which program has it. There are 28; all are MIT, BSD, ISC or Apache 2.0, plus SQLite's public domain.
  - `THIRD-PARTY-LICENSES.txt` holds each one's licence files in full, and Go's own. It's written by `make notices` (`cmd/notices`) from what `go list` says each program is built from, and is to ship beside the app and command line in every release.
  - `make verify` fails when the file is out of date, or when the notices miss a module. A new dependency therefore can't slip in uncredited.
- **zod 4 in the plugin:** zod 3 had a memory-use advisory (GHSA-mrhx-c7xc-m3w5), accepted in ADR-0042 because Elgato's SDK asks for zod 3. Elgato's code turns out to use only zod 4's API (`zod/v4-mini`), which zod 4 still provides. The plugin now forces zod 4.6.7 with an npm override, and `npm audit` is clean. ADR-0042 records why that's safe and when to drop the override.

## D-084. A first beta: unsigned installers for a small group (amends D-005)

Decided 2026-11-12. The user: "i'd like to get some beta testers. do we need to update anything in the help docs? can we build a window installer?" Asked how, the user chose unsigned builds for a small group, on Windows and macOS. On reports: "i'll manually tell each tester in this stage".

- **Who it's for:** a few streamers the user invites, on Windows and macOS.
- **Unsigned for this small group,** amending D-005, which planned signing before any outside tester:
  - **Windows:** SmartScreen warns, and the guide says "More info", then "Run anyway".
  - **macOS:** the app is signed ad hoc, so it isn't called damaged, and the guide says how to allow it in Privacy & Security.
  - **Before a wider beta:** signing still comes first (Apple Developer Program; a Windows certificate, checked for current options then).
- **Installers:** `make release VERSION=… TWITCH_CLIENT_ID=…` builds a Windows installer (per user, no administrator) and a universal macOS dmg, both checked by `tests/release`. See [ADR-0043](adr/0043-beta-installers.md).
- **The Windows installer installs the Stream Deck plugin if asked.** The user asked where it puts the plugin, then: "add the checkbox on the installer's". Its last page has **Install the Stream Deck plugin**, unticked, beside "Open StreamGremlin" and "Show the beta guide". On macOS the plugin is in the dmg.
- **Reports go to the user,** who tells each tester how to reach them. The beta guide and Help say to tell whoever invited you, with what happened, the version, the system and the logs.
- **The beta guide,** [docs/BETA.md](BETA.md), ships in both installers. It warns that beta data may need a fresh start at the first public release (ADR-0001).
- **Help covers every screen,** checked by a test against the sidebar. New entries: Giveaways, Points, Viewers, Wheels, Add-ons (wheel looks as well as plugins), Ads, Backups and the window. Help also has a "Something went wrong?" section, with the version and the logs folder.


## D-085. Chat filters

Decided 2026-11-13. Asked what common bot features were missing while the beta testers try the app, I suggested chat filters first, then commands for the channel. The user: "let's do chat filters and the channel commands".

**Who it's for:** every streamer with a busy or spam-prone chat, and streamers coming from Nightbot or Streamer.bot, who expect spam protection to be there.

- **Six rules,** each with its own switch, under one switch for all of them. Everything starts off.
  - **Links:** a web address, with or without `http` or `www`. Matching uses a list of common endings (`.com`, `.tv`, `.ly`…), so "ok.so" isn't a link. Allowed sites never count; `twitch.tv` and `clips.twitch.tv` are allowed at first.
  - **Capitals:** a message with at least 10 letters, of which 70% or more are capitals.
  - **Symbols:** a message of at least 15 characters, half or more of them symbols.
  - **Emotes:** more than 10 emotes in one message, counted from Twitch's emote fragments. `emote_count` is added to chat messages' payload.
  - **Repeats:** the same viewer sending the same message (ignoring case and spaces) a third time within 60 seconds.
  - **Words:** the streamer's list of words and phrases, matched as whole words without regard to case. A `*` at the start or end matches the rest of a word.
- **What happens:** each rule deletes the message (the default), times the viewer out (1 second to 2 weeks) or warns them. The first rule that matches acts, in the order Words, Links, Emotes, Capitals, Symbols, Repeats. A filter never bans: a mistake would cost a viewer too much.
- **Never filtered:** you, your moderators and your bot. Each rule can also leave VIPs and subscribers alone (VIPs are left alone at first). Messages from another channel in a shared chat aren't filtered: they aren't yours to moderate.
- **`!permit name`:** a new step, **Let a viewer post a link**, lets one viewer post links for 60 seconds (1 to 10 minutes). A template, "Permit a link" (mods and you), uses it. Permits live only in memory.
- **Recorded like moderating by hand (ADR-0041):** each action is sent once, as the streamer, and recorded with Twitch's answer, from `filter`. The request id is made from the message id, so a message is never acted on twice. Reasons are shown to the viewer for a warning, and to moderators for a timeout: "Please don't post links here." and so on.
- **Workflows can react:** each catch adds a `chat.filter.caught` event (rule, action, Twitch's answer, the message), with the trigger "A chat filter catches a message". A template, "Say why a message went", replies in chat.
- **Test messages are judged too,** but nothing is sent to Twitch: the caught event is a test event.
- **A Chat filters screen,** in the sidebar after Viewers: the switches and settings, a box to try a message, and what the filters did lately. CLI: `streamgremlin filters` and `filters check TEXT`.
- **Twitch's own tools stay Twitch's:** AutoMod and Blocked Terms act before StreamGremlin sees a message. Help says where to find them. Copying the word list to Twitch's Blocked Terms waits for a request (requested and built: D-088).
- **No new scopes:** deleting, timing out and warning were added in D-079. No new storage: the settings are saved with the others, and what filters did is in the moderation record.

## D-086. Commands for the channel: title, category, clips, markers, uptime and follow age

Decided 2026-11-13, with D-085. The user: "let's do chat filters and the channel commands".

**Who it's for:** streamers and their moderators, from chat and from the Live screen's buttons. Viewers asking "how long has the stream been on?" or "how long have I followed?". Streamers coming from Nightbot and Streamer.bot, which have these.

- **Six new steps, using Twitch's documented API:**
  - **Change the stream title** (Modify Channel Information, 1 to 140 characters).
  - **Change the category:** a name, matched exactly with Get Games, otherwise Search Categories' first answer. The step says which category it chose.
  - **Make a clip** (Create Clip, as the streamer, with an optional title and a length of 5 to 60 seconds). Twitch makes clips after answering, so the step checks with Get Clips every 5 seconds. It finishes with the clip's link once Twitch has the clip, and fails after 60 seconds, as Twitch's reference says to assume then. Never sent twice.
  - **Add a stream marker** (Create Stream Marker, up to 140 characters), only while live with past broadcasts on.
  - **Read the stream:** live or not, since when, how long in words ("2 hours 5 minutes"), the title, the category and the viewer count (Get Streams and Get Channel Information). It reaches nothing, so it runs for test events too.
  - **Read when a viewer followed:** following or not, since when, and how long in words (Get Channel Followers). The viewer is the one who chatted, or a name.
- **Templates:** `!title` and `!game` (mods and you: with words they change the title or category, without them they say what it is), `!clip` (mods, VIPs and you), `!marker` (mods and you), `!uptime` and `!followage` (everyone). Each can be changed in the editor like any workflow.
- **Two scopes are added:** `channel:manage:broadcast` and `clips:edit`. The streamer signs in again once; until then the steps say to.
- **Not in this slice:** tags, content labels, language and stream delay.

## D-087. Fixes after the filters and channel commands: sign in again, Live's height, and the bot's replies

Decided 2026-11-13. The user: "when i need to sign in again to get new permissions, the "needs you" section in the Home screen should say so. something changed on the live screen. the right side (bottons, chant modes) is stuck under the virtual scroll bar. also on the live page, if i put a command in chat (!uptime) i don't see the response from the bot account there. also the chat on the live page should stick to the height of the page and have it's own scroll bar instead of constantly growing."

**Who it's for:** every streamer, after an update that asks for new Twitch permissions, and while live.

- **Home asks you to sign in again** when your sign-in (or the bot's) lacks permissions StreamGremlin now asks for, and says in words what for: "Sign in to your Twitch account again on Connections to let StreamGremlin change your title and category and add markers, and make clips." It replaces the older tip about channel events, so it's said once.
- **Live fits the window.** Its chat and side took their content's height, so a long chat grew the whole screen past the window. The screen then scrolled, and its scroll bar sat over the buttons and chat modes. Live now takes exactly the height left: chat keeps its own scroll bar, and the side stays clear.
- **The bot's replies show on Live,** marked **Bot**, and a workflow's messages sent as you are marked **Workflow**. They were dropped so StreamGremlin would never answer itself (D-023). They're now stored as a new event type, `chat.message.sent`, which is never viewer chat:
  - no trigger offers it;
  - no workflow starts from it, whatever the trigger's pattern;
  - filters, points, First Words and viewer records ignore it.
- **Nothing to moderate on them:** these lines have no gear menu.
- **The side never scrolls sideways** (fixed 2026-11-13). The user: "the buttons and chat modes on the live page now has a horizontal scroll bar no matter how wide i make the page." Fitting the window meant the side could scroll up and down. Its scroll bar then took width the Followers-only row (a button and a menu side by side) needed, so the side scrolled sideways too. The menus now shrink and wrap under their buttons. No room is kept for the scroll bar: the user asked for the side to line up with the "Top" button, so the side reaches the right edge, and when it scrolls its scroll bar sits on that edge.

## D-088. Twitch's Blocked Terms on the Chat filters screen, and the gap under Live's chat

Decided 2026-11-13. The user: "fix the gap under the chat send box and the syncing blocked terms".

**Who it's for:** streamers who keep a word list and want Twitch to hold those words back before anyone sees them, without going to the Creator Dashboard; and anyone moving a list from Twitch into StreamGremlin's word filter.

- **A "Twitch's Blocked Terms" panel on Chat filters** lists Twitch's terms, newest first. A term AutoMod blocked for a while says until when. **Remove** takes one off Twitch's list. **Refresh** asks again; the list is read when the screen opens, never every few seconds, since each read is a request to Twitch.
- **Copying, not syncing.** Two buttons, each saying how many are new:
  - **Copy my words to Twitch** adds the word filter's words that Twitch hasn't got (the form as it is, saved or not);
  - **Copy Twitch's terms to my words** puts Twitch's terms in the word list and ticks the words filter; Save keeps them.

  Copying only adds. Nothing is ever removed from either list automatically: Twitch's list can hold AutoMod's terms and ones made on Twitch, and a mistake there would quietly unblock words. Matching is compared in lower case, as the word filter saves words.
- **Why it's still worth copying Twitch's terms here:** Twitch blocks them first, so StreamGremlin's filter only sees them if a term is later removed or AutoMod's ends. The copy makes the word filter a fallback, and keeps the list in one place.
- **Safe to repeat:** Twitch answers adding a term that's there with the term it has, and removing one it doesn't have with success, so a retry never doubles anything. When Twitch says the list is busy with another change (409, or a 400 naming a conflict), the add is tried again after 0.3 and 1 second, as the reference asks. No answer is shown as unknown, never as done.
- **Each term's answer is shown:** done, refused with Twitch's words (too short, for instance), or unknown.
- **A new Twitch permission,** `moderator:manage:blocked_terms`, so streamers sign in again; Home's Needs you says so, "to manage Twitch's blocked terms". The list is read as the streamer, so private terms are included, as the reference says.
- **No storage:** the list stays Twitch's. Not workflow steps: nobody asked, and editing the list from chat would be a moderator's command for a later request.
- **What wasn't verbatim:** Add Blocked Term's length rule and all of Remove Blocked Term were read from a summary of Twitch's reference. StreamGremlin checks only 2 to 500 characters and shows Twitch's own refusal otherwise. The capability manifest notes it, to check in the live check.
- **API and CLI:** `GET`, `POST` and `DELETE /v1/filters/blocked-terms`; `streamgremlin filters blocked`, `filters block TERM...`, `filters unblock ID`.
- **Live-checked** 2026-11-13 on real Twitch. The user: "live checks look good."
- **The gap under Live's chat:** the always-present, empty status line for moderation notices kept a line's height and margin under the send box, and the "Sign in to send" note kept its bottom margin. Empty notices now take no room (they stay in place to be announced), so chat ends at the panel's padding and its log gets the space.

## D-089. Discord, Tier 1: webhooks

Decided 2026-11-13. The user asked what Discord features would match other bots, then: "please work on discord tier 1".

**Who it's for:** streamers coming from Streamer.bot, who post to Discord through webhooks, and any streamer who announces streams in a Discord server.

**Tier 1, built:** posting through Discord webhooks, with no bot account, Discord application or live connection (ADR-0044).
- **Connections → Discord:** add a webhook by pasting its link and naming it, send a test post, forget it after asking. The link is kept with your passwords and never shown again.
- **The step "Post to Discord":** a message, a card (title, text, link, picture, colour, footer), or both, through a webhook picked by name; optionally as another name. Nobody is notified unless the step chooses @everyone or @here, whatever the text says, since posts often carry a viewer's words. Long text is cut to fit. Sent once; unknown if Discord doesn't answer.
- **"A clip is made":** a new event and trigger, added whenever StreamGremlin makes a clip (`!clip`, a button, a Stream Deck key, any workflow). Clips made on Twitch's site aren't seen: Twitch offers no event for them. Templates can't share a chat command, so posting clips hangs off this event instead of a second `!clip`.
- **The step "Sum up the stream":** counts real events since Twitch last said you went live, until it said you went offline: how long, follows, subs, gifted subs, bits, raids (and raiders), chatters and their messages. Test events and your own chat don't count. Unknown when StreamGremlin didn't see you go live.
- **"Read the stream"** also gives your channel's link, and a preview picture of the stream while live (Get Streams' thumbnail, 1280x720).
- **Three templates, in a new Discord category:**
  - **Announce on Discord when you go live:** @everyone, with a card of the title, category, preview picture and link. If Twitch can't be asked, it still posts, without the card.
  - **Post new clips to Discord.**
  - **Sum up the stream on Discord**, when you go offline.
- **Command line:** `streamgremlin discord`, `discord add NAME` (the link on standard input), `discord test NAME`, `discord forget NAME`.

**Live-checked** 2026-11-13 with real Discord and Twitch. The user: "live checks look good".

**Not built (Tier 2 and 3, waiting for a request):** a bot account, editing the announcement when the stream ends, slash commands and other Discord triggers, roles, relaying chat, linking accounts. Twitch's own Discord integration already syncs sub roles; Help should point to it if asked. Levels, Discord moderation, music, tickets and welcome messages are left to hosted bots: they need to be online all the time, and StreamGremlin runs only while your computer is on.

## D-090. Pretzel: a Now playing overlay and !song

**Replaced by D-091** the same day: the overlay reads Pretzel's files, and the API, !song and the Pretzel card were removed.

Decided 2026-11-13. The user brought a Pretzel integration spec (tmp/prezel-rock-AGENTS.md), then: "I use pretzel. i'm mostly interested in a nice overlay of the current song. the !song would be a bonus. song change trigger isn't needed right now."

**Who it's for:** the user, and streamers who play Pretzel's stream-safe music. Pretzel's help shows !song commands for Nightbot, Streamlabs and Mix It Up, so streamers coming from those bots expect one.

**What Pretzel offers (checked 2026-11-13; its help pages were updated 2026-05-27):** one documented, read-only address for bots: `https://api.pretzel.rocks/playing/twitch/{numeric Twitch user id}`. Its format isn't documented. Seen on the user's stream it answers `Now Playing: Title by Artist -> https://prtzl.io/…` as plain text, and `400 ID not found` for an account Pretzel doesn't know. Sampled every 30 seconds for 20 minutes, five songs came through in that form, all with an empty artist, so on the user's stream the overlay shows titles only. What it says while paused wasn't seen. No rate limit is published. There's no picture, no paused or stopped state, no event when the song changes, and no way for other apps to control playback. Pretzel requires its own chat attribution unless the streamer has Premium or Pro, and says a !song command doesn't replace it.

**Built:**
- **A fourth kind of overlay, Now playing:** the title and artist (a missing artist leaves no gap) beside three bouncing bars, in the same looks as alerts (box, font, colours, position, animation). New overlays start bottom-left with "Picture, then words". It stays up while a song plays, or shows each song for 2–60 seconds as it starts (`song_hides`). A change slides the old song out, then the new one in. Song names are set only as text.
- **When Pretzel is asked:** every 15 seconds while a Now playing overlay is open, in OBS or the app's preview, and right away when one opens; by the step; and by Check. Nothing is asked otherwise, so there's no switch to turn it on. A check moments after another, for the same account, reuses it, so a busy !song doesn't hammer Pretzel. A 429 or 503 is left alone for its Retry-After, up to 5 minutes.
- **Unknown is shown as unknown:** a song stays up through one failed check, so a moment's trouble doesn't blink it away. After two failed checks in a row it goes. The overlay shows only a song Pretzel said is playing, never Pretzel's other answers.
- **Reading the line:** split at the last " by ", so "Stand by Me by Ben" works. An artist with " by " in its name can't be told apart. Only a `https://prtzl.io/…` link at the end counts as Pretzel's link. Control and direction-changing characters are dropped, and each part is capped at 200 characters.
- **Connections → Pretzel:** Check what's playing, the latest result, and Pretzel's attribution rule.
- **The step "Get the current song (Pretzel)"** (group Music): state, title, artist, text, link, and `chat`, ready for chat. **The Song template** answers `!song` with it.
- **Command line:** `streamgremlin pretzel`, `overlays add -song NAME`. **mockpretzel** stands in for Pretzel, and the engine's `-pretzel-api-url` takes a loopback address only. Acceptance tests point Pretzel at a closed port.
- **Storage:** migration 0031 rebuilds the overlays table to allow the new kind, as 0018 and 0019 did. Saved looks are unchanged; `song_hides` defaults to off.

**Not built:**
- **Album art:** the API has none. Pretzel's desktop app writes the cover to a JPG (Write to File), and OBS can show that file with an Image source. Reading local files to put them on the overlay waits for a request.
- **A song-changed trigger:** the user doesn't need it now. The checks already notice changes, so it's cheap to add later.
- **Playback control and choosing stations:** Pretzel offers no way to do it. The spec's pressing of Pretzel's hotkeys was refused: it's fragile, needs OS accessibility permissions, and could send keys to the wrong window.
- **A general music-provider layer:** the spec's provider interface for Spotify and others isn't built. There's one provider, and the rule is no abstractions for an undecided future.

**Fixed the same day:** the overlay server didn't serve the new `song.js`, so the page's script failed to load and no overlay of any kind connected in OBS. A test now loads every file the page imports.

## D-091. Now playing from Pretzel's files

Decided 2026-11-13. After D-090's overlay showed no picture, the user asked: "since we can't show the album art from the api, should we switch at least some of the info to the local files generated by the local prezel.rocks app? we would have to configure the path in the overlay settings". Then: "on second thought, let's remove the pretzel features. i think it's cleaner to just use the files it generates and build an overlay that way".

**Who it's for:** the user, and streamers who play Pretzel through its desktop app. Pretzel's help suggests showing these files in OBS as Text and Image sources; this overlay shows them together, styled like the streamer's alerts.

**Removed:** everything that used Pretzel's API: the checks every 15 seconds, the step "Get the current song (Pretzel)", the Song template (`!song`), Connections → Pretzel, `streamgremlin pretzel`, mockpretzel and `-pretzel-api-url`. StreamGremlin no longer contacts Pretzel at all.

**Built:**
- **The Now playing overlay** (kind `song`, migration 0031 from D-090) reads two files Pretzel's desktop app writes with Write to File: the song as text and the cover as a JPG. Each overlay has its own paths in its settings, plus Pretzel's paused text. They're full paths: `.txt` for the song, `.jpg`, `.jpeg` or `.png` for the cover.
- **Reading:** only while the overlay is open, in OBS or the app's preview, every half second. A file is read once it looks the same twice in a row, so one caught half-written is never shown. A new song shows within about a second.
- **What's shown:** the first non-empty line is the title and the second the artist; more lines are ignored. Control and direction-changing characters are dropped, and lines are capped at 200 characters. An empty file, or one that says the paused text (ignoring case), shows nothing. A missing or unreadable file shows nothing, and the settings say why.
- **The cover:** sent to the page as `art?v=…` through the overlay's private link, and named anew when it changes. It's served only if it's a JPEG or PNG under 5 MB, so a path to any other file serves nothing. The page's CSP allows pictures from its own origin. Without a cover, bars bounce beside the song.
- **The paths stay in the engine:** they're in the overlay's look, but taken out of what the page is sent. Only the app and CLI see them.
- **The settings** say what's showing, or what's wrong, while the overlay is open. A path pasted with quotes around it works. Command line: `overlays look NAME -song-file PATH -art-file PATH -paused-text TEXT -song-hides`. `overlays` lists what each Now playing overlay is showing.

**Not built:**
- **Telling a song from a quit Pretzel:** if Pretzel quits without clearing its files, the last song stays up. What Pretzel writes when it quits wasn't seen.
- **!song:** removed with the API. It could come back reading the same file, if asked.
- **A song-changed trigger.**
- **Other music apps:** any app that writes the song to a text file works the same way, but only Pretzel is described.

**Added the same day:** at the user's request, "file selector popups for the files so we can just browse for them", and notes on the recommended size. **Browse…** beside each path opens the system's own open dialog, through the desktop app's `POST /desktop/choose-file`, like its save dialog. The app names a kind only (`song_text` or `song_art`); the dialog's title and filter come from the kind, and only the chosen path comes back. Nothing is read until the look is saved, and the engine checks the path then. Help and the overlay's settings recommend a 1920 × 1080 browser source, or about 1100 × 200 at the usual text size: the card is up to 720 px wide but never more than 70% of the source, and about 110 px tall (96 px with "Picture, then words").

**Made generic the same day:** the user: "can we make it more generic. this isn't prezel specific anymore since any audio players could write these files". Nothing in how the files are read was Pretzel's, so only words changed: the screens, Help, the CLI's flags and the problems the settings show speak of "your music player", with Pretzel's Write to File as one example. The "Pretzel's files" settings are now "Song files".

## D-092. Clocks, countdowns and stopwatches on overlays

Decided 2026-11-13. The user asked: "can we add some example plugins for time. a configurable clock, a countdown timer, and a stop watch. ideally, all in one plugin. but if it's easier, different ones will work as well. to goal is to allow overlays to show the current time (maybe date as an option), a stream starting countdown, etc." Plugins can't draw on overlays (Lua plugins have no screen; themes only restyled wheels), so the user was asked how to build it, and chose "Timer overlay + example look".

**Who it's for:** the user, and any streamer with a "starting soon" screen or a clock on stream; Streamer.bot users do this with OBS's own text sources and scripts.

**Built (ADR-0045):**
- **A Clock and timers overlay:** one overlay shows a clock (12- or 24-hour, seconds if chosen, the date short or long if chosen), a countdown (a length, or until a time, and text at zero, like "Starting now!") or a stopwatch, with a label above it, in the same looks as alerts. Add one for each on screen at once.
- **Countdowns and stopwatches survive restarts:** the engine keeps their state and sends it to every page, and the page counts the seconds.
- **Controls:** Start, Pause, Reset, +1 minute and "Count down until" on the Overlays screen. Steps: **Start a countdown or stopwatch** (for minutes, or until a time), **Pause…**, **Reset…**, **Add time to a countdown**, so buttons and Stream Deck keys can. The trigger **A countdown ends** runs workflows at zero, like switching to the first scene. A countdown that ran out while StreamGremlin was off doesn't fire late.
- **Looks for timers:** a theme can be `"for": ["timer"]`, in the same sandbox as wheel looks, and the Overlays screen offers each kind only its own looks. **Flip clock** (`examples/themes/flip-clock`: zip the folder to install it) is the example: split-flap cards that flip as digits change, for all three modes.
- **Command line:** `overlays add -timer NAME`, `overlays look NAME -mode countdown -length 5m -label … -end-text …` (or `-mode clock -24h -clock-seconds -date short`), `overlays timer NAME start [-minutes N | -until 19:00] | pause | reset | add -minutes N`.
- **Fixed on the way:** saving a look checked kind-only settings (song files, timers, themes) against the overlay's kind only when a theme was set; it always checks now.

**Not built:** time zones other than this computer's; counting up from a past time ("live for…": Read the stream's uptime does that); several timers on one overlay; sounds at zero (a workflow on "A countdown ends" can play one).

**Added the same day: start on load.** The user: "the timer should have an option to auto-start on load/reload". A countdown or stopwatch can be set to **Start it whenever OBS loads the overlay**: each time OBS loads its page (OBS starting, or the browser source refreshing, including OBS's "Refresh browser when scene becomes active"), a countdown starts afresh from its length and a stopwatch from zero. The page says `loaded` once per load, never on a reconnect, so StreamGremlin restarting doesn't reset a running countdown; the app's preview never starts it; loads within 3 seconds of each other (two sources on one overlay) start it once. Command line: `-auto-start`.

**Added the same day: built-in timer looks.** The user: "can you add styles for them that match the look of the ones for the wheel?" Workshop, Neon arcade and Selector board now come in a version for clocks and timers (`builtin:workshop-timer`, `builtin:neon-arcade-timer`, `builtin:selector-board-timer`), made of their wheel's materials: the label in the look's title plate, and the time on a parchment plate in brass and wood, in neon tubes on the arcade cabinet, or on the board's lit gold row between its marker lines. They run in the same sandbox as the wheel looks, with one shared script (`themes/shared/timertheme.js`). Settings: show the label, and the accent colour. They have no sounds. On the command line, `-look workshop` on a timer picks the timer version.

**Live-checked** 2026-11-13, with D-093's fixes, in the app and OBS. The user: "looks good".

## D-093. Overlays' animations: a static choice, and looks that come in properly

Decided 2026-11-13. The user: "when doing the slide-in, pop-in, etc, the Look isn't correct. It seems to be the default. also, I think we need a "static" option so there is no motion when it starts. you may need to review the other customized things other than clocks/timers as well".

**What was wrong**, found by opening the real overlay pages in a browser:
- **A clock or timer with a look (Workshop, Flip clock…) came in as Classic.** The overlay's animation ran on the Classic box while the look loaded, and then the look replaced it with no motion of its own.
- **Looks ignored the overlay's position:** they always sat in the middle.
- **Unfold broke anything without a picture to unfold from.** A Classic clock or timer, and a giveaway's panel, lost their box and were cut off at the left. Alerts and Now playing have a picture or bars, so they were fine.

**Changed:**
- **Static** ("Static (no motion)", `none`): for every kind of overlay. It shows, and goes, at once.
- **A clock or timer look comes in itself:** nothing shows while it loads. Its frame then slides, pops, unfolds, or just appears, as the overlay says, from where the overlay is placed. If the look isn't ready in 5 seconds, Classic shows until it is.
- **Looks are told the overlay's position** (`timer.position`, top-left to bottom-right), and the built-in timer looks and Flip clock (1.1.0) sit there, as Classic does.
- **Unfold without a picture:** a clock, timer or giveaway's whole box unfolds, toward the middle.

**Checked:** each kind's animations in a real browser, with the page served by the engine. Alerts, Now playing, and wheels' spins were already right. Wheel looks still sit in the middle: a wheel fills its overlay.

**Live-checked** 2026-11-13. The user: "looks good".

**Live-checked 2026-11-13: D-091's files.** Now playing from Pretzel's real Write to File files passed on M7's real stream, on macOS and Windows. The user: "live stream checks went well.  no issues found".

## D-094. Two races the tests caught: OBS's status and ads' breaks

Decided 2026-11-13. The user asked to fix a test that failed once under `make verify` and passed alone. A second did the same while fixing it. Each was a real, rare bug, not only a test's:

- **OBS: "connected" with no connection.** Turning OBS on just before StreamGremlin first read its settings left the change's signal waiting. The session that followed ended at once and reconnected, and in between the status said connected while a scene switch failed with "not connected". Now a signal from before the settings are read is dropped. A session's end, dropping the connection and leaving "connected", is one step. A test forces the case: one save, one session.
- **Ads: a break not linked to its request.** Twitch's "ad break began" can arrive before Twitch answers the request: EventSub and the API are separate. The request was still pending, so the break was recorded without it, for good. Now a break links to a pending request too. Twitch's answer keeps the link, and no answer, with the break begun, is accepted rather than unknown. A test holds the answer back until the event is in.

## D-095. M6, YouTube: one combined chat, matching Streamer.bot, each streamer's own quota

Decided 2026-11-13, after M7 closed. The user: "draft the M6 proposal with a combined chat. we should try to match streamer.bot features here." Then: "yes, linked account share points. yes, send box if for both by default." And: "no vertical, not monetised, option 4 is fine". The plan is in [docs/milestones/M6.md](milestones/M6.md).

- **One combined chat on Live,** each message marked with its platform. Replies, command answers and moderation go to the platform a message came from. **The send box sends to both by default.**
- **Streamer.bot's YouTube triggers, matched** where YouTube's documented API allows: about 20 of its 26. Present Viewers becomes "active chatters": YouTube says nothing about who is watching. Jewels and polls are built only if their message types are documented when slice 4 is built.
- **Chat and its events are pushed** (`liveChatMessages.streamList`, gRPC, 5 units per connection). **Subscribers can't be:** Google has no push for them. They're asked for each minute while live, public subscriptions only, and the alert says so.
- **Quota is shown and budgeted.** Timeouts and bans cost 200 units, so YouTube's chat filters delete by default.
- **Quota: each streamer's own Google project first,** with a guide in the app; StreamGremlin's own project once Google's verification and quota increase are approved. Quota is per project, so a shared one would run out. Client IDs and secrets are given at build time, never committed.
- **Linked viewers share one points balance** (`!link`, D-045). Unlinked, a YouTube viewer is their own viewer.
- **Later:** several broadcasts at once, scheduled broadcasts, new-upload posts, cuepoints. **Leave:** comments, community posts, clips, retention.

## D-096. M6 slice 1: YouTube sign-in through Google's desktop flow, with the streamer's own project

Decided 2026-11-13, building M6 slice 1 after the user's "proceed". The design is [ADR-0046](adr/0046-youtube-sign-in.md). In short:

- **Google's flow for desktop apps, not Twitch's device codes.** Google's device flow only allows the `youtube` and `youtube.readonly` scopes, and Google recommends the loopback flow for desktop apps. The browser opens Google's page, which comes back to a one-time address on this computer. PKCE and a random state every time.
- **One scope, `youtube.force-ssl`,** for both accounts: sending chat, moderating and the broadcast all need it or the broader `youtube`.
- **The streamer's own Google project,** pasted on Connections with its client secret, as D-095 decided. The card has a step-by-step guide to making one, including publishing it: a project left in Testing has its sign-ins ended by Google after 7 days. A build can carry StreamGremlin's own project once Google approves it, set at build time and never committed.
- **States like Twitch's:** not configured, signed out, signing in, connected, needs signing in again, unknown. A sign-in is checked by refreshing it (no quota) at start and every 50 minutes.
- **Two accounts, two channels.** The bot can't be your own channel. A refused sign-in of a channel that's already signed in isn't revoked, because Google ends the whole grant.
- **Quota is counted by StreamGremlin,** since Google doesn't report it: each call's documented cost, per Pacific day. When Google says it's used up, nothing more is asked until midnight Pacific.
- **`mockyoutube`** stands in for Google's sign-in and the Data API, in tests and to try it (`-youtube-url`).


## D-097. M6 slice 2: reading YouTube chat, log only until the combined chat

Decided 2026-11-14, building M6 slice 2 after the user's "proceed". They accepted the proposal of checking every 2 minutes while not live. The design is [ADR-0047](adr/0047-youtube-chat-stream.md). In short:

- **Finding the broadcast:** `liveBroadcasts.list` (1 unit). It runs every 2 minutes while you're not live, and at once when your channel signs in, when your Twitch stream starts, and on **Find my stream**. It never runs while chat is being read.
- **Reading chat:** Google's gRPC stream (`liveChatMessages.streamList`, 5 units a connection), with Google's gRPC library and no generated code. A dropped connection or a restart resumes from the last page token, so nothing is missed or repeated. Messages from before StreamGremlin was reading are marked as backlog.
- **YouTube text messages are stored** as `chat.message.received` with source `youtube`; the bot's own are stored as `chat.message.sent`.
- **Only the event log reads them in this slice.** Workflows, commands, filters, points, giveaways, first words, viewers, the Discord summary, timers and Live's chat list skip YouTube events (`events.LogOnly`) until the slice that brings each one: Live's combined chat, commands and workflows in slice 3, moderating in 5, viewers and points in 6. This way no part of the app treats a YouTube viewer as a Twitch one in the meantime.
- **Connections and Live say whether YouTube chat is being read,** and why not: not live, quota used up, signed out, or lost and trying again.
- **Other message types wait for their slices:** Super Chats and memberships for slice 4, deletions and bans for slice 5.

## D-098. M6 slice 3: one combined chat

Decided 2026-11-14. The user said "proceed" to this proposal: "Commands and the 'Someone chats' trigger work on both… Each trigger gets a platform filter (**Any**, **Twitch** or **YouTube**), defaulting to **Any**", and "The default is **Where it came from**… Workflows started by anything else (follows, timers, buttons) send to Twitch only."

- **Live shows Twitch and YouTube chat in one list.** Each line is marked TW or YT, and both platforms' roles use the same words. YouTube lines have no moderating buttons until slice 5. Their viewer card shows the channel link and recent messages until slice 6.
- **The send box goes to both chats by default** while YouTube chat is being read. You can switch it to just Twitch or just YouTube. It sends as the bot on each platform where one is signed in. A line under the box says each YouTube message costs 20 units.
- **Sending to YouTube** uses `liveChatMessages.insert`, at 20 units a message. Google's reference gives no length limit; YouTube's chat box takes 200 characters. So longer messages are split at a space into up to 3. Nothing is retried. A workflow's own message sent as you is recognised and stored as sent, so it starts nothing. The tracker that does this for Twitch is now shared (`chat.OwnSends`).
- **`internal/chatsend` decides where a message goes:**
  - Workflows' "Send a chat message" step has a new **Where** option: Twitch, YouTube, or both.
  - Left unset, the step answers where the message came from: YouTube for a YouTube viewer's message, Twitch for everything else (follows, timers, buttons). So existing workflows never spend YouTube quota without being told to.
  - With both, YouTube is skipped while you're not live there.
  - If only one platform refuses, the result says which; it's only an error when every platform refuses.
  - The API's `POST /v1/chat/messages` takes `to` and returns what each platform did.
- **Replies:** a reply goes back to the platform its message came from. On YouTube, which has no replies, the answer starts with `@name`.
- **Triggers:** "Someone chats", "A viewer types a command" and "A viewer chats for the first time" hear both platforms unless the trigger's **Which chat** (its `sources`) says Twitch only or YouTube only. A test event for a YouTube-only trigger comes from YouTube (`events simulate -source youtube`).
- **YouTube `!commands` become `chat.command.received`,** except those in the backlog: messages from before StreamGremlin was reading start nothing. Cooldowns are shared across both platforms.
- **Roles:**
  - your channel counts as the broadcaster;
  - YouTube moderators count as moderators;
  - channel members count as subscribers (`role` subscriber, `is_subscriber`, plus `is_member`);
  - YouTube has no VIPs.
- **First words and timers' chat-line count** include YouTube from now on.
- **Still log-only for YouTube:** chat filters (slice 5); points, giveaways and viewers (slice 6); and the Discord stream summary.
- **Add-ons' own chat messages** (plugins' `chat.send` permission) still go to Twitch only; a decision is needed before they reach YouTube.

## D-099. M6 slice 4: YouTube's events as triggers and alerts

Decided 2026-11-14. The user said "proceed" to both recommendations: memberships use the subscription triggers, and existing go-live and go-offline triggers are kept to Twitch once.

- **Where the events come from:** YouTube's chat stream, which slice 2 already reads, so there's no extra quota.
- **New event types**, from source `youtube`:
  - `monetization.super_chat.received` and `monetization.super_sticker.received`: the amount as YouTube shows it (`amount_display`), as a number (`amount`), the currency, and the tier. No currency conversion.
  - `monetization.jewels.gifted`: the gift's name, its Jewels and how many.
  - `chat.members_only.changed`, with `enabled`.
  - `moderation.user.banned` and `moderation.user.timed_out`, about the banned viewer, with who did it.
- **Memberships use the subscription types**, as members count as subscribers (D-098): becoming a member or receiving a gifted membership is `monetization.subscription.started`, a milestone is `.renewed`, and gifting memberships is `.gifted`.
  - Each has the level's name in `level`, and `tier` 0, since YouTube's levels have names, not numbers.
  - Existing sub workflows and alerts now work for YouTube members, and answer on YouTube.
  - The editor's triggers are renamed: "Someone subscribes or becomes a member", and so on.
- **Your YouTube broadcast going live and offline** is `broadcast.channel.online` / `.offline` from `youtube`.
  - Going live is recorded when its chat first answers, once per broadcast. Offline is recorded when its chat ends.
  - A broadcast whose chat is turned off never goes live here.
- **Existing workflows are kept to Twitch, once:** at the first start of this version, every go-live and go-offline trigger with no platform is set to Twitch only, as a new version of each workflow. YouTube is opt-in through the trigger's **Which platform**.
  - Deleted workflows are left as they are.
  - So are workflows imported later from older files.
  - The Discord go-live and stream-summary templates now say Twitch only.
- **StreamGremlin's own idea of "live"** keeps following Twitch and OBS, never YouTube, for now: points for watching, first-words resets, timers' "only while live", giveaways' stream window and the Discord summary.
- **New templates** under "YouTube events": thank Super Chats, Super Stickers and Jewels, welcome new members, and thank gifted memberships. Most show an alert and answer in YouTube chat. There's also a Discord go-live post for YouTube.
- **Events** describes each event in words. A trigger that can come from either platform has **Which platform**. Test events can come from YouTube.

## D-100. M6 slice 5: moderating YouTube from Live

Decided 2026-11-14. Before building, I checked Google's current docs and found three things that changed the plan. I recommended five defaults and the user said "proceed". The design is in [ADR-0041](adr/0041-moderation-record.md) under "YouTube".

- **What Google's API allows:**
  - deleting a message (20 quota units);
  - a ban or a timeout (200);
  - undoing one (200), but only by the id YouTube gave when it was made, and Google has no way to list bans;
  - adding or removing a moderator (200; the chat's owner only); listing moderators costs 50.
  - There's no reason field, no warnings and no VIPs.
  - Members-only mode and slow mode can't be changed through the API at all.
- **Members-only mode is left out of Live's controls.** Live says when it last turned on or off (from slice 4's event) and to use YouTube Studio.
- **Unban is offered only for a ban or timeout made from StreamGremlin.** Its YouTube id is kept in the moderation record (new column `result_id`). The card offers Ban otherwise, and says to undo other bans in YouTube Studio.
- **Always as your channel**, never the bot: the owner may do everything, while the bot would need to be a moderator, and a refusal there is confusing.
- **Chat filters watch YouTube too, and delete there by default.**
  - A timeout rule times out on YouTube only with "Time out on YouTube too", and never with fewer than 1,000 of the day's units left.
  - A warning rule deletes on YouTube, which has no warnings.
  - Each catch says on YouTube, with a note when it was changed.
- **Moderators are looked up only when needed.** The card knows a moderator from their message (YouTube marks them) or from StreamGremlin making them one. YouTube's list (50 units) is asked only to remove a moderator added elsewhere, and kept 10 minutes. This is cheaper than recommended: the list isn't read when a card opens.
- **Deleted messages are struck through on Live**, whoever deleted them.
  - YouTube's tombstones and StreamGremlin's own deletes are stored as `chat.message.deleted` (source `youtube`), once each.
  - Google's wording leaves open whether a deletion sends a tombstone.
  - It's not a trigger: it marks a message rather than being news.
- **A "Moderate the viewer" workflow step** (Chat, under More steps): delete the message being answered, time out or ban its sender, on the platform it came from, through the same record, once per step.
  - It works for Twitch events too. The M6 plan listed these steps for YouTube, and a YouTube-only step would be odd beside the Twitch chat it answers.
  - Twitch had no moderating step before, and none was asked for. Removing the Twitch half is easy if it isn't wanted.
- **Every quota cost is said where it's spent:** in the confirmation before an action, on the card, in the filter settings, in the step's description and in Help.
- **The CLI** has `streamgremlin mod -youtube …`, naming viewers by channel id until YouTube viewers have records (slice 6).

## D-101. M6 slice 6: YouTube viewers, points and `!link`

Decided 2026-11-14. I proposed this slice with five recommendations and the user said "proceed". The design is [ADR-0048](adr/0048-viewer-links.md).

- **YouTube chatters are viewers**, recorded like Twitch's from real chat and events (never backlog or test events), with points, a card and a place on the Viewers screen.
  - A YouTube viewer's login is their @handle, lower case, so `@name` works on both platforms.
  - Every part that skipped YouTube until now takes it: viewers, earning, giveaways and the Discord stream summary. `events.LogOnly` is gone.
- **`!link` (recommendation 1, changed to be safe):**
  - The proposal was "`!link` gives a code; type it on the other platform". A code said in public chat could be typed first by anyone reading it, joining their account to someone else's balance.
  - So the first `!link` names the other account (`!link @handle` on Twitch, `!link twitchname` on YouTube), and only that account's `!link CODE` works.
  - Codes are 6 digits from `crypto/rand`, last 10 minutes and work once. Five wrong tries in 10 minutes stop an account.
  - There's a "Link Twitch and YouTube" template under Chat commands, and the streamer can link and unlink from the Viewers screen or the CLI.
- **Balances add up when linked (recommendation 2).**
  - The YouTube viewer's balance moves into the Twitch viewer's, with ledger adjustments that say why. From then on, both accounts' points go to one balance.
  - Unlinking leaves the shared points with Twitch; YouTube starts from 0. Only the streamer unlinks.
- **Giveaways (recommendation 3):**
  - YouTube viewers enter while YouTube chat is read.
  - What's said to everyone goes to both chats (20 quota units a message on YouTube), and entrants hear in their own chat, batched to fit YouTube's 200 characters.
  - Rules that need Twitch (followers, account age, months subscribed) turn YouTube viewers away, saying so.
  - Linked accounts enter once. Unlinked, they can't be told apart, and the form says so.
- **Earning (recommendation 4):**
  - Members earn as tier 1 subs, milestones as resubs, and gifted memberships as gifted subs, for the gifter.
  - A new rule, "Each YouTube Super Chat or Super Sticker", is a flat amount, off by default: no currency conversion.
- **"Live" still follows Twitch and OBS (recommendation 5).** Time in chat pays YouTube chatters while you're live on Twitch. YouTube going live or offline doesn't change it.
- **One person is paid once:** time in chat, gives to groups and the wait between chat awards count the shared balance's holder.
- **Names that are two people:** when `@name` is a Twitch viewer and a different, unlinked YouTube viewer, `!give` and similar steps say so instead of guessing; the CLI asks for the ID.
- **The Discord stream summary** counts YouTube chatters with Twitch's, and adds `members`, `gifted_memberships`, `super_chats` and `youtube_chatters`. YouTube going offline doesn't end it.
- **Also:** a message to YouTube that would say "@@name" (a template's `@${event.actor.display_name}` with a handle) says "@name".

## D-102. M6 slice 7: YouTube's viewer count, title, subscribers, polls and active chatters

Decided 2026-11-14. I proposed this slice and the user said "proceed". Checked against Google's docs the same day (videos.list and .update, subscriptions.list, liveChatMessages insert and transition, stream_list.proto).

- **The minute check.** Google pushes none of this, so while your broadcast's chat is read StreamGremlin asks each minute: `videos.list` (1 unit) for the viewer count, title and description, and `subscriptions.list` with `myRecentSubscribers` (1 unit). About 120 units an hour. The first check is at once when the chat connects.
- **The viewer count** shows on Live (YouTube's line and a **YouTube stream** panel), as unknown, never 0, when YouTube doesn't say. A new trigger, **The viewer count changes**, fires for Twitch and YouTube when the count moved by a tenth or more (at least 1) since it was last announced; the first count of a stream is only noted.
  - Changed from the proposal: "you choose how big a change counts" isn't possible, because triggers only match words, and StreamGremlin has no conditions. The fixed rule keeps it to the changes that matter, at most once a minute.
  - Twitch's count comes from Get Streams once a minute (no quota), in the new `audience` package.
- **Title and description:** changed from Live, a step ("Change the YouTube title or description") or the CLI, with `videos.update` (50 units) after reading the video (1 unit), sending the snippet back whole because Google deletes any snippet field an update leaves out. `liveBroadcasts.update` costs 1 unit but needs `contentDetails.monitorStream` and resets the privacy status when it's left out, so it wasn't used. A title or description changed anywhere fires **Your title or category changes** from YouTube.
- **Subscribers are follows.** A YouTube subscription is free, like a Twitch follow, so a new public subscriber fires **Someone follows or subscribes on YouTube** (`audience.follow.created`, source youtube), not "Someone subscribes or becomes a member" (paid). So follow points and follow alerts apply.
  - Changed from the proposal, which said it would fire "Someone subscribes": that trigger is for paid subs and members, and would have paid sub points for a free subscription.
  - Only subscriptions made after the broadcast started count, once per channel ever, so a restart or a later stream doesn't repeat anyone. Private subscriptions are never listed, and Google says the list "might be limited".
- **Polls**, documented after all: `liveChatMessages.insert` with type `pollEvent` (2 to 4 choices, 20 units) starts one; `liveChatMessages.transition` to closed (20 units) ends it. YouTube polls have no timer: one runs until it's ended. The stream sends the poll when it's made and the running poll (`active_poll_item`) in each answer, with votes for the chat's owner. The poll triggers fire for YouTube with Twitch's payload. A poll is shown and run on **Polls**, and by the steps "Start a YouTube poll" and "End the YouTube poll".
  - A poll that leaves the stream's answers ended (closed on YouTube's page). Whether Google sends an answer for each vote isn't documented; votes show when the next answer comes.
  - StreamGremlin's own limits, since Google gives none: a question up to 100 characters, a choice up to 35.
- **Active chatters:** every 5 minutes while you're live on Twitch or YouTube, the people who spoke in either chat in the last 10 minutes (**Active chatters**, `chat.chatters.active`), never you, history or test events, and only when someone spoke. It never claims who's watching. Changed from the proposal's "each minute": a minute would store 60 events an hour for little use.
- **Workflows made before this slice** keep their follow, title and poll triggers to Twitch, once, as D-099 did for going live. New triggers hear both unless **Which platform** says otherwise.

## D-103. Add-ons see YouTube and any later chat

Decided 2026-11-14. Until now an add-on's `sg.chat` sent to Twitch only, even when the event that ran it came from YouTube. The user: "add-ons should see youtube and any future chats".

- Add-ons get every platform's chat and events the way workflows do, and nothing in an add-on names a platform unless it wants to. A platform added later reaches add-ons without changing them.
- **Built in M6 slice 8** (I proposed it; the user said "proceed"):
  - `sg.chat` sends through the same path as workflows' "Send a chat message" (`chatsend`). Without `to`, it answers on the platform whose event ran the call (`event.source`), or, when no platform's event ran it (a button, a timer, `sg.after`, StreamGremlin's own events), in every chat being read: Twitch, and YouTube while its chat is read.
  - `sg.chat(text, {to = "twitch" | "youtube" | "both"})` chooses; `both` is every chat platform. An existing add-on works unchanged.
  - Future platforms: `chatsend.Platforms` lists the chat platforms. A new one added there reaches "both" and "where it came from" for workflows and add-ons alike. The add-on guide (ADR-0038's amendment) says to read `event.source` and `event.actor.platform` rather than assume Twitch.
  - The `chat.send` permission covers every platform; its wording now says so, and that each YouTube message costs 20 quota units. The 20-a-minute limit counts messages, whatever the platforms.
  - The activity log says which chats each message reached ("to Twitch and YouTube: …") and which refused. A chat skipped because you're not live on YouTube isn't a failure and isn't said.
  - The example **Dice tray** (1.4.0) answers each viewer where they typed, and says an open round in both (`to = "both"`).

## D-104. M8, Protection: Shield Mode, a panic button, bulk bans from a file, a raid guard, new-account rules and follow-bot review (amends D-006)

Decided 2026-11-14. A user asked about protection like Sery_Bot's (a free Twitch moderation bot: spam and ASCII filters, hate-raid and follow-bot protection from a bot list it keeps centrally, and turning Shield Mode on and off). I suggested our own version, through Twitch's documented API only, without a bot list of ours. The user: "i'll be the "who asked" for this. let's put it before release work. for the "act on it's own" question, i'll go with your recommendations." Then: "since we don't want to maintain our own bot list, can we add a "bulk ban" sort of function and let the user import their ban list from a file that the app would use to add to the twitch side ban list? sort of like the commandroot tool does via the browser but from our app instead".

- **Who will use it:** the user, for their own streams; then the streamers StreamGremlin is for (D-001).
- **A new milestone, M8. Protection, comes before the release work.** The plan is in [docs/milestones/M8.md](milestones/M8.md).
- **What acts on its own:** only the chat filters (as now, and the new rules in them). The raid guard alerts by default and locks chat only when the streamer chooses that. Follow-bot blocking and bulk bans always ask first.
- **No bot list of ours:** StreamGremlin keeps no shared list of bad accounts (it would need a server of ours, and guesses about who is a bot). Instead, the streamer's own lists: bans imported from a file, added to Twitch's ban list, and Twitch's ban list exported to a file.
- **Twitch only for now:** YouTube's API has no Shield Mode, members-only or follower switch. YouTube keeps its filters and moderating (D-100).
- Checked 2026-11-14 against Twitch's docs; each slice checks again: Shield Mode (Get and Update Shield Mode Status, `channel.shield_mode.begin`/`.end`, `moderator:read:shield_mode`/`moderator:manage:shield_mode`); Ban User one user a request (`moderator:manage:banned_users`, reason up to 500 characters, 429 when over the per-minute limit, no bulk endpoint); Get Banned Users (up to 100 user IDs at once); Get Users (100 a request, `created_at`; deleted and suspended accounts aren't returned); Block User (`user:manage:blocked_users`). Not documented: that blocking removes a follow, and any way to remove a follower; Twitch's chat messages carry no "first time chatter" flag, so StreamGremlin's own record of who it has seen is used.

## D-105. M8 slice 1: Shield Mode and Lock chat

Decided 2026-11-14, building M8 slice 1 after the user's "proceed".

- **Shield Mode:** Twitch's Get and Update Shield Mode Status, and `channel.shield_mode.begin`/`.end` stored as `moderation.shield_mode.changed` (enabled, moderator). StreamGremlin only turns it on and off; what it does is the profile the streamer set up in Twitch's Mod View, which Twitch's API doesn't expose. Sign-in asks for `moderator:manage:shield_mode`, so the streamer signs in to Twitch again once.
- **Lock chat** (the panic button) is StreamGremlin's own: Shield Mode on, then emote-only and followers-only (followed at least 10 minutes, or longer if followers-only already asked for longer). It acts at once, without asking: it's for the moment a raid starts, and **Unlock chat** puts back only what the lock changed (what was on before stays on). What was there is kept in the moderation record, so an unlock works after a restart. One lock at a time: a second is refused until it's unlocked. A part Twitch refuses is said, and what worked stays done; no answer is unknown, as for all moderating.
  - Why 10 minutes: a follow bot can't follow and chat at once, and real followers of 10 minutes or more still can.
- **Where:** the Chat modes panel on **Live**; `mod shield on|off`, `mod lock` and `mod unlock` on the CLI; the steps "Turn Shield Mode on or off" and "Lock or unlock chat"; the trigger "Shield Mode turns on or off"; the templates "Shield Mode on when I go offline", "Shield Mode off when I go live" and "!lock and !unlock chat" (mods and you) under Moderation.
- **Twitch only:** YouTube's API has no equivalent (D-104).
- **Decluttered (2026-11-14, after slice 2).** The user: "i think the chat modes section on live is a little cluttered. and the "uses the settings you chose in twitch's mode view" but doesn't link to it or anything. can we maybe move that and the description to "lock chat" to an on-hover for the button?" Lock (or Unlock), Shield Mode and a small **Mod View ↗** button now share one row. **Mod View** opens `https://www.twitch.tv/moderator/<your login>` in the browser, where Shield Mode's settings are; the app's list of pages it may open allows exactly that path. The explanations moved into each button's hover text, and are still read out by screen readers. "Locked" is one short line above the row.

## D-106. M8 slice 2: ban lists from files

Decided 2026-11-14, building M8 slice 2 after the user's "proceed". The design is [ADR-0049](adr/0049-ban-lists.md). In short:

- **A new Ban lists screen** (after Chat filters), `streamgremlin bans`, and the routes under `/v1/ban-lists`. A list is imported from a file (one name or id a line, or a CSV with a login or id column, up to 5 million lines), or copied from Twitch's own ban list.
- **Checked first, shown, then confirmed:** counts and a sample of what will be banned, what's already banned, what wasn't found, and who's left alone (you, your mods and VIPs, viewers with points; the last can be turned off). Nothing is banned until **Start banning**.
- **At Twitch's pace, in the background:** one account a request, keeping 200 of Twitch's rate-limit points for everything else, waiting while you're live (on by default), pausing in words on a sign-in or permission problem. Progress, the pace and an estimated finish are on the screen.
- **Picks up where it left off:** each name's state is saved before and after its request; a ban whose answer never came is settled from Twitch's ban list, never sent twice. Tested by killing the engine part way.
- **Syncing:** a newer file under the same name checks and bans only its new names; names gone from it are offered for unbanning (only bans this list made). **Check against Twitch** finds bans lifted by hand, which are left alone unless the streamer chooses to ban them again. **Undo** lifts only the list's own bans; forgetting a list unbans nobody.
- **Changed from what I described before building:**
  - The bot doesn't share the work yet. It would need the bot to sign in again with ban permission, and a second runner. Left for later: ask if wanted.
  - "Export" became **Copy Twitch's ban list** into a list, plus **Save to a file** for any list. A million-name export takes many requests, so it runs in the background like the rest.
  - The app saves up to 16 MiB through its save dialog (about a million names). The CLI streams any size.
- No new Twitch permission: `moderator:manage:banned_users`, `moderation:read` and `channel:read:vips` are already asked for.

## D-107. M8 slice 3: the raid guard

Decided 2026-11-14, building M8 slice 3 after the user's "proceed". As D-104 set, it warns by default and locks chat only when chosen, and never unlocks on its own.

- **What counts:** a chat message from an account StreamGremlin has never seen (no viewer record, or one made within 3 seconds of the message), on Twitch or YouTube, once per account. You, mods, VIPs, subscribers and YouTube members never count, nor history (backlog) or test events.
- **Two bursts**, within a window (30 seconds by default):
  - first-time chatters (8 by default);
  - Twitch accounts younger than a number of days (5 accounts younger than 7 days by default). Get Users' `created_at` is asked once per account and kept in memory.
  - YouTube gives no account age, so YouTube only counts toward first-time chatters.
- **After a raid** (`audience.raid.received`), for 2 minutes by default, half the counts (at least 2) are a burst, and the warning names the raider.
- **When it fires:** at most once every 5 minutes. Live shows a warning for 15 minutes or until dismissed. The event `moderation.raid.suspected` (source streamgremlin) starts the new trigger **A raid looks suspicious**. With mode **lock**, it also locks chat (D-105's lock, recorded as from the raid guard).
- **On Live's warning:**
  - **Lock chat**.
  - **Ban these N accounts…** asks first, then makes a ban list from the burst's Twitch accounts that bans as soon as they're checked, even while live. It's still checked: mods, VIPs and viewers with points are left alone, and **Undo** on Ban lists lifts them. Up to 1,000 names; YouTube accounts are listed but not banned.
  - **Dismiss**.
- **Settings** on Chat filters (**Raid guard**), with **Test the warning** (a test event: nothing locks, no workflow runs outside a test); `streamgremlin raidguard`; `/v1/raid-guard`.
- **Not done:** an alert for YouTube-only bursts beyond first-time chatters (YouTube says nothing of account age), and unlocking on its own (never, by D-104).
