<script lang="ts">
  // One action parameter as a control, by its editor hint and type.
  // Values the control can't show (an expression in a number, a list) are
  // shown as text and kept.
  import type { ActionParam } from '../lib/api';
  import { toDisplay, toTemplate, typedValue, type StepDef } from '../lib/editor';
  import { allValues, editorEnv } from '../lib/editorEnv';
  import MessageInput from './MessageInput.svelte';

  let { param, step = $bindable() }: { param: ActionParam; step: StepDef } = $props();

  const env = editorEnv();
  const named = $derived(allValues(env));
  const label = $derived(param.label + (param.required ? '' : ' (optional)'));
  const current = $derived(step.with?.[param.name]);

  function set(v: unknown) {
    step.with ??= {};
    if (v === undefined || v === '') delete step.with[param.name];
    else step.with[param.name] = v;
  }

  // "Values to pass" for workflow.run: rows of a name and text.
  const argRows = $derived(
    current && typeof current === 'object' && !Array.isArray(current) && Object.values(current).every((v) => typeof v !== 'object')
      ? Object.entries(current as Record<string, unknown>)
      : undefined,
  );
  let newArg = $state('');

  function setArg(name: string, v: unknown) {
    const next = { ...((current as Record<string, unknown>) ?? {}) };
    next[name] = v;
    set(next);
  }
  function removeArg(name: string) {
    const next = { ...((current as Record<string, unknown>) ?? {}) };
    delete next[name];
    set(Object.keys(next).length ? next : undefined);
  }
  // A message can be several ways to say it, {one_of: [...]}: one is
  // picked at random each run.
  const versions = $derived(
    current && typeof current === 'object' && Array.isArray((current as { one_of?: unknown }).one_of)
      ? ((current as { one_of: unknown[] }).one_of.map((x) => (typeof x === 'string' ? x : String(x))) as string[])
      : undefined,
  );
  // Rows are rebuilt when one is added or removed, so each shows its own text.
  let versionsKey = $state(0);

  function addVersion() {
    const list = versions ?? [typeof current === 'string' ? current : ''];
    set({ one_of: [...list, ''] });
    versionsKey++;
  }

  function removeVersion(i: number) {
    const list = (versions ?? []).filter((_, j) => j !== i);
    // Down to one: a plain message again.
    set(list.length === 1 ? list[0] : { one_of: list });
    versionsKey++;
  }

  function setVersion(i: number, v: string) {
    const list = [...(versions ?? [])];
    list[i] = v;
    set({ one_of: list });
  }

  const textOf = (v: unknown) => (typeof v === 'string' ? toDisplay(v, named) : v === undefined ? '' : String(v));
</script>

