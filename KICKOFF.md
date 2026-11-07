# Kickoff discussions

Hold these one at a time, in order, before writing any code. Each one ends with a decision recorded in the new repo's docs/DECISIONS.md, with the user's own words. An agent leading a discussion presents the options and a recommendation, then waits for the user's choice.

## K-1. Audience and first users

**Decide:** who the first release is for, and who will try it.

- Twitch streamers switching from Streamer.bot? Streamers with no bot yet? The user's own stream first?
- Which streams will run it in the first month? Their event logs and Streamer.bot exports are the real data for every later discussion.

**Suggested:** Twitch streamers coming from Streamer.bot, starting with the user's own stream.

## K-2. Name

**Decide:** the new name, after checking all of these are free:

- domain;
- Twitch username (for the app's owner account and bot);
- the code host's organization or user name;
- a quick trademark search, and the name in the stores we may ship to.

**Also decide:** the Go module path, binary names, data folder name and format prefix, all derived from the name.

## K-3. Platforms

**Decide:** which streaming platforms and operating systems the first release supports.

- Streaming: Twitch only? YouTube and Kick later or never?
- OS: macOS, Windows, Linux desktop, headless Linux server? Which can we actually test before a release?

**Suggested:** Twitch and OBS only. macOS and Windows desktop, with a Windows machine or runner arranged. Linux desktop and headless server as "builds, lightly tested".

## K-4. Feature cut

**Decide:** walk through [FEATURES.md](FEATURES.md) and mark each row carry, later or leave.

- Settle the "Discuss" rows: games and packs, ads and intermissions, plugins, the desktop agent, paid spins, the parity registry.
- Pick the **three features that make a streamer switch**. They're the first milestone after the engine.

## K-5. Outside accounts and costs

**Decide:** who registers what, and when. Start these in week one.

- Twitch app registration under the StreamGremlinHQ account (built-in Client ID, D-131).
- Code signing: Apple Developer ID and a Windows certificate, or first-run warnings (D-077)?
- Website hosting, a security contact address, a support channel.
- License (StreamWisp was Apache-2.0, D-078) and whether contributors sign anything.

## K-6. Milestones and "done"

**Decide:** the first three milestones and what "done" means.

**Suggested:**

1. **Spine:** engine, Twitch sign-in and chat, OBS, workflows with the editor, traces, a working desktop app on macOS and Windows. Checked live on the user's stream.
2. **The three switch features** from K-4. Checked live.
3. **Import from Streamer.bot** for whatever the first users' exports contain. Checked with their real exports.

A milestone is done when the user has used it on a real stream and the result is written down.

## K-7. How we work

**Decide:** what carries over from StreamWisp's process.

- Branch, commit, fast-forward main and push each slice; push freely while the repo is internal?
- Decision log with the user's words: yes. ADRs only for storage, protocols, file formats and security?
- How often the user tries the app on stream, and how they report what they find.
- Which StreamWisp data, if any, moves over (the user's workflows, points, viewers). An import from a StreamWisp backup, or a fresh start?

## After the kickoff

1. Create the repository under the new name.
2. Copy every file in this folder into its root, replacing "the project" with the name.
3. Write the kickoff decisions as D-001 onwards, and the feature cut as docs/FEATURES.md.
4. Start milestone 1.
