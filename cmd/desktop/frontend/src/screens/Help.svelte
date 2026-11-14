<script lang="ts">
  // Help: how StreamGremlin fits together, in a few words, with shortcuts
  // and where your data lives. Help for a field sits beside the field; this
  // is the overview.
  import { onMount } from 'svelte';
  import { getStatus } from '../lib/api';

  let {
    shortcuts = [],
    ongo = () => undefined,
  }: { shortcuts?: { label: string; key: string }[]; ongo?: (screen: string) => void } = $props();

  let dataDir = $state('');
  let version = $state('');
  // The logs folder, written with the data folder's own separator.
  const logsDir = $derived(dataDir ? dataDir + (dataDir.includes('\\') ? '\\' : '/') + 'logs' : '');
  const mac = typeof navigator !== 'undefined' && /Mac/.test(navigator.platform);
  const mod = mac ? '⌘' : 'Ctrl';

  onMount(() => {
    getStatus()
      .then((s) => {
        dataDir = s.data_dir;
        version = `${s.version} (${s.commit})`;
      })
      .catch(() => undefined);
  });
</script>

<h1>Help</h1>
<p class="sg-muted screen-intro">How StreamGremlin fits together. Help for each field is beside it.</p>

<section class="sg-panel card" aria-labelledby="help-start">
  <h2 id="help-start">Getting started</h2>
  <p>
    <strong>Home</strong> has a Get set up checklist that ticks itself as you go: sign in to Twitch, turn on a workflow, test it,
    then watch it answer in your chat.
  </p>
  <div class="row-actions">
    <button type="button" class="button" onclick={() => ongo('home')}>Open Home</button>
    <button type="button" class="button" onclick={() => ongo('workflows')}>Open Workflows</button>
  </div>
</section>

