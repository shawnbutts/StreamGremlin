<script lang="ts">
  import { onMount } from 'svelte';
  import iconUrl from '../../../../brand-kit/streamgremlin-icon-64.png';
  import { engineInfo, getUISettings, type EngineInfo } from './lib/api';
  import { applyTheme } from './lib/theme';
  import Events from './screens/Events.svelte';
  import Home from './screens/Home.svelte';
  import Settings from './screens/Settings.svelte';

  let { pollMs = 300 }: { pollMs?: number } = $props();

  type Screen = 'home' | 'events' | 'settings';
  const screens: { id: Screen; label: string; key: string }[] = [
    { id: 'home', label: 'Home', key: '1' },
    { id: 'events', label: 'Events', key: '2' },
    { id: 'settings', label: 'Settings', key: '3' },
  ];

  let screen = $state<Screen>('home');
  let info = $state<EngineInfo>({ name: 'StreamGremlin', state: 'starting' });

  onMount(() => {
    let stopped = false;
    applyTheme('system');
    (async () => {
      while (!stopped) {
        try {
          info = await engineInfo();
        } catch {
          info = { name: 'StreamGremlin', state: 'starting' };
        }
        if (info.state !== 'starting') break;
        await new Promise((r) => setTimeout(r, pollMs));
      }
      if (!stopped && info.state === 'ready') {
        try {
          applyTheme((await getUISettings()).theme);
        } catch {
          // Keep following the system theme; the Settings screen shows the error.
        }
      }
    })();
    return () => {
      stopped = true;
    };
  });

  function onKey(e: KeyboardEvent) {
    if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return;
    const target = screens.find((s) => s.key === e.key);
    if (target) {
      e.preventDefault();
      screen = target.id;
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<a class="skip-link" href="#main">Skip to content</a>
<div class="shell">
  <nav class="sg-panel sidebar" aria-label="Main">
    <div class="brand">
      <img src={iconUrl} alt="" width="32" height="32" />
      <span>StreamGremlin</span>
    </div>
    <ul>
      {#each screens as s (s.id)}
        <li>
          <button
            type="button"
            class="nav-item"
            aria-current={screen === s.id ? 'page' : undefined}
            aria-keyshortcuts={`Control+${s.key} Meta+${s.key}`}
            onclick={() => (screen = s.id)}>{s.label}</button
          >
        </li>
      {/each}
    </ul>
  </nav>

  <main id="main" tabindex="-1">
    {#if info.state === 'starting'}
      <section class="sg-panel card" aria-live="polite">
        <h1>Starting StreamGremlin…</h1>
        <p class="sg-muted">This usually takes a second.</p>
      </section>
    {:else if info.state === 'failed'}
      <section class="sg-panel card" role="alert">
        <h1>StreamGremlin couldn't start</h1>
        <p>{info.message}</p>
      </section>
    {:else if screen === 'home'}
      <Home />
    {:else if screen === 'events'}
      <Events />
    {:else}
      <Settings />
    {/if}
  </main>
</div>
