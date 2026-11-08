<script lang="ts">
  // Text that can include values: shown as {viewer}, stored as
  // ${event.actor.display_name}. "Insert a value" adds one at the cursor.
  import { toDisplay, toTemplate } from '../lib/editor';
  import { allValues, editorEnv } from '../lib/editorEnv';

  let {
    label,
    value = $bindable(),
    multiline = false,
    hint = '',
  }: { label: string; value: string | undefined; multiline?: boolean; hint?: string } = $props();

  const env = editorEnv();
  const named = $derived(allValues(env));
  let text = $state('');
  let typed = false;
  let field = $state<HTMLInputElement | HTMLTextAreaElement>();

  // Until the streamer types, follow the stored value: the values the
  // trigger offers can arrive after the field is shown.
  $effect(() => {
    const shown = toDisplay(value ?? '', named);
    if (!typed) text = shown;
  });

  function changed() {
    typed = true;
    value = toTemplate(text, named);
  }

  function insert(e: Event) {
    const sel = e.currentTarget as HTMLSelectElement;
    const tok = sel.value;
    sel.value = '';
    if (!tok) return;
    const at = field?.selectionStart ?? text.length;
    text = text.slice(0, at) + `{${tok}}` + text.slice(at);
    changed();
    field?.focus();
  }
</script>

<div class="field message-field">
  <label class="field">
    <span>{label}</span>
    {#if multiline}
      <textarea class="sg-input" rows="2" bind:this={field} bind:value={text} oninput={changed}></textarea>
    {:else}
      <input class="sg-input" bind:this={field} bind:value={text} oninput={changed} />
    {/if}
  </label>
  {#if named.length}
    <select class="sg-input insert" aria-label={`Insert a value into ${label}`} onchange={insert}>
      <option value="">Insert a value…</option>
      {#each named as v (v.token)}<option value={v.token}>{v.label} {`{${v.token}}`}</option>{/each}
    </select>
  {/if}
  {#if hint}<span class="sg-muted field-hint">{hint}</span>{/if}
</div>
