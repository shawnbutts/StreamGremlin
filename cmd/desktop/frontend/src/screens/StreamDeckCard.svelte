<script lang="ts">
  // Stream Deck pairing (M7 slice 4, ADR-0042). Pairing shows a one-time
  // code to type into the StreamGremlin plugin; the plugin gets its own
  // token, which can only see and press buttons. Paired decks are listed
  // with when they were last used, and can be forgotten.
  import { onDestroy, onMount } from 'svelte';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';
  import {
    ApiError,
    forgetClient,
    getClients,
    getPairing,
    startPairing,
    stopPairing,
    type PairedClient,
    type PairingState,
  } from '../lib/api';

  let { pollMs = 2000 }: { pollMs?: number } = $props();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  let decks = $state<PairedClient[] | undefined>();
  let listError = $state('');
  let pairing = $state<PairingState | undefined>();
  let notice = $state('');
  let problem = $state('');
  let busy = $state(false);
  let forgetting = $state<PairedClient | undefined>();
  let timer: ReturnType<typeof setInterval> | undefined;

  const waiting = $derived(pairing?.state === 'waiting');

  async function load() {
    try {
      decks = (await getClients()).clients;
      listError = '';
    } catch (e) {
      listError = message(e);
    }
  }

  function stopPolling() {
    clearInterval(timer);
    timer = undefined;
  }

  async function poll() {
    try {
      const st = await getPairing();
      const was = pairing?.state;
      pairing = st;
      if (st.state === 'waiting') return;
      stopPolling();
      if (was !== 'waiting') return;
      if (st.state === 'paired' && st.client) {
        notice = `Paired “${st.client.name}”. It can see and press your buttons.`;
        await load();
      } else if (st.state === 'expired') {
        problem = 'The code ran out before it was used. Pair again for a new one.';
      } else if (st.state === 'locked') {
        problem = 'Too many wrong codes were tried, so that code stopped working. Pair again for a new one.';
      }
    } catch (e) {
      problem = `Unknown whether it’s paired: ${message(e)}`;
    }
  }

  async function pair() {
    busy = true;
    notice = '';
    problem = '';
    try {
      await startPairing();
      await poll();
      stopPolling();
      timer = setInterval(poll, pollMs);
    } catch (e) {
      problem = message(e);
    } finally {
      busy = false;
    }
  }

  async function stop() {
    stopPolling();
    try {
      await stopPairing();
      pairing = { state: 'none', address: pairing?.address };
      notice = 'Stopped pairing. That code no longer works.';
    } catch (e) {
      problem = message(e);
    }
  }

  async function forget(c: PairedClient) {
    forgetting = undefined;
    notice = '';
    problem = '';
    try {
      await forgetClient(c.id);
      notice = `Forgot “${c.name}”. It can’t reach StreamGremlin until it’s paired again.`;
    } catch (e) {
      problem = message(e);
    }
    await load();
  }

  onMount(async () => {
    await load();
    try {
      pairing = await getPairing();
      if (pairing.state === 'waiting') timer = setInterval(poll, pollMs);
    } catch {
      // The list says unknown already, if the engine isn't answering.
    }
  });

  onDestroy(stopPolling);
</script>

<section class="sg-panel card" aria-labelledby="streamdeck-heading">
  <h2 id="streamdeck-heading">Stream Deck</h2>
  <p class="sg-muted">
    Pair the StreamGremlin plugin for Stream Deck so its keys can press your buttons.
    A paired Stream Deck can only see and press your buttons: it can’t change anything, see your sign-ins or moderate.
  </p>

  {#if listError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {listError}</p>
  {:else if decks && decks.length === 0}
    <p>No Stream Deck is paired yet.</p>
  {:else if decks}
    <ul class="deck-list">
      {#each decks as d (d.id)}
        <li>
          <div>
            <strong>{d.name}</strong>
            <span class="sg-muted">
              Paired {eventTime(d.created_at)}. {d.last_used_at ? `Last used ${eventTime(d.last_used_at)}.` : 'Never used.'}
            </span>
          </div>
          <button type="button" class="button button-danger" onclick={() => (forgetting = d)}>Forget…</button>
        </li>
      {/each}
    </ul>
  {/if}

  {#if waiting && pairing?.code}
    <div class="pair-box">
      <p>Type this code in the StreamGremlin plugin’s settings on your Stream Deck:</p>
      <p class="pair-code">{pairing.code}</p>
      <p class="sg-muted">
        It works once{pairing.expires_at ? `, until ${eventTime(pairing.expires_at)}` : ''}. Five wrong codes stop it.
        {#if pairing.address}The plugin finds StreamGremlin at <code>{pairing.address}</code>.{/if}
      </p>
      <button type="button" class="button" onclick={stop}>Stop pairing</button>
    </div>
  {:else}
    <button type="button" class="sg-button" disabled={busy} onclick={pair}>Pair a Stream Deck</button>
  {/if}

  <p class="saved" role="status">{notice}</p>
  {#if problem}<p class="error" role="alert">{problem}</p>{/if}
</section>

{#if forgetting}
  {@const c = forgetting}
  <ConfirmDialog
    title={`Forget “${c.name}”?`}
    message="Its keys stop working until you pair it again with a new code."
    confirmLabel="Forget"
    onconfirm={() => forget(c)}
    oncancel={() => (forgetting = undefined)}
  />
{/if}

<style>
  .deck-list {
    list-style: none;
    margin: 0 0 1rem;
    padding: 0;
    display: grid;
    gap: 0.5rem;
  }
  .deck-list li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--sg-border);
    border-radius: 8px;
  }
  .deck-list li div {
    display: grid;
  }
  .pair-box {
    border: 1px solid var(--sg-border);
    border-radius: 8px;
    padding: 0.75rem 1rem;
    margin-bottom: 0.5rem;
  }
  .pair-code {
    font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
    font-size: 2rem;
    font-weight: 700;
    letter-spacing: 0.15em;
    margin: 0.25rem 0 0.5rem;
  }
</style>
