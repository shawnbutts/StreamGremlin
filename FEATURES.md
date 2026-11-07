# Features for discussion

> **This was the input to kickoff K-4, and is kept for reference.** The decided cut is in [docs/FEATURES.md](docs/FEATURES.md) (D-004).

Everything StreamWisp built, grouped by area, with a suggestion for each:

- **Carry:** core to the product or cheap to keep. Suggested for the first milestones.
- **Discuss:** useful, but its scope or timing is a real choice.
- **Later:** worth having, after the core is used on real streams.
- **Leave:** not carried over unless the discussion finds a user for it.

"Old ADR" and "D-" numbers point into the StreamWisp repository, where the design and its tradeoffs are written down. The suggestions assume Twitch is the main audience (D-101). If the kickoff decides otherwise, the platform rows change first.

## Engine and core

| Feature | What it does | Old ADR / decision | Suggestion |
|---|---|---|---|
| Event inbox | Every outside event stored once, deduplicated, replayable by sequence number | 0004, D-004, D-015 | **Carry** |
| Workflows | Trigger → steps (action, set, if/else, foreach, weighted choice, wait), queues, timeouts | 0006, 0007, D-017, D-018 | **Carry** |
| Run another workflow | A step that runs another workflow inside the same run | 0073, D-116 | **Carry** |
| Traces and runs | Every step's inputs (secrets redacted), result and time, viewable per run | 0008, D-019 | **Carry** |
| Simulate and dry run | Test a workflow with a fake event; outside actions are skipped and recorded | 0015, D-027 | **Carry** |
| Plain-words workflow editor | When/Then editor with a palette of named triggers and steps | 0062, D-088 | **Carry** |
| Secrets | OS keyring or encrypted file; secrets never printed | 0009, D-021 | **Carry** |
| Local API and CLI | Loopback API with token, Host/Origin checks, per-route permissions; CLI over the API | 0008, 0013 | **Carry** |
| Headless server | Same engine without a window, for a server or container | D-001 | **Discuss:** who runs it? Keep the boundary, maybe not the packages. |
| Backups and restore | Scheduled ZIP backups without secrets, verified offline restore | 0047, D-072 | **Carry** |
| Configuration export/import | Settings as one document without secrets, previewed on import | 0048, D-073 | **Later** |
| Delete and restore workflows | Soft delete, history kept | D-103 | **Carry** |
| Chat command ownership | Each chat command answered by one thing at a time | 0068, D-094, D-111 | **Carry** |
| Nested scopes and persistent variables | Global, viewer and workflow variables | not built | **Discuss:** Streamer.bot users rely on globals |

