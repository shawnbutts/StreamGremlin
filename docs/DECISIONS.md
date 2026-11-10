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