<section class="sg-panel card" aria-labelledby="help-how">
  <h2 id="help-how">How it works</h2>
  <dl class="help-terms">
    <dt>Connections</dt>
    <dd>Your Twitch account (and, if you like, a bot account that answers under its own name) and OBS. Sign-ins and passwords are kept in your system’s keyring, never in files or logs. <strong>Stream Deck</strong> pairs the StreamGremlin plugin (install it by double-clicking <code>com.streamgremlin.streamdeck.streamDeckPlugin</code>, which comes with StreamGremlin: on Windows, tick “Install the Stream Deck plugin” at the end of installing, or find it in the Stream Deck folder where StreamGremlin is installed; on macOS, it’s in the disk image; it needs Stream Deck 7.1 or later) with a one-time code typed into a key’s settings; a paired Stream Deck can only see and press your buttons, and <strong>Forget…</strong> stops it. <strong>Discord</strong> adds webhooks, so workflows can post to your Discord server with “Post to Discord”: in Discord, open Server Settings → Integrations → Webhooks, make one for a channel, copy its link, and paste it here with a short name. Its link is kept with your passwords. The Discord templates announce going live, post new clips, and sum up the stream when you go offline. A post notifies nobody unless the step chooses @everyone or @here. <strong>YouTube</strong> signs in your channel (and, if you like, a bot channel) with Google. For now it uses a Google project of your own, which gives you your own daily YouTube quota: the card’s guide walks through making one (about 10 minutes, free); publish it, or Google signs you out every 7 days. YouTube allows 10,000 quota units a day, counted from midnight Pacific time; the card shows about how many StreamGremlin has used. Once you’re signed in, StreamGremlin finds your live YouTube broadcast and reads its chat: it checks every 2 minutes while you’re not live (1 unit each), at once when your Twitch stream starts, or when you click <strong>Find my stream</strong>. YouTube chat shows on <strong>Live</strong> with Twitch chat, each line marked TW or YT. The send box sends to both by default, or to one. Commands and chat workflows answer YouTube viewers too, on YouTube; a workflow’s trigger can listen to Twitch, YouTube or both (<strong>Which chat</strong>), and its “Send a chat message” step answers where the message came from unless it says otherwise. Each message sent to YouTube costs 20 quota units. YouTube channel members count as subscribers. Super Chats, Super Stickers, Jewels, memberships, members-only chat, bans and your YouTube stream going live or offline are triggers too; Templates → YouTube events has thanks and alerts for them. Going live and offline triggers say which platform under <strong>Which platform</strong>; workflows you had before YouTube was added stay Twitch only.</dd>
    <dt>Live</dt>
    <dd>
      The screen for while you’re live: your Twitch chat in the middle, with a box to send as you or the bot, and your buttons
      beside it or along the top (Buttons: Right or Top). A button runs the workflows that start from it, with the trigger “You
      press a button”: a scene, a sound, a message. <strong>Edit buttons</strong> makes, orders and deletes them, and gives each an icon if you like, from Tabler Icons or Nerd Fonts (search by name; “Show only the icon” hides the label, and icon-only buttons sit side by side as small squares), and “Make a
      workflow” gives a new one something to do. Badges in chat are one letter (rest the pointer on one for the word): B
      broadcaster, M moderator, L lead moderator, V VIP, S subscriber, F founder, T Twitch staff, ✓ verified, A artist. A button press has no viewer behind it, like a timer. Rest the pointer on a
      chatter’s name, or click it, for their card: what Twitch says about them now (following, subscribed, VIP, moderator,
      banned) and what StreamGremlin knows (points, recent messages). Anything Twitch didn’t give says unknown, never no.
      The card also moderates: time out (with a reason if you like), ban, unban, warn, shoutout, VIP and
      moderator, and how Twitch treats a suspicious user. The gear (⚙) beside a chatter’s name opens quick actions: delete
      the message, a 10-minute timeout, Ban, or their card. Anything that can’t be taken back asks first: deleting, a
      timeout, a ban, a warning, a shoutout and making a moderator (buttons ending in “…”). Each is sent to Twitch once, as you, and kept under “Done from StreamGremlin”; if Twitch
      doesn’t answer it says unknown and isn’t sent again. <strong>Chat modes</strong> turns emote-only, followers-only,
      slow, subscribers-only and unique chat on and off. Twitch’s suspicious-user flags (a likely or possible ban evader,
      someone banned in a channel you share bans with) show on their messages and card, and “A suspicious user chats” can
      start a workflow. YouTube lines are moderated too, as your YouTube channel, while its chat is read: delete (20 quota
      units), time out, ban, and make or remove a moderator (200 each). YouTube takes no reason, has no warnings, and can
      only undo a ban or timeout StreamGremlin made: undo others in YouTube Studio. Members-only and slow mode on YouTube
      change only in YouTube Studio; Live says when members-only turns on or off. Messages deleted on YouTube, by anyone,
      are struck through. The step “Moderate the viewer” deletes, times out or bans the viewer behind a workflow’s event,
      on the platform it came from.
    </dd>
    <dt>Events</dt>
    <dd>Everything StreamGremlin hears: chat messages, <code>!commands</code>, OBS scene changes. Test events look real to workflows but are marked Test.</dd>
    <dt>Workflows</dt>
    <dd>
      “When this happens, do that”: when a viewer types <code>!hello</code>, say hi back. Start from a template or build one in
      plain words. A new workflow starts on, unless another workflow already answers its command; its switch turns it off.
    </dd>
    <dt>Test</dt>
    <dd>Runs a workflow with a test event. Nothing reaches your chat or OBS, and saved variables don’t change; the run’s trace shows what it would have done.</dd>
    <dt>Runs and traces</dt>
    <dd>Every run is listed with whether it worked, and its trace shows each step: what it was given and what it gave back. A run that didn’t happen says why.</dd>
    <dt>One answer per command</dt>
    <dd>Each <code>!command</code> is answered by one workflow, so chat never gets two replies. Two can share a command when “Who can use it” never overlaps, like one for mods and one for everyone else.</dd>
    <dt>Timers</dt>
    <dd>
      Go off every so often, or on a schedule, and start the workflows that use them: a reminder about your socials every 15
      minutes. A timer can wait for viewers to be chatting, run only while you’re live, and run once.
    </dd>
    <dt>Quotes</dt>
    <dd>Numbered quotes from your streams. Viewers see one with <code>!quote</code>, and mods add them with <code>!quoteadd</code>. Deleting one never renumbers the rest.</dd>
    <dt>Sounds</dt>
    <dd>Your sound library, each with its own volume. “Play a sound” plays them in the StreamGremlin window, so keep the app open; OBS hears them only if it captures your computer’s sound (on macOS, add an audio capture source). A sound with an alert plays in the overlay instead, which OBS hears through the browser source.</dd>
    <dt>First words</dt>
    <dd>A viewer’s first message of the stream starts “A viewer chats for the first time this stream”, for a welcome. Going live starts it over; so does Start over on the Events screen. Your own messages never count.</dd>
    <dt>Overlays</dt>
    <dd>Alerts in OBS. Each overlay is one browser source: copy its private link into OBS as a browser source, 1920 by 1080, and workflows show alerts on it with “Show an alert”, one at a time, with the viewer’s profile picture and a sound if you like. The overlay plays that sound, so tick “Control audio via OBS” on the browser source and it gets its own mixer bar. A Test shows the alert in the app’s preview, never in OBS. A <strong>Now playing</strong> overlay shows the song your music player is playing, with its album art, in the same kinds of looks. It reads the files many players write for streaming software: the song as text, and its album art as a picture. In Pretzel, for one, turn them on in Settings → Advanced → Write to File. Then choose them with <strong>Browse…</strong> in the overlay’s settings, or paste their paths. The file’s first line is the title and its second the artist. It stays up while a song plays, or shows each song for a few seconds as it starts. Its size in OBS: a browser source of 1920 by 1080, like your canvas, fits everything; place the song with “Where alerts appear”. For a small source to drag around, use about 1100 by 200 at the usual text size (about 1550 by 260 at 150%), and set “Where alerts appear” to the corner the song should hug. A narrower source cuts long names short with “…”. A <strong>Clock and timers</strong> overlay shows the time (with the date if you like), a countdown such as “Starting soon”, or a stopwatch. Choose which in its settings, where countdowns and stopwatches have Start, Pause, Reset and +1 minute; workflows can start them too (“Start a countdown or stopwatch”), so a button or Stream Deck key can, and “A countdown ends” runs when one reaches zero, to switch scenes, say. They keep going if StreamGremlin restarts, or can start afresh whenever OBS loads the overlay: tick “Refresh browser when scene becomes active” on the browser source to start one as its scene shows. Pick Workshop, Neon arcade or Selector board under Look to match your wheels (they come in with the overlay’s animation, or none: choose “Static” under Placement for no motion), or install an add-on look like Flip clock from the examples.</dd>
    <dt>Rewards</dt>
    <dd>Your Twitch channel point rewards (Affiliates and Partners have them). Make rewards here, pause them, and mark redemptions done or refund them, by hand or with “Complete or refund the redemption” in a workflow. Rewards made on Twitch can start workflows, but only ones made here can be changed.</dd>
    <dt>Polls</dt>
    <dd>Twitch polls and channel point predictions (Affiliates and Partners): start them from the Polls screen or a workflow, watch the votes, end polls, and lock, resolve or cancel predictions. Twitch runs one of each at a time.</dd>
    <dt>Giveaways</dt>
    <dd>
      Viewers enter by typing a command in chat, once each; you can limit who may enter (followers, account age, subscribers)
      and give subscribers more chances, which chat is told. StreamGremlin draws winners with a secure random source and records
      every draw, so <strong>Check the draws</strong> can confirm them. A practice giveaway uses test chat from Events and
      sends nothing. You can also draw at once from everyone who chatted recently.
    </dd>
    <dt>Points</dt>
    <dd>
      Your channel’s own points (name them what you like): how viewers earn them (chatting, time in chat, follows, subs,
      cheers), giving to everyone, and who holds how many. Workflows give and take points with “Give points” and “Take
      points”. Nothing is ever changed or deleted: a mistake is undone, and nobody goes below zero.
    </dd>
    <dt>Viewers</dt>
    <dd>Everyone who has chatted, followed, subscribed or cheered, from your real chat and events. Find a viewer to see their record and points, give or take points, export their record, or forget them if they ask.</dd>
    <dt>Channel commands</dt>
    <dd>
      Templates for !title and !game (anyone sees them; mods and you change them), !uptime, !followage, !clip (mods, VIPs and
      you; it posts the clip’s link) and !marker. Their steps, under Twitch in the workflow editor, work from buttons too:
      “Change the stream title”, “Change the category” (the exact name, or the closest Twitch finds), “Make a clip”, “Add a
      stream marker”, “Read the stream” and “Read when a viewer followed”. They need you to sign in to Twitch again once.
    </dd>
    <dt>Chat filters</dt>
    <dd>
      Catch spam in chat: links (except allowed sites), capitals, symbols, too many emotes, repeats, and your own words. Each
      filter deletes the message, times the viewer out or warns them, as you, and never touches you, your moderators or your bot;
      each can leave VIPs or subscribers alone. They watch YouTube chat too, where they delete (20 quota units) unless a
      timeout rule says “Time out on YouTube too” (200 units each, and only while at least 1,000 units are left today);
      YouTube has no warnings, so a warning deletes there. What they caught is listed with the answer. Try a message to see what would
      catch it. Templates add !permit, which lets a viewer post a link for a minute, and a reply saying why a message went.
      Twitch’s Blocked Terms are listed there too: remove one, or copy your words to Twitch and Twitch’s terms to your words
      (copying only adds). Twitch’s AutoMod is set on Twitch.
    </dd>
    <dt>Wheels</dt>
    <dd>A wheel of choices, like the next game. Each spin is drawn with a secure random source and recorded; a wheel overlay in OBS spins to that result, and each wedge is drawn at its real size, so the odds you see are the real odds. Workflows spin one with “Spin a wheel”.</dd>
    <dt>Variables</dt>
    <dd>Values workflows keep between streams: for everyone (a death counter), per viewer (their lurk count), or private to one workflow.</dd>
    <dt>Lua scripts</dt>
    <dd>
      For what the built-in steps don’t do: “Run a Lua script” (under More steps) runs a few lines of Lua you write in the step. It reads the values
      you pass as <code>sg.args</code> and the event as <code>sg.event</code>, writes lines to the trace with <code>sg.log</code>, and what it
      returns is the step’s result for the steps after it. It runs in a separate process, stopped at its time limit, and can’t reach chat, OBS,
      files or the internet. Try it in the step before saving.
    </dd>
    <dt>Add-ons</dt>
    <dd>
      Wheel looks and plugins made by other people, installed from a ZIP file. A wheel look runs in a sandbox in OBS, and
      the real result is always in chat and on Wheels. A plugin adds steps to your workflows, written in Lua. Install one from a ZIP on Add-ons: it shows the
      steps it adds and asks you to allow each thing it wants to do, like sending chat. It’s installed off; turn it on with its
      switch. Its steps are marked “(add-on)” in Add a step, and any templates it brings are in the gallery in its own section.
      It runs in a separate process, can’t reach your files, the internet or your accounts, and keeps its own memory while it’s on. “What it has done” lists everything it asked StreamGremlin to do.
      A plugin can also start your workflows with its own events: they’re under “When”, marked “(add-on)”. One you
      allow to hold points in rounds can take a viewer’s stake and pay it back, within the limits it states; its odds are
      its own, and “Show its rounds” lists every stake, roll and payout.
      To update one, install its new version: it says first what changes, and asks only for permissions you haven’t allowed.
      Roll back returns to the version before.
    </dd>
    <dt>Ads</dt>
    <dd>For Affiliates and Partners, the Ads card on Home shows your next ad and your snoozes, and runs or snoozes one. A running break shows an estimate of the time left: Twitch doesn’t say when ads end.</dd>
    <dt>Backups</dt>
    <dd>Settings → Backups copies everything StreamGremlin keeps, every day and whenever you ask. Restoring one asks first and backs up what it replaces. Sign-ins aren’t in backups.</dd>
    <dt>The window</dt>
    <dd>Opens where you left it, at the same size. Live stretches with it: chat in the middle, your buttons on the right.</dd>
    <dt>Unknown</dt>
    <dd>When StreamGremlin can’t check something (Twitch didn’t answer, OBS is closed) it says Unknown rather than guessing it’s fine.</dd>
  </dl>
