# Project reboot

This folder is the starting kit for a new project that replaces StreamWisp. The new project will have a new name, which isn't chosen yet. It reuses what StreamWisp taught us: its decisions, architecture, feature list and mistakes. It doesn't reuse its code. Until the name is chosen, these documents call it **the project**.

StreamWisp ran from 2026-10-25 to 2026-11-07: 169 commits, 132 decisions, 79 ADRs and 95 migrations. Its repository stays available as reference. Paths below such as `docs/adr/0045-giveaways.md` point into it.

## What's here

| File | What it's for |
|---|---|
| [AGENTS.md](AGENTS.md) | Rules for coding agents in the new repository. Copy it to the new repo's root. |
| [KICKOFF.md](KICKOFF.md) | The discussions to have before any code, in order, with what each must decide. |
| [LESSONS-LEARNED.md](LESSONS-LEARNED.md) | What went well and what we'd change, with evidence from StreamWisp. |
| [FEATURES.md](FEATURES.md) | Every StreamWisp feature, with a suggestion to carry, discuss or leave behind. Input to the feature discussion. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | The technical architecture we're keeping, and the few changes to make from the start. |
| [PRINCIPLES.md](PRINCIPLES.md) | The product rules worth keeping whatever features we pick (fairness, ads, security, honesty about platforms). |

## How to start

1. Hold the kickoff discussions in [KICKOFF.md](KICKOFF.md), one at a time. Record each outcome as a decision in the new repo's docs/DECISIONS.md.
2. Once the name, audience and feature cut are decided, create the new repository, copy every file in this folder into its root, and replace "the project" with the name.
3. Build the first milestone from the feature cut, following [AGENTS.md](AGENTS.md).

The suggestions in [FEATURES.md](FEATURES.md) and [KICKOFF.md](KICKOFF.md) are suggestions. None of them is decided until the discussion says so.
