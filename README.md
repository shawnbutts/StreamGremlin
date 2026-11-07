# StreamGremlin

![StreamGremlin](brand-kit/StreamGremlin%20Workshop%20Banner.png)

StreamGremlin is a local-first streaming automation app for Twitch streamers, with YouTube in a later milestone. It's for streamers switching from Streamer.bot and for streamers with no bot yet. It runs on your own computer, with no required account, no required cloud service and no telemetry by default.

It's a rebuild of an earlier project, StreamWisp (2026-10-25 to 2026-11-07: 169 commits, 132 decisions, 79 ADRs and 95 migrations). It keeps StreamWisp's decisions, architecture and lessons, but none of its code. StreamWisp's repository stays available as reference. Paths such as `docs/adr/0045-giveaways.md` in these documents point into it.

**Status:** planning. The kickoff is complete. Next is milestone M1, the Spine. No code yet.

## Documents

| File | What it's for |
|---|---|
| [docs/DECISIONS.md](docs/DECISIONS.md) | Every decision, with the user's own words |
| [docs/FEATURES.md](docs/FEATURES.md) | The decided feature cut, and the milestone each feature belongs to |
| [PRINCIPLES.md](PRINCIPLES.md) | Rules that hold for every feature: fairness, ads, security, honesty about platforms, and the won't-do list |
| [ARCHITECTURE.md](ARCHITECTURE.md) | The architecture kept from StreamWisp, the changes made from the start, and known traps |
| [AGENTS.md](AGENTS.md) | Rules for coding agents working in this repository |
| [KICKOFF.md](KICKOFF.md) | The kickoff discussions. All seven are decided (D-001 to D-007). |
| [LESSONS-LEARNED.md](LESSONS-LEARNED.md) | What went well in StreamWisp, and what's done differently here |
| [FEATURES.md](FEATURES.md) | StreamWisp's full feature list with the original suggestions, kept as the input to the feature cut |
| [brand-kit/](brand-kit/README.md) | Mascot, icons, banner and UI theme |

## Where the code lives

During beta, the code is on a private server. After beta it moves to [github.com/shawnbutts/StreamGremlin](https://github.com/shawnbutts/StreamGremlin). The Go module path, `code.streamgremlin.com/streamgremlin`, stays the same through the move.
