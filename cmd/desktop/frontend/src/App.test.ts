import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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
      '/v1/packages': () => ({ status: 200, body: { packages: [] } }),
      '/v1/packages/audit': () => ({ status: 200, body: { audit: [] } }),
      '/v1/points': () => ({ status: 200, body: { currency: { id: 'points', name: 'points', allow_negative: false }, summary: { transactions: 0, viewers: 0, issued: 0 }, top: [] } }),
      '/v1/viewers': () => ({ status: 200, body: { viewers: [] } }),
      '/v1/timers': () => ({ status: 200, body: { timers: [] } }),
      '/v1/buttons': () => ({ status: 200, body: { buttons: [] } }),
      '/v1/quotes': () => ({ status: 200, body: { quotes: [], count: 0, next: 1 } }),
      '/v1/sounds': () => ({ status: 200, body: { sounds: [], player: true } }),
      '/v1/overlays': () => ({
        status: 200,
        body: { overlays: [], server: 'http://127.0.0.1:7431', positions: ['top'], animations: ['slide', 'pop'], default_look: {} },
      }),
      '/v1/settings/ui': () => ({ status: 200, body: { theme: 'light' } }),
    });
    const el = start();
    await settle();
    // Live is second: it's the screen for while you're live (D-075).
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '2', ctrlKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Live');
    // Live fills the window's height too (D-082): chat takes what's left.
    expect(el.querySelector('main')!.classList.contains('main-fit')).toBe(true);
    await noViolations('live');
    // Timers, Quotes and Rewards start under More…, so the numbers go on.
    const keyTo = async (key: string, title: string) => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key, ctrlKey: true }));
      flushSync();
      await settle();
      expect(el.querySelector('h1')?.textContent).toBe(title);
    };
    // A screen opens at its top, whatever the last one was scrolled to.
    // (jsdom doesn't scroll, so the test watches what's set.)
    let top = 400;
    Object.defineProperty(el.querySelector('main')!, 'scrollTop', { get: () => top, set: (v: number) => (top = v), configurable: true });
    await keyTo('3', 'Connections');
    expect(top).toBe(0);
    expect(el.querySelector('main')!.classList.contains('main-fit')).toBe(false);
    await noViolations('connections');
    await keyTo('4', 'Events');
    await noViolations('events');
    await keyTo('5', 'Workflows');
    await noViolations('workflows');
    await keyTo('6', 'Sounds');
    await noViolations('sounds');
    await keyTo('7', 'Overlays');
    await noViolations('overlays');
    await keyTo('8', 'Giveaways');
    await noViolations('giveaways');
    await keyTo('9', 'Points');
    await noViolations('points');
    // Viewers and Variables are past nine, so they're reached from the sidebar.
    Array.from(el.querySelectorAll<HTMLButtonElement>('nav button')).find((b) => b.textContent === 'Viewers')!.click();
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Viewers');
    await noViolations('viewers');
    Array.from(el.querySelectorAll<HTMLButtonElement>('nav button')).find((b) => b.textContent === 'Variables')!.click();
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Variables');
    await noViolations('variables');
    Array.from(el.querySelectorAll<HTMLButtonElement>('nav button')).find((b) => b.textContent === 'Add-ons')!.click();
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Add-ons');
    // More… opens in place; its screens work as any other.
    const more = Array.from(el.querySelectorAll<HTMLButtonElement>('nav button')).find((b) => b.textContent?.includes('More…'))!;
    expect(more.getAttribute('aria-expanded')).toBe('false');
    more.click();
    flushSync();
    expect(more.getAttribute('aria-expanded')).toBe('true');
    Array.from(el.querySelectorAll<HTMLButtonElement>('#nav-more-list button')).find((b) => b.textContent === 'Timers')!.click();
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Timers');
    await noViolations('timers');
    Array.from(el.querySelectorAll<HTMLButtonElement>('#nav-more-list button')).find((b) => b.textContent === 'Quotes')!.click();
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Quotes');
    await noViolations('quotes');
    // Settings and Help are last, Help at the very bottom.
    const labels = Array.from(el.querySelectorAll('nav button')).map((b) => b.textContent?.trim());
    expect(labels.slice(-2)).toEqual(['Settings', 'Help']);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '/', metaKey: true }));
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Help');
    expect(el.textContent).toContain('/tmp/sg'); // where your data lives
    await noViolations('help');
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

  it('opens the screen a Home step points to', async () => {
    mockFetch({
      '/desktop/info': readyEngine,
      '/v1/status': runningStatus,
      '/v1/settings/ui': () => ({ status: 200, body: { theme: 'dark' } }),
      '/v1/overview': () => ({
        status: 200,
        body: {
          checklist: [{ id: 'twitch', label: 'Sign in with your Twitch account', help: 'h', done: false, screen: 'connections' }],
          set_up: false,
          attention: [],
          workflows: { total: 0, on: 0, tested: false, answered: false, failed_24h: 0 },
        },
      }),
      '/v1/connections': () => twitchStatus(),
      '/v1/events': noEvents,
    });
    const el = start();
    await settle(40);
    Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Open Connections')!.click();
    flushSync();
    await settle();
    expect(el.querySelector('h1')?.textContent).toBe('Connections');
  });
});

