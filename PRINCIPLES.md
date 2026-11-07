# Product principles

These hold whatever features StreamGremlin carries. Most come from StreamWisp decisions, noted in brackets. Copy this file into the new repository as the first entries of its decision log.

## Fairness and money-like things

- Winners are chosen on the server with `crypto/rand`, recorded before anything is shown, and checkable afterwards. Overlays only animate a result already decided (D-034, D-069).
- Odds and requirements are always shown. Nothing is called fair unless it's verified. A plugin's odds are "stated by the plugin" (D-081).
- Points use an append-only, double-entry ledger with unique operation IDs. Balances never go negative unless the streamer allows it. History is never changed (D-029).
- Duplicate delivery never pays twice or repeats an outside action when an idempotency key exists.
- Viewers never pay to play games. No point wagering by StreamGremlin itself: bets, duels and double-or-nothing aren't built (D-031, D-043). Plugins may hold points in escrow for a round, with their odds shown as "stated by the plugin" (D-004).
- Games score every correct answer in the window the same, with no speed bonus, because viewers see chat at different delays (D-049).
- Chat-only viewers get everything they need to play; overlays alone are never enough.

## Ads

- StreamGremlin never starts an ad on its own. The streamer starts it or confirms a suggestion (D-035, D-041).
- Only the broadcaster can ask for an ad (D-037).
- A refused request is never announced. An unknown answer is never retried, and never shown as confirmed (D-036, D-038).
- Ad end, per-viewer ad visibility and passive viewer presence aren't offered by Twitch, and are never invented.

## OBS

- A scene is switched back only while the scene StreamGremlin switched to is still showing. A manual change always wins (D-039).

## Platforms and honesty

- Only documented, public platform APIs. Every capability, scope, limit and uncertainty is written in the adapter's manifest. Never invent an outside API capability (D-009).
- Unknown and disconnected states are shown as unknown, never as a confirmed state.

## Security

- Secret tokens never appear in logs, exports, backups, bug reports or traces. Secrets go in the OS keyring, with an encrypted file as the documented fallback (D-021).
- Lua scripts and plugins are untrusted: separate processes, time and memory limits, approved permissions, audited calls (D-014, D-065).
- Content packs are data only and can never run code. Archives are read in memory and refused on any unsafe entry (D-046).
- Imported scripts are never run. Streamer.bot C# is shown and inventoried, never converted or executed (D-058, D-132).
- The API listens on loopback by default, with a token, Host and Origin checks, and per-route permissions (D-019).
- No telemetry by default, no required account, no required cloud service.

## Won't do

Decided in StreamWisp or at kickoff, so they don't need discussing again unless something changes:

- Running or converting Streamer.bot C# (D-132).
- Automatic ads without the streamer (D-035).
- Point wagering run by StreamGremlin (D-031).
- Undocumented or private platform endpoints.
- Copying another product's design, wording or paid add-ons. Features are rebuilt from StreamGremlin's own parts (D-123).
- In-process Go plugins.
- A hosted multi-tenant cloud service.
- Paid wheel spins, or any other way for viewers to pay to play (D-004).

## People and data

- Viewer accounts are never merged by name. Accounts link only when proven in chat (D-045).
- Viewers can be exported and forgotten.
- Chat is public. Sending it to a service the streamer chose doesn't need viewer opt-in (D-090), but any such feature says what it sends.
