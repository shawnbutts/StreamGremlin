import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Settings from './Settings.svelte';
import { mockFetch, noSecrets, settle } from '../test/helpers';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  comp = mount(Settings, { target });
  return target;
}

const radio = (el: HTMLElement, value: string) =>
  el.querySelector<HTMLInputElement>(`input[value="${value}"]`)!;

describe('Settings', () => {
  it('saves the chosen theme through the API and applies it', async () => {
    let saved = 'system';
    const calls = mockFetch({
      '/v1/secrets': noSecrets,
      '/v1/settings/ui': (method, body) => {
        if (method === 'PUT') saved = (body as { theme: string }).theme;
        return { status: 200, body: { theme: saved } };
      },
    });
    const el = start();
    await settle();
    expect(radio(el, 'system').checked).toBe(true);
    radio(el, 'midnight').click();
    flushSync();
    await settle();
    expect(calls.some((c) => c.method === 'PUT' && (c.body as { theme: string }).theme === 'midnight')).toBe(true);
    expect(document.documentElement.dataset.theme).toBe('midnight');
    expect(el.querySelector('[role="status"]')?.textContent).toBe('Saved.');
  });

  it('puts the old theme back and says so when saving fails', async () => {
    mockFetch({
      '/v1/secrets': noSecrets,
      '/v1/settings/ui': (method) =>
        method === 'PUT'
          ? { status: 503, body: { error: { code: 'engine_failed', message: 'Engine stopped' } } }
          : { status: 200, body: { theme: 'light' } },
    });
    const el = start();
    await settle();
    radio(el, 'dark').click();
    flushSync();
    await settle();
    expect(radio(el, 'light').checked).toBe(true);
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(el.querySelector('[role="alert"]')?.textContent).toContain("Couldn't save the theme");
  });

  it('saves text size, spacing and which screens go under More…, and puts them back if saving fails', async () => {
    let ui = { theme: 'dark', text_size: 'standard', density: 'comfortable', more: ['timers', 'quotes', 'rewards'] };
    let refuse = false;
    const calls = mockFetch({
      '/v1/secrets': noSecrets,
      '/v1/settings/ui': (method, body) => {
        if (method === 'PUT') {
          if (refuse) return { status: 503, body: { error: { code: 'engine_failed', message: 'Engine stopped' } } };
          ui = { ...ui, ...(body as object) };
        }
        return { status: 200, body: ui };
      },
    });
    const el = start();
    await settle();
    radio(el, 'large').click();
    await settle();
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', body: { text_size: 'large' } });
    expect(document.documentElement.dataset.textSize).toBe('large');
    radio(el, 'compact').click();
    await settle();
    expect(document.documentElement.dataset.density).toBe('compact');
    const underMore = (name: string) =>
      Array.from(el.querySelectorAll('ul[aria-label="Screens"] li'))
        .find((li) => li.querySelector('strong')?.textContent === name)!
        .querySelector<HTMLInputElement>('input')!;
    expect(underMore('Quotes').checked).toBe(true);
    expect(underMore('Sounds').checked).toBe(false);
    expect(el.textContent).toContain('Home, Events, Workflows, Settings and Help always stay');
    underMore('Quotes').click();
    await settle();
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', body: { more: ['timers', 'rewards'] } });
    underMore('Connections').click();
    await settle();
    expect(calls.at(-1)).toMatchObject({ body: { more: ['connections', 'timers', 'rewards'] } }); // sidebar order
    refuse = true;
    underMore('Sounds').click();
    await settle();
    expect(underMore('Sounds').checked).toBe(false);
    expect(el.textContent).toContain("Couldn't save it");
  });
});
