<script lang="ts">
  // Settings → Updates (D-120): whether a newer StreamGremlin is out, the
  // switch for checking once a day, and Download then Install, which backs
  // up, closes StreamGremlin, installs it and opens it again. Nothing is
  // downloaded until the streamer asks, and nothing installs while live.
  import { onDestroy, onMount } from 'svelte';
  import {
    ApiError,
    checkForUpdates,
    downloadUpdate,
    getUpdates,
    installUpdate,
    openInBrowser,
    putUpdateSettings,
    type UpdateStatus,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  let { poll = 1000 }: { poll?: number } = $props();

  let st = $state<UpdateStatus | undefined>();
  let loadError = $state('');
  let error = $state('');
  let notice = $state('');
  let busy = $state(false);
  let confirming = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const mb = (n: number) => `${(n / 1e6).toFixed(1)} MB`;

  async function load() {
    clearTimeout(timer);
    try {
      st = await getUpdates();
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
    // While something runs, follow it.
    if (st && (st.state === 'checking' || st.download.state === 'downloading')) {
      timer = setTimeout(load, poll);
    }
  }

  onMount(load);
  onDestroy(() => clearTimeout(timer));

  async function act(f: () => Promise<unknown>, done = '') {
    busy = true;
    error = '';
    notice = '';
    try {
      await f();
      notice = done;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  async function setCheck(on: boolean) {
    await act(() => putUpdateSettings(on), on ? 'StreamGremlin checks for updates at start and once a day.' : 'Turned off. You can still check now.');
  }

  async function install() {
    confirming = false;
    busy = true;
    error = '';
    notice = '';
    try {
      await installUpdate();
      notice = 'Installing: StreamGremlin closes now and opens again when it’s done.';
    } catch (e) {
      error = message(e);
      busy = false;
      await load();
    }
  }

  const stateWords = (s: UpdateStatus): string => {
    switch (s.state) {
      case 'dev':
        return 'This is a development build, which doesn’t check for updates.';
      case 'off':
        return 'Checking is off.';
      case 'not_checked':
        return 'Not checked yet: StreamGremlin checks a minute after it starts.';
      case 'checking':
        return 'Checking…';
      case 'up_to_date':
        return 'This is the newest version.';
      case 'failed':
        return `The last check didn’t work: ${s.error}`;
      default:
        return '';
    }
  };
</script>

<section class="sg-panel card" aria-labelledby="updates-heading" aria-busy={busy}>
  <h2 id="updates-heading">Updates</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {/if}
  {#if st}
    <p>
      This is StreamGremlin {st.version}.
      {#if st.state !== 'available'}{stateWords(st)}{/if}
      {#if st.checked_at && st.state !== 'checking'}<span class="sg-muted">Last checked {eventTime(st.checked_at)}.</span>{/if}
    </p>
    {#if st.test_server}<p class="sg-muted">Checking a test server on this computer, not GitHub.</p>{/if}

    {#if st.latest}
      {@const l = st.latest}
      {@const d = st.download}
      <div class="fill-in" role="status">
        <p><strong>StreamGremlin {l.version} is out.</strong>{#if l.prerelease} It’s a beta.{/if}</p>
        {#if l.notes}
          <details>
            <summary>What’s new</summary>
            <pre class="release-notes">{l.notes}</pre>
          </details>
        {/if}
        {#if !l.installable}
          <p class="sg-muted">{l.why}</p>
        {:else if d.state === 'downloading'}
          <p>Downloading {l.version}…</p>
          <progress max={d.total || undefined} value={d.done} aria-label="Downloading the update"></progress>
        {:else if d.state === 'ready' && d.version === l.version}
          <p class="secret-check"><span class="icon-ok" aria-hidden="true">✓</span> Downloaded, and its signature checks out.</p>
        {:else if d.state === 'installing'}
          <p>Installing {d.version}: StreamGremlin closes and opens again.</p>
        {:else if d.state === 'failed'}
          <p class="error">Downloading it didn’t work, and nothing was installed: {d.error}</p>
        {/if}
        <div class="row-actions">
          {#if l.installable && d.state === 'ready' && d.version === l.version}
            <button type="button" class="sg-button" disabled={busy} onclick={() => (confirming = true)}>Install and restart</button>
          {:else if l.installable && d.state !== 'downloading' && d.state !== 'installing'}
            <button type="button" class="sg-button" disabled={busy} onclick={() => act(downloadUpdate)}
              >Download {l.version}{l.size ? ` (${mb(l.size)})` : ''}</button
            >
          {/if}
          <button type="button" class="button" disabled={busy} onclick={() => act(() => openInBrowser(l.url))}>Open the release page</button>
        </div>
      </div>
    {/if}

    {#if st.state !== 'dev'}
      <label class="choice-inline">
        <input type="checkbox" checked={st.settings.check} disabled={busy} onchange={(e) => setCheck(e.currentTarget.checked)} />
        Check for updates when StreamGremlin starts and once a day
      </label>
      <p class="sg-muted field-hint">
        Checking asks GitHub for the list of StreamGremlin’s releases. GitHub sees your computer’s internet address, as any website
        does; nothing else is sent. Nothing is downloaded until you choose to, and an update installs only if it’s signed by
        StreamGremlin’s own key.
      </p>
      <div class="row-actions">
        <button type="button" class="button" disabled={busy || st.state === 'checking'} onclick={() => act(checkForUpdates)}>Check now</button>
      </div>
    {/if}
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

{#if confirming && st?.latest}
  <ConfirmDialog
    title={`Install StreamGremlin ${st.latest.version}?`}
    message="StreamGremlin backs up your data, closes, installs the update and opens again, in about a minute. Overlays and Stream Deck keys reconnect once it’s back. Do it when you’re not live."
    confirmLabel="Install and restart"
    onconfirm={install}
    oncancel={() => (confirming = false)}
  />
{/if}

<style>
  .release-notes {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-height: 16rem;
    overflow: auto;
    font: inherit;
    margin: 0.5rem 0 0;
  }
</style>