</section>

<section class="sg-panel card" aria-labelledby="help-keys">
  <h2 id="help-keys">Keyboard shortcuts</h2>
  <dl class="facts">
    {#each shortcuts as s (s.key)}
      <dt><kbd>{mod}</kbd> + <kbd>{s.key}</kbd></dt>
      <dd>{s.label}</dd>
    {/each}
    <dt><kbd>{mod}</kbd> + <kbd>,</kbd></dt>
    <dd>Settings</dd>
    <dt><kbd>{mod}</kbd> + <kbd>F</kbd></dt>
    <dd>Search on this screen (Workflows, Events, Quotes, Variables)</dd>
    <dt><kbd>{mod}</kbd> + <kbd>+</kbd> / <kbd>−</kbd> / <kbd>0</kbd></dt>
    <dd>Bigger text, smaller text, standard size</dd>
  </dl>
  <p class="sg-muted">Numbers follow the screens in the sidebar; screens under More… have none.</p>
</section>

<section class="sg-panel card" aria-labelledby="help-data">
  <h2 id="help-data">Your data</h2>
  <p>
    Everything StreamGremlin keeps (workflows, runs, events, variables and settings) is in one folder on this computer{#if dataDir}:
      <code>{dataDir}</code>{/if}. Nothing is sent anywhere but Twitch and OBS. Sign-ins and passwords are in your system’s
    keyring, not in that folder.
  </p>
  {#if version}
    <p>This is StreamGremlin <strong>{version}</strong>.</p>
  {/if}
  <p>
    The <code>streamgremlin</code> command does what the app does, from a terminal: try <code>streamgremlin status</code>,
    <code>streamgremlin workflows</code> or <code>streamgremlin help</code>.
  </p>
</section>

<section class="sg-panel card" aria-labelledby="help-report">
  <h2 id="help-report">Something went wrong?</h2>
  <p>Tell whoever invited you to the beta (or wherever you got StreamGremlin). The most useful report has:</p>
  <ul>
    <li>what you did, what you expected, and what happened instead (a screenshot helps);</li>
    <li>the version{#if version}: <strong>{version}</strong>{/if}, and Windows or macOS;</li>
    <li>
      the log files from the logs folder{#if logsDir}, <code>{logsDir}</code>{/if}. Logs never contain your passwords or sign-ins,
      but they do have chat names and messages from your stream.
    </li>
  </ul>
</section>

<section class="sg-panel card" aria-labelledby="help-credits">
  <h2 id="help-credits">Icon credits</h2>
  <p>Button icons are bundled with the app, so they work offline.</p>
  <ul>
    <li><strong>Tabler Icons</strong> (tabler.io/icons), MIT licence.</li>
    <li>
      <strong>Nerd Fonts</strong> (nerdfonts.com), MIT licence, with icons from: Material Design (Apache 2.0); Font Awesome
      (CC BY 4.0, font SIL OFL 1.1) and its Extension (MIT); Devicons (MIT); Codicons by Microsoft (CC BY 4.0); Octicons by
      GitHub (MIT); Weather Icons (SIL OFL 1.1); Font Logos (Unlicense).
    </li>
  </ul>
  <p class="sg-muted">Brand logos are trademarks of their owners.</p>
</section>
