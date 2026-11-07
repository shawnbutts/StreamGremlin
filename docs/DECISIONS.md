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
