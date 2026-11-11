<script lang="ts">
  // Timers: Streamer.bot's Timed Actions (docs/adr/0013-timers.md). A timer
  // goes off every so often or on a schedule, and starts the workflows that
  // use it. It can wait for chat, run only while live, and run once.
  import { onMount } from 'svelte';
  import {
    ApiError,
    deleteTimer,
    fireTimer,
    getTimers,
    getWorkflows,
    saveTimer,
    type TimerStatus,
    type WorkflowSummary,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { idFromName, type Def } from '../lib/editor';
  import { eventTime } from '../lib/events';
  import { conditionWords, formOf, newForm, nextWords, scheduleWords, starterWorkflow, timerOf, usedBy, type TimerForm } from '../lib/timers';
  import WorkflowEditor from '../editor/WorkflowEditor.svelte';
  import { onOffWords } from '../lib/workflows';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  let timers = $state<TimerStatus[]>([]);
  let live = $state<boolean | undefined>();
  let workflows = $state<WorkflowSummary[]>([]);
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let now = $state(new Date());

  // The form: closed, a new timer (id ''), or the timer being changed.
  let editing = $state<{ id: string } | undefined>();
  let form = $state<TimerForm>(newForm());
  let formError = $state('');
  let deleting = $state<TimerStatus | undefined>();
  // The workflow editor, opened on a new workflow for a timer.
  let starting = $state<{ def: Def; blanks: { path: string; hint: string }[] } | undefined>();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const anyLiveOnly = $derived(timers.some((t) => t.live_only));
  const cronExamples = [
    { cron: '0 20 * * *', words: 'every day at 8 pm' },
    { cron: '*/30 * * * *', words: 'every half hour' },
    { cron: '0 18 * * mon-fri', words: 'weekdays at 6 pm' },
    { cron: '@hourly', words: 'every hour, on the hour' },
  ];

  async function load() {
    try {
      const r = await getTimers();
      timers = r.timers;
      live = r.live;
      now = new Date();
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  async function loadWorkflows() {
    try {
      workflows = (await getWorkflows()).workflows;
    } catch {
      // The list still works; "used by" is just missing.
    }
  }

  onMount(() => {
    load();
    loadWorkflows();
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
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  function add() {
    notice = '';
    formError = '';
    form = newForm();
    editing = { id: '' };
  }

  function edit(t: TimerStatus) {
    notice = '';
    formError = '';
    form = formOf(t);
    editing = { id: t.id };
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!editing) return;
    const isNew = editing.id === '';
    const taken = timers.map((t) => t.id);
    const id = isNew ? idFromName(/[a-z0-9]/i.test(form.name) ? form.name : 'timer', taken) : editing.id;
    const { timer, problem } = timerOf(id, form);
    if (!timer) {
      formError = problem ?? '';
      return;
    }
    busy = true;
    formError = '';
    try {
      const saved = await saveTimer(timer);
      editing = undefined;
      const unused = usedBy(saved.id, workflows).length === 0;
      notice = `Saved “${saved.name}”.` + (unused ? ' No workflow uses it yet: “Make a workflow” gives it something to do.' : '');
    } catch (err) {
      formError = message(err);
    } finally {
      busy = false;
      await load();
    }
  }

  function remove(t: TimerStatus) {
    deleting = undefined;
    act(() => deleteTimer(t.id), `Deleted “${t.name}”.`);
  }

  function makeWorkflow(t: TimerStatus) {
    notice = '';
    starting = starterWorkflow(t);
  }
</script>

<h1>Timers</h1>
<p class="sg-muted screen-intro">
  A timer goes off every so often, or on a schedule, and starts the workflows that use it: a reminder about your socials every
  15 minutes, say. It can wait for chat to be talking, and run only while you’re live.
</p>

{#if starting}
  <WorkflowEditor
    initial={starting.def}
    fresh
    blanks={starting.blanks}
    title="A workflow for a timer"
    taken={workflows.map((w) => w.id)}
    {workflows}
    onsaved={(w, _changed, offBecause) => {
      starting = undefined;
      notice = `Saved “${w.name}”. ${onOffWords(w, offBecause)}`;
      loadWorkflows();
    }}
    oncancel={() => (starting = undefined)}
  />
{:else}
  <section class="sg-panel card" aria-labelledby="timers-heading" aria-busy={busy}>
    <h2 id="timers-heading">Your timers</h2>
    {#if loadError}
      <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
    {:else if timers.length === 0}
      <p class="sg-muted">No timers yet.</p>
    {/if}
    {#if anyLiveOnly}
      <p class="sg-muted">
        {#if live === true}OBS says you’re live.{:else if live === false}OBS says you’re not live, so “only while live” timers skip their turns.{:else}OBS
          hasn’t said whether you’re live, so “only while live” timers skip their turns.{/if}
      </p>
    {/if}
    <ul class="secret-list" aria-label="Timers">
      {#each timers as t (t.id)}
        {@const users = usedBy(t.id, workflows)}
        <li class="secret-row">
          <span class="secret-info">
            <strong>{t.name}</strong>
            <span class="sg-muted secret-saved">{[scheduleWords(t), ...conditionWords(t)].join(', ')}.</span>
            <span class="secret-saved">{nextWords(t, now, live)}</span>
            <span class="sg-muted secret-saved">
              Gone off {t.counter} {t.counter === 1 ? 'time' : 'times'}{t.last_fired_at ? `, last ${eventTime(t.last_fired_at)}` : ''}.
              {#if users.length}Starts: {users.map((w) => `“${w.name}”${w.enabled ? '' : ' (off)'}`).join(', ')}.{:else}No workflow uses it yet.{/if}
            </span>
          </span>
          <label class="choice-inline">
            <input
              type="checkbox"
              role="switch"
              aria-label={`${t.name} on`}
              checked={t.enabled}
              disabled={busy}
              onchange={(e) => {
                // Show what the engine says, not the click.
                const on = e.currentTarget.checked;
                e.currentTarget.checked = t.enabled;
                act(() => saveTimer({ ...t, enabled: on }), `“${t.name}” is ${on ? 'on' : 'off'}.`);
              }}
            />
            On
          </label>
          <button type="button" class="button" aria-label={`Run ${t.name} now`} disabled={busy}
            onclick={() => act(() => fireTimer(t.id), `“${t.name}” went off. Its workflows run for real.`)}>Run now</button
          >
          {#if !users.length}
            <button type="button" class="button" aria-label={`Make a workflow for ${t.name}`} onclick={() => makeWorkflow(t)}>Make a workflow</button>
          {/if}
          <button type="button" class="button" aria-label={`Edit ${t.name}`} onclick={() => edit(t)}>Edit</button>
          <button type="button" class="button button-danger" aria-label={`Delete ${t.name}`} disabled={busy} onclick={() => (deleting = t)}>Delete</button>
        </li>
      {/each}
    </ul>
    {#if !editing}
      <div class="row-actions">
        <button type="button" class="sg-button" onclick={add}>New timer</button>
      </div>
    {/if}
    <p class="saved" role="status">{notice}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </section>

  {#if editing}
    <section class="sg-panel card" aria-labelledby="timer-form-heading">
      <h2 id="timer-form-heading">{editing.id ? `Change “${form.name || editing.id}”` : 'New timer'}</h2>
      <form class="test-form" onsubmit={save}>
        <label class="field">
          <span>Name</span>
          <input class="sg-input" bind:value={form.name} placeholder="Socials" maxlength="100" />
          {#if editing.id}<span class="sg-muted field-hint">Workflows find it by its id, <code>{editing.id}</code>, which stays the same.</span>{/if}
        </label>
        <fieldset class="field">
          <legend>Goes off</legend>
          <label class="choice-inline"><input type="radio" name="mode" value="every" bind:group={form.mode} /> Every so often</label>
          <label class="choice-inline"><input type="radio" name="mode" value="cron" bind:group={form.mode} /> On a schedule</label>
        </fieldset>
        {#if form.mode === 'every'}
          <div class="row-actions">
            <label class="field">
              <span>Every</span>
              <input class="sg-input" type="number" min="1" step="any" bind:value={form.every} />
            </label>
            <label class="field">
              <span>Unit</span>
              <select class="sg-input" bind:value={form.unit}>
                <option value="seconds">seconds</option>
                <option value="minutes">minutes</option>
                <option value="hours">hours</option>
              </select>
            </label>
          </div>
          <label class="choice-inline"><input type="checkbox" bind:checked={form.random} /> At random, up to…</label>
          {#if form.random}
            <label class="field">
              <span>Up to ({form.unit})</span>
              <input class="sg-input" type="number" min="1" step="any" bind:value={form.upTo} />
              <span class="sg-muted field-hint">Each time, it picks a time between the two.</span>
            </label>
          {/if}
        {:else}
          <label class="field">
            <span>Schedule (minute, hour, day of the month, month, day of the week)</span>
            <input class="sg-input" bind:value={form.cron} spellcheck="false" />
            <span class="sg-muted field-hint">
              In this computer’s time. For example:
              {#each cronExamples as ex, i (ex.cron)}<button type="button" class="link-button" onclick={() => (form.cron = ex.cron)}><code>{ex.cron}</code></button> {ex.words}{i < cronExamples.length - 1 ? '; ' : '.'}{/each}
            </span>
          </label>
        {/if}
        <label class="field">
          <span>Wait for chat lines</span>
          <input class="sg-input" type="number" min="0" max="1000" bind:value={form.lines} />
          <span class="sg-muted field-hint">
            0 doesn’t wait. Otherwise, when its time comes it waits until viewers have sent this many messages since it last went off,
            so it doesn’t talk into a quiet chat. Your own messages, and the bot’s, don’t count.
          </span>
        </label>
        <label class="choice-inline"><input type="checkbox" bind:checked={form.liveOnly} /> Only while I’m live (OBS is streaming)</label>
        <label class="choice-inline"><input type="checkbox" bind:checked={form.once} /> Run once, then turn off</label>
        <label class="choice-inline"><input type="checkbox" bind:checked={form.enabled} /> On</label>
        {#if formError}<p class="error" role="alert">{formError}</p>{/if}
        <div class="row-actions">
          <button type="submit" class="sg-button" disabled={busy}>Save</button>
          <button type="button" class="button" onclick={() => (editing = undefined)}>Cancel</button>
        </div>
      </form>
    </section>
  {/if}
{/if}

{#if deleting}
  {@const t = deleting}
  {@const users = usedBy(t.id, workflows)}
  <ConfirmDialog
    title="Delete this timer?"
    message={`“${t.name}” is deleted for good.` +
      (users.length ? ` ${users.map((w) => `“${w.name}”`).join(', ')} ${users.length === 1 ? 'is' : 'are'} kept, but won’t start from it any more.` : '')}
    confirmLabel="Delete"
    onconfirm={() => remove(t)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
