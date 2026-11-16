<script lang="ts">
  // Variables: what workflows have saved between runs, in three scopes:
  // global (every workflow), per viewer, and per workflow. Each can be
  // changed or deleted by hand; global and workflow ones can be added.
  import { onMount } from 'svelte';
  import {
    ApiError,
    deleteVariable,
    getVariables,
    getWorkflows,
    putVariable,
    type VarKey,
    type VarScope,
    type Variable,
    type WorkflowSummary,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';
  import { kindOf, parseValue, showValue, type ValueKind } from '../lib/variables';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  const NAME = /^[a-z][a-z0-9_]{0,63}$/;
  const scopes: { id: VarScope | ''; label: string }[] = [
    { id: '', label: 'All' },
    { id: 'global', label: 'Global' },
    { id: 'viewer', label: 'Per viewer' },
    { id: 'workflow', label: 'Per workflow' },
  ];
  const kinds: { id: ValueKind; label: string }[] = [
    { id: 'number', label: 'Number' },
    { id: 'text', label: 'Text' },
    { id: 'bool', label: 'True or false' },
    { id: 'json', label: 'List or map (JSON)' },
  ];

  let list = $state<Variable[]>([]);
  let workflows = $state<WorkflowSummary[]>([]);
  let scope = $state<VarScope | ''>('');
  let query = $state('');
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);

  // The variable being changed, keyed as scope/owner/name, and its form.
  let editing = $state('');
  let editKind = $state<ValueKind>('number');
  let editText = $state('');
  let formError = $state('');

  let adding = $state(false);
  let addWhere = $state('global'); // 'global' or a workflow id
  let addName = $state('');
  let addKind = $state<ValueKind>('number');
  let addText = $state('');

  let deleting = $state<Variable | undefined>();

  const keyOf = (v: VarKey) => `${v.scope}/${v.owner}/${v.name}`;
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const workflowName = (id: string) => workflows.find((w) => w.id === id)?.name ?? id;
  const whose = (v: Variable) =>
    v.scope === 'global' ? 'Global' : v.scope === 'viewer' ? `Viewer: ${v.owner_label || v.owner}` : `Workflow: ${workflowName(v.owner)}`;
  const by = (v: Variable) =>
    v.updated_by.startsWith('workflow:') ? workflowName(v.updated_by.slice(9)) : v.updated_by.startsWith('stream_deck:') ? 'your Stream Deck' : v.updated_by;

  async function load() {
    try {
      list = (await getVariables({ scope: scope || undefined, q: query.trim() || undefined })).variables;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    getWorkflows()
      .then((w) => (workflows = w.workflows))
      .catch(() => undefined);
    const t = setInterval(load, refreshMs);
    return () => clearInterval(t);
  });

  function startEdit(v: Variable) {
    editing = keyOf(v);
    editKind = kindOf(v.value);
    editText = editKind === 'json' ? JSON.stringify(v.value) : String(v.value);
    formError = '';
  }

  async function save(k: VarKey, kind: ValueKind, text: string): Promise<boolean> {
    const parsed = parseValue(kind, text);
    if (!parsed.ok) {
      formError = parsed.error;
      return false;
    }
    busy = true;
    error = '';
    notice = '';
    formError = '';
    try {
      await putVariable(k, parsed.value);
      notice = `Saved ${k.name}.`;
      return true;
    } catch (e) {
      formError = message(e);
      return false;
    } finally {
      busy = false;
      await load();
    }
  }

  async function saveEdit(e: SubmitEvent, v: Variable) {
    e.preventDefault();
    if (await save(v, editKind, editText)) editing = '';
  }

  async function saveNew(e: SubmitEvent) {
    e.preventDefault();
    const name = addName.trim();
    if (!NAME.test(name)) {
      formError = 'A name is lowercase letters, digits and _, starting with a letter.';
      return;
    }
    const k: VarKey = addWhere === 'global' ? { scope: 'global', owner: '', name } : { scope: 'workflow', owner: addWhere, name };
    if (list.some((v) => keyOf(v) === keyOf(k))) {
      formError = `${name} already exists there; change it in the list instead.`;
      return;
    }
    if (await save(k, addKind, addText)) {
      adding = false;
      addName = '';
      addText = '';
    }
  }

  async function remove(v: Variable) {
    deleting = undefined;
    busy = true;
    error = '';
    notice = '';
    let failed = '';
    try {
      await deleteVariable(v);
    } catch (e) {
      failed = `Couldn't delete ${v.name}: ${message(e)}`;
    }
    busy = false;
    await load();
    if (failed) error = failed;
    else notice = `Deleted ${v.name}.`;
  }
</script>

<h1>Variables</h1>

