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
- M3 slice 2, giveaway entry rules:
  - **Who can enter:** followers, followed for at least so many days, a Twitch account at least so many days old, subscribed for at least so many months, so many chat messages this stream first, and no giveaway win in the last so many days. Rules decide who may enter, never anyone's chance.
  - Twitch is asked when someone enters, and never guessed: if it can't be asked, the viewer hears to try again. Anyone turned away hears why in chat, once, and again only if the reason changes. Typing `!join` again checks again.
  - **Anyone who chats enters** is an option, as well as the command.
  - The screen shows who was turned away and why, and what each entry was checked against. `giveaways open` takes the rules as flags.
  - Test chat messages and commands on Events can say how long a test viewer has followed, their account's age and months subscribed, for practice giveaways.
- M3 slice 3, more chances for subscribers:
  - A giveaway can give each subscriber 2 to 10 chances. Chat is told the odds ("subscribers get 3 chances to everyone else's 1") instead of "the same chance", and a subscriber's "you're in" names their chances.
  - Chances are set when someone enters and kept. Draws pick a ticket, and **Check the draws** and `giveaways verify` check tickets too. Earlier giveaways check as before.
  - `giveaways open -sub-chances N`.
- M3 slice 4, drawing from recent chat:
  - **Draw now from everyone who chatted recently:** pick a window of 1 to 60 minutes, and everyone StreamGremlin saw chatting then enters, once each. Winners are drawn at once, with the same record and checks as any giveaway.
  - Entry rules and subscribers' chances apply. Nobody is told they were left out; the screen lists them. Your own chat never enters.
  - `giveaways open -from-chat N`.
- M3 slice 5, the giveaway overlay:
  - **A giveaway overlay** (Overlays → Add an overlay → Giveaways): one browser source in OBS that shows the running giveaway, with how to enter, the count, a countdown, who can enter and the odds. On each draw, names roll and land on the winner StreamGremlin drew, then their picture pops in.
  - While it's open in OBS, chat names each winner as their roll lands, about 4 seconds after the draw. With no overlay open, nothing waits. Chat still tells viewers everything.
  - Practice giveaways show only in the app's preview. **Preview a draw** and **Sample in OBS** show a sample. Reduced motion shows the winner without the roll.
  - `overlays add -giveaway NAME`.
- M3 slice 6, wheels:
  - **A Wheels screen** (under More…): wheels of choices, like the next game, with a weight for each choice and the exact chance shown as you type. Spin, edit, delete, and see recent spins.
  - **Spins are drawn by StreamGremlin** with a secure random source and recorded. **Check the spin** and `wheels verify` confirm the result from the record.
  - **A wheel overlay** (Overlays → Wheels) spins to the result, with each wedge at its real size. While one is open in OBS, the result is announced when the wheel stops.
  - **"Spin a wheel"** waits until the wheel stops and gives its result to the next steps, so `!spin` or a channel point reward can spin and reply. **"Say a wheel's odds"** gives them for chat. A trigger: "A wheel stops". A template, **Spin a wheel from chat** (`!spin`).
  - `streamgremlin wheels` (add, set, delete, spin, verify) and `overlays add -wheel`.
- Sounds on giveaway and wheel overlays: on the Overlays screen, pick a sound from your library to play as a roll or spin starts, and another as it lands, each with its own volume. The overlay plays them, so OBS hears them. `overlays look NAME -start-sound S -end-sound S -cheer-sound S`.
  - A third sound, **When it has stopped** (or **When the winner shows**), plays half a second after the wheel stops or the roll lands, as the result shows: for a cheer. Chat names the result at the same moment.
