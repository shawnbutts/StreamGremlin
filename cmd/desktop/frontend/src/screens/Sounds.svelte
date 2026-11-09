<script lang="ts">
  // Sounds: the library (docs/adr/0016-sounds.md). Each sound has its own
  // volume. This window plays them for workflows, and says when it can't.
  import { onMount } from 'svelte';
  import { ApiError, addSound, deleteSound, getSounds, playSound, setSoundVolume, stopSounds, type Sound } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';
  import { eventTime } from '../lib/events';

  let { refreshMs = 3000 }: { refreshMs?: number } = $props();

  const MAX_BYTES = 5 * 1024 * 1024;
  const NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/;

  let list = $state<Sound[]>([]);
  let player = $state(false);
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let file = $state<File | undefined>();
  let name = $state('');
  let volume = $state(80);
  let formError = $state('');
  let deleting = $state<Sound | undefined>();
  // Volumes being dragged, saved when let go.
  let volumes = $state<Record<string, number>>({});

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const kb = (n: number) => (n < 1e6 ? `${Math.max(1, Math.round(n / 1e3))} KB` : `${(n / 1e6).toFixed(1)} MB`);

  /** "Air Horn!.mp3" → "air-horn", as the engine would. */
  function nameFromFile(f: string) {
    const base = f.replace(/\.[^.]*$/, '').toLowerCase();
    const n = base.replace(/[^a-z0-9_]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64).replace(/-+$/, '');
    return n || 'sound';
  }

  async function load() {
    try {
      const r = await getSounds();
      list = r.sounds;
      player = r.player;
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
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

  function pick(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    file = f;
    formError = '';
    if (f) name = nameFromFile(f.name);
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    if (!file) return;
    if (file.size === 0) {
      formError = 'That file is empty.';
      return;
    }
    if (file.size > MAX_BYTES) {
      formError = 'A sound is up to 5 MB.';
      return;
    }
    if (!NAME.test(name)) {
      formError = 'A name is lowercase letters, digits, - and _, like airhorn.';
      return;
    }
    const n = name;
    if (await act(() => addSound(n, file!, volume), `Added ${n}.`)) {
      file = undefined;
      name = '';
      const input = document.getElementById('sound-file') as HTMLInputElement | null;
      if (input) input.value = '';
    }
  }

  async function saveVolume(s: Sound) {
    const v = volumes[s.name];
    if (v === undefined || v === s.volume) return;
    await act(() => setSoundVolume(s.name, v), `${s.name} plays at volume ${v}.`);
    delete volumes[s.name];
  }

  function remove(s: Sound) {
    deleting = undefined;
    act(() => deleteSound(s.name), `Deleted ${s.name}.`);
  }
</script>

<h1>Sounds</h1>
<p class="sg-muted screen-intro">
  Your sound library. Workflows play these with “Play a sound”, each at its own volume, in this window, so keep
  StreamGremlin open; OBS hears them only if it captures your computer’s sound. A sound with “Show an alert” plays in the
  overlay instead, so OBS hears it through the browser source.
</p>

<section class="sg-panel card" aria-labelledby="sounds-heading" aria-busy={busy}>
  <h2 id="sounds-heading">Your sounds</h2>
  <p class="state">
    {#if player}
      <span class="state-ok"><span aria-hidden="true">●</span></span> This window plays sounds for your workflows.
    {:else}
      <span class="state-unknown"><span aria-hidden="true">?</span></span> No StreamGremlin window is playing sounds yet. If this
      stays, restart the app.
    {/if}
  </p>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if list.length === 0}
    <p class="sg-muted">No sounds yet. Add an MP3, WAV, OGG or M4A file below.</p>
  {/if}
  <ul class="secret-list" aria-label="Sounds">
    {#each list as s (s.name)}
      <li class="secret-row">
        <span class="secret-info">
          <strong>{s.name}</strong>
          <span class="sg-muted secret-saved">{s.format.toUpperCase()}, {kb(s.size)}, added {eventTime(s.created_at)}</span>
        </span>
        <label class="field sound-volume">
          <span>Volume {volumes[s.name] ?? s.volume}</span>
          <input
            type="range"
            min="0"
            max="100"
            step="5"
            value={volumes[s.name] ?? s.volume}
            aria-label={`Volume of ${s.name}`}
            oninput={(e) => (volumes[s.name] = Number(e.currentTarget.value))}
            onchange={() => saveVolume(s)}
          />
        </label>
        <button type="button" class="button" aria-label={`Play ${s.name}`} disabled={busy}
          onclick={() => act(() => playSound(s.name), `Playing ${s.name}.`)}>Play</button
        >
        <button type="button" class="button button-danger" aria-label={`Delete ${s.name}`} disabled={busy} onclick={() => (deleting = s)}
          >Delete</button
        >
      </li>
    {/each}
  </ul>
  {#if list.length}
    <div class="row-actions">
      <button type="button" class="button" disabled={busy} onclick={() => act(stopSounds, 'Stopped every sound.')}>Stop all sounds</button>
    </div>
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<section class="sg-panel card" aria-labelledby="sound-add-heading">
  <h2 id="sound-add-heading">Add a sound</h2>
  <form class="test-form" onsubmit={add}>
    <label class="field">
      <span>File (MP3, WAV, OGG or M4A, up to 5 MB)</span>
      <input id="sound-file" class="sg-input" type="file" accept=".mp3,.wav,.ogg,.m4a,audio/*" onchange={pick} />
    </label>
    <label class="field">
      <span>Name</span>
      <input class="sg-input" bind:value={name} maxlength="64" spellcheck="false" placeholder="airhorn" />
      <span class="sg-muted field-hint">Workflows find it by this name.</span>
    </label>
    <label class="field">
      <span>Volume {volume}</span>
      <input type="range" min="0" max="100" step="5" bind:value={volume} aria-label="Volume of the new sound" />
    </label>
    {#if formError}<p class="error" role="alert">{formError}</p>{/if}
    <div class="row-actions">
      <button type="submit" class="sg-button" disabled={busy || !file}>Add</button>
    </div>
  </form>
</section>

{#if deleting}
  {@const s = deleting}
  <ConfirmDialog
    title={`Delete ${s.name}?`}
    message={`${s.name} is deleted for good. Workflows that play it fail until you add a sound with that name again.`}
    confirmLabel="Delete"
    onconfirm={() => remove(s)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