<section class="sg-panel card" aria-labelledby="vars-heading" aria-busy={busy}>
  <h2 id="vars-heading">Saved variables</h2>
  <p class="sg-muted">
    Values your workflows keep between runs and restarts, such as a death counter or each viewer’s lurk count. Workflows
    save them with “Save a variable” and “Add to a saved number”, and read them as <code>saved.global.deaths</code>,
    <code>saved.viewer.lurks</code> or <code>saved.workflow.last</code>. Test events never change them.
  </p>
  <div class="var-filters">
    <div class="segmented" role="group" aria-label="Show">
      {#each scopes as s (s.id)}
        <button type="button" class="button" aria-pressed={scope === s.id} onclick={() => { scope = s.id; load(); }}>{s.label}</button>
      {/each}
    </div>
    <label class="field var-search">
      <span>Find</span>
      <input class="sg-input" type="search" placeholder="A name or a viewer" bind:value={query} oninput={load} />
    </label>
  </div>

  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if list.length === 0}
    <p class="sg-muted">
      {query || scope ? 'None match.' : 'Nothing saved yet. Try the Death counter or Lurk example workflow, or add one below.'}
    </p>
  {/if}

  <ul class="secret-list" aria-label="Variables">
    {#each list as v (keyOf(v))}
      <li class="secret-row">
        <span class="secret-info">
          <strong><code>{v.name}</code></strong>
          <span class="tag">{whose(v)}</span>
          {#if editing !== keyOf(v)}
            <span class="secret-saved var-value">{showValue(v.value)}</span>
          {/if}
          <span class="sg-muted secret-saved">Changed {eventTime(v.updated_at)} by {by(v)}</span>
        </span>
        {#if editing === keyOf(v)}
          <form class="var-form" onsubmit={(e) => saveEdit(e, v)} aria-label={`Change ${v.name}`}>
            <label class="field">
              <span>Kind</span>
              <select class="sg-input" bind:value={editKind} onchange={() => { if (editKind === 'bool' && editText !== 'false') editText = 'true'; }}>
                {#each kinds as k (k.id)}<option value={k.id}>{k.label}</option>{/each}
              </select>
            </label>
            <label class="field">
              <span>Value</span>
              {#if editKind === 'bool'}
                <select class="sg-input" bind:value={editText}>
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              {:else}
                <input class="sg-input" bind:value={editText} spellcheck="false" />
              {/if}
            </label>
            {#if formError}<p class="error" role="alert">{formError}</p>{/if}
            <div class="row-actions">
              <button type="submit" class="sg-button" disabled={busy}>Save</button>
              <button type="button" class="button" onclick={() => (editing = '')}>Cancel</button>
            </div>
          </form>
        {:else}
          <button type="button" class="button" disabled={busy} onclick={() => startEdit(v)}>Change</button>
          <button type="button" class="button button-danger" disabled={busy} onclick={() => (deleting = v)}>Delete</button>
        {/if}
      </li>
    {/each}
  </ul>

  <div class="row-actions">
    {#if !adding}
      <button type="button" class="sg-button" onclick={() => { adding = true; editing = ''; formError = ''; }}>Add a variable</button>
    {/if}
  </div>
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>

{#if adding}
  <section class="sg-panel card" aria-labelledby="add-var-heading">
    <h2 id="add-var-heading">Add a variable</h2>
    <p class="sg-muted">Viewer variables appear when a workflow saves one for a viewer; you can change them here after that.</p>
    <form class="test-form" onsubmit={saveNew}>
      <label class="field">
        <span>Kept for</span>
        <select class="sg-input" bind:value={addWhere}>
          <option value="global">Every workflow (global)</option>
          {#each workflows as w (w.id)}<option value={w.id}>Only “{w.name}”</option>{/each}
        </select>
      </label>
      <label class="field">
        <span>Name</span>
        <input class="sg-input" bind:value={addName} spellcheck="false" autocomplete="off" aria-describedby="var-name-hint" />
        <span id="var-name-hint" class="sg-muted field-hint">Lowercase letters, digits and _, like <code>deaths</code> or <code>giveaway_open</code>.</span>
      </label>
      <label class="field">
        <span>Kind</span>
        <select class="sg-input" bind:value={addKind} onchange={() => { if (addKind === 'bool' && addText !== 'false') addText = 'true'; }}>
          {#each kinds as k (k.id)}<option value={k.id}>{k.label}</option>{/each}
        </select>
      </label>
      <label class="field">
        <span>Value</span>
        {#if addKind === 'bool'}
          <select class="sg-input" bind:value={addText}>
            <option value="true">true</option>
            <option value="false">false</option>
          </select>
        {:else}
          <input class="sg-input" bind:value={addText} spellcheck="false" />
        {/if}
      </label>
      {#if formError && !editing}<p class="error" role="alert">{formError}</p>{/if}
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={busy || !addName.trim()}>Save</button>
        <button type="button" class="button" onclick={() => { adding = false; formError = ''; }}>Cancel</button>
      </div>
    </form>
  </section>
{/if}

{#if deleting}
  {@const v = deleting}
  <ConfirmDialog
    title={`Delete ${v.name}?`}
    message={`${whose(v)}: ${showValue(v.value)}. Workflows that read it will see it as missing until one saves it again.`}
    confirmLabel="Delete"
    onconfirm={() => remove(v)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
