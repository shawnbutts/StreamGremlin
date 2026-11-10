<script lang="ts">
  // Wheels (docs/adr/0030-wheels.md): wheels of weighted choices, spun by
  // the engine with a secure random source and recorded so each spin can be
  // checked. The odds shown are exact, and the wheel overlay draws each
  // wedge at its real size.
  import { onMount } from 'svelte';
  import {
    ApiError,
    addWheel,
    deleteWheel,
    getSpin,
    getWheels,
    setWheel,
    spinWheel,
    type Wheel,
    type WheelChoice,
    type WheelSpin,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';
  import { howSpun, percents, percentWords, verifySpin } from '../lib/wheels';

  let { refreshMs = 2000 }: { refreshMs?: number } = $props();

  const NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/;
  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const key = () => (crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`);

  let wheels = $state<Wheel[]>([]);
  let spins = $state<WheelSpin[]>([]);
  let pages = $state(0);
  let loaded = $state(false);
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let deleting = $state<Wheel | undefined>();
  let checked = $state<{ id: string; problems: string[] } | undefined>();
  // The spin this screen started, until it stops.
  let waiting = $state<string | undefined>();

  type Row = { label: string; weight: number; own: boolean; color: string };
  // The form: undefined when closed; editing names the wheel being changed.
  let form = $state<{ editing?: string; name: string; title: string; seconds: number; rows: Row[] } | undefined>();
  let formError = $state('');

  const rowOf = (c: WheelChoice): Row => ({ label: c.label, weight: c.weight, own: !!c.color, color: c.color || '#e9c46a' });
  const formChoices = (f: NonNullable<typeof form>): WheelChoice[] =>
    f.rows.map((r) => ({ label: r.label.trim(), weight: Number(r.weight) || 1, ...(r.own ? { color: r.color } : {}) }));
  const formOdds = $derived(form ? percents(formChoices(form)) : []);

  async function load() {
    try {
      const r = await getWheels();
      wheels = r.wheels;
      spins = r.spins;
      pages = r.pages;
      loadError = '';
      if (waiting) {
        const sp = spins.find((s) => s.id === waiting) ?? (await getSpin(waiting));
        if (sp.settled_at) {
          notice = `${sp.title} landed on ${sp.label}.`;
          waiting = undefined;
        }
      }
    } catch (e) {
      loadError = message(e);
    } finally {
      loaded = true;
    }
  }

  onMount(() => {
    load();
    const t = setInterval(load, refreshMs);
    return () => clearInterval(t);
  });

  async function act(f: () => Promise<unknown>, done: string): Promise<boolean> {
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

  async function spin(w: Wheel) {
    busy = true;
    error = '';
    notice = '';
    try {
      const sp = await spinWheel(w.name, key());
      waiting = sp.id;
      notice = pages > 0 ? `${w.title} is spinning on your wheel overlay…` : `${w.title} is spinning…`;
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
      await load();
    }
  }

  function startAdd() {
    formError = '';
    form = {
      name: '',
      title: '',
      seconds: 6,
      rows: [rowOf({ label: '', weight: 1 }), rowOf({ label: '', weight: 1 })],
    };
  }

  function startEdit(w: Wheel) {
    formError = '';
    form = { editing: w.name, name: w.name, title: w.title, seconds: w.spin_seconds, rows: w.choices.map(rowOf) };
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!form) return;
    formError = '';
    const f = form;
    if (!f.editing && !NAME.test(f.name.trim())) {
      formError = 'A name is lowercase letters, digits, - and _, like next-game.';
      return;
    }
    const s = { title: f.title.trim(), choices: formChoices(f), spin_seconds: Number(f.seconds) || 6 };
    const ok = await act(
      () => (f.editing ? setWheel(f.editing, s) : addWheel(f.name.trim(), s)),
      f.editing ? `Saved ${s.title}.` : `Added ${s.title}.`,
    );
    if (ok) form = undefined;
    else formError = error;
  }

  async function remove(w: Wheel) {
    deleting = undefined;
    await act(() => deleteWheel(w.name), `Deleted ${w.title}. Its spins are kept.`);
  }

  function check(sp: WheelSpin) {
    checked = { id: sp.id, problems: verifySpin(sp) };
  }

  const displayWords: Record<string, string> = {
    shown: 'shown on the wheel overlay',
    no_audience: 'no wheel overlay was open',
    recovered: 'StreamGremlin stopped while it spun; the result stands',
  };
</script>

<h1>Wheels</h1>
<p class="sg-muted screen-intro">
  A wheel of choices, like the next game or a challenge. StreamGremlin draws each spin with a secure random source and records
  it, so it can be checked; a wheel overlay in OBS spins to that result, and each wedge is drawn at its real size, so the odds you
  see are the real odds. Workflows can spin a wheel from a chat command or a channel point reward with “Spin a wheel”.
</p>

{#if loadError}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
{/if}
{#if loaded && !loadError && pages === 0}
  <p class="sg-muted">No wheel overlay is open in OBS, so spins stop at once. To show them on stream, add a wheel overlay on the Overlays screen.</p>
{/if}

<section class="sg-panel card" aria-labelledby="wheels-heading" aria-busy={busy}>
  <h2 id="wheels-heading">Your wheels</h2>
  {#if loaded && wheels.length === 0}
    <p class="sg-muted">No wheels yet.</p>
  {/if}
  {#each wheels as w (w.name)}
    <article class="wheel-card" aria-labelledby={`wheel-${w.name}`}>
      <h3 id={`wheel-${w.name}`}>{w.title} <span class="sg-muted">({w.name})</span></h3>
      <table class="odds">
        <caption class="visually-hidden">{w.title}: the odds</caption>
        <thead><tr><th scope="col">Choice</th><th scope="col">Weight</th><th scope="col">Chance</th></tr></thead>
        <tbody>
          {#each w.odds as o, i (i)}
            <tr><td>{o.label}</td><td>{o.weight}</td><td>{percentWords(o.percent)}</td></tr>
          {/each}
        </tbody>
      </table>
      <div class="row-actions">
        <button type="button" class="sg-button" disabled={busy || !!waiting} onclick={() => spin(w)}>Spin</button>
        <button type="button" class="button" disabled={busy} onclick={() => startEdit(w)}>Edit</button>
        <button type="button" class="button button-danger" disabled={busy} onclick={() => (deleting = w)}>Delete</button>
      </div>
    </article>
  {/each}
  {#if !form}
    <div class="row-actions"><button type="button" class="button" disabled={busy} onclick={startAdd}>Add a wheel</button></div>
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error && !form}<p class="error" role="alert">{error}</p>{/if}
</section>

{#if form}
  <section class="sg-panel card" aria-labelledby="wheel-form-heading">
    <h2 id="wheel-form-heading">{form.editing ? `Change ${form.title || form.editing}` : 'Add a wheel'}</h2>
    <form class="test-form" onsubmit={save} aria-label={form.editing ? 'Change the wheel' : 'Add a wheel'}>
      {#if !form.editing}
        <label class="field">
          <span>Name</span>
          <input class="sg-input" bind:value={form.name} maxlength="64" spellcheck="false" placeholder="next-game" required />
          <span class="sg-muted field-hint">Workflows spin it by this name.</span>
        </label>
      {/if}
      <label class="field"><span>Title</span><input class="sg-input" bind:value={form.title} maxlength="100" placeholder="Next game" required /></label>
      <label class="field">
        <span>Spin time (seconds)</span>
        <input class="sg-input" type="number" min="2" max="20" bind:value={form.seconds} />
      </label>
      <fieldset class="field">
        <legend>Choices</legend>
        <p class="sg-muted field-hint">A weight of 2 is twice as likely as 1. The chance is worked out as you type.</p>
        {#each form.rows as r, i (i)}
          <div class="choice-row">
            <label class="field">
              <span>Choice {i + 1}</span>
              <input class="sg-input" bind:value={r.label} maxlength="60" required />
            </label>
            <label class="field weight-field">
              <span>Weight</span>
              <input class="sg-input" type="number" min="1" max="1000" bind:value={r.weight} />
            </label>
            <span class="sg-muted chance" aria-label={`Choice ${i + 1}'s chance`}>{percentWords(formOdds[i] ?? 0)}</span>
            <label class="choice-inline"><input type="checkbox" bind:checked={r.own} /> Own colour</label>
            {#if r.own}
              <label class="field"><span class="visually-hidden">Choice {i + 1}'s colour</span><input type="color" bind:value={r.color} /></label>
            {/if}
            <button type="button" class="button" aria-label={`Remove choice ${i + 1}`} disabled={form.rows.length <= 2}
              onclick={() => form && (form.rows = form.rows.filter((_, j) => j !== i))}>Remove</button
            >
          </div>
        {/each}
        <button type="button" class="button" disabled={form.rows.length >= 50}
          onclick={() => form && (form.rows = [...form.rows, rowOf({ label: '', weight: 1 })])}>Add a choice</button
        >
      </fieldset>
      {#if formError}<p class="error" role="alert">{formError}</p>{/if}
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={busy}>{form.editing ? 'Save' : 'Add'}</button>
        <button type="button" class="button" onclick={() => (form = undefined)}>Cancel</button>
      </div>
    </form>
  </section>
{/if}

{#if spins.length}
  <section class="sg-panel card" aria-labelledby="spins-heading">
    <h2 id="spins-heading">Recent spins</h2>
    <ul class="live-list" aria-label="Recent spins">
      {#each spins as sp (sp.id)}
        <li>
          <strong>{sp.title}</strong>:
          {#if sp.settled_at}{sp.label}{:else}spinning…{/if}
          <span class="sg-muted">
            {eventTime(sp.spun_at)}, by {sp.started_by}{sp.settled_at && sp.display ? `, ${displayWords[sp.display] ?? sp.display}` : ''} ({howSpun(sp)})
          </span>
          {#if sp.settled_at}<button type="button" class="button" onclick={() => check(sp)}>Check the spin</button>{/if}
          {#if checked?.id === sp.id}
            {#if checked.problems.length === 0}
              <p class="state state-ok" role="status">
                <span aria-hidden="true">✓</span> Number {sp.draw + 1} of {sp.total} lands on {sp.label}, as recorded.
              </p>
            {:else}
              <p class="error" role="alert">{checked.problems.join(' ')}</p>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
    <p class="sg-muted">The random number itself can’t be replayed: it comes from your system’s secure random source.</p>
  </section>
{/if}

{#if deleting}
  {@const w = deleting}
  <ConfirmDialog
    title={`Delete ${w.title}?`}
    message="Workflows that spin it fail until you add a wheel with that name again. Its spins are kept."
    confirmLabel="Delete"
    onconfirm={() => remove(w)}
    oncancel={() => (deleting = undefined)}
  />
{/if}

<style>
  .wheel-card { margin-block: 0.75rem 1.25rem; }
  .odds { border-collapse: collapse; margin-block: 0.5rem; }
  .odds th, .odds td { padding: 0.2rem 0.75rem 0.2rem 0; text-align: left; }
  .choice-row { display: flex; flex-wrap: wrap; align-items: end; gap: 0.5rem 0.75rem; margin-block-end: 0.5rem; }
  .weight-field input { width: 6rem; }
  .chance { min-width: 4rem; padding-block-end: 0.5rem; }
</style>
