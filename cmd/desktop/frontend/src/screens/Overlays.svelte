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
  import OverlayPreview from '../lib/OverlayPreview.svelte';

  let { refreshMs = 3000 }: { refreshMs?: number } = $props();

  const NAME = /^[a-z0-9][a-z0-9_-]{0,63}$/;
  const ANIMATION_LABELS: Record<string, string> = { slide: 'slide in', pop: 'pop in', unfold: 'picture, then words' };
  const FONT_NAME = /^[A-Za-z0-9][A-Za-z0-9 _.&'-]{0,63}$/;
  const FONT_LABELS: Record<string, string> = {
    system: 'Clean (your system’s)',
    rounded: 'Rounded',
    serif: 'Serif',
    mono: 'Typewriter',
    condensed: 'Bold and narrow',
    handwritten: 'Handwritten',
    custom: 'A font on this computer…',
  };
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
  let lookError = $state('');
  let selected = $state('');
  let look = $state<OverlayLook | undefined>();
  let lookFor = $state('');
  let previewLoaded = $state(false);
  let previewReload = $state(0);
  let shownLink = $state('');
  let deleting = $state<Overlay | undefined>();
  let replacing = $state<Overlay | undefined>();

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
      previewReload++; // the preview loads the new link
    }
  }

  /** The form's look, as numbers, or why it can't be used yet. */
  function formLook(): OverlayLook | undefined {
    if (!look) return undefined;
    lookError = '';
    if (look.font === 'custom' && !FONT_NAME.test(look.font_name.trim())) {
      lookError = 'Type the name of a font installed on this computer, like Comic Neue.';
      return undefined;
    }
    const n = (v: unknown) => Number(v);
    return {
      ...look,
      font_name: look.font === 'custom' ? look.font_name.trim() : look.font_name,
      seconds: n(look.seconds), opacity: n(look.opacity), gradient_angle: n(look.gradient_angle), radius: n(look.radius),
      border_width: n(look.border_width), shadow: n(look.shadow), text_size: n(look.text_size),
      text_outline: n(look.text_outline), text_shadow: n(look.text_shadow),
    };
  }

  function setGradient(on: boolean) {
    if (!look) return;
    look.background2 = on ? look.background2 || '#ff0080' : '';
  }

  async function saveLook(e: SubmitEvent) {
    e.preventDefault();
    if (!current) return;
    const name = current.name;
    const l = formLook();
    if (!l) return;
    if (await act(() => setOverlayLook(name, l), `Saved ${name}'s look.`)) lookFor = '';
  }

  /** A sample in the preview, in the form's look, saved or not. */
  function previewLook() {
    if (!current) return;
    const name = current.name;
    const l = formLook();
    if (!l) return;
    act(() => testOverlay(name, true, l), changed ? 'A sample is showing in the preview, with your unsaved changes.' : 'A sample alert is showing in the preview.');
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
            {openState(o)} · {POSITION_LABELS[o.look.position] ?? o.look.position}, {ANIMATION_LABELS[o.look.animation] ?? o.look.animation}
          </span>
        </span>
        <button type="button" class="button" aria-label={`Edit ${o.name}`} aria-pressed={o.name === selected}
          onclick={() => {
            selected = o.name;
            shownLink = '';
          }}>Edit</button
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
        <fieldset class="look-group">
          <legend>Placement</legend>
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
              <option value="unfold">Picture, then words</option>
            </select>
          </label>
          <label class="field">
            <span>Seconds on screen</span>
            <input class="sg-input" type="number" min="2" max="60" bind:value={look.seconds} />
            <span class="sg-muted field-hint">A step can choose its own.</span>
          </label>
        </fieldset>

        <fieldset class="look-group">
          <legend>Box</legend>
          <div class="color-row">
            <label class="field">
              <span>Background</span>
              <input class="sg-input color-input" type="color" bind:value={look.background} />
            </label>
            <label class="choice-inline">
              <input type="checkbox" checked={!!look.background2} onchange={(e) => setGradient(e.currentTarget.checked)} />
              Gradient
            </label>
            {#if look.background2}
              <label class="field">
                <span>To</span>
                <input class="sg-input color-input" type="color" bind:value={look.background2} />
              </label>
            {/if}
          </div>
          {#if look.background2}
            <label class="field">
              <span>Gradient direction {look.gradient_angle}°</span>
              <input type="range" min="0" max="359" step="15" bind:value={look.gradient_angle} />
            </label>
          {/if}
          <label class="field">
            <span>Background opacity {look.opacity}%</span>
            <input type="range" min="0" max="100" step="5" bind:value={look.opacity} />
          </label>
          <label class="field">
            <span>Corner roundness {look.radius}</span>
            <input type="range" min="0" max="48" step="2" bind:value={look.radius} />
          </label>
          <div class="color-row">
            <label class="field">
              <span>Outline {look.border_width ? `${look.border_width} px` : 'none'}</span>
              <input type="range" min="0" max="12" step="1" bind:value={look.border_width} />
            </label>
            <label class="field">
              <span>Outline color</span>
              <input class="sg-input color-input" type="color" bind:value={look.border_color} />
            </label>
          </div>
          <div class="color-row">
            <label class="field">
              <span>Drop shadow {look.shadow ? look.shadow : 'none'}</span>
              <input type="range" min="0" max="100" step="5" bind:value={look.shadow} />
            </label>
            <label class="field">
              <span>Shadow color</span>
              <input class="sg-input color-input" type="color" bind:value={look.shadow_color} />
              <span class="sg-muted field-hint">For the box's and the words' shadows.</span>
            </label>
          </div>
        </fieldset>

        <fieldset class="look-group">
          <legend>Text</legend>
          <label class="field">
            <span>Font</span>
            <select class="sg-input" bind:value={look.font}>
              {#each data?.fonts ?? [] as f (f)}
                <option value={f}>{FONT_LABELS[f] ?? f}</option>
              {/each}
            </select>
          </label>
          {#if look.font === 'custom'}
            <label class="field">
              <span>Font name</span>
              <input class="sg-input" bind:value={look.font_name} maxlength="64" spellcheck="false" placeholder="Comic Neue" />
              <span class="sg-muted field-hint">Any font installed on this computer, as your other apps name it. OBS uses the same fonts.</span>
            </label>
          {/if}
          <label class="field">
            <span>Text size {look.text_size}%</span>
            <input type="range" min="50" max="200" step="10" bind:value={look.text_size} />
          </label>
          <div class="color-row">
            <label class="field">
              <span>Text color</span>
              <input class="sg-input color-input" type="color" bind:value={look.text_color} />
            </label>
            <label class="field">
              <span>Title color</span>
              <input class="sg-input color-input" type="color" bind:value={look.title_color} />
            </label>
          </div>
          <div class="color-row">
            <label class="field">
              <span>Letter outline {look.text_outline ? `${look.text_outline} px` : 'none'}</span>
              <input type="range" min="0" max="6" step="1" bind:value={look.text_outline} />
            </label>
            <label class="field">
              <span>Letter outline color</span>
              <input class="sg-input color-input" type="color" bind:value={look.outline_color} />
            </label>
          </div>
          <label class="field">
            <span>Text shadow {look.text_shadow ? look.text_shadow : 'none'}</span>
            <input type="range" min="0" max="100" step="5" bind:value={look.text_shadow} />
          </label>
        </fieldset>

        {#if lookError}<p class="error" role="alert">{lookError}</p>{/if}
        <div class="row-actions">
          <button type="submit" class="sg-button" disabled={busy || !changed}>Save look</button>
          <button type="button" class="button" disabled={busy || !previewLoaded} onclick={previewLook}>Preview an alert</button>
        </div>
        {#if changed}<p class="sg-muted">Preview an alert shows your changes here. Save to use them in OBS.</p>{/if}
      </form>
      <div class="overlay-preview-wrap">
        <OverlayPreview name={current.name} reload={previewReload} bind:loaded={previewLoaded} />
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
