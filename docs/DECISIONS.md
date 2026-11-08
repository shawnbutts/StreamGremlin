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
