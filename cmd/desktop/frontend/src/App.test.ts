import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App.svelte';
import { eventCatalog, mockFetch, twitchStatus, noEvents, noSecrets, readyEngine, runningStatus, settle } from './test/helpers';

let app: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  app = mount(App, { target, props: { pollMs: 5 } });
  return target;
}

async function noViolations(label: string) {
  // Contrast is checked from the tokens in contrast.test.ts; jsdom can't render colours.
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  const summary = result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`);
  expect(summary, label).toEqual([]);
}

describe('App', () => {
  it('shows the engine as running, accessibly, in every theme', async () => {
    mockFetch({
      '/desktop/info': readyEngine,
      '/v1/status': runningStatus,
      '/v1/secrets': noSecrets,
      '/v1/settings/ui': () => ({ status: 200, body: { theme: 'system' } }),
    });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Running');
    expect(el.textContent).toContain('/tmp/sg');
    for (const theme of ['dark', 'light', 'midnight']) {
      document.documentElement.dataset.theme = theme;
      await noViolations(`home, ${theme}`);
    }
  });

  it('shows Unknown, not Running, when the engine stops answering', async () => {
    mockFetch({
      '/desktop/info': readyEngine,
      '/v1/status': () => ({ status: 503, body: { error: { code: 'engine_failed', message: 'Engine stopped' } } }),
      '/v1/secrets': noSecrets,
      '/v1/settings/ui': () => ({ status: 200, body: { theme: 'dark' } }),
    });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Unknown');
    expect(el.textContent).not.toContain('Running');
  });

  it('explains why the engine could not start', async () => {
    mockFetch({
      '/desktop/info': () => ({
        status: 200,
        body: { name: 'StreamGremlin', state: 'failed', message: 'StreamGremlin is already running.' },
      }),
    });
    const el = start();
    await settle();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('already running');
    await noViolations('failed');
  });

  it('switches screens with Ctrl/Cmd+number and marks the current one', async () => {
    mockFetch({
      '/desktop/info': readyEngine,
      '/v1/status': runningStatus,
      '/v1/secrets': noSecrets,
      '/v1/events': noEvents,
      '/v1/events/catalog': eventCatalog,
      '/v1/connections': () => twitchStatus(),
      '/v1/workflows': () => ({ status: 200, body: { workflows: [] } }),
      '/v1/jobs': () => ({ status: 200, body: { jobs: [] } }),
      '/v1/actions': () => ({ status: 200, body: { actions: [] } }),
      '/v1/variables': () => ({ status: 200, body: { variables: [] } }),
      '/v1/settings/ui': () => ({ status: 200, body: { theme: 'light' } }),
    });
    const el = start();
    await settle();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '2', ctrlKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Connections');
    await noViolations('connections');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '3', ctrlKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Events');
    await noViolations('events');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '4', ctrlKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Workflows');
    await noViolations('workflows');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '5', ctrlKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Variables');
    await noViolations('variables');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '6', metaKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Settings');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '1', ctrlKey: true }));
    flushSync();
    await settle();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: ',', metaKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Settings');
    expect(el.querySelector('[aria-current="page"]')?.textContent).toBe('Settings');
    await noViolations('settings');
  });
});
