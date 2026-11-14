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
  // Twitch didn't give is shown as unknown, with why. From slice 3 (D-079)
  // it also moderates: each request is sent once, as you, and recorded.
  import { onMount, tick } from 'svelte';
  import { ApiError, getChatterCard, moderate, openInBrowser, type ModAction, type ModRecord } from '../lib/api';
  import { eventTime } from '../lib/events';
  import {
    TIMEOUTS,
    chatLine,
    confirmWords,
    historyWords,
    lengthWords,
    modWords,
    newRequestId,
    recordWords,
    suspicionRow,
    twitchRows,
    type ChatLine,
    type ConfirmWords,
  } from '../lib/live';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let {
    line,
    anchor,
    pinned = false,
    onclose,
    onpointerenter,
    onpointerleave,
    canModerate = false,
    onpin,
    onmoderated,
  }: {
    line: ChatLine;
    /** Where the name is, to place the card beside it. */
    anchor?: DOMRect;
    /** Opened by a click or the keyboard: it stays until closed, and takes focus. */
    pinned?: boolean;
    onclose: (restoreFocus: boolean) => void;
    onpointerenter?: () => void;
    onpointerleave?: () => void;
    /** Signed in, and not you: the moderating buttons show. */
    canModerate?: boolean;
    /** The card is being used: keep it open. */
    onpin?: () => void;
    onmoderated?: (r: ModRecord) => void;
  } = $props();

  let busy = $state(false);
  let modNotice = $state('');
  let modProblem = $state('');
  let seconds = $state(600);
  let reason = $state('');
  // Actions that can't be taken back ask first (confirmWords).
  let asking = $state<{ action: ModAction; extra: { seconds?: number; reason?: string }; words: ConfirmWords } | undefined>();

  function askThen(action: ModAction, extra: { seconds?: number; reason?: string } = {}) {
    const words = confirmWords(action, line.name, { ...extra, platform: line.platform });
    if (words) asking = { action, extra, words };
    else act(action, extra);
  }

  async function act(action: ModAction, extra: { seconds?: number; reason?: string } = {}) {
    busy = true;
    modNotice = '';
    modProblem = '';
    try {
      const res = await moderate({ request_id: newRequestId(), action, platform: line.platform, user_id: line.userId, login: line.login || line.name, from: 'live', ...extra });
      const words = modWords(res.record, line.name, res.duplicate);
      if (res.record.state === 'done') {
        modNotice = words;
        if (action === 'timeout' || action === 'ban' || action === 'warn') reason = '';
      } else modProblem = words;
      onmoderated?.(res.record);
      await load(true);
    } catch (e) {
      modProblem = `Not sent: ${e instanceof ApiError ? e.message : String(e)}`;
    } finally {
      busy = false;
    }
  }

  function warn() {
    if (!reason.trim()) {
      modProblem = 'A warning needs a reason: type it in Reason.';
      return;
    }
    askThen('warn', { reason: reason.trim() });
  }

  let card = $state<ChatterCard | undefined>();
  // A YouTube moderator: as StreamGremlin last made them, else as their
  // message said.
  const youtubeModerator = $derived.by(() => {
    const last = card?.moderation.find((m) => m.state === 'done' && (m.action === 'mod' || m.action === 'unmod'));
    return last ? last.action === 'mod' : line.badges.includes('Moderator');
  });
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
      const c = await getChatterCard(line.platform, line.userId, line.test);
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
  onpointerdown={() => onpin?.()}
  onfocusin={() => onpin?.()}
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
    <p class="sg-muted" aria-live="polite">{line.platform === 'youtube' ? 'Loading…' : 'Asking Twitch…'}</p>
  {:else}
    {#if line.platform === 'youtube'}
      <h3>On YouTube</h3>
      {#if line.channelUrl}
        <p><button type="button" class="button" onclick={() => openInBrowser(line.channelUrl!)}>Open their YouTube channel</button></p>
      {/if}
      <p class="sg-muted">YouTube’s API tells apps nothing more about one viewer.</p>
    {/if}
    {#if card.twitch}
      <h3>On Twitch</h3>
      <dl class="viewer-card-facts">
        {#each twitchRows(card.twitch) as r (r.label)}
          <dt>{r.label}</dt>
          <dd class:state-unknown={r.unknown}>
            {#if r.unknown}<span aria-hidden="true">{'? '}</span>{`Unknown: ${r.value}`}{:else}{r.value}{/if}
          </dd>
        {/each}
        <dt>Suspicious</dt>
        <dd class:viewer-card-flag={!!card.suspicious}>{suspicionRow(card.suspicious).value}</dd>
      </dl>
      {#if card.twitch.description}<p class="viewer-card-bio">{card.twitch.description}</p>{/if}
      <p class="sg-muted viewer-card-when">Asked Twitch at {eventTime(card.twitch.read_at)}.</p>
    {/if}
    <h3>In StreamGremlin</h3>
    {#each recordWords(card) as w (w)}<p class="viewer-card-record">{w}</p>{/each}
    {#if canModerate && card.twitch}
      {@const tw = card.twitch}
      <h3>Moderate</h3>
      <div class="viewer-card-mod">
        <label class="field">
          <span>Reason (for a timeout or ban if you like; a warning needs one)</span>
          <input class="sg-input" bind:value={reason} maxlength="500" autocomplete="off" />
        </label>
        <div class="row-actions">
          <label>
            <span class="visually-hidden">Timeout length</span>
            <select class="sg-input" bind:value={seconds}>
              {#each TIMEOUTS as t (t)}<option value={t}>{lengthWords(t)}</option>{/each}
            </select>
          </label>
          <button type="button" class="button" disabled={busy} onclick={() => askThen('timeout', { seconds, reason: reason.trim() || undefined })}>Time out…</button>
          {#if tw.ban?.banned}
            <button type="button" class="button" disabled={busy} onclick={() => act('unban')}>Unban</button>
          {:else}
            <button type="button" class="button button-danger" disabled={busy} onclick={() => askThen('ban', { reason: reason.trim() || undefined })}>Ban…</button>
          {/if}
          <button type="button" class="button" disabled={busy} onclick={warn}>Warn…</button>
        </div>
        <div class="row-actions">
          {#if tw.vip}
            <button type="button" class="button" disabled={busy} onclick={() => act('unvip')}>Remove VIP</button>
          {:else}
            <button type="button" class="button" disabled={busy} onclick={() => act('vip')}>Make VIP</button>
          {/if}
          {#if tw.moderator}
            <button type="button" class="button" disabled={busy} onclick={() => act('unmod')}>Remove moderator</button>
          {:else}
            <button type="button" class="button" disabled={busy} onclick={() => askThen('mod')}>Make moderator…</button>
          {/if}
          <button type="button" class="button" disabled={busy} onclick={() => askThen('shoutout')}>Shoutout…</button>
        </div>
        <div class="row-actions" role="group" aria-label="How Twitch treats them as a suspicious user">
          <button type="button" class="button" disabled={busy} onclick={() => act('monitor')}>Monitor</button>
          <button type="button" class="button" disabled={busy} onclick={() => act('restrict')}>Restrict</button>
          {#if card.suspicious && card.suspicious.treatment !== 'none'}
            <button type="button" class="button" disabled={busy} onclick={() => act('unsuspect')}>Treat as usual</button>
          {/if}
        </div>
        <p class="saved" role="status">{modNotice}</p>
        {#if modProblem}<p class="error" role="alert">{modProblem}</p>{/if}
      </div>
    {/if}
    {#if canModerate && line.platform === 'youtube'}
      <h3>Moderate on YouTube</h3>
      <div class="viewer-card-mod">
        <div class="row-actions">
          <label>
            <span class="visually-hidden">Timeout length</span>
            <select class="sg-input" bind:value={seconds}>
              {#each TIMEOUTS as t (t)}<option value={t}>{lengthWords(t)}</option>{/each}
            </select>
          </label>
          <button type="button" class="button" disabled={busy} onclick={() => askThen('timeout', { seconds })}>Time out…</button>
          {#if card.youtube_ban}
            <button type="button" class="button" disabled={busy} onclick={() => act('unban')}>
              {card.youtube_ban.action === 'timeout' ? 'End the timeout' : 'Unban'}
            </button>
          {:else}
            <button type="button" class="button button-danger" disabled={busy} onclick={() => askThen('ban')}>Ban…</button>
          {/if}
          {#if youtubeModerator}
            <button type="button" class="button" disabled={busy} onclick={() => act('unmod')}>Remove moderator</button>
          {:else}
            <button type="button" class="button" disabled={busy} onclick={() => askThen('mod')}>Make moderator…</button>
          {/if}
        </div>
        <p class="sg-muted viewer-card-when">
          Each uses 200 of today’s YouTube quota units. YouTube takes no reason. Only a ban or timeout made from StreamGremlin can be
          undone here; undo others in YouTube Studio.
        </p>
        <p class="saved" role="status">{modNotice}</p>
        {#if modProblem}<p class="error" role="alert">{modProblem}</p>{/if}
      </div>
    {/if}
    {#if card.moderation?.length}
      <h3>Done from StreamGremlin</h3>
      <ul class="viewer-card-messages">
        {#each card.moderation as m (m.id)}
          <li><span class="sg-muted">{eventTime(m.created_at)}</span> {historyWords(m)}</li>
        {/each}
      </ul>
    {/if}
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

{#if asking}
  {@const q = asking}
  <ConfirmDialog
    title={q.words.title}
    message={q.words.message}
    confirmLabel={q.words.confirmLabel}
    onconfirm={() => {
      const { action, extra } = q; // read before closing clears it
      asking = undefined;
      act(action, extra);
    }}
    oncancel={() => (asking = undefined)}
  />
{/if}

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
  .viewer-card-flag {
    font-weight: 650;
  }
  .viewer-card-mod .row-actions {
    flex-wrap: wrap;
    margin: 0.35rem 0;
  }
  .viewer-card-mod .field input {
    width: 100%;
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
