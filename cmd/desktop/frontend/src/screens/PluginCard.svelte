<script lang="ts">
  // An installed Lua plugin on Add-ons (docs/adr/0038-lua-plugins.md): on
  // or off, what it may do, its steps, its settings, and what it has done.
  import {
    ApiError,
    getPackageActivity,
    setPackageEnabled,
    setPackageSettings,
    type InstalledPackage,
    type PluginActivity,
  } from '../lib/api';
  import { eventTime } from '../lib/events';

  let {
    p,
    busy = false,
    onchanged,
    onremove,
  }: { p: InstalledPackage; busy?: boolean; onchanged: () => void; onremove: () => void } = $props();

  const plugin = $derived(p.plugin!);
  let working = $state(false);
  let error = $state('');
  let saved = $state('');
  let edits = $state<Record<string, unknown>>({});
  let activity = $state<PluginActivity[] | undefined>();
  let showActivity = $state(false);

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const value = (key: string) => (key in edits ? edits[key] : plugin.values[key]);
  const changed = $derived(Object.keys(edits).some((k) => edits[k] !== plugin.values[k]));
  const OUTCOMES: Record<PluginActivity['outcome'], string> = { ok: 'done', refused: 'refused', failed: 'failed' };

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
  {#if plugin.templates.length}
    <p class="field-hint">
      <strong>Templates:</strong>
      {plugin.templates.map((t) => t.title).join(', ')}. Find them in the gallery under “{p.name} (add-on)” when you make a new workflow.
    </p>
  {/if}
  <p class="sg-muted field-hint">
    {p.workflows?.length ? `Used by ${p.workflows.join(', ')}.` : 'No workflow uses it yet.'}
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
            {a.call === 'log' ? 'Logged' : a.call === 'chat.send' ? 'Chat' : a.call}
            {#if a.call !== 'log'}<span class={a.outcome === 'ok' ? '' : 'error'}>({OUTCOMES[a.outcome]})</span>{/if}:
            {a.detail}
          </li>
        {/each}
      </ul>
    {/if}
  </div>

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
