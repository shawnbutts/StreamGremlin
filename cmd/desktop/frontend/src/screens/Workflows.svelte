<script lang="ts">
  // Workflows: what runs when, switched on or off, tested with a test event,
  // edited in plain words, deleted and restored, and every run's trace.
  import { onMount } from 'svelte';
  import {
    ApiError,
    deleteWorkflow,
    getDeletedWorkflows,
    getEventCatalog,
    getJobs,
    getWorkflow,
    getWorkflows,
    restoreWorkflow,
    setWorkflowEnabled,
    simulateEvent,
    type EventKind,
    type Job,
    type WorkflowSummary,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import type { Def } from '../lib/editor';
  import { eventTime } from '../lib/events';
  import { describeTrigger, jobOk, jobWords, testFields } from '../lib/workflows';
  import WorkflowEditor from '../editor/WorkflowEditor.svelte';
  import RunTrace from './RunTrace.svelte';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  let workflows = $state<WorkflowSummary[]>([]);
  let deleted = $state<WorkflowSummary[]>([]);
  let kinds = $state<EventKind[]>([]);
  let jobs = $state<Job[]>([]);
  let shown = $state(''); // workflow whose runs are listed ('' = all)
  let openJob = $state('');
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);

  // The editor: closed, a new workflow, or an existing one's definition.
  let editing = $state<{ def?: Def } | undefined>();
  let deleting = $state<WorkflowSummary | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  async function load() {
    try {
      workflows = (await getWorkflows()).workflows;
      deleted = (await getDeletedWorkflows()).workflows;
      jobs = (await getJobs(shown || undefined)).jobs;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    getEventCatalog()
      .then((c) => (kinds = c.kinds))
      .catch(() => undefined);
    const t = setInterval(load, refreshMs);
    return () => clearInterval(t);
  });

  async function act(f: () => Promise<unknown>, done: string) {
    busy = true;
    error = '';
    notice = '';
    try {
      await f();
      notice = done;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  function saved(w: WorkflowSummary, changed: boolean) {
    editing = undefined;
    error = '';
    notice = changed
      ? `Saved “${w.name}” as version ${w.version}.${w.enabled ? '' : ' It’s off until you turn it on.'}`
      : `“${w.name}” is unchanged.`;
    load();
  }

  async function edit(w: WorkflowSummary) {
    error = '';
    try {
      editing = { def: (await getWorkflow(w.id)).definition };
    } catch (e) {
      error = `Couldn’t open “${w.name}”: ${message(e)}`;
    }
  }

  function remove(w: WorkflowSummary) {
    deleting = undefined;
    act(() => deleteWorkflow(w.id), `Deleted “${w.name}”. You can restore it under “Deleted workflows”.`);
  }

  function test(w: WorkflowSummary) {
    const t = w.triggers.find((t) => testFields(t, kinds.find((k) => k.type === t.type)));
    if (!t) return;
    const fields = testFields(t, kinds.find((k) => k.type === t.type))!;
    act(() => simulateEvent(t.type, fields, crypto.randomUUID()), `Sent a test event. “${w.name}” runs as a test: nothing reaches Twitch or OBS, and saved variables don’t change.`);
  }

  const testable = (w: WorkflowSummary) => w.triggers.some((t) => testFields(t, kinds.find((k) => k.type === t.type)));
  const canTest = (w: WorkflowSummary) => w.enabled && testable(w);
  /** Why Test can't be used, said on screen rather than in a hover tip. */
  const whyNoTest = (w: WorkflowSummary) =>
    !w.enabled ? 'Turn it on to test it.' : !testable(w) ? 'Its trigger can’t be imitated by a test event yet.' : '';

  const taken = $derived([...workflows, ...deleted].map((w) => w.id));
</script>

<h1>Workflows</h1>

{#if editing}
  <WorkflowEditor initial={editing.def} {taken} {workflows} onsaved={saved} oncancel={() => (editing = undefined)} />
{:else}
<section class="sg-panel card" aria-labelledby="wf-heading" aria-busy={busy}>
  <h2 id="wf-heading">Your workflows</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if workflows.length === 0}
    <p class="sg-muted">No workflows yet. A workflow does something when something happens, like answering <code>!hello</code> in chat.</p>
  {/if}
  <ul class="secret-list" aria-label="Workflows">
    {#each workflows as w (w.id)}
      <li class="secret-row">
        <span class="secret-info">
          <strong>{w.name}</strong>
          <span class="sg-muted secret-saved">
            When: {w.triggers.map((t) => describeTrigger(t, kinds)).join('; or ')}
          </span>
          {#if whyNoTest(w)}
            <span class="sg-muted secret-saved">{whyNoTest(w)}</span>
          {/if}
          {#if w.last_job}
            <span class="secret-check">
              <span class={jobOk(w.last_job) ? 'icon-ok' : 'icon-bad'} aria-hidden="true">{jobOk(w.last_job) ? '✓' : '✗'}</span>
              Last run: {jobWords(w.last_job)}{w.last_job.dry_run ? ' (test)' : ''}, {eventTime(w.last_job.created_at)}
            </span>
          {/if}
        </span>
        <label class="choice-inline">
          <input
            type="checkbox"
            role="switch"
            checked={w.enabled}
            disabled={busy}
            onchange={(e) => act(() => setWorkflowEnabled(w.id, e.currentTarget.checked), `“${w.name}” is ${e.currentTarget.checked ? 'on' : 'off'}.`)}
          />
          On
        </label>
        <button type="button" class="button" disabled={busy || !canTest(w)} onclick={() => test(w)}>Test</button>
        <button type="button" class="button" aria-label={`Edit ${w.name}`} onclick={() => edit(w)}>Edit</button>
        <button type="button" class="button" aria-pressed={shown === w.id} onclick={() => { shown = shown === w.id ? '' : w.id; load(); }}
          >Runs</button
        >
        <button type="button" class="button button-danger" aria-label={`Delete ${w.name}`} disabled={busy} onclick={() => (deleting = w)}>Delete</button>
      </li>
    {/each}
  </ul>
  <div class="row-actions">
    <button type="button" class="sg-button" onclick={() => { notice = ''; editing = {}; }}>New workflow</button>
  </div>
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
  {#if deleted.length}
    <details class="deleted">
      <summary>Deleted workflows ({deleted.length})</summary>
      <ul class="secret-list" aria-label="Deleted workflows">
        {#each deleted as w (w.id)}
          <li class="secret-row">
            <span class="secret-info">
              <strong>{w.name}</strong>
              <span class="sg-muted secret-saved">Deleted {w.deleted_at ? eventTime(w.deleted_at) : ''}. Its runs and its own saved variables are kept.</span>
            </span>
            <button type="button" class="button" aria-label={`Restore ${w.name}`} disabled={busy}
              onclick={() => act(() => restoreWorkflow(w.id), `Restored “${w.name}”. It’s off until you turn it on.`)}>Restore</button
            >
          </li>
        {/each}
      </ul>
    </details>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="runs-heading">
  <h2 id="runs-heading">{shown ? `Runs of ${workflows.find((w) => w.id === shown)?.name ?? shown}` : 'Recent runs'}</h2>
  {#if jobs.length === 0}
    <p class="sg-muted">No runs yet.</p>
  {/if}
  <ul class="secret-list" aria-label="Runs">
    {#each jobs as j (j.id)}
      <li class="secret-row">
        <span class="secret-info">
          <span class={jobOk(j) ? 'icon-ok' : 'icon-bad'} aria-hidden="true">{jobOk(j) ? '✓' : '✗'}</span>
          <strong>{jobWords(j)}</strong>
          {#if j.dry_run}<span class="tag">Test</span>{/if}
          <span class="sg-muted">{workflows.find((w) => w.id === j.workflow_id)?.name ?? j.workflow_id}, {eventTime(j.created_at)}</span>
          {#if j.error || j.reason}
            <span class="secret-saved">{j.error || j.reason}</span>
          {/if}
        </span>
        <button type="button" class="button" aria-expanded={openJob === j.id} onclick={() => (openJob = openJob === j.id ? '' : j.id)}
          >Trace</button
        >
        {#if openJob === j.id}
          <RunTrace id={j.id} onchange={load} />
        {/if}
      </li>
    {/each}
  </ul>
</section>
{/if}

{#if deleting}
  {@const w = deleting}
  <ConfirmDialog
    title={`Delete “${w.name}”?`}
    message="It turns off and leaves this list. Waiting runs are cancelled and a running one is stopped. Its runs and its own saved variables are kept, and you can restore it."
    confirmLabel="Delete"
    onconfirm={() => remove(w)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
