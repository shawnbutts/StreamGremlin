<script lang="ts">
  // An installed Lua plugin on Add-ons (docs/adr/0038-lua-plugins.md): on
  // or off, what it may do, its steps, its settings, and what it has done.
  import {
    ApiError,
    getPackageActivity,
    getPackageRounds,
    setPackageEnabled,
    setPackageSettings,
    type InstalledPackage,
    type PluginActivity,
    type PluginRound,
  } from '../lib/api';
  import { eventTime } from '../lib/events';

  let {
    p,
    busy = false,
    onchanged,
    onremove,
    onrollback,
  }: { p: InstalledPackage; busy?: boolean; onchanged: () => void; onremove: () => void; onrollback?: () => void } = $props();

  const plugin = $derived(p.plugin!);
  let working = $state(false);
  let error = $state('');
  let saved = $state('');
  let edits = $state<Record<string, unknown>>({});
  let activity = $state<PluginActivity[] | undefined>();
  let showActivity = $state(false);
  let roundList = $state<PluginRound[] | undefined>();
  let showRounds = $state(false);
  const hasRounds = $derived(plugin.permissions.some((x) => x.name === 'points.rounds'));
  const STATES: Record<PluginRound['state'], string> = { open: 'open', settling: 'paying out', settled: 'paid out', refunded: 'refunded' };

  async function toggleRounds() {
    showRounds = !showRounds;
    if (!showRounds) return;
    try {
      roundList = (await getPackageRounds(p.id)).rounds;
    } catch (e) {
      error = message(e);
    }
  }

  function paidBack(r: PluginRound) {
    const names = Object.fromEntries(r.stakes.map((s) => [s.viewer, s.name]));
    return Object.entries(r.payouts ?? {})
      .map(([v, n]) => `${names[v] ?? v} ${n}`)
      .join(', ');
  }

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const value = (key: string) => (key in edits ? edits[key] : plugin.values[key]);
  const changed = $derived(Object.keys(edits).some((k) => edits[k] !== plugin.values[k]));
  const OUTCOMES: Record<PluginActivity['outcome'], string> = { ok: 'done', refused: 'refused', failed: 'failed' };
  const CALLS: Record<string, string> = {
    log: 'Logged',
    'chat.send': 'Chat',
    'events.publish': 'Event',
    timer: 'Timer',
    'points.balance': 'Balance',
    'points.rounds': 'Rounds',
    'round.open': 'Round opened',
    'round.stake': 'Stake',
    'round.roll': 'Roll',
    'round.settle': 'Paid out',
    'round.cancel': 'Round cancelled',
  };

  async function toggle(on: boolean) {
    working = true;
    error = '';
    saved = '';
    try {
      await setPackageEnabled(p.id, on);
      saved = on ? `${p.name} is on.` : `${p.name} is off: its steps fail until it’s on again; its workflows stay on.`;
    } catch (e) {
      error = message(e);
    } finally {
      working = false;
      onchanged();
      if (showActivity) await loadActivity();
    }
  }

  async function save() {
    working = true;
    error = '';
    saved = '';
    try {
      const r = await setPackageSettings(p.id, edits);
      edits = {};
      saved = r.plugin?.error
        ? `Saved, but ${p.name} is off: ${r.plugin.error}.`
        : r.plugin?.enabled
          ? `Saved. ${p.name} started again, fresh, to read them.`
          : 'Saved.';
    } catch (e) {
      error = message(e);
    } finally {
      working = false;
      onchanged();
    }
  }

  async function loadActivity() {
    try {
      activity = (await getPackageActivity(p.id)).activity;
    } catch (e) {
      error = message(e);
    }
  }

  async function toggleActivity() {
    showActivity = !showActivity;
    if (showActivity) await loadActivity();
  }
</script>

