<script lang="ts">
  // The template gallery: ready-made workflows to start from. Choosing one
  // opens it in the editor, turned off; nothing is saved until you save.
  import { onMount } from 'svelte';
  import { ApiError, getCommands, getTemplates, type CommandEntry, type Template, type WorkflowSummary } from '../lib/api';
  import { takenBy } from '../lib/workflows';

  let {
    workflows = [],
    onpick,
    oncancel,
  }: { workflows?: WorkflowSummary[]; onpick: (t: Template | undefined) => void; oncancel: () => void } = $props();

  let list = $state<Template[]>([]);
  let categories = $state<string[]>([]);
  let commands = $state<CommandEntry[]>([]);
  let error = $state('');
  let loading = $state(true);

  onMount(async () => {
    try {
      const r = await getTemplates();
      list = r.templates;
      categories = r.categories;
    } catch (e) {
      error = `Couldn’t load the templates: ${e instanceof ApiError ? e.message : String(e)}`;
    }
    loading = false;
    getCommands()
      .then((r) => (commands = r.commands))
      .catch(() => undefined);
  });

  /** Why using it might need a change: you have it already, or its command is taken. */
  function note(t: Template): string {
    const w = t.workflow as { id?: string; name?: string; triggers?: { where?: Record<string, unknown>; sources?: string[] }[] };
    if (workflows.some((x) => x.id === w.id)) return `You already have “${workflows.find((x) => x.id === w.id)?.name}”. This makes a second copy.`;
    for (const tr of w.triggers ?? []) {
      const cmd = tr.where?.command;
      if (typeof cmd !== 'string') continue;
      const by = takenBy(cmd, tr.where?.['chatter.role'], tr.sources, commands, '');
      if (by.length) return `!${cmd} is already answered by ${by.map((n) => `“${n}”`).join(' and ')}: change the command, or turn that off before turning this on.`;
    }
    return '';
  }
</script>

<section class="sg-panel card" aria-labelledby="gallery-heading">
  <h2 id="gallery-heading">New workflow</h2>
  <p class="sg-muted">Start from a template, or from scratch. A template opens in the editor, turned off: change anything, then save.</p>
  <div class="row-actions">
    <button type="button" class="sg-button" onclick={() => onpick(undefined)}>Start from scratch</button>
    <button type="button" class="button" onclick={oncancel}>Cancel</button>
  </div>
  {#if loading}
    <p class="sg-muted">Loading…</p>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#each categories as c (c)}
    {@const items = list.filter((t) => t.category === c)}
    {#if items.length}
      <h3>{c}</h3>
      <ul class="gallery" aria-label={c}>
        {#each items as t (t.id)}
          <li class="sg-panel gallery-card">
            <strong>{t.title}</strong>
            <span class="sg-muted">{t.description}</span>
            {#if (t.workflow as { group?: string }).group}
              <span class="sg-muted">Goes in the {(t.workflow as { group?: string }).group} group.</span>
            {/if}
            {#if t.blanks.length}
              <span class="tag">{t.blanks.length === 1 ? '1 thing to fill in' : `${t.blanks.length} things to fill in`}</span>
            {/if}
            {#if note(t)}<span class="field-hint taken">{note(t)}</span>{/if}
            <button type="button" class="button" aria-label={`Use ${t.title}`} onclick={() => onpick(t)}>Use this</button>
          </li>
        {/each}
      </ul>
    {/if}
  {/each}
</section>
