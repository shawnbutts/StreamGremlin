<script lang="ts">
  // Help: how StreamGremlin fits together, in a few words, with shortcuts
  // and where your data lives. Help for a field sits beside the field; this
  // is the overview.
  import { onMount } from 'svelte';
  import { ApiError, getStatus, openInBrowser } from '../lib/api';
  // The same text is the website's Help (src/help/guide.html).
  import guide from '../help/guide.html?raw';

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

  // Where reports go (D-115): GitHub's issues, and its private security
  // reports. The engine opens only these exact pages.
  const issuesURL = 'https://github.com/shawnbutts/StreamGremlin/issues/new/choose';
  const securityURL = 'https://github.com/shawnbutts/StreamGremlin/security/advisories/new';
  let openError = $state('');
  async function open(url: string) {
    openError = '';
    try {
      await openInBrowser(url);
    } catch (e) {
      openError = `Couldn’t open your browser (${e instanceof ApiError ? e.message : String(e)}). The address is ${url}`;
    }
  }

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
    {@html guide}
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
      <code>{dataDir}</code>{/if}. Nothing is sent anywhere but the services you connect (Twitch, YouTube, OBS, Discord). Sign-ins and passwords are in your system’s
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
  <p>Report it on GitHub, where you can also see whether someone has already. You’ll need a free GitHub account. The most useful report has:</p>
  <ul>
    <li>what you did, what you expected, and what happened instead (a screenshot helps);</li>
    <li>the version{#if version}: <strong>{version}</strong>{/if}, and Windows, macOS or Linux;</li>
    <li>
      the log files from the logs folder{#if logsDir}, <code>{logsDir}</code>{/if}. Logs never contain your passwords or sign-ins,
      but they do have chat names and messages from your stream, so attach only what you’re happy to share.
    </li>
  </ul>
  <div class="row-actions">
    <button type="button" class="button" onclick={() => open(issuesURL)}>Report a problem or idea…</button>
    <button type="button" class="button" onclick={() => open(securityURL)}>Report a security problem privately…</button>
  </div>
  {#if openError}<p class="error" role="alert">{openError}</p>{/if}
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
