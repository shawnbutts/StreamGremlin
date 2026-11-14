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
| **Live** | M7: the Live screen, moderating from chat, and the Stream Deck plugin (D-075) |
| **Import** | Import from Streamer.bot, checked against real exports |
| **YouTube** | The YouTube milestone (D-003) |
| **Release** | Built up across milestones, required before the first public release |
| **Later** | Not in the first release. Revisit once the core is used on real streams. |
| **Leave** | Not carried over. The won't-do items are also in [PRINCIPLES.md](../PRINCIPLES.md). |

Milestones (D-006): M1 Spine, M2 F1 and F2, M3 F3 and F4, M4 Import (moved to Later by D-061), M5 Lua & plugins, M7 Live (before M6, D-075), M6 YouTube.

Every feature is built with its desktop screen in the same slice (`AGENTS.md`).

## Engine and core

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Event inbox, with deduplication and replay | 0004, D-004, D-015 | Spine |
| Workflows: trigger → steps, queues, timeouts | 0006, 0007, D-017, D-018 | Spine |
| Workflow groups: a switch per group that keeps each workflow's own; templates in groups | new (D-032); built in M2 (ADR-0020) | M2 |
| Run another workflow | 0073, D-116; ADR-0010, D-016 | Spine |
| Traces and runs | 0008, D-019 | Spine |
| Simulate and dry run | 0015, D-027 | Spine |
| Plain-words When/Then workflow editor | 0062, D-088; ADR-0010, D-016 | Spine |
| Secrets, in the OS keyring or an encrypted file | 0009, D-021 | Spine |
| Local API and CLI | 0008, 0013 | Spine |
| Persistent variables: global, per-viewer, per-workflow | not built in StreamWisp; ADR-0009, D-015 | Spine (designed early) |
| Chat command ownership | 0068, D-094, D-111; ADR-0011, D-017 | Spine |
| Delete and restore workflows | D-103; ADR-0010, D-016 | Spine |
| Backups and restore | 0047, D-072; ADR-0012, D-020 | Spine |
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
| Native save dialog for exports | D-115; built in M3 for viewer exports (ADR-0032) | Spine (rule) |
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
| Follows, subs, bits, channel point redeems, chatters | 0018, D-030; events built in M2 (ADR-0015, D-026); active chatters built in M6 (D-102) | F2 |
| Raids, Hype Trains, polls, predictions (events, and starting them) | 0060, 0061, D-086, D-087; raids built in M2 (ADR-0015), Hype Trains (ADR-0022, D-040), polls and predictions (ADR-0023, D-041) | F2 |
| First Words | 0059, D-085; built in M2 (ADR-0021, D-033) | F2 |
| Profile pictures for overlays | 0074, D-118; built in M2 for alerts (ADR-0017, D-028) | F2 |
| Channel point reward management: create, pause, change cost, fulfil or refund. Only for rewards StreamGremlin created; others are "trigger only", with an offer to create a managed copy. | new; built in M2 (ADR-0018, D-030) | F2 |
| Ad break begins trigger; get ad schedule, snooze next ad and start commercial steps; "next ad / snooze" card on Home | 0022, D-036; built in M2 (ADR-0024, D-042) | F2 |
| Ad requests and confirmed breaks recorded separately | 0023, D-038; built in M2 (ADR-0024, D-042) | F2 |
| Stream category for matching content packs | 0033, D-051 | Later, with packs |
| A Live screen: chat in the middle with sending, buttons that run workflows (right side or top), with icons (D-076) | new (D-075); built in M7 (ADR-0040, D-076) | Live |
| A "You press a button" trigger, for the Live screen's buttons and the Stream Deck | new (D-075); built in M7 (ADR-0040, D-076) | Live |
| A viewer card on a chatter's name: what Twitch's API gives (account, follow, subscription, VIP, moderator, banned) and StreamGremlin's record | new (D-075); built in M7 (D-078) | Live |
| Moderating from chat: delete, time out, ban and unban, warn, shoutout, VIP, moderator, chat modes, as Twitch's API allows | new (D-075); built in M7 (ADR-0041, D-079) | Live |
| Channel commands: change the title and category, clips, stream markers, uptime and follow age, as steps and templates | new (D-086) | Built after M7 |
| Chat filters: links, capitals, symbols, emotes, repeats and the streamer's words; delete, time out or warn, recorded; `!permit` | new (D-085) | Built after M7 |
| Twitch's Blocked Terms on the Chat filters screen: list, remove, copy words either way | new (D-088) | Built after M7 |
| Twitch's suspicious-user flags (ban evaders, shared bans) on messages and the viewer card, with a workflow trigger; monitor and restrict | new (D-079); built in M7 (ADR-0041) | Live |

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
| Viewer records per platform account, with export and delete | 0016; built in M3 (ADR-0032, D-052); their own Viewers screen (D-060) | Spine (built with F4) |
| Counters and quotes | Quotes built in M2 (ADR-0014, D-025); counters are saved variables (D-024) | F1 |
| Points ledger: append-only, double entry, no negative balances | 0017, D-029; built in M3 (ADR-0032, D-052), one renamable currency | F4 |
| Several currencies, retire, below zero (renaming `points` is built) | 0072, D-102; moved to Later by D-057 | Later |
| Earning rules: chat, follows, subs, bits, time in chat (not "watch time") | 0018, D-028, D-030; chat, follows, subs, gifts and bits built in M3 (ADR-0033, D-053); time in chat built in M3 (ADR-0034, D-055) | F4 |
| Give to everyone by a rule (chatter list, recent chatters, subscribers), and export everyone | D-054; built in M3 (ADR-0035, D-056) | F4 |
| Rewards shop and redemption queue | 0019, D-032; moved to Later by D-058 | Later |
| Convert between currencies | 0072 | Later |
| Account links proven in chat (`!link`) | 0034, D-045 | YouTube |

