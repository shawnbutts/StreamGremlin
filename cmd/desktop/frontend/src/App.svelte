<script lang="ts">
  import { onMount } from 'svelte';
  import iconUrl from '../../../../brand-kit/streamgremlin-icon-64.png';
  import { engineInfo, getUISettings, type EngineInfo } from './lib/api';
  import { applyTheme } from './lib/theme';
  import Connections from './screens/Connections.svelte';
  import Events from './screens/Events.svelte';
  import Help from './screens/Help.svelte';
  import Home from './screens/Home.svelte';
  import Settings from './screens/Settings.svelte';
  import Timers from './screens/Timers.svelte';
  import Variables from './screens/Variables.svelte';
  import Workflows from './screens/Workflows.svelte';

  let { pollMs = 300 }: { pollMs?: number } = $props();

  type Screen = 'home' | 'connections' | 'events' | 'workflows' | 'timers' | 'variables' | 'help' | 'settings';
  // Screens take Ctrl/⌘+number in order; Settings is always last and also
  // Ctrl/⌘+comma, as in most apps, so adding a screen never moves it.
  const screens: { id: Screen; label: string; key: string }[] = [
    { id: 'home', label: 'Home', key: '1' },
    { id: 'connections', label: 'Connections', key: '2' },
    { id: 'events', label: 'Events', key: '3' },
    { id: 'workflows', label: 'Workflows', key: '4' },
    { id: 'timers', label: 'Timers', key: '5' },
    { id: 'variables', label: 'Variables', key: '6' },
    { id: 'help', label: 'Help', key: '7' },
    { id: 'settings', label: 'Settings', key: '8' },
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

  const go = (id: string) => (screen = screens.find((s) => s.id === id)?.id ?? screen);

  function onKey(e: KeyboardEvent) {
    if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return;
    const target = e.key === ',' ? screens.find((s) => s.id === 'settings') : screens.find((s) => s.key === e.key);
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
            aria-keyshortcuts={`Control+${s.key} Meta+${s.key}` + (s.id === 'settings' ? ' Control+, Meta+,' : '')}
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
      <Home ongo={go} />
    {:else if screen === 'connections'}
      <Connections />
    {:else if screen === 'events'}
      <Events />
    {:else if screen === 'workflows'}
      <Workflows />
    {:else if screen === 'timers'}
      <Timers />
    {:else if screen === 'variables'}
      <Variables />
    {:else if screen === 'help'}
      <Help shortcuts={screens.filter((s) => s.id !== 'settings')} ongo={go} />
    {:else}
      <Settings />
    {/if}
  </main>
</div>
