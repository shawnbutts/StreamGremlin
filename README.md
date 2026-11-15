# StreamGremlin

![StreamGremlin](brand-kit/StreamGremlin%20Workshop%20Banner.png)

StreamGremlin is a local-first streaming automation app for Twitch streamers, with YouTube in a later milestone. It's for streamers switching from Streamer.bot and for streamers with no bot yet. It runs on your own computer, with no required account, no required cloud service and no telemetry by default.

It's a rebuild of an earlier project, StreamWisp (2026-10-25 to 2026-11-07: 169 commits, 132 decisions, 79 ADRs and 95 migrations). It keeps StreamWisp's decisions, architecture and lessons, but none of its code. StreamWisp's repository stays available as reference. Paths such as `docs/adr/0045-giveaways.md` in these documents point into it.

**Status:** a beta. Milestones M1 to M8 are done (M4, importing from Streamer.bot, moved to later), each used on real streams on macOS and Windows; M9, the release work, is in progress ([progress](docs/milestones/M9.md)). Installers for Windows and macOS are unsigned for now, and Linux is a beta, lightly tested: see the [beta guide](docs/BETA.md). Found a bug or have an idea? [Open an issue](https://github.com/shawnbutts/StreamGremlin/issues/new/choose). A security problem? See [SECURITY.md](SECURITY.md). Developers: see [CONTRIBUTING.md](CONTRIBUTING.md).

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
| [CONTRIBUTING.md](CONTRIBUTING.md) | Developer setup, `make verify`, DCO sign-off |
| [CHANGELOG.md](CHANGELOG.md) | What changed |
| [website/](website) | The website, built with `make site`; Help comes from the app's own Help text |
| [SECURITY.md](SECURITY.md) | Reporting a security problem, and what StreamGremlin protects |
| [docs/milestones/](docs/milestones/M1.md) | Milestone progress and live-check results |
| [docs/adr/](docs/adr/) | Architecture decisions for storage, protocols, file formats and security |
| [brand-kit/](brand-kit/README.md) | Mascot, icons, banner and UI theme |
| [streamdeck/](streamdeck/) | The Stream Deck plugin: `make streamdeck` builds its installer |
| [docs/BETA.md](docs/BETA.md) | The beta guide for testers: installing, reporting problems, where data is |
| [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) | What the app bundles from others, and their licences |

## Where the code lives

The code, issues and private security reports are at [github.com/shawnbutts/StreamGremlin](https://github.com/shawnbutts/StreamGremlin). The Go module path, `code.streamgremlin.com/streamgremlin`, stays the same through the move.