## Engagement

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Timers: timed actions, cron schedules | 0060, 0070, D-096; built in M2 (ADR-0013, D-022) | F1 |
| Alert overlays with slide or pop | 0020, D-124; built in M2, one browser source each, no grouping (ADR-0017, D-028) | F2 |
| Sounds, played by the desktop app, with volume per sound | 0042, D-057, D-066; built in M2, played in the app's window (ADR-0016, D-027) | F2 |
| Giveaways: equal entries, fingerprinted entry lists, checkable draws | 0045, D-069; built in M3 (ADR-0025, D-043) | F3 |
| Giveaway entry rules | 0075, D-125, D-127; built in M3 (ADR-0026, D-044); no watch time or sub tier | F3 |
| Giveaway extra chances, announced in chat | 0077, D-128, D-129; built in M3 for subscribers (ADR-0027, D-045); no tier or watch-time chances | F3 |
| Giveaway draw from recent chat | 0078, D-130; built in M3 (ADR-0028, D-046) | F3 |
| Giveaway overlay (never required: chat carries everything) | 0076, D-126; built in M3 (ADR-0029, D-047) | F3 |
| Wheels with audited server-side draws: a dedicated wheel feature, its look from the wheel concept in `tmp/streamgremlin-theme-starter` | 0021, D-034; D-048; built in M3 (ADR-0030, D-049); points prizes moved to Later (D-059), never paid spins (D-031) | M3 |
| Points in giveaways and wheels: points prizes; a giveaway entry cost would be a wager and needs its own decision | D-052, D-059 | Later |
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
| Third-party bot lists (CommanderRoot, TwitchInsights) | D-079: no documented API, sends viewers' names out | Leave |

