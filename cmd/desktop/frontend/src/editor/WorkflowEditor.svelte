<script lang="ts">
  // The plain-words workflow editor: When cards, Then cards, and a JSON tab
  // for everything else (docs/adr/0010-editor-run-delete.md). It edits the
  // definition itself, so what it can't show is kept.
  import { onMount } from 'svelte';
  import {
    ApiError,
    ProblemsError,
    getCommands,
    getOBSScenes,
    getSounds,
    getOverlays,
    getRewards,
    getTimers,
    getPalette,
    getSceneItems,
    getVariables,
    saveWorkflow,
    type Blank,
    type PaletteValue,
    type Problem,
    type Trigger,
    type WorkflowSummary,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { emptyDefinition, idFromName, problemsAt, unfilled, validId, type Def } from '../lib/editor';
  import { setEditorEnv, type EditorEnv } from '../lib/editorEnv';
  import StepList from './StepList.svelte';
  import TriggerCard from './TriggerCard.svelte';

  let {
    initial,
    fresh = false,
    blanks = [],
    title = '',
    taken = [],
    workflows = [],
    onsaved,
    oncancel,
  }: {
    initial?: Def;
    /** A new workflow started from a template (initial), not an existing one. */
    fresh?: boolean;
    /** The template's values to fill in before saving. */
    blanks?: Blank[];
    /** The template's title, for the heading. */
    title?: string;
    taken?: string[];
    workflows?: WorkflowSummary[];
    onsaved: (w: WorkflowSummary, changed: boolean) => void;
    oncancel: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  const isNew = !initial || fresh;
  // svelte-ignore state_referenced_locally
  let def = $state<Def>(JSON.parse(JSON.stringify(initial ?? emptyDefinition())));
  // svelte-ignore state_referenced_locally
  let start = JSON.stringify(def); // what it was when opened, to tell if it changed
  // A template keeps its own id unless that's taken; then the id follows the name.
  // svelte-ignore state_referenced_locally
  let idTouched = $state(fresh && !!initial?.id && !taken.includes(initial.id as string));
  let tab = $state<'editor' | 'json'>('editor');
  let jsonText = $state('');
  let jsonError = $state('');
  let version = $state(0); // re-shows the cards after the JSON tab changed things
  let error = $state('');
  let busy = $state(false);
  let loading = $state(true);
  let leaving = $state(false);

  const env = $state<EditorEnv>({
    triggers: [],
    actions: [],
    values: [],
    saved: [],
    // svelte-ignore state_referenced_locally
    workflows,
    timers: [],
    sounds: [],
    overlays: [],
    rewards: [],
    // svelte-ignore state_referenced_locally
    selfId: def.id,
    scenes: [],
    sources: [],
    problems: [],
    commands: [],
    more: false,
  });
  setEditorEnv(env);

  onMount(async () => {
    try {
      const p = await getPalette();
      env.triggers = p.triggers;
      env.actions = p.actions;
    } catch (e) {
      error = `Couldn’t load the editor’s choices: ${e instanceof ApiError ? e.message : String(e)}`;
    }
    loading = false;
    getCommands()
      .then((r) => {
        env.commands = r.commands;
      })
      .catch(() => undefined);
    getTimers()
      .then((r) => {
        env.timers = r.timers;
      })
      .catch(() => undefined);
    getSounds()
      .then((r) => {
        env.sounds = r.sounds;
      })
      .catch(() => undefined);
    getOverlays()
      .then((r) => {
        env.overlays = r.overlays;
      })
      .catch(() => undefined);
    getRewards()
      .then((r) => {
        env.rewards = r.rewards;
      })
      .catch(() => undefined);
    getVariables()
      .then((r) => {
        const seen = new Map<string, PaletteValue>();
        for (const v of r.variables) {
          const token = `saved.${v.scope}.${v.name}`;
          const whose = v.scope === 'global' ? 'saved' : v.scope === 'viewer' ? 'saved for the viewer' : 'saved for this workflow';
          if (v.scope !== 'workflow' || v.owner === def.id) seen.set(token, { token, label: `${v.name} (${whose})`, expr: '' });
        }
        env.saved = [...seen.values()];
      })
      .catch(() => undefined);
    // OBS's scenes and sources for the pickers, when OBS is connected.
    try {
      const s = await getOBSScenes();
      env.scenes = s.scenes;
      const sources = new Set<string>();
      for (const scene of s.scenes) {
        for (const it of (await getSceneItems(scene)).items) sources.add(it.source);
      }
      env.sources = [...sources].sort();
    } catch {
      // Not connected: the fields are plain text.
    }
  });

  // The values messages can include come from the workflow's triggers.
  $effect(() => {
    const seen = new Map<string, PaletteValue>();
    for (const t of def.triggers as Trigger[]) {
      for (const v of env.triggers.find((e) => e.type === t.type)?.values ?? []) seen.set(v.token, v);
    }
    env.values = [...seen.values()];
  });

  $effect(() => {
    if (isNew && !idTouched) def.id = def.name.trim() ? idFromName(def.name, taken) : '';
    env.selfId = def.id;
  });

  const dirty = () => JSON.stringify(def) !== start;
  const queueWords: { id: string; label: string }[] = [
    { id: 'serial', label: 'One at a time, in order' },
    { id: 'drop', label: 'Skip new ones while it runs' },
    { id: 'replace', label: 'Stop it and start again with the newest' },
    { id: 'parallel', label: 'Several at once' },
  ];

  function showJSON() {
    jsonText = JSON.stringify(def, null, 2);
    jsonError = '';
    tab = 'json';
  }

  function applyJSON(): boolean {
    try {
      const d = JSON.parse(jsonText);
      if (!d || typeof d !== 'object' || Array.isArray(d)) throw new Error('a workflow is a JSON object');
      def = d;
      jsonError = '';
      version++;
      return true;
    } catch (e) {
      jsonError = `That isn’t valid JSON: ${(e as Error).message}`;
      return false;
    }
  }

  function showEditor() {
    if (applyJSON()) tab = 'editor';
  }

  async function save() {
    if (tab === 'json' && !applyJSON()) return;
    error = '';
    env.problems = unfilled(def, blanks);
    if (env.problems.length) return; // the template's blanks, shown at their places
    busy = true;
    try {
      const r = await saveWorkflow(JSON.stringify(def));
      start = JSON.stringify(def);
      onsaved(r.workflow, r.changed);
    } catch (e) {
      if (e instanceof ProblemsError) env.problems = e.problems;
      else if (e instanceof ApiError && e.code === 'deleted') {
        error = `“${def.id}” belongs to a deleted workflow. Restore it from “Deleted workflows”, or give this one another id under Advanced.`;
      } else error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  function cancel() {
    if (tab === 'json') applyJSON();
    if (dirty()) leaving = true;
    else oncancel();
  }
</script>

<section class="sg-panel card editor" aria-labelledby="editor-heading" aria-busy={busy}>
  <h2 id="editor-heading">{title ? `New workflow from “${title}”` : isNew ? 'New workflow' : `Edit “${initial?.name ?? def.name}”`}</h2>
  {#if blanks.length}
    {@const left = unfilled(def, blanks)}
    <div class={left.length ? 'fill-in' : 'fill-in fill-in-done'} role="status">
      {#if left.length}
        <p><strong>Fill in before saving:</strong></p>
        <ul>
          {#each blanks as b (b.path)}
            <li>{left.some((l) => l.path === b.path) ? '○' : '✓'} {b.hint}</li>
          {/each}
        </ul>
      {:else}
        <p>✓ Everything to fill in is filled in.</p>
      {/if}
    </div>
  {/if}
  <div class="segmented" role="group" aria-label="View">
    <button type="button" class="button" aria-pressed={tab === 'editor'} onclick={showEditor}>Editor</button>
    <button type="button" class="button" aria-pressed={tab === 'json'} onclick={showJSON}>JSON</button>
  </div>

  {#if loading}
    <p class="sg-muted">Loading…</p>
  {:else if tab === 'json'}
    <label class="field">
      <span>Workflow (<code>streamgremlin.workflow/v1</code>)</span>
      <textarea class="sg-input code-input" rows="18" bind:value={jsonText} spellcheck="false"></textarea>
    </label>
    {#if jsonError}<p class="error" role="alert">{jsonError}</p>{/if}
  {:else}
    {#key version}
      <label class="field">
        <span>Name</span>
        <input class="sg-input" bind:value={def.name} placeholder="Say hello back" />
      </label>
      {#each problemsAt(env.problems, '$.name') as pr (pr.message)}<p class="error">{pr.message}</p>{/each}

      <h3>When</h3>
      <ol class="step-list">
        {#each def.triggers as t, i (t)}
          <TriggerCard
            bind:trigger={def.triggers[i]}
            index={i}
            removable={def.triggers.length > 1}
            onremove={() => (def.triggers = def.triggers.filter((x: unknown) => x !== t))}
          />
        {/each}
      </ol>
      <button type="button" class="button" onclick={() => (def.triggers = [...def.triggers, { type: 'chat.command.received', where: {} }])}
        >Or when…</button
      >

      <h3>Then</h3>
      <StepList bind:steps={def.steps} path="$.steps" />

      <label class="choice-inline more-toggle">
        <input type="checkbox" bind:checked={env.more} />
        Show rarely used triggers and steps
      </label>

      <details class="more-options help-box">
        <summary>How values and conditions work</summary>
        <p>
          <strong>Values</strong> in curly braces are filled in when the workflow runs: <code>{'{viewer}'}</code> is the name of the
          viewer who typed the command. Use <em>Insert a value</em> beside a message to add one. Others you can type:
          <code>{'{saved.global.deaths}'}</code> (a saved variable), <code>{'{vars.face}'}</code> (remembered earlier in this run),
          <code>{'{steps.n.output.value}'}</code> (what step <code>n</code> gave back; name a step under its More options) and
          <code>{'{args.text}'}</code> (a value another workflow passed in).
        </p>
        <p>
          <strong>Conditions</strong> compare a value with a text: “is” and “is not” match exactly, “contains” ignores capitals, and
          “is more than” and “is less than” compare numbers. Anything fancier can be written as an expression.
        </p>
        <p><strong>Test</strong> on the workflow list runs it with a test event: nothing reaches your chat or OBS, and saved variables don’t change.</p>
      </details>

      <details class="more-options">
        <summary>Advanced</summary>
        <label class="field">
          <span>Id</span>
          <input
            class="sg-input"
            value={def.id}
            disabled={!isNew}
            spellcheck="false"
            oninput={(e) => { idTouched = true; def.id = e.currentTarget.value; }}
          />
          <span class="sg-muted field-hint">
            {isNew ? 'How runs and other workflows find it. It follows the name until you change it.' : 'The id can’t change: runs and other workflows use it.'}
          </span>
        </label>
        {#if isNew && def.id && !validId(def.id)}<p class="error">Lowercase words separated by dots, like chat.say-hello.</p>{/if}
        <label class="field">
          <span>Description (optional)</span>
          <input class="sg-input" value={def.description ?? ''} onchange={(e) => (e.currentTarget.value ? (def.description = e.currentTarget.value) : delete def.description)} />
        </label>
        <label class="field">
          <span>When events come faster than it runs</span>
          <select
            class="sg-input"
            value={def.queue?.policy ?? 'serial'}
            onchange={(e) => {
              const policy = e.currentTarget.value;
              const q = { ...(def.queue ?? {}), policy };
              if (policy === 'parallel') q.concurrency = q.concurrency ?? 4;
              else delete q.concurrency;
              def.queue = q;
            }}
          >
            {#each queueWords as q (q.id)}<option value={q.id}>{q.label}</option>{/each}
          </select>
        </label>
        {#if def.queue?.policy === 'parallel'}
          <label class="field">
            <span>At most at once</span>
            <input class="sg-input" type="number" min="2" max="64" bind:value={def.queue.concurrency} />
          </label>
        {/if}
        <h4>Final steps</h4>
        <p class="sg-muted field-hint">Always run at the end, even when a step failed or the run was stopped: for cleaning up, like switching a scene back.</p>
        {#if def.finally}
          <StepList bind:steps={def.finally} path="$.finally" what="a final step" />
        {:else}
          <button type="button" class="button" onclick={() => (def.finally = [])}>Add final steps</button>
        {/if}
      </details>
    {/key}
  {/if}

  {#if env.problems.length}
    <div role="alert">
      <p class="error">It has {env.problems.length === 1 ? 'a problem' : `${env.problems.length} problems`}, marked above:</p>
      <ul class="problems">
        {#each env.problems as p (p.path + p.message)}
          <li><code>{p.path}</code>: {p.message}</li>
        {/each}
      </ul>
    </div>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <div class="row-actions">
    <button type="button" class="sg-button" disabled={busy || loading} onclick={save}>Save</button>
    <button type="button" class="button" onclick={cancel}>Cancel</button>
  </div>

  <datalist id="editor-scenes">{#each env.scenes as s (s)}<option value={s}></option>{/each}</datalist>
  <datalist id="editor-sources">{#each env.sources as s (s)}<option value={s}></option>{/each}</datalist>
</section>

{#if leaving}
  <ConfirmDialog
    title="Leave without saving?"
    message="Your changes to this workflow will be lost."
    confirmLabel="Leave"
    onconfirm={() => { leaving = false; oncancel(); }}
    oncancel={() => (leaving = false)}
  />
{/if}
