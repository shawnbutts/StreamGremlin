<script lang="ts">
  // Points (docs/adr/0032-viewers-and-points.md): who holds how many,
  // renaming the points, checking the ledger adds up, giving to everyone,
  // and how viewers earn. Each viewer's record and history, and giving or
  // taking by hand, are on the Viewers screen (D-060).
  import { onMount } from 'svelte';
  import {
    ApiError,
    auditPoints,
    getEarning,
    getGives,
    giveAll,
    previewGive,
    undoGive,
    getPoints,
    renamePoints,
    setEarning,
    type EarningRules,
    type EarningTick,
    type GiveGroup,
    type GiveWho,
    type PointsGive,
    type PointsView,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  // onviewer opens a viewer on the Viewers screen.
  let { refreshMs = 5000, onviewer }: { refreshMs?: number; onviewer?: (id: string) => void } = $props();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const key = () => (crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`);

  let view = $state<PointsView | undefined>();
  let loadError = $state('');
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');
  let audit = $state<{ ok: boolean; problems: string[] } | undefined>();
  let renaming = $state('');
  // The earning form, read once so a refresh never undoes what's being typed.
  let rules = $state<EarningRules | undefined>();
  let live = $state<'live' | 'offline' | 'unknown'>('unknown');
  let lastTick = $state<EarningTick | undefined>();
  // Giving to everyone in a group.
  let audience = $state<GiveWho['audience']>('chat');
  let minutes = $state(15);
  let each = $state(10);
  let giveReason = $state('');
  let group = $state<GiveGroup | undefined>();
  let groupError = $state('');
  let giveKey = key(); // one request id per give, kept while it's retried
  let gives = $state<PointsGive[]>([]);
  let undoingGive = $state<PointsGive | undefined>();

  const name = $derived(view?.currency.name ?? 'points');

  async function load() {
    try {
      view = await getPoints();
      gives = (await getGives()).gives;
      const earn = await getEarning();
      live = earn.live;
      lastTick = earn.last_tick;
      if (!rules) rules = earn.rules;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    const t = setInterval(load, refreshMs);
    return () => clearInterval(t);
  });

  async function act(f: () => Promise<unknown>, done: string): Promise<boolean> {
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

  async function rename(e: SubmitEvent) {
    e.preventDefault();
    const n = renaming.trim();
    if (await act(() => renamePoints(n), `Your points are now called ${n}.`)) renaming = '';
  }

  async function check() {
    busy = true;
    try {
      audit = await auditPoints();
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  async function saveEarning(e: SubmitEvent) {
    e.preventDefault();
    if (!rules) return;
    const r = $state.snapshot(rules) as EarningRules;
    const whole = (n: unknown) => Math.max(0, Math.round(Number(n) || 0));
    r.chat.amount = whole(r.chat.amount);
    r.chat.every_seconds = whole(r.chat.every_seconds);
    r.time = { ...r.time, every_minutes: whole(r.time.every_minutes), active: whole(r.time.active), active_minutes: whole(r.time.active_minutes), quiet: whole(r.time.quiet) };
    r.sub = { tier1: whole(r.sub.tier1), tier2: whole(r.sub.tier2), tier3: whole(r.sub.tier3) };
    r.follow = whole(r.follow);
    r.gift = whole(r.gift);
    r.bits = whole(r.bits);
    r.super_chat = whole(r.super_chat ?? 0);
    let saved: EarningRules | undefined;
    if (await act(async () => (saved = (await setEarning(r)).rules), r.on ? 'Saved. Viewers earn from now on.' : 'Saved. Earning is off.')) rules = saved;
  }

  const who = (): GiveWho => (audience === 'recent' ? { audience, minutes: Math.round(Number(minutes)) } : { audience });
  const eachN = () => Math.round(Number(each));

  // Anything changed: the group shown is no longer the one that'd be given to.
  function changed() {
    group = undefined;
    groupError = '';
    giveKey = key();
  }

  async function seeWho() {
    busy = true;
    groupError = '';
    try {
      group = await previewGive(who());
    } catch (e) {
      group = undefined;
      groupError = message(e);
    } finally {
      busy = false;
    }
  }

  async function give() {
    if (!group || !(eachN() > 0)) return;
    const k = giveKey;
    let done: PointsGive | undefined;
    if (await act(async () => (done = await giveAll(who(), eachN(), giveReason.trim(), k)), '')) {
      notice = `Gave ${done!.viewers} ${done!.viewers === 1 ? 'viewer' : 'viewers'} ${done!.amount} ${name} each, ${done!.total} in all.`;
      group = undefined;
      giveReason = '';
      giveKey = key();
    }
  }

  async function undoAll(g: PointsGive) {
    undoingGive = undefined;
    let res: { undone: number; short: string[] } | undefined;
    await act(async () => (res = await undoGive(g.id)), '');
    if (!res) return;
    notice = `Took it back from ${res.undone} ${res.undone === 1 ? 'viewer' : 'viewers'}.`;
    if (res.short.length) {
      error = `${res.short.join(', ')} spent some already and keep theirs: nobody goes below zero. Undo again later to try them again.`;
    }
  }

  const liveWords = { live: 'You’re live.', offline: 'You’re offline.', unknown: 'Unknown whether you’re live: nothing has said yet.' };
</script>

<h1>Points</h1>
<p class="sg-muted screen-intro">
  Viewers’ points: who holds how many, giving to everyone, and how viewers earn. Nothing is ever changed or deleted: a mistake is
  undone, once, and nobody goes below zero. Give, take or undo for one viewer on the Viewers screen. Workflows give and take points with
  “Give points” and “Take points”; a retried run never pays twice.
</p>

{#if loadError}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
{/if}

<section class="sg-panel card" aria-labelledby="points-heading" aria-busy={busy}>
  <h2 id="points-heading">Your {name}</h2>
  {#if view}
    <p>{view.summary.viewers} {view.summary.viewers === 1 ? 'viewer holds' : 'viewers hold'} {view.summary.issued} {name}.</p>
  {/if}
  <form class="row-actions" onsubmit={rename} aria-label="Rename your points">
    <label class="field inline-field">
      <span>Call them</span>
      <input class="sg-input" bind:value={renaming} maxlength="40" placeholder={name} />
    </label>
    <button type="submit" class="button" disabled={busy || !renaming.trim()}>Rename</button>
  </form>
  <div class="row-actions">
    <button type="button" class="button" disabled={busy} onclick={check}>Check the ledger</button>
  </div>
  {#if audit?.ok}
    <p class="state state-ok" role="status"><span aria-hidden="true">✓</span> Every change balances, and every balance matches its history.</p>
  {:else if audit}
    <p class="error" role="alert">{audit.problems.join(' ')}</p>
  {/if}
  {#if view?.top.length}
    <h3>Top viewers</h3>
    <ol class="live-list" aria-label="Top viewers">
      {#each view.top as v (v.id)}
        <li>
          {#if onviewer}<button type="button" class="link-button" onclick={() => onviewer(v.id)}>{v.display_name}</button>{:else}{v.display_name}{/if}: {v.balance}
          {name}
        </li>
      {/each}
    </ol>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="give-all-heading" aria-busy={busy}>
  <h2 id="give-all-heading">Give to everyone</h2>
  <p class="sg-muted">Give everyone in a group {name} at once. See who first; each viewer gets them once, even if it’s tried again.</p>
  <fieldset onchange={changed}>
    <legend>Who</legend>
    <label class="choice-inline"><input type="radio" name="audience" value="chat" bind:group={audience} /> Everyone in chat</label>
    <label class="choice-inline"><input type="radio" name="audience" value="recent" bind:group={audience} /> Everyone who chatted lately</label>
    <label class="choice-inline"><input type="radio" name="audience" value="subs" bind:group={audience} /> Subscribers</label>
  </fieldset>
  <div class="row-actions">
    {#if audience === 'recent'}
      <label class="field inline-field"
        ><span>In the last (minutes)</span><input class="sg-input" type="number" min="1" max="240" bind:value={minutes} oninput={changed} /></label
      >
    {/if}
    <label class="field inline-field"><span>How many each</span><input class="sg-input" type="number" min="1" bind:value={each} oninput={changed} /></label>
    <label class="field inline-field"
      ><span>Why</span><input class="sg-input" bind:value={giveReason} maxlength="200" placeholder="optional" oninput={changed} /></label
    >
  </div>
  <p class="sg-muted">
    {#if audience === 'chat'}Twitch’s list of who’s in your chat, which it updates a little late. You and your bot aren’t included.
    {:else if audience === 'subs'}Twitch’s list of your subscribers.
    {:else}Viewers who chatted, from your real chat.{/if}
  </p>
  <div class="row-actions">
    <button type="button" class="button" disabled={busy} onclick={seeWho}>See who</button>
    {#if group && group.viewers > 0}
      <button type="button" class="sg-button" disabled={busy || !(eachN() > 0)} onclick={give}
        >Give {group.viewers} {group.viewers === 1 ? 'viewer' : 'viewers'} {eachN()} each</button
      >
    {/if}
  </div>
  {#if groupError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> {groupError}</p>
  {:else if group && group.viewers === 0}
    <p class="sg-muted" role="status">{group.note}</p>
  {:else if group}
    <p role="status">
      {group.viewers} {group.viewers === 1 ? 'viewer' : 'viewers'}: {group.sample.join(', ')}{group.viewers > group.sample.length
        ? ` and ${group.viewers - group.sample.length} more`
        : ''}. {eachN()} {name} each, {eachN() * group.viewers} in all.
    </p>
  {/if}
  {#if gives.length}
    <h3>Recent gives</h3>
    <ul class="live-list" aria-label="Recent gives">
      {#each gives as g (g.id)}
        <li>
          {eventTime(g.created_at)}: {g.amount} each to {g.viewers} ({g.words}){g.memo ? `: ${g.memo}` : ''}{g.undone_at ? ' (undone)' : ''}
          {#if !g.undone_at}
            <button type="button" class="button" disabled={busy} aria-label={`Undo the give to ${g.words}`} onclick={() => (undoingGive = g)}>Undo</button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="earning-heading" aria-busy={busy}>
  <h2 id="earning-heading">How viewers earn {name}</h2>
  <p class="sg-muted">
    Real chat and events only, never test events or your own. It starts from now: nothing that happened before pays. Set an amount to 0 to
    turn that one off.
  </p>
  {#if rules}
    <form onsubmit={saveEarning} aria-label={`How viewers earn ${name}`}>
      <label class="choice-inline"><input type="checkbox" bind:checked={rules.on} /> Viewers earn {name}</label>
      <fieldset>
        <legend>Chatting</legend>
        <div class="row-actions">
          <label class="field inline-field"><span>Each message</span><input class="sg-input" type="number" min="0" bind:value={rules.chat.amount} /></label>
          <label class="field inline-field"
            ><span>At most every (seconds)</span><input class="sg-input" type="number" min="0" bind:value={rules.chat.every_seconds} /></label
          >
        </div>
        <label class="choice-inline"><input type="checkbox" bind:checked={rules.chat.live_only} /> Only while you’re live</label>
        {#if rules.chat.live_only}
          <p class="sg-muted" role="status">{liveWords[live]}{live === 'live' ? '' : ' Chat earns nothing until you go live.'}</p>
        {/if}
      </fieldset>
      <fieldset>
        <legend>Time in chat</legend>
        <label class="choice-inline"><input type="checkbox" bind:checked={rules.time.on} /> Pay for time in chat while you’re live</label>
        {#if rules.time.on}
          <div class="row-actions">
            <label class="field inline-field"
              ><span>Every (minutes)</span><input class="sg-input" type="number" min="1" max="60" bind:value={rules.time.every_minutes} /></label
            >
            <label class="field inline-field"><span>If they chatted</span><input class="sg-input" type="number" min="0" bind:value={rules.time.active} /></label>
            <label class="field inline-field"
              ><span>In the last (minutes)</span><input class="sg-input" type="number" min="1" max="120" bind:value={rules.time.active_minutes} /></label
            >
            <label class="field inline-field"><span>If they’re in chat but quiet</span><input class="sg-input" type="number" min="0" bind:value={rules.time.quiet} /></label>
          </div>
          <p class="sg-muted">
            Quiet viewers come from Twitch’s list of who’s in your chat, which Twitch updates a little late. Time while StreamGremlin
            was closed is never paid: who was there can’t be known.
          </p>
        {/if}
        {#if lastTick}
          <p class="sg-muted" role="status">
            Last paid {eventTime(lastTick.at)}: {lastTick.active} chatting, {lastTick.quiet} quiet.
          </p>
          {#if lastTick.note}<p class="state state-unknown"><span aria-hidden="true">?</span> Why: {lastTick.note}.</p>{/if}
        {/if}
      </fieldset>
      <fieldset>
        <legend>Following, subs and bits</legend>
        <div class="row-actions">
          <label class="field inline-field"><span>A first follow (once each)</span><input class="sg-input" type="number" min="0" bind:value={rules.follow} /></label>
          <label class="field inline-field"><span>Tier 1 sub</span><input class="sg-input" type="number" min="0" bind:value={rules.sub.tier1} /></label>
          <label class="field inline-field"><span>Tier 2 sub</span><input class="sg-input" type="number" min="0" bind:value={rules.sub.tier2} /></label>
          <label class="field inline-field"><span>Tier 3 sub</span><input class="sg-input" type="number" min="0" bind:value={rules.sub.tier3} /></label>
          <label class="field inline-field"><span>Each sub gifted (to the gifter)</span><input class="sg-input" type="number" min="0" bind:value={rules.gift} /></label>
          <label class="field inline-field"><span>Each 100 bits</span><input class="sg-input" type="number" min="0" bind:value={rules.bits} /></label>
          <label class="field inline-field"
            ><span>Each YouTube Super Chat or Super Sticker</span><input class="sg-input" type="number" min="0" bind:value={rules.super_chat} /></label
          >
        </div>
        <p class="sg-muted">
          Resubs earn the same as subs. A gifted sub earns for the gifter, not the viewer who gets it. YouTube members earn as tier 1 subs, and
          gifted memberships as gifted subs. A Super Chat earns the same whatever its amount, since amounts come in many currencies.
        </p>
      </fieldset>
      <button type="submit" class="sg-button" disabled={busy}>Save</button>
    </form>
  {/if}
</section>

<p class="saved" role="status">{notice}</p>
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if undoingGive}
  {@const g = undoingGive}
  <ConfirmDialog
    title={`Undo the give to ${g.words}?`}
    message={`${g.amount} ${name} are taken back from each of the ${g.viewers}. Anyone who has spent some keeps theirs: nobody goes below zero.`}
    confirmLabel="Undo"
    onconfirm={() => undoAll(g)}
    oncancel={() => (undoingGive = undefined)}
  />
{/if}

<style>
  fieldset { border: none; padding: 0; margin: 0 0 0.75rem; }
  legend { font-weight: 600; margin-bottom: 0.25rem; }
</style>