{#if param.editor === 'message'}
  {#if versions}
    <div class="versions" role="group" aria-label={`${param.label}: one of these, picked at random`}>
      <span class="versions-title">{param.label}: one of these, picked at random each time</span>
      {#key versionsKey}
        {#each versions as v, i (i)}
          <div class="version-row">
            <MessageInput label={`Way ${i + 1}`} multiline={param.name === 'message'} bind:value={() => v, (nv) => setVersion(i, nv ?? '')} />
            <button type="button" class="button" aria-label={`Remove way ${i + 1}`} onclick={() => removeVersion(i)}>Remove</button>
          </div>
        {/each}
      {/key}
      <button type="button" class="button" disabled={versions.length >= 50} onclick={addVersion}>+ Another way to say it</button>
    </div>
  {:else}
    <MessageInput {label} multiline={param.name === 'message'} bind:value={() => current as string | undefined, (v) => set(v)} />
    <button type="button" class="button add-version" onclick={addVersion}>+ Another way to say it</button>
  {/if}
{:else if param.editor === 'workflow'}
  <label class="field">
    <span>{label}</span>
    <select class="sg-input" value={current ?? ''} onchange={(e) => set(e.currentTarget.value)}>
      <option value="">Choose a workflow…</option>
      {#each env.workflows.filter((w) => w.id !== env.selfId) as w (w.id)}
        <option value={w.id}>{w.name}{w.enabled ? '' : ' (off: turn it on to run it)'}</option>
      {/each}
      {#if typeof current === 'string' && current && !env.workflows.some((w) => w.id === current)}
        <option value={current}>{current} (not found)</option>
      {/if}
    </select>
  </label>
{:else if param.editor === 'sound'}
  <label class="field">
    <span>{label}</span>
    <select class="sg-input" value={current ?? ''} onchange={(e) => set(e.currentTarget.value)}>
      <option value="">{param.required ? 'Choose a sound…' : (param.default_hint ?? 'None')}</option>
      {#each env.sounds as s (s.name)}<option value={s.name}>{s.name}</option>{/each}
      {#if typeof current === 'string' && current && !env.sounds.some((s) => s.name === current)}
        <option value={current}>{current} (not in your library)</option>
      {/if}
    </select>
    {#if !env.sounds.length}<span class="sg-muted field-hint">No sounds yet: add one on the Sounds screen.</span>{/if}
  </label>
{:else if param.editor === 'overlay'}
  <label class="field">
    <span>{label}</span>
    <select class="sg-input" value={current ?? ''} onchange={(e) => set(e.currentTarget.value)}>
      <option value="">Choose an overlay…</option>
      {#each env.overlays as o (o.name)}<option value={o.name}>{o.name}</option>{/each}
      {#if typeof current === 'string' && current && !env.overlays.some((o) => o.name === current)}
        <option value={current}>{current} (not found)</option>
      {/if}
    </select>
    {#if !env.overlays.length}<span class="sg-muted field-hint">No overlays yet: add one on the Overlays screen.</span>{/if}
  </label>
{:else if param.editor === 'reward'}
  <label class="field">
    <span>{label}</span>
    <select class="sg-input" value={current ?? ''} onchange={(e) => set(e.currentTarget.value)}>
      <option value="">Choose a reward…</option>
      {#each env.rewards.filter((r) => r.managed) as r (r.id)}<option value={r.title}>{r.title}</option>{/each}
      {#if typeof current === 'string' && current && !env.rewards.some((r) => r.managed && r.title === current)}
        <option value={current}>{current} (not made in StreamGremlin)</option>
      {/if}
    </select>
    {#if !env.rewards.some((r) => r.managed)}
      <span class="sg-muted field-hint">Only rewards made on the Rewards screen can be changed by a workflow.</span>
    {/if}
  </label>
{:else if param.editor === 'timer'}
  <label class="field">
    <span>{label}</span>
    <select class="sg-input" value={current ?? ''} onchange={(e) => set(e.currentTarget.value)}>
      <option value="">Choose a timer…</option>
      {#each env.timers as t (t.id)}<option value={t.id}>{t.name}{t.enabled ? '' : ' (off)'}</option>{/each}
      {#if typeof current === 'string' && current && !env.timers.some((t) => t.id === current)}
        <option value={current}>{current} (not found)</option>
      {/if}
    </select>
  </label>
{:else if param.type === 'choice'}
  <label class="field">
    <span>{label}</span>
    <select class="sg-input" value={current ?? ''} onchange={(e) => set(e.currentTarget.value)}>
      <option value="">{param.required ? 'Choose…' : (param.default_hint ?? 'Leave it unset')}</option>
      {#each param.choices ?? [] as c (c)}<option value={c}>{c}</option>{/each}
    </select>
  </label>
{:else if param.type === 'bool' && (current === undefined || typeof current === 'boolean')}
  <label class="choice-inline">
    <input type="checkbox" checked={current === true} onchange={(e) => set(e.currentTarget.checked)} />
    {param.label}
  </label>
{:else if param.type === 'number' && (current === undefined || typeof current === 'number')}
  <label class="field">
    <span>{label}</span>
    <input
      class="sg-input"
      type="number"
      step="any"
      value={current ?? ''}
      placeholder={param.default !== undefined ? String(param.default) : ''}
      onchange={(e) => set(e.currentTarget.value === '' ? undefined : Number(e.currentTarget.value))}
    />
  </label>
{:else if param.name === 'args' && param.type === 'any' && (current === undefined || argRows)}
  <fieldset class="args">
    <legend>{label}</legend>
    {#each argRows ?? [] as [name, v] (name)}
      <div class="arg-row">
        <MessageInput label={`args.${name}`} bind:value={() => (typeof v === 'string' ? v : String(v)), (nv) => setArg(name, nv)} />
        <button type="button" class="button" aria-label={`Remove ${name}`} onclick={() => removeArg(name)}>Remove</button>
      </div>
    {/each}
    <div class="arg-row">
      <label class="field">
        <span>New value’s name</span>
        <input class="sg-input" bind:value={newArg} placeholder="who" spellcheck="false" />
      </label>
      <button
        type="button"
        class="button"
        disabled={!/^[a-z][a-z0-9_]*$/.test(newArg)}
        onclick={() => { setArg(newArg, ''); newArg = ''; }}>Add</button
      >
    </div>
  </fieldset>
{:else if param.editor === 'value' || param.type === 'any'}
  <label class="field">
    <span>{label}</span>
    <input
      class="sg-input"
      value={typeof current === 'object' && current !== null ? JSON.stringify(current) : textOf(current)}
      readonly={typeof current === 'object' && current !== null}
      onchange={(e) => set(e.currentTarget.value === '' ? undefined : typedValue(e.currentTarget.value, named))}
    />
    <span class="sg-muted field-hint">A number, true or false, or text (with values like {'{viewer}'}).</span>
  </label>
{:else}
  <label class="field">
    <span>{label}</span>
    <input
      class="sg-input"
      list={param.editor === 'scene' ? 'editor-scenes' : param.editor === 'source' ? 'editor-sources' : undefined}
      value={textOf(current)}
      spellcheck="false"
      onchange={(e) => set(e.currentTarget.value === '' ? undefined : toTemplate(e.currentTarget.value, named))}
    />
    {#if param.pattern_hint}<span class="sg-muted field-hint">{param.pattern_hint}</span>{/if}
  </label>
{/if}
