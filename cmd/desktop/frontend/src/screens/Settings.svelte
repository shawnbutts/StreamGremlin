<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, getUISettings, putUISettings, type Theme } from '../lib/api';
  import { applyTheme } from '../lib/theme';
  import BackupsCard from './BackupsCard.svelte';
  import SavedSecrets from './SavedSecrets.svelte';

  const options: { value: Theme; label: string; hint: string }[] = [
    { value: 'system', label: 'System', hint: 'Dark or light, following your computer' },
    { value: 'dark', label: 'Dark workshop', hint: 'Near-black canvas with navy panels' },
    { value: 'light', label: 'Light workshop', hint: 'White cards with blue-gray borders' },
    { value: 'midnight', label: 'Midnight blue', hint: 'Cool blue-black surfaces' },
  ];

  let theme = $state<Theme | undefined>();
  let saved = $state('');
  let error = $state('');

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  onMount(async () => {
    try {
      theme = (await getUISettings()).theme;
    } catch (e) {
      error = `Couldn't load your settings: ${message(e)}`;
    }
  });

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
  <p class="saved" role="status">{saved}</p>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</section>

<SavedSecrets />

<BackupsCard />