## Desktop app

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Wails v3 + Svelte 5 shell, reaching the engine only through the API | 0014, D-026 | **Carry** |
| Home with health, live events and the Get set up checklist | D-109 | **Carry** |
| Settings: hide screens and connections you don't use | 0071, D-097, D-101 | **Carry** |
| Help screen with the user docs built in | D-112 | **Carry** |
| Ask dialogs in the app, never `window.confirm` | D-099 | **Carry** (it's a rule) |
| Native save dialog for exports | D-115 | **Carry** |
| Links to setup sites open in the system browser, allowlisted | D-098 | **Carry** |
| Accessibility: axe checks in both themes and text sizes, contrast tests | 0051 | **Carry** |
| Twitch → OBS command wizard | 0015 | **Discuss:** the checklist and templates may replace it |
| Gallery of templates (First Words and others) | D-123 | **Carry** |
| Localization | D-076 | **Leave:** English only, as decided |

## Twitch

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Device-code sign-in, refresh, validation | 0011, D-023 | **Carry** |
| Built-in Client ID in release builds | D-131 | **Carry:** register the app in week one |
| Chat read and send over EventSub and Helix | 0012, D-024 | **Carry** |
| Separate bot account for chat | 0057, D-084 | **Carry** |
| Follows, subs, bits, channel points, chatters | 0018, D-030 | **Carry** |
| Raids, Hype Trains, polls, predictions (events and starting them) | 0060, 0061, D-086, D-087 | **Carry** |
| First Words | 0059, D-085 | **Carry** |
| Profile pictures for overlays | 0074, D-118 | **Carry** |
| Stream category for matching content packs | 0033, D-051 | **Discuss:** only if packs are carried |
| Ads: schedule, start, snooze, ad-break-begin | 0022, D-036 | **Discuss:** see the Ads section |

## OBS

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| obs-websocket 5 connection, scene switch, source visibility | 0010, D-022 | **Carry** |
| Scene snapshots restored only while the project still owns the scene | 0024, D-039 | **Carry** |
| Filters, mute, text, media, recording, replay buffer, screenshots | D-110, D-121 | **Carry** |
| Random color for color sources | D-121 | **Carry** |

## Viewers and points

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Viewer records per platform account, export and delete | 0016 | **Carry** |
| Points ledger: append-only, double entry, no negative balances | 0017, D-029 | **Carry** |
| Several currencies, rename, retire, convert | 0072, D-102 | **Carry** |
| Earning rules: chat, follows, subs, bits, watch time | 0018, D-028, D-030 | **Carry** |
| Rewards shop and redemption queue | 0019, D-032 | **Carry** |
| Counters and quotes | — | **Carry** |
| Account links proven in chat (`!link`) | 0034, D-045 | **Discuss:** mostly useful with more than one platform |

## Engagement

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Wheels with audited server-side draws | 0021, D-034 | **Carry** |
| Paid wheel spins | D-031 | **Discuss:** legal exposure versus demand |
| Giveaways: equal entries, fingerprinted lists, checkable draws | 0045, D-069 | **Carry** |
| Giveaway entry rules (follower, account age, watch time, messages, recent wins, sub tier and months) | 0075, D-125, D-127 | **Carry** |
| Giveaway extra chances, announced | 0077, D-128, D-129 | **Carry** |
| Giveaway draw from recent chat | 0078, D-130 | **Carry** |
| Giveaway overlay | 0076, D-126 | **Carry** |
| Timers (Streamer.bot-style timed actions, cron schedules) | 0060, 0070, D-096 | **Carry** |
| Alert overlays with slide or pop | 0020, D-124 | **Carry** |

## Games and content packs

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Game runtime (sessions, registration, prizes, recovery) | 0029, D-048 | **Discuss:** carry if games are carried |
| Trivia with fair equal scoring | 0030, D-049 | **Discuss** |
| Word scramble | 0031 | **Discuss** |
| Guess the number | 0029 | **Discuss** |
| Community challenges | 0032, D-050 | **Later** |
| Content packs: safe ZIP format, install, versions, rollback | 0027, 0028, D-046, D-047 | **Discuss:** only needed for trivia and words |
| Pack catalogs | 0049, D-075 | **Leave** until a community catalog exists |
| Category-matched packs and games during breaks | 0033, D-051 | **Later** |

## Ads and breaks

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Ad requests and confirmed breaks recorded separately | 0023, D-038 | **Discuss:** the ad work was never checked live |
| Intermissions (ad, BRB, technical) with scene, overlay and chat | 0025, D-040 | **Discuss** |
| Ad policies: manual or suggest, limits, emergency stop | 0026, D-041 | **Discuss** |
| Ad Break Director screen and banner | — | **Discuss** |

## Other platforms

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| YouTube: sign-in, chat, Super Chats, memberships, cuepoints, retention | 0036–0039 | **Leave** for the first release; keep the design for later |
| Kick: sign-in, webhooks, chat, ads | 0043, D-054, D-055, D-067 | **Leave** |
| Chat router across platforms | 0035, D-059 | **Leave** until a second platform; keep the interface |

## Extending it

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Lua in supervised worker processes | 0003, D-014 | **Carry** |
| Plugins as separate processes, Go SDK | 0041, D-065 | **Discuss:** wait for a plugin someone wants? |
| Lua plugins | 0067, D-093 | **Discuss** with plugins |
| Plugins shipping workflows; uninstall turns them off | D-106, D-107 | **Discuss** with plugins |
| `points.wager` for plugin games (the raid game) | 0054, D-081 | **Discuss:** the user's own raid game needed it |
| Streamer.bot WebSocket gateway | 0040, D-056, D-064 | **Leave** unless a tool that needs it is named |
| Parity registry against Streamer.bot's docs | 0046, D-070 | **Discuss:** useful as a checklist, heavy to maintain |

## Moving from Streamer.bot

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Import of export strings, converting what converts exactly | 0044, D-058, D-068 | **Carry:** it's how switchers arrive |
| Type mappings for triggers and steps, OBS states | D-113 to D-121 | **Carry** |
| C# inventory in the import report | D-122 | **Carry** |
| Live inventory over Streamer.bot's WebSocket | D-058 | **Later** |
| Running or converting C# | D-132 | **Leave** (decided) |

## Desktop agent

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Global hotkeys, sounds, text to speech, per-OS availability | 0042, D-057, D-066 | **Discuss:** sounds are common; hotkeys vary by OS |
| MIDI, window events, clipboard | not built | **Later** |

## AI

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| AI providers, spending ledger, chat personality, `persona.say` | 0058, 0063–0066, D-089 to D-092 | **Leave:** hidden since D-092 |

## Releasing

| Feature | Old ADR / decision | Suggestion |
|---|---|---|
| Reproducible archives, Linux packages, OCI image, macOS dmg, Windows installer, SBOMs | 0052 | **Carry**, platforms decided at kickoff |
| Code signing and notarization | D-077 | **Discuss:** cost versus first-run warnings |
| Upgrade and package install tests in containers | — | **Carry** |
| Website from the same docs as the Help screen | 0055 | **Carry** |
| Fuzzing, load tests, route guard sweep | 0050 | **Carry** |

## Questions to answer while going through this list

- Which three features would make a Twitch streamer switch? Build those first, completely.
- Which of the "Discuss" rows does the user use today, on Streamer.bot or StreamWisp?
- Which features does the user want to show off first, and to whom?
