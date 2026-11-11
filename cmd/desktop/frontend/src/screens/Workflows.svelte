<script lang="ts">
  // Workflows: what runs when, switched on or off, tested with a test event,
  // edited in plain words, deleted and restored, and every run's trace.
  import { onMount } from 'svelte';
  import {
    ApiError,
    deleteWorkflow,
    getCommands,
    getDeletedWorkflows,
    getEventCatalog,
    getJobs,
    getOverlays,
    getWorkflow,
    getWorkflowGroups,
    renameWorkflowGroup,
    setWorkflowGroupEnabled,
    getWorkflows,
    restoreWorkflow,
    setWorkflowEnabled,
    simulateEvent,
    type Blank,
    type CommandEntry,
    type EventKind,
    type Job,
    type WorkflowGroup,
    type WorkflowSummary,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import type { Def } from '../lib/editor';
  import TemplateGallery from '../editor/TemplateGallery.svelte';
  import { eventTime } from '../lib/events';
  import { describeTrigger, forWhom, jobOk, jobWords, overlaysIn, testFields, onOffWords } from '../lib/workflows';
  import OverlayPreview from '../lib/OverlayPreview.svelte';
  import WorkflowEditor from '../editor/WorkflowEditor.svelte';
  import RunTrace from './RunTrace.svelte';

  let { refreshMs = 2000, previewWaitMs = 4000 }: { refreshMs?: number; previewWaitMs?: number } = $props();

  let workflows = $state<WorkflowSummary[]>([]);
  let groups = $state<WorkflowGroup[]>([]);
  let renaming = $state<{ from: string; to: string } | undefined>();
  let deleted = $state<WorkflowSummary[]>([]);
  let commands = $state<CommandEntry[]>([]);
  let kinds = $state<EventKind[]>([]);
  let jobs = $state<Job[]>([]);
  let shown = $state(''); // workflow whose runs are listed ('' = all)
  let openJob = $state('');
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  // A test of a workflow that shows alerts: previews of its overlays, here
  // where Test was pressed, since a test never reaches OBS.
  let testPreview = $state<{ workflow: string; overlays: string[] } | undefined>();

  // The editor: closed, a new workflow, or an existing one's definition.
  // The editor: closed, the template gallery, or a definition to edit
  // (from a template: fresh, with its blanks).
  let editing = $state<{ gallery?: boolean; def?: Def; fresh?: boolean; blanks?: Blank[]; title?: string } | undefined>();
  let deleting = $state<WorkflowSummary | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  async function load() {
    try {
      workflows = (await getWorkflows()).workflows;
      // Groups are extra: without them, the list still shows.
      groups = (await getWorkflowGroups().catch(() => ({ groups: [] as WorkflowGroup[] }))).groups;
      deleted = (await getDeletedWorkflows()).workflows;
      jobs = (await getJobs(shown || undefined)).jobs;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
    // The commands list is extra: if it can't load, the rest still shows.
    getCommands()
      .then((r) => (commands = r.commands))
      .catch(() => undefined);
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

  function saved(w: WorkflowSummary, changed: boolean, offBecause?: string) {
    editing = undefined;
    error = '';
    notice = changed ? `Saved “${w.name}” as version ${w.version}. ${onOffWords(w, offBecause)}` : `“${w.name}” is unchanged.`;
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

  /** Waits until the app's previews of these overlays are connected, so the test alert reaches them. */
  async function previewsOpen(names: string[]) {
    const deadline = Date.now() + previewWaitMs;
    while (Date.now() < deadline) {
      try {
        const list = (await getOverlays()).overlays;
        if (names.every((n) => (list.find((o) => o.name === n)?.previews ?? 0) > 0)) return true;
      } catch {
        // Keep trying until the deadline.
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    return false;
  }

  async function test(w: WorkflowSummary) {
    const t = w.triggers.find((t) => testFields(t, kinds.find((k) => k.type === t.type)));
    if (!t) return;
    const fields = testFields(t, kinds.find((k) => k.type === t.type))!;
    let names: string[] = [];
    try {
      names = overlaysIn((await getWorkflow(w.id)).definition);
    } catch {
      // Without its steps, test without previews; the run still says how it went.
    }
    let done = `Sent a test event. “${w.name}” runs as a test: nothing reaches Twitch or OBS, and saved variables don’t change.`;
    if (names.length) {
      testPreview = { workflow: w.name, overlays: names };
      busy = true;
      const open = await previewsOpen(names);
      busy = false;
      done = open
        ? `Sent a test event. “${w.name}” runs as a test: its alert shows in the preview below, never in OBS, and nothing reaches Twitch.`
        : `Sent a test event, but the preview below didn't connect in time, so the alert may not show there. Nothing reaches OBS or Twitch.`;
    }
    act(() => simulateEvent(t.type, fields, crypto.randomUUID()), done);
  }

  const testable = (w: WorkflowSummary) => w.triggers.some((t) => testFields(t, kinds.find((k) => k.type === t.type)));
  // It runs (and can be tested) when it's on and its group is on.
  const runs = (w: WorkflowSummary) => w.running ?? w.enabled;
  const canTest = (w: WorkflowSummary) => runs(w) && testable(w);
  /** Why Test can't be used, said on screen rather than in a hover tip. */
  const whyNoTest = (w: WorkflowSummary) =>
    !w.enabled
      ? 'Turn it on to test it.'
      : !runs(w)
        ? `It’s on, but its group (${w.group}) is off, so it doesn’t run.`
        : !testable(w)
          ? w.triggers.every((t) => t.type.startsWith('plugin.'))
            ? 'An add-on’s event starts it: use the add-on to try it.'
            : 'Its trigger can’t be imitated by a test event yet.'
          : '';

  // The list in sections: each group (by name), then the ones in none.
  const sections = $derived.by(() => {
    const byGroup = new Map<string, WorkflowSummary[]>();
    for (const w of shownWorkflows) {
      const g = w.group ?? '';
      byGroup.set(g, [...(byGroup.get(g) ?? []), w]);
    }
    const out: { group?: WorkflowGroup; name: string; list: WorkflowSummary[] }[] = [];
    for (const g of groups) {
      const list = byGroup.get(g.name);
      if (list?.length) out.push({ group: g, name: g.name, list });
    }
    const none = byGroup.get('') ?? [];
    if (none.length) out.push({ name: '', list: none });
    // A group the list knows but /v1/workflow-groups didn't send (yet).
    for (const [name, list] of byGroup) {
      if (name && !groups.some((g) => g.name === name)) out.push({ name, list });
    }
    return out;
  });

  // Folding groups: groups start folded; the ones opened are remembered in
  // this window's storage (a convenience: if it's cleared, they start
  // folded again). A search opens every group with a match.
  const OPEN_KEY = 'streamgremlin.workflows.open-groups';
  let openGroups = $state<string[]>(readOpen());
  function readOpen(): string[] {
    try {
      const v = JSON.parse(localStorage.getItem(OPEN_KEY) ?? '[]');
      return Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [];
    } catch {
      return [];
    }
  }
  function saveOpen(list: string[]) {
    openGroups = list;
    try {
      localStorage.setItem(OPEN_KEY, JSON.stringify(list));
    } catch {
      // Not kept; it still works for now.
    }
  }
  const isOpen = (name: string) => !name || !!query.trim() || openGroups.some((g) => g.toLowerCase() === name.toLowerCase());
  const toggleGroup = (name: string) =>
    saveOpen(isOpen(name) ? openGroups.filter((g) => g.toLowerCase() !== name.toLowerCase()) : [...openGroups, name]);

  async function saveRename(e: SubmitEvent) {
    e.preventDefault();
    const r = renaming;
    if (!r) return;
    const to = r.to.trim();
    if (!to || to === r.from) {
      renaming = undefined;
      return;
    }
    await act(() => renameWorkflowGroup(r.from, to), `Renamed “${r.from}” to “${to}”.`);
    if (!error) renaming = undefined;
  }

  // Search: by name, id, or what starts it in words (which includes the
  // command), ignoring case. Every word typed must match.
  let query = $state('');
  const shownWorkflows = $derived.by(() => {
    const words = query.toLowerCase().split(/\s+/).filter(Boolean);
    if (!words.length) return workflows;
    return workflows.filter((w) => {
      const text = [w.name, w.id, ...w.triggers.map((t) => describeTrigger(t, kinds))].join(' ').toLowerCase();
      return words.every((x) => text.includes(x));
    });
  });

  const taken = $derived([...workflows, ...deleted].map((w) => w.id));
</script>

<h1>Workflows</h1>
<p class="sg-muted screen-intro">A workflow does something when something happens, like answering !hello or switching scenes on !brb. Start from a template or build your own.</p>

{#if editing?.gallery}
  <TemplateGallery
    {workflows}
    onpick={(t) => (editing = t ? { def: t.workflow, fresh: true, blanks: t.blanks, title: t.title } : {})}
    oncancel={() => (editing = undefined)}
  />
{:else if editing}
  <WorkflowEditor
    initial={editing.def}
    fresh={editing.fresh}
    blanks={editing.blanks}
    title={editing.title}
    {taken}
    {workflows}
    onsaved={saved}
    oncancel={() => (editing = undefined)}
  />
{:else}
<section class="sg-panel card" aria-labelledby="wf-heading" aria-busy={busy}>
  <h2 id="wf-heading">Your workflows</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if workflows.length === 0}
    <p class="sg-muted">No workflows yet. A workflow does something when something happens, like answering <code>!hello</code> in chat.</p>
  {/if}
  {#if workflows.length}
    <label class="field var-search">
      <span>Search</span>
      <input class="sg-input" type="search" placeholder="A name, a command or a trigger" bind:value={query}
        onkeydown={(e) => e.key === 'Escape' && (query = '')} />
    </label>
    {#if query.trim() && shownWorkflows.length === 0}
      <p class="sg-muted" role="status">No workflow matches “{query.trim()}”.</p>
    {/if}
  {/if}
  {#snippet row(w: WorkflowSummary)}
      <li class="secret-row">
        <span class="secret-info">
          <strong>{w.name}</strong>
          <span class="sg-muted secret-saved">
            When: {w.triggers.map((t) => describeTrigger(t, kinds)).join('; or ')}
          </span>
          {#if w.group && !groups.length}
            <span class="sg-muted secret-saved">Group: {w.group}</span>
          {/if}
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
            onchange={(e) => {
              // Show what the engine says, not the click: turning on can be refused.
              const on = e.currentTarget.checked;
              e.currentTarget.checked = w.enabled;
              act(() => setWorkflowEnabled(w.id, on), `“${w.name}” is ${on ? 'on' : 'off'}.`);
            }}
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
  {/snippet}
  {#if !groups.length && !sections.some((x) => x.name)}
    <ul class="secret-list" aria-label="Workflows">
      {#each shownWorkflows as w (w.id)}
        {@render row(w)}
      {/each}
    </ul>
  {:else}
    {#if sections.some((x) => x.name) && !query.trim()}
      <div class="row-actions group-fold">
        <button type="button" class="button" onclick={() => saveOpen(sections.filter((x) => x.name).map((x) => x.name))}>Open all</button>
        <button type="button" class="button" onclick={() => saveOpen([])}>Fold all</button>
      </div>
    {/if}
    {#each sections as sec, i (sec.name)}
      <div class="wf-group" role="group" aria-label={sec.name ? `Group ${sec.name}` : 'No group'}>
        <div class="wf-group-head">
          {#if sec.name}
            <h3>
              <button
                type="button"
                class="group-toggle"
                aria-expanded={isOpen(sec.name)}
                aria-controls={`wf-group-${i}`}
                disabled={!!query.trim()}
                onclick={() => toggleGroup(sec.name)}
                ><span aria-hidden="true">{isOpen(sec.name) ? '▾' : '▸'}</span> {sec.name}</button
              >
            </h3>
          {:else}
            <h3>No group</h3>
          {/if}
          {#if sec.group}
            {@const g = sec.group}
            <span class="sg-muted">{g.workflows} {g.workflows === 1 ? 'workflow' : 'workflows'}, {g.on} switched on</span>
            <label class="choice-inline">
              <input
                type="checkbox"
                role="switch"
                checked={g.enabled}
                disabled={busy}
                aria-label={`Group ${g.name} on`}
                onchange={(e) => {
                  const on = e.currentTarget.checked;
                  e.currentTarget.checked = g.enabled; // the engine decides
                  act(() => setWorkflowGroupEnabled(g.name, on), on
                    ? `“${g.name}” is on: its workflows that are switched on run.`
                    : `“${g.name}” is off: none of its workflows run. Each keeps its own switch.`);
                }}
              />
              Group on
            </label>
            <button type="button" class="button" aria-label={`Rename ${g.name}`} disabled={busy}
              onclick={() => (renaming = { from: g.name, to: g.name })}>Rename</button
            >
          {/if}
        </div>
        {#if renaming && renaming.from === sec.name}
          <form class="rename-form" onsubmit={saveRename}>
            <label class="field">
              <span>New name for “{sec.name}”</span>
              <input class="sg-input" bind:value={renaming.to} maxlength="40" />
            </label>
            <button type="submit" class="sg-button" disabled={busy}>Save</button>
            <button type="button" class="button" onclick={() => (renaming = undefined)}>Cancel</button>
          </form>
        {/if}
        {#if sec.group && !sec.group.enabled}
          <p class="state state-unknown"><span aria-hidden="true">!</span> This group is off: none of its workflows run. Each keeps its own switch.</p>
        {/if}
        {#if isOpen(sec.name)}
          <ul id={`wf-group-${i}`} class="secret-list" aria-label={sec.name ? `Workflows in ${sec.name}` : 'Workflows in no group'}>
            {#each sec.list as w (w.id)}
              {@render row(w)}
            {/each}
          </ul>
        {/if}
      </div>
    {/each}
  {/if}
  <div class="row-actions">
    <button type="button" class="sg-button" onclick={() => { notice = ''; editing = { gallery: true }; }}>New workflow</button>
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

{#if testPreview}
  <section class="sg-panel card" aria-labelledby="test-preview-heading">
    <h2 id="test-preview-heading">Test preview: {testPreview.workflow}</h2>
    <p class="sg-muted">Tests show alerts here, never in OBS.</p>
    {#each testPreview.overlays as name (name)}
      <div class="test-preview">
        <h3>{name}</h3>
        <OverlayPreview {name} />
      </div>
    {/each}
    <div class="row-actions">
      <button type="button" class="button" onclick={() => (testPreview = undefined)}>Close the preview</button>
    </div>
  </section>
{/if}

<section class="sg-panel card" aria-labelledby="cmd-heading">
  <h2 id="cmd-heading">Chat commands</h2>
  <p class="sg-muted">One answer per command: a workflow can only be turned on when no other that’s on answers its command for the same chatters.</p>
  {#if commands.length === 0}
    <p class="sg-muted">No workflow answers a chat command yet.</p>
  {/if}
  <ul class="secret-list" aria-label="Chat commands">
    {#each commands as c (c.command)}
      <li class="secret-row">
        <span class="secret-info">
          <strong><code>!{c.command}</code></strong>
          {#each c.owners as o (o.owner)}
            <span class={o.enabled ? 'secret-saved' : 'sg-muted secret-saved'}>
              {o.name}{forWhom(o.roles)}{o.sources?.length ? ` on ${o.sources.join(', ')}` : ''}{o.enabled ? '' : ' (off)'}
            </span>
          {/each}
          {#if c.conflict}
            <span class="secret-check"><span class="icon-bad" aria-hidden="true">✗</span> More than one that’s on answers this. Turn one off.</span>
          {/if}
        </span>
      </li>
    {/each}
  </ul>
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
