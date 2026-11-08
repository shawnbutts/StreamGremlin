<script lang="ts">
  // A condition in words: a value, a comparison and a text. Conditions the
  // editor didn't write are shown and edited as expressions, unchanged.
  import { buildCondition, ops, parseCondition, type Condition, type Op } from '../lib/editor';
  import { allValues, editorEnv } from '../lib/editorEnv';

  let {
    value = $bindable(),
    label = 'Only if',
    optional = true,
  }: { value: string | undefined; label?: string; optional?: boolean } = $props();

  const env = editorEnv();
  const named = $derived(allValues(env));
  let raw = $state(false);
  let cond = $state<Condition>({ value: '', op: 'is', text: '' });
  let hint = $state('');
  let touched = false;

  // Until it's changed here, follow the stored condition: the values the
  // trigger offers can arrive after the field is shown.
  $effect(() => {
    const parsed = parseCondition(value ?? '', named);
    if (touched) return;
    raw = !!value && !parsed;
    if (parsed) cond = parsed;
  });

  function changed() {
    touched = true;
    if (!cond.value) {
      hint = 'Pick a value to compare.';
      return;
    }
    const src = buildCondition(cond, named);
    if (!src) {
      hint = cond.op === 'more' || cond.op === 'less' ? 'Type a number to compare with.' : 'Pick a value to compare.';
      return;
    }
    hint = '';
    value = src;
  }
</script>

<fieldset class="condition">
  <legend>{label}</legend>
  {#if raw}
    <label class="field">
      <span>Condition (an expression)</span>
      <input class="sg-input code-input" bind:value spellcheck="false" />
    </label>
    <span class="sg-muted field-hint">Written as an expression, like <code>event.payload.chatter.is_vip</code>.</span>
  {:else}
    <div class="condition-row">
      <select class="sg-input" aria-label="Value" bind:value={cond.value} onchange={changed}>
        <option value="">Choose a value…</option>
        {#each named as v (v.token)}<option value={`{${v.token}}`}>{v.label}</option>{/each}
        {#if cond.value && !named.some((v) => `{${v.token}}` === cond.value)}<option value={cond.value}>{cond.value}</option>{/if}
      </select>
      <select class="sg-input" aria-label="Comparison" bind:value={cond.op} onchange={changed}>
        {#each ops as o (o.id)}<option value={o.id as Op}>{o.label}</option>{/each}
      </select>
      <input class="sg-input" aria-label="Compared with" bind:value={cond.text} oninput={changed} />
    </div>
    {#if hint}<p class="sg-muted field-hint" role="status">{hint}</p>{/if}
  {/if}
  <div class="row-actions">
    {#if !raw}
      <button type="button" class="button" onclick={() => { touched = true; raw = true; }}>Write it as an expression</button>
    {/if}
    {#if value && optional}
      <button type="button" class="button" onclick={() => { touched = true; value = undefined; cond = { value: '', op: 'is', text: '' }; raw = false; hint = ''; }}
        >Remove the condition</button
      >
    {/if}
  </div>
</fieldset>
