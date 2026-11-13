<script lang="ts">
  // Overlays (docs/adr/0017-alert-overlays.md): each is one browser source
  // in OBS, with its own private link. Workflows show alerts on them with
  // "Show an alert". The preview is the overlay page itself, in a frame.
  import { onMount } from 'svelte';
  import {
    ApiError,
    addOverlay,
    chooseFile,
    deleteOverlay,
    getOverlayLink,
    getOverlays,
    getSounds,
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
  let newKind = $state<'alert' | 'giveaway' | 'wheel' | 'song'>('alert');
  const isGiveaway = (o?: Overlay) => o?.kind === 'giveaway';
  const isWheel = (o?: Overlay) => o?.kind === 'wheel';
  const isSong = (o?: Overlay) => o?.kind === 'song';
  // What a sample shows on each kind.
  const sampleOf = (o?: Overlay) => (isGiveaway(o) ? 'giveaway and draw are' : isWheel(o) ? 'spin is' : isSong(o) ? 'song is' : 'alert is');
  const sampleWord = (o?: Overlay) => (isGiveaway(o) ? 'giveaway' : isWheel(o) ? 'spin' : isSong(o) ? 'song' : 'alert');
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
  // A wheel overlay's look, when it isn't Classic.
  const chosenTheme = $derived(look?.theme ? data?.themes?.find((t) => t.id === look?.theme) : undefined);

  function setTheme(id: string) {
    if (!look) return;
    if (!id) {
      look.theme = ''; // Classic, said outright: a missing one keeps the saved look
      delete look.theme_settings;
      return;
    }
    look.theme = id;
    look.theme_settings = {}; // each look's own settings, from its defaults
  }

  const settingValue = (key: string, dflt: string | boolean | number) => look?.theme_settings?.[key] ?? dflt;
  // A look with its own sounds plays them for any cue left at "No sound" (D-065).
  const themeSoundsSetting = $derived(chosenTheme?.settings.find((s) => s.key === 'sounds'));
  const themeSounds = $derived(Boolean(themeSoundsSetting && settingValue('sounds', themeSoundsSetting.default) === true));
  function setSetting(key: string, v: string | boolean | number) {
    if (look) look.theme_settings = { ...(look.theme_settings ?? {}), [key]: v };
  }

  // The sound library's names, for giveaway and wheel overlays' sounds.
  let soundNames = $state<string[]>([]);

  async function load() {
    try {
      data = await getOverlays();
      if (data.overlays.some((o) => o.kind !== 'alert')) {
        soundNames = (await getSounds().catch(() => ({ sounds: [] }))).sounds.map((s) => s.name);
      }
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
    // A Now playing overlay starts in a corner, its bars beside the song.
    const start = newKind === 'song' && data ? { ...data.default_look, position: 'bottom-left' as const, animation: 'unfold' as const } : undefined;
    if (await act(() => addOverlay(n, start, newKind), `Added ${n}. Copy its link into OBS as a browser source.`)) {
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
        notice = `Copied ${o.name}'s link. In OBS: Sources, +, Browser, paste it as the URL, width 1920, height 1080, and tick “Control audio via OBS” so alert sounds reach your stream.`;
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

  /** 100 → "usual", 150 → "1.5× slower", 50 → "2× faster". */
  function paceWords(p: number) {
    const n = Number(p) || 100;
    if (n === 100) return 'usual';
    const x = n > 100 ? n / 100 : 100 / n;
    return `${Number.isInteger(x) ? x : x.toFixed(1)}× ${n > 100 ? 'slower' : 'faster'}`;
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
    // A path pasted with quotes around it, as Terminal copies one, works too.
    const path = (v?: string) => (v ?? '').trim().replace(/^(['"])(.*)\1$/, '$2');
    const files = isSong(current)
      ? { song_file: path(look.song_file), art_file: path(look.art_file), paused_text: (look.paused_text ?? '').trim() }
      : {};
    return {
      ...look,
      ...files,
      font_name: look.font === 'custom' ? look.font_name.trim() : look.font_name,
      seconds: n(look.seconds), pace: n(look.pace ?? 100), opacity: n(look.opacity), gradient_angle: n(look.gradient_angle), radius: n(look.radius),
      border_width: n(look.border_width), shadow: n(look.shadow), text_size: n(look.text_size),
      text_outline: n(look.text_outline), text_shadow: n(look.text_shadow),
      start_volume: n(look.start_volume ?? 80), end_volume: n(look.end_volume ?? 80), cheer_volume: n(look.cheer_volume ?? 80),
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
  /** A Now playing overlay's file, chosen with the system's own dialog. */
  async function browse(kind: 'song_text' | 'song_art') {
    if (!look) return;
    lookError = '';
    try {
      const p = await chooseFile(kind);
      if (!p) return; // cancelled
      if (kind === 'song_text') look.song_file = p;
      else look.art_file = p;
    } catch (e) {
      lookError = message(e);
    }
  }

  function previewLook() {
    if (!current) return;
    const name = current.name;
    const l = formLook();
    if (!l) return;
    if (isGiveaway(current) || isWheel(current)) {
      act(() => testOverlay(name, true), `A sample ${sampleOf(current)} showing in the preview, in the saved look.`);
      return;
    }
    act(() => testOverlay(name, true, l), changed ? 'A sample is showing in the preview, with your unsaved changes.' : `A sample ${sampleOf(current)} showing in the preview.`);
  }

  function openState(o: Overlay) {
    return o.pages > 0 ? `Open in OBS${o.pages > 1 ? ` (${o.pages} pages)` : ''}` : 'Not open in OBS';
  }
</script>

<h1>Overlays</h1>
<p class="sg-muted screen-intro">
  Each overlay is one browser source in OBS. Alert overlays show what workflows send with “Show an alert”, one at a time. A
  giveaway overlay shows the running giveaway and rolls to each winner by itself; chat still tells viewers everything. A Now playing
  overlay shows the song Pretzel's desktop app is playing, from the files it writes. An alert’s
  sound plays in the overlay, so OBS hears it through the browser source (tick “Control audio via OBS” on it). Keep links
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
          {#if isGiveaway(o)}<span class="tag">Giveaway</span>{/if}
          {#if isWheel(o)}<span class="tag">Wheel</span>{/if}
          {#if isSong(o)}<span class="tag">Now playing</span>{/if}
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
        <button type="button" class="button" aria-label={`Show a sample ${sampleWord(o)} on ${o.name} in OBS`}
          disabled={busy}
          onclick={() =>
            act(() => testOverlay(o.name), `A sample ${sampleOf(o)} showing on ${o.name} in OBS.`)}
          >Sample in OBS</button
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
        {#if isWheel(current)}
          <fieldset class="look-group">
            <legend>Look</legend>
            <label class="field">
              <span>Look</span>
              <select class="sg-input" value={look.theme ?? ''} onchange={(e) => setTheme(e.currentTarget.value)}>
                <option value="">Classic</option>
                {#each data?.themes ?? [] as t (t.id)}<option value={t.id}>{t.name}{t.publisher ? ` (add-on)` : ""}</option>{/each}
              </select>
            </label>
            {#if chosenTheme}
              {#if chosenTheme.publisher}
                <p class="sg-muted field-hint">
                  An add-on: version {chosenTheme.version}, published by {chosenTheme.publisher} (not verified). It’s told the result
                  StreamGremlin drew, but what it draws is up to whoever made it: the real result is always in chat.
                </p>
              {/if}
              <p class="sg-muted field-hint">
                {chosenTheme.description}
                {chosenTheme.odds_checked
                  ? 'It shows the real odds: StreamGremlin’s tests check it.'
                  : 'StreamGremlin can’t check how this look draws the odds: the real odds are on the Wheels screen and in “Say a wheel’s odds”.'}
              </p>
              {#each chosenTheme.settings as s (s.key)}
                {#if s.kind === 'choice'}
                  <label class="field">
                    <span>{s.label}</span>
                    <select class="sg-input" value={String(settingValue(s.key, s.default))} onchange={(e) => setSetting(s.key, e.currentTarget.value)}>
                      {#each s.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
                    </select>
                  </label>
                  {#if s.hint}<p class="sg-muted field-hint">{s.hint}</p>{/if}
                {:else if s.kind === 'number'}
                  <label class="field">
                    <span>{s.label}</span>
                    <input class="sg-input" type="number" step="1" min={s.min ?? 0} max={s.max} value={Number(settingValue(s.key, s.default))}
                      oninput={(e) => {
                        const n = e.currentTarget.valueAsNumber;
                        if (Number.isInteger(n)) setSetting(s.key, n);
                      }} />
                  </label>
                  {#if s.hint}<p class="sg-muted field-hint">{s.hint}</p>{/if}
                {:else if s.kind === 'switch'}
                  <label class="choice-inline">
                    <input type="checkbox" checked={settingValue(s.key, s.default) === true} onchange={(e) => setSetting(s.key, e.currentTarget.checked)} />
                    {s.label}
                  </label>
                  {#if s.hint}<p class="sg-muted field-hint">{s.hint}.</p>{/if}
                {:else}
                  <label class="field">
                    <span>{s.label}</span>
                    <input
                      class={s.kind === 'colour' ? 'sg-input color-input' : 'sg-input'}
                      type={s.kind === 'colour' ? 'color' : 'text'}
                      maxlength={s.max}
                      placeholder={s.hint}
                      value={String(settingValue(s.key, s.default))}
                      oninput={(e) => setSetting(s.key, e.currentTarget.value)}
                    />
                  </label>
                {/if}
              {/each}
              <p class="sg-muted field-hint">
                The look runs in a sandbox: it can’t reach the internet, your accounts or StreamGremlin, and it’s only told the
                result StreamGremlin drew. In OBS, leave this browser source’s “Page permissions” at “No access to OBS”: a look never needs it,
                and OBS would let the look use whatever it allows. An add-on look only loads at “No access to OBS”; otherwise Classic
                shows instead.
              </p>
            {:else}
              <p class="sg-muted field-hint">Classic uses the colours and font below. If another look can’t show a spin, Classic shows it.</p>
            {/if}
          </fieldset>
        {/if}
        {#if isGiveaway(current) || isWheel(current)}
          <fieldset class="look-group">
            <legend>Sounds</legend>
            <p class="sg-muted field-hint">
              Played by the overlay, so OBS hears them (tick “Control audio via OBS” on its browser source). The last plays half a second
              after it stops, as the result shows: good for a cheer. Add sounds on the Sounds screen.
              {#if themeSounds}{chosenTheme?.name}’s own sounds play where you leave “{chosenTheme?.name}’s sound”.{/if}
            </p>
            {#each [
              ['start', isWheel(current) ? 'As a spin starts' : 'As a roll starts'],
              ['end', isWheel(current) ? 'As it stops' : 'As it lands on the winner'],
              ['cheer', isWheel(current) ? 'When it has stopped' : 'When the winner shows'],
            ] as [which, label] (which)}
              {@const nameKey = `${which}_sound` as 'start_sound' | 'end_sound' | 'cheer_sound'}
              {@const volKey = `${which}_volume` as 'start_volume' | 'end_volume' | 'cheer_volume'}
              <label class="field">
                <span>{label}</span>
                <select class="sg-input" bind:value={look[nameKey]}>
                  <option value="">{themeSounds ? `${chosenTheme?.name}’s sound` : 'No sound'}</option>
                  {#each soundNames as s (s)}<option value={s}>{s}</option>{/each}
                </select>
              </label>
              {#if look[nameKey] || themeSounds}
                <label class="field">
                  <span>Volume {look[volKey] ?? 80}</span>
                  <input type="range" min="0" max="100" step="5" bind:value={look[volKey]} />
                </label>
              {/if}
            {/each}
          </fieldset>
        {/if}
        {#if isSong(current)}
          <fieldset class="look-group">
            <legend>Pretzel's files</legend>
            <p class="sg-muted field-hint">
              In the Pretzel app, open Settings → Advanced → Write to File, and turn on the song's text file and its album art. Put the
              title on the first line and the artist on the second, if you like. Then choose each file with Browse, or paste its full path.
              In OBS, make the browser source 1920 × 1080 and place the song with “Where alerts appear” below, or make it about 1100 × 200
              and drag it where you like.
            </p>
            <div class="field">
              <label for="song-file">Song's text file</label>
              <div class="path-row">
                <input id="song-file" class="sg-input" bind:value={look.song_file} placeholder="/Users/you/Pretzel/song.txt" spellcheck="false" autocomplete="off" />
                <button type="button" class="button" disabled={busy} onclick={() => browse('song_text')}>Browse…</button>
              </div>
            </div>
            <div class="field">
              <label for="art-file">Album art file</label>
              <div class="path-row">
                <input id="art-file" class="sg-input" bind:value={look.art_file} placeholder="/Users/you/Pretzel/album.jpg" spellcheck="false" autocomplete="off" />
                <button type="button" class="button" disabled={busy} onclick={() => browse('song_art')}>Browse…</button>
              </div>
              <span class="sg-muted field-hint">Optional. Without it, bars bounce beside the song.</span>
            </div>
            <label class="field">
              <span>Paused text</span>
              <input class="sg-input" bind:value={look.paused_text} maxlength="100" placeholder="Paused" spellcheck="false" autocomplete="off" />
              <span class="sg-muted field-hint">What Pretzel writes when paused, if you set one there. The overlay hides then.</span>
            </label>
            <div aria-live="polite">
              {#if !current.song?.open}
                <p class="sg-muted">The files are read only while this overlay is open, in OBS or the preview here.</p>
              {:else if current.song.problem}
                <p class="state state-unknown" role="alert"><span aria-hidden="true">!</span> {current.song.problem}</p>
              {:else if current.song.song.playing}
                <p class="state">
                  <span class="state-ok"><span aria-hidden="true">●</span></span> Showing <strong>{current.song.song.title}</strong>{#if current.song.song.artist}{' by '}<strong
                      >{current.song.song.artist}</strong
                    >{/if}{current.song.song.picture ? ', with its album art' : ''}.
                </p>
              {:else}
                <p class="state"><span class="state-unknown"><span aria-hidden="true">○</span></span> Nothing playing.</p>
              {/if}
            </div>
          </fieldset>
        {/if}
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
            <span>Animation speed: {paceWords(look.pace)}</span>
            <input type="range" min="50" max="300" step="10" bind:value={look.pace} aria-valuetext={paceWords(look.pace)} />
            <span class="sg-muted field-hint">How long coming in and leaving take. Right is slower.</span>
          </label>
          {#if isSong(current)}
            <label class="choice-inline">
              <input type="checkbox" bind:checked={look.song_hides} /> Show each song for a while as it starts, then hide
            </label>
            {#if look.song_hides}
              <label class="field">
                <span>Seconds on screen</span>
                <input class="sg-input" type="number" min="2" max="60" bind:value={look.seconds} />
              </label>
            {:else}
              <p class="sg-muted field-hint">It stays up while a song plays, and goes when nothing does or Pretzel can't say.</p>
            {/if}
          {:else if !isGiveaway(current) && !isWheel(current)}
            <label class="field">
              <span>Seconds on screen</span>
              <input class="sg-input" type="number" min="2" max="60" bind:value={look.seconds} />
              <span class="sg-muted field-hint">A step can choose its own.</span>
            </label>
          {/if}
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
          <button type="button" class="button" disabled={busy || !previewLoaded} onclick={previewLook}
            >{isGiveaway(current) ? 'Preview a draw' : isWheel(current) ? 'Preview a spin' : isSong(current) ? 'Preview a song' : 'Preview an alert'}</button
          >
        </div>
        {#if changed && (isGiveaway(current) || isWheel(current))}
          <p class="sg-muted">Save to see your changes in the preview and in OBS.</p>
        {:else if changed}
          <p class="sg-muted">{isSong(current) ? 'Preview a song' : 'Preview an alert'} shows your changes here. Save to use them in OBS.</p>
        {/if}
      </form>
      <div class="overlay-preview-wrap">
        <OverlayPreview name={current.name} reload={previewReload} bind:loaded={previewLoaded} />
      </div>
    </div>
  </section>
{/if}

<section class="sg-panel card" aria-labelledby="overlay-add-heading">
  <h2 id="overlay-add-heading">Add an overlay</h2>
  <form class="test-form" onsubmit={add}>
    <fieldset class="field">
      <legend>Kind</legend>
      <label class="choice-inline"><input type="radio" name="overlay-kind" value="alert" bind:group={newKind} /> Alerts, shown by workflows</label>
      <label class="choice-inline">
        <input type="radio" name="overlay-kind" value="giveaway" bind:group={newKind} /> Giveaways: the running giveaway, and a roll to each winner
      </label>
      <label class="choice-inline">
        <input type="radio" name="overlay-kind" value="wheel" bind:group={newKind} /> Wheels: every spin, as it's drawn
      </label>
      <label class="choice-inline">
        <input type="radio" name="overlay-kind" value="song" bind:group={newKind} /> Now playing: the song Pretzel's desktop app is playing
      </label>
      {#if newKind === 'song'}
        <p class="sg-muted field-hint">
          It shows the song and album art Pretzel's desktop app writes to files (Write to File, in Pretzel's Advanced settings). Set the
          files' paths in the overlay's settings after adding it.
        </p>
      {/if}
      {#if newKind === 'wheel'}
        <p class="sg-muted field-hint">
          While it's open in OBS, a spin stops when the wheel does, so chat and workflows hear the result as viewers see it.
        </p>
      {/if}
      {#if newKind === 'giveaway'}
        <p class="sg-muted field-hint">
          While it's open in OBS, chat names each winner as their roll lands, about 4 seconds after the draw. A winner who must claim has that
          much less time after chat names them. Practice giveaways show only in the app's preview.
        </p>
      {/if}
    </fieldset>
    <label class="field">
      <span>Name</span>
      <input class="sg-input" bind:value={newName} maxlength="64" spellcheck="false" placeholder="alerts" />
      <span class="sg-muted field-hint">
        {newKind !== 'alert'
          ? `Any name, like ${newKind === 'song' ? 'now-playing' : newKind}. One is usually enough.`
          : 'Workflows show alerts on it by this name. One overlay usually does; add more for different places on screen.'}
      </span>
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
