<script lang="ts">
  // Connections: Twitch's Client ID and sign-ins. Polls the engine, faster
  // while a sign-in is pending; when the engine stops answering it shows
  // Unknown, never the last answer as if it were current.
  import { onMount } from 'svelte';
  import {
    ApiError,
    getConnections,
    openInBrowser,
    setTwitchClientId,
    type OBSStatus,
    type TwitchStatus,
  } from '../lib/api';
  import DiscordCard from './DiscordCard.svelte';
  import OBSCard from './OBSCard.svelte';
  import StreamDeckCard from './StreamDeckCard.svelte';
  import TwitchAccount from './TwitchAccount.svelte';
  import TwitchChat from './TwitchChat.svelte';
  import YouTubeCard from './YouTubeCard.svelte';

  let { fastMs = 1500, slowMs = 15_000 }: { fastMs?: number; slowMs?: number } = $props();

  let twitch = $state<TwitchStatus | undefined>();
  let obs = $state<OBSStatus | undefined>();
  let loadError = $state('');
  let clientId = $state('');
  let showOwnApp = $state(false);
  let savingId = $state(false);
  let idError = $state('');
  let idNotice = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  let wake: (() => void) | undefined;

  async function load(signal?: AbortSignal) {
    try {
      const c = await getConnections(signal);
      twitch = c.twitch;
      obs = c.obs;
      loadError = '';
    } catch (e) {
      if (signal?.aborted) return;
      twitch = undefined;
      obs = undefined;
      loadError = message(e);
    }
  }

  onMount(() => {
    const abort = new AbortController();
    (async () => {
      while (!abort.signal.aborted) {
        await load(abort.signal);
        // Faster while something is changing, or OBS is connected (its scene can change any time).
        const pending =
          (twitch && [twitch.streamer, twitch.bot].some((a) => a.state === 'signing_in' || a.state === 'unknown')) ||
          (obs && obs.state !== 'off' && obs.state !== 'needs_action');
        await new Promise<void>((r) => {
          const t = setTimeout(r, pending || !twitch ? fastMs : slowMs);
          wake = () => {
            clearTimeout(t);
            r();
          };
        });
      }
    })();
    return () => {
      abort.abort();
      wake?.();
    };
  });

  const refresh = () => wake?.();

  async function saveClientId(value: string) {
    savingId = true;
    idError = '';
    idNotice = '';
    try {
      twitch = (await setTwitchClientId(value.trim())).twitch;
      idNotice = value ? 'Saved. You can sign in now.' : 'Using the built-in Client ID.';
      clientId = '';
      showOwnApp = false;
    } catch (err) {
      idError = message(err);
    } finally {
      savingId = false;
    }
  }

  const openConsole = () => openInBrowser('https://dev.twitch.tv/console').catch((e) => (idError = message(e)));
</script>

<h1>Connections</h1>
<p class="sg-muted screen-intro">Where StreamGremlin signs in to Twitch and YouTube and connects to OBS. Sign-ins and passwords stay in your system’s keyring.</p>

{#if loadError}
  <section class="sg-panel card" role="alert">
    <p class="state state-unknown"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  </section>
{:else if !twitch}
  <p class="sg-muted">Checking…</p>
{:else}
  {#if twitch.test_server}
    <section class="sg-panel card warning-card" aria-label="Test server">
      <p>
        <strong>Test mode.</strong> Twitch sign-in goes to a stand-in at <code>{twitch.test_server}</code>, not the real Twitch.
      </p>
    </section>
  {/if}

  <section class="sg-panel card" aria-labelledby="twitch-heading">
    <h2 id="twitch-heading">Twitch</h2>

    {#snippet clientIdForm(intro: string)}
      <form
        class="test-form"
        onsubmit={(e) => {
          e.preventDefault();
          saveClientId(clientId);
        }}
      >
        <p>{intro}</p>
        <ol class="steps">
          <li>
            Open the Twitch developer console and register an application. Set <strong>Client Type</strong> to
            <strong>Public</strong>, and add <code>https://localhost</code> as the OAuth Redirect URL. Twitch requires one, but StreamGremlin never uses it.
          </li>
          <li>Copy its Client ID and paste it here. It isn't a secret.</li>
        </ol>
        <div class="row-actions">
          <button type="button" class="button" onclick={openConsole}>Open the Twitch developer console</button>
        </div>
        <label class="field">
          <span>Client ID</span>
          <input class="sg-input" bind:value={clientId} autocomplete="off" spellcheck="false" maxlength="64" />
        </label>
        <div class="row-actions">
          <button type="submit" class="sg-button" disabled={savingId || !clientId.trim()}>Save Client ID</button>
        </div>
      </form>
    {/snippet}

    {#if twitch.client_id_source === 'none'}
      {@render clientIdForm('StreamGremlin needs a Twitch app’s Client ID before you can sign in. This build doesn’t include one.')}
    {:else}
      <p class="sg-muted">
        {twitch.client_id_source === 'built_in' ? 'Using StreamGremlin’s Twitch app.' : 'Using your own Twitch app:'}
        {#if twitch.client_id_source === 'pasted'}<code>{twitch.client_id}</code>{/if}
      </p>
      <div class="row-actions">
        {#if !showOwnApp}
          <button type="button" class="button" onclick={() => (showOwnApp = true)}
            >{twitch.client_id_source === 'pasted' ? 'Change Client ID' : 'Use my own Twitch app'}</button
          >
        {/if}
        {#if twitch.client_id_source === 'pasted' && twitch.has_built_in}
          <button type="button" class="button" disabled={savingId} onclick={() => saveClientId('')}
            >Use StreamGremlin’s app</button
          >
        {/if}
      </div>
      {#if showOwnApp}
        {@render clientIdForm('Changing the Client ID signs both accounts out.')}
      {/if}
    {/if}
    <p class="saved" role="status">{idNotice}</p>
    {#if idError}
      <p class="error" role="alert">{idError}</p>
    {/if}

    <TwitchAccount
      account={twitch.streamer}
      title="Your account"
      intro="Your channel’s own Twitch account. StreamGremlin reads chat and channel events with it."
      onchange={refresh}
    />
    <TwitchAccount
      account={twitch.bot}
      title="Bot account (optional)"
      intro="A second Twitch account that sends StreamGremlin’s chat messages, so they don’t appear under your name. Twitch signs in whoever is logged in to twitch.tv in your browser: use a private window, or sign out of twitch.tv first."
      onchange={refresh}
    />
    <TwitchChat {twitch} />
  </section>

  <YouTubeCard />

  {#if obs}
    <OBSCard status={obs} onchange={refresh} />
  {/if}
  <DiscordCard />
  <StreamDeckCard />
{/if}
