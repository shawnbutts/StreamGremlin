<script lang="ts">
  // Workflows: what runs when, switched on or off, tested with a test event,
  // and every run's trace. New workflows are pasted as JSON for now; the
  // plain-words editor comes in slice 9.
  import { onMount } from 'svelte';
  import {
    ApiError,
    ProblemsError,
    getActions,
    getEventCatalog,
    getJobs,
    getWorkflows,
    saveWorkflow,
    setWorkflowEnabled,
    simulateEvent,
    type ActionInfo,
    type EventKind,
    type Job,
    type Problem,
    type WorkflowSummary,
  } from '../lib/api';
  import { eventTime } from '../lib/events';
  import { describeTrigger, example, jobOk, jobWords, testFields } from '../lib/workflows';
  import RunTrace from './RunTrace.svelte';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  let workflows = $state<WorkflowSummary[]>([]);
  let kinds = $state<EventKind[]>([]);
  let actions = $state<ActionInfo[]>([]);
  let jobs = $state<Job[]>([]);
  let shown = $state(''); // workflow whose runs are listed ('' = all)
  let openJob = $state('');
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);

  let adding = $state(false);
  let text = $state('');
  let problems = $state<Problem[]>([]);

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  async function load() {
    try {
      workflows = (await getWorkflows()).workflows;
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
    getActions()
      .then((a) => (actions = a.actions))
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

  async function save(e: SubmitEvent) {
    e.preventDefault();
    problems = [];
    busy = true;
    error = '';
    notice = '';
    try {
      const r = await saveWorkflow(text);
      notice = r.changed
        ? `Saved “${r.workflow.name}” as version ${r.workflow.version}.${r.workflow.enabled ? '' : ' It’s off until you turn it on.'}`
        : `“${r.workflow.name}” is unchanged.`;
      adding = false;
      text = '';
    } catch (err) {
      if (err instanceof ProblemsError) problems = err.problems;
      else error = message(err);
    } finally {
      busy = false;
      await load();
    }
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

  const groups = $derived([...new Set(actions.map((a) => a.group))]);
</script>

<h1>Workflows</h1>

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
        <button type="button" class="button" aria-pressed={shown === w.id} onclick={() => { shown = shown === w.id ? '' : w.id; load(); }}
          >Runs</button
        >
      </li>
    {/each}
  </ul>
  <div class="row-actions">
    {#if !adding}
      <button type="button" class="sg-button" onclick={() => { adding = true; text = text || example; }}>Add a workflow</button>
    {/if}
  </div>
  <p class="saved" role="status">{notice}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>

{#if adding}
  <section class="sg-panel card" aria-labelledby="add-heading">
    <h2 id="add-heading">Add or update a workflow</h2>
    <p class="sg-muted">
      Paste a workflow (<code>streamgremlin.workflow/v1</code>). Saving one with an existing id makes a new version. New
      workflows start off. A plain-words editor is coming.
    </p>
    <form class="test-form wide" onsubmit={save}>
      <label class="field">
        <span>Workflow</span>
        <textarea class="sg-input code-input" rows="14" bind:value={text} spellcheck="false"></textarea>
      </label>
      {#if problems.length}
        <div role="alert">
          <p class="error">It has problems:</p>
          <ul class="problems">
            {#each problems as p (p.path + p.message)}
              <li><code>{p.path}</code>: {p.message}</li>
            {/each}
          </ul>
        </div>
      {/if}
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={busy || !text.trim()}>Save</button>
        <button type="button" class="button" onclick={() => { adding = false; problems = []; }}>Cancel</button>
      </div>
    </form>
    <details>
      <summary>Actions you can use</summary>
      {#each groups as g (g)}
        <h3>{g}</h3>
        <dl class="facts">
          {#each actions.filter((a) => a.group === g) as a (a.name)}
            <dt><code>{a.name}</code></dt>
            <dd>
              {a.label}{a.external ? '' : a.saves_state ? ' (a test event changes it for that run only, never saved)' : ' (runs for test events too)'}.
              {#if a.params.length}
                <span class="sg-muted">With: {a.params.map((p) => p.name + (p.required ? '' : '?')).join(', ')}</span>
              {/if}
            </dd>
          {/each}
        </dl>
      {/each}
    </details>
  </section>
{/if}

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