## Other platforms

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| YouTube: sign-in, live chat, Super Chats, memberships | 0036–0039 | YouTube |
| YouTube: combined chat on Live, triggers matching Streamer.bot's, moderating, viewers and points, linked accounts sharing points, viewer count, title, subscribers, polls; add-ons on every chat | 0036–0039, D-095 to D-103 | YouTube (built in M6) |
| YouTube: several live broadcasts at once, scheduled broadcasts, new-upload posts, cuepoints | D-095 | Later |
| YouTube: comments, community posts, clips, retention | 0036–0039, D-095 | Leave |
| Chat router across platforms | 0035, D-059 | YouTube. The single chat interface and `source` on events exist from the Spine. |
| Kick | 0043, D-054, D-055, D-067 | Leave |
| Discord Tier 1: webhooks; post from workflows; go-live, clip and stream-summary templates | new (D-089, ADR-0044) | Built after M7 |
| Discord Tier 2: a bot account, editing the announcement, slash commands, roles | new (D-089) | Waiting for a request |
| A Now playing overlay: the song and album art from the files a music player writes (Pretzel's Write to File, for one) | new (D-091) | Built after M7 |
| Now playing: !song, a song-changed trigger | new (D-091) | Waiting for a request |
| Clock and timer overlays: a clock, countdowns and stopwatches, with looks for timers (Flip clock example) | new (D-092, ADR-0045) | Built after M7 |

## Extending it

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Lua scripts in supervised worker processes | 0003, D-014; built in M5 (ADR-0037, D-069) | Lua & plugins |
| Plugins as separate processes | 0041, D-065 | Lua & plugins |
| Lua plugins (the first plugin kind) | 0067, D-093; part 1 built in M5 (ADR-0038, D-070), updating and rolling back built in M5 (D-072), events and timers built in M5 (D-073) | Lua & plugins |
| Plugins shipping workflows; uninstall turns them off. Ownership, install, update and uninstall are designed as an ADR before any plugin code. | D-106, D-107; designed in M5 (ADR-0036, D-063) | Lua & plugins |
| Points API for plugins: award, charge, refund, and escrow for rounds, with odds "stated by the plugin" | 0054, D-081; built in M5 as rounds (ADR-0039, D-074) | Lua & plugins |
| Custom overlay themes for wheels: three built-in looks (workshop, neon arcade, a selector board), and themes installed from a ZIP, scripted, sandboxed, never deciding a result | D-062; the concept in `tmp/streamgremlin-theme-starter`; designed in M5 (ADR-0036, D-063); built-in looks built in M5 (D-064), with their own sounds (D-065); installing and removing built in M5 (D-066); updating and rolling back built in M5 (D-068), add-on looks only at OBS's "No access" (D-067) | Lua & plugins |
| Go plugin SDK | 0041 | Later |
| Streamer.bot WebSocket gateway | 0040, D-056, D-064 | Leave |
| Parity registry against Streamer.bot's docs | 0046, D-070 | Leave |

## Moving from Streamer.bot

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Import of export strings, converting only what converts exactly; imports start disabled | 0044, D-058, D-068; moved to Later by D-061 | Later |
| Type mappings for triggers, sub-actions and OBS states, limited to this cut | D-113 to D-121; moved to Later by D-061 | Later |
| Import report with a C# inventory, pointing to Lua or templates | D-122; moved to Later by D-061 | Later |
| Live inventory over Streamer.bot's WebSocket | D-058 | Later |
| Running or converting C# | D-132 | Leave (won't do) |

## Desktop agent

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Sounds | 0042, D-057, D-066 | F2 (see Engagement) |
| Text to speech | 0042, D-057, D-066 | Later |
| Global hotkeys | 0042, D-057, D-066 | Later |
| Our own Stream Deck plugin: paired once, buttons run StreamGremlin buttons or workflows and show state | new (D-075); pairing and the plugin built in M7 (ADR-0042, D-080, D-081); keys press buttons (a button runs its workflows); the Streamer.bot gateway stays Leave | Live |
| MIDI, window events, clipboard | not built | Later |

## AI

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| AI providers, spending ledger, chat personality, `persona.say` | 0058, 0063–0066, D-089 to D-092 | Leave |

## Releasing

| Feature | Old ADR / decision | Lands in |
|---|---|---|
| Release builds: macOS dmg, Windows installer, a Linux package, reproducible archives, SBOMs | 0052 | Release. Beta installers for Windows and macOS built (D-084, ADR-0043) |
| Upgrade and install tests: containers for Linux, the user's Mac and Windows machines for installers | — | Release |
| Website built from the Help docs, on streamgremlin.com | 0055 | Release |
| Fuzzing, load tests, route guard sweep | 0050 | Release |
| Code signing and notarization | D-077 | Decided in K-5 |
| OCI container image | 0052 | Leave, while there's no headless server |
