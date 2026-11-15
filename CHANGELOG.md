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
- **YouTube sign-in** (M6 slice 1, D-096, ADR-0046): **Connections → YouTube** signs in your channel, and a bot channel if you like, with Google. For now you use a Google project of your own, which gives you your own daily YouTube quota: the card's guide walks through making one (about 10 minutes, free). The card shows about how much of today's 10,000 quota units StreamGremlin has used, and says when YouTube says it's used up. In the terminal: `streamgremlin youtube`, `youtube client ID -secret` (the secret from standard input), `youtube sign-in [-bot]`, `youtube sign-out [-bot]`. `mockyoutube` (`make mockyoutube`) stands in for Google to try it.
- **YouTube chat** (M6 slice 2, D-097, ADR-0047): once you're signed in, StreamGremlin finds your live YouTube broadcast and reads its chat. It checks every 2 minutes while you're not live, at once when your Twitch stream starts, or when you click **Find my stream**. Messages go to the event log for now and join Live's chat in the next update. A dropped connection or a restart carries on where it left off, without missing or repeating messages. **Connections → YouTube → Chat** and **Live** say whether YouTube chat is being read, and why not. In the terminal: `streamgremlin youtube find`. `mockyoutube` can go live and chat at `http://127.0.0.1:7700/mock`.
- **One combined chat** (M6 slice 3, D-098): **Live** shows Twitch and YouTube chat in one list, each line marked TW or YT. The send box sends to both by default, or to just one. Your commands and chat workflows answer YouTube viewers too, on YouTube: a reply starts with @name, since YouTube has no replies. A chat trigger's **Which chat** limits it to Twitch or YouTube, and a "Send a chat message" step's **Where** chooses Twitch, YouTube or both. Left unset, it answers where the message came from, and follows, timers and buttons stay on Twitch. YouTube channel members count as subscribers. First words and timers count YouTube chat. Each message to YouTube costs 20 quota units, and longer than 200 characters it's split. In the terminal: `streamgremlin chat send -to twitch|youtube|both`, and `events simulate -source youtube`. Chat filters, points and giveaways don't include YouTube yet.
- **YouTube's events** (M6 slice 4, D-099): Super Chats, Super Stickers, Jewels gifts, new members, member milestones, gifted memberships, members-only chat, bans and timeouts, and your YouTube stream going live and offline are now triggers, from the chat StreamGremlin already reads. Memberships use the subscription triggers, so your sub thanks and alerts work for YouTube members. New templates under **YouTube events** show alerts and thank people in YouTube chat, and Discord can announce going live on YouTube. Your existing go-live and go-offline workflows were set to **Twitch only** the first time this version starts; choose YouTube under **Which platform**. Amounts are shown as YouTube gives them, with no currency conversion.
- **Moderating YouTube** (M6 slice 5, D-100): YouTube lines on **Live** have the gear and the viewer card. You can delete a message, time out, ban, undo a ban or timeout made from StreamGremlin, and make or remove a moderator, as your channel. Each says its quota cost before it's sent and is kept under **Done from StreamGremlin**. Messages deleted on YouTube, by anyone, are struck through. **Chat filters** watch YouTube chat and delete there by default; a timeout rule can time out on YouTube too. A new step, **Moderate the viewer**, deletes, times out or bans the viewer behind a workflow's event, on Twitch or YouTube. `streamgremlin mod -youtube …` does the same from the command line. YouTube's API can't change members-only or slow mode, or undo a ban made elsewhere: use YouTube Studio for those.
- **The website** (M9 slice 4, D-112): `make site` builds streamgremlin.com: a home page with screenshots, the features and a comparison with other stream bots; Download with the installers' checksums and the beta guide; Help with a picture of each screen; Privacy, Security and this changelog. It also builds the go-import pages for code.streamgremlin.com. The app's Help and the site's Help are now one text. `make screenshots` photographs the app with made-up data.
- **Ban lists show how far every step has got** (D-113): reading the file, looking names up on Twitch, checking against Twitch and copying now each have a progress bar, a pace and an estimated finish, like banning already did. While names are looked up, the screen shows what's been found so far (to ban, already banned, not found, left alone). A list waiting behind another says which. `streamgremlin bans` prints the same. "Twitch says to wait a moment" now shows only on a list that's running.
- **code.streamgremlin.com is a site of its own** (D-116): a home page saying what the module is, how to get the code and install the command line, and how to help, with every program and package listed by its package comment; a page for each, styled like streamgremlin.com, with links to its source on GitHub and its documentation on pkg.go.dev; and a 404. Every package page keeps the go-import tag and adds go-source, so pkg.go.dev links to the source.
- A flaky acceptance test is fixed: 72 waits in the acceptance tests took empty output as success (D-116).
- Inner pages of streamgremlin.com lost their side margin above the text (a heading touched the screen's edge on a phone); fixed.
- The website links to GitHub from its header, the home page and Download; the README starts with links to the website, Help, issues and security.
- **Reports go to GitHub** (D-115): bugs and ideas as issues, with forms for each, and security problems as private security advisories. Help in the app has a button for each; the beta guide, the website and `SECURITY.md` say the same. The code is at github.com/shawnbutts/StreamGremlin, and the website's go-import pages point there.
- **Linux builds** (M9 slice 2, D-114): `make release-linux` makes a .deb (Ubuntu, Debian), an .rpm (Fedora) and a .tar.gz, for Ubuntu 24.04, Debian 13, Fedora 42 or newer, 64-bit Intel and AMD. The app is compiled in a container; the packages come out the same bytes each time. `make linux-smoke` installs each in a fresh container and starts the app. Linux is a beta, lightly tested: the website, Download and the beta guide say so.
- **The raid warning on Live** is now "A burst of new chatters", says a friendly raid needs nothing but Dismiss (lock chat or ban only for a hate raid or a flood of bots), and has room around its text.
- The website's Features page has its own Ban lists section, with a screenshot of a 250,000-name list being looked up.
- **Hardening** (M9 slice 1, D-111, ADR-0050): a test proves every API route needs the token, the right permission and a local address; fuzz tests cover everything read from outside (chat, Twitch and YouTube events, add-ons, packages, imported files); a load test sends 2,000 chat commands at once and checks each runs exactly once. `SECURITY.md` says how to report a problem privately.
- **Follow-bot review** (M8 slice 5, D-109): on **Ban lists**, find Twitch follows from accounts made only days before they followed that never chatted, with those in a burst marked and ticked, then **Block** or **Ban** the ones you pick (asking first), as a list you can undo. Command line: `streamgremlin followbots`. Sign in to Twitch again to allow blocking.
- **New accounts and Text art filters** (M8 slice 4, D-108): two more chat filters. **New accounts** catches messages from Twitch accounts made only days ago (7 by default; subscribers and VIPs left alone unless you say). **Text art** catches pictures drawn in text with Braille dots, blocks and box lines. Both delete, time out or warn like the other filters, and are off until you turn them on.
- **The raid guard** (M8 slice 3, D-107): StreamGremlin notices a hate raid as it starts: a burst of chatters it has never seen, or of brand-new Twitch accounts, watched more closely right after a raid. It warns on **Live**, with **Lock chat**, **Ban these accounts…** (asks first; undoable on Ban lists) and **Dismiss**, and starts the new trigger **A raid looks suspicious**. Set it up on **Chat filters → Raid guard**: warn, warn and lock chat, or off, with your own thresholds and a test button. It never unlocks on its own. Command line: `streamgremlin raidguard`.
- **Ban lists** (M8 slice 2, D-106, ADR-0049): a **Ban lists** screen imports your own list of accounts to ban on Twitch from a file (one name or id a line, or a CSV), up to 5 million names. It checks every name first and shows what it will do: how many it will ban, how many are already banned, which don't exist, and who it leaves alone (you, your mods and VIPs, viewers with points). Nothing is banned until you confirm. Then it bans at Twitch's pace in the background, leaving room for chat and moderating, waiting while you're live, and carrying on after a restart without banning anyone twice. A newer file bans only its new names; **Check against Twitch** notices bans lifted by hand and leaves them alone; **Undo** lifts only the list's own bans. **Copy Twitch's ban list** and **Save to a file** export. Command line: `streamgremlin bans`.
- **Lock chat and Shield Mode** (M8 slice 1, D-105): on **Live**, **Lock chat** turns on Twitch's Shield Mode, emote-only and followers-only (followed at least 10 minutes) in one press, for when a raid starts; **Unlock chat** puts back only what it changed. **Shield Mode** has its own switch, and its own trigger and step for workflows. New templates under Moderation: **Shield Mode on when I go offline**, **Shield Mode off when I go live**, and **!lock and !unlock chat** for mods. Command line: `mod shield on|off`, `mod lock`, `mod unlock`. Sign in to Twitch again to allow Shield Mode.
- **Add-ons on every chat** (M6 slice 8, D-103): an add-on's chat message goes back where its event came from (a YouTube viewer is answered on YouTube), or, with no viewer behind it, to every chat being read. Add-ons can choose with `sg.chat(text, {to = "twitch" | "youtube" | "both"})`. A platform added later reaches add-ons without any change to them. The add-on's activity says which chats each message reached. The example **Dice tray** (1.4.0) answers each viewer where they typed and announces rounds in both.
- **YouTube's viewer count, title, subscribers and polls, and active chatters** (M6 slice 7, D-102): while YouTube chat is read, StreamGremlin checks each minute (2 quota units) for the viewer count, title and description, and new public subscribers. **Live** shows YouTube's viewer count and a **YouTube stream** panel to change the title and description (51 units). A new public YouTube subscriber is a follow (**Someone follows or subscribes on YouTube**). **Polls** runs YouTube polls (2 to 4 choices, until you end them; 20 units to start and to end), with the steps **Start a YouTube poll** and **End the YouTube poll**, and the poll triggers fire for YouTube. New triggers: **The viewer count changes** (Twitch and YouTube, by a tenth or more) and **Active chatters** (every 5 minutes while live, who spoke in the last 10 minutes). New step: **Change the YouTube title or description**. Workflows you already had keep their follow, title and poll triggers to Twitch. Command line: `youtube title`, `youtube description`, `youtube poll`, `youtube poll end`.
- **YouTube viewers and points** (M6 slice 6, D-101): YouTube chatters are viewers, with points, on **Viewers** and their card. They earn from chat, time in chat (while you're live on Twitch), memberships (as tier 1 subs) and, if you set it, a flat amount per Super Chat. They can enter giveaways, which now speak in both chats. With the new **Link Twitch and YouTube** template, a viewer types `!link` and their name on the other platform, then the code they're given there, and their two accounts share one points balance. You can also link and unlink on **Viewers**. A name that's two different people is no longer guessed. The Discord stream summary counts YouTube too.
- **OBS: one connection when turned on, and never "connected" without one.** Saving OBS's settings just before StreamGremlin first read them connected twice, and in between the status could say connected while a scene switch failed with "not connected". Fixed, with a test that catches it (D-094).
- **Ads: a break is always linked to the request that asked for it** (D-094), even when Twitch says the break began before it answers the request.
- **Static overlays, and animations fixed** (D-093): every overlay can be **Static (no motion)**. A clock or timer with a look now comes in with that look (it came in as Classic first) and sits where the overlay is placed. Unfold no longer cuts off a clock, timer or giveaway's box.
- **Clock and timer overlays** (D-092, ADR-0045): the time (12- or 24-hour, with seconds and the date if you like), a countdown like "Starting soon", or a stopwatch, styled like your alerts.
  - **Overlays → Add an overlay → Clock and timers**, then choose what it shows. Countdowns and stopwatches have **Start**, **Pause**, **Reset**, **+1 minute** and **Count down until** a time; they keep going across restarts. **Start it whenever OBS loads the overlay** starts one afresh each time OBS loads or refreshes it (with “Refresh browser when scene becomes active”, as its scene shows).
  - **Steps:** Start a countdown or stopwatch, Pause, Reset, and Add time to a countdown, for buttons and Stream Deck keys. **A countdown ends** runs workflows at zero.
  - **Looks for timers:** **Workshop**, **Neon arcade** and **Selector board**, matching the wheel looks, are built in. Add-on themes can now restyle clocks and timers, in the same sandbox as wheel looks. **Flip clock** (`examples/themes/flip-clock`: zip the folder to install it) is an example: split-flap cards that flip as each digit changes.
  - **Command line:** `overlays add -timer NAME`, look flags (`-mode`, `-length`, `-label`, `-end-text`, `-24h`, `-clock-seconds`, `-date`), and `overlays timer NAME start|pause|reset|add`.
- **A Now playing overlay** (D-091): the song your music player is playing, with its album art, from the files it writes for streaming software (Pretzel's Write to File, for one).
  - **Overlays → Add an overlay → Now playing**, then choose the song's text file and album art with **Browse…** in its settings (the system's own file dialog), or paste their paths. The file's first line is the title, its second the artist. Set the player's paused text there too, and the overlay hides while paused. The settings say what's showing, or what's wrong (a missing file, a picture that isn't one).
  - The song and its cover, or bouncing bars without a cover, in the same looks as alerts. It starts in the bottom-left corner. It stays up while a song plays, or shows each song for a few seconds as it starts. A new song shows within a second.
  - The files are read only while the overlay is open, and the paths never reach OBS. The cover is served only through the overlay's private link, and only if it's a JPEG or PNG.
  - **Command line:** `overlays add -song NAME`, and `overlays look NAME -song-file PATH -art-file PATH -paused-text TEXT -song-hides`.
- **Discord** (D-089, ADR-0044): post to your Discord server from workflows, through webhooks: no bot to set up.
  - **Connections → Discord:** paste a webhook's link (Discord: Server Settings → Integrations → Webhooks → Copy Webhook URL) and name it. **Send a test**, or **Forget…** it. The link is kept with your passwords and never shown again.
  - **The step "Post to Discord":** a message and a card (title, text, link, picture, colour, footer). Nobody is notified unless you choose @everyone or @here, so a viewer's name or message can never ping anyone. Sent once.
  - **Templates, under Discord:** **Announce on Discord when you go live** (title, category, a preview of the stream and its link), **Post new clips to Discord**, and **Sum up the stream on Discord** when you go offline (how long, new followers, subs, bits, raids and chatters).
  - **New:** the trigger **A clip is made**, whenever StreamGremlin makes a clip, and the step **Sum up the stream**. **Read the stream** also gives your channel's link and a preview picture.
  - **Command line:** `streamgremlin discord`, `discord add NAME` (paste the link on standard input), `discord test NAME`, `discord forget NAME`. `mockdiscord` stands in for Discord to try it (`make mockdiscord`).
- **Twitch's Blocked Terms** (D-088): the Chat filters screen lists Twitch's own Blocked Terms (which Twitch holds back before anyone sees them), with **Remove** for each. **Copy my words to Twitch** adds your filter's words Twitch hasn't got; **Copy Twitch's terms to my words** brings Twitch's into your word list. Copying only adds, and each term shows Twitch's answer. Command line: `streamgremlin filters blocked`, `filters block TERM...`, `filters unblock ID`. Twitch sign-in asks for one more permission, to manage blocked terms: sign in again on Connections.
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

- **A chat surge no longer loses messages** (D-111): when hundreds of chat messages arrived at once, the connection to Twitch could back up and drop, losing what was still on the way. A message Twitch delivers twice no longer logs a second warning.
- **No gap under Live's chat** (D-088): an empty notice line under the send box took room; chat now ends at the panel's edge.
- **Live fits the window again** (D-087): a long chat no longer grows the screen and puts a scroll bar over the buttons and chat modes. Chat keeps its own scroll bar. The buttons and chat modes no longer scroll sideways: the Followers-only and Slow choices shrink, and wrap under their buttons when the side is narrow. The side lines up with the Right and Top buttons above it.
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