<div class="plugin">
  <div class="plugin-head">
    <div>
      <strong>{p.name}</strong>
      {p.version} <span class="sg-muted">· Lua plugin · published by {p.publisher} (not verified)</span>
      {#if p.description}<p class="sg-muted field-hint">{p.description}</p>{/if}
    </div>
    <div class="actions">
      <label class="choice-inline">
        <input
          type="checkbox"
          role="switch"
          checked={plugin.enabled}
          disabled={busy || working}
          aria-describedby={`plugin-state-${p.id}`}
          onchange={(e) => toggle(e.currentTarget.checked)}
        />
        On
      </label>
      {#if p.rollback_to && onrollback}
        <button type="button" class="button" aria-label={`Roll ${p.name} back to ${p.rollback_to}`} disabled={busy || working} onclick={onrollback}
          >Roll back to {p.rollback_to}</button
        >
      {/if}
      <button type="button" class="button button-danger" aria-label={`Remove ${p.name}`} disabled={busy || working} onclick={onremove}>Remove</button>
    </div>
  </div>
  <p id={`plugin-state-${p.id}`} class={plugin.error ? 'error' : 'sg-muted field-hint'}>
    {#if plugin.error}
      <span aria-hidden="true">!</span> Off: {plugin.error}.
    {:else if plugin.enabled}
      On. It keeps its own memory while it’s on; turning it off, changing its settings or restarting StreamGremlin starts it over.
    {:else}
      Off. Its steps fail until you turn it on.
    {/if}
  </p>

  <p class="field-hint">
    <strong>It may:</strong>
    {#if plugin.permissions.length}
      {plugin.permissions.map((x) => x.what).join('; ')}.
    {:else}
      nothing beyond its own steps.
    {/if}
    It can’t reach your files, the internet or your accounts.
  </p>
  <p class="field-hint">
    <strong>Steps it adds:</strong>
    {plugin.actions.map((a) => a.label).join(', ')}. Find them under “{p.name}” when you add a step.
  </p>
  {#if plugin.events?.length}
    <p class="field-hint">
      <strong>Events it sends:</strong>
      {plugin.events.map((e) => e.label).join(', ')}. Find them under “When” in a workflow, under “{p.name} (add-on)”.
    </p>
  {/if}
  {#if plugin.templates.length}
    <p class="field-hint">
      <strong>Templates:</strong>
      {plugin.templates.map((t) => t.title).join(', ')}. Find them in the gallery under “{p.name} (add-on)” when you make a new workflow.
    </p>
  {/if}
  <p class="sg-muted field-hint">
    {p.workflows?.length ? `Used by ${p.workflows.join(', ')}.` : 'No workflow uses it yet.'}
    {p.kept.length ? `Kept to roll back to: ${p.kept.join(', ')}.` : ''}
  </p>

  {#if p.settings?.length}
    <fieldset class="plugin-settings" disabled={busy || working}>
      <legend>Settings</legend>
      {#each p.settings as s (s.key)}
        {#if s.kind === 'choice'}
          <label class="field">
            <span>{s.label}</span>
            <select class="sg-input" value={String(value(s.key))} onchange={(e) => (edits[s.key] = e.currentTarget.value)}>
              {#each s.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
            </select>
          </label>
        {:else if s.kind === 'number'}
          <label class="field">
            <span>{s.label}</span>
            <input class="sg-input" type="number" step="1" min={s.min} max={s.max} value={Number(value(s.key))}
              oninput={(e) => {
                const n = e.currentTarget.valueAsNumber;
                if (Number.isInteger(n)) edits[s.key] = n;
              }} />
          </label>
        {:else if s.kind === 'switch'}
          <label class="choice-inline">
            <input type="checkbox" checked={value(s.key) === true} onchange={(e) => (edits[s.key] = e.currentTarget.checked)} />
            {s.label}
          </label>
        {:else}
          <label class="field">
            <span>{s.label}</span>
            <input
              class={s.kind === 'colour' ? 'sg-input color-input' : 'sg-input'}
              type={s.kind === 'colour' ? 'color' : 'text'}
              maxlength={s.max}
              value={String(value(s.key))}
              oninput={(e) => (edits[s.key] = e.currentTarget.value)}
            />
          </label>
        {/if}
        {#if s.hint}<p class="sg-muted field-hint">{s.hint}</p>{/if}
      {/each}
      <button type="button" class="button" disabled={!changed} onclick={save}>Save settings</button>
      {#if plugin.enabled}<span class="sg-muted field-hint">Saving starts it again, fresh.</span>{/if}
    </fieldset>
  {/if}

  <button type="button" class="button" aria-expanded={showActivity} aria-controls={`plugin-activity-${p.id}`} onclick={toggleActivity}>
    {showActivity ? 'Hide' : 'Show'} what it has done
  </button>
  <div id={`plugin-activity-${p.id}`} hidden={!showActivity}>
    {#if activity && activity.length === 0}
      <p class="sg-muted">Nothing yet.</p>
    {:else if activity}
      <ul class="activity" aria-label={`What ${p.name} has done`}>
        {#each activity as a (a.seq)}
          <li>
            <span class="sg-muted">{eventTime(a.at)}</span>
            {CALLS[a.call] ?? a.call}
            {#if a.call !== 'log'}<span class={a.outcome === 'ok' ? '' : 'error'}>({OUTCOMES[a.outcome]})</span>{/if}:
            {a.detail}
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  {#if hasRounds}
    <button type="button" class="button" aria-expanded={showRounds} aria-controls={`plugin-rounds-${p.id}`} onclick={toggleRounds}>
      {showRounds ? 'Hide' : 'Show'} its rounds
    </button>
    <div id={`plugin-rounds-${p.id}`} hidden={!showRounds}>
      <p class="sg-muted field-hint">
        Each round holds viewers’ points until it pays out or gives them back. The rules are the plugin’s own words:
        StreamGremlin can’t check them. The rolls are made and recorded by StreamGremlin.
      </p>
      {#if roundList && roundList.length === 0}
        <p class="sg-muted">No rounds yet.</p>
      {:else if roundList}
        <ul class="activity" aria-label={`${p.name}’s rounds`}>
          {#each roundList as r (r.id)}
            <li>
              <span class="sg-muted">{eventTime(r.opened_at)}</span>
              <strong>{r.title}</strong>, {STATES[r.state]}{r.reason ? `: ${r.reason}` : ''}.
              <span class="sg-muted">Rules, as {p.name} states them: {r.rules}</span>
              Stakes: {r.stakes.length ? r.stakes.map((s) => `${s.name} ${s.amount}`).join(', ') : 'none'}.
              {#if r.rolls.length}
                Rolls by StreamGremlin: {r.rolls.map((x) => `${x.label} ${x.value} of ${x.sides}`).join(', ')}.
              {/if}
              {#if r.state === 'settled'}Paid back: {paidBack(r) || 'nothing'}.{/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}

  <p class="saved" role="status">{saved}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</div>

<style>
  .plugin {
    display: grid;
    gap: 6px;
    width: 100%;
  }
  .plugin-head {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }
  .actions {
    display: flex;
    gap: 8px;
    align-items: center;
    flex-wrap: wrap;
  }
  .plugin-settings {
    display: grid;
    gap: 8px;
    border: 1px solid var(--sg-border);
    border-radius: 8px;
    padding: 8px 12px 12px;
  }
  .plugin p {
    margin: 0;
  }
  .activity {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: 4px;
    max-height: 16em;
    overflow: auto;
    overflow-wrap: anywhere;
  }
</style>
