# Feature cut

The features in StreamGremlin's first release, decided in kickoff K-4 ([D-004](DECISIONS.md)). A feature that isn't here needs a decision first, naming who will use it.

"Old ADR" and "D-" numbers point into StreamWisp's repository. Read the ADR before building the feature again.

## Where each feature lands

| Label | Meaning |
|---|---|
| **Spine** | Engine, Twitch sign-in and chat, OBS, workflows with the editor, traces, the desktop app |
| **F1** | Switch feature 1: chat commands, timers, counters and quotes |
| **F2** | Switch feature 2: event → OBS / sound / alert automation |
| **F3** | Switch feature 3: giveaways |
| **F4** | Switch feature 4: points ledger, currencies and rewards shop, native so plugins can use it |
| **Lua & plugins** | Lua scripts, then Lua plugins with the points API |
| **Import** | Import from Streamer.bot, checked against real exports |
| **YouTube** | The YouTube milestone (D-003) |
| **Release** | Built up across milestones, required before the first public release |
| **Later** | Not in the first release. Revisit once the core is used on real streams. |
| **Leave** | Not carried over. The won't-do items are also in [PRINCIPLES.md](../PRINCIPLES.md). |

Milestones (D-006): M1 Spine, M2 F1 and F2, M3 F3 and F4, M4 Import (then the first outside beta), M5 Lua & plugins, M6 YouTube.

Every feature is built with its desktop screen in the same slice (`AGENTS.md`).

## Engine and core

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Event inbox, with deduplication and replay | 0004, D-004, D-015 | Spine |
| Workflows: trigger → steps, queues, timeouts | 0006, 0007, D-017, D-018 | Spine |
| Run another workflow | 0073, D-116; ADR-0010, D-016 | Spine |
| Traces and runs | 0008, D-019 | Spine |
| Simulate and dry run | 0015, D-027 | Spine |
| Plain-words When/Then workflow editor | 0062, D-088; ADR-0010, D-016 | Spine |
| Secrets, in the OS keyring or an encrypted file | 0009, D-021 | Spine |
| Local API and CLI | 0008, 0013 | Spine |
| Persistent variables: global, per-viewer, per-workflow | not built in StreamWisp; ADR-0009, D-015 | Spine (designed early) |
| Chat command ownership | 0068, D-094, D-111 | Spine |
| Delete and restore workflows | D-103; ADR-0010, D-016 | Spine |
| Backups and restore | 0047, D-072 | Spine |
| Configuration export/import | 0048, D-073 | Later |
| Headless server | D-001 | Not built. The engine boundary is kept so it stays possible (D-003). |

## Desktop app

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Wails v3 + Svelte 5 shell, reaching the engine only through the API | 0014, D-026 | Spine |
| Home: health, live events, Get set up checklist | D-109 | Spine. The main onboarding path. |
| Settings: hide screens and connections you don't use | 0071, D-097, D-101 | Spine |
| Help screen with the user docs built in | D-112 | Spine |
| In-app ask dialogs, never `window.confirm` | D-099 | Spine (rule) |
| Native save dialog for exports | D-115 | Spine (rule) |
| Setup links open in the system browser, allowlisted | D-098 | Spine |
| Accessibility: axe checks in all themes and text sizes, contrast tests | 0051 | Spine, then every slice |
| Gallery of templates, with ready-made templates for each switch feature | D-123 | Spine, with templates added in F1–F4 |
| Twitch → OBS command wizard | 0015 | Leave. The checklist and templates take over its job. |
| Localization | D-076 | Leave. English only. |

## Twitch

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Device-code sign-in, refresh, validation | 0011, D-023 | Spine |
| Built-in Client ID in release builds, registered under `StreamGremlinHQ` | D-131 | Spine (paste-your-own until registered) |
| Chat read (EventSub) and send (Helix) | 0012, D-024 | Spine |
| Separate bot account for chat (optional) | 0057, D-084 | Spine |
| Follows, subs, bits, channel point redeems, chatters | 0018, D-030 | F2 |
| Raids, Hype Trains, polls, predictions (events, and starting them) | 0060, 0061, D-086, D-087 | F2 |
| First Words | 0059, D-085 | F2 |
| Profile pictures for overlays | 0074, D-118 | F2 |
| Channel point reward management: create, pause, change cost, fulfil or refund. Only for rewards StreamGremlin created; others are "trigger only", with an offer to create a managed copy. | new | F2 |
| Ad break begins trigger; get ad schedule, snooze next ad and start commercial steps; "next ad / snooze" card on Home | 0022, D-036 | F2 |
| Ad requests and confirmed breaks recorded separately | 0023, D-038 | F2 |
| Stream category for matching content packs | 0033, D-051 | Later, with packs |

## OBS

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| obs-websocket 5 connection, scene switch, source visibility | 0010, D-022 | Spine |
| Scene snapshots, restored only while StreamGremlin still owns the scene | 0024, D-039 | Spine |
| Filters, mute, text, media, recording, replay buffer, screenshots | D-110, D-121 | Spine |
| Random color for color sources | D-121 | Spine |

