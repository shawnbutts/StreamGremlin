<script lang="ts">
  // What a paired Stream Deck may use beyond buttons (D-123, ADR-0053).
  // Counters are global variables a key shows and changes by one; switches
  // are workflows, timers and the chat lock a key turns on and off. Nothing
  // is allowed until ticked here, and un-ticking refuses the next press.
  // Status keys (live, chat, OBS, the chat lock) need nothing ticked.
  import { onMount } from 'svelte';
  import {
    ApiError,
    getDeck,
    getDeckSettings,
    getOverlays,
    getTimers,
    getVariables,
    getWorkflows,
    putDeckSettings,
    type DeckSettings,
    type DeckView,
    type Overlay,
    type TimerStatus,
    type WorkflowSummary,
  } from '../lib/api';

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const NAME = /^[a-z][a-z0-9_]{0,63}$/;

  let settings = $state<DeckSettings | undefined>();
  let view = $state<DeckView | undefined>();
  let workflows = $state<WorkflowSummary[]>([]);
  let timers = $state<TimerStatus[]>([]);
  let globals = $state<string[]>([]);
  let clocks = $state<Overlay[]>([]);
  let loadError = $state('');
  let newCounter = $state('');
  let counterProblem = $state('');
  let notice = $state('');
  let problem = $state('');
  let saving = $state(false);

  const chosen = (kind: string, id?: string) => !!settings?.switches.some((s) => s.kind === kind && (s.id ?? '') === (id ?? ''));
  const valueOf = (name: string) => {
    const c = view?.counters.find((x) => x.name === name);
    if (!c || c.value === null || c.value === undefined) return '0';
    return typeof c.value === 'number' ? String(c.value) : '? (not a number)';
  };
  const yesNo = (b: boolean | null, yes: string, no: string) => (b === null ? 'unknown' : b ? yes : no);

  async function load() {
    try {
      const [s, v, w, t, vars, ovs] = await Promise.all([
        getDeckSettings(),
        getDeck(),
        getWorkflows(),
        getTimers(),
        getVariables({ scope: 'global' }),
        getOverlays(),
      ]);
      settings = s;
      view = v;
      workflows = w.workflows;
      timers = t.timers;
      globals = vars.variables.map((x) => x.name);
      clocks = ovs.overlays.filter((o) => o.kind === 'timer' && (o.look.timer_mode === 'countdown' || o.look.timer_mode === 'stopwatch'));
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  async function save(next: DeckSettings, said: string) {
    saving = true;
    notice = '';
    problem = '';
    try {
      settings = await putDeckSettings(next);
      view = await getDeck();
      notice = said;
    } catch (e) {
      problem = message(e);
    } finally {
      saving = false;
    }
  }

  function addCounter(e: SubmitEvent) {
    e.preventDefault();
    if (!settings) return;
    const name = newCounter.trim();
    counterProblem = '';
    if (!NAME.test(name)) {
      counterProblem = 'A variable name is lowercase letters, digits and _, starting with a letter.';
      return;
    }
    if (settings.counters.includes(name)) {
      counterProblem = `${name} is already a counter.`;
      return;
    }
    newCounter = '';
    void save({ ...settings, counters: [...settings.counters, name] }, `${name} can be a counter key now.`);
  }

  function removeCounter(name: string) {
    if (!settings) return;
    void save({ ...settings, counters: settings.counters.filter((n) => n !== name) }, `${name} is no longer a counter: its keys stop changing it.`);
  }

  function toggle(kind: 'workflow' | 'timer' | 'chat_lock', id: string | undefined, label: string, on: boolean) {
    if (!settings) return;
    const rest = settings.switches.filter((s) => !(s.kind === kind && (s.id ?? '') === (id ?? '')));
    const switches = on ? [...rest, id ? { kind, id } : { kind }] : rest;
    void save({ ...settings, switches }, on ? `${label} can be a switch key now.` : `${label} is no longer a switch: its keys stop working.`);
  }

  function pick(list: 'runs' | 'countdowns', id: string, label: string, on: boolean, what: string) {
    if (!settings) return;
    const rest = settings[list].filter((x) => x !== id);
    void save({ ...settings, [list]: on ? [...rest, id] : rest }, on ? `${label} can be ${what} key now.` : `${label} is no longer ${what} key: its keys stop working.`);
  }

  function allow(what: 'scenes' | 'ads', on: boolean, label: string) {
    if (!settings) return;
    void save({ ...settings, [what]: on }, on ? `${label}: allowed.` : `${label}: no longer allowed; those keys stop working.`);
  }

  const needsViewer = (id: string) => !!view?.runs.find((r) => r.id === id)?.needs_viewer;

  onMount(load);
</script>

<section class="sg-panel card" aria-labelledby="deckkeys-heading">
  <h2 id="deckkeys-heading">What your Stream Deck can use</h2>
  <p class="sg-muted">
    Besides pressing your buttons, a paired Stream Deck can show and change only what you choose here. Status keys (live, chat, OBS and the chat lock) need
    nothing chosen: they only show.
  </p>

  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if settings && view}
    <p class="status-line" aria-label="What status keys show now">
      Now: {view.status.live === null ? 'live unknown' : view.status.live ? 'live' : 'not live'} · chat {view.status.chat} · OBS {view.status.obs} · chat lock {yesNo(view.status.chat_locked, 'on', 'off')}
    </p>

    <h3>Counters</h3>
    <p class="sg-muted">A counter key shows a variable and adds one, takes one (never below 0) or sets it to 0, as you choose in the key's settings.</p>
    {#if settings.counters.length === 0}
      <p>No counters yet.</p>
    {:else}
      <ul class="pick-list">
        {#each settings.counters as name (name)}
          <li>
            <span><code>{name}</code> <span class="sg-muted">now {valueOf(name)}</span></span>
            <button type="button" class="button" disabled={saving} onclick={() => removeCounter(name)} aria-label={`Stop using ${name} as a counter`}>Remove</button>
          </li>
        {/each}
      </ul>
    {/if}
    <form class="add-counter" onsubmit={addCounter}>
      <label for="deck-counter">Variable</label>
      <input id="deck-counter" list="deck-globals" bind:value={newCounter} placeholder="deaths" autocomplete="off" />
      <datalist id="deck-globals">
        {#each globals.filter((g) => !settings?.counters.includes(g)) as g (g)}<option value={g}></option>{/each}
      </datalist>
      <button type="submit" class="sg-button" disabled={saving}>Add counter</button>
    </form>
    {#if counterProblem}<p class="error" role="alert">{counterProblem}</p>{/if}

    <h3>Switches</h3>
    <p class="sg-muted">A switch key turns something on and off, lit when it's on.</p>
    <fieldset>
      <legend>Chat</legend>
      <label class="pick">
        <input type="checkbox" checked={chosen('chat_lock')} disabled={saving} onchange={(e) => toggle('chat_lock', undefined, 'The chat lock', e.currentTarget.checked)} />
        The chat lock: Shield Mode, emote-only and followers-only on Twitch, and back
      </label>
    </fieldset>
    <fieldset>
      <legend>Workflows</legend>
      {#if workflows.length === 0}<p class="sg-muted">No workflows yet.</p>{/if}
      {#each workflows as w (w.id)}
        <label class="pick">
          <input type="checkbox" checked={chosen('workflow', w.id)} disabled={saving} onchange={(e) => toggle('workflow', w.id, w.name, e.currentTarget.checked)} />
          {w.name}
        </label>
      {/each}
    </fieldset>
    <fieldset>
      <legend>Timers</legend>
      {#if timers.length === 0}<p class="sg-muted">No timers yet.</p>{/if}
      {#each timers as t (t.id)}
        <label class="pick">
          <input type="checkbox" checked={chosen('timer', t.id)} disabled={saving} onchange={(e) => toggle('timer', t.id, t.name, e.currentTarget.checked)} />
          {t.name}
        </label>
      {/each}
    </fieldset>

    <h3>Run a workflow</h3>
    <p class="sg-muted">
      A run key starts a workflow now, whatever starts it otherwise, if it’s on. A button on Live is usually better: its workflows start from the press.
    </p>
    <fieldset>
      <legend>Workflows to run</legend>
      {#if workflows.length === 0}<p class="sg-muted">No workflows yet.</p>{/if}
      {#each workflows as w (w.id)}
        <label class="pick">
          <input type="checkbox" checked={!!settings?.runs.includes(w.id)} disabled={saving} onchange={(e) => pick('runs', w.id, w.name, e.currentTarget.checked, 'a run')} />
          {w.name}
        </label>
        {#if settings?.runs.includes(w.id) && needsViewer(w.id)}
          <p class="warn">
            “{w.name}” starts from a viewer or their message. Run from the Stream Deck, it has neither, so steps that use them get nothing.
          </p>
        {/if}
      {/each}
    </fieldset>

    <h3>OBS, countdowns and ads</h3>
    <fieldset>
      <legend>OBS</legend>
      <label class="pick">
        <input type="checkbox" checked={settings.scenes} disabled={saving} onchange={(e) => allow('scenes', e.currentTarget.checked, 'Switching OBS scenes')} />
        Scene keys may switch OBS’s scene (lit while it’s on air)
      </label>
    </fieldset>
    <fieldset>
      <legend>Countdowns and stopwatches</legend>
      {#if clocks.length === 0}<p class="sg-muted">No countdown or stopwatch overlays yet: make a Clock and timers overlay on Overlays.</p>{/if}
      {#each clocks as o (o.name)}
        <label class="pick">
          <input type="checkbox" checked={!!settings?.countdowns.includes(o.name)} disabled={saving} onchange={(e) => pick('countdowns', o.name, o.look.timer_label || o.name, e.currentTarget.checked, 'a countdown')} />
          {o.look.timer_label || o.name} <span class="sg-muted">({o.look.timer_mode})</span>
        </label>
      {/each}
    </fieldset>
    <fieldset>
      <legend>Ads</legend>
      <label class="pick">
        <input type="checkbox" checked={settings.ads} disabled={saving} onchange={(e) => allow('ads', e.currentTarget.checked, 'Snoozing ads')} />
        An ad key shows when Twitch’s next ad is due and may snooze it
      </label>
    </fieldset>
    {#each view.switches.filter((s) => s.gone) as g (g.kind + g.id)}
      <p class="state state-unknown">
        <span aria-hidden="true">?</span> A chosen {g.kind} was deleted.
        <button type="button" class="button" disabled={saving} onclick={() => toggle(g.kind, g.id, g.label, false)}>Remove it</button>
      </p>
    {/each}
  {/if}

  <p class="saved" role="status">{notice}</p>
  {#if problem}<p class="error" role="alert">{problem}</p>{/if}
</section>

<style>
  .status-line {
    padding: 0.4rem 0.75rem;
    border: 1px solid var(--sg-border);
    border-radius: 8px;
  }
  h3 {
    margin: 1rem 0 0.25rem;
  }
  .pick-list {
    list-style: none;
    margin: 0 0 0.5rem;
    padding: 0;
    display: grid;
    gap: 0.4rem;
  }
  .pick-list li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 0.4rem 0.75rem;
    border: 1px solid var(--sg-border);
    border-radius: 8px;
  }
  .add-counter {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }
  fieldset {
    border: 1px solid var(--sg-border);
    border-radius: 8px;
    margin: 0 0 0.5rem;
    padding: 0.4rem 0.75rem;
    max-height: 16rem;
    overflow-y: auto;
  }
  .warn {
    margin: 0 0 0.4rem 1.6rem;
    font-size: 0.9em;
  }
  .pick {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    padding: 0.15rem 0;
  }
</style>
