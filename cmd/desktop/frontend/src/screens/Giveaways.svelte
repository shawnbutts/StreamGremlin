<script lang="ts">
  // Giveaways (docs/adr/0025-giveaways.md): open one, watch entries come
  // in, draw, and see what was said in chat. Every draw is shown with how it
  // was picked, and can be checked here against the fingerprint.
  import { onMount } from 'svelte';
  import {
    ApiError,
    cancelGiveaway,
    closeGiveaway,
    drawGiveaway,
    endGiveaway,
    getGiveaway,
    getGiveaways,
    openGiveaway,
    setAsideWinner,
    type Giveaway,
    type GiveawayDetail,
    type GiveawayDraw,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';
  import { duration } from '../lib/format';
  import { howDrawn, outcomeWords, verify } from '../lib/giveaways';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  let current = $state<GiveawayDetail | null>(null);
  let recent = $state<Giveaway[]>([]);
  let loaded = $state(false);
  let loadError = $state('');
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');
  let now = $state(Date.now());

  let form = $state({ title: '', command: 'join', winners: 1, minutes: 0, claim: false, claimSeconds: 60, subs: false, practice: false });
  let confirm = $state<{ title: string; message: string; label: string; run: () => void } | undefined>();
  let shown = $state<GiveawayDetail | undefined>();
  let check = $state<{ id: string; problems: string[] } | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  async function load() {
    try {
      const v = await getGiveaways();
      current = v.current;
      recent = v.recent.filter((g) => g.id !== v.current?.id);
      loadError = '';
    } catch (e) {
      current = null;
      loadError = message(e);
    } finally {
      loaded = true;
    }
  }

  onMount(() => {
    load();
    const id = setInterval(load, refreshMs);
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => {
      clearInterval(id);
      clearInterval(tick);
    };
  });

  async function act(f: () => Promise<unknown>, done: string) {
    busy = true;
    error = '';
    notice = '';
    try {
      await f();
      notice = done;
      return true;
    } catch (e) {
      error = message(e);
      return false;
    } finally {
      busy = false;
      await load();
    }
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    const ok = await act(
      () =>
        openGiveaway({
          title: form.title.trim(),
          command: form.command,
          winners: Number(form.winners),
          minutes: Number(form.minutes) || 0,
          claim_seconds: form.claim ? Number(form.claimSeconds) : 0,
          subscribers_only: form.subs,
          practice: form.practice,
        }),
      form.practice ? 'Practice giveaway open: enter with a test chat command on Events.' : 'The giveaway is open, and chat has been told how to enter.',
    );
    if (ok) form.title = '';
  }

  const g = $derived(current);
  const active = $derived(g ? g.draws.filter((d) => d.outcome === 'won' || d.outcome === 'pending_claim' || d.outcome === 'claimed').length : 0);
  const wanted = $derived(g ? g.winners - active : 0);
  const left = $derived(g ? g.entries.length - g.draws.length : 0);
  const pending = $derived(g?.draws.some((d) => d.outcome === 'pending_claim') ?? false);
  const stateWords: Record<string, string> = { open: 'Taking entries', closed: 'Entries closed', ended: 'Ended', cancelled: 'Cancelled' };
  const messageIcon: Record<string, string> = { sent: '✓', practice: '○', failed: '✗', unknown: '?', queued: '…', sending: '…' };
  const messageWords: Record<string, string> = {
    sent: 'Sent', practice: 'Would say (practice)', failed: 'Not sent', unknown: 'Unknown: may or may not be in chat', queued: 'Sending', sending: 'Sending',
  };

  function setAside(d: GiveawayDraw) {
    confirm = {
      title: `Set ${d.winner.display_name} aside?`,
      message: 'They stay in the record as set aside, and a replacement is drawn from those not yet drawn. Chat hears about the new winner.',
      label: 'Set aside and draw again',
      run: () => act(() => setAsideWinner(g!.id, d.id), 'A replacement was drawn.'),
    };
  }

  function cancel() {
    confirm = {
      title: `Cancel “${g!.title}”?`,
      message: 'Chat is told there’s no winner this time. Everything recorded is kept.',
      label: 'Cancel the giveaway',
      run: () => act(() => cancelGiveaway(g!.id), 'Cancelled.'),
    };
  }

  async function runCheck(d: GiveawayDetail) {
    check = { id: d.id, problems: await verify(d) };
  }

  async function show(id: string) {
    error = '';
    try {
      shown = shown?.id === id ? undefined : await getGiveaway(id);
    } catch (e) {
      error = message(e);
    }
  }
</script>

{#snippet record(d: GiveawayDetail)}
  {#if d.fingerprint}
    <p class="sg-muted">Entry list fingerprint (SHA-256): <code>{d.fingerprint}</code></p>
    <button type="button" class="button" onclick={() => runCheck(d)}>Check the draws</button>
    {#if check?.id === d.id}
      {#if check.problems.length === 0}
        <p class="state state-ok" role="status">
          <span aria-hidden="true">✓</span> The entry list matches its fingerprint, and every winner is at the recorded place among
          those not yet drawn. (The random picks themselves can’t be replayed: they come from your computer’s secure random source.)
        </p>
      {:else}
        <div role="alert"><ul class="error">{#each check.problems as p (p)}<li>{p}</li>{/each}</ul></div>
      {/if}
    {/if}
  {/if}
{/snippet}

<h1>Giveaways</h1>
<p class="sg-muted screen-intro">
  Viewers enter by typing a command in chat. One entry each, and every entry has the same chance. Winners are drawn by StreamGremlin
  with a secure random source, and every draw is recorded so it can be checked. Chat is told everything: how to enter, who’s in, and
  who won.
</p>

{#if loadError}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
{/if}

{#if g}
  <section class="sg-panel card" aria-labelledby="gw-heading" aria-busy={busy}>
    <h2 id="gw-heading">
      {g.title}
      <span class="tag">{stateWords[g.state]}</span>
      {#if g.practice}<span class="tag">Practice</span>{/if}
    </h2>
    <p>
      Viewers type <strong>!{g.command}</strong> to enter{g.subscribers_only ? ' (subscribers only)' : ''}.
      {g.entries.length} {g.entries.length === 1 ? 'entry' : 'entries'}.
      {#if g.state === 'open' && g.closes_at}Entries close in {duration((Date.parse(g.closes_at) - now) / 1000)}.{/if}
      {#if g.claim_command}Winners type <strong>!{g.claim_command}</strong> within {g.claim_seconds} seconds.{/if}
    </p>
    {#if g.practice}
      <p class="sg-muted">Practice: only test chat commands from the Events screen enter, and nothing is sent to Twitch.</p>
    {/if}
    <div class="row-actions">
      {#if g.state === 'open'}
        <button type="button" class="button" disabled={busy} onclick={() => act(() => closeGiveaway(g.id), 'Entries closed.')}>Close entries</button>
      {/if}
      {#if wanted > 0 && left > 0}
        <button type="button" class="sg-button" disabled={busy} onclick={() => act(() => drawGiveaway(g.id), 'Drawn, and announced in chat.')}
          >{wanted === 1 ? 'Draw a winner' : `Draw ${wanted} winners`}</button
        >
      {/if}
      {#if g.state === 'closed'}
        <button type="button" class="button" disabled={busy || pending} onclick={() => act(() => endGiveaway(g.id), 'Ended. Chat heard the winners.')}
          >End the giveaway</button
        >
      {/if}
      <button type="button" class="button button-danger" disabled={busy} onclick={cancel}>Cancel</button>
    </div>
    {#if pending}<p class="sg-muted">A winner can still claim: end the giveaway once they have, or set them aside.</p>{/if}
    {#if g.state === 'closed' && left === 0 && wanted > 0}
      <p class="sg-muted">Everyone who entered has been drawn.</p>
    {/if}

    {#if g.draws.length}
      <h3>Winners</h3>
      <ul class="live-list" aria-label="Winners">
        {#each g.draws as d (d.id)}
          <li>
            <strong>{d.winner.display_name}</strong>: {outcomeWords(d, now)}
            <span class="sg-muted">({howDrawn(d)})</span>
            {#if d.outcome === 'won' || d.outcome === 'pending_claim' || d.outcome === 'claimed'}
              <button type="button" class="button" disabled={busy} onclick={() => setAside(d)}>Set aside and draw again</button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {@render record(g)}

    <details>
      <summary>Entries ({g.entries.length}){g.refusals.length ? `, ${g.refusals.length} turned away` : ''}</summary>
      <ol class="live-list" aria-label="Entries">
        {#each g.entries as e (e.seq)}<li>{e.display_name} <span class="sg-muted">{eventTime(e.entered_at)}</span></li>{/each}
      </ol>
      {#each g.refusals as r (r.login)}<p class="sg-muted">{r.display_name}: turned away (subscribers only)</p>{/each}
    </details>

    <h3>In chat</h3>
    <ul class="live-list" aria-label="Giveaway chat messages">
      {#each g.messages as m (m.id)}
        <li>
          <span aria-hidden="true">{messageIcon[m.state]}</span>
          <span class="visually-hidden">{messageWords[m.state]}:</span>
          {m.text}
          {#if m.state !== 'sent' && m.state !== 'practice'}<span class="sg-muted">({messageWords[m.state]}{m.detail ? `: ${m.detail}` : ''})</span>{/if}
        </li>
      {/each}
    </ul>
    <p class="saved" role="status">{notice}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </section>
{:else if loaded && !loadError}
  <section class="sg-panel card" aria-labelledby="open-heading" aria-busy={busy}>
    <h2 id="open-heading">Open a giveaway</h2>
    <form class="test-form" onsubmit={submit} aria-label="Open a giveaway">
      <label class="field"><span>What’s being given away</span><input class="sg-input" bind:value={form.title} maxlength="100" placeholder="a Steam key" required /></label>
      <label class="field"><span>Entry command</span><input class="sg-input" bind:value={form.command} maxlength="25" /></label>
      <label class="field"><span>Winners</span><input class="sg-input" type="number" min="1" max="20" bind:value={form.winners} /></label>
      <label class="field">
        <span>Close entries after (minutes)</span>
        <input class="sg-input" type="number" min="0" max="1440" bind:value={form.minutes} />
        <span class="sg-muted field-hint">0: when you close them.</span>
      </label>
      <label class="choice-inline"><input type="checkbox" bind:checked={form.claim} /> Winners must claim by typing !claim</label>
      {#if form.claim}
        <label class="field"><span>Seconds to claim</span><input class="sg-input" type="number" min="15" max="600" bind:value={form.claimSeconds} /></label>
      {/if}
      <label class="choice-inline"><input type="checkbox" bind:checked={form.subs} /> Subscribers only</label>
      <label class="choice-inline">
        <input type="checkbox" bind:checked={form.practice} /> Practice: only test chat from Events enters, and nothing is sent to Twitch
      </label>
      <div class="row-actions"><button type="submit" class="sg-button" disabled={busy || !form.title.trim()}>Open the giveaway</button></div>
    </form>
    <p class="saved" role="status">{notice}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </section>
{/if}

{#if recent.length}
  <section class="sg-panel card" aria-labelledby="recent-heading">
    <h2 id="recent-heading">Earlier giveaways</h2>
    <ul class="live-list" aria-label="Earlier giveaways">
      {#each recent as r (r.id)}
        <li>
          <strong>{r.title}</strong> <span class="tag">{stateWords[r.state]}</span>
          {#if r.practice}<span class="tag">Practice</span>{/if}
          <span class="sg-muted">{eventTime(r.opened_at)}, {r.entry_count} {r.entry_count === 1 ? 'entry' : 'entries'}</span>
          <button type="button" class="button" aria-expanded={shown?.id === r.id} onclick={() => show(r.id)}>{shown?.id === r.id ? 'Hide' : 'Show'}</button>
          {#if shown?.id === r.id}
            <ul class="live-list" aria-label="Winners">
              {#each shown.draws as d (d.id)}
                <li><strong>{d.winner.display_name}</strong>: {outcomeWords(d, now)} <span class="sg-muted">({howDrawn(d)})</span></li>
              {:else}
                <li class="sg-muted">No one was drawn.</li>
              {/each}
            </ul>
            {@render record(shown)}
          {/if}
        </li>
      {/each}
    </ul>
  </section>
{/if}

{#if confirm}
  {@const c = confirm}
  <ConfirmDialog
    title={c.title}
    message={c.message}
    confirmLabel={c.label}
    onconfirm={() => {
      const run = c.run;
      confirm = undefined;
      run();
    }}
    oncancel={() => (confirm = undefined)}
  />
{/if}
