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
  const textOf = (v: unknown) => (typeof v === 'string' ? toDisplay(v, named) : v === undefined ? '' : String(v));
</script>

{#if param.editor === 'message'}
  <MessageInput {label} multiline={param.name === 'message'} bind:value={() => current as string | undefined, (v) => set(v)} />
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
