<script lang="ts">
  // A list of Then steps, each a card with its fields. Flow steps nest
  // their own lists. Steps the editor can't show are kept as they are.
  import type { ActionInfo } from '../lib/api';
  import { exprToText, ownProblems, stepKind, textToExpr, waitSeconds, type StepDef } from '../lib/editor';
  import { allValues, editorEnv } from '../lib/editorEnv';
  import ConditionInput from './ConditionInput.svelte';
  import MessageInput from './MessageInput.svelte';
  import ParamField from './ParamField.svelte';
  import StepList from './StepList.svelte';

  let { steps = $bindable(), path, what = 'a step' }: { steps: StepDef[]; path: string; what?: string } = $props();

  const env = editorEnv();
  const named = $derived(allValues(env));
  const actionOf = (s: StepDef) => env.actions.find((a) => a.name === s.action);
  const title = (s: StepDef) => {
    switch (stepKind(s)) {
      case 'action':
        return actionOf(s)?.label ?? s.action;
      case 'set':
        return 'Remember a value';
      case 'if':
        return 'If…';
      case 'choose':
        return 'Pick one at random';
      case 'wait':
        return 'Wait';
      default:
        return 'A step the editor can’t show yet';
    }
  };
  const groups = $derived([...new Set(env.actions.filter((a) => env.more || !a.more).map((a) => a.group))]);

  function add(e: Event) {
    const sel = e.currentTarget as HTMLSelectElement;
    const v = sel.value;
    sel.value = '';
    if (!v) return;
    const [kind, name] = v.split(':');
    let s: StepDef;
    if (kind === 'action') {
      const a = env.actions.find((x) => x.name === name) as ActionInfo;
      s = { action: name, with: {} };
      for (const p of a.params) if (p.type === 'bool' && p.required) s.with[p.name] = true;
    } else if (name === 'wait') s = { wait: '5s' };
    else if (name === 'set') s = { set: { value: '""' } };
    else if (name === 'if') s = { if: '', do: [] };
    else s = { choose: [{ weight: 1, steps: [] }, { weight: 1, steps: [] }] };
    steps = [...steps, s];
  }

  function move(i: number, by: number) {
    const next = [...steps];
    [next[i], next[i + by]] = [next[i + by], next[i]];
    steps = next;
  }

  function renameSet(s: StepDef, from: string, to: string) {
    if (!to || to === from || !/^[a-z][a-z0-9_]{0,63}$/.test(to)) return;
    const next: Record<string, string> = {};
    for (const [k, v] of Object.entries(s.set as Record<string, string>)) next[k === from ? to : k] = v;
    s.set = next;
  }
</script>

