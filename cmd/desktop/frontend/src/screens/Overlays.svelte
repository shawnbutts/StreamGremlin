<script lang="ts">
  // Overlays (docs/adr/0017-alert-overlays.md): each is one browser source
  // in OBS, with its own private link. Workflows show alerts on them with
  // "Show an alert". The preview is the overlay page itself, in a frame.
  import { onMount } from 'svelte';
  import {
    ApiError,
    addOverlay,
    deleteOverlay,
    getOverlayLink,
    getOverlays,
    newOverlayLink,
    setOverlayLook,
    testOverlay,
    type Overlay,
    type OverlayList,
    type OverlayLook,
  } from '../lib/api';
  import ConfirmDialog from '../lib/ConfirmDialog.svelte';

  let { refreshMs = 3000 }: { refreshMs?: number } = $props();

  const NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/;
  const POSITION_LABELS: Record<string, string> = {
    'top-left': 'Top left',
    top: 'Top middle',
    'top-right': 'Top right',
    center: 'Center',
    'bottom-left': 'Bottom left',
    bottom: 'Bottom middle',
    'bottom-right': 'Bottom right',
  };

  let data = $state<OverlayList | undefined>();
  let loadError = $state('');
  let notice = $state('');
  let error = $state('');
  let busy = $state(false);
  let newName = $state('alerts');
  let formError = $state('');
  let selected = $state('');
  let look = $state<OverlayLook | undefined>();
  let lookFor = $state('');
  let previewURL = $state('');
  let previewFor = $state('');
  let linkProblem = $state('');
  let shownLink = $state('');
  let deleting = $state<Overlay | undefined>();
  let replacing = $state<Overlay | undefined>();
  let frameWidth = $state(0);

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));
  const current = $derived(data?.overlays.find((o) => o.name === selected));
  const changed = $derived(!!current && !!look && JSON.stringify(look) !== JSON.stringify(current.look));

  async function load() {
    try {
      data = await getOverlays();
      loadError = '';
      if (!data.overlays.some((o) => o.name === selected)) selected = data.overlays[0]?.name ?? '';
    } catch (e) {
      loadError = message(e);
    }
  }

  onMount(() => {
    load();
    const id = setInterval(load, refreshMs);
    return () => clearInterval(id);
  });

  // The look being edited, and the preview, follow the chosen overlay.
  $effect(() => {
    const o = current;
    if (o && lookFor !== o.name) {
      look = { ...o.look };
      lookFor = o.name;
    }
  });
  $effect(() => {
    const name = selected;
    if (!name || previewFor === name) return;
    previewFor = name;
    previewURL = '';
    linkProblem = '';
    shownLink = '';
    getOverlayLink(name)
      .then((l) => {
        if (selected === name) previewURL = `${l.url}?preview=1`;
      })
      .catch((e) => {
        if (selected === name) linkProblem = message(e);
      });
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
    formError = '';
    const n = newName.trim();
    if (!NAME.test(n)) {
      formError = 'A name is lowercase letters, digits, - and _, like alerts.';
      return;
    }
    if (await act(() => addOverlay(n), `Added ${n}. Copy its link into OBS as a browser source.`)) {
      selected = n;
      newName = '';
    }
  }

  async function copyLink(o: Overlay) {
    error = '';
    notice = '';
    shownLink = '';
    try {
      const l = await getOverlayLink(o.name);
      try {
        await navigator.clipboard.writeText(l.url);
        notice = `Copied ${o.name}'s link. In OBS: Sources, +, Browser, paste it as the URL, width 1920, height 1080.`;
      } catch {
        // Some systems refuse the clipboard: show it to copy by hand.
        selected = o.name;
        shownLink = l.url;
      }
    } catch (e) {
      error = message(e);
    }
  }

  function remove(o: Overlay) {
    deleting = undefined;
    act(() => deleteOverlay(o.name), `Deleted ${o.name}. Remove its browser source from OBS too.`);
  }

  async function replace(o: Overlay) {
    replacing = undefined;
    if (await act(() => newOverlayLink(o.name), `${o.name} has a new link. Copy it into OBS: the old one no longer works.`)) {
      selected = o.name;
      previewFor = ''; // the preview reloads with the new link
    }
  }

  async function saveLook(e: SubmitEvent) {
    e.preventDefault();
    if (!current || !look) return;
    const name = current.name;
    const l = { ...look, opacity: Number(look.opacity), seconds: Number(look.seconds) };
    if (await act(() => setOverlayLook(name, l), `Saved ${name}'s look.`)) lookFor = '';
  }

  function openState(o: Overlay) {
    return o.pages > 0 ? `Open in OBS${o.pages > 1 ? ` (${o.pages} pages)` : ''}` : 'Not open in OBS';
  }
</script>

<h1>Overlays</h1>
<p class="sg-muted screen-intro">
  Each overlay is one browser source in OBS. Workflows show alerts on them with “Show an alert”, one at a time. Keep links
  private: anyone with one can see that overlay.
</p>

<section class="sg-panel card" aria-labelledby="overlays-heading" aria-busy={busy}>
  <h2 id="overlays-heading">Your overlays</h2>
  {#if loadError}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
  {:else if data && !data.server}
    <p class="state state-unknown"><span aria-hidden="true">?</span> The overlay server is off, so OBS can't show overlays.</p>
  {:else if data?.server_note}
    <p class="state state-unknown" role="alert"><span aria-hidden="true">!</span> {data.server_note}</p>
  {/if}
  {#if data && data.overlays.length === 0}
    <p class="sg-muted">No overlays yet. Add one below, then paste its link into OBS.</p>
  {/if}
  <ul class="secret-list" aria-label="Overlays">
    {#each data?.overlays ?? [] as o (o.name)}
      <li class="secret-row" class:overlay-chosen={o.name === selected}>
        <span class="secret-info">
          <strong>{o.name}</strong>
          <span class="secret-saved">
            {#if o.pages > 0}
              <span class="state-ok"><span aria-hidden="true">●</span></span>
            {:else}
              <span class="state-unknown"><span aria-hidden="true">○</span></span>
            {/if}
            {openState(o)} · {POSITION_LABELS[o.look.position] ?? o.look.position}, {o.look.animation}
          </span>
        </span>
        <button type="button" class="button" aria-label={`Edit ${o.name}`} aria-pressed={o.name === selected}
          onclick={() => (selected = o.name)}>Edit</button
        >
        <button type="button" class="button" aria-label={`Copy ${o.name}'s link`} disabled={busy} onclick={() => copyLink(o)}
          >Copy link</button
        >
        <button type="button" class="button" aria-label={`Show a sample alert on ${o.name} in OBS`} disabled={busy}
          onclick={() => act(() => testOverlay(o.name), `A sample alert is showing on ${o.name} in OBS.`)}>Sample in OBS</button
        >
        <button type="button" class="button" aria-label={`New link for ${o.name}`} disabled={busy} onclick={() => (replacing = o)}
          >New link</button
        >
        <button type="button" class="button button-danger" aria-label={`Delete ${o.name}`} disabled={busy} onclick={() => (deleting = o)}
          >Delete</button
        >
      </li>
    {/each}
  </ul>
  {#if shownLink}
    <label class="field">
      <span>{selected}'s link: copy it, and keep it private</span>
      <input class="sg-input" readonly value={shownLink} onfocus={(e) => e.currentTarget.select()} />
    </label>
  {/if}
  <p class="saved" role="status">{notice}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

{#if current && look}
  <section class="sg-panel card" aria-labelledby="overlay-look-heading">
    <h2 id="overlay-look-heading">{current.name}: look and preview</h2>
    <div class="overlay-edit">
      <form class="test-form overlay-look" onsubmit={saveLook}>
        <label class="field">
          <span>Where alerts appear</span>
          <select class="sg-input" bind:value={look.position}>
            {#each data?.positions ?? [] as p (p)}
              <option value={p}>{POSITION_LABELS[p] ?? p}</option>
            {/each}
          </select>
        </label>
        <label class="field">
          <span>How they come in</span>
          <select class="sg-input" bind:value={look.animation}>
            <option value="slide">Slide in</option>
            <option value="pop">Pop in</option>
          </select>
        </label>
        <label class="field">
          <span>Text color</span>
          <input class="sg-input color-input" type="color" bind:value={look.text_color} />
        </label>
        <label class="field">
          <span>Background color</span>
          <input class="sg-input color-input" type="color" bind:value={look.background} />
        </label>
        <label class="field">
          <span>Background opacity {look.opacity}%</span>
          <input type="range" min="0" max="100" step="5" bind:value={look.opacity} />
        </label>
        <label class="field">
          <span>Seconds on screen</span>
          <input class="sg-input" type="number" min="2" max="60" bind:value={look.seconds} />
          <span class="sg-muted field-hint">A step can choose its own.</span>
        </label>
        <div class="row-actions">
          <button type="submit" class="sg-button" disabled={busy || !changed}>Save look</button>
          <button type="button" class="button" disabled={busy || !previewURL}
            onclick={() => act(() => testOverlay(current.name, true), 'A sample alert is showing in the preview.')}>Preview an alert</button
          >
        </div>
        {#if changed}<p class="sg-muted">Save to see the change in the preview and in OBS.</p>{/if}
      </form>
      <div class="overlay-preview-wrap">
        {#if previewURL}
          <div class="overlay-preview" bind:clientWidth={frameWidth}>
            <iframe
              title={`Preview of ${current.name}`}
              src={previewURL}
              width="1920"
              height="1080"
              referrerpolicy="no-referrer"
              sandbox="allow-scripts allow-same-origin"
              style:transform={`scale(${frameWidth / 1920 || 0.25})`}
            ></iframe>
          </div>
          <p class="sg-muted field-hint">The preview shows tests and samples; OBS never does.</p>
        {:else if linkProblem}
          <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> No preview: {linkProblem}</p>
        {:else}
          <p class="sg-muted">Loading the preview…</p>
        {/if}
      </div>
    </div>
  </section>
{/if}

<section class="sg-panel card" aria-labelledby="overlay-add-heading">
  <h2 id="overlay-add-heading">Add an alert overlay</h2>
  <form class="test-form" onsubmit={add}>
    <label class="field">
      <span>Name</span>
      <input class="sg-input" bind:value={newName} maxlength="64" spellcheck="false" placeholder="alerts" />
      <span class="sg-muted field-hint">Workflows show alerts on it by this name. One overlay usually does; add more for different places on screen.</span>
    </label>
    {#if formError}<p class="error" role="alert">{formError}</p>{/if}
    <div class="row-actions">
      <button type="submit" class="sg-button" disabled={busy}>Add</button>
    </div>
  </form>
</section>

{#if deleting}
  {@const o = deleting}
  <ConfirmDialog
    title={`Delete ${o.name}?`}
    message={`Its link stops working, so OBS shows nothing there. Workflows that show alerts on it fail until you add an overlay with that name again.`}
    confirmLabel="Delete"
    onconfirm={() => remove(o)}
    oncancel={() => (deleting = undefined)}
  />
{/if}
{#if replacing}
  {@const o = replacing}
  <ConfirmDialog
    title={`Make a new link for ${o.name}?`}
    message={`The old link stops working at once. Paste the new one into OBS's browser source. Do this if the link was seen by someone else.`}
    confirmLabel="Make a new link"
    onconfirm={() => replace(o)}
    oncancel={() => (replacing = undefined)}
  />
{/if}
