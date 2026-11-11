<script module lang="ts">
  import type { ChatterCard } from '../lib/api';
  // Cards read in the last minute, so hovering the same name again doesn't
  // ask Twitch again. Refresh asks anyway.
  const cache = new Map<string, { at: number; card: ChatterCard }>();
  export const CARD_FRESH_MS = 60_000;
  export function forgetCards() {
    cache.clear();
  }
</script>

<script lang="ts">
  // The viewer card (D-078): what Twitch says about a chatter now, with
  // StreamGremlin's record, points and their recent messages. Anything
  // Twitch didn't give is shown as unknown, with why.
  import { onMount, tick } from 'svelte';
  import { ApiError, getChatterCard } from '../lib/api';
  import { eventTime } from '../lib/events';
  import { chatLine, recordWords, twitchRows, type ChatLine } from '../lib/live';

  let {
    line,
    anchor,
    pinned = false,
    onclose,
    onpointerenter,
    onpointerleave,
  }: {
    line: ChatLine;
    /** Where the name is, to place the card beside it. */
    anchor?: DOMRect;
    /** Opened by a click or the keyboard: it stays until closed, and takes focus. */
    pinned?: boolean;
    onclose: (restoreFocus: boolean) => void;
    onpointerenter?: () => void;
    onpointerleave?: () => void;
  } = $props();

  let card = $state<ChatterCard | undefined>();
  let error = $state('');
  let loading = $state(false);
  let headingEl = $state<HTMLHeadingElement | undefined>();
  const key = $derived(`${line.test ? 'test:' : ''}${line.userId}`);

  async function load(fresh: boolean) {
    const hit = cache.get(key);
    if (!fresh && hit && Date.now() - hit.at < CARD_FRESH_MS) {
      card = hit.card;
      return;
    }
    loading = true;
    error = '';
    try {
      const c = await getChatterCard('twitch', line.userId, line.test);
      cache.set(key, { at: Date.now(), card: c });
      card = c;
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (line.userId) load(false);
    else error = 'This message has no account to look up.';
    if (pinned) tick().then(() => headingEl?.focus());
  });

  // Pinned later (clicked while hovering): take focus then.
  $effect(() => {
    if (pinned) tick().then(() => headingEl?.focus());
  });

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      onclose(true);
    }
  }

  const place = $derived.by(() => {
    if (!anchor || typeof window === 'undefined') return '';
    const w = Math.min(352, window.innerWidth - 16);
    const left = Math.max(8, Math.min(anchor.left, window.innerWidth - w - 8));
    const below = window.innerHeight - anchor.bottom;
    return below > 320 || below > anchor.top
      ? `left:${left}px;top:${anchor.bottom + 4}px;max-height:${Math.max(160, below - 12)}px`
      : `left:${left}px;bottom:${window.innerHeight - anchor.top + 4}px;max-height:${Math.max(160, anchor.top - 12)}px`;
  });
  const recent = $derived((card?.messages ?? []).map(chatLine).reverse());
  const headingId = $derived(`viewer-card-${line.seq}`);
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  class="sg-panel viewer-card"
  role="dialog"
  aria-labelledby={headingId}
  style={place}
  tabindex="-1"
  {onkeydown}
  onpointerenter={() => onpointerenter?.()}
  onpointerleave={() => onpointerleave?.()}
>
  <div class="viewer-card-head">
    <h2 id={headingId} tabindex="-1" bind:this={headingEl}>
      {line.name}{#if line.login && line.login.toLowerCase() !== line.name.toLowerCase()}<span class="sg-muted"> ({line.login})</span>{/if}
    </h2>
    <div class="row-actions">
      <button type="button" class="button" disabled={loading || !line.userId} onclick={() => load(true)}>Refresh</button>
      <button type="button" class="button" aria-label={`Close ${line.name}’s card`} onclick={() => onclose(true)}>Close</button>
    </div>
  </div>
  {#if line.badges.length}<p class="sg-muted viewer-card-badges">In this message: {line.badges.join(', ')}</p>{/if}

  {#if error}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {error}</p>
  {:else if !card}
    <p class="sg-muted" aria-live="polite">Asking Twitch…</p>
  {:else}
    {#if card.twitch}
      <h3>On Twitch</h3>
      <dl class="viewer-card-facts">
        {#each twitchRows(card.twitch) as r (r.label)}
          <dt>{r.label}</dt>
          <dd class:state-unknown={r.unknown}>
            {#if r.unknown}<span aria-hidden="true">{'? '}</span>{`Unknown: ${r.value}`}{:else}{r.value}{/if}
          </dd>
        {/each}
      </dl>
      {#if card.twitch.description}<p class="viewer-card-bio">{card.twitch.description}</p>{/if}
      <p class="sg-muted viewer-card-when">Asked Twitch at {eventTime(card.twitch.read_at)}.</p>
    {/if}
    <h3>In StreamGremlin</h3>
    {#each recordWords(card) as w (w)}<p class="viewer-card-record">{w}</p>{/each}
    <h3>Recent messages</h3>
    {#if recent.length === 0}
      <p class="sg-muted">None kept.</p>
    {:else}
      <ul class="viewer-card-messages">
        {#each recent as m (m.seq)}
          <li><span class="sg-muted">{eventTime(m.at)}</span> {m.text}</li>
        {/each}
      </ul>
    {/if}
  {/if}
</div>

<style>
  .viewer-card {
    position: fixed;
    z-index: 20;
    width: min(22rem, calc(100vw - 1rem));
    overflow-y: auto;
    box-shadow: 0 8px 24px rgb(0 0 0 / 0.25);
    padding: 0.75rem 1rem;
  }
  .viewer-card-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 0.5rem;
  }
  .viewer-card h2 {
    font-size: 1.1rem;
    margin: 0 0 0.25rem;
    overflow-wrap: anywhere;
  }
  .viewer-card h2:focus {
    outline: none;
  }
  .viewer-card h2:focus-visible {
    outline: 2px solid var(--sg-primary);
  }
  .viewer-card h3 {
    font-size: 0.95rem;
    margin: 0.75rem 0 0.25rem;
  }
  .viewer-card-badges,
  .viewer-card-when {
    font-size: 0.85rem;
    margin: 0.25rem 0;
  }
  .viewer-card-facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.2rem 0.75rem;
    margin: 0;
  }
  .viewer-card-facts dt {
    color: var(--sg-muted);
  }
  .viewer-card-facts dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .viewer-card-bio {
    margin: 0.5rem 0 0;
    overflow-wrap: anywhere;
  }
  .viewer-card-record {
    margin: 0.2rem 0;
  }
  .viewer-card-messages {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .viewer-card-messages li {
    padding: 0.15rem 0;
    overflow-wrap: anywhere;
  }
</style>