{#if steps.length === 0}
  <p class="sg-muted">No steps yet.</p>
{/if}
<ol class="step-list">
  {#each steps as s, i (s)}
    {@const p = `${path}[${i}]`}
    {@const kind = stepKind(s)}
    <li class="sg-panel step-card">
      <div class="step-head">
        <strong>{i + 1}. {title(s)}</strong>
        <span class="step-tools">
          <button type="button" class="button" aria-label={`Move step ${i + 1} up`} disabled={i === 0} onclick={() => move(i, -1)}>↑</button>
          <button type="button" class="button" aria-label={`Move step ${i + 1} down`} disabled={i === steps.length - 1} onclick={() => move(i, 1)}
            >↓</button
          >
          <button type="button" class="button button-danger" aria-label={`Remove step ${i + 1}`} onclick={() => (steps = steps.filter((x) => x !== s))}
            >Remove</button
          >
        </span>
      </div>
      {#if kind === 'action'}
        {@const a = actionOf(s)}
        {#if a}
          <p class="sg-muted field-hint">{a.description}</p>
          {#each a.params as prm (prm.name)}
            <ParamField param={prm} bind:step={steps[i]} />
          {/each}
        {:else}
          <p class="error">There’s no action “{s.action}” in this version of StreamGremlin.</p>
        {/if}
      {:else if kind === 'wait'}
        {#if waitSeconds(s.wait) !== undefined}
          <label class="field">
            <span>Seconds</span>
            <input class="sg-input" type="number" min="0.1" max="3600" step="any" value={waitSeconds(s.wait)}
              onchange={(e) => (s.wait = `${Number(e.currentTarget.value)}s`)} />
          </label>
        {:else}
          <label class="field">
            <span>How long (like 5s or 2m)</span>
            <input class="sg-input" bind:value={s.wait} />
          </label>
        {/if}
      {:else if kind === 'set'}
        <p class="sg-muted field-hint">Kept for the rest of this run only; read it as {'{vars.<name>}'}. To keep it longer, use “Save a variable”.</p>
        {#each Object.keys(s.set) as name (name)}
          {@const asText = exprToText(s.set[name], named)}
          <div class="set-row">
            <label class="field">
              <span>Name</span>
              <input class="sg-input" value={name} spellcheck="false" onchange={(e) => renameSet(s, name, e.currentTarget.value)} />
            </label>
            {#if asText !== undefined}
              <MessageInput label="Value" bind:value={() => asText, (v) => (s.set[name] = textToExpr(v ?? '', named))} />
            {:else}
              <label class="field">
                <span>Value (an expression)</span>
                <input class="sg-input code-input" bind:value={s.set[name]} spellcheck="false" />
              </label>
            {/if}
          </div>
        {/each}
      {:else if kind === 'if'}
        <ConditionInput label="If" optional={false} bind:value={s.if} />
        <div class="nested">
          <h4>Then</h4>
          <StepList bind:steps={s.do} path={`${p}.do`} what="a step to do when it’s true" />
        </div>
        <div class="nested">
          <h4>Otherwise</h4>
          {#if s.else}
            <StepList bind:steps={s.else} path={`${p}.else`} what="a step to do otherwise" />
            {#if s.else.length === 0}
              <button type="button" class="button" onclick={() => delete s.else}>Remove “otherwise”</button>
            {/if}
          {:else}
            <button type="button" class="button" onclick={() => (s.else = [])}>Add “otherwise”</button>
          {/if}
        </div>
      {:else if kind === 'choose'}
        {#each s.choose as b, bi (b)}
          <div class="nested">
            <div class="step-head">
              <h4>Choice {bi + 1}</h4>
              <label class="choice-inline">
                Chances
                <input class="sg-input weight" type="number" min="1" max="1000000" bind:value={b.weight} />
              </label>
              {#if s.choose.length > 1}
                <button type="button" class="button" aria-label={`Remove choice ${bi + 1}`} onclick={() => (s.choose = s.choose.filter((x: unknown) => x !== b))}
                  >Remove</button
                >
              {/if}
            </div>
            <StepList bind:steps={b.steps} path={`${p}.choose[${bi}].steps`} what={`a step for choice ${bi + 1}`} />
          </div>
        {/each}
        <button type="button" class="button" onclick={() => (s.choose = [...s.choose, { weight: 1, steps: [] }])}>Add a choice</button>
      {:else}
        <p class="sg-muted">It’s kept as it is. Change it on the JSON tab.</p>
        <pre class="payload">{JSON.stringify(s, null, 2)}</pre>
      {/if}
      {#if kind === 'action' || kind === 'set' || kind === 'wait'}
        <details class="more-options">
          <summary>More options</summary>
          <ConditionInput bind:value={s.if} />
          <label class="choice-inline">
            <input type="checkbox" checked={s.on_error === 'continue'} onchange={(e) => (e.currentTarget.checked ? (s.on_error = 'continue') : delete s.on_error)} />
            If it fails, carry on with the next step
          </label>
          <label class="field">
            <span>Step name (to use its result later, as {'{steps.<name>.output…}'})</span>
            <input class="sg-input" value={s.id ?? ''} spellcheck="false"
              onchange={(e) => (e.currentTarget.value ? (s.id = e.currentTarget.value) : delete s.id)} />
          </label>
        </details>
      {/if}
      {#each ownProblems(env.problems, p) as pr (pr.path + pr.message)}
        <p class="error">{pr.message} <span class="sg-muted">({pr.path})</span></p>
      {/each}
    </li>
  {/each}
</ol>
<div class="add-step">
  <select class="sg-input" onchange={add} aria-label={`Add ${what}`}>
    <option value="">Add {what}…</option>
    {#each groups as g (g)}
      <optgroup label={g}>
        {#each env.actions.filter((a) => a.group === g && (env.more || !a.more)) as a (a.name)}
          <option value={`action:${a.name}`}>{a.label}</option>
        {/each}
      </optgroup>
    {/each}
    <optgroup label="Flow">
      <option value="flow:wait">Wait</option>
      <option value="flow:set">Remember a value</option>
      <option value="flow:if">If… otherwise…</option>
      <option value="flow:choose">Pick one at random</option>
    </optgroup>
  </select>
</div>
