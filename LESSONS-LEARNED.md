# Lessons learned from StreamWisp

Evidence comes from StreamWisp's decision log (D-numbers) and history. Each lesson ends with what the new project does differently.

## What worked: keep doing it

- **The architecture.** Go modular monolith, SQLite, one engine behind the API for the desktop app, CLI and headless server, out-of-process Lua and plugins. It held up through seven milestones with no rewrites. → Keep it ([ARCHITECTURE.md](ARCHITECTURE.md)).
- **Thin slices with tests, docs and a screen in each.** The giveaway work (D-125 to D-130) went in as six small slices, and none was reversed. → Keep slices small and complete.
- **Mocks of every outside service, and acceptance tests through the real binaries.** `tools/mocktwitch`, `tools/mockobs` and the `tests/acceptance` suite caught crash, duplicate-delivery and restart bugs that unit tests didn't. → Build the mocks with the adapters, as before.
- **Recording the user's own words in each decision.** It made later "why did we do this?" questions quick to answer. → Keep it in the decision log.
- **Honest states.** Unknown is shown as unknown, refused ads are never announced, draws are checkable. No feature had to be walked back for overclaiming. → Keep these as principles ([PRINCIPLES.md](PRINCIPLES.md)).
- **Features that came from real use were the best ones.** Hiding unused screens (D-097), deleting workflows (D-103), renaming currencies (D-102), the Get set up checklist (D-109) and the importer fixes (D-113 to D-121) all came from the user trying the app. → Use the app on a real stream early and often.

## What we'd change

### Product and scope

1. **We built for every platform before we knew who the users were.** YouTube, Kick and the AI features were built and tested, then hidden (D-092, D-101) once Twitch turned out to be the audience. → Decide the audience at kickoff. Build one platform well first. Add another when someone needs it.
2. **We learned how Streamer.bot works late.** Timers (D-096), OBS sub-actions (D-110) and much of the importer (D-113 to D-121) were changed afterwards to match how Streamer.bot users expect things to work. → Study Streamer.bot's behaviour for each feature before designing it. Name things the way switching streamers expect, but never copy wording or design.
3. **We designed before seeing real data.** The C#-to-Lua spec was written before anyone looked at a real export. The first real one showed 49 of 78 calls going to StreamUP's library, and the plan was abandoned (D-122, D-123, D-132). → Collect real exports and real streams' event logs before designing anything that depends on them.
4. **Too much was built that nobody asked for.** The AI personality and providers, paid wheel spins, YouTube quota accounting, Kick webhooks, catalogs and the Streamer.bot WebSocket gateway took real effort, and are hidden, unused or unproven. → For every feature at kickoff, name who will use it. Without an answer, it waits.
5. **The CLI came first and screens came later.** "Everything has a UI" (D-083) was decided on day four, and screens were added afterwards for work already done. → The screen is part of the first slice of every feature from day one.
6. **Every milestone was "done" in three days, and then the real work began.** M1 to M7 were marked complete between 2026-10-25 and 2026-10-27. Most of the useful changes came after the user started using the app (2026-10-28 onwards). The v0.1.0 release checklist still has no results. → A milestone isn't done until the user has used it on a real stream. Fewer milestones, each checked live.

### Names and outside accounts

7. **The name changed twice.** It was renamed to StreamLoom on day one and to StreamWisp two days later (D-079), which needed a one-time data, keyring and secrets migration that was then removed (D-080). The `StreamWisp` Twitch username turned out to be taken. → Pick the name first, and check the domain, Twitch username, GitHub/Forgejo name and trademark together before the first commit. Put the name in as few places as possible: one constant for the module, binaries and data folder names.
8. **Outside accounts were left until last.** The Twitch app registration (D-131), code signing (D-077), website hosting and a security contact were still open at the end. They take little code but block a release. → Start them in week one, in parallel with the code.

### Engineering

9. **Real services weren't tested after the early milestones.** M2 and M3 were checked live by the user with real Twitch and OBS. M4 onwards (ads, games, giveaways, YouTube, Kick) only ran against mocks. Windows was never run. → One live check per milestone, by a person, recorded in the milestone's notes. A Windows machine or runner before the first release.
10. **Migrations piled up.** 95 migrations in two weeks, none editable. → Before the first public release, fold the schema into one baseline migration. After that, migrations are frozen as before.
11. **Plugin rules were settled by surprises.** Plugin workflows (D-106), uninstall (D-107) and command ownership (D-094, D-111) were each decided after something broke. → Decide what a plugin owns, and what happens on install, update and uninstall, before building plugins.
12. **Desktop webview quirks were found by the user.** macOS's webview ignored `window.confirm` (D-099) and `<a download>` (D-115). Twitch turned "B)" into an emote (D-105). → A short list of known webview and chat-platform quirks in the architecture doc, and the desktop app used on every OS before a release.
13. **We argued about compatibility we couldn't deliver.** Running C# took a spec, an inventory and two decisions to end with "no" (D-132). → A "won't do" list on day one ([PRINCIPLES.md](PRINCIPLES.md)).

### Process

14. **There was too much paperwork for one user.** 79 ADRs and a 132-entry log for a project with one installation, and every ADR amended by later decisions. → Write an ADR only for a decision that's expensive to reverse (storage, protocols, security, file formats). Everything else is one line in the decision log and the changelog.
15. **The spec arrived before the product.** The starting documents (roadmap, parity matrix, feature specs) described a full Streamer.bot replacement, and milestones were built to them. → Start from the feature cut decided at kickoff, and grow the roadmap from use.

## If we keep only three

1. Real data and real use first.
2. Name and outside accounts in week one.
3. One platform done well before the next.