## Viewers and points

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Viewer records per platform account, with export and delete | 0016 | Spine |
| Counters and quotes | — | F1 |
| Points ledger: append-only, double entry, no negative balances | 0017, D-029 | F4 |
| Several currencies, rename, retire | 0072, D-102 | F4 |
| Earning rules: chat, follows, subs, bits, time in chat (not "watch time") | 0018, D-028, D-030 | F4 |
| Rewards shop and redemption queue | 0019, D-032 | F4 |
| Convert between currencies | 0072 | Later |
| Account links proven in chat (`!link`) | 0034, D-045 | YouTube |

## Engagement

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Timers: timed actions, cron schedules | 0060, 0070, D-096 | F1 |
| Alert overlays with slide or pop | 0020, D-124 | F2 |
| Sounds, played by the desktop app, with volume per sound | 0042, D-057, D-066 | F2 |
| Giveaways: equal entries, fingerprinted entry lists, checkable draws | 0045, D-069 | F3 |
| Giveaway entry rules | 0075, D-125, D-127 | F3 |
| Giveaway extra chances, announced in chat | 0077, D-128, D-129 | F3 |
| Giveaway draw from recent chat | 0078, D-130 | F3 |
| Giveaway overlay (never required: chat carries everything) | 0076, D-126 | F3 |
| Wheels with audited server-side draws | 0021, D-034 | Later |
| Paid wheel spins | D-031 | Leave (won't do) |

## Ads and breaks

The carried ad features are listed under Twitch (F2). Twitch sends no "ad ended" event. A workflow may wait for the length Twitch announced, labelled as such, and the app never shows an ad as confirmed ended.

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Intermissions (ad, BRB, technical) | 0025, D-040 | Later |
| Ad policies: manual or suggest, limits, emergency stop | 0026, D-041 | Later |
| Ad Break Director screen and banner | — | Later |

## Games and content packs

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Game runtime | 0029, D-048 | Later |
| Trivia, word scramble, guess the number | 0029–0031, D-049 | Later |
| Community challenges | 0032, D-050 | Later |
| Content packs | 0027, 0028, D-046, D-047 | Later |
| Category-matched packs and games during breaks | 0033, D-051 | Later |
| Pack catalogs | 0049, D-075 | Leave |

## Other platforms

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| YouTube: sign-in, live chat, Super Chats, memberships | 0036–0039 | YouTube |
| YouTube: cuepoints and retention | 0036–0039 | Decided at the YouTube milestone |
| Chat router across platforms | 0035, D-059 | YouTube. The single chat interface and `source` on events exist from the Spine. |
| Kick | 0043, D-054, D-055, D-067 | Leave |

## Extending it

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Lua scripts in supervised worker processes | 0003, D-014 | Lua & plugins |
| Plugins as separate processes | 0041, D-065 | Lua & plugins |
| Lua plugins (the first plugin kind) | 0067, D-093 | Lua & plugins |
| Plugins shipping workflows; uninstall turns them off. Ownership, install, update and uninstall are designed as an ADR before any plugin code. | D-106, D-107 | Lua & plugins |
| Points API for plugins: award, charge, refund, and escrow for rounds, with odds "stated by the plugin" | 0054, D-081 | Lua & plugins |
| Go plugin SDK | 0041 | Later |
| Streamer.bot WebSocket gateway | 0040, D-056, D-064 | Leave |
| Parity registry against Streamer.bot's docs | 0046, D-070 | Leave |

## Moving from Streamer.bot

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Import of export strings, converting only what converts exactly; imports start disabled | 0044, D-058, D-068 | Import |
| Type mappings for triggers, sub-actions and OBS states, limited to this cut | D-113 to D-121 | Import |
| Import report with a C# inventory, pointing to Lua or templates | D-122 | Import |
| Live inventory over Streamer.bot's WebSocket | D-058 | Later |
| Running or converting C# | D-132 | Leave (won't do) |

## Desktop agent

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Sounds | 0042, D-057, D-066 | F2 (see Engagement) |
| Text to speech | 0042, D-057, D-066 | Later |
| Global hotkeys | 0042, D-057, D-066 | Later |
| MIDI, window events, clipboard | not built | Later |

## AI

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| AI providers, spending ledger, chat personality, `persona.say` | 0058, 0063–0066, D-089 to D-092 | Leave |

## Releasing

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Release builds: macOS dmg, Windows installer, a Linux package, reproducible archives, SBOMs | 0052 | Release |
| Upgrade and install tests: containers for Linux, the user's Mac and Windows machines for installers | — | Release |
| Website built from the Help docs, on streamgremlin.com | 0055 | Release |
| Fuzzing, load tests, route guard sweep | 0050 | Release |
| Code signing and notarization | D-077 | Decided in K-5 |
| OCI container image | 0052 | Leave, while there's no headless server |
