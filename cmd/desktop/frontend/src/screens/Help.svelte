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
  const mac = typeof navigator !== 'undefined' && /Mac/.test(navigator.platform);
  const mod = mac ? '⌘' : 'Ctrl';

  onMount(() => {
    getStatus()
      .then((s) => (dataDir = s.data_dir))
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
    <dd>Your Twitch account (and, if you like, a bot account that answers under its own name) and OBS. Sign-ins and passwords are kept in your system’s keyring, never in files or logs.</dd>
    <dt>Events</dt>
    <dd>Everything StreamGremlin hears: chat messages, <code>!commands</code>, OBS scene changes. Test events look real to workflows but are marked Test.</dd>
    <dt>Workflows</dt>
    <dd>
      “When this happens, do that”: when a viewer types <code>!hello</code>, say hi back. Start from a template or build one in
      plain words. New workflows start off; turn one on when it’s ready.
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
    <dd>Alerts in OBS. Each overlay is one browser source: copy its private link into OBS as a browser source, 1920 by 1080, and workflows show alerts on it with “Show an alert”, one at a time, with the viewer’s profile picture and a sound if you like. The overlay plays that sound, so tick “Control audio via OBS” on the browser source and it gets its own mixer bar. A Test shows the alert in the app’s preview, never in OBS.</dd>
    <dt>Rewards</dt>
    <dd>Your Twitch channel point rewards (Affiliates and Partners have them). Make rewards here, pause them, and mark redemptions done or refund them, by hand or with “Complete or refund the redemption” in a workflow. Rewards made on Twitch can start workflows, but only ones made here can be changed.</dd>
    <dt>Polls</dt>
    <dd>Twitch polls and channel point predictions (Affiliates and Partners): start them from the Polls screen or a workflow, watch the votes, end polls, and lock, resolve or cancel predictions. Twitch runs one of each at a time.</dd>
    <dt>Variables</dt>
    <dd>Values workflows keep between streams: for everyone (a death counter), per viewer (their lurk count), or private to one workflow.</dd>
    <dt>Lua scripts</dt>
    <dd>
      For what the built-in steps don’t do: “Run a Lua script” (under More steps) runs a few lines of Lua you write in the step. It reads the values
      you pass as <code>sg.args</code> and the event as <code>sg.event</code>, writes lines to the trace with <code>sg.log</code>, and what it
      returns is the step’s result for the steps after it. It runs in a separate process, stopped at its time limit, and can’t reach chat, OBS,
      files or the internet. Try it in the step before saving.
    </dd>
    <dt>Plugins</dt>
    <dd>
      Add-ons that add steps to your workflows, written in Lua by other people. Install one from a ZIP on Add-ons: it shows the
      steps it adds and asks you to allow each thing it wants to do, like sending chat. It’s installed off; turn it on with its
      switch. It runs in a separate process, can’t reach your files, the internet or your accounts, and keeps its own memory
      while it’s on. “What it has done” lists everything it asked StreamGremlin to do.
    </dd>
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
      <code>{dataDir}</code>{/if}. Nothing is sent anywhere but Twitch and OBS.
  </p>
  <p>
    The <code>streamgremlin</code> command does what the app does, from a terminal: try <code>streamgremlin status</code>,
    <code>streamgremlin workflows</code> or <code>streamgremlin help</code>.
  </p>
</section>
