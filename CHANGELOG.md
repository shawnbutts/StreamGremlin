# Changelog

All notable changes to StreamGremlin. Versions follow [Semantic Versioning](https://semver.org) from the first release.

## Unreleased

### Added

- M1 slice 1, the skeleton:
  - The engine, with its data folder, a lock so only one engine uses it, and a SQLite database with a baseline schema and automatic backups before schema changes.
  - The authenticated local API, loopback only: token, Host and Origin checks, permissions per route, and a version header.
  - The `streamgremlin` command line, with `status` and `version`.
  - The desktop app (Wails v3, Svelte 5):
    - a **Home** screen showing whether the engine is running, its version, how long it has been running, and the data folder;
    - a **Settings** screen with the colour theme: System, Dark workshop, Light workshop or Midnight blue.
- M1 slice 2, secrets:
  - Sign-ins and passwords are kept in the macOS Keychain, Windows Credential Manager or Secret Service. Where none answers, they go in an encrypted file in the data folder. Values never appear in the database, logs, backups or API answers.
  - **Settings → Passwords and sign-ins** shows where they're kept and which are saved. It can check that each one can still be read, and forget one after asking.
  - `streamgremlin secrets [-check] [-json]` and `streamgremlin secrets forget KEY`.
- M1 slice 3, events:
  - The event inbox. Every event is stored once in arrival order, platform redeliveries are recognised, and any reader can resume where it left off.
  - An **Events** screen: live events, newest first, with details, and a form to send test events (chat message, chat command, OBS scene change). Test events are marked "Test".
  - `streamgremlin events`, `events watch`, `events simulate TYPE field=value…` and `events types`.
- M1 slice 4, Twitch sign-in:
  - A **Connections** screen. You sign in to Twitch by entering a code at twitch.tv/activate, which the app opens in your browser. An optional bot account sends chat under its own name.
  - Sign-ins are kept in your system's keyring, refreshed and checked hourly. A sign-in Twitch ends is shown with the reason; an unreachable Twitch is shown as unknown.
  - Your own Twitch app's Client ID can be pasted in. Release builds will include StreamGremlin's.
  - `streamgremlin twitch [status|sign-in|sign-out|client-id]`, with `-bot` for the bot account.
  - `mocktwitch`, a stand-in for Twitch's sign-in server, for tests and for trying the app.
- M1 slice 5, Twitch chat:
  - StreamGremlin reads your channel's chat while you're signed in. Messages and `!commands` show live on Events. It reconnects by itself, and says so when chat is lost (Twitch doesn't resend missed messages).
  - **Connections → Twitch → Chat:** whether chat is being read, and a box to send a message as the bot or as you. It explains `/mod` when the bot isn't a moderator.
  - Sending stays under Twitch's rate limits, is never retried, and says exactly why a message wasn't sent.
  - The bot's own messages are never treated as viewer chat.
  - `streamgremlin chat send`. mocktwitch gains chat, with a `/chat` page to type as a viewer.
- M1 slice 6, OBS:
  - **Connections → OBS:** connect to OBS's WebSocket server (OBS 28 or newer), with the password kept in your keyring. It reconnects by itself, and says plainly when it needs you (wrong password, a Kick from OBS, an old OBS).
  - While connected: switch scenes, "Switch for now" and come back, and show or hide sources.
  - Switching back never undoes a scene you chose yourself in OBS meanwhile, or while StreamGremlin was closed.
  - Every change in OBS shows on Events: scenes, sources, filters, mute, media, stream, recording, replay.
  - `streamgremlin obs …`, and `mockobs`, a stand-in for OBS, for tests and for trying the app.
- M1 slice 7, workflows:
  - When something happens (a chat command, an OBS change…), do things: answer in chat, switch scenes, show or hide sources, mute, set text, and the other OBS controls, with conditions, loops, random choices and waits.
  - A **Workflows** screen: add a workflow (pasted as JSON for now; the plain-words editor comes later), turn it on, **Test** it with a test event, and read each run's trace: every step, what it was given, what it did, and why a run didn't happen.
  - Test events never reach Twitch or OBS: those steps are skipped and shown.
  - A workflow never runs twice for one event. After a crash, runs in progress are marked interrupted and never repeated on their own.
  - `streamgremlin workflows …` and `streamgremlin runs …`; examples in `examples/workflows/`.

- M1 slice 8, saved variables:
  - Workflows keep values between runs and restarts: for every workflow (global), per viewer (by their Twitch id, so a rename keeps them), or private to one workflow.
  - New actions: "Save a variable", "Add to a saved number" (counts exactly even when runs overlap) and "Forget a saved variable". Workflows read them as `saved.global.deaths`, `saved.viewer.lurks` and `saved.workflow.last`.
  - Test events never change saved variables: the change is kept for that test run only, and the trace says so.
  - A **Variables** screen (Ctrl/⌘+5): see every saved variable, filter by scope, search, and change, add or delete them. Settings moves to Ctrl/⌘+6 (Ctrl/⌘+comma still works).
  - Mistyped variable names and choices are now caught when a workflow is saved.
  - `streamgremlin vars`, `vars set` and `vars delete`.
  - Example workflows: a death counter and a per-viewer lurk count.

- M1 slice 9, the workflow editor:
  - **Build workflows in plain words** (Workflows → New workflow, or Edit). Choose what starts it ("A viewer types a command", "OBS switches scene"…) and what it does, step by step. Messages can include values like {viewer} from an "Insert a value" menu. Conditions are a value, a comparison and a text. "Pick one at random", "If… otherwise…", "Wait" and "Remember a value" are there too. A JSON tab shows and edits everything, and the editor never loses what it can't show.
  - **Who can use it:** Everyone, Subscribers and up, VIPs and up, Mods and you, or Only you, with no code. Test events can be from a chatter with any role, so these workflows can still be tested.
  - **Run another workflow,** passing it values, as part of the current run. Loops are refused, and a turned-off workflow isn't run.
  - **Delete and restore workflows.** Deleting asks first, keeps its runs and saved variables, and it can be restored (turned off) from "Deleted workflows".
  - `streamgremlin workflows delete`, `deleted` and `restore`.

- M1 slice 10, one answer per chat command:
  - A workflow can't be turned on when another that's on already answers its `!command`, unless "Who can use it" (or the chat) never overlaps: `!so` for mods and `!so` for viewers can be two workflows. The refusal names the workflow that answers it.
  - **Workflows → Chat commands** lists every command and what answers it, and flags overlaps left from before.
  - The editor warns as you type a command that's already taken. "Who can use it" gains "Everyone except mods and you", to pair with "Mods and you".
  - `streamgremlin commands`.

- M1 slice 11a, Home:
  - **Needs you:** what's wrong (a sign-in Twitch ended, chat not read, OBS needing you, a command answered twice, failed runs), what can't be checked right now, and tips like making the bot a moderator. Each opens the screen that fixes it.
  - **Get set up:** a checklist from signing in to seeing a workflow answer in chat, ticked from what's really done. It can be hidden.
  - **Happening now:** the latest events, live, and workflow runs. **Connections:** you, the bot, chat and OBS at a glance, with unknown said as unknown.
  - `streamgremlin status` also prints what needs you and, until you're set up, the checklist.

- M1 slice 11b, templates and help:
  - **New workflow opens a gallery** of ready-made workflows: Lurk, Say hello back, Hug, Shoutout, Death counter, Roll a die, Be right back, Back from the break, and We're live. One opens in the editor turned off. Anything to fill in, like your break scene, is listed, and saving waits until it's done.
  - **Help** (Ctrl/⌘+6): how StreamGremlin fits together, keyboard shortcuts, and where your data lives. Each screen has a line saying what it's for, and the editor explains values and conditions.
  - `streamgremlin templates` and `templates show ID`.

- M1 slice 11c, backups and history:
  - **Settings → Backups:** back up now, check a backup, restore one, or delete one. Restoring asks first, backs up what it replaces, and restarts StreamGremlin to do it. A backup that's damaged or from a newer version is refused, and nothing changes. Sign-ins and passwords are never in a backup.
  - **Automatic backups** every day by default, keeping the newest 7.
  - **History:** events older than 30 days and finished runs older than 90 days are cleared automatically, both adjustable (0 keeps them forever). Workflows and saved variables are never cleared.
  - `streamgremlin backups`, `backups create`, `verify`, `restore`, `cancel-restore` and `delete`.

- M2 slice 1, timers:
  - **A Timers screen:** timers go off every so often (or at random between two times) or on a schedule, and start the workflows that use them.
    - A timer can wait for viewers to send so many chat messages, run only while OBS is streaming, and run once.
    - Each counts how many times it has gone off. **Run now** tries one out.
    - **Make a workflow** starts one for a timer.
  - **Workflows:** the trigger "A timer goes off", with `{timer}` and `{counter}`, and the step "Turn a timer on or off".
  - `streamgremlin timers`, `timers set`, `run`, `on`, `off` and `delete`.

- M2 slice 2, quotes:
  - **A Quotes screen:**
    - numbered quotes, with who added them, a category and the date;
    - search, add, edit and delete;
    - numbers are never reused, so "#12" always means the same quote.
  - **Chat commands** from three new templates, which the Quotes screen can add in one step: `!quote` (at random, or `!quote 5`) for anyone, and `!quoteadd` and `!quotedel` for mods and you.
  - **Workflow steps:** "Add a quote", "Pick a quote" and "Delete a quote".
  - `streamgremlin quotes`, `quotes add`, `show`, `edit` and `delete`.

- M2 slice 3, Twitch channel events:
  - **New triggers:** someone follows, subscribes, resubscribes, gifts subs, cheers bits, raids you, or redeems channel points (optionally only one reward); you go live or offline on Twitch; your title or category changes.
  - **Five templates:** thank followers, subs and resubs, gifted subs and cheers, and welcome raiders.
  - **Connections** lists which events your channel sends, and why any aren't coming in. Home says when signing in to Twitch again would add some.
  - Quotes added from chat take your Twitch category.
  - "Only while live" timers also count going live on Twitch.
  - mocktwitch sends every new event, from tests or its `/chat` page.

- M2 slice 4, sounds:
  - **A Sounds screen:** add MP3, WAV, OGG or M4A files (up to 5 MB), set each one's volume, play them, and stop them all.
  - **Workflow steps:** "Play a sound" (at its own volume or another) and "Stop sounds".
  - Sounds play in the StreamGremlin window, and a run says whether each actually played. OBS hears them through Desktop Audio.
  - Backups include the sound library.
  - `streamgremlin sounds`, `sounds add`, `play`, `volume`, `delete` and `stop`.

- M2 slice 5, alert overlays:
  - **An Overlays screen:** add alert overlays, copy each one's private link into OBS as a browser source, and set where alerts appear, slide or pop, colors, opacity and time on screen, with a live preview. It shows whether OBS has each overlay open, and can show a sample alert in OBS or in the preview.
  - **Workflow step:** "Show an alert", with a title and text, and if you like the viewer's Twitch profile picture and a sound. Alerts on one overlay take turns. A run says whether the alert showed. Test on the Workflows screen opens a preview of the overlay right there, and shows the alert only in it, never in OBS; the run says whether a preview showed it.
  - **More looks** (D-029): gradients, rounder or squarer corners, an outline and a drop shadow on the box; a font (or any font on your computer), size, title color, letter outline and text shadow on the words. "Preview an alert" tries changes before you save them.
  - **"Picture, then words"** (D-034): a third animation. The viewer's picture slides in and the message unfolds from behind it, touching it, mirrored on the right. Without a picture, their initial keeps the shape, so Test shows it too.
  - **Animation speed** (D-035): each overlay's animations can run up to twice as fast or three times as slow.
  - **A template:** "Show an alert for new followers".
  - Links can be replaced (the old one stops at once) and are never logged or kept in backups.
  - `streamgremlin overlays`, `overlays add`, `link`, `new-link`, `look`, `test` and `delete`.

- M2 slice 6, channel point rewards:
  - **A Rewards screen:** your rewards from Twitch, each marked made in StreamGremlin or made on Twitch. For StreamGremlin's: make, edit, pause, turn on or off, delete, and a queue to mark redemptions done or refund them. For Twitch's: "Make a copy". It says plainly when your channel can't have channel points yet (not Affiliate or Partner).
  - **Workflow steps:** "Complete or refund the redemption" and "Change a reward".
  - `streamgremlin rewards`, `rewards add`, `set`, `queue`, `done`, `refund` and `delete`.

- M2 slice 7, app comfort:
  - **More… in the sidebar:** screens you don't use often can go there, out of the way (Settings → Screens), and keep working. Timers, Quotes and Rewards start there. Settings and Help are always last.
  - **Text size** (Small, Standard, Large, Larger; also Ctrl/⌘ with + − 0) and **spacing** (Comfortable or Compact) in Settings.
  - **Search** on Workflows and Events (Events searches all stored history), and Ctrl/⌘+F to jump to a screen's search.

- M2 slice 8, workflow groups:
  - **Groups on the Workflows screen:** each with a switch that turns its workflows off and back on without losing which ones were on, and Rename. The editor has a Group field; a group comes and goes with its workflows.
  - **Groups fold:** click a group's name to show or hide its workflows, or Open all and Fold all. Groups start folded, the app remembers which you opened, and a search opens the groups it finds something in.
  - **Templates go into groups:** Alerts, Thanks, Commands, Quotes and Scenes.
  - A group can't come on if it would make two workflows answer one chat command.
  - `streamgremlin workflows groups`, `workflows group NAME on|off` and `workflows group NAME rename NEW`.

- M2 slice 9, First Words:
  - **A new trigger:** "A viewer chats for the first time this stream", once per viewer, starting over when you go live (OBS or Twitch), when you start it over, or after 12 quiet hours. Your own messages never count.
  - **Two templates** in a new Welcome group: welcome first-time chatters in chat, or on screen with their name and profile picture.
  - **A First words card on Events:** since when it counts, how many viewers have spoken, and Start over. Also the step "Start First Words over" and `streamgremlin first-words [reset]`.

- **Several ways to say a message** (D-036): any message in a workflow (chat, alert title and text, notes) can have more than one version with "+ Another way to say it"; one is picked at random each time. The "Welcome first-time chatters" template uses three. Sounds too: "Play a sound" and an alert's sound can be several sounds ("+ Another sound"), one played at random.

- **Mark as seen** on Home's "workflow runs failed" notice (D-037): it then counts only new failures. The runs and traces are kept.

- **Alert sounds play in the overlay** (D-038): OBS hears them through the browser source, so they reach the mixer, the stream and recordings (tick "Control audio via OBS" on the source). The run says whether the sound started.

- M2 slice 10, Hype Trains (Affiliate or Partner; checked against mocktwitch only):
  - **Triggers:** a Hype Train starts, moves (under More) and ends, with its level, total, progress and top contributor.
  - **A template,** "Thank the Hype Train".
  - The sign-in asks to read Hype Trains: sign in to Twitch again once.

- M2 slice 11, polls and predictions (Affiliate or Partner; checked against mocktwitch only):
  - **A Polls screen** (under More…): the running poll with live votes, the prediction with its bets, and starting, ending, locking, resolving and canceling, with a confirmation before paying out or refunding.
  - **Triggers:** polls start, get votes and end; predictions start, get bets, lock and end.
  - **Steps:** Start a poll, End the poll, Start a prediction, End the prediction.
  - **Templates:** announce poll results, announce prediction results.
  - `streamgremlin polls`, with `start`, `end`, `predict`, `lock`, `resolve` and `cancel`.

- M2 slice 12, ads (Affiliate or Partner; checked against mocktwitch only):
  - **An Ads card on Home:** the next ad, snoozes left, a running break with the time left (an estimate: Twitch doesn't say when ads end), Run an ad now and Snooze the next ad, and what was asked, in words.
  - **Triggers:** an ad break begins; an ad break should be over (estimated); under More, Twitch accepts, refuses or doesn't clearly answer a request, and a snooze.
  - **Steps:** Run an ad break, Snooze the next ad. A refused or unclear request fails the step, so nothing after it announces an ad. From chat, only your own message runs them.
  - **Templates:** "Tell chat about ad breaks" and "Run an ad from chat" (`!ad`, you only).
  - A request is sent once and never retried. If Twitch's answer is unclear, StreamGremlin checks Twitch's schedule rather than asking again.
  - `streamgremlin ads`, `ads run SECONDS` and `ads snooze`.
  - The sign-in asks to read the ad schedule, run ads and snooze them: sign in to Twitch again once.

- M3 slice 1, giveaways:
  - **A Giveaways screen:** open a giveaway, watch entries come in, draw, set a winner aside and draw again, end or cancel, and see every chat message it sent (or couldn't). A practice giveaway takes test chat from Events and sends nothing.
  - Viewers type `!join` (or your command). One entry each, and every entry has the same chance, as chat is told. Entrants hear they're in, a few names to a message. Subscribers only, closing entries after a set time, and claiming with `!claim` within a time are options.
  - **Checkable draws:** winners are drawn with a secure random source, and closing entries fingerprints the list (SHA-256). **Check the draws** in the app, or `streamgremlin giveaways verify`, confirms each winner from the record.
  - While a giveaway runs, it holds its chat commands: no workflow answers them meanwhile.
  - **Triggers** for a giveaway opening, closing, ending, and a winner drawn, claiming or not; **steps** to open, close, draw and end.
  - `streamgremlin giveaways`, with `open`, `close`, `draw`, `set-aside`, `end`, `cancel`, `show` and `verify`.
  - Giveaways sits in the sidebar after Overlays, so Variables' shortcut is now Ctrl/⌘+8.

### Fixed

- With no bot signed in, a workflow that answers chat could answer its own messages, over and over. Its own messages are no longer treated as your chat (D-023).

### Changed

- The Twitch sign-in asks for four more permissions, to read follows, subs, bits and channel point redemptions. Sign in again once to get them; chat keeps working meanwhile.
- The Twitch sign-in also asks to manage channel point rewards. Sign in again once to make and change rewards.

- Screen shortcuts follow the sidebar: Ctrl/⌘+1–9 for the main screens in order (at first: Home 1, Connections 2, Events 3, Workflows 4, Sounds 5, Overlays 6, Variables 7); Settings is Ctrl/⌘+comma and Help Ctrl/⌘+/.
- A workflow's On switch shows what the engine says: a refused turn-on no longer looks on.
- Toggle buttons that are on (such as a Variables filter, or Runs on a workflow) show a check mark and an accent border.

- Development schema changes are now new numbered files, so data folders keep working between builds. They're squashed into one baseline at the first public release.

- The baseline schema gained the secrets tables. A development data folder from slice 1 has to be moved aside.
