<script lang="ts">
  import { onMount } from 'svelte';
  import iconUrl from '../../../../brand-kit/streamgremlin-icon-64.png';
  import { engineInfo, getUISettings, putUISettings, type EngineInfo, type TextSize } from './lib/api';
  import { appearance, applyAppearance, stepTextSize } from './lib/appearance.svelte';
  import { startPlayer } from './lib/player';
  import { applyTheme } from './lib/theme';
  import Connections from './screens/Connections.svelte';
  import Events from './screens/Events.svelte';
  import Help from './screens/Help.svelte';
  import Home from './screens/Home.svelte';
  import Quotes from './screens/Quotes.svelte';
  import Settings from './screens/Settings.svelte';
  import Sounds from './screens/Sounds.svelte';
  import Overlays from './screens/Overlays.svelte';
  import Rewards from './screens/Rewards.svelte';
  import Polls from './screens/Polls.svelte';
  import Giveaways from './screens/Giveaways.svelte';
  import Timers from './screens/Timers.svelte';
  import Variables from './screens/Variables.svelte';
  import Workflows from './screens/Workflows.svelte';

  let { pollMs = 300 }: { pollMs?: number } = $props();

  type Screen = 'home' | 'connections' | 'events' | 'workflows' | 'timers' | 'quotes' | 'sounds' | 'overlays' | 'giveaways' | 'rewards' | 'polls' | 'variables' | 'help' | 'settings';
  // The sidebar (docs/adr/0019-app-comfort.md): the main screens in this
  // order, then "More…" with the ones the streamer moved there, then
  // Settings and Help, always last. Home, Events, Workflows, Settings and
  // Help never move. Ctrl/⌘+1–9 go to the main screens in order; Settings
  // is Ctrl/⌘+comma and Help Ctrl/⌘+/, as in most apps.
  const screens: { id: Screen; label: string }[] = [
    { id: 'home', label: 'Home' },
    { id: 'connections', label: 'Connections' },
    { id: 'events', label: 'Events' },
    { id: 'workflows', label: 'Workflows' },
    { id: 'timers', label: 'Timers' },
    { id: 'quotes', label: 'Quotes' },
    { id: 'sounds', label: 'Sounds' },
    { id: 'overlays', label: 'Overlays' },
    { id: 'giveaways', label: 'Giveaways' },
    { id: 'rewards', label: 'Rewards' },
    { id: 'polls', label: 'Polls' },
    { id: 'variables', label: 'Variables' },
  ];
  const pinned: { id: Screen; label: string; key: string }[] = [
    { id: 'settings', label: 'Settings', key: ',' },
    { id: 'help', label: 'Help', key: '/' },
  ];

  const mainScreens = $derived(
    screens.filter((s) => !appearance.more.includes(s.id)).map((s, i) => ({ ...s, key: i < 9 ? String(i + 1) : undefined })),
  );
  const moreScreens = $derived(screens.filter((s) => appearance.more.includes(s.id)));
  let moreOpen = $state(false);
  const moreShown = $derived(moreOpen || moreScreens.some((s) => s.id === screen));

  let screen = $state<Screen>('home');
  let info = $state<EngineInfo>({ name: 'StreamGremlin', state: 'starting' });

  onMount(() => {
    let stopped = false;
    let stopPlayer: (() => void) | undefined;
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
        // This window plays the workflows' sounds while it's open.
        stopPlayer = startPlayer();
        try {
          const ui = await getUISettings();
          applyTheme(ui.theme);
          applyAppearance(ui);
        } catch {
          // Keep following the system theme; the Settings screen shows the error.
        }
      }
    })();
    return () => {
      stopped = true;
      stopPlayer?.();
    };
  });

  const go = (id: string) => (screen = [...screens, ...pinned].find((s) => s.id === id)?.id ?? screen);

  async function resize(size: TextSize) {
    if (size === appearance.textSize) return;
    const before = appearance.textSize;
    applyAppearance({ text_size: size });
    try {
      await putUISettings({ text_size: size });
    } catch {
      applyAppearance({ text_size: before }); // not saved: don't pretend
    }
  }

  function onKey(e: KeyboardEvent) {
    if (!(e.metaKey || e.ctrlKey) || e.altKey) return;
    // Text size, as in a browser: Ctrl/⌘ with + (or =), - and 0.
    if (e.key === '=' || e.key === '+' || e.key === '-' || e.key === '0') {
      e.preventDefault();
      resize(e.key === '0' ? 'standard' : stepTextSize(e.key === '-' ? -1 : 1));
      return;
    }
    if (e.shiftKey) return;
    // Ctrl/⌘+F: the screen's search, when it has one.
    if (e.key === 'f') {
      const search = document.querySelector<HTMLInputElement>('main input[type="search"]');
      if (search) {
        e.preventDefault();
        search.focus();
        search.select();
      }
      return;
    }
    const target = [...mainScreens, ...pinned].find((s) => s.key && s.key === e.key);
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
      {#each mainScreens as s (s.id)}
        <li>
          <button
            type="button"
            class="nav-item"
            aria-current={screen === s.id ? 'page' : undefined}
            aria-keyshortcuts={s.key ? `Control+${s.key} Meta+${s.key}` : undefined}
            onclick={() => (screen = s.id)}>{s.label}</button
          >
        </li>
      {/each}
      {#if moreScreens.length}
        <li>
          <button
            type="button"
            class="nav-item nav-more"
            aria-expanded={moreShown}
            aria-controls="nav-more-list"
            onclick={() => (moreOpen = !moreShown)}
            ><span aria-hidden="true">{moreShown ? '▾' : '▸'}</span> More…</button
          >
          {#if moreShown}
            <ul id="nav-more-list" class="nav-more-list" aria-label="More screens">
              {#each moreScreens as s (s.id)}
                <li>
                  <button type="button" class="nav-item" aria-current={screen === s.id ? 'page' : undefined} onclick={() => (screen = s.id)}
                    >{s.label}</button
                  >
                </li>
              {/each}
            </ul>
          {/if}
        </li>
      {/if}
    </ul>
    <ul class="nav-pinned">
      {#each pinned as s (s.id)}
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
      <Home ongo={go} />
    {:else if screen === 'connections'}
      <Connections />
    {:else if screen === 'events'}
      <Events />
    {:else if screen === 'workflows'}
      <Workflows />
    {:else if screen === 'timers'}
      <Timers />
    {:else if screen === 'quotes'}
      <Quotes />
    {:else if screen === 'sounds'}
      <Sounds />
    {:else if screen === 'overlays'}
      <Overlays />
    {:else if screen === 'giveaways'}
      <Giveaways />
    {:else if screen === 'rewards'}
      <Rewards />
    {:else if screen === 'polls'}
      <Polls />
    {:else if screen === 'variables'}
      <Variables />
    {:else if screen === 'help'}
      <Help
        shortcuts={[...mainScreens.filter((s) => s.key), pinned[1]].map((s) => ({ label: s.label, key: s.key! }))}
        ongo={go}
      />
    {:else}
      <Settings />
    {/if}
  </main>
</div>
