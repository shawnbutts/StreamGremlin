<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, getUISettings, putUISettings, type Density, type TextSize, type Theme, type UISettings } from '../lib/api';
  import { MOVABLE, appearance, applyAppearance } from '../lib/appearance.svelte';
  import { applyTheme } from '../lib/theme';
  import BackupsCard from './BackupsCard.svelte';
  import SavedSecrets from './SavedSecrets.svelte';

  const options: { value: Theme; label: string; hint: string }[] = [
    { value: 'system', label: 'System', hint: 'Dark or light, following your computer' },
    { value: 'dark', label: 'Dark workshop', hint: 'Near-black canvas with navy panels' },
    { value: 'light', label: 'Light workshop', hint: 'White cards with blue-gray borders' },
    { value: 'midnight', label: 'Midnight blue', hint: 'Cool blue-black surfaces' },
  ];

  const sizes: { value: TextSize; label: string }[] = [
    { value: 'small', label: 'Small' },
    { value: 'standard', label: 'Standard' },
    { value: 'large', label: 'Large' },
    { value: 'larger', label: 'Larger' },
  ];
  const densities: { value: Density; label: string; hint: string }[] = [
    { value: 'comfortable', label: 'Comfortable', hint: 'Room around everything' },
    { value: 'compact', label: 'Compact', hint: 'More on screen at once' },
  ];
  const LABELS: Record<string, string> = {
    connections: 'Connections', timers: 'Timers', quotes: 'Quotes', sounds: 'Sounds', overlays: 'Overlays', rewards: 'Rewards', polls: 'Polls and predictions',
    variables: 'Variables',
  };

  let theme = $state<Theme | undefined>();
  let loaded = $state(false);
  let saved = $state('');
  let error = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  onMount(async () => {
    try {
      const ui = await getUISettings();
      theme = ui.theme;
      applyAppearance(ui);
      loaded = true;
    } catch (e) {
      error = `Couldn't load your settings: ${message(e)}`;
    }
  });

  /** Save some settings; on failure, put the page back as it was. */
  async function save(change: Partial<UISettings>, undo: () => void) {
    saved = '';
    error = '';
    try {
      applyAppearance(await putUISettings(change));
      saved = 'Saved.';
    } catch (e) {
      undo();
      error = `Couldn't save it: ${message(e)}`;
    }
  }

  function setSize(size: TextSize) {
    const before = appearance.textSize;
    applyAppearance({ text_size: size });
    save({ text_size: size }, () => applyAppearance({ text_size: before }));
  }

  function setDensity(d: Density) {
    const before = appearance.density;
    applyAppearance({ density: d });
    save({ density: d }, () => applyAppearance({ density: before }));
  }

  function setMore(id: string, underMore: boolean) {
    const before = [...appearance.more];
    const more = underMore ? [...before, id] : before.filter((x) => x !== id);
    const ordered = MOVABLE.filter((x) => more.includes(x));
    applyAppearance({ more: ordered });
    save({ more: ordered }, () => applyAppearance({ more: before }));
  }

  async function choose(next: Theme) {
    const previous = theme;
    theme = next;
    applyTheme(next);
    saved = '';
    error = '';
    try {
      await putUISettings({ theme: next });
      saved = 'Saved.';
    } catch (e) {
      theme = previous;
      if (previous) applyTheme(previous);
      error = `Couldn't save the theme: ${message(e)}`;
    }
  }
</script>

<h1>Settings</h1>
<p class="sg-muted screen-intro">How StreamGremlin looks, the passwords and sign-ins it keeps, backups, and how long it keeps history.</p>

<section class="sg-panel card" aria-labelledby="appearance-heading">
  <h2 id="appearance-heading">Appearance</h2>
  <fieldset>
    <legend>Theme</legend>
    {#each options as o (o.value)}
      <label class="choice">
        <input
          type="radio"
          name="theme"
          value={o.value}
          checked={theme === o.value}
          disabled={theme === undefined}
          onchange={() => choose(o.value)}
        />
        <span>
          <span class="choice-label">{o.label}</span>
          <span class="sg-muted choice-hint">{o.hint}</span>
        </span>
      </label>
    {/each}
  </fieldset>
  <fieldset>
    <legend>Text size</legend>
    <div class="choice-row">
      {#each sizes as o (o.value)}
        <label class="choice-inline">
          <input type="radio" name="text-size" value={o.value} checked={appearance.textSize === o.value} disabled={!loaded}
            onchange={() => setSize(o.value)} />
          {o.label}
        </label>
      {/each}
    </div>
    <span class="sg-muted field-hint">Also Ctrl/⌘ with + and −, and Ctrl/⌘+0 for Standard.</span>
  </fieldset>
  <fieldset>
    <legend>Spacing</legend>
    {#each densities as o (o.value)}
      <label class="choice">
        <input type="radio" name="density" value={o.value} checked={appearance.density === o.value} disabled={!loaded}
          onchange={() => setDensity(o.value)} />
        <span>
          <span class="choice-label">{o.label}</span>
          <span class="sg-muted choice-hint">{o.hint}</span>
        </span>
      </label>
    {/each}
  </fieldset>
  <p class="saved" role="status">{saved}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>

<section class="sg-panel card" aria-labelledby="screens-heading">
  <h2 id="screens-heading">Screens</h2>
  <p class="sg-muted">
    Screens you don’t use often can go under “More…” in the sidebar, out of the way. They keep working: timers still run and
    quote commands still answer. Home, Events, Workflows, Settings and Help always stay.
  </p>
  <ul class="secret-list" aria-label="Screens">
    {#each MOVABLE as id (id)}
      <li class="secret-row">
        <span class="secret-info"><strong>{LABELS[id]}</strong></span>
        <label class="choice-inline">
          <input type="checkbox" checked={appearance.more.includes(id)} disabled={!loaded}
            onchange={(e) => setMore(id, e.currentTarget.checked)} />
          Under More…
        </label>
      </li>
    {/each}
  </ul>
</section>

<SavedSecrets />

<BackupsCard />
