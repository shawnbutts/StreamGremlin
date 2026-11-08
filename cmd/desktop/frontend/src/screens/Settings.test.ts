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
});