describe('App appearance', () => {
  it('applies saved text size, spacing and More, and changes text size from the keyboard', async () => {
    const calls = mockFetch({
      '/desktop/info': readyEngine,
      '/v1/status': runningStatus,
      '/v1/events': noEvents,
      '/v1/settings/ui': (method, body) =>
        method === 'PUT'
          ? { status: 200, body: { theme: 'dark', text_size: 'larger', density: 'compact', more: ['rewards'], ...(body as object) } }
          : { status: 200, body: { theme: 'dark', text_size: 'large', density: 'compact', more: ['rewards'] } },
    });
    const el = start();
    await settle(60);
    const root = document.documentElement;
    expect(root.dataset.textSize).toBe('large');
    expect(root.dataset.density).toBe('compact');
    // Only Rewards is under More now: Timers and Quotes are main screens.
    const labels = Array.from(el.querySelectorAll('nav button')).map((b) => b.textContent?.trim());
    expect(labels).toContain('Timers');
    expect(labels).toContain('Quotes');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '=', ctrlKey: true }));
    await settle();
    expect(root.dataset.textSize).toBe('larger');
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ text_size: 'larger' });
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '0', metaKey: true }));
    await settle();
    expect(root.dataset.textSize).toBe('standard');
  });
});

// The layout as the window resizes (D-082): the window never scrolls as a
// whole, so it never shows scroll bars for "more window" than there is;
// the sidebar stays left, and every screen takes the rest of the width,
// scrolling inside only when its content is taller. Live fills the height,
// with the buttons pinned right and chat taking the rest.
describe('resizing', () => {
  const css = readFileSync(resolve(process.cwd(), 'src/app.css'), 'utf8');
  const live = readFileSync(resolve(process.cwd(), 'src/screens/Live.svelte'), 'utf8');
  const rule = (src: string, selector: string) => {
    const at = src.search(new RegExp(`(^|\\n)\\s*${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\{`));
    expect(at, selector).toBeGreaterThanOrEqual(0);
    return src.slice(at, src.indexOf('}', at));
  };
  it('keeps the page the window’s size, scrolling only inside', () => {
    expect(rule(css, 'html,\nbody')).toMatch(/overflow:\s*hidden/);
    expect(rule(css, '.shell')).toMatch(/height:\s*100vh/);
    expect(rule(css, '.shell')).toMatch(/grid-template-columns:\s*220px minmax\(0, 1fr\)/);
    expect(rule(css, 'main')).toMatch(/overflow:\s*auto/);
    expect(rule(css, 'main')).toMatch(/min-height:\s*0/);
  });
  it('lets every screen use the whole width', () => {
    expect(rule(css, 'main')).not.toMatch(/max-width/);
  });
  it('fits Live to the window: no sizes from the viewport, chat filling what’s left', () => {
    expect(live).not.toMatch(/100vh/);
    expect(rule(css, 'main.main-fit')).toMatch(/flex-direction:\s*column/);
    expect(rule(live, '.live')).toMatch(/flex:\s*1/);
    expect(rule(live, '.live')).toMatch(/grid-template-columns:\s*minmax\(0, 1fr\) var\(--live-side\)/);
    expect(live).toMatch(/--live-side:\s*16rem/);
  });
});
