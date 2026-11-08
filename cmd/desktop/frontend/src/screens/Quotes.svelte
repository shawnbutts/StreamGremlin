<script lang="ts">
  // Quotes: numbered sayings from the stream (docs/adr/0014-quotes.md).
  // Viewers use them through the !quote, !quoteadd and !quotedel workflows,
  // which this screen can add from the templates in one step.
  import { onMount } from 'svelte';
  import {
    ApiError,
    addQuote,
    deleteQuote,
    editQuote,
    getDeletedWorkflows,
    getQuotes,
    getTemplates,
    getWorkflows,
    saveWorkflow,
    setWorkflowEnabled,
    type Quote,
    type QuoteInput,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  let { refreshMs = 3000 }: { refreshMs?: number } = $props();

  /** The workflows the quote templates make. */
  const COMMAND_IDS = ['chat.quote', 'chat.quoteadd', 'chat.quotedel'];

  let quotes = $state<Quote[]>([]);
  let count = $state(0);
  let next = $state(1);
  let query = $state('');
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  // Whether the chat commands exist: undefined until known.
  let commandsMade = $state<boolean | undefined>();

  let adding = $state<QuoteInput>({ text: '', added_by: '', category: '' });
  let addKey = crypto.randomUUID(); // the same while one add is retried
  let editing = $state<number | undefined>();
  let edit = $state<QuoteInput>({ text: '', added_by: '', category: '' });
  let deleting = $state<Quote | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const about = (q: Quote) =>
    [q.added_by ? `added by ${q.added_by}` : '', q.category ?? '', eventTime(q.created_at)].filter(Boolean).join(', ');

  async function load() {
    try {
      const r = await getQuotes(query.trim() || undefined);
      quotes = r.quotes;
      count = r.count;
      next = r.next;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  async function checkCommands() {
    try {
      const ids = [...(await getWorkflows()).workflows, ...(await getDeletedWorkflows()).workflows].map((w) => w.id);
      commandsMade = COMMAND_IDS.some((id) => ids.includes(id));
    } catch {
      commandsMade = undefined;
    }
  }

  onMount(() => {
    load();
    checkCommands();
    const id = setInterval(load, refreshMs);
    return () => clearInterval(id);
  });

  async function act(f: () => Promise<unknown>, done: string) {
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

  async function add(e: SubmitEvent) {
    e.preventDefault();
    const q = { ...adding, added_by: adding.added_by.trim() || 'you' };
    let saved: Quote | undefined;
    if (await act(async () => (saved = await addQuote(q, addKey)), '')) {
      notice = `Added quote #${saved!.id}.`;
      adding = { text: '', added_by: '', category: '' };
      addKey = crypto.randomUUID();
    }
  }

  function startEdit(q: Quote) {
    notice = '';
    editing = q.id;
    edit = { text: q.text, added_by: q.added_by, category: q.category ?? '' };
  }

  async function saveEdit(e: SubmitEvent, id: number) {
    e.preventDefault();
    if (await act(() => editQuote(id, edit), `Saved quote #${id}.`)) editing = undefined;
  }

  function remove(q: Quote) {
    deleting = undefined;
    act(() => deleteQuote(q.id), `Deleted quote #${q.id}. Later quotes keep their numbers.`);
  }

  // Saves the three quote templates as workflows and turns them on.
  async function makeCommands() {
    busy = true;
    error = '';
    notice = '';
    const problems: string[] = [];
    try {
      const all = (await getTemplates()).templates.filter((t) => t.category === 'Quotes');
      for (const t of all) {
        try {
          const { workflow } = await saveWorkflow(JSON.stringify(t.workflow));
          await setWorkflowEnabled(workflow.id, true);
        } catch (e) {
          problems.push(`${t.title}: ${message(e)}`);
        }
      }
      if (problems.length) error = `Some commands weren’t set up. ${problems.join(' ')}`;
      else notice = 'Added !quote (anyone), !quoteadd and !quotedel (mods and you), and turned them on. Change them on the Workflows screen.';
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await checkCommands();
    }
  }
</script>

<h1>Quotes</h1>
<p class="sg-muted screen-intro">
  Numbered quotes from your streams. Viewers see them with <code>!quote</code> (at random) or <code>!quote 5</code>, and mods add them
  with <code>!quoteadd</code>. Deleting one never renumbers the rest.
</p>

{#if commandsMade === false}
  <section class="sg-panel card" aria-labelledby="quote-commands-heading">
    <h2 id="quote-commands-heading">Chat commands</h2>
    <p>
      Nothing answers <code>!quote</code> yet. Add three ready-made workflows: <code>!quote</code> for anyone, and
      <code>!quoteadd</code> and <code>!quotedel</code> for mods and you.
    </p>
    <div class="row-actions">
      <button type="button" class="sg-button" disabled={busy} onclick={makeCommands}>Add the quote commands</button>
    </div>
  </section>
{/if}

<section class="sg-panel card" aria-labelledby="quotes-heading" aria-busy={busy}>
  <h2 id="quotes-heading">Your quotes</h2>
  <label class="field var-search">
    <span>Find</span>
    <input class="sg-input" type="search" placeholder="Words, who added it, or a number" bind:value={query} oninput={load} />
  </label>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if quotes.length === 0}
    <p class="sg-muted">{query ? 'None match.' : 'No quotes yet.'}</p>
  {:else}
    <p class="sg-muted">{count} {count === 1 ? 'quote' : 'quotes'}{query ? `; ${quotes.length} match` : ''}.</p>
  {/if}
  <ul class="secret-list" aria-label="Quotes">
    {#each quotes as q (q.id)}
      <li class="secret-row">
        {#if editing === q.id}
          <form class="test-form quote-edit" onsubmit={(e) => saveEdit(e, q.id)}>
            <label class="field">
              <span>Quote #{q.id}</span>
              <textarea class="sg-input" rows="2" maxlength="500" bind:value={edit.text}></textarea>
            </label>
            <label class="field"><span>Added by</span><input class="sg-input" maxlength="100" bind:value={edit.added_by} /></label>
            <label class="field"><span>Category</span><input class="sg-input" maxlength="140" bind:value={edit.category} /></label>
            <div class="row-actions">
              <button type="submit" class="sg-button" disabled={busy}>Save</button>
              <button type="button" class="button" onclick={() => (editing = undefined)}>Cancel</button>
            </div>
          </form>
        {:else}
          <span class="secret-info">
            <strong>#{q.id}</strong> {q.text}
            <span class="sg-muted secret-saved">{about(q)}</span>
          </span>
          <button type="button" class="button" aria-label={`Edit quote ${q.id}`} onclick={() => startEdit(q)}>Edit</button>
          <button type="button" class="button button-danger" aria-label={`Delete quote ${q.id}`} disabled={busy} onclick={() => (deleting = q)}>Delete</button>
        {/if}
      </li>
    {/each}
  </ul>
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<section class="sg-panel card" aria-labelledby="quote-add-heading">
  <h2 id="quote-add-heading">Add a quote</h2>
  <form class="test-form" onsubmit={add}>
    <label class="field">
      <span>Quote #{next}</span>
      <textarea class="sg-input" rows="2" maxlength="500" bind:value={adding.text} placeholder="I meant to do that"></textarea>
    </label>
    <label class="field"><span>Added by (optional)</span><input class="sg-input" maxlength="100" bind:value={adding.added_by} placeholder="you" /></label>
    <label class="field">
      <span>Category (optional)</span>
      <input class="sg-input" maxlength="140" bind:value={adding.category} placeholder="What you were playing" />
    </label>
    <div class="row-actions">
      <button type="submit" class="sg-button" disabled={busy || !adding.text.trim()}>Add</button>
    </div>
  </form>
</section>

{#if deleting}
  {@const q = deleting}
  <ConfirmDialog
    title={`Delete quote #${q.id}?`}
    message={`“${q.text}” is deleted for good. Later quotes keep their numbers, and #${q.id} isn’t used again.`}
    confirmLabel="Delete"
    onconfirm={() => remove(q)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
