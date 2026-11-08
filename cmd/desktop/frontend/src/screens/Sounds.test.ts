import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Sounds from './Sounds.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Sound } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Sounds, { target, props: { refreshMs: 30 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const now = new Date().toISOString();
const horn: Sound = { name: 'airhorn', format: 'mp3', size: 48_000, sha256: 'x', volume: 80, created_at: now, updated_at: now };

function engine(initial: Sound[], opts: { player?: boolean; refusePlay?: string } = {}) {
  const sounds = [...initial];
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/sounds': () => ({ status: 200, body: { sounds, player: opts.player ?? true } }),
    '/v1/player/stop': () => ({ status: 204, body: null }),
  };
  for (const n of ['airhorn', 'ding', 'big-boom']) {
    routes[`/v1/sounds/${n}`] = (method, body) => {
      const i = sounds.findIndex((s) => s.name === n);
      if (method === 'DELETE') {
        sounds.splice(i, 1);
        return { status: 204, body: null };
      }
      sounds[i] = { ...sounds[i], volume: (body as { volume: number }).volume };
      return { status: 200, body: sounds[i] };
    };
    routes[`/v1/sounds/${n}/play`] = () =>
      opts.refusePlay
        ? { status: 503, body: { error: { code: 'unavailable', message: opts.refusePlay } } }
        : { status: 200, body: { played: true } };
  }
  const calls = mockFetch(routes);
  // The upload sends the file's bytes, not JSON: answered here.
  const inner = (globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> }).getMockImplementation();
  vi.stubGlobal('fetch', vi.fn(async (p: string, i?: RequestInit) => {
    if (i?.method === 'POST' && p.startsWith('/v1/sounds/') && p.includes('?volume=')) {
      const name = p.split('/')[3].split('?')[0];
      calls.push({ method: 'POST', path: p, body: i.body });
      const s: Sound = { name, format: 'wav', size: (i.body as Blob).size, sha256: 'y', volume: Number(p.split('volume=')[1]), created_at: now, updated_at: now };
      sounds.push(s);
      return new Response(JSON.stringify(s), { status: 201 });
    }
    return inner(p, i);
  }));
  return { calls, sounds };
}

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Sounds', () => {
  it('lists sounds, says this window plays them, and plays one, accessibly', async () => {
    const { calls } = engine([horn]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('This window plays sounds for your workflows.');
    expect(el.textContent).toContain('MP3, 48 KB');
    await noViolations('list');
    button(el, 'Play')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/sounds/airhorn/play')).toBe(true);
    expect(el.textContent).toContain('Playing airhorn.');
    button(el, 'Stop all sounds')!.click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/player/stop')).toBe(true);
  });

  it('says when no window plays sounds, and why a play failed', async () => {
    engine([horn], { player: false, refusePlay: 'no StreamGremlin window is open to play sounds; open the app' });
    const el = start();
    await settle();
    expect(el.textContent).toContain('No StreamGremlin window is playing sounds yet.');
    button(el, 'Play')!.click();
    await settle();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('no StreamGremlin window is open');
  });

  it('adds a sound from a file, naming it from the file', async () => {
    const { calls } = engine([]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('No sounds yet.');
    const input = el.querySelector<HTMLInputElement>('input[type="file"]')!;
    const file = new File([new Uint8Array(100)], 'Big Boom!.wav', { type: 'audio/wav' });
    Object.defineProperty(input, 'files', { value: [file] });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(el.querySelector<HTMLInputElement>('input[placeholder="airhorn"]')!.value).toBe('big-boom');
    button(el, 'Add')!.click();
    await settle();
    const post = calls.find((c) => c.method === 'POST')!;
    expect(post.path).toBe('/v1/sounds/big-boom?volume=80');
    expect(post.body).toBe(file);
    expect(el.textContent).toContain('Added big-boom.');
  });

  it('refuses a file over 5 MB before sending it', async () => {
    const { calls } = engine([]);
    const el = start();
    await settle();
    const input = el.querySelector<HTMLInputElement>('input[type="file"]')!;
    const file = new File([new Uint8Array(5 * 1024 * 1024 + 1)], 'huge.mp3');
    Object.defineProperty(input, 'files', { value: [file] });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    button(el, 'Add')!.click();
    await settle();
    expect(el.textContent).toContain('A sound is up to 5 MB.');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it('saves a volume when the slider is let go, and deletes after asking', async () => {
    const { calls } = engine([horn]);
    const el = start();
    await settle();
    const slider = el.querySelector<HTMLInputElement>('input[aria-label="Volume of airhorn"]')!;
    slider.value = '35';
    slider.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    expect(el.textContent).toContain('Volume 35');
    slider.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ path: '/v1/sounds/airhorn', body: { volume: 35 } });
    button(el, 'Delete')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Workflows that play it fail');
    button(dialog, 'Delete')!.click();
    await settle();
    expect(el.textContent).toContain('Deleted airhorn.');
  });
});