- F4 slice 1, viewers and points:
  - **Viewer records:** everyone who chats, follows, subscribes or cheers, with when they were first and last seen and how much they've chatted. Recorded from real chat and events only, never test events. Export a viewer's record (through your system's save dialog), or forget them.
  - **Points:** a **Points** screen with who holds how many, each viewer's history, giving and taking by hand with a reason, undoing a mistake (once), renaming the points (like Gold), and **Check the ledger**. Nothing is changed or deleted, and nobody goes below zero.
  - **Steps:** "Give points", "Take points" (fails, changing nothing, when they haven't enough) and "Check points". A retried run never pays twice, and a test event never changes a real balance. A template: **Check your points** (`!points`).
  - `streamgremlin viewers` and `streamgremlin points`.
  - The Points screen sits after Giveaways, so Variables' shortcut is now Ctrl/⌘+9.
- F4 slice 2, earning rules: viewers earn points for chatting (at most once a minute each, only while you're live, by default), a first follow (once ever), subs and resubs by tier, gifted subs (to the gifter) and bits (per 100). Off until you turn it on under **How viewers earn** on Points, and nothing that happened before pays. Never test events or your own. `streamgremlin points earning`.
- F4 slice 3, time in chat: every 5 minutes while you're live, viewers who chatted in the last 10 minutes earn 10, and quiet viewers in your chat (from Twitch's list of who's there) earn 3. Its own switch under **How viewers earn**, off at first. Nothing is paid while you're offline, when it's unknown whether you're live, or for time StreamGremlin was closed, and the screen says why when no one, or only chatters, were paid.
- F4 slice 4, giving to everyone: under **Give to everyone** on Points, give everyone in chat, everyone who chatted lately, or your subscribers points at once. **See who** first, with the total; each viewer gets them once, even if it's tried again, and nothing is given if Twitch's list can't be read. Undo a whole give (nobody goes below zero; whoever has spent some is named). A workflow step, "Give points to everyone". **Export everyone** saves every viewer's record in one file. `streamgremlin points give-all`, `points gives`, `points undo-give` and `viewers export-all`.

- A **Viewers** screen, after Points (Ctrl/⌘+9): find a viewer, their record, points and history, give, take, undo, export and forget, and Export everyone. These moved off the Points screen; a top viewer's name on Points opens them there. Variables no longer has a default shortcut.
- M5 slice 2, wheel looks: on **Overlays**, a wheel overlay's **Look** can be Classic, Workshop, Neon arcade or Selector board (a list that rolls to the result, with each choice's chance), with a title and an accent colour. The new looks run sandboxed, from their own address with no network, and only show the result StreamGremlin drew; if one can't show a spin, Classic does. `streamgremlin overlays look NAME -look workshop`.
- Wheel look sounds: Workshop, Neon arcade and Selector board each have their own start, stop and cheer sounds, made by StreamGremlin. They play for any sound you leave empty under **Sounds**, at its volume; a sound you pick still wins. Turn them off with **Play the look's sounds**, or `overlays look NAME -look-sounds=false`.
- M5 slice 3a, add-ons: an **Add-ons** screen installs wheel looks from a ZIP. It shows what a look is before asking (who published it, marked not verified; that it runs sandboxed with no internet; that its odds can't be checked), and removing one puts its overlays back to Classic. Installed looks appear under **Look** on Overlays with their own settings. `streamgremlin packages`, `packages install FILE -yes`, `packages remove ID -yes`, `packages history`, and `overlays look NAME -look ID -set KEY=VALUE`. An example look is in `examples/themes/paper-wheel`, with its own paper sounds; it now turns as it spins (it had jumped straight to the result).
- M5 slice 3b, part 1 (D-067): add-on wheel looks only load while the wheel's OBS browser source has **Page permissions** at "No access to OBS", because OBS lets a look's frame use whatever that setting allows. At any other level Classic shows instead, and the status line (`?status=1`) and the engine's log say why. Built-in looks still load at any level. With `?status=1`, a look's status line now names the level OBS gives it.
- M5 slice 3b (D-068): installed wheel looks can be updated and rolled back. Installing another version of an installed look updates it: the check shows what it replaces, overlays using it switch together keeping the settings it still takes (and say which went back to the default), and a spin on screen finishes first. Two earlier versions are kept, and **Roll back** on Add-ons (or `streamgremlin packages rollback ID`) returns to the one before. A version from another publisher needs that publisher's name typed exactly (`-publisher NAME` in the terminal). The screens no longer say a look "only shows the result StreamGremlin drew": a look draws what its maker chose, and the real result is always in chat. A hostile test look tests what a look can't do.
- M5 slice 4 (D-069): Lua scripts. "Run a Lua script" (under More steps) runs Lua you write in the step. It reads the values passed as `sg.args` and the event as `sg.event`, logs with `sg.log` or `print`, and what it returns is the step's result for later steps. It runs in a separate process with a time limit (1 second unless set, at most 30) and 64 MiB of memory, and can't reach chat, OBS, files or the internet. `math.random` uses a secure random source. **Try it** in the step, or `streamgremlin lua try FILE`, runs a script once with a test chat message.
- M5 slice 5a (D-070): Lua plugins. A plugin is installed from a ZIP on **Add-ons**, like a wheel look: the screen shows the steps it adds and asks you to tick each permission it wants (in 5a, sending chat). It's installed off; turn it on with its switch. Its steps appear in the workflow editor under its name, and it keeps its own memory while it's on. Its settings are on its card, and **What it has done** lists every message it sent or was refused, and every line it logged. Removing it turns off the workflows that use it. `streamgremlin packages install FILE -yes -allow chat.send`, `packages on|off ID`, `packages set ID KEY=VALUE`, `packages activity ID`. An example plugin, **Dice tray**, is in `examples/plugins/dice-tray`. In **Add a step**, an add-on's steps come after StreamGremlin's own, marked "(add-on)". A plugin can bring templates: the gallery shows them in the add-on's own section, "Dice tray (add-on)" (D-071); Dice tray 1.1.0 brings `!roll` and `!best`.
- New workflows start on (D-077), whether made in the editor, from a template (StreamGremlin's or an add-on's), from Timers or Live, or with `streamgremlin workflows save`. One whose chat command another workflow that's on already answers starts off, and says why. Saving an existing workflow keeps its switch.
- M7 slice 1 (D-076, ADR-0040): a **Live** screen, second in the sidebar (Ctrl/⌘+2), to run a stream from. Your Twitch chat fills the middle as it arrives, with badges in words and a box to send as you or the bot. Your buttons sit down the right side or along the top (your choice, kept). Pressing one runs the workflows that start from it, through the new trigger **You press a button**, and says which it started. A retried press never runs twice. **Edit buttons** makes, colours, orders and deletes buttons, and **Make a workflow** gives a new one something to do. In the terminal: `streamgremlin buttons`, `buttons set ID -label TEXT`, `buttons press ID`, `buttons move`, `buttons delete`. Every Ctrl/⌘+number shortcut after Home moves down one.
- M7 slice 2 (D-078): a viewer card on **Live**. Rest the pointer on a chatter's name (or click it, or press Enter) to see what Twitch says about them now: when their account was made, whether they follow and since when, their subscription (tier, gifted and by whom), VIP, moderator, and whether they're banned or timed out. Anything Twitch didn't give says unknown and why, never "no". Below it, StreamGremlin's record: first seen, messages, points, and their recent chat. Twitch sign-in now asks for two more permissions, to read moderators and bans, and VIPs: sign in again on Connections. `streamgremlin viewers card @NAME`.
- The window remembers its size, place, and whether it was maximised (D-082), and opens there next time. It's never put on a screen that's gone: then it opens centred. Every screen now uses the window's whole width, and the window no longer shows scroll bars for more than there is: a screen scrolls inside only when it's taller than the window. On **Live**, the sidebar stays on the left, the buttons and chat modes on the right, and chat stretches in between and fills the height as you resize.
- **The bot's replies show on Live** (D-087), marked **Bot**; a workflow's messages sent as you are marked **Workflow**. They're stored as `chat.message.sent`, which never starts a workflow, so StreamGremlin still never answers itself.
- **Home says when to sign in again** (D-087): when your Twitch sign-in, or the bot's, lacks permissions StreamGremlin now asks for, **Needs you** says so and what they're for.
- **Channel commands** (D-086): new templates `!title` and `!game` (anyone sees the title or category; mods and you change them, `!game` taking the closest Twitch category to the name), `!uptime`, `!followage` (yours, or `!followage name`), `!clip` (mods, VIPs and you; posts the link once Twitch has made it) and `!marker` (mods and you). Their steps are in the workflow editor under Twitch, for buttons and other workflows too: **Change the stream title**, **Change the category**, **Make a clip**, **Add a stream marker**, **Read the stream** and **Read when a viewer followed**. Twitch sign-in asks for two more permissions, to change your title and category and to make clips: sign in again on Connections.
- **Chat filters** (D-085): a **Chat filters** screen, after Viewers in the sidebar, catches spam in your Twitch chat.
  - **Six filters, each with its own switch:** links (allowed sites never count; `twitch.tv` at first), capitals, symbols, too many emotes, the same message again and again, and your own words and phrases (a `*` at a word's start or end matches the rest).
  - **What happens:** each deletes the message, times the viewer out or warns them, as you, and can leave VIPs or subscribers alone. You, your moderators and your bot are never filtered. A filter never bans.
  - **Recorded:** each action is sent to Twitch once and listed under **Caught lately** with Twitch's answer, and in the moderation history.
  - **Try a message** says which filter would catch it, before you save.
  - **For workflows:** the trigger **A chat filter catches a message**, the step **Let a viewer post a link**, and two templates under the new Moderation category: **Permit a link** (`!permit name`, mods and you) and **Say why a message went**.
  - **Command line:** `streamgremlin filters` and `filters check [-role vip] TEXT`.
  - Chat messages' events now list the emotes used (`emotes`).
- **Beta installers** (D-084, ADR-0043): `make release VERSION=0.1.0-beta.1 TWITCH_CLIENT_ID=…` builds a Windows installer and a macOS disk image (Apple silicon and Intel), with the Stream Deck plugin and a beta guide (`docs/BETA.md`) in each.
  - **On Windows:** it installs for you only, without an administrator, adds a Start menu entry and an optional desktop shortcut, and appears in Apps & features. Its last page can install the Stream Deck plugin. Its uninstaller keeps your data.
  - **On macOS:** drag the app to Applications.
  - **Unsigned for now:** the guide says how to get past the warnings.
- **Help** explains every screen, with new entries for Giveaways, Points, Viewers, Wheels, Add-ons, Ads, Backups and the window. A new **Something went wrong?** section says what to put in a report, with the version and where the logs are.
- **Licences** (D-083): `THIRD-PARTY-NOTICES.md` credits every Go library in the app and command line, and `THIRD-PARTY-LICENSES.txt` has their licences in full (`make notices` rewrites it; `make verify` checks it).
- M7 slice 5 (D-081, ADR-0042): the **StreamGremlin plugin for Stream Deck**. Double-click `com.streamgremlin.streamdeck.streamDeckPlugin` (from `make streamdeck`) to install it in Stream Deck 7.1 or later, on macOS or Windows. Drag **Press a button** onto a key, pair it in the key's settings with the code from Connections, and pick which of your buttons it presses. The key shows the button's colour, the same icon as in the app (Tabler or Nerd Fonts) and its label, dims when the button starts nothing that's on, and says **Not running**, **Pair first** or **Button gone** rather than ever looking like it works when it doesn't. A press runs once, even if it's sent again after a lost answer.
- M7 slice 4 (D-080, ADR-0042): pairing a Stream Deck. **Connections → Stream Deck → Pair a Stream Deck** shows a one-time code to type into the StreamGremlin plugin (coming in slice 5); it works once, for 5 minutes. A paired Stream Deck gets its own key, which can only see and press your buttons, and its presses say they came from the Stream Deck. Paired decks are listed with when they were last used; **Forget…** stops one at once. `streamgremlin streamdeck`, `streamdeck pair`, `streamdeck forget ID`.
- M7 slice 3 (D-079, ADR-0041): moderating from **Live**.
  - **Coloured buttons are filled with their colour** on Live and on Stream Deck keys, with dark text and icons that read on every colour.
  - **Button icons:** in **Edit buttons**, pick an icon for a button from **Tabler Icons** or **Nerd Fonts** (Material Design, Font Awesome, Codicons and more): suggestions, or search by name (pause, music, twitch). **Show only the icon** hides the label, which is still its name for screen readers and on hover. Icon-only buttons are small squares that sit side by side. The icons are bundled with the app, so they work offline; their licences are under Help → Icon credits and in `THIRD-PARTY-NOTICES.md`. `streamgremlin buttons set ID -icon player-pause -hide-label`, or `-icon nf-md-microphone`.
  - **A tighter Live screen:** the Buttons and Chat modes panels have proper padding, and smaller buttons and controls.
  - **Badges are one letter** on Live (B broadcaster, M moderator, L lead moderator, V VIP, S subscriber, F founder, T Twitch staff, ✓ verified, A artist), with the word on hover and for screen readers.
  - **Asks first** before anything that can't be taken back, to guard against mis-clicks: deleting a message, a timeout, a ban, a warning, a shoutout, and making a moderator. Each question names who and what (the message, the length, the reason). Unban, VIP, removing a moderator, Monitor, Restrict and chat modes are easy to undo, so they don't ask.
  - **A message:** the gear (⚙) beside the chatter's name opens **Delete message…**, **Time out 10 minutes…**, **Ban…** and **More in their card…**. It sits by the name, so a long message doesn't push it away, and works from the keyboard (arrow keys, Escape).
  - **A chatter's card:** time out for 1 minute to 1 week with a reason, ban or unban, warn, shoutout, make or remove a VIP or moderator, and see what was done to them.
  - **Chat modes:** turn emote-only, followers-only, slow, subscribers-only and unique chat on and off.
  - **Done once:** each action is sent to Twitch once, as you, and kept with Twitch's answer under **Done from StreamGremlin**. If Twitch doesn't answer, it says unknown and doesn't send it again.
  - **Suspicious users:** Twitch's flags (likely or possible ban evader, banned in a channel you share bans with) mark their messages and card. **Monitor** and **Restrict** change how Twitch treats them, and the workflow trigger **A suspicious user chats** reacts to them.
  - **Sign in again:** Twitch sign-in asks for the moderating permissions, so sign in again on Connections.
  - **Command line:** `streamgremlin mod timeout|ban|unban|warn|delete|shoutout|vip|mod|restrict|modes|history`.
- M5 slice 6 (D-074, ADR-0039): points for plugins, as rounds. A plugin with the new permission to hold viewers' points in rounds can take a viewer's stake, have StreamGremlin roll for it, and pay back up to the limit it states, or give the stakes back. The install check says its limits in words and that its odds are its own. A stake is always from the viewer whose command ran the step. Nothing is taken or paid twice. Open rounds are refunded when the plugin stops, changes, runs out of time or StreamGremlin restarts. Each round is recorded with the plugin's stated rules, every stake and every roll, under **Show its rounds** on the plugin's card (`streamgremlin packages rounds ID`). Dice tray 1.3.0 adds **!gamble**.
- M5 slice 5c (D-073): Lua plugins can start your workflows with their own events, and set timers. A plugin's events are under **When** in the workflow editor, after StreamGremlin's own, marked "(add-on)", with their values to use in messages. Sending them is a new permission, "Start your workflows with its own events", at most 10 a second, and every one is in **What it has done**. A plugin can't start a loop of its own events. Timers (`sg.after`) let a plugin do something later, like closing a round. Dice tray 1.2.0 has timed **dice rounds**, the events **A dice round ends** and **A new best roll**, and two templates: **!diceround** and **Cheer a dice round's winner**.
- M5 slice 5b (D-072): Lua plugins can be updated and rolled back. Installing another version of an installed plugin updates it. The check says what changes: permissions it asks for that you haven't allowed (tick them to update), steps it no longer has and the workflows that use them (turned off, not deleted), and settings that go back to the default. A plugin that's on starts again, fresh, as the new version. **Roll back** on its card (or `streamgremlin packages rollback ID`) returns to the version before; one that asks for more says so, and its button reads **Allow and roll back**.

### Fixed

- **Live fits the window again** (D-087): a long chat no longer grows the screen and puts a scroll bar over the buttons and chat modes. Chat keeps its own scroll bar. The buttons and chat modes no longer scroll sideways: the Followers-only and Slow choices shrink, and wrap under their buttons when the side is narrow.
- The Stream Deck plugin uses zod 4.6.7 in place of zod 3, which had a memory-use security advisory (D-083).
- A `!join` typed in the same millisecond as a giveaway opened could be missed.
- With no bot signed in, a workflow that answers chat could answer its own messages, over and over. Its own messages are no longer treated as your chat (D-023).

### Changed

- The Twitch sign-in asks for four more permissions, to read follows, subs, bits and channel point redemptions. Sign in again once to get them; chat keeps working meanwhile.
- The Twitch sign-in also asks to manage channel point rewards. Sign in again once to make and change rewards.
- The Twitch sign-in also asks to see who's in your chat, for time in chat. Sign in again once; until then only viewers who chat earn for time in chat.

- Screen shortcuts follow the sidebar: Ctrl/⌘+1–9 for the main screens in order (at first: Home 1, Connections 2, Events 3, Workflows 4, Sounds 5, Overlays 6, Variables 7); Settings is Ctrl/⌘+comma and Help Ctrl/⌘+/.
- A workflow's On switch shows what the engine says: a refused turn-on no longer looks on.
- Toggle buttons that are on (such as a Variables filter, or Runs on a workflow) show a check mark and an accent border.

- Development schema changes are now new numbered files, so data folders keep working between builds. They're squashed into one baseline at the first public release.

- The baseline schema gained the secrets tables. A development data folder from slice 1 has to be moved aside.
